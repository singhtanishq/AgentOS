package agents

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/audit"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/auth"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// Handler implements agent registry endpoints.
type Handler struct {
	Store  *store.Store
	Logger *slog.Logger
}

type agentResponse struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Status        string `json:"status"`
	LatestVersion int    `json:"latest_version"`
	VersionCount  int    `json:"version_count"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type versionResponse struct {
	ID          string          `json:"id"`
	Version     int             `json:"version"`
	Status      string          `json:"status"`
	Changelog   string          `json:"changelog"`
	Config      json.RawMessage `json:"configuration"`
	PublishedAt *string         `json:"published_at"`
	CreatedAt   string          `json:"created_at"`
}

// Create registers a new agent in draft status with an initial version.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())

	var req struct {
		Name          string          `json:"name"`
		Description   string          `json:"description"`
		Configuration json.RawMessage `json:"configuration"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if len(req.Name) < 2 || len(req.Name) > 120 {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"agent name must be between 2 and 120 characters")
		return
	}
	if len(req.Description) > 2000 {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"agent description must be at most 2000 characters")
		return
	}
	cfg, err := ParseConfiguration(req.Configuration)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	cfgJSON, _ := jsonMarshal(cfg)

	var a agentResponse
	err = h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		err := tx.QueryRow(r.Context(), `
			INSERT INTO agents (organization_id, name, description, status, created_by)
			VALUES ($1, $2, $3, 'draft', $4)
			RETURNING id, name, description, status,
				1 AS latest_version, 1 AS version_count, created_at::text, updated_at::text`,
			orgID, req.Name, req.Description, actorID,
		).Scan(&a.ID, &a.Name, &a.Description, &a.Status, &a.LatestVersion, &a.VersionCount, &a.CreatedAt, &a.UpdatedAt)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO agent_versions (organization_id, agent_id, version, configuration, changelog, created_by)
			VALUES ($1, $2, 1, $3::jsonb, 'initial version', $4)`,
			orgID, a.ID, string(cfgJSON), actorID); err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "agent.created", ResourceType: "agent", ResourceID: a.ID,
			Result: "success", Metadata: map[string]any{"name": req.Name},
		})
	})
	if err != nil {
		if isUniqueViolation(err) {
			httpx.Error(w, r, http.StatusConflict, "name_taken",
				"an agent with this name already exists in the organization")
			return
		}
		h.Logger.Error("agent create failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to create agent")
		return
	}
	httpx.JSON(w, r, http.StatusCreated, a)
}

// List returns the organization's agents with version summaries.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))

	pattern := "%"
	args := []any{orgID, pattern}
	if search != "" {
		pattern = "%" + search + "%"
		args[1] = pattern
	}
	statusClause := ""
	if statusFilter != "" {
		statusClause = " AND a.status = $3"
		args = append(args, statusFilter)
	}

	var agents []agentResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT a.id, a.name, a.description, a.status,
			       COALESCE(MAX(v.version), 0) AS latest_version,
			       COUNT(v.id) AS version_count,
			       a.created_at::text, a.updated_at::text
			FROM agents a
			LEFT JOIN agent_versions v ON v.agent_id = a.id
			WHERE a.organization_id = $1 AND a.deleted_at IS NULL AND a.name ILIKE $2`+statusClause+`
			GROUP BY a.id
			ORDER BY a.created_at DESC`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		agents = []agentResponse{}
		for rows.Next() {
			var a agentResponse
			if err := rows.Scan(&a.ID, &a.Name, &a.Description, &a.Status,
				&a.LatestVersion, &a.VersionCount, &a.CreatedAt, &a.UpdatedAt); err != nil {
				return err
			}
			agents = append(agents, a)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("agent list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list agents")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": agents})
}

// Get returns one agent with its version list.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	agentID := chi.URLParam(r, "agentId")

	var a agentResponse
	var versions []versionResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		err := tx.QueryRow(r.Context(), `
			SELECT a.id, a.name, a.description, a.status,
			       COALESCE(MAX(v.version), 0), COUNT(v.id),
			       a.created_at::text, a.updated_at::text
			FROM agents a
			LEFT JOIN agent_versions v ON v.agent_id = a.id
			WHERE a.id = $1 AND a.organization_id = $2 AND a.deleted_at IS NULL
			GROUP BY a.id`, agentID, orgID,
		).Scan(&a.ID, &a.Name, &a.Description, &a.Status,
			&a.LatestVersion, &a.VersionCount, &a.CreatedAt, &a.UpdatedAt)
		if err != nil {
			return err
		}
		vrows, err := tx.Query(r.Context(), `
			SELECT id, version, status, changelog, configuration, published_at::text, created_at::text
			FROM agent_versions
			WHERE agent_id = $1
			ORDER BY version DESC`, agentID)
		if err != nil {
			return err
		}
		defer vrows.Close()
		versions = []versionResponse{}
		for vrows.Next() {
			var v versionResponse
			var published *string
			if err := vrows.Scan(&v.ID, &v.Version, &v.Status, &v.Changelog, &v.Config, &published, &v.CreatedAt); err != nil {
				return err
			}
			v.PublishedAt = published
			versions = append(versions, v)
		}
		return vrows.Err()
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "agent not found")
		return
	}
	if err != nil {
		h.Logger.Error("agent get failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to load agent")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"agent": a, "versions": versions})
}

