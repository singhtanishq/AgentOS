//go:build integration

package tests

import (
	"net/http"
	"testing"
)

// TestDeploymentLifecycle deploys versions, submits executions against the
// active deployment and rolls back.
func TestDeploymentLifecycle(t *testing.T) {
	a := newAuthTestServer(t)

	// Owner + org + active agent with two published versions.
	resp, body := a.do(http.MethodPost, "/v1/auth/signup", "", map[string]any{
		"name": "Deploy Owner", "email": "deploy-owner@example.test",
		"password": "deploy-pass-99", "organization_name": "Deploy Org",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("signup = %d: %v", resp.StatusCode, body)
	}
	cookie := cookieFrom(resp)
	orgID := body["organizations"].([]any)[0].(map[string]any)["id"].(string)
	base := "/v1/organizations/" + orgID

	config := func(instructions string) map[string]any {
		return map[string]any{
			"instructions": instructions,
			"model":        map[string]any{"provider_type": "mock", "model_id": "mock-mini"},
			"tools":        []any{},
		}
	}

	// Create agent with v1.
	resp, body = a.do(http.MethodPost, base+"/agents", cookie, map[string]any{
		"name": "Deployed Agent", "configuration": config("v1 instructions"),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("agent create = %d: %v", resp.StatusCode, body)
	}
	agentID := body["id"].(string)

	// Add v2 and publish both.
	resp, body = a.do(http.MethodPost, base+"/agents/"+agentID+"/versions", cookie,
		map[string]any{"configuration": config("v2 instructions"), "changelog": "second"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("v2 create = %d: %v", resp.StatusCode, body)
	}
	for _, v := range []string{"1", "2"} {
		resp, _ = a.do(http.MethodPost, base+"/agents/"+agentID+"/versions/"+v+"/publish", cookie, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("publish v%s = %d", v, resp.StatusCode)
		}
	}
	// Activate the agent.
	resp, _ = a.do(http.MethodPut, base+"/agents/"+agentID+"/status", cookie,
		map[string]any{"status": "active"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("activate = %d", resp.StatusCode)
	}

	// Fetch version ids (ordered version DESC).
	resp, body = a.do(http.MethodGet, base+"/agents/"+agentID, cookie, nil)
	versions := body["versions"].([]any)
	v2ID := versions[0].(map[string]any)["id"].(string)
	v1ID := versions[1].(map[string]any)["id"].(string)

	// Deploy v1 to production, then v2 (two history entries).
	resp, body = a.do(http.MethodPost, base+"/agents/"+agentID+"/deploy", cookie,
		map[string]any{"agent_version_id": v1ID, "environment": "production"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("deploy v1 = %d: %v", resp.StatusCode, body)
	}
	if body["version"].(float64) != 1 {
		t.Fatalf("v1 deployment = %v", body)
	}
	resp, body = a.do(http.MethodPost, base+"/agents/"+agentID+"/deploy", cookie,
		map[string]any{"agent_version_id": v2ID, "environment": "production"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("deploy v2 = %d: %v", resp.StatusCode, body)
	}
	deploymentID := body["id"].(string)
	if body["version"].(float64) != 2 || body["status"] != "active" {
		t.Fatalf("deployment = %v", body)
	}

	// Deploying a draft version fails — create a draft first.
	resp, body = a.do(http.MethodPost, base+"/agents/"+agentID+"/versions", cookie,
		map[string]any{"configuration": config("v3 draft")})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("v3 create = %d", resp.StatusCode)
	}
	v3ID := body["id"].(string)
	resp, _ = a.do(http.MethodPost, base+"/agents/"+agentID+"/deploy", cookie,
		map[string]any{"agent_version_id": v3ID, "environment": "production"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("deploy draft = %d, want 409", resp.StatusCode)
	}

	// Rollback to v1: the old deployment retires, a new active one appears.
	resp, body = a.do(http.MethodPost, base+"/deployments/"+deploymentID+"/rollback", cookie, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("rollback = %d: %v", resp.StatusCode, body)
	}
	if body["version"].(float64) != 1 {
		t.Fatalf("rollback deployment version = %v, want 1", body["version"])
	}
	newDeploymentID := body["id"].(string)

	// History shows: v1 active (rolled back to), v2 rolled_back.
	resp, body = a.do(http.MethodGet, base+"/deployments?agent_id="+agentID, cookie, nil)
	history := body["items"].([]any)
	if len(history) != 3 {
		t.Fatalf("history = %d, want 3", len(history))
	}
	var activeCount, rolledBack int
	for _, item := range history {
		d := item.(map[string]any)
		switch d["status"] {
		case "active":
			activeCount++
		case "rolled_back":
			rolledBack++
		}
	}
	if activeCount != 1 || rolledBack != 1 {
		t.Fatalf("history statuses: active=%d rolled_back=%d", activeCount, rolledBack)
	}
	if history[0].(map[string]any)["version"].(float64) != 1 {
		t.Fatalf("latest history entry = %v, want v1 after rollback", history[0])
	}

	// Deactivate the remaining deployment.
	resp, _ = a.do(http.MethodPost, base+"/deployments/"+newDeploymentID+"/deactivate", cookie, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("deactivate = %d", resp.StatusCode)
	}
	// Double deactivation fails.
	resp, _ = a.do(http.MethodPost, base+"/deployments/"+newDeploymentID+"/deactivate", cookie, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("double deactivate = %d, want 404", resp.StatusCode)
	}
}
