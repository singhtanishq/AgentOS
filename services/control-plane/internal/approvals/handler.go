// Package approvals implements the human approval workflow: listing
// pending requests, deciding them with authorization checks, and resuming
// executions after approval.
package approvals

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/audit"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/auth"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/queue"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// Handler implements approval endpoints.
type Handler struct {
	Store     *store.Store
	Publisher *queue.Publisher
	Logger    *slog.Logger
}

type approvalResponse struct {
	ID            string          `json:"id"`
	Organization  string          `json:"organization_id"`
	ExecutionID   string          `json:"execution_id"`
	AgentID       string          `json:"agent_id"`
	Action        string          `json:"action"`
	Resource      json.RawMessage `json:"resource"`
	Justification string          `json:"justification"`
	Status        string          `json:"status"`
	AssignedRole  string          `json:"assigned_role"`
	ApproverID    string          `json:"approver_id,omitempty"`
	DecisionNote  string          `json:"decision_reason,omitempty"`
	ExpiresAt     string          `json:"expires_at"`
	DecidedAt     string          `json:"decided_at,omitempty"`
	CreatedAt     string          `json:"created_at"`
}

// List returns approvals with a status filter (pending by default).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}

	var items []approvalResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT id, organization_id, execution_id, agent_id, action, resource,
			       justification, status, assigned_role, COALESCE(approver_id::text, ''),
			       decision_reason, expires_at::text, COALESCE(decided_at::text, ''), created_at::text
			FROM approvals WHERE organization_id = $1 AND status = $2
			ORDER BY created_at DESC`, orgID, status)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []approvalResponse{}
		for rows.Next() {
			var a approvalResponse
			if err := rows.Scan(&a.ID, &a.Organization, &a.ExecutionID, &a.AgentID,
				&a.Action, &a.Resource, &a.Justification, &a.Status, &a.AssignedRole,
				&a.ApproverID, &a.DecisionNote, &a.ExpiresAt, &a.DecidedAt, &a.CreatedAt); err != nil {
				return err
			}
			items = append(items, a)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("approval list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list approvals")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// Decide records the human decision. Approval resumes the execution by
// re-publishing its job; rejection fails the execution.
func (h *Handler) Decide(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	approvalID := chi.URLParam(r, "approvalId")

	var req struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	decision := req.Decision
	if decision != "approved" && decision != "rejected" {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"decision must be approved or rejected")
		return
	}
	if len(req.Reason) > 2000 {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"reason must be at most 2000 characters")
		return
	}

	var a approvalResponse
	var resumeJob *queue.Job
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		// Role checks: operators and admins decide; auditors cannot (the
		// route middleware already enforces PermDecideApprovals).
		row := tx.QueryRow(r.Context(), `
			UPDATE approvals SET
				status = $3, approver_id = $4, decision_reason = $5, decided_at = now()
			WHERE id = $1 AND organization_id = $2 AND status = 'pending' AND expires_at > now()
			RETURNING id, organization_id, execution_id, agent_id, action, resource,
				justification, status, assigned_role, COALESCE(approver_id::text, ''),
				decision_reason, expires_at::text, COALESCE(decided_at::text, ''), created_at::text`,
			approvalID, orgID, decision, actorID, req.Reason)
		if err := row.Scan(&a.ID, &a.Organization, &a.ExecutionID, &a.AgentID,
			&a.Action, &a.Resource, &a.Justification, &a.Status, &a.AssignedRole,
			&a.ApproverID, &a.DecisionNote, &a.ExpiresAt, &a.DecidedAt, &a.CreatedAt); err != nil {
			return err
		}

		if decision == "approved" {
			// Re-publish the execution job for a fresh attempt; the state
			// machine allows waiting_approval → running on the resume path.
			var agentID, versionID string
			var input []byte
			if err := tx.QueryRow(r.Context(), `
				SELECT agent_id, agent_version_id, input FROM executions WHERE id = $1`,
				a.ExecutionID).Scan(&agentID, &versionID, &input); err != nil {
				return err
			}
			resumeJob = &queue.Job{
				ExecutionID:    a.ExecutionID,
				OrganizationID: orgID,
				AgentID:        agentID,
				AgentVersionID: versionID,
				Input:          input,
				Attempt:        1,
			}
		} else {
			// Rejection fails the execution deterministically.
			tag, err := tx.Exec(r.Context(), `
				UPDATE executions SET status = 'failed', completed_at = now(),
					error = $2::jsonb, updated_at = now()
				WHERE id = $1 AND status = 'waiting_approval'`,
				a.ExecutionID,
				`{"code":"approval_rejected","message":"an approver rejected the requested action","classification":"terminal"}`)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return pgx.ErrNoRows
			}
		}

		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "approval.decided", ResourceType: "approval", ResourceID: approvalID,
			Result: "success", Metadata: map[string]any{"decision": decision},
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, r, http.StatusNotFound, "not_found",
			"approval not found, already decided or expired")
		return
	case err != nil:
		h.Logger.Error("approval decide failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to record decision")
		return
	}

	if resumeJob != nil && h.Publisher != nil {
		if err := h.Publisher.PublishJob(r.Context(), *resumeJob); err != nil {
			h.Logger.Error("resume publish failed", slog.Any("error", err))
			httpx.Error(w, r, http.StatusServiceUnavailable, "queue_unavailable",
				"approval recorded but resume dispatch failed — the execution can be resumed manually")
			return
		}
	}
	httpx.JSON(w, r, http.StatusOK, a)
}
