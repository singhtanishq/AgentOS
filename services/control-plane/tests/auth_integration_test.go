//go:build integration

package tests

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/config"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/health"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/server"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// authTestServer builds an HTTP test server backed by the integration
// database using the production route wiring.
type authTestServer struct {
	t      *testing.T
	server *httptest.Server
	client *http.Client
}

func newAuthTestServer(t *testing.T) *authTestServer {
	t.Helper()
	p := testPool(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	logger := discardLogger()
	st := &store.Store{Pool: p}
	router := server.NewRouter(cfg, logger, health.NewRegistry(), &server.Deps{Store: st})
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)
	return &authTestServer{t: t, server: ts, client: ts.Client()}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func (a *authTestServer) do(method, path, cookie string, body any) (*http.Response, map[string]any) {
	a.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			a.t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, a.server.URL+path, reader)
	if err != nil {
		a.t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil && resp.ContentLength != 0 {
		a.t.Fatalf("%s %s: decode response: %v", method, path, err)
	}
	return resp, out
}

func cookieFrom(resp *http.Response) string {
	for _, c := range resp.Cookies() {
		if c.Name == "agentos_session" {
			return c.Name + "=" + c.Value
		}
	}
	return ""
}

func TestAuthEndToEnd(t *testing.T) {
	a := newAuthTestServer(t)

	// --- Signup with an initial organization -------------------------------
	resp, body := a.do(http.MethodPost, "/v1/auth/signup", "", map[string]any{
		"name": "Alice Owner", "email": "alice@example.test",
		"password": "correct-horse-9", "organization_name": "Acme Technologies",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("signup = %d: %v", resp.StatusCode, body)
	}
	user, _ := body["user"].(map[string]any)
	if user["email"] != "alice@example.test" {
		t.Fatalf("signup user email = %v", user["email"])
	}
	orgs := body["organizations"].([]any)
	if len(orgs) != 1 || orgs[0].(map[string]any)["role"] != "owner" {
		t.Fatalf("signup orgs = %v, want one owner org", orgs)
	}
	orgID := orgs[0].(map[string]any)["id"].(string)
	aliceCookie := cookieFrom(resp)
	if aliceCookie == "" {
		t.Fatal("no session cookie set on signup")
	}

	// --- Session reflects the user and organization ------------------------
	resp, body = a.do(http.MethodGet, "/v1/auth/session", aliceCookie, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session = %d: %v", resp.StatusCode, body)
	}
	if body["user"].(map[string]any)["name"] != "Alice Owner" {
		t.Fatalf("session user = %v", body["user"])
	}

	// --- Password policy and duplicate email are enforced ------------------
	resp, _ = a.do(http.MethodPost, "/v1/auth/signup", "", map[string]any{
		"name": "Weak", "email": "weak@example.test", "password": "short",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("weak password signup = %d, want 400", resp.StatusCode)
	}
	resp, _ = a.do(http.MethodPost, "/v1/auth/signup", "", map[string]any{
		"name": "Dup", "email": "alice@example.test", "password": "another-pass-9",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate email signup = %d, want 409", resp.StatusCode)
	}

	// --- Second user, own organization -------------------------------------
	resp, body = a.do(http.MethodPost, "/v1/auth/signup", "", map[string]any{
		"name": "Bob Ops", "email": "bob@example.test", "password": "bobs-password-1",
		"organization_name": "Bob Industries",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bob signup = %d: %v", resp.StatusCode, body)
	}
	bobCookie := cookieFrom(resp)
	bobOrgID := body["organizations"].([]any)[0].(map[string]any)["id"].(string)

	// --- Login invalid credentials → 401 ------------------------------------
	resp, _ = a.do(http.MethodPost, "/v1/auth/login", "", map[string]any{
		"email": "alice@example.test", "password": "wrong-password-1",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad login = %d, want 401", resp.StatusCode)
	}

	// --- Invitation flow ----------------------------------------------------
	resp, body = a.do(http.MethodPost, "/v1/organizations/"+orgID+"/invitations", aliceCookie, map[string]any{
		"email": "bob@example.test", "role": "developer",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite = %d: %v", resp.StatusCode, body)
	}
	token := body["token"].(string)

	// Cross-tenant guard: Bob cannot even read Alice's organization yet.
	resp, _ = a.do(http.MethodGet, "/v1/organizations/"+orgID, bobCookie, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("bob reading foreign org = %d, want 403", resp.StatusCode)
	}

	resp, body = a.do(http.MethodPost, "/v1/invitations/accept", bobCookie, map[string]any{"token": token})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accept = %d: %v", resp.StatusCode, body)
	}

	// --- Members visible; developer cannot manage members -------------------
	resp, body = a.do(http.MethodGet, "/v1/organizations/"+orgID+"/members", aliceCookie, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("members = %d: %v", resp.StatusCode, body)
	}
	members := body["items"].([]any)
	if len(members) != 2 {
		t.Fatalf("members = %d, want 2", len(members))
	}

	var bobUserID string
	for _, m := range members {
		mm := m.(map[string]any)
		if mm["email"] == "bob@example.test" {
			bobUserID = mm["user_id"].(string)
		}
	}
	if bobUserID == "" {
		t.Fatal("bob not found among members")
	}

	resp, _ = a.do(http.MethodPatch, "/v1/organizations/"+orgID+"/members/"+bobUserID, bobCookie,
		map[string]any{"role": "operator"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("developer changing roles = %d, want 403", resp.StatusCode)
	}

	// --- Owner promotes Bob to operator; operator permissions apply ---------
	resp, _ = a.do(http.MethodPatch, "/v1/organizations/"+orgID+"/members/"+bobUserID, aliceCookie,
		map[string]any{"role": "operator"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner role change = %d", resp.StatusCode)
	}

	// --- Tenant isolation through the API -----------------------------------
	resp, _ = a.do(http.MethodGet, "/v1/organizations/"+bobOrgID, aliceCookie, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("alice reading bob org = %d, want 403", resp.StatusCode)
	}

	// --- Owner-only management is enforced ----------------------------------
	// Alice is not a member of Bob's organization: management must fail.
	resp, _ = a.do(http.MethodPatch, "/v1/organizations/"+bobOrgID+"/members/"+bobUserID, aliceCookie,
		map[string]any{"role": "admin"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-org member update = %d, want 403", resp.StatusCode)
	}

	// --- Logout invalidates the session -------------------------------------
	resp, _ = a.do(http.MethodPost, "/v1/auth/logout", aliceCookie, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logout = %d", resp.StatusCode)
	}
	resp, _ = a.do(http.MethodGet, "/v1/auth/session", aliceCookie, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session after logout = %d, want 401", resp.StatusCode)
	}
}

// TestSessionCookieAttributes verifies the security attributes of the
// session cookie.
func TestSessionCookieAttributes(t *testing.T) {
	a := newAuthTestServer(t)
	resp, _ := a.do(http.MethodPost, "/v1/auth/signup", "", map[string]any{
		"name": "Cookie Check", "email": "cookie@example.test", "password": "cookie-pass-12",
	})
	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "agentos_session" {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("session cookie missing")
	}
	if !sessionCookie.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie SameSite = %v, want Lax", sessionCookie.SameSite)
	}
	if sessionCookie.Secure {
		t.Error("session cookie must not be Secure in development (plain HTTP)")
	}
	if sessionCookie.Path != "/" {
		t.Errorf("session cookie path = %q", sessionCookie.Path)
	}
	if sessionCookie.MaxAge <= 0 {
		t.Errorf("session cookie max age = %d, want positive", sessionCookie.MaxAge)
	}
}
