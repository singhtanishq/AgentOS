// Package webhooks implements webhook management and signed event
// delivery with retry state.
package webhooks

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/audit"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/auth"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/secrets"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// Handler implements webhook endpoints.
type Handler struct {
	Store   *store.Store
	Secrets *secrets.Manager
	Logger  *slog.Logger
}

type webhookResponse struct {
	ID             string   `json:"id"`
	URL            string   `json:"url"`
	Description    string   `json:"description"`
	Events         []string `json:"events"`
	Status         string   `json:"status"`
	CreatedAt      string   `json:"created_at"`
	LastDeliveryAt *string  `json:"last_delivery_at,omitempty"`
}

// Create registers a webhook with a generated signing secret.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())

	var req struct {
		URL         string   `json:"url"`
		Description string   `json:"description"`
		Events      []string `json:"events"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"url must start with http(s)://")
		return
	}
	if len(req.Events) == 0 {
		req.Events = []string{"execution.completed", "execution.failed", "approval.requested"}
	}

	secret := "whsec_" + httpx.NewRequestID() + httpx.NewRequestID()
	envelope, err := h.Secrets.Encrypt([]byte(secret))
	if err != nil {
		h.Logger.Error("webhook secret encryption failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to store secret")
		return
	}
	nonce, ciphertext := envelope[1:13], envelope[13:]

	var wh webhookResponse
	err = h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		err := tx.QueryRow(r.Context(), `
			INSERT INTO webhooks (organization_id, url, description, events, secret_encrypted, secret_nonce, key_version, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id, url, description, events, status, created_at::text, last_delivery_at::text`,
			orgID, req.URL, req.Description, req.Events, ciphertext, nonce,
			int16(h.Secrets.ActiveKeyVersion()), actorID,
		).Scan(&wh.ID, &wh.URL, &wh.Description, &wh.Events, &wh.Status, &wh.CreatedAt, &wh.LastDeliveryAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "webhook.created", ResourceType: "webhook", ResourceID: wh.ID,
			Result: "success", Metadata: map[string]any{"url": req.URL},
		})
	})
	if err != nil {
		h.Logger.Error("webhook create failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to create webhook")
		return
	}
	httpx.JSON(w, r, http.StatusCreated, map[string]any{
		"webhook": wh,
		"secret":  secret,
		"note":    "use this signing secret to verify X-AgentOS-Signature headers",
	})
}

// List returns the organization's webhooks (no secrets).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	var items []webhookResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT id, url, description, events, status, created_at::text, last_delivery_at::text
			FROM webhooks WHERE organization_id = $1
			ORDER BY created_at DESC`, orgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []webhookResponse{}
		for rows.Next() {
			var wh webhookResponse
			if err := rows.Scan(&wh.ID, &wh.URL, &wh.Description, &wh.Events,
				&wh.Status, &wh.CreatedAt, &wh.LastDeliveryAt); err != nil {
				return err
			}
			items = append(items, wh)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("webhook list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list webhooks")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// Delete removes a webhook.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	webhookID := chi.URLParam(r, "webhookId")

	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		tag, err := tx.Exec(r.Context(),
			"DELETE FROM webhooks WHERE id = $1 AND organization_id = $2", webhookID, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "webhook.deleted", ResourceType: "webhook", ResourceID: webhookID,
			Result: "success",
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "webhook not found")
		return
	}
	if err != nil {
		h.Logger.Error("webhook delete failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to delete webhook")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]string{"status": "deleted"})
}

// Dispatcher consumes platform events and delivers signed webhooks with
// bounded retries. One delivery attempt happens per invocation; the
// caller schedules the next attempt via next_retry_at.
type Dispatcher struct {
	Store   *store.Store
	Secrets *secrets.Manager
	Logger  *slog.Logger
	Client  *http.Client
}

// MaxAttempts bounds delivery tries before exhaustion.
const MaxAttempts = 5

// Backoff returns the delay before the next attempt (attempt is 1-based).
func Backoff(attempt int) time.Duration {
	switch {
	case attempt >= 4:
		return 15 * time.Minute
	case attempt == 3:
		return 5 * time.Minute
	case attempt == 2:
		return 1 * time.Minute
	default:
		return 15 * time.Second
	}
}

