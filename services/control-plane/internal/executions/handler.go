// Package executions implements the execution management API: submission
// to the durable queue, listing, detail and cancellation.
package executions

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/audit"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/auth"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/queue"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// Handler implements execution endpoints.
type Handler struct {
	Store     *store.Store
	Publisher *queue.Publisher
	Logger    *slog.Logger
}

type executionResponse struct {
	ID                string          `json:"id"`
	OrganizationID    string          `json:"organization_id"`
	AgentID           string          `json:"agent_id"`
	AgentVersionID    string          `json:"agent_version_id"`
	Status            string          `json:"status"`
	Input             json.RawMessage `json:"input"`
	Output            json.RawMessage `json:"output,omitempty"`
	Error             json.RawMessage `json:"error,omitempty"`
	IdempotencyKey    string          `json:"idempotency_key,omitempty"`
	StepCount         int             `json:"step_count"`
	ToolCallCount     int             `json:"tool_call_count"`
	PromptTokens      int64           `json:"prompt_tokens"`
	CompletionTokens  int64           `json:"completion_tokens"`
	TotalTokens       int64           `json:"total_tokens"`
	CostEstimateMinor int64           `json:"cost_estimate_minor"`
	StartedAt         *string         `json:"started_at,omitempty"`
	CompletedAt       *string         `json:"completed_at,omitempty"`
	CreatedAt         string          `json:"created_at"`
}

const selectExecution = `
	SELECT id, organization_id, agent_id, agent_version_id, status,
	       input, output, error,
	       COALESCE(idempotency_key, ''), step_count, tool_call_count,
	       prompt_tokens, completion_tokens, total_tokens, cost_estimate_minor,
	       started_at::text, completed_at::text, created_at::text
	FROM executions`

func scanExecutionRow(rows pgx.Rows) (executionResponse, error) {
	var e executionResponse
	var input, outputPtr, errPtr []byte
	if err := rows.Scan(&e.ID, &e.OrganizationID, &e.AgentID, &e.AgentVersionID,
		&e.Status, &input, &outputPtr, &errPtr,
		&e.IdempotencyKey, &e.StepCount, &e.ToolCallCount,
		&e.PromptTokens, &e.CompletionTokens, &e.TotalTokens, &e.CostEstimateMinor,
		&e.StartedAt, &e.CompletedAt, &e.CreatedAt); err != nil {
		return e, err
	}
	e.Input, e.Output, e.Error = input, outputPtr, errPtr
	return e, nil
}

