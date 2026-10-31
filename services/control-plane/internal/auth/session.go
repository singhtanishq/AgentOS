package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// Session lifetime and renewal thresholds.
const (
	sessionTTL        = 12 * time.Hour
	renewBeforeMargin = 6 * time.Hour // renew when less than this remains
)

// ErrSessionInvalid is returned for unknown, expired or revoked sessions.
var ErrSessionInvalid = errors.New("session invalid")

// Session is a resolved session row.
type Session struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
}

// CreateSession issues a new session for the user and returns the opaque
// token (to be placed in a cookie) alongside the stored row.
func CreateSession(ctx context.Context, st *store.Store, userID, userAgent, ip string) (token string, s Session, err error) {
	token, tokenHash, err := NewSessionToken()
	if err != nil {
		return "", Session{}, err
	}
	expires := time.Now().Add(sessionTTL)
	var ipVal any
	if ip != "" {
		if parsed := net.ParseIP(ip); parsed != nil {
			ipVal = parsed.String()
		}
	}
	err = st.Pool.QueryRow(ctx, `
		INSERT INTO sessions (user_id, token_hash, user_agent, ip_address, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, user_id, expires_at`,
		userID, tokenHash, userAgent, ipVal, expires,
	).Scan(&s.ID, &s.UserID, &s.ExpiresAt)
	if err != nil {
		return "", Session{}, fmt.Errorf("create session: %w", err)
	}
	return token, s, nil
}

// ResolveSession maps a raw token to its session, rejecting unknown,
// expired or revoked sessions. Expired sessions are opportunistically
// deleted.
func ResolveSession(ctx context.Context, st *store.Store, token string) (Session, error) {
	hash := TokenHash(token)
	var s Session
	var revokedAt *time.Time
	err := st.Pool.QueryRow(ctx, `
		SELECT id, user_id, expires_at, revoked_at
		FROM sessions
		WHERE token_hash = $1`, hash,
	).Scan(&s.ID, &s.UserID, &s.ExpiresAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrSessionInvalid
	}
	if err != nil {
		return Session{}, fmt.Errorf("resolve session: %w", err)
	}
	if revokedAt != nil || time.Now().After(s.ExpiresAt) {
		// Best-effort cleanup of expired rows.
		_, _ = st.Pool.Exec(ctx, "DELETE FROM sessions WHERE id = $1", s.ID)
		return Session{}, ErrSessionInvalid
	}
	return s, nil
}

// TouchSession renews sliding expiry when the session is past the renewal
// threshold, and records activity.
func TouchSession(ctx context.Context, st *store.Store, s Session) {
	if time.Until(s.ExpiresAt) > renewBeforeMargin {
		_, _ = st.Pool.Exec(ctx,
			"UPDATE sessions SET last_seen_at = now() WHERE id = $1", s.ID)
		return
	}
	newExpiry := time.Now().Add(sessionTTL)
	_, _ = st.Pool.Exec(ctx, `
		UPDATE sessions SET expires_at = $2, last_seen_at = now() WHERE id = $1`, s.ID, newExpiry)
}

// RevokeSession invalidates the session immediately.
func RevokeSession(ctx context.Context, st *store.Store, sessionID string) error {
	_, err := st.Pool.Exec(ctx,
		"UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL", sessionID)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}