// Update modifies mutable agent fields (name, description) — never the
// published configuration, which lives in versions.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	agentID := chi.URLParam(r, "agentId")

	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	if req.Name != nil {
		*req.Name = strings.TrimSpace(*req.Name)
		if len(*req.Name) < 2 || len(*req.Name) > 120 {
			httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
				"agent name must be between 2 and 120 characters")
			return
		}
	}
	if req.Description != nil && len(*req.Description) > 2000 {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"agent description must be at most 2000 characters")
		return
	}

	var a agentResponse
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		err := tx.QueryRow(r.Context(), `
			UPDATE agents SET
				name = COALESCE($3, name),
				description = COALESCE($4, description)
			WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
			RETURNING id, name, description, status,
				COALESCE((SELECT MAX(version) FROM agent_versions WHERE agent_id = agents.id), 0),
				COALESCE((SELECT COUNT(*) FROM agent_versions WHERE agent_id = agents.id), 0),
				created_at::text, updated_at::text`,
			agentID, orgID, req.Name, req.Description,
		).Scan(&a.ID, &a.Name, &a.Description, &a.Status, &a.LatestVersion, &a.VersionCount, &a.CreatedAt, &a.UpdatedAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "agent.updated", ResourceType: "agent", ResourceID: agentID, Result: "success",
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "agent not found")
		return
	}
	if err != nil {
		if isUniqueViolation(err) {
			httpx.Error(w, r, http.StatusConflict, "name_taken",
				"an agent with this name already exists in the organization")
			return
		}
		h.Logger.Error("agent update failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to update agent")
		return
	}
	httpx.JSON(w, r, http.StatusOK, a)
}

// validAgentTransitions is the agent status lifecycle state machine.
var validAgentTransitions = map[string][]string{
	"draft":     {"active", "archived"},
	"active":    {"paused", "suspended", "archived"},
	"paused":    {"active", "suspended", "archived"},
	"suspended": {"active", "archived"},
	"archived":  {}, // terminal
}

