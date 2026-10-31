// Package models implements the model gateway control surfaces: provider
// registration, encrypted credential storage and the model registry with
// versioned pricing.
package models

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/agents"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/audit"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/auth"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/secrets"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// Handler implements model gateway endpoints.
type Handler struct {
	Store   *store.Store
	Secrets *secrets.Manager
	Logger  *slog.Logger
}

type providerResponse struct {
	ID          string `json:"id"`
	Provider    string `json:"provider_type"`
	DisplayName string `json:"display_name"`
	BaseURL     string `json:"base_url,omitempty"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

type credentialResponse struct {
	ID          string  `json:"id"`
	ProviderID  string  `json:"provider_id"`
	Label       string  `json:"label"`
	Fingerprint string  `json:"fingerprint"`
	Status      string  `json:"status"`
	CreatedAt   string  `json:"created_at"`
	RotatedAt   *string `json:"rotated_at,omitempty"`
}

type modelResponse struct {
	ID                 string  `json:"id"`
	ProviderID         string  `json:"provider_id"`
	Provider           string  `json:"provider_type"`
	ModelID            string  `json:"model_id"`
	DisplayName        string  `json:"display_name"`
	Capabilities       jsonRaw `json:"capabilities"`
	InputPricePerMTok  int64   `json:"input_price_per_mtok_minor"`
	OutputPricePerMTok int64   `json:"output_price_per_mtok_minor"`
	PricingVersion     int     `json:"pricing_version"`
	Status             string  `json:"status"`
	CreatedAt          string  `json:"created_at"`
}

// jsonRaw is the raw JSON alias for capability payloads.
type jsonRaw = json.RawMessage

// CreateProvider registers a model provider for the organization.
func (h *Handler) CreateProvider(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())

	var req struct {
		ProviderType string `json:"provider_type"`
		DisplayName  string `json:"display_name"`
		BaseURL      string `json:"base_url"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	if !agents.ValidProvider(agents.ProviderType(req.ProviderType)) {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"provider_type must be one of openai, anthropic, google, compatible, mock")
		return
	}
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.DisplayName == "" {
		req.DisplayName = strings.ToUpper(req.ProviderType)
	}
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	if req.ProviderType == string(agents.ProviderCompatible) && req.BaseURL == "" {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"base_url is required for OpenAI-compatible providers")
		return
	}
	if req.BaseURL != "" && !strings.HasPrefix(req.BaseURL, "http") {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "base_url must start with http(s)")
		return
	}

	var p providerResponse
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		err := tx.QueryRow(r.Context(), `
			INSERT INTO model_providers (organization_id, provider_type, display_name, base_url)
			VALUES ($1, $2, $3, $4)
			RETURNING id, provider_type, display_name, base_url, status, created_at::text`,
			orgID, req.ProviderType, req.DisplayName, req.BaseURL,
		).Scan(&p.ID, &p.Provider, &p.DisplayName, &p.BaseURL, &p.Status, &p.CreatedAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "model_provider.created", ResourceType: "model_provider", ResourceID: p.ID,
			Result: "success", Metadata: map[string]any{"provider_type": req.ProviderType},
		})
	})
	if err != nil {
		if isUniqueViolation(err) {
			httpx.Error(w, r, http.StatusConflict, "provider_exists", "this provider is already registered")
			return
		}
		h.Logger.Error("provider create failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to register provider")
		return
	}
	httpx.JSON(w, r, http.StatusCreated, p)
}

// ListProviders returns the organization's registered providers.
func (h *Handler) ListProviders(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	var providers []providerResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT id, provider_type, display_name, base_url, status, created_at::text
			FROM model_providers WHERE organization_id = $1 ORDER BY created_at`, orgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		providers = []providerResponse{}
		for rows.Next() {
			var p providerResponse
			if err := rows.Scan(&p.ID, &p.Provider, &p.DisplayName, &p.BaseURL, &p.Status, &p.CreatedAt); err != nil {
				return err
			}
			providers = append(providers, p)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("provider list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list providers")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": providers})
}

// CreateCredential stores an encrypted provider API key. The plaintext key
// is never persisted and never returned.
func (h *Handler) CreateCredential(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	providerID := chi.URLParam(r, "providerId")

	var req struct {
		Label  string `json:"label"`
		APIKey string `json:"api_key"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	req.APIKey = strings.TrimSpace(req.APIKey)
	if len(req.APIKey) < 8 || len(req.APIKey) > 512 {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"api_key must be between 8 and 512 characters")
		return
	}
	req.Label = strings.TrimSpace(req.Label)

	envelope, err := h.Secrets.Encrypt([]byte(req.APIKey))
	if err != nil {
		h.Logger.Error("credential encryption failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to store credential")
		return
	}
	fingerprint := secrets.Fingerprint([]byte(req.APIKey))
	nonce := envelope[1:13]
	ciphertext := envelope[13:]

	var c credentialResponse
	err = h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		// The provider must exist within this organization (RLS scopes it).
		var provID string
		if err := tx.QueryRow(r.Context(),
			"SELECT id FROM model_providers WHERE id = $1 AND organization_id = $2",
			providerID, orgID).Scan(&provID); err != nil {
			return err
		}
		err := tx.QueryRow(r.Context(), `
			INSERT INTO model_credentials
				(organization_id, provider_id, label, ciphertext, nonce, key_version, key_fingerprint)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id, provider_id, label, key_fingerprint, status, created_at::text, rotated_at::text`,
			orgID, providerID, req.Label, ciphertext, nonce, int16(h.Secrets.ActiveKeyVersion()), fingerprint,
		).Scan(&c.ID, &c.ProviderID, &c.Label, &c.Fingerprint, &c.Status, &c.CreatedAt, &c.RotatedAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "model_credential.created", ResourceType: "model_credential", ResourceID: c.ID,
			Result: "success", Metadata: map[string]any{"provider_id": providerID, "fingerprint": fingerprint},
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, r, http.StatusNotFound, "not_found", "provider not found")
	case err != nil:
		h.Logger.Error("credential create failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to store credential")
	default:
		// The response intentionally contains metadata only — never the key.
		httpx.JSON(w, r, http.StatusCreated, c)
	}
}

