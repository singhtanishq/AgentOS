// Package apikeys implements developer API key management.
package apikeys

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/audit"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/auth"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// Handler implements API key endpoints.
type Handler struct {
	Store  *store.Store
	Logger *slog.Logger
}

type keyResponse struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"key_prefix"`
	Scopes     []string `json:"scopes"`
	Status     string   `json:"status"`
	CreatedAt  string   `json:"created_at"`
	LastUsedAt *string  `json:"last_used_at,omitempty"`
	RevokedAt  *string  `json:"revoked_at,omitempty"`
}

var validScopes = map[string]bool{
	"executions.submit": true, "executions.read": true, "executions.cancel": true,
	"agents.read": true, "agents.edit": true,
}

// Create issues a new API key. The raw key is disclosed exactly once.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())

	var req struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
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
	if len(req.Scopes) == 0 {
		req.Scopes = []string{"executions.submit", "executions.read", "agents.read"}
	}
	for _, s := range req.Scopes {
		if !validScopes[s] {
			httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
				"unknown scope: "+s)
			return
		}
	}

	key, keyHash, prefix, err := auth.NewAPIKey()
	if err != nil {
		h.Logger.Error("api key generation failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to generate key")
		return
	}

	var k keyResponse
	err = h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		err := tx.QueryRow(r.Context(), `
			INSERT INTO api_keys (organization_id, name, key_hash, key_prefix, scopes, created_by)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, name, key_prefix, scopes, status, created_at::text,
			          last_used_at::text, revoked_at::text`,
			orgID, req.Name, keyHash, prefix, req.Scopes, actorID,
		).Scan(&k.ID, &k.Name, &k.Prefix, &k.Scopes, &k.Status, &k.CreatedAt,
			&k.LastUsedAt, &k.RevokedAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "api_key.created", ResourceType: "api_key", ResourceID: k.ID,
			Result: "success", Metadata: map[string]any{"scopes": req.Scopes},
		})
	})
	if err != nil {
		h.Logger.Error("api key create failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to create key")
		return
	}
	httpx.JSON(w, r, http.StatusCreated, map[string]any{
		"key":    k,
		"secret": key,
		"note":   "store this key now — it is not retrievable later",
	})
}

// List returns the organization's API keys (metadata only).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	var items []keyResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT id, name, key_prefix, scopes, status, created_at::text,
			       last_used_at::text, revoked_at::text
			FROM api_keys WHERE organization_id = $1
			ORDER BY created_at DESC`, orgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []keyResponse{}
		for rows.Next() {
			var k keyResponse
			if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &k.Scopes, &k.Status,
				&k.CreatedAt, &k.LastUsedAt, &k.RevokedAt); err != nil {
				return err
			}
			items = append(items, k)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("api key list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list keys")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// Revoke permanently disables a key.
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	keyID := chi.URLParam(r, "keyId")

	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		tag, err := tx.Exec(r.Context(), `
			UPDATE api_keys SET status = 'revoked', revoked_at = now()
			WHERE id = $1 AND organization_id = $2 AND status = 'active'`,
			keyID, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "api_key.revoked", ResourceType: "api_key", ResourceID: keyID,
			Result: "success",
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "key not found or already revoked")
		return
	}
	if err != nil {
		h.Logger.Error("api key revoke failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to revoke key")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]string{"status": "revoked"})
}
