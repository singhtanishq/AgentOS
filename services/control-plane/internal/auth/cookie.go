package auth

import (
	"net/http"
	"time"
)

// Cookie configuration: HTTP-only, SameSite=Lax. The Secure flag follows
// the deployment environment so local HTTP development keeps working while
// any non-local (TLS-terminated) deployment requires HTTPS.
func sessionCookie(name, value string, ttl time.Duration, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// SetSessionCookie writes the session cookie on the response.
func SetSessionCookie(w http.ResponseWriter, name, token string, ttl time.Duration, secure bool) {
	http.SetCookie(w, sessionCookie(name, token, ttl, secure))
}

// ClearSessionCookie expires the session cookie.
func ClearSessionCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, sessionCookie(name, "", -1*time.Second, secure))
}
