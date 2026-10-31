// Package httpx provides shared HTTP plumbing for the control plane:
// JSON response envelopes, error responses and standard middleware.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// CtxKey is a typed context key used by middleware.
type CtxKey string

const requestIDKey CtxKey = "request_id"

// RequestID returns the request ID injected by the RequestID middleware.
func RequestID(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// ErrorBody is the consistent error envelope for all API errors.
type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// ErrorResponse is the top-level error response shape.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// Error writes a JSON error envelope with the given status and code.
func Error(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	body := ErrorResponse{Error: ErrorBody{
		Code:      code,
		Message:   message,
		RequestID: RequestID(r.Context()),
	}}
	JSON(w, r, status, body)
}

// JSON writes v as a JSON response with the given status code.
func JSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Request-Id", RequestID(r.Context()))
	w.WriteHeader(status)
	if v != nil {
		enc := json.NewEncoder(w)
		// Prevent HTML escaping surprises in API payloads.
		enc.SetEscapeHTML(false)
		_ = enc.Encode(v)
	}
}

// DecodeJSON decodes a strict JSON request body into dst.
// Unknown fields are rejected to surface client errors early.
func DecodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	// Reject trailing content after the JSON document.
	if dec.More() {
		return errors.New("unexpected trailing data in request body")
	}
	return nil
}

// Recover middleware converts panics into 500 responses instead of
// crashing the server, and logs the panic with the structured logger.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recovered",
						slog.String("request_id", RequestID(r.Context())),
						slog.Any("panic", rec),
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
					)
					Error(w, r, http.StatusInternalServerError,
						"internal_error", "an internal error occurred")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RequestID middleware ensures every request carries a unique request ID,
// propagating an incoming X-Request-Id when provided by a trusted caller.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = NewRequestID()
		}
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Logging middleware emits one structured access log line per request.
func Logging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(ww, r)
			logger.Info("http_request",
				slog.String("request_id", RequestID(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", ww.status),
			)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status = code
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(code)
}
