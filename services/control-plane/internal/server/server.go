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
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/auth"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/config"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/health"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/organizations"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// Deps carries the wired application dependencies for feature routes.
type Deps struct {
	Store *store.Store
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

	// API root.
	r.Get("/v1", func(w http.ResponseWriter, req *http.Request) {
		httpx.JSON(w, req, http.StatusOK, map[string]string{
			"name":    "AgentOS Control Plane API",
			"version": "1.0.0",
		})
	})

	return r
}
