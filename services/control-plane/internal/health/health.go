// Package health implements liveness and readiness endpoints with a
// pluggable component-check registry.
package health

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
)

// Check reports whether a component is currently healthy.
type Check func(ctx context.Context) error

// Registry tracks named component checks and evaluates readiness.
type Registry struct {
	mu     sync.RWMutex
	checks map[string]Check
}

// NewRegistry returns an empty component registry.
func NewRegistry() *Registry {
	return &Registry{checks: make(map[string]Check)}
}

// Register adds or replaces a named component check.
func (r *Registry) Register(name string, check Check) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[name] = check
}

// Liveness always returns 200 while the process can serve HTTP.
func (r *Registry) Liveness(w http.ResponseWriter, req *http.Request) {
	httpx.JSON(w, req, http.StatusOK, map[string]string{"status": "alive"})
}

// readinessResult is the JSON body of the readiness endpoint.
type readinessResult struct {
	Status     string            `json:"status"`
	Components map[string]string `json:"components"`
}

// Readiness evaluates every registered component and returns 503 when any
// component fails, listing the failing components.
func (r *Registry) Readiness(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	r.mu.RLock()
	names := make([]string, 0, len(r.checks))
	checks := make(map[string]Check, len(r.checks))
	for name, check := range r.checks {
		names = append(names, name)
		checks[name] = check
	}
	r.mu.RUnlock()
	sort.Strings(names)

	results := make(map[string]string, len(checks))
	failed := false
	for _, name := range names {
		if err := runCheck(checks[name], ctx); err != nil {
			results[name] = "unavailable: " + err.Error()
			failed = true
		} else {
			results[name] = "ok"
		}
	}

	status := "ready"
	code := http.StatusOK
	if failed {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}
	httpx.JSON(w, req, code, readinessResult{Status: status, Components: results})
}

func runCheck(check Check, ctx context.Context) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("check panicked: %v", rec)
		}
	}()
	return check(ctx)
}
