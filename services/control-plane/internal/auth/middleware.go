package auth

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

type ctxKey string

const (
	ctxUserID    ctxKey = "user_id"
	ctxSessionID ctxKey = "session_id"
	ctxOrgID     ctxKey = "org_id"
	ctxRole      ctxKey = "org_role"
)

// UserID returns the authenticated user ID from the request context.
func UserID(ctx context.Context) string {
	if v, ok := ctx.Value(ctxUserID).(string); ok {
		return v
	}
	return ""
}

// SessionID returns the current session ID from the request context.
func SessionID(ctx context.Context) string {
	if v, ok := ctx.Value(ctxSessionID).(string); ok {
		return v
	}
	return ""
}

// OrgID returns the organization ID resolved by the org middleware.
func OrgID(ctx context.Context) string {
	if v, ok := ctx.Value(ctxOrgID).(string); ok {
		return v
	}
	return ""
}

// RoleFrom returns the caller's role within the resolved organization.
func RoleFrom(ctx context.Context) Role {
	if v, ok := ctx.Value(ctxRole).(Role); ok {
		return v
	}
	return ""
}

// RequireAuth authenticates the caller via session cookie or bearer API
// key and injects the principal into the request context. Sessions are
// renewed when near expiry.
func RequireAuth(st *store.Store, cookieName string, logger *slog.Logger) func(http.Handler) http.Handler {
	return AuthMiddleware(st, cookieName, logger)
}

// RequireOrg resolves the {orgId} route parameter, verifies membership via
// the same row-level security the data layer uses, enforces a permission,
// and injects organization context into the request.
func RequireOrg(st *store.Store, permission Permission, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			orgID := chi.URLParam(r, "orgId")
			if orgID == "" {
				httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "organization id is required")
				return
			}
			userID := UserID(r.Context())

			// Membership is checked under the target organization's RLS
			// context: non-members observe zero rows.
			var roleStr string
			found := false
			err := st.OrgTx(r.Context(), orgID, userID, func(tx pgx.Tx) error {
				scanErr := tx.QueryRow(r.Context(),
					"SELECT role FROM memberships WHERE organization_id = $1 AND user_id = $2",
					orgID, userID,
				).Scan(&roleStr)
				if errors.Is(scanErr, pgx.ErrNoRows) {
					return nil // not a member — found remains false
				}
				if scanErr != nil {
					return scanErr
				}
				found = true
				return nil
			})
			if err != nil {
				logger.Error("membership lookup failed", slog.Any("error", err))
				httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to resolve membership")
				return
			}
			if !found {
				httpx.Error(w, r, http.StatusForbidden, "forbidden", "not a member of this organization")
				return
			}

			role := Role(roleStr)
			if permission != "" && !role.Can(permission) {
				logger.Warn("permission denied",
					slog.String("user_id", userID),
					slog.String("org_id", orgID),
					slog.String("permission", string(permission)),
					slog.String("role", string(role)),
				)
				httpx.Error(w, r, http.StatusForbidden, "forbidden",
					"role "+string(role)+" does not grant "+string(permission))
				return
			}

			ctx := context.WithValue(r.Context(), ctxOrgID, orgID)
			ctx = context.WithValue(ctx, ctxRole, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClientIP extracts the direct peer address (no proxy trust by default).
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
