package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLiveness(t *testing.T) {
	reg := NewRegistry()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	reg.Liveness(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestReadinessAllHealthy(t *testing.T) {
	reg := NewRegistry()
	reg.Register("db", func(context.Context) error { return nil })
	reg.Register("nats", func(context.Context) error { return nil })

	rec := httptest.NewRecorder()
	reg.Readiness(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
}

func TestReadinessDegraded(t *testing.T) {
	reg := NewRegistry()
	reg.Register("db", func(context.Context) error { return nil })
	reg.Register("nats", func(context.Context) error { return errors.New("connection refused") })

	rec := httptest.NewRecorder()
	reg.Readiness(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", rec.Code, rec.Body.String())
	}
}

func TestReadinessCheckPanicRecovered(t *testing.T) {
	reg := NewRegistry()
	reg.Register("boom", func(context.Context) error { panic("exploded") })

	rec := httptest.NewRecorder()
	reg.Readiness(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (panic must degrade readiness)", rec.Code)
	}
}
