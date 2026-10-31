// Package budgets implements budget management, usage analytics and
// operational alerts.
package budgets

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/audit"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/auth"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// Handler implements budget, usage and alert endpoints.
type Handler struct {
	Store  *store.Store
	Logger *slog.Logger
}

type budgetResponse struct {
	ID                string `json:"id"`
	AgentID           string `json:"agent_id,omitempty"`
	Name              string `json:"name"`
	Period            string `json:"period"`
	LimitMinor        int64  `json:"limit_minor"`
	SpentMinor        int64  `json:"spent_minor"`
	Currency          string `json:"currency"`
	EnforcementAction string `json:"enforcement_action"`
	Status            string `json:"status"`
	PeriodStartedAt   string `json:"period_started_at"`
	CreatedAt         string `json:"created_at"`
}

// Create adds a budget for the organization or a single agent.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())

	var req struct {
		AgentID           string `json:"agent_id"`
		Name              string `json:"name"`
		Period            string `json:"period"`
		LimitMinor        int64  `json:"limit_minor"`
		EnforcementAction string `json:"enforcement_action"`
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
	switch req.Period {
	case "daily", "weekly", "monthly", "total":
	default:
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"period must be daily, weekly, monthly or total")
		return
	}
	if req.LimitMinor <= 0 {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"limit_minor must be positive")
		return
	}
	if req.EnforcementAction == "" {
		req.EnforcementAction = "alert_only"
	}
	if req.EnforcementAction != "alert_only" && req.EnforcementAction != "block" {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"enforcement_action must be alert_only or block")
		return
	}

	var b budgetResponse
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		if req.AgentID != "" {
			var exists bool
			if err := tx.QueryRow(r.Context(),
				"SELECT EXISTS (SELECT 1 FROM agents WHERE id = $1 AND deleted_at IS NULL)",
				req.AgentID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return pgx.ErrNoRows
			}
		}
		err := tx.QueryRow(r.Context(), `
			INSERT INTO budgets (organization_id, agent_id, name, period, limit_minor, enforcement_action, created_by)
			VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7)
			RETURNING id, COALESCE(agent_id::text, ''), name, period, limit_minor,
				spent_minor, currency, enforcement_action, status, period_started_at::text, created_at::text`,
			orgID, req.AgentID, req.Name, req.Period, req.LimitMinor,
			req.EnforcementAction, actorID,
		).Scan(&b.ID, &b.AgentID, &b.Name, &b.Period, &b.LimitMinor,
			&b.SpentMinor, &b.Currency, &b.EnforcementAction, &b.Status,
			&b.PeriodStartedAt, &b.CreatedAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "budget.created", ResourceType: "budget", ResourceID: b.ID,
			Result: "success",
			Metadata: map[string]any{
				"period": req.Period, "limit_minor": req.LimitMinor,
				"enforcement": req.EnforcementAction,
			},
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, r, http.StatusNotFound, "not_found", "agent not found")
	case err != nil:
		h.Logger.Error("budget create failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to create budget")
	default:
		httpx.JSON(w, r, http.StatusCreated, b)
	}
}

// List returns the organization's budgets with live period spend.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	var items []budgetResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT b.id, COALESCE(b.agent_id::text, ''), b.name, b.period, b.limit_minor,
			       b.spent_minor, b.currency, b.enforcement_action, b.status,
			       b.period_started_at::text, b.created_at::text
			FROM budgets b
			WHERE b.organization_id = $1
			ORDER BY b.created_at`, orgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []budgetResponse{}
		for rows.Next() {
			var b budgetResponse
			if err := rows.Scan(&b.ID, &b.AgentID, &b.Name, &b.Period, &b.LimitMinor,
				&b.SpentMinor, &b.Currency, &b.EnforcementAction, &b.Status,
				&b.PeriodStartedAt, &b.CreatedAt); err != nil {
				return err
			}
			items = append(items, b)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("budget list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list budgets")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// Update changes limit, enforcement or status.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	budgetID := chi.URLParam(r, "budgetId")

	var req struct {
		LimitMinor        *int64  `json:"limit_minor"`
		EnforcementAction *string `json:"enforcement_action"`
		Status            *string `json:"status"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	if req.LimitMinor != nil && *req.LimitMinor <= 0 {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"limit_minor must be positive")
		return
	}
	if req.EnforcementAction != nil {
		s := *req.EnforcementAction
		if s != "alert_only" && s != "block" {
			httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
				"enforcement_action must be alert_only or block")
			return
		}
	}
	if req.Status != nil {
		s := strings.ToLower(*req.Status)
		if s != "active" && s != "disabled" {
			httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
				"status must be active or disabled")
			return
		}
		req.Status = &s
	}

	var b budgetResponse
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		err := tx.QueryRow(r.Context(), `
			UPDATE budgets SET
				limit_minor = COALESCE($3, limit_minor),
				enforcement_action = COALESCE($4, enforcement_action),
				status = COALESCE($5, status)
			WHERE id = $1 AND organization_id = $2
			RETURNING id, COALESCE(agent_id::text, ''), name, period, limit_minor,
				spent_minor, currency, enforcement_action, status,
				period_started_at::text, created_at::text`,
			budgetID, orgID, req.LimitMinor, req.EnforcementAction, req.Status,
		).Scan(&b.ID, &b.AgentID, &b.Name, &b.Period, &b.LimitMinor,
			&b.SpentMinor, &b.Currency, &b.EnforcementAction, &b.Status,
			&b.PeriodStartedAt, &b.CreatedAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "budget.updated", ResourceType: "budget", ResourceID: budgetID,
			Result: "success",
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "budget not found")
		return
	}
	if err != nil {
		h.Logger.Error("budget update failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to update budget")
		return
	}
	httpx.JSON(w, r, http.StatusOK, b)
}

