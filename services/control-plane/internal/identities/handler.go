// Package identities implements the agent identity lifecycle: issuance,
// suspension and revocation. Raw credentials are shown exactly once at
// creation; only their hash is stored.
package identities

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/audit"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/auth"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// Handler implements identity endpoints.
type Handler struct {
	Store  *store.Store
	Logger *slog.Logger
}

type identityResponse struct {
	ID         string  `json:"id"`
	AgentID    string  `json:"agent_id"`
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	Prefix     string  `json:"credential_prefix"`
	ExpiresAt  *string `json:"credential_expires_at,omitempty"`
	LastUsedAt *string `json:"last_used_at,omitempty"`
	RevokedAt  *string `json:"revoked_at,omitempty"`
	CreatedAt  string  `json:"created_at"`
}

// Create issues a new identity credential for an agent.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	agentID := chi.URLParam(r, "agentId")

	var req struct {
		Name      string `json:"name"`
		ExpiresAt string `json:"credential_expires_at"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	req.Name = trim(req.Name)
	if req.Name == "" || len(req.Name) > 120 {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"name is required (max 120 characters)")
		return
	}
	var expiresAt *time.Time
	if req.ExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
				"credential_expires_at must be RFC3339")
			return
		}
		if parsed.Before(time.Now()) {
			httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
				"credential_expires_at must be in the future")
			return
		}
		expiresAt = &parsed
	}

	secret, secretHash, prefix, err := auth.NewAPIKey()
	if err != nil {
		h.Logger.Error("identity credential generation failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to generate credential")
		return
	}

	var identity identityResponse
	err = h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		// The agent must exist in this organization (RLS scopes it).
		var exists bool
		if err := tx.QueryRow(r.Context(),
			"SELECT EXISTS (SELECT 1 FROM agents WHERE id = $1 AND deleted_at IS NULL)",
			agentID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return pgx.ErrNoRows
		}
		err := tx.QueryRow(r.Context(), `
			INSERT INTO agent_identities
				(organization_id, agent_id, name, credential_hash, credential_prefix, credential_expires_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, agent_id, name, status, credential_prefix,
			          credential_expires_at::text, last_used_at::text, revoked_at::text, created_at::text`,
			orgID, agentID, req.Name, secretHash, prefix, expiresAt,
		).Scan(&identity.ID, &identity.AgentID, &identity.Name, &identity.Status,
			&identity.Prefix, &identity.ExpiresAt, &identity.LastUsedAt, &identity.RevokedAt, &identity.CreatedAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "identity.created", ResourceType: "agent_identity", ResourceID: identity.ID,
			Result: "success", Metadata: map[string]any{"agent_id": agentID, "prefix": prefix},
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, r, http.StatusNotFound, "not_found", "agent not found")
	case err != nil:
		h.Logger.Error("identity create failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to create identity")
	default:
		// The raw secret is disclosed exactly once, at creation.
		httpx.JSON(w, r, http.StatusCreated, map[string]any{
			"identity":   identity,
			"credential": secret,
			"note":       "store this credential now — it is not retrievable later",
		})
	}
}

// List returns an agent's identities (metadata only — never credentials).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	agentID := chi.URLParam(r, "agentId")
	var items []identityResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT id, agent_id, name, status, credential_prefix,
			       credential_expires_at::text, last_used_at::text, revoked_at::text, created_at::text
			FROM agent_identities WHERE agent_id = $1
			ORDER BY created_at DESC`, agentID)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []identityResponse{}
		for rows.Next() {
			var i identityResponse
			var expires, lastUsed, revoked *string
			if err := rows.Scan(&i.ID, &i.AgentID, &i.Name, &i.Status, &i.Prefix,
				&expires, &lastUsed, &revoked, &i.CreatedAt); err != nil {
				return err
			}
			i.ExpiresAt, i.LastUsedAt, i.RevokedAt = expires, lastUsed, revoked
			items = append(items, i)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("identity list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list identities")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// Suspend temporarily disables an identity without destroying it.
func (h *Handler) Suspend(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, "suspended", "identity.suspended")
}

// Revoke permanently disables an identity (emergency stop).
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, "revoked", "identity.revoked")
}

func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request, status, action string) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	identityID := chi.URLParam(r, "identityId")

	var identity identityResponse
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		setRevoked := status == "revoked"
		query := `UPDATE agent_identities SET status = $3`
		if setRevoked {
			query += `, revoked_at = now()`
		}
		query += ` WHERE id = $1 AND organization_id = $2 AND status <> 'revoked'
			RETURNING id, agent_id, name, status, credential_prefix,
				credential_expires_at::text, last_used_at::text, revoked_at::text, created_at::text`
		err := tx.QueryRow(r.Context(), query, identityID, orgID, status).Scan(&identity.ID, &identity.AgentID, &identity.Name, &identity.Status,
			&identity.Prefix, &identity.ExpiresAt, &identity.LastUsedAt, &identity.RevokedAt, &identity.CreatedAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: action, ResourceType: "agent_identity", ResourceID: identityID,
			Result: "success", Metadata: map[string]any{"status": status},
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, r, http.StatusNotFound, "not_found",
			"identity not found or already revoked")
	case err != nil:
		h.Logger.Error("identity status update failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to update identity")
	default:
		httpx.JSON(w, r, http.StatusOK, identity)
	}
}

func trim(s string) string {
	out := []rune(s)
	start, end := 0, len(out)
	for start < end && (out[start] == ' ' || out[start] == '\t') {
		start++
	}
	for end > start && (out[end-1] == ' ' || out[end-1] == '\t') {
		end--
	}
	return string(out[start:end])
}
