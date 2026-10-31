//go:build integration

package tests

import (
	"net/http"
	"testing"
)

// validAgentConfig returns a configuration payload accepted by the schema.
func validAgentConfig() map[string]any {
	return map[string]any{
		"instructions": "You are a precise research assistant. Cite sources.",
		"model": map[string]any{
			"provider_type": "mock",
			"model_id":      "mock-mini",
			"temperature":   0.4,
			"max_tokens":    1024,
		},
		"tools":  []any{map[string]any{"name": "calculator"}},
		"limits": map[string]any{"max_steps": 8, "timeout_ms": 120000},
	}
}

// agentTestEnv signs up an owner with an organization and returns the
// session cookie and organization id.
func agentTestEnv(t *testing.T, email string) (cookie, orgID string) {
	t.Helper()
	a := newAuthTestServer(t)
	resp, body := a.do(http.MethodPost, "/v1/auth/signup", "", map[string]any{
		"name": "Owner " + email, "email": email, "password": "registry-pass-9",
		"organization_name": "Org " + email,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("signup = %d: %v", resp.StatusCode, body)
	}
	return cookieFrom(resp), body["organizations"].([]any)[0].(map[string]any)["id"].(string)
}

func TestAgentRegistryLifecycle(t *testing.T) {
	a := newAuthTestServer(t)
	cookie, orgID := agentTestEnv(t, "registry@example.test")
	base := "/v1/organizations/" + orgID + "/agents"

	// --- Create --------------------------------------------------------------
	resp, body := a.do(http.MethodPost, base, cookie, map[string]any{
		"name":          "Research Assistant",
		"description":   "Searches and summarizes sources",
		"configuration": validAgentConfig(),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d: %v", resp.StatusCode, body)
	}
	agentID := body["id"].(string)
	if body["status"] != "draft" || body["latest_version"].(float64) != 1 {
		t.Fatalf("created agent = %v", body)
	}

	// --- Validation is enforced ----------------------------------------------
	resp, _ = a.do(http.MethodPost, base, cookie, map[string]any{
		"name": "Broken",
		"configuration": map[string]any{
			"instructions": "",
			"model":        map[string]any{"provider_type": "nope", "model_id": "x"},
		},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid config = %d, want 400", resp.StatusCode)
	}

	// Duplicate names are rejected.
	resp, _ = a.do(http.MethodPost, base, cookie, map[string]any{
		"name": "Research Assistant", "configuration": validAgentConfig(),
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate name = %d, want 409", resp.StatusCode)
	}

	// --- Version management ---------------------------------------------------
	updated := validAgentConfig()
	updated["instructions"] = "v2 instructions with more rigor."
	resp, body = a.do(http.MethodPost, base+"/"+agentID+"/versions", cookie, map[string]any{
		"configuration": updated, "changelog": "tighter instructions",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create version = %d: %v", resp.StatusCode, body)
	}
	if body["version"].(float64) != 2 || body["status"] != "draft" {
		t.Fatalf("new version = %v", body)
	}

	// Publish v1; re-publish is rejected; v1 configuration stays intact.
	resp, _ = a.do(http.MethodPost, base+"/"+agentID+"/versions/1/publish", cookie, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish v1 = %d", resp.StatusCode)
	}
	resp, body = a.do(http.MethodPost, base+"/"+agentID+"/versions/1/publish", cookie, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("re-publish v1 = %d, want 409", resp.StatusCode)
	}
	resp, body = a.do(http.MethodGet, base+"/"+agentID, cookie, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get = %d", resp.StatusCode)
	}
	versions := body["versions"].([]any)
	if len(versions) != 2 {
		t.Fatalf("versions = %d, want 2", len(versions))
	}
	if versions[1].(map[string]any)["status"] != "published" {
		t.Fatalf("v1 status = %v, want published", versions[1])
	}
	// The DB immutability guard is exercised by TestAgentVersionImmutability.

	// --- Status lifecycle -----------------------------------------------------
	resp, body = a.do(http.MethodPut, base+"/"+agentID+"/status", cookie, map[string]any{"status": "active"})
	if resp.StatusCode != http.StatusOK || body["status"] != "active" {
		t.Fatalf("activate = %d %v", resp.StatusCode, body)
	}
	// draft → active → draft is invalid.
	resp, _ = a.do(http.MethodPut, base+"/"+agentID+"/status", cookie, map[string]any{"status": "draft"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("invalid transition = %d, want 409", resp.StatusCode)
	}
	resp, _ = a.do(http.MethodPut, base+"/"+agentID+"/status", cookie, map[string]any{"status": "paused"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pause = %d", resp.StatusCode)
	}

	// --- Search and list -------------------------------------------------------
	resp, body = a.do(http.MethodGet, base+"?q=research", cookie, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search = %d", resp.StatusCode)
	}
	if items := body["items"].([]any); len(items) != 1 {
		t.Fatalf("search results = %d, want 1", len(items))
	}
	resp, body = a.do(http.MethodGet, base+"?q=nonexistent-agent", cookie, nil)
	if items := body["items"].([]any); len(items) != 0 {
		t.Fatalf("no-match search results = %d, want 0", len(items))
	}

	// --- Update and soft delete -------------------------------------------------
	resp, body = a.do(http.MethodPatch, base+"/"+agentID, cookie, map[string]any{
		"description": "Now with citations and confidence scores.",
	})
	if resp.StatusCode != http.StatusOK || body["description"] != "Now with citations and confidence scores." {
		t.Fatalf("update = %d %v", resp.StatusCode, body)
	}
	resp, _ = a.do(http.MethodDelete, base+"/"+agentID, cookie, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	resp, _ = a.do(http.MethodGet, base+"/"+agentID, cookie, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", resp.StatusCode)
	}
}

// TestAgentRegistryCrossTenant verifies an agent is invisible from another
// organization through the API.
func TestAgentRegistryCrossTenant(t *testing.T) {
	a := newAuthTestServer(t)
	aliceCookie, aliceOrg := agentTestEnv(t, "rt-alice@example.test")
	_, bobOrg := agentTestEnv(t, "rt-bob@example.test")

	resp, body := a.do(http.MethodPost, "/v1/organizations/"+aliceOrg+"/agents", aliceCookie, map[string]any{
		"name": "Alice Private Agent", "configuration": validAgentConfig(),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d: %v", resp.StatusCode, body)
	}
	agentID := body["id"].(string)

	// Bob's session: sign in as bob.
	resp, body = a.do(http.MethodPost, "/v1/auth/login", "", map[string]any{
		"email": "rt-bob@example.test", "password": "registry-pass-9",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bob login = %d: %v", resp.StatusCode, body)
	}
	bobCookie := cookieFrom(resp)

	// Bob cannot see, modify or delete Alice's agent.
	resp, _ = a.do(http.MethodGet, "/v1/organizations/"+bobOrg+"/agents/"+agentID, bobCookie, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bob get foreign agent = %d, want 404", resp.StatusCode)
	}
	// Bob's request is well-formed in his own org; RLS makes the foreign
	// agent invisible, so the correct response is 404 (no existence leak).
	resp, _ = a.do(http.MethodDelete, "/v1/organizations/"+bobOrg+"/agents/"+agentID, bobCookie, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bob delete foreign agent = %d, want 404", resp.StatusCode)
	}
	// Bob cannot create versions for Alice's agent even with a forged path:
	// RLS makes the agent invisible, yielding 404 without an existence leak.
	resp, _ = a.do(http.MethodPost, "/v1/organizations/"+bobOrg+"/agents/"+agentID+"/versions", bobCookie,
		map[string]any{"configuration": validAgentConfig()})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bob version foreign agent = %d, want 404", resp.StatusCode)
	}
}
