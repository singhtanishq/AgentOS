// Package server assembles the control-plane HTTP server: routing,
// middleware chain and health endpoints.
package server

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/config"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/health"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
)

// NewRouter builds the top-level mux for the control plane API.
// Versioned resource routes are mounted by feature packages under /v1.
func NewRouter(cfg config.Config, logger *slog.Logger, readiness *health.Registry) http.Handler {
	r := chi.NewRouter()

	r.Use(httpx.RequestIDMiddleware)
	r.Use(httpx.Recover(logger))
	r.Use(httpx.Logging(logger))

	// Health endpoints are unauthenticated infrastructure probes.
	r.Get("/healthz", readiness.Liveness)
	r.Get("/readyz", readiness.Readiness)

	// API root: machine-readable service description.
	r.Get("/api", func(w http.ResponseWriter, req *http.Request) {
		httpx.JSON(w, req, http.StatusOK, map[string]any{
			"name":    "agentos-control-plane",
			"version": "v1",
			"status":  "initializing",
		})
	})

	// Versioned API namespace. Feature packages mount their sections here
	// as they are implemented (auth, agents, executions, ...).
	r.Route("/v1", func(v1 chi.Router) {
		v1.Get("/", func(w http.ResponseWriter, req *http.Request) {
			httpx.JSON(w, req, http.StatusOK, map[string]string{
				"name":    "AgentOS Control Plane API",
				"version": "1.0.0",
			})
		})
	})

	_ = cfg // configuration is consumed by feature wiring in later milestones

	return r
}
