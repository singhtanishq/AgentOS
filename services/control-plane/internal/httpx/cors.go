package httpx

import "net/http"

// CORS development middleware: the dashboard (:3000) calls the API
// (:8080) cross-origin on the same machine with cookie credentials.
// The origin allowlist keeps production origins browser-controlled via
// reverse proxies (same-origin) rather than by this middleware.
func CORSMiddleware(allowedOrigins map[string]bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && allowedOrigins[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Vary", "Origin")
			}
			if r.Method == http.MethodOptions && origin != "" {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-Id")
				w.Header().Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// DevOrigins returns the standard local development origin set.
func DevOrigins() map[string]bool {
	return map[string]bool{
		"http://localhost:3000": true,
		"http://127.0.0.1:3000": true,
	}
}
