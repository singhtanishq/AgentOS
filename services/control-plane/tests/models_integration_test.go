//go:build integration

package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// modelGatewayEnv signs up an org and returns a helper bound to it.
func modelGatewayEnv(t *testing.T, email string) (*authTestServer, string, string) {
	t.Helper()
	a := newAuthTestServer(t)
	resp, body := a.do(http.MethodPost, "/v1/auth/signup", "", map[string]any{
		"name": "Gateway Owner " + email, "email": email, "password": "gateway-pass-9",
		"organization_name": "Gateway " + email,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("signup = %d: %v", resp.StatusCode, body)
	}
	return a, cookieFrom(resp), body["organizations"].([]any)[0].(map[string]any)["id"].(string)
}

func TestModelGatewayFlow(t *testing.T) {
	a, cookie, orgID := modelGatewayEnv(t, "gateway@example.test")
	base := "/v1/organizations/" + orgID

	// --- Provider registration ------------------------------------------------
	resp, body := a.do(http.MethodPost, base+"/model-providers", cookie, map[string]any{
		"provider_type": "mock", "display_name": "AgentOS Mock",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("provider create = %d: %v", resp.StatusCode, body)
	}
	providerID := body["id"].(string)

	// Compatible providers require a base_url.
	resp, _ = a.do(http.MethodPost, base+"/model-providers", cookie, map[string]any{
		"provider_type": "compatible",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("compatible without base_url = %d, want 400", resp.StatusCode)
	}

	// --- Credentials: encrypted, never returned -------------------------------
	_, body, rawBody := a.doRaw(http.MethodPost, base+"/model-providers/"+providerID+"/credentials", cookie, map[string]any{
		"label": "primary", "api_key": "sk-mock-very-secret-123456",
	})
	if body["id"] == nil {
		t.Fatalf("credential create shape invalid: %v", body)
	}
	cred := body
	credentialID := cred["id"].(string)
	// The secret must not appear anywhere in the response body.
	if strings.Contains(rawBody, "sk-mock-very-secret-123456") {
		t.Fatal("credential response leaked the api key")
	}
	if cred["fingerprint"] == "" {
		t.Fatal("fingerprint missing from credential metadata")
	}

	// Credential listing shows metadata only.
	resp, body, listRaw := a.doRaw(http.MethodGet, base+"/model-providers/"+providerID+"/credentials", cookie, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("credential list = %d", resp.StatusCode)
	}
	if items := body["items"].([]any); len(items) != 1 {
		t.Fatalf("credentials = %d, want 1", len(items))
	}
	if strings.Contains(listRaw, "sk-mock-very-secret-123456") {
		t.Fatal("credential list leaked the api key")
	}

	// Internal reveal works only with the service token.
	revealURL := "/internal/credentials/" + credentialID + "?organization_id=" + orgID
	resp, body = a.do(http.MethodGet, revealURL, "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reveal without token = %d, want 401", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodGet, a.server.URL+revealURL, nil)
	req.Header.Set("Authorization", "Bearer internal-dev-token")
	revealResp, err := a.client.Do(req)
	if err != nil {
		t.Fatalf("reveal request: %v", err)
	}
	defer func() { _ = revealResp.Body.Close() }()
	if revealResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(revealResp.Body)
		t.Fatalf("reveal = %d: %s", revealResp.StatusCode, raw)
	}
	var revealed map[string]any
	if err := json.NewDecoder(revealResp.Body).Decode(&revealed); err != nil {
		t.Fatalf("decode reveal: %v", err)
	}
	if revealed["api_key"] != "sk-mock-very-secret-123456" {
		t.Fatalf("reveal api_key = %v", revealed["api_key"])
	}
	if revealed["provider_type"] != "mock" {
		t.Fatalf("reveal provider = %v", revealed["provider_type"])
	}

	// Cross-tenant reveal is impossible: another org's id yields 404.
	a2, _, org2 := modelGatewayEnv(t, "gateway2@example.test")
	req2, _ := http.NewRequest(http.MethodGet, a2.server.URL+"/internal/credentials/"+credentialID+"?organization_id="+org2, nil)
	req2.Header.Set("Authorization", "Bearer internal-dev-token")
	reveal2, err := a2.client.Do(req2)
	if err != nil {
		t.Fatalf("cross reveal request: %v", err)
	}
	defer func() { _ = reveal2.Body.Close() }()
	if reveal2.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant reveal = %d, want 404", reveal2.StatusCode)
	}

	// --- Model registry with pricing ------------------------------------------
	resp, body = a.do(http.MethodPost, base+"/models", cookie, map[string]any{
		"provider_id": providerID, "model_id": "mock-mini",
		"display_name":               "Mock Mini",
		"capabilities":               map[string]any{"context_window": 32000, "tools": true, "streaming": true},
		"input_price_per_mtok_minor": 15, "output_price_per_mtok_minor": 60,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("model create = %d: %v", resp.StatusCode, body)
	}
	resp, body = a.do(http.MethodGet, base+"/models", cookie, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("models list = %d", resp.StatusCode)
	}
	items := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("models = %d, want 1", len(items))
	}
	model := items[0].(map[string]any)
	if model["input_price_per_mtok_minor"].(float64) != 15 {
		t.Fatalf("model pricing = %v", model)
	}

	// --- Credential revocation --------------------------------------------------
	resp, _ = a.do(http.MethodDelete, base+"/model-providers/"+providerID+"/credentials/"+credentialID, cookie, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("credential revoke = %d", resp.StatusCode)
	}
	// Revoked credentials cannot be revealed.
	req3, _ := http.NewRequest(http.MethodGet, a.server.URL+revealURL, nil)
	req3.Header.Set("Authorization", "Bearer internal-dev-token")
	reveal3, err := a.client.Do(req3)
	if err != nil {
		t.Fatalf("post-revoke reveal: %v", err)
	}
	defer func() { _ = reveal3.Body.Close() }()
	if reveal3.StatusCode != http.StatusNotFound {
		t.Fatalf("revoked credential reveal = %d, want 404", reveal3.StatusCode)
	}
}
