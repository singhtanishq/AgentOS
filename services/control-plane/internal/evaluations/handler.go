// Package evaluations implements evaluation datasets, test cases and run
// triggering. Runs execute in the agent runtime over the internal
// service surface.
package evaluations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/audit"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/auth"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// Handler implements evaluation endpoints.
type Handler struct {
	Store   *store.Store
	Runtime *RuntimeClient
	Logger  *slog.Logger
}

// RuntimeClient calls the agent runtime's internal evaluation surface.
type RuntimeClient struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

// TriggerRun asks the runtime to execute a dataset against a version.
func (c *RuntimeClient) TriggerRun(
	ctx context.Context, orgID, datasetID, agentID, agentVersionID string,
) (map[string]any, error) {
	body, err := json.Marshal(map[string]string{
		"organization_id":  orgID,
		"dataset_id":       datasetID,
		"agent_id":         agentID,
		"agent_version_id": agentVersionID,
	})
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(c.BaseURL, "/") + "/internal/evaluations/run"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("runtime evaluation failed (%d): %s", resp.StatusCode, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type datasetResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CaseCount   int    `json:"case_count"`
	CreatedAt   string `json:"created_at"`
}

type caseResponse struct {
	ID        string          `json:"id"`
	DatasetID string          `json:"dataset_id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Expected  json.RawMessage `json:"expected"`
	Rules     json.RawMessage `json:"rules"`
	Position  int             `json:"position"`
}

type runResponse struct {
	ID             string   `json:"id"`
	DatasetID      string   `json:"dataset_id"`
	AgentID        string   `json:"agent_id"`
	AgentVersionID string   `json:"agent_version_id"`
	Status         string   `json:"status"`
	PassRate       *float64 `json:"pass_rate"`
	TotalCases     int      `json:"total_cases"`
	PassedCases    int      `json:"passed_cases"`
	StartedAt      string   `json:"started_at"`
	CompletedAt    *string  `json:"completed_at,omitempty"`
}

// CreateDataset adds an evaluation dataset.
func (h *Handler) CreateDataset(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
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

	var d datasetResponse
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		err := tx.QueryRow(r.Context(), `
			INSERT INTO evaluation_datasets (organization_id, name, description, created_by)
			VALUES ($1, $2, $3, $4)
			RETURNING id, name, description, 0, created_at::text`,
			orgID, req.Name, req.Description, actorID,
		).Scan(&d.ID, &d.Name, &d.Description, &d.CaseCount, &d.CreatedAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "evaluation_dataset.created", ResourceType: "evaluation_dataset", ResourceID: d.ID,
			Result: "success",
		})
	})
	if err != nil {
		if isUniqueViolation(err) {
			httpx.Error(w, r, http.StatusConflict, "name_taken", "a dataset with this name exists")
			return
		}
		h.Logger.Error("dataset create failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to create dataset")
		return
	}
	httpx.JSON(w, r, http.StatusCreated, d)
}

// ListDatasets returns datasets with case counts.
func (h *Handler) ListDatasets(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	var items []datasetResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT d.id, d.name, d.description,
			       (SELECT COUNT(*) FROM evaluation_cases c WHERE c.dataset_id = d.id),
			       d.created_at::text
			FROM evaluation_datasets d
			WHERE d.organization_id = $1 AND d.deleted_at IS NULL
			ORDER BY d.created_at`, orgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []datasetResponse{}
		for rows.Next() {
			var d datasetResponse
			if err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.CaseCount, &d.CreatedAt); err != nil {
				return err
			}
			items = append(items, d)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("dataset list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list datasets")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// AddCase appends a test case to a dataset.
func (h *Handler) AddCase(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	datasetID := chi.URLParam(r, "datasetId")

	var req struct {
		Name     string          `json:"name"`
		Input    json.RawMessage `json:"input"`
		Expected json.RawMessage `json:"expected"`
		Rules    json.RawMessage `json:"rules"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "name is required")
		return
	}
	var inputCheck any
	if err := json.Unmarshal(req.Input, &inputCheck); err != nil || inputCheck == nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "input must be a JSON object")
		return
	}
	if len(req.Rules) == 0 {
		req.Rules = json.RawMessage("[]")
	}
	if len(req.Expected) == 0 {
		req.Expected = json.RawMessage("{}")
	}

	var c caseResponse
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		var exists bool
		if err := tx.QueryRow(r.Context(),
			"SELECT EXISTS (SELECT 1 FROM evaluation_datasets WHERE id = $1 AND deleted_at IS NULL)",
			datasetID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return pgx.ErrNoRows
		}
		err := tx.QueryRow(r.Context(), `
			INSERT INTO evaluation_cases (organization_id, dataset_id, name, input, expected, rules, position)
			VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, $6::jsonb,
				(SELECT COALESCE(MAX(position), 0) + 1 FROM evaluation_cases WHERE dataset_id = $2))
			RETURNING id, dataset_id, name, input, expected, rules, position`,
			orgID, datasetID, req.Name, string(req.Input), string(req.Expected), string(req.Rules),
		).Scan(&c.ID, &c.DatasetID, &c.Name, &c.Input, &c.Expected, &c.Rules, &c.Position)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "evaluation_case.created", ResourceType: "evaluation_case", ResourceID: c.ID,
			Result: "success",
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, r, http.StatusNotFound, "not_found", "dataset not found")
	case err != nil:
		h.Logger.Error("case create failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to add case")
	default:
		httpx.JSON(w, r, http.StatusCreated, c)
	}
}

// ListCases returns a dataset's cases.
func (h *Handler) ListCases(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	datasetID := chi.URLParam(r, "datasetId")
	var items []caseResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT id, dataset_id, name, input, expected, rules, position
			FROM evaluation_cases WHERE dataset_id = $1 AND organization_id = $2
			ORDER BY position, created_at`, datasetID, orgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []caseResponse{}
		for rows.Next() {
			var c caseResponse
			if err := rows.Scan(&c.ID, &c.DatasetID, &c.Name, &c.Input, &c.Expected,
				&c.Rules, &c.Position); err != nil {
				return err
			}
			items = append(items, c)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("case list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list cases")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// ListRuns returns runs for a dataset.
func (h *Handler) ListRuns(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	datasetID := chi.URLParam(r, "datasetId")
	var items []runResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT id, dataset_id, agent_id, agent_version_id, status, pass_rate,
			       total_cases, passed_cases, started_at::text, completed_at::text
			FROM evaluation_runs WHERE dataset_id = $1 AND organization_id = $2
			ORDER BY started_at DESC LIMIT 50`, datasetID, orgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []runResponse{}
		for rows.Next() {
			var run runResponse
			var completed *string
			if err := rows.Scan(&run.ID, &run.DatasetID, &run.AgentID, &run.AgentVersionID,
				&run.Status, &run.PassRate, &run.TotalCases, &run.PassedCases,
				&run.StartedAt, &completed); err != nil {
				return err
			}
			run.CompletedAt = completed
			items = append(items, run)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("run list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list runs")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// TriggerRun executes a dataset against a published agent version in the
// runtime and returns the run summary.
func (h *Handler) TriggerRun(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	datasetID := chi.URLParam(r, "datasetId")

	var req struct {
		AgentID        string `json:"agent_id"`
		AgentVersionID string `json:"agent_version_id"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}

	// Validate ownership + published status before calling the runtime.
	var vStatus string
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		var vOrg, vAgent string
		if err := tx.QueryRow(r.Context(), `
			SELECT organization_id, agent_id, status FROM agent_versions WHERE id = $1`,
			req.AgentVersionID).Scan(&vOrg, &vAgent, &vStatus); err != nil {
			return err
		}
		if vOrg != orgID || vAgent != req.AgentID {
			return pgx.ErrNoRows
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "dataset or version not found")
		return
	}
	if err != nil {
		h.Logger.Error("run validation failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to start run")
		return
	}
	if vStatus != "published" {
		httpx.Error(w, r, http.StatusConflict, "version_not_published",
			"evaluations run against published versions")
		return
	}

	summary, err := h.Runtime.TriggerRun(r.Context(), orgID, datasetID, req.AgentID, req.AgentVersionID)
	if err != nil {
		h.Logger.Error("evaluation run failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusBadGateway, "runtime_unavailable", err.Error())
		return
	}

	_ = h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "evaluation_run.triggered", ResourceType: "evaluation_dataset", ResourceID: datasetID,
			Result: "success",
		})
	})
	httpx.JSON(w, r, http.StatusOK, summary)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
