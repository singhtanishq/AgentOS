// Package server assembles the control-plane HTTP server: routing,
// middleware chain, health endpoints and feature wiring.
package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/agents"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/approvals"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/auth"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/config"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/executions"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/health"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/identities"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/mcp"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/models"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/organizations"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/policies"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/queue"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/secrets"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/tools"
)

// Deps carries the wired application dependencies for feature routes.
type Deps struct {
	Store          *store.Store
	Secrets        *secrets.Manager
	Publisher      *queue.Publisher
	PolicyDeployer *policies.Deployer
}

// NewRouter builds the top-level mux for the control plane API.
func NewRouter(cfg config.Config, logger *slog.Logger, readiness *health.Registry, deps *Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(httpx.RequestIDMiddleware)
	r.Use(httpx.Recover(logger))
	r.Use(httpx.Logging(logger))
	r.Use(middleware.Timeout(30 * time.Second))

	// Health endpoints are unauthenticated infrastructure probes.
	r.Get("/healthz", readiness.Liveness)
	r.Get("/readyz", readiness.Readiness)

	r.Get("/api", func(w http.ResponseWriter, req *http.Request) {
		httpx.JSON(w, req, http.StatusOK, map[string]any{
			"name":    "agentos-control-plane",
			"version": "v1",
			"status":  "serving",
		})
	})

	secureCookies := cfg.Env == "production"
	authHandler := &auth.Handler{
		Store:         deps.Store,
		CookieName:    cfg.SessionCookieName,
		SecureCookies: secureCookies,
		Logger:        logger,
	}
	orgHandler := &organizations.Handler{Store: deps.Store, Logger: logger}

	// Authentication endpoints: aggressively rate limited per IP.
	r.Route("/v1/auth", func(authRoutes chi.Router) {
		authRoutes.Use(httpx.RateLimit(30, time.Minute, false))
		authRoutes.Post("/signup", authHandler.SignUp)
		authRoutes.Post("/login", authHandler.Login)
		authRoutes.With(auth.RequireAuth(deps.Store, cfg.SessionCookieName, logger)).Post("/logout", authHandler.Logout)
		authRoutes.With(auth.RequireAuth(deps.Store, cfg.SessionCookieName, logger)).Get("/session", authHandler.Session)
	})

	// Organizations and their members/invitations.
	requireAuth := auth.RequireAuth(deps.Store, cfg.SessionCookieName, logger)
	r.Route("/v1/organizations", func(orgRoutes chi.Router) {
		orgRoutes.Use(requireAuth)
		orgRoutes.Post("/", orgHandler.Create)
		orgRoutes.Get("/", orgHandler.List)

		orgRoutes.Route("/{orgId}", func(org chi.Router) {
			// View routes require membership but no specific permission.
			org.With(auth.RequireOrg(deps.Store, "", logger)).Get("/", orgHandler.Get)
			org.With(auth.RequireOrg(deps.Store, "", logger)).Get("/members", orgHandler.ListMembers)
			org.With(auth.RequireOrg(deps.Store, auth.PermManageMembers, logger)).Patch("/members/{userID}", orgHandler.UpdateMemberRole)
			org.With(auth.RequireOrg(deps.Store, auth.PermManageMembers, logger)).Delete("/members/{userID}", orgHandler.RemoveMember)
			org.With(auth.RequireOrg(deps.Store, auth.PermManageMembers, logger)).Post("/invitations", orgHandler.CreateInvitation)
			org.With(auth.RequireOrg(deps.Store, auth.PermManageMembers, logger)).Get("/invitations", orgHandler.ListInvitations)
			org.With(auth.RequireOrg(deps.Store, auth.PermManageMembers, logger)).Delete("/invitations/{invitationID}", orgHandler.RevokeInvitation)
		})
	})

	// Invitation acceptance is user-scoped, not org-scoped.
	r.With(requireAuth).Post("/v1/invitations/accept", orgHandler.AcceptInvitation)

	// Agent registry.
	agentHandler := &agents.Handler{Store: deps.Store, Logger: logger}
	r.Route("/v1/organizations/{orgId}/agents", func(agentRoutes chi.Router) {
		agentRoutes.Use(requireAuth)
		agentRoutes.With(auth.RequireOrg(deps.Store, auth.PermViewAgents, logger)).Get("/", agentHandler.List)
		agentRoutes.With(auth.RequireOrg(deps.Store, auth.PermEditAgents, logger)).Post("/", agentHandler.Create)
		agentRoutes.Route("/{agentId}", func(agent chi.Router) {
			agent.With(auth.RequireOrg(deps.Store, auth.PermViewAgents, logger)).Get("/", agentHandler.Get)
			agent.With(auth.RequireOrg(deps.Store, auth.PermEditAgents, logger)).Patch("/", agentHandler.Update)
			agent.With(auth.RequireOrg(deps.Store, auth.PermEditAgents, logger)).Delete("/", agentHandler.Delete)
			agent.With(auth.RequireOrg(deps.Store, auth.PermEditAgents, logger)).Put("/status", agentHandler.UpdateStatus)
			agent.With(auth.RequireOrg(deps.Store, auth.PermEditAgents, logger)).Post("/versions", agentHandler.CreateVersion)
			agent.With(auth.RequireOrg(deps.Store, auth.PermEditAgents, logger)).Post("/versions/{version}/publish", agentHandler.PublishVersion)
		})
	})

	// Execution management.
	executionsHandler := &executions.Handler{Store: deps.Store, Publisher: deps.Publisher, Logger: logger}
	r.Route("/v1/organizations/{orgId}/executions", func(ex chi.Router) {
		ex.Use(requireAuth)
		ex.With(auth.RequireOrg(deps.Store, auth.PermViewExecutions, logger)).Get("/", executionsHandler.List)
		ex.With(auth.RequireOrg(deps.Store, auth.PermSubmitExecutions, logger)).Post("/", executionsHandler.Submit)
		ex.Route("/{executionId}", func(e chi.Router) {
			e.With(auth.RequireOrg(deps.Store, auth.PermViewExecutions, logger)).Get("/", executionsHandler.Get)
			e.With(auth.RequireOrg(deps.Store, auth.PermViewExecutions, logger)).Get("/events", executionsHandler.Events)
			e.With(auth.RequireOrg(deps.Store, auth.PermCancelExecutions, logger)).Post("/cancel", executionsHandler.Cancel)
		})
	})

	// Tool registry.
	toolsHandler := &tools.Handler{Store: deps.Store, Logger: logger}
	r.Route("/v1/organizations/{orgId}/tools", func(tr chi.Router) {
		tr.Use(requireAuth)
		tr.With(auth.RequireOrg(deps.Store, auth.PermViewAgents, logger)).Get("/", toolsHandler.List)
		tr.Route("/{toolId}", func(t chi.Router) {
			t.With(auth.RequireOrg(deps.Store, auth.PermViewAgents, logger)).Get("/", toolsHandler.Get)
			t.With(auth.RequireOrg(deps.Store, auth.PermManageTools, logger)).Patch("/", toolsHandler.Update)
		})
	})

	// Policies (governance).
	policiesHandler := &policies.Handler{Store: deps.Store, Deployer: deps.PolicyDeployer, Logger: logger}
	r.Route("/v1/organizations/{orgId}/policies", func(pr chi.Router) {
		pr.Use(requireAuth)
		pr.With(auth.RequireOrg(deps.Store, auth.PermViewAgents, logger)).Get("/", policiesHandler.List)
		pr.With(auth.RequireOrg(deps.Store, auth.PermManagePolicies, logger)).Post("/", policiesHandler.Create)
		pr.With(auth.RequireOrg(deps.Store, auth.PermManagePolicies, logger)).Delete("/{policyId}", policiesHandler.Delete)
	})

	// Approvals.
	approvalsHandler := &approvals.Handler{Store: deps.Store, Publisher: deps.Publisher, Logger: logger}
	r.Route("/v1/organizations/{orgId}/approvals", func(ar chi.Router) {
		ar.Use(requireAuth)
		ar.With(auth.RequireOrg(deps.Store, auth.PermViewExecutions, logger)).Get("/", approvalsHandler.List)
		ar.With(auth.RequireOrg(deps.Store, auth.PermDecideApprovals, logger)).Post("/{approvalId}/decide", approvalsHandler.Decide)
	})

	// Agent identities.
	identitiesHandler := &identities.Handler{Store: deps.Store, Logger: logger}
	r.Route("/v1/organizations/{orgId}/agents/{agentId}/identities", func(idn chi.Router) {
		idn.Use(requireAuth)
		idn.With(auth.RequireOrg(deps.Store, auth.PermViewAgents, logger)).Get("/", identitiesHandler.List)
		idn.With(auth.RequireOrg(deps.Store, auth.PermManageCredentials, logger)).Post("/", identitiesHandler.Create)
		idn.With(auth.RequireOrg(deps.Store, auth.PermManageCredentials, logger)).Post("/{identityId}/suspend", identitiesHandler.Suspend)
		idn.With(auth.RequireOrg(deps.Store, auth.PermManageCredentials, logger)).Post("/{identityId}/revoke", identitiesHandler.Revoke)
	})

	// MCP connections.
	mcpHandler := &mcp.Handler{Store: deps.Store, Logger: logger}
	r.Route("/v1/organizations/{orgId}/mcp-connections", func(mc chi.Router) {
		mc.Use(requireAuth)
		mc.With(auth.RequireOrg(deps.Store, auth.PermViewAgents, logger)).Get("/", mcpHandler.List)
		mc.With(auth.RequireOrg(deps.Store, auth.PermManageTools, logger)).Post("/", mcpHandler.Create)
		mc.Route("/{connectionId}", func(c chi.Router) {
			c.With(auth.RequireOrg(deps.Store, auth.PermManageTools, logger)).Put("/status", mcpHandler.UpdateStatus)
			c.With(auth.RequireOrg(deps.Store, auth.PermManageTools, logger)).Delete("/", mcpHandler.Delete)
		})
	})

	// Model gateway.
	modelsHandler := &models.Handler{Store: deps.Store, Secrets: deps.Secrets, Logger: logger}
	r.Route("/v1/organizations/{orgId}/model-providers", func(mp chi.Router) {
		mp.Use(requireAuth)
		mp.With(auth.RequireOrg(deps.Store, auth.PermViewAgents, logger)).Get("/", modelsHandler.ListProviders)
		mp.With(auth.RequireOrg(deps.Store, auth.PermManageCredentials, logger)).Post("/", modelsHandler.CreateProvider)
		mp.Route("/{providerId}", func(provider chi.Router) {
			provider.With(auth.RequireOrg(deps.Store, auth.PermViewAgents, logger)).Get("/credentials", modelsHandler.ListCredentials)
			provider.With(auth.RequireOrg(deps.Store, auth.PermManageCredentials, logger)).Post("/credentials", modelsHandler.CreateCredential)
			provider.With(auth.RequireOrg(deps.Store, auth.PermManageCredentials, logger)).Delete("/credentials/{credentialId}", modelsHandler.RevokeCredential)
		})
	})
	r.Route("/v1/organizations/{orgId}/models", func(mr chi.Router) {
		mr.Use(requireAuth)
		mr.With(auth.RequireOrg(deps.Store, auth.PermViewAgents, logger)).Get("/", modelsHandler.ListModels)
		mr.With(auth.RequireOrg(deps.Store, auth.PermManageCredentials, logger)).Post("/", modelsHandler.CreateModel)
	})

	// Internal service-to-service surface (execution plane only).
	internalHandler := &models.InternalHandler{
		Store: deps.Store, Secrets: deps.Secrets, Token: cfg.InternalAPIToken, Logger: logger,
	}
	r.Route("/internal", func(internal chi.Router) {
		internal.Use(internalHandler.RequireServiceToken)
		internal.Get("/credentials/{credentialId}", internalHandler.RevealCredential)
	})

	// API root.
	r.Get("/v1", func(w http.ResponseWriter, req *http.Request) {
		httpx.JSON(w, req, http.StatusOK, map[string]string{
			"name":    "AgentOS Control Plane API",
			"version": "1.0.0",
		})
	})

	return r
}
