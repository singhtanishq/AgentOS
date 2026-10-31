// Package deployments implements agent deployment management: publishing
// versions to environments, activation history and rollback.
package deployments

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/audit"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/auth"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// Handler implements deployment endpoints.
type Handler struct {
	Store  *store.Store
	Logger *slog.Logger
}

type deploymentResponse struct {
	ID             string  `json:"id"`
	OrganizationID string  `json:"organization_id"`
	AgentID        string  `json:"agent_id"`
	AgentVersionID string  `json:"agent_version_id"`
	Version        int     `json:"version"`
	Environment    string  `json:"environment"`
	Status         string  `json:"status"`
	CreatedByEmail string  `json:"created_by_email,omitempty"`
	DeactivatedAt  *string `json:"deactivated_at,omitempty"`
	CreatedAt      string  `json:"created_at"`
}

const selectDeployment = `
	SELECT d.id, d.organization_id, d.agent_id, d.agent_version_id,
	       v.version, d.environment, d.status,
	       COALESCE(u.email, ''), d.deactivated_at::text, d.created_at::text
	FROM deployments d
	JOIN agent_versions v ON v.id = d.agent_version_id
	LEFT JOIN users u ON u.id = d.created_by`

// Deploy activates a published version in an environment. The previously
// active deployment in that environment becomes inactive (history kept);
// rolling back means deploying an older version again.
func (h *Handler) Deploy(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	agentID := chi.URLParam(r, "agentId")

	var req struct {
		AgentVersionID string `json:"agent_version_id"`
		Environment    string `json:"environment"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	if req.Environment == "" {
		req.Environment = "production"
	}
	switch req.Environment {
	case "development", "staging", "production":
	default:
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"environment must be development, staging or production")
		return
	}

	var d deploymentResponse
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		// The version must be published and belong to this agent/org.
		var vOrg, vAgent, vStatus string
		if err := tx.QueryRow(r.Context(), `
			SELECT organization_id, agent_id, status FROM agent_versions WHERE id = $1`,
			req.AgentVersionID).Scan(&vOrg, &vAgent, &vStatus); err != nil {
			return err
		}
		if vOrg != orgID || vAgent != agentID {
			return pgx.ErrNoRows
		}
		if vStatus != "published" {
			return errVersionNotPublished
		}

		// Deactivate any current active deployment in the environment.
		if _, err := tx.Exec(r.Context(), `
			UPDATE deployments SET status = 'inactive', deactivated_at = now()
			WHERE agent_id = $1 AND environment = $2 AND status = 'active'`,
			agentID, req.Environment); err != nil {
			return err
		}

		err := tx.QueryRow(r.Context(), `
			INSERT INTO deployments
				(organization_id, agent_id, agent_version_id, environment, status, created_by)
			VALUES ($1, $2, $3, $4, 'active', $5)
			RETURNING id, organization_id, agent_id, agent_version_id,
				(SELECT version FROM agent_versions WHERE id = agent_version_id),
				environment, status, COALESCE((SELECT email FROM users WHERE id = created_by), ''),
				deactivated_at::text, created_at::text`,
			orgID, agentID, req.AgentVersionID, req.Environment, actorID,
		).Scan(&d.ID, &d.OrganizationID, &d.AgentID, &d.AgentVersionID,
			&d.Version, &d.Environment, &d.Status, &d.CreatedByEmail,
			&d.DeactivatedAt, &d.CreatedAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "deployment.created", ResourceType: "deployment", ResourceID: d.ID,
			Result: "success",
			Metadata: map[string]any{
				"agent_id": agentID, "environment": req.Environment, "version": d.Version,
			},
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, r, http.StatusNotFound, "not_found", "agent or version not found")
	case errors.Is(err, errVersionNotPublished):
		httpx.Error(w, r, http.StatusConflict, "version_not_published",
			"only published versions can be deployed")
	case err != nil:
		h.Logger.Error("deploy failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to deploy")
	default:
		httpx.JSON(w, r, http.StatusCreated, d)
	}
}

// List returns deployment history with optional filters.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	q := r.URL.Query()
	where := " WHERE d.organization_id = $1"
	args := []any{orgID}
	if agentID := q.Get("agent_id"); agentID != "" {
		args = append(args, agentID)
		where += " AND d.agent_id = $" + itoa(len(args))
	}
	if env := q.Get("environment"); env != "" {
		args = append(args, env)
		where += " AND d.environment = $" + itoa(len(args))
	}
	if status := q.Get("status"); status != "" {
		args = append(args, status)
		where += " AND d.status = $" + itoa(len(args))
	}

	var items []deploymentResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), selectDeployment+where+
			" ORDER BY d.created_at DESC", args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []deploymentResponse{}
		for rows.Next() {
			var d deploymentResponse
			if err := rows.Scan(&d.ID, &d.OrganizationID, &d.AgentID, &d.AgentVersionID,
				&d.Version, &d.Environment, &d.Status, &d.CreatedByEmail,
				&d.DeactivatedAt, &d.CreatedAt); err != nil {
				return err
			}
			items = append(items, d)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("deployment list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list deployments")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// Deactivate stops an active deployment.
func (h *Handler) Deactivate(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	deploymentID := chi.URLParam(r, "deploymentId")

	var d deploymentResponse
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		err := tx.QueryRow(r.Context(), `
			UPDATE deployments SET status = 'inactive', deactivated_at = now()
			WHERE id = $1 AND organization_id = $2 AND status = 'active'
			RETURNING id, organization_id, agent_id, agent_version_id,
				(SELECT version FROM agent_versions WHERE id = agent_version_id),
				environment, status, COALESCE((SELECT email FROM users WHERE id = created_by), ''),
				deactivated_at::text, created_at::text`,
			deploymentID, orgID,
		).Scan(&d.ID, &d.OrganizationID, &d.AgentID, &d.AgentVersionID,
			&d.Version, &d.Environment, &d.Status, &d.CreatedByEmail,
			&d.DeactivatedAt, &d.CreatedAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "deployment.deactivated", ResourceType: "deployment", ResourceID: deploymentID,
			Result: "success",
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found",
			"deployment not found or already inactive")
		return
	}
	if err != nil {
		h.Logger.Error("deactivate failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to deactivate")
		return
	}
	httpx.JSON(w, r, http.StatusOK, d)
}

// Rollback reverts the agent+environment to the version that was deployed
// before the given deployment. A new active deployment row is created
// (history preserved) and the current one is retired. With no prior
// deployment there is nothing to roll back to.
func (h *Handler) Rollback(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	deploymentID := chi.URLParam(r, "deploymentId")

	var d deploymentResponse
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		var agentID, environment string
		if err := tx.QueryRow(r.Context(), `
			SELECT agent_id, environment FROM deployments
			WHERE id = $1 AND organization_id = $2`, deploymentID, orgID,
		).Scan(&agentID, &environment); err != nil {
			return err
		}
		// The most recent earlier deployment for this agent+environment.
		var previousVersionID string
		if err := tx.QueryRow(r.Context(), `
			SELECT agent_version_id FROM deployments
			WHERE agent_id = $1 AND environment = $2 AND organization_id = $3
			  AND id <> $4 AND created_at < (SELECT created_at FROM deployments WHERE id = $4)
			ORDER BY created_at DESC LIMIT 1`,
			agentID, environment, orgID, deploymentID,
		).Scan(&previousVersionID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errNothingToRollback
			}
			return err
		}
		if _, err := tx.Exec(r.Context(), `
			UPDATE deployments SET status = 'rolled_back', deactivated_at = now()
			WHERE agent_id = $1 AND environment = $2 AND status = 'active'`,
			agentID, environment); err != nil {
			return err
		}
		err := tx.QueryRow(r.Context(), `
			INSERT INTO deployments
				(organization_id, agent_id, agent_version_id, environment, status, created_by)
			VALUES ($1, $2, $3, $4, 'active', $5)
			RETURNING id, organization_id, agent_id, agent_version_id,
				(SELECT version FROM agent_versions WHERE id = agent_version_id),
				environment, status, COALESCE((SELECT email FROM users WHERE id = created_by), ''),
				deactivated_at::text, created_at::text`,
			orgID, agentID, previousVersionID, environment, actorID,
		).Scan(&d.ID, &d.OrganizationID, &d.AgentID, &d.AgentVersionID,
			&d.Version, &d.Environment, &d.Status, &d.CreatedByEmail,
			&d.DeactivatedAt, &d.CreatedAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "deployment.rollback", ResourceType: "deployment", ResourceID: d.ID,
			Result: "success", Metadata: map[string]any{"from_deployment": deploymentID},
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, r, http.StatusNotFound, "not_found", "deployment not found")
	case errors.Is(err, errNothingToRollback):
		httpx.Error(w, r, http.StatusConflict, "nothing_to_rollback",
			"there is no earlier deployment to roll back to")
	case err != nil:
		h.Logger.Error("rollback failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to roll back")
	default:
		httpx.JSON(w, r, http.StatusCreated, d)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

var (
	errVersionNotPublished = errors.New("version is not published")
	errNothingToRollback   = errors.New("no earlier deployment to roll back to")
)