// DeliverEvent dispatches one platform event to every matching webhook of
// its organization. Errors are recorded as retryable deliveries.
func (d *Dispatcher) DeliverEvent(
	ctx context.Context, orgID, eventID, eventType string, payload []byte,
) error {
	rows, err := d.Store.Pool.Query(ctx, `
		SELECT id, url, secret_encrypted, secret_nonce, key_version
		FROM webhooks
		WHERE organization_id = $1 AND status = 'active' AND $2 = ANY(events)`,
		orgID, eventType)
	if err != nil {
		return fmt.Errorf("list webhooks: %w", err)
	}
	type target struct {
		id, url string
		secret  []byte
		nonce   []byte
		version int16
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.url, &t.secret, &t.nonce, &t.version); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, t := range targets {
		plaintext, err := d.Secrets.DecryptParts(byte(t.version), t.nonce, t.secret)
		if err != nil {
			d.Logger.Error("webhook secret decryption failed",
				slog.Any("error", err), slog.String("webhook_id", t.id))
			continue
		}
		timestamp := time.Now().Unix()
		body := payload
		signature := secrets.HMACSign(plaintext,
			[]byte(fmt.Sprintf("%d.%s", timestamp, string(body))))
		go func(t target) {
			attemptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()
			d.deliver(attemptCtx, t.id, eventID, t.url, eventType, body, timestamp,
				hex.EncodeToString(signature))
		}(t)
	}
	return nil
}

func (d *Dispatcher) deliver(
	ctx context.Context, webhookID, eventID, url, eventType string,
	body []byte, ts int64, signature string,
) {
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		d.recordFailure(ctx, webhookID, eventID, eventType, 0, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AgentOS-Event", eventType)
	req.Header.Set("X-AgentOS-Timestamp", fmt.Sprint(ts))
	req.Header.Set("X-AgentOS-Signature", "v1="+signature)

	resp, err := client.Do(req)
	if err != nil {
		d.recordFailure(ctx, webhookID, eventID, eventType, 0, err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_, _ = d.Store.Pool.Exec(ctx, `
			INSERT INTO webhook_deliveries
				(webhook_id, organization_id, event_type, event_id, status, attempt, response_status, delivered_at)
			VALUES ($1, (SELECT organization_id FROM webhooks WHERE id = $1), $2, $3,
			        'delivered', 1, $4, now())`,
			webhookID, eventType, eventID, resp.StatusCode)
		_, _ = d.Store.Pool.Exec(ctx,
			"UPDATE webhooks SET last_delivery_at = now() WHERE id = $1", webhookID)
		return
	}
	d.recordFailure(ctx, webhookID, eventID, eventType, resp.StatusCode,
		fmt.Sprintf("status %d", resp.StatusCode))
}

func (d *Dispatcher) recordFailure(
	ctx context.Context, webhookID, eventID, eventType string, status int, message string,
) {
	attempt := 1
	var prior int
	err := d.Store.Pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(attempt), 0) FROM webhook_deliveries
		WHERE webhook_id = $1 AND event_type = $2 AND event_id = $3`,
		webhookID, eventType, eventID).Scan(&prior)
	if err == nil {
		attempt = prior + 1
	}
	status2 := "failed"
	var nextRetry any
	if attempt < MaxAttempts {
		status2 = "pending"
		nextRetry = time.Now().Add(Backoff(attempt))
	} else {
		status2 = "exhausted"
	}
	_, _ = d.Store.Pool.Exec(ctx, `
		INSERT INTO webhook_deliveries
			(webhook_id, organization_id, event_type, event_id, status, attempt,
			 response_status, response_body, next_retry_at)
		VALUES ($1, (SELECT organization_id FROM webhooks WHERE id = $1), $2, $3,
		        $4, $5, NULLIF($6, 0)::int, $7, $8)`,
		webhookID, eventType, eventID, status2, attempt, status, message, nextRetry)
	d.Logger.Warn("webhook delivery failed",
		slog.String("webhook_id", webhookID),
		slog.String("event", eventType),
		slog.String("status", status2),
		slog.Int("attempt", attempt),
	)
}