// Submit validates the request, creates the execution row (idempotent per
// organization) and publishes the durable job.
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())

	var req struct {
		AgentID        string          `json:"agent_id"`
		AgentVersionID string          `json:"agent_version_id"`
		Input          json.RawMessage `json:"input"`
		IdempotencyKey string          `json:"idempotency_key"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	if req.AgentID == "" {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "agent_id is required")
		return
	}
	if len(req.Input) == 0 {
		req.Input = json.RawMessage("{}")
	}
	var inputCheck any
	if err := json.Unmarshal(req.Input, &inputCheck); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "input must be valid JSON")
		return
	}

	var e executionResponse
	created := false
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		// The agent must be active and visible in this organization.
		var agentStatus string
		if err := tx.QueryRow(r.Context(),
			"SELECT status FROM agents WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL",
			req.AgentID, orgID).Scan(&agentStatus); err != nil {
			return err
		}
		if agentStatus != "active" {
			return errAgentNotActive
		}

		// Version: explicit or the latest published.
		versionID := req.AgentVersionID
		if versionID == "" {
			err := tx.QueryRow(r.Context(), `
				SELECT id FROM agent_versions
				WHERE agent_id = $1 AND status = 'published'
				ORDER BY version DESC LIMIT 1`, req.AgentID).Scan(&versionID)
			if errors.Is(err, pgx.ErrNoRows) {
				return errNoPublishedVersion
			}
			if err != nil {
				return err
			}
		} else {
			var vOrg string
			if err := tx.QueryRow(r.Context(),
				"SELECT organization_id FROM agent_versions WHERE id = $1", versionID,
			).Scan(&vOrg); err != nil {
				return err
			}
			if vOrg != orgID {
				return pgx.ErrNoRows
			}
		}

		// Idempotent insert: a duplicate key returns the original row.
		row := tx.QueryRow(r.Context(), `
			INSERT INTO executions (organization_id, agent_id, agent_version_id, input, idempotency_key)
			VALUES ($1, $2, $3, $4, NULLIF($5, ''))
			ON CONFLICT (organization_id, idempotency_key) WHERE idempotency_key IS NOT NULL
			DO UPDATE SET updated_at = now()
			RETURNING id, organization_id, agent_id, agent_version_id, status,
				input, output, error, COALESCE(idempotency_key, ''),
				step_count, tool_call_count, prompt_tokens, completion_tokens,
				total_tokens, cost_estimate_minor,
				started_at::text, completed_at::text, created_at::text,
				(xmax = 0) AS inserted`,
			orgID, req.AgentID, versionID, req.Input, req.IdempotencyKey)

		var inserted bool
		var outputPtr, errPtr []byte
		if err := row.Scan(&e.ID, &e.OrganizationID, &e.AgentID, &e.AgentVersionID,
			&e.Status, &e.Input, &outputPtr, &errPtr,
			&e.IdempotencyKey, &e.StepCount, &e.ToolCallCount,
			&e.PromptTokens, &e.CompletionTokens, &e.TotalTokens, &e.CostEstimateMinor,
			&e.StartedAt, &e.CompletedAt, &e.CreatedAt, &inserted); err != nil {
			return err
		}
		e.Output, e.Error = outputPtr, errPtr
		created = inserted

		if !inserted {
			return nil // idempotent replay — do not re-enqueue
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "execution.submitted", ResourceType: "execution", ResourceID: e.ID,
			Result: "success", Metadata: map[string]any{"agent_id": req.AgentID},
		})
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, r, http.StatusNotFound, "not_found", "agent or version not found")
		return
	case errors.Is(err, errAgentNotActive):
		httpx.Error(w, r, http.StatusConflict, "agent_not_active",
			"executions can only be submitted for active agents")
		return
	case errors.Is(err, errNoPublishedVersion):
		httpx.Error(w, r, http.StatusConflict, "no_published_version",
			"the agent has no published version to execute")
		return
	case err != nil:
		h.Logger.Error("execution submit failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to submit execution")
		return
	}

	// Publish only for genuinely new submissions (idempotent replays skip).
	if created && h.Publisher != nil {
		job := queue.Job{
			ExecutionID:    e.ID,
			OrganizationID: e.OrganizationID,
			AgentID:        e.AgentID,
			AgentVersionID: e.AgentVersionID,
			Input:          e.Input,
			Attempt:        1,
		}
		if err := h.Publisher.PublishJob(r.Context(), job); err != nil {
			// The row exists; the job publish can be retried — surface 503
			// so the caller knows the execution is queued but not dispatched.
			h.Logger.Error("job publish failed", slog.Any("error", err))
			httpx.Error(w, r, http.StatusServiceUnavailable, "queue_unavailable",
				"execution accepted but dispatch is temporarily unavailable")
			return
		}
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.JSON(w, r, status, e)
}

// List returns executions with optional filters and paging.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	q := r.URL.Query()
	status := q.Get("status")
	agentID := q.Get("agent_id")
	limit := clampLimit(q.Get("limit"))
	offset := clampOffset(q.Get("offset"))

	where := " WHERE organization_id = $1"
	args := []any{orgID}
	if status != "" {
		args = append(args, status)
		where += " AND status = $" + strconv.Itoa(len(args))
	}
	if agentID != "" {
		args = append(args, agentID)
		where += " AND agent_id = $" + strconv.Itoa(len(args))
	}
	args = append(args, limit, offset)

	var items []executionResponse
	var total int64
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		if err := tx.QueryRow(r.Context(),
			"SELECT count(*) FROM executions"+where, args[:len(args)-2]...,
		).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(r.Context(), selectExecution+where+
			" ORDER BY created_at DESC LIMIT $"+strconv.Itoa(len(args)-1)+
			" OFFSET $"+strconv.Itoa(len(args)), args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []executionResponse{}
		for rows.Next() {
			e, err := scanExecutionRow(rows)
			if err != nil {
				return err
			}
			items = append(items, e)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("execution list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list executions")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{
		"items": items, "total": total, "limit": limit, "offset": offset,
	})
}

// Get returns one execution.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	executionID := chi.URLParam(r, "executionId")
	var e executionResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		row := tx.QueryRow(r.Context(), selectExecution+" WHERE id = $1", executionID)
		var input, outputPtr, errPtr []byte
		scanErr := row.Scan(&e.ID, &e.OrganizationID, &e.AgentID, &e.AgentVersionID,
			&e.Status, &input, &outputPtr, &errPtr,
			&e.IdempotencyKey, &e.StepCount, &e.ToolCallCount,
			&e.PromptTokens, &e.CompletionTokens, &e.TotalTokens, &e.CostEstimateMinor,
			&e.StartedAt, &e.CompletedAt, &e.CreatedAt)
		e.Input, e.Output, e.Error = input, outputPtr, errPtr
		return scanErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "execution not found")
		return
	}
	if err != nil {
		h.Logger.Error("execution get failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to load execution")
		return
	}
	httpx.JSON(w, r, http.StatusOK, e)
}

// Events returns the execution's event stream.
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	executionID := chi.URLParam(r, "executionId")
	type event struct {
		ID        string          `json:"id"`
		Seq       int             `json:"seq"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
		CreatedAt string          `json:"created_at"`
	}
	var items []event
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT id, seq, type, payload, created_at::text
			FROM execution_events WHERE execution_id = $1
			ORDER BY seq`, executionID)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []event{}
		for rows.Next() {
			var ev event
			if err := rows.Scan(&ev.ID, &ev.Seq, &ev.Type, &ev.Payload, &ev.CreatedAt); err != nil {
				return err
			}
			items = append(items, ev)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("execution events failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to load events")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// Cancel requests cooperative cancellation; the worker honors it at the
// next step boundary.
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	executionID := chi.URLParam(r, "executionId")

	var e executionResponse
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		tag, err := tx.Exec(r.Context(), `
			UPDATE executions SET cancel_requested = true
			WHERE id = $1 AND organization_id = $2
			  AND status IN ('queued', 'running', 'waiting_approval')`,
			executionID, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		row := tx.QueryRow(r.Context(), selectExecution+" WHERE id = $1", executionID)
		var input, outputPtr, errPtr []byte
		scanErr := row.Scan(&e.ID, &e.OrganizationID, &e.AgentID, &e.AgentVersionID,
			&e.Status, &input, &outputPtr, &errPtr,
			&e.IdempotencyKey, &e.StepCount, &e.ToolCallCount,
			&e.PromptTokens, &e.CompletionTokens, &e.TotalTokens, &e.CostEstimateMinor,
			&e.StartedAt, &e.CompletedAt, &e.CreatedAt)
		if scanErr != nil {
			return scanErr
		}
		e.Input, e.Output, e.Error = input, outputPtr, errPtr
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "execution.cancel_requested", ResourceType: "execution", ResourceID: executionID,
			Result: "success",
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, r, http.StatusConflict, "not_cancellable",
			"execution not found or already in a terminal state")
	case err != nil:
		h.Logger.Error("execution cancel failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to cancel execution")
	default:
		httpx.JSON(w, r, http.StatusOK, e)
	}
}

func clampLimit(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 50
	}
	if n > 200 {
		return 200
	}
	return n
}

func clampOffset(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// sentinel errors mapped to HTTP responses
var (
	errAgentNotActive     = errors.New("agent is not active")
	errNoPublishedVersion = errors.New("agent has no published version")
)
