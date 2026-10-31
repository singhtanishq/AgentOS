package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// Principal is the authenticated caller: either a user (session cookie)
// or an API key (bearer token).
type Principal struct {
	Type string // "user" | "api_key"
	ID   string
	// OrgID is set for API-key principals (their owning organization).
	OrgID string
	// Scopes is set for API-key principals.
	Scopes []string
	// Role is set for user principals within the resolved organization.
	Role Role
}

// CanAuthorize reports whether the principal holds the permission. API
// keys map their scopes directly to permissions.
func (p Principal) Can(permission Permission) bool {
	if p.Type == "api_key" {
		for _, s := range p.Scopes {
			if Permission(s) == permission {
				return true
			}
		}
		return false
	}
	return p.Role.Can(permission)
}

// ErrAPIKeyInvalid is returned for unknown or revoked API keys.
var ErrAPIKeyInvalid = errors.New("api key invalid")

// ResolveAPIKey maps a bearer token to its API key row. Only the SHA-256
// hash is stored; raw keys exist solely at the caller.
func ResolveAPIKey(ctx context.Context, st *store.Store, bearer string) (Principal, error) {
	if !strings.HasPrefix(bearer, "agos_") {
		return Principal{}, ErrAPIKeyInvalid
	}
	var p Principal
	var scopes []string
	err := st.Pool.QueryRow(ctx, `
		SELECT id, organization_id, scopes
		FROM api_keys
		WHERE key_hash = $1 AND status = 'active'
		  AND (expires_at IS NULL OR expires_at > now())`,
		TokenHash(bearer),
	).Scan(&p.ID, &p.OrgID, &scopes)
	if errors.Is(err, context.Canceled) {
		return Principal{}, err
	}
	if err != nil {
		return Principal{}, ErrAPIKeyInvalid
	}
	p.Type = "api_key"
	p.Scopes = scopes
	// Last-used tracking is best effort and never blocks authentication.
	_, _ = st.Pool.Exec(ctx,
		"UPDATE api_keys SET last_used_at = now() WHERE id = $1", p.ID)
	return p, nil
}

// principalKey is the context key for the resolved principal.
type principalKey struct{}

// WithPrincipal stores the resolved principal on the request context.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the authenticated principal, falling back to a
// user principal derived from legacy context values.
func PrincipalFrom(ctx context.Context) Principal {
	if v, ok := ctx.Value(principalKey{}).(Principal); ok {
		return v
	}
	return Principal{Type: "user", ID: UserID(ctx)}
}

// AuthMiddleware authenticates via session cookie or bearer API key.
func AuthMiddleware(st *store.Store, cookieName string, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authz := r.Header.Get("Authorization")
			if strings.HasPrefix(authz, "Bearer ") {
				principal, err := ResolveAPIKey(r.Context(), st, strings.TrimSpace(authz[7:]))
				if err != nil {
					httpx.Error(w, r, http.StatusUnauthorized, "unauthorized", "invalid API key")
					return
				}
				next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
				return
			}

			// Session-cookie authentication.
			c, err := r.Cookie(cookieName)
			if err != nil || c.Value == "" {
				httpx.Error(w, r, http.StatusUnauthorized, "unauthorized", "authentication required")
				return
			}
			session, err := ResolveSession(r.Context(), st, c.Value)
			if err != nil {
				httpx.Error(w, r, http.StatusUnauthorized, "unauthorized", "session expired or invalid")
				return
			}
			TouchSession(r.Context(), st, session)
			ctx := context.WithValue(r.Context(), ctxUserID, session.UserID)
			ctx = context.WithValue(ctx, ctxSessionID, session.ID)
			ctx = WithPrincipal(ctx, Principal{Type: "user", ID: session.UserID})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
