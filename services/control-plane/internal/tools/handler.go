// Package tools implements the tool registry API and the platform builtin
// catalog that is seeded for every organization.
package tools

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/audit"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/auth"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// Handler implements tool registry endpoints.
type Handler struct {
	Store  *store.Store
	Logger *slog.Logger
}

type toolResponse struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Kind         string          `json:"kind"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
	Config       json.RawMessage `json:"config"`
	RequiredRole string          `json:"required_role"`
	Status       string          `json:"status"`
	TimeoutMs    int             `json:"timeout_ms"`
	Version      int             `json:"version"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
}

// List returns the organization's registered tools.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	kindFilter := strings.TrimSpace(r.URL.Query().Get("kind"))
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))

	where := " WHERE organization_id = $1"
	args := []any{orgID}
	if kindFilter != "" {
		args = append(args, kindFilter)
		where += " AND kind = $" + itoa(len(args))
	}
	if statusFilter != "" {
		args = append(args, statusFilter)
		where += " AND status = $" + itoa(len(args))
	}

	var items []toolResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(),
			`SELECT id, name, kind, description, input_schema, output_schema, config,
			        required_role, status, timeout_ms, version, created_at::text, updated_at::text
			 FROM tools`+where+` ORDER BY name`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []toolResponse{}
		for rows.Next() {
			var t toolResponse
			if err := rows.Scan(&t.ID, &t.Name, &t.Kind, &t.Description, &t.InputSchema,
				&t.OutputSchema, &t.Config, &t.RequiredRole, &t.Status, &t.TimeoutMs,
				&t.Version, &t.CreatedAt, &t.UpdatedAt); err != nil {
				return err
			}
			items = append(items, t)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("tool list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list tools")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// Get returns one tool.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	toolID := chi.URLParam(r, "toolId")
	var t toolResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		return tx.QueryRow(r.Context(), `
			SELECT id, name, kind, description, input_schema, output_schema, config,
			       required_role, status, timeout_ms, version, created_at::text, updated_at::text
			FROM tools WHERE id = $1`, toolID,
		).Scan(&t.ID, &t.Name, &t.Kind, &t.Description, &t.InputSchema, &t.OutputSchema,
			&t.Config, &t.RequiredRole, &t.Status, &t.TimeoutMs, &t.Version, &t.CreatedAt, &t.UpdatedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "tool not found")
		return
	}
	if err != nil {
		h.Logger.Error("tool get failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to load tool")
		return
	}
	httpx.JSON(w, r, http.StatusOK, t)
}

// Update changes tool configuration, status and permissions. The schema
// and kind are immutable (registry integrity); the version bumps on every
// change so bindings can reference specific behavior.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	toolID := chi.URLParam(r, "toolId")

	var req struct {
		Description  *string         `json:"description"`
		Config       json.RawMessage `json:"config"`
		Status       *string         `json:"status"`
		RequiredRole *string         `json:"required_role"`
		TimeoutMs    *int            `json:"timeout_ms"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	if req.Status != nil {
		s := strings.ToLower(strings.TrimSpace(*req.Status))
		if s != "active" && s != "disabled" {
			httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
				"status must be active or disabled")
			return
		}
		req.Status = &s
	}
	if req.RequiredRole != nil {
		role := strings.ToLower(strings.TrimSpace(*req.RequiredRole))
		switch role {
		case "operator", "developer", "admin":
			req.RequiredRole = &role
		default:
			httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
				"required_role must be operator, developer or admin")
			return
		}
	}
	if req.TimeoutMs != nil && (*req.TimeoutMs < 100 || *req.TimeoutMs > 60_000) {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"timeout_ms must be between 100 and 60000")
		return
	}

	var t toolResponse
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		err := tx.QueryRow(r.Context(), `
			UPDATE tools SET
				description = COALESCE($3, description),
				config = COALESCE($4::jsonb, config),
				status = COALESCE($5, status),
				required_role = COALESCE($6, required_role),
				timeout_ms = COALESCE($7, timeout_ms),
				version = version + 1
			WHERE id = $1 AND organization_id = $2
			RETURNING id, name, kind, description, input_schema, output_schema, config,
			          required_role, status, timeout_ms, version, created_at::text, updated_at::text`,
			toolID, orgID, req.Description, nullJSON(req.Config), req.Status,
			req.RequiredRole, req.TimeoutMs,
		).Scan(&t.ID, &t.Name, &t.Kind, &t.Description, &t.InputSchema, &t.OutputSchema,
			&t.Config, &t.RequiredRole, &t.Status, &t.TimeoutMs, &t.Version, &t.CreatedAt, &t.UpdatedAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "tool.updated", ResourceType: "tool", ResourceID: toolID, Result: "success",
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "tool not found")
		return
	}
	if err != nil {
		h.Logger.Error("tool update failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to update tool")
		return
	}
	httpx.JSON(w, r, http.StatusOK, t)
}

func nullJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

func itoa(n int) string { return strconv.Itoa(n) }
