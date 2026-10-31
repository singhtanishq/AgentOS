// Package policies implements policy management and OPA synchronization.
// Policy content is Rego; the control plane pushes policy modules to OPA's
// REST API, and the execution plane evaluates decisions against OPA at run
// time with fail-closed semantics.
package policies

import (
	"context"
	_ "embed"
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

// DefaultPolicy is the platform baseline deployed to OPA at startup. The
// rego source is embedded from policies/agentos_baseline.rego so the Go
// control plane and Python tests share one definition.
const DefaultPolicyName = "agentos_baseline"

//go:embed agentos_baseline.rego
var DefaultPolicyRego string

// Deployer pushes policy modules to OPA.
type Deployer struct {
	BaseURL string
	Client  *http.Client
	Logger  *slog.Logger
}

// Deploy installs one named policy module in OPA. OPA accepts the raw
// Rego module as the request body (Content-Type text/plain).
func (d *Deployer) Deploy(ctx context.Context, name, rego string) error {
	url := strings.TrimRight(d.BaseURL, "/") + "/v1/policies/" + name
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, strings.NewReader(rego))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("push policy to opa: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("opa rejected policy %s: %d %s", name, resp.StatusCode, body)
	}
	return nil
}

// Remove deletes a policy module from OPA.
func (d *Deployer) Remove(ctx context.Context, name string) error {
	url := strings.TrimRight(d.BaseURL, "/") + "/v1/policies/" + name
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("remove policy from opa: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("opa remove failed: %d", resp.StatusCode)
	}
	return nil
}

// EnsureBaseline deploys the platform baseline policy (idempotent).
func (d *Deployer) EnsureBaseline(ctx context.Context) error {
	return d.Deploy(ctx, DefaultPolicyName, DefaultPolicyRego)
}

// Handler implements policy endpoints.
type Handler struct {
	Store    *store.Store
	Deployer *Deployer
	Logger   *slog.Logger
}

type policyResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Version int    `json:"version"`
	Status  string `json:"status"`
	Content string `json:"content,omitempty"`
}

// Create validates and registers a policy, deploying it to OPA.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())

	var req struct {
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		Content string `json:"content"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if !validPolicyName(req.Name) {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"name must be 2-80 characters of letters, digits, dash or underscore")
		return
	}
	switch req.Kind {
	case "authorization", "approval", "budget", "model_usage":
	default:
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"kind must be one of authorization, approval, budget, model_usage")
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" || len(content) > 64_000 {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"content is required (max 64000 characters)")
		return
	}
	if !strings.Contains(content, "package agentos.authz") {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"policy content must declare 'package agentos.authz'")
		return
	}

	// Validate the Rego compiles by deploying first (fail fast).
	version := int(time.Now().Unix())
	policyName := policyModuleID(orgID, req.Name, version)
	if err := h.Deployer.Deploy(r.Context(), policyName, content); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "policy_invalid", err.Error())
		return
	}

	var p policyResponse
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		err := tx.QueryRow(r.Context(), `
			INSERT INTO policies (organization_id, name, kind, content, version)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id, name, kind, version, status`,
			orgID, req.Name, req.Kind, content, version,
		).Scan(&p.ID, &p.Name, &p.Kind, &p.Version, &p.Status)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "policy.created", ResourceType: "policy", ResourceID: p.ID,
			Result: "success", Metadata: map[string]any{"version": version},
		})
	})
	if err != nil {
		if isUniqueViolation(err) {
			httpx.Error(w, r, http.StatusConflict, "name_taken",
				"a policy with this name already exists")
			return
		}
		h.Logger.Error("policy create failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to create policy")
		return
	}
	p.Content = content
	httpx.JSON(w, r, http.StatusCreated, p)
}

// List returns the organization's policies.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	var items []policyResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT id, name, kind, version, status FROM policies
			WHERE organization_id = $1 ORDER BY name, version DESC`, orgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []policyResponse{}
		for rows.Next() {
			var p policyResponse
			if err := rows.Scan(&p.ID, &p.Name, &p.Kind, &p.Version, &p.Status); err != nil {
				return err
			}
			items = append(items, p)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("policy list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list policies")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// Delete disables a policy and removes its module from OPA.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	policyID := chi.URLParam(r, "policyId")

	var p policyResponse
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		err := tx.QueryRow(r.Context(), `
			UPDATE policies SET status = 'disabled'
			WHERE id = $1 AND organization_id = $2 AND status = 'active'
			RETURNING id, name, kind, version, status`, policyID, orgID,
		).Scan(&p.ID, &p.Name, &p.Kind, &p.Version, &p.Status)
		if err != nil {
			return err
		}
		if err := audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "policy.disabled", ResourceType: "policy", ResourceID: policyID,
			Result: "success",
		}); err != nil {
			return err
		}
		// Best-effort removal from OPA; the module name embeds the version.
		return h.Deployer.Remove(r.Context(), policyModuleID(orgID, p.Name, p.Version))
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "policy not found or already disabled")
		return
	}
	if err != nil {
		h.Logger.Error("policy delete failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to disable policy")
		return
	}
	httpx.JSON(w, r, http.StatusOK, p)
}

func validPolicyName(name string) bool {
	if len(name) < 2 || len(name) > 80 {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func policyModuleID(orgID, name string, version int) string {
	return "agentos_org_" + sanitize(orgID) + "_" + sanitize(name) + "_" + itoa(version)
}

func sanitize(s string) string {
	out := strings.Map(func(r rune) rune {
		if r == '-' || r == '_' {
			return r
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, s)
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
