package auth

import (
	"strings"
	"testing"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	password := "correct horse battery 7"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("hash is not argon2id: %q", hash[:20])
	}
	ok, err := VerifyPassword(password, hash)
	if err != nil || !ok {
		t.Fatalf("VerifyPassword correct = (%v, %v), want true", ok, err)
	}
	ok, err = VerifyPassword("wrong password 7", hash)
	if err != nil {
		t.Fatalf("VerifyPassword wrong errored: %v", err)
	}
	if ok {
		t.Fatal("VerifyPassword accepted a wrong password")
	}
}

func TestValidatePasswordPolicy(t *testing.T) {
	cases := []struct {
		password string
		valid    bool
	}{
		{"short1a", false},                      // too short
		{"alllettersonly", false},               // no digits
		{"12345678901", false},                  // no letters
		{"GoodPass123", true},                   // valid
		{"another-Good-Pass-99", true},          // valid
		{"contains space 123", true},            // spaces allowed
		{strings.Repeat("a", 201) + "1", false}, // too long
	}
	for _, c := range cases {
		err := ValidatePassword(c.password)
		if c.valid && err != nil {
			t.Errorf("ValidatePassword(%q...) rejected: %v", c.password[:8], err)
		}
		if !c.valid && err == nil {
			t.Errorf("ValidatePassword(%q...) accepted", c.password[:8])
		}
	}
}

func TestSessionTokenUniquenessAndHash(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for range 100 {
		token, hash, err := NewSessionToken()
		if err != nil {
			t.Fatalf("NewSessionToken: %v", err)
		}
		if _, dup := seen[token]; dup {
			t.Fatal("duplicate session token")
		}
		seen[token] = struct{}{}
		if hash != TokenHash(token) {
			t.Fatal("token hash mismatch")
		}
		if len(hash) != 64 { // sha256 hex
			t.Fatalf("hash length = %d, want 64", len(hash))
		}
	}
}

func TestAPIKeyFormat(t *testing.T) {
	key, hash, prefix, err := NewAPIKey()
	if err != nil {
		t.Fatalf("NewAPIKey: %v", err)
	}
	if !strings.HasPrefix(key, "agos_") {
		t.Fatalf("api key prefix missing: %q", key)
	}
	if hash != TokenHash(key) {
		t.Fatal("api key hash mismatch")
	}
	if !strings.HasPrefix(key, prefix) || len(prefix) != 14 {
		t.Fatalf("prefix %q not a prefix of key %q", prefix, key)
	}
}

func TestRBACMatrix(t *testing.T) {
	cases := []struct {
		role  Role
		perm  Permission
		allow bool
	}{
		// Owner can do everything.
		{RoleOwner, PermManageOrganization, true},
		{RoleOwner, PermManageMembers, true},
		{RoleOwner, PermManageCredentials, true},
		// Admin can do everything except organization-level settings.
		{RoleAdmin, PermManageOrganization, false},
		{RoleAdmin, PermManageMembers, true},
		{RoleAdmin, PermEditAgents, true},
		// Developer builds but cannot manage members or view credentials.
		{RoleDeveloper, PermEditAgents, true},
		{RoleDeveloper, PermSubmitExecutions, true},
		{RoleDeveloper, PermManageMembers, false},
		{RoleDeveloper, PermViewCredentials, false},
		{RoleDeveloper, PermDecideApprovals, false},
		// Operator runs deployments and approvals but cannot edit agents.
		{RoleOperator, PermManageDeployments, true},
		{RoleOperator, PermDecideApprovals, true},
		{RoleOperator, PermEditAgents, false},
		{RoleOperator, PermViewCredentials, false},
		// Auditor is strictly read-only.
		{RoleAuditor, PermViewAuditLog, true},
		{RoleAuditor, PermViewAgents, true},
		{RoleAuditor, PermManagePolicies, false},
		{RoleAuditor, PermEditAgents, false},
		{RoleAuditor, PermDecideApprovals, false},
		// Member sees the basics.
		{RoleMember, PermViewAgents, true},
		{RoleMember, PermSubmitExecutions, false},
	}
	for _, c := range cases {
		if got := c.role.Can(c.perm); got != c.allow {
			t.Errorf("role %s perm %s = %v, want %v", c.role, c.perm, got, c.allow)
		}
	}
}

func TestParseRole(t *testing.T) {
	if r, err := ParseRole("ADMIN"); err != nil || r != RoleAdmin {
		t.Errorf("ParseRole(ADMIN) = (%v, %v)", r, err)
	}
	if _, err := ParseRole("superuser"); err == nil {
		t.Error("ParseRole(superuser) accepted an unknown role")
	}
}
