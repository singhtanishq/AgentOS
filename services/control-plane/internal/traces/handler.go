// Package traces implements the observability query surface: trace lists
// and the span timeline for a single execution.
package traces

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/auth"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// Handler implements trace endpoints.
type Handler struct {
	Store  *store.Store
	Logger *slog.Logger
}

type traceSummary struct {
	ID          string  `json:"id"`
	ExecutionID string  `json:"execution_id"`
	AgentID     string  `json:"agent_id"`
	Status      string  `json:"status"`
	DurationMs  *int    `json:"duration_ms"`
	StartedAt   string  `json:"started_at"`
	EndedAt     *string `json:"ended_at"`
}

type spanResponse struct {
	ID           string          `json:"id"`
	ParentSpanID string          `json:"parent_span_id,omitempty"`
	Name         string          `json:"name"`
	Kind         string          `json:"kind"`
	Status       string          `json:"status"`
	DurationMs   *int            `json:"duration_ms"`
	StartedAt    string          `json:"started_at"`
	EndedAt      *string         `json:"ended_at"`
	Attributes   json.RawMessage `json:"attributes"`
}

type traceDetail struct {
	Trace traceSummary   `json:"trace"`
	Spans []spanResponse `json:"spans"`
}

// List returns recent traces for the organization with execution context.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n := parseInt(raw); n > 0 && n <= 200 {
			limit = n
		}
	}

	var items []traceSummary
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT t.id, t.execution_id, e.agent_id, t.status, t.duration_ms,
			       t.started_at::text, t.ended_at::text
			FROM traces t
			JOIN executions e ON e.id = t.execution_id
			WHERE t.organization_id = $1
			ORDER BY t.started_at DESC LIMIT $2`, orgID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []traceSummary{}
		for rows.Next() {
			var t traceSummary
			var ended *string
			if err := rows.Scan(&t.ID, &t.ExecutionID, &t.AgentID, &t.Status,
				&t.DurationMs, &t.StartedAt, &ended); err != nil {
				return err
			}
			t.EndedAt = ended
			items = append(items, t)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("trace list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list traces")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// Get returns one trace with its ordered span timeline.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	traceID := chi.URLParam(r, "traceId")

	var detail traceDetail
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		var ended *string
		err := tx.QueryRow(r.Context(), `
			SELECT t.id, t.execution_id, e.agent_id, t.status, t.duration_ms,
			       t.started_at::text, t.ended_at::text
			FROM traces t
			JOIN executions e ON e.id = t.execution_id
			WHERE t.id = $1`, traceID,
		).Scan(&detail.Trace.ID, &detail.Trace.ExecutionID, &detail.Trace.AgentID,
			&detail.Trace.Status, &detail.Trace.DurationMs, &detail.Trace.StartedAt, &ended)
		if err != nil {
			return err
		}
		detail.Trace.EndedAt = ended

		rows, err := tx.Query(r.Context(), `
			SELECT id, COALESCE(parent_span_id::text, ''), name, kind, status,
			       duration_ms, started_at::text, ended_at::text, attributes
			FROM spans WHERE trace_id = $1
			ORDER BY started_at, name`, traceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		detail.Spans = []spanResponse{}
		for rows.Next() {
			var s spanResponse
			var ended *string
			if err := rows.Scan(&s.ID, &s.ParentSpanID, &s.Name, &s.Kind, &s.Status,
				&s.DurationMs, &s.StartedAt, &ended, &s.Attributes); err != nil {
				return err
			}
			s.EndedAt = ended
			detail.Spans = append(detail.Spans, s)
		}
		return rows.Err()
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "trace not found")
		return
	}
	if err != nil {
		h.Logger.Error("trace get failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to load trace")
		return
	}
	httpx.JSON(w, r, http.StatusOK, detail)
}

func parseInt(raw string) int {
	n := 0
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
		if n > 1_000_000 {
			return 0
		}
	}
	return n
}
