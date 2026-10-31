package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/config"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/health"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// nilStore is used for routes that do not touch the database in tests.
func testRouter(t *testing.T) http.Handler {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return NewRouter(cfg, discardLogger(), health.NewRegistry(), &Deps{Store: nil})
}

func TestHealthEndpoints(t *testing.T) {
	h := testRouter(t)

	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
	}
}

func TestAPIRootResponds(t *testing.T) {
	h := testRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/v1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /v1 = %d, want 200", rec.Code)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("expected X-Request-Id on API responses")
	}
}

func TestUnknownAPIRouteIs404(t *testing.T) {
	h := testRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/does-not-exist", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET unknown route = %d, want 404", rec.Code)
	}
}

func TestProtectedRoutesRequireAuth(t *testing.T) {
	h := testRouter(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/v1/auth/session"},
		{http.MethodPost, "/v1/auth/logout"},
		{http.MethodPost, "/v1/organizations/"},
		{http.MethodGet, "/v1/organizations/"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without cookie = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

// Compile-time guard: store import used through Deps.
var _ = store.Store{}