// UpdateStatus moves an agent through its lifecycle.
func (h *Handler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	agentID := chi.URLParam(r, "agentId")

	var req struct {
		Status string `json:"status"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	newStatus := strings.ToLower(strings.TrimSpace(req.Status))
	if _, ok := validAgentTransitions[newStatus]; !ok {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"status must be one of draft, active, paused, suspended, archived")
		return
	}

	var a agentResponse
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		var current string
		if err := tx.QueryRow(r.Context(),
			"SELECT status FROM agents WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL",
			agentID, orgID).Scan(&current); err != nil {
			return err
		}
		allowed := validAgentTransitions[current]
		ok := false
		for _, s := range allowed {
			if s == newStatus {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("cannot transition from %s to %s", current, newStatus)
		}
		err := tx.QueryRow(r.Context(), `
			UPDATE agents SET status = $3
			WHERE id = $1 AND organization_id = $2
			RETURNING id, name, description, status,
				COALESCE((SELECT MAX(version) FROM agent_versions WHERE agent_id = agents.id), 0),
				COALESCE((SELECT COUNT(*) FROM agent_versions WHERE agent_id = agents.id), 0),
				created_at::text, updated_at::text`,
			agentID, orgID, newStatus,
		).Scan(&a.ID, &a.Name, &a.Description, &a.Status, &a.LatestVersion, &a.VersionCount, &a.CreatedAt, &a.UpdatedAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "agent.status_changed", ResourceType: "agent", ResourceID: agentID,
			Result: "success", Metadata: map[string]any{"from": current, "to": newStatus},
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, r, http.StatusNotFound, "not_found", "agent not found")
	case err != nil && strings.Contains(err.Error(), "cannot transition"):
		httpx.Error(w, r, http.StatusConflict, "invalid_transition", err.Error())
	case err != nil:
		h.Logger.Error("agent status update failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to update agent status")
	default:
		httpx.JSON(w, r, http.StatusOK, a)
	}
}

// Delete soft-deletes an agent.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	agentID := chi.URLParam(r, "agentId")

	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		tag, err := tx.Exec(r.Context(), `
			UPDATE agents SET deleted_at = now()
			WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`, agentID, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "agent.deleted", ResourceType: "agent", ResourceID: agentID, Result: "success",
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "agent not found")
		return
	}
	if err != nil {
		h.Logger.Error("agent delete failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to delete agent")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]string{"status": "deleted"})
}

// CreateVersion adds a new draft version derived from the latest version.
func (h *Handler) CreateVersion(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	agentID := chi.URLParam(r, "agentId")

	var req struct {
		Configuration json.RawMessage `json:"configuration"`
		Changelog     string          `json:"changelog"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	if len(req.Changelog) > 2000 {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"changelog must be at most 2000 characters")
		return
	}
	cfg, err := ParseConfiguration(req.Configuration)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	cfgJSON, _ := jsonMarshal(cfg)

	var v versionResponse
	err = h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		// Lock the agent row: this both verifies existence under RLS (a
		// foreign agent is invisible) and serializes concurrent version
		// creation so version numbers allocate safely. The FK alone would
		// not help — it ignores row-level security.
		var lockedID string
		if err := tx.QueryRow(r.Context(), `
			SELECT id FROM agents
			WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
			FOR UPDATE`, agentID, orgID).Scan(&lockedID); err != nil {
			return err
		}
		var next int
		if err := tx.QueryRow(r.Context(),
			"SELECT COALESCE(MAX(version), 0) + 1 FROM agent_versions WHERE agent_id = $1",
			agentID).Scan(&next); err != nil {
			return err
		}
		// (pgx cannot scan into **string, so published_at lands in a local.)
		var published *string
		if err := tx.QueryRow(r.Context(), `
			INSERT INTO agent_versions (organization_id, agent_id, version, configuration, changelog, created_by)
			VALUES ($1, $2, $3, $4::jsonb, $5, $6)
			RETURNING id, version, status, changelog, configuration, published_at::text, created_at::text`,
			orgID, agentID, next, string(cfgJSON), req.Changelog, actorID,
		).Scan(&v.ID, &v.Version, &v.Status, &v.Changelog, &v.Config, &published, &v.CreatedAt); err != nil {
			return err
		}
		v.PublishedAt = published
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "agent.version_created", ResourceType: "agent", ResourceID: agentID,
			Result: "success", Metadata: map[string]any{"version": v.Version},
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, r, http.StatusNotFound, "not_found", "agent not found")
	case err != nil:
		h.Logger.Error("version create failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to create version")
	default:
		httpx.JSON(w, r, http.StatusCreated, v)
	}
}

// PublishVersion promotes a draft version to published (immutable).
func (h *Handler) PublishVersion(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	agentID := chi.URLParam(r, "agentId")
	versionNumber, ok := pathInt(r, "version")
	if !ok {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "version must be an integer")
		return
	}

	var v versionResponse
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		// Validate the configuration before it becomes immutable.
		var raw []byte
		var status string
		if err := tx.QueryRow(r.Context(), `
			SELECT configuration, status FROM agent_versions
			WHERE agent_id = $1 AND version = $2 AND organization_id = $3`,
			agentID, versionNumber, orgID).Scan(&raw, &status); err != nil {
			return err
		}
		if status != "draft" {
			return fmt.Errorf("version %d is already %s", versionNumber, status)
		}
		if _, err := ParseConfiguration(raw); err != nil {
			return err
		}
		err := tx.QueryRow(r.Context(), `
			UPDATE agent_versions
			SET status = 'published', published_at = now()
			WHERE agent_id = $1 AND version = $2 AND organization_id = $3
			RETURNING id, version, status, changelog, configuration, published_at::text, created_at::text`,
			agentID, versionNumber, orgID,
		).Scan(&v.ID, &v.Version, &v.Status, &v.Changelog, &v.Config, &v.PublishedAt, &v.CreatedAt)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "agent.version_published", ResourceType: "agent", ResourceID: agentID,
			Result: "success", Metadata: map[string]any{"version": versionNumber},
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, r, http.StatusNotFound, "not_found", "version not found")
	case err != nil && strings.Contains(err.Error(), "already"):
		httpx.Error(w, r, http.StatusConflict, "already_published", err.Error())
	case err != nil && strings.Contains(err.Error(), "invalid configuration"):
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", err.Error())
	case err != nil:
		h.Logger.Error("version publish failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to publish version")
	default:
		httpx.JSON(w, r, http.StatusOK, v)
	}
}

// pathInt parses a positive integer URL parameter.
func pathInt(r *http.Request, name string) (int, bool) {
	raw := chi.URLParam(r, name)
	if raw == "" {
		return 0, false
	}
	n := 0
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n > 1_000_000 {
			return 0, false
		}
	}
	return n, n > 0
}

func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