// Delete removes a budget.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	budgetID := chi.URLParam(r, "budgetId")

	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		tag, err := tx.Exec(r.Context(),
			"DELETE FROM budgets WHERE id = $1 AND organization_id = $2", budgetID, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "budget.deleted", ResourceType: "budget", ResourceID: budgetID,
			Result: "success",
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "budget not found")
		return
	}
	if err != nil {
		h.Logger.Error("budget delete failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to delete budget")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]string{"status": "deleted"})
}

// UsageSummary aggregates token and cost analytics for the organization.
func (h *Handler) UsageSummary(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}

	type byDay struct {
		Day               string `json:"day"`
		InputTokens       int64  `json:"input_tokens"`
		OutputTokens      int64  `json:"output_tokens"`
		CostEstimateMinor int64  `json:"cost_estimate_minor"`
	}
	type byAgent struct {
		AgentID           string `json:"agent_id"`
		Executions        int64  `json:"executions"`
		TotalTokens       int64  `json:"total_tokens"`
		CostEstimateMinor int64  `json:"cost_estimate_minor"`
	}
	out := map[string]any{}
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT date_trunc('day', created_at)::date::text AS day,
			       SUM(input_tokens), SUM(output_tokens), SUM(cost_estimate_minor)
			FROM usage_records
			WHERE organization_id = $1 AND created_at >= now() - make_interval(days => $2)
			GROUP BY 1 ORDER BY 1`, orgID, days)
		if err != nil {
			return err
		}
		days2 := []byDay{}
		for rows.Next() {
			var d byDay
			if err := rows.Scan(&d.Day, &d.InputTokens, &d.OutputTokens, &d.CostEstimateMinor); err != nil {
				return err
			}
			days2 = append(days2, d)
		}
		rows.Close()
		out["by_day"] = days2

		rows, err = tx.Query(r.Context(), `
			SELECT agent_id, COUNT(*), SUM(total_tokens), SUM(cost_estimate_minor)
			FROM usage_records
			WHERE organization_id = $1 AND created_at >= now() - make_interval(days => $2)
			GROUP BY agent_id ORDER BY SUM(cost_estimate_minor) DESC`, orgID, days)
		if err != nil {
			return err
		}
		defer rows.Close()
		agents := []byAgent{}
		for rows.Next() {
			var a byAgent
			if err := rows.Scan(&a.AgentID, &a.Executions, &a.TotalTokens, &a.CostEstimateMinor); err != nil {
				return err
			}
			agents = append(agents, a)
		}
		out["by_agent"] = agents
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("usage summary failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to summarize usage")
		return
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

type alertResponse struct {
	ID        string  `json:"id"`
	Type      string  `json:"type"`
	Severity  string  `json:"severity"`
	Title     string  `json:"title"`
	Message   string  `json:"message"`
	CreatedAt string  `json:"created_at"`
	AckAt     *string `json:"acknowledged_at,omitempty"`
}

// Alerts returns operational alerts (open by default).
func (h *Handler) Alerts(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	open := r.URL.Query().Get("open") != "false"
	where := " WHERE organization_id = $1"
	if open {
		where += " AND acknowledged_at IS NULL"
	}
	var items []alertResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT id, type, severity, title, message, created_at::text, acknowledged_at::text
			FROM alerts`+where+` ORDER BY created_at DESC LIMIT 100`, orgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []alertResponse{}
		for rows.Next() {
			var a alertResponse
			if err := rows.Scan(&a.ID, &a.Type, &a.Severity, &a.Title, &a.Message,
				&a.CreatedAt, &a.AckAt); err != nil {
				return err
			}
			items = append(items, a)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("alerts list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list alerts")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// AckAlert marks an alert acknowledged.
func (h *Handler) AckAlert(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	alertID := chi.URLParam(r, "alertId")

	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		tag, err := tx.Exec(r.Context(), `
			UPDATE alerts SET acknowledged_at = now(), acknowledged_by = $3
			WHERE id = $1 AND organization_id = $2 AND acknowledged_at IS NULL`,
			alertID, orgID, actorID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "alert.acknowledged", ResourceType: "alert", ResourceID: alertID,
			Result: "success",
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "alert not found or already acknowledged")
		return
	}
	if err != nil {
		h.Logger.Error("alert ack failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to acknowledge alert")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]string{"status": "acknowledged"})
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