// ListCredentials returns credential metadata (never key material).
func (h *Handler) ListCredentials(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	providerID := chi.URLParam(r, "providerId")
	var creds []credentialResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT id, provider_id, label, key_fingerprint, status, created_at::text, rotated_at::text
			FROM model_credentials
			WHERE organization_id = $1 AND provider_id = $2 AND status = 'active'
			ORDER BY created_at DESC`, orgID, providerID)
		if err != nil {
			return err
		}
		defer rows.Close()
		creds = []credentialResponse{}
		for rows.Next() {
			var c credentialResponse
			if err := rows.Scan(&c.ID, &c.ProviderID, &c.Label, &c.Fingerprint, &c.Status, &c.CreatedAt, &c.RotatedAt); err != nil {
				return err
			}
			creds = append(creds, c)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("credential list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list credentials")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": creds})
}

// RevokeCredential disables a stored key without deleting the audit trail.
func (h *Handler) RevokeCredential(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	credentialID := chi.URLParam(r, "credentialId")

	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		tag, err := tx.Exec(r.Context(), `
			UPDATE model_credentials SET status = 'revoked', revoked_at = now()
			WHERE id = $1 AND organization_id = $2 AND status = 'active'`,
			credentialID, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "model_credential.revoked", ResourceType: "model_credential", ResourceID: credentialID,
			Result: "success",
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, r, http.StatusNotFound, "not_found", "credential not found")
	case err != nil:
		h.Logger.Error("credential revoke failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to revoke credential")
	default:
		httpx.JSON(w, r, http.StatusOK, map[string]string{"status": "revoked"})
	}
}

// CreateModel registers a model with capability metadata and pricing.
func (h *Handler) CreateModel(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())

	var req struct {
		ProviderID         string  `json:"provider_id"`
		ModelID            string  `json:"model_id"`
		DisplayName        string  `json:"display_name"`
		Capabilities       jsonRaw `json:"capabilities"`
		InputPricePerMTok  int64   `json:"input_price_per_mtok_minor"`
		OutputPricePerMTok int64   `json:"output_price_per_mtok_minor"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	req.ModelID = strings.TrimSpace(req.ModelID)
	if req.ModelID == "" || len(req.ModelID) > 200 {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "model_id is required (max 200 chars)")
		return
	}
	if len(req.Capabilities) == 0 {
		req.Capabilities = jsonRaw("{}")
	}
	if req.InputPricePerMTok < 0 || req.OutputPricePerMTok < 0 {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "prices must be non-negative")
		return
	}

	var m modelResponse
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		var providerType string
		if err := tx.QueryRow(r.Context(),
			"SELECT provider_type FROM model_providers WHERE id = $1 AND organization_id = $2",
			req.ProviderID, orgID).Scan(&providerType); err != nil {
			return err
		}
		if req.DisplayName == "" {
			req.DisplayName = req.ModelID
		}
		err := tx.QueryRow(r.Context(), `
			INSERT INTO models (organization_id, provider_id, model_id, display_name, capabilities,
				input_price_per_mtok_minor, output_price_per_mtok_minor)
			VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7)
			RETURNING id, provider_id, $8, model_id, display_name, capabilities,
				input_price_per_mtok_minor, output_price_per_mtok_minor, pricing_version, status, created_at::text`,
			orgID, req.ProviderID, req.ModelID, req.DisplayName, string(req.Capabilities),
			req.InputPricePerMTok, req.OutputPricePerMTok, providerType,
		).Scan(&m.ID, &m.ProviderID, &m.Provider, &m.ModelID, &m.DisplayName, &m.Capabilities,
			&m.InputPricePerMTok, &m.OutputPricePerMTok, &m.PricingVersion, &m.Status, &m.CreatedAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "model.created", ResourceType: "model", ResourceID: m.ID,
			Result: "success", Metadata: map[string]any{"model_id": req.ModelID},
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, r, http.StatusNotFound, "not_found", "provider not found")
	case err != nil:
		if isUniqueViolation(err) {
			httpx.Error(w, r, http.StatusConflict, "model_exists", "this model is already registered for the provider")
			return
		}
		h.Logger.Error("model create failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to register model")
	default:
		httpx.JSON(w, r, http.StatusCreated, m)
	}
}

// ListModels returns the organization's model registry.
func (h *Handler) ListModels(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	var models []modelResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT m.id, m.provider_id, p.provider_type, m.model_id, m.display_name, m.capabilities,
			       m.input_price_per_mtok_minor, m.output_price_per_mtok_minor, m.pricing_version,
			       m.status, m.created_at::text
			FROM models m
			JOIN model_providers p ON p.id = m.provider_id
			WHERE m.organization_id = $1
			ORDER BY p.provider_type, m.model_id`, orgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		models = []modelResponse{}
		for rows.Next() {
			var m modelResponse
			if err := rows.Scan(&m.ID, &m.ProviderID, &m.Provider, &m.ModelID, &m.DisplayName, &m.Capabilities,
				&m.InputPricePerMTok, &m.OutputPricePerMTok, &m.PricingVersion, &m.Status, &m.CreatedAt); err != nil {
				return err
			}
			models = append(models, m)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("model list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list models")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": models})
}

// isUniqueViolation reports PostgreSQL unique constraint violations.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
