package httpx

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDGenerated(t *testing.T) {
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := RequestID(r.Context()); got == "" {
			t.Error("expected request ID in context")
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("expected X-Request-Id response header")
	}
}

func TestRequestIDPropagated(t *testing.T) {
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := RequestID(r.Context()); got != "req-123" {
			t.Errorf("RequestID = %q, want req-123", got)
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "req-123")
	handler.ServeHTTP(httptest.NewRecorder(), req)
}

func TestNewRequestIDUnique(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		id := NewRequestID()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate request id %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestErrorEnvelope(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	Error(rec, req, http.StatusNotFound, "not_found", "resource missing")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}
	var body ErrorResponse
	if err := jsonUnmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Error.Code != "not_found" || body.Error.Message != "resource missing" {
		t.Errorf("unexpected body: %+v", body)
	}
}

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}
	req := httptest.NewRequest(http.MethodPost, "/", stringReader(`{"name":"a","evil":1}`))
	var p payload
	if err := DecodeJSON(req, &p); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestDecodeJSONRejectsTrailingData(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}
	req := httptest.NewRequest(http.MethodPost, "/", stringReader(`{"name":"a"} {"name":"b"}`))
	var p payload
	if err := DecodeJSON(req, &p); err == nil {
		t.Fatal("expected error for trailing data")
	}
}

func TestRecoverPanics(t *testing.T) {
	boom := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	Recover(discardLogger())(boom).ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestHexEncoding(t *testing.T) {
	id := NewRequestID()
	if len(id) != 32 { // 16 random bytes hex-encoded
		t.Errorf("request id length = %d, want 32", len(id))
	}
	if _, err := hex.DecodeString(id); err != nil {
		t.Errorf("request id is not hex: %v", err)
	}
}
