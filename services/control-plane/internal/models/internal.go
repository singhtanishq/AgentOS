package models

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/secrets"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// InternalHandler serves the service-to-service surface consumed by the
// Python runtime. It is protected by a shared bearer token, never exposed
// through the dashboard, and returns decrypted provider credentials only
// to the execution plane at run time.
type InternalHandler struct {
	Store   *store.Store
	Secrets *secrets.Manager
	Token   string
	Logger  *slog.Logger
}

// RequireServiceToken authenticates internal calls with a constant-time
// bearer token comparison.
func (h *InternalHandler) RequireServiceToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(authz) <= len(prefix) || subtle.ConstantTimeCompare(
			[]byte(authz[:len(prefix)]), []byte(prefix)) != 1 {
			httpx.Error(w, r, http.StatusUnauthorized, "unauthorized", "service token required")
			return
		}
		if subtle.ConstantTimeCompare([]byte(authz[len(prefix):]), []byte(h.Token)) != 1 {
			httpx.Error(w, r, http.StatusUnauthorized, "invalid service token", "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RevealCredential returns the decrypted provider credential for the
// execution plane. The runtime supplies the organization it is executing
// for; the lookup is scoped by both, so a cross-tenant read is impossible.
func (h *InternalHandler) RevealCredential(w http.ResponseWriter, r *http.Request) {
	credentialID := chi.URLParam(r, "credentialId")
	orgID := r.URL.Query().Get("organization_id")
	if credentialID == "" || orgID == "" {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"credential id and organization_id are required")
		return
	}

	var providerType, baseURL string
	var version byte
	var ciphertext, nonce []byte
	err := h.Store.OrgTx(r.Context(), orgID, "", func(tx store.Tx) error {
		return tx.QueryRow(r.Context(), `
			SELECT p.provider_type, p.base_url, c.key_version, c.ciphertext, c.nonce
			FROM model_credentials c
			JOIN model_providers p ON p.id = c.provider_id
			WHERE c.id = $1 AND c.organization_id = $2 AND c.status = 'active'`,
			credentialID, orgID,
		).Scan(&providerType, &baseURL, &version, &ciphertext, &nonce)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "credential not found")
		return
	}
	if err != nil {
		h.Logger.Error("credential reveal lookup failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to load credential")
		return
	}

	plaintext, err := h.Secrets.DecryptParts(version, nonce, ciphertext)
	if err != nil {
		h.Logger.Error("credential decryption failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to decrypt credential")
		return
	}

	httpx.JSON(w, r, http.StatusOK, map[string]string{
		"provider_type": providerType,
		"base_url":      baseURL,
		"api_key":       string(plaintext),
	})
}
