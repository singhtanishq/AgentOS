// Package mcp implements MCP connection management: registering servers,
// checking reachability metadata and controlling status.
package mcp

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/audit"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/auth"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// Handler implements MCP connection endpoints.
type Handler struct {
	Store  *store.Store
	Logger *slog.Logger
}

type connectionResponse struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Transport     string `json:"transport"`
	URL           string `json:"url"`
	Status        string `json:"status"`
	HealthStatus  string `json:"health_status"`
	LastCheckedAt string `json:"last_checked_at,omitempty"`
	CreatedAt     string `json:"created_at"`
}

// Create registers an MCP server connection.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())

	var req struct {
		Name      string `json:"name"`
		Transport string `json:"transport"`
		URL       string `json:"url"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 120 {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"name is required (max 120 characters)")
		return
	}
	if req.Transport == "" {
		req.Transport = "streamable_http"
	}
	if req.Transport != "streamable_http" && req.Transport != "sse" {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"transport must be streamable_http or sse")
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	parsed, err := url.Parse(req.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"url must be a valid http(s) endpoint")
		return
	}

	var c connectionResponse
	err = h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		err := tx.QueryRow(r.Context(), `
			INSERT INTO mcp_connections (organization_id, name, transport, url)
			VALUES ($1, $2, $3, $4)
			RETURNING id, name, transport, url, status, health_status, last_checked_at::text, created_at::text`,
			orgID, req.Name, req.Transport, req.URL,
		).Scan(&c.ID, &c.Name, &c.Transport, &c.URL, &c.Status, &c.HealthStatus, &c.LastCheckedAt, &c.CreatedAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "mcp_connection.created", ResourceType: "mcp_connection", ResourceID: c.ID,
			Result: "success", Metadata: map[string]any{"url": req.URL},
		})
	})
	if err != nil {
		if isUniqueViolation(err) {
			httpx.Error(w, r, http.StatusConflict, "name_taken",
				"a connection with this name already exists")
			return
		}
		h.Logger.Error("mcp create failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to register connection")
		return
	}
	httpx.JSON(w, r, http.StatusCreated, c)
}

// List returns the organization's MCP connections.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	var items []connectionResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT id, name, transport, url, status, health_status,
			       COALESCE(last_checked_at::text, ''), created_at::text
			FROM mcp_connections WHERE organization_id = $1
			ORDER BY created_at`, orgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []connectionResponse{}
		for rows.Next() {
			var c connectionResponse
			if err := rows.Scan(&c.ID, &c.Name, &c.Transport, &c.URL, &c.Status,
				&c.HealthStatus, &c.LastCheckedAt, &c.CreatedAt); err != nil {
				return err
			}
			items = append(items, c)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("mcp list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list connections")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// UpdateStatus enables or disables a connection.
func (h *Handler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	connectionID := chi.URLParam(r, "connectionId")

	var req struct {
		Status string `json:"status"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	status := strings.ToLower(strings.TrimSpace(req.Status))
	if status != "active" && status != "disabled" {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"status must be active or disabled")
		return
	}

	var c connectionResponse
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		err := tx.QueryRow(r.Context(), `
			UPDATE mcp_connections SET status = $3
			WHERE id = $1 AND organization_id = $2
			RETURNING id, name, transport, url, status, health_status,
			          COALESCE(last_checked_at::text, ''), created_at::text`,
			connectionID, orgID, status,
		).Scan(&c.ID, &c.Name, &c.Transport, &c.URL, &c.Status, &c.HealthStatus, &c.LastCheckedAt, &c.CreatedAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "mcp_connection.status_changed", ResourceType: "mcp_connection", ResourceID: connectionID,
			Result: "success", Metadata: map[string]any{"status": status},
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "connection not found")
		return
	}
	if err != nil {
		h.Logger.Error("mcp status update failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to update connection")
		return
	}
	httpx.JSON(w, r, http.StatusOK, c)
}

// Delete removes a connection (bindings through tools.config become
// unavailable and executions fail validation until rebound).
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	connectionID := chi.URLParam(r, "connectionId")

	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		tag, err := tx.Exec(r.Context(),
			"DELETE FROM mcp_connections WHERE id = $1 AND organization_id = $2",
			connectionID, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "mcp_connection.deleted", ResourceType: "mcp_connection", ResourceID: connectionID,
			Result: "success",
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "connection not found")
		return
	}
	if err != nil {
		h.Logger.Error("mcp delete failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to delete connection")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]string{"status": "deleted"})
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
