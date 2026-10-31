// Package auth implements password hashing, session management and the
// organization role-based access control matrix.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"

	"github.com/alexedwards/argon2id"
)

// argon2id parameters: conservative defaults balancing security and
// interactive latency (memory 64 MiB, 3 iterations, 1 thread).
var hashParams = &argon2id.Params{
	Memory:      64 * 1024,
	Iterations:  3,
	Parallelism: 1,
	SaltLength:  16,
	KeyLength:   32,
}

// HashPassword derives an Argon2id encoded hash for storage.
func HashPassword(password string) (string, error) {
	return argon2id.CreateHash(password, hashParams)
}

// VerifyPassword checks a password against a stored Argon2id hash.
func VerifyPassword(password, hash string) (bool, error) {
	return argon2id.ComparePasswordAndHash(password, hash)
}

// PasswordPolicyError describes a rejected password.
type PasswordPolicyError struct{ Message string }

func (e *PasswordPolicyError) Error() string { return e.Message }

// ValidatePassword enforces the platform password policy: at least 10
// characters with a mix of letters and digits. This is a baseline policy —
// enterprises configure SSO for stricter control in later milestones.
func ValidatePassword(password string) error {
	if len(password) < 10 {
		return &PasswordPolicyError{Message: "password must be at least 10 characters"}
	}
	if len(password) > 200 {
		return &PasswordPolicyError{Message: "password must be at most 200 characters"}
	}
	var hasLetter, hasDigit bool
	for _, r := range password {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return &PasswordPolicyError{Message: "password must contain both letters and digits"}
	}
	return nil
}

// NewSessionToken returns a fresh opaque session token. Only its SHA-256
// digest is persisted; the raw token lives exclusively in the cookie.
func NewSessionToken() (token string, tokenHash string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}
	token = hex.EncodeToString(b[:])
	return token, TokenHash(token), nil
}

// TokenHash returns the hex SHA-256 digest used for token comparison.
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewInvitationToken returns an opaque invitation token and its hash.
func NewInvitationToken() (token string, tokenHash string, err error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("generate invitation token: %w", err)
	}
	token = hex.EncodeToString(b[:])
	return token, TokenHash(token), nil
}

// NewAPIKey generates a platform API key with a stable, greppable prefix:
// agos_<random>. Returns the full key, its SHA-256 hash and the display
// prefix (first 14 characters).
func NewAPIKey() (key string, keyHash string, prefix string, err error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", "", fmt.Errorf("generate api key: %w", err)
	}
	random := hex.EncodeToString(b[:])
	key = "agos_" + random
	return key, TokenHash(key), key[:14], nil
}

// SanitizeName normalizes display names for storage.
func SanitizeName(name string) string {
	return strings.TrimSpace(name)
}

// Role is an organization role in the access control matrix.
type Role string

const (
	RoleOwner     Role = "owner"
	RoleAdmin     Role = "admin"
	RoleDeveloper Role = "developer"
	RoleOperator  Role = "operator"
	RoleAuditor   Role = "auditor"
	RoleMember    Role = "member"
)

// ParseRole validates a role string coming from input.
func ParseRole(s string) (Role, error) {
	r := Role(strings.ToLower(strings.TrimSpace(s)))
	switch r {
	case RoleOwner, RoleAdmin, RoleDeveloper, RoleOperator, RoleAuditor, RoleMember:
		return r, nil
	}
	return "", fmt.Errorf("unknown role %q", s)
}

// Permission is a discrete capability within an organization.
type Permission string

const (
	PermManageOrganization Permission = "organization.manage"
	PermManageMembers      Permission = "members.manage"
	PermViewAuditLog       Permission = "audit.read"
	PermManagePolicies     Permission = "policies.manage"
	PermViewCredentials    Permission = "credentials.read"
	PermManageCredentials  Permission = "credentials.manage"
	PermEditAgents         Permission = "agents.edit"
	PermViewAgents         Permission = "agents.read"
	PermManageDeployments  Permission = "deployments.manage"
	PermSubmitExecutions   Permission = "executions.submit"
	PermCancelExecutions   Permission = "executions.cancel"
	PermViewExecutions     Permission = "executions.read"
	PermDecideApprovals    Permission = "approvals.decide"
	PermManageAPIKeys      Permission = "api_keys.manage"
	PermManageBudgets      Permission = "budgets.manage"
	PermManageTools        Permission = "tools.manage"
	PermManageWebhooks     Permission = "webhooks.manage"
)

// rolePermissions is the canonical role → permission matrix. Read the
// matrix, not ad-hoc role string comparisons, in handlers.
var rolePermissions = map[Role]map[Permission]bool{
	RoleOwner:     allPermissions(),
	RoleAdmin:     allExcept(PermManageOrganization),
	RoleDeveloper: developerPermissions(),
	RoleOperator:  operatorPermissions(),
	RoleAuditor:   auditorPermissions(),
	RoleMember: {
		PermViewAgents:     true,
		PermViewExecutions: true,
	},
}

func allPermissions() map[Permission]bool {
	m := make(map[Permission]bool)
	for _, p := range []Permission{
		PermManageOrganization, PermManageMembers, PermViewAuditLog,
		PermManagePolicies, PermViewCredentials, PermManageCredentials,
		PermEditAgents, PermViewAgents, PermManageDeployments,
		PermSubmitExecutions, PermCancelExecutions, PermViewExecutions,
		PermDecideApprovals, PermManageAPIKeys, PermManageBudgets,
		PermManageTools, PermManageWebhooks,
	} {
		m[p] = true
	}
	return m
}

func allExcept(excluded ...Permission) map[Permission]bool {
	m := allPermissions()
	for _, e := range excluded {
		delete(m, e)
	}
	return m
}

func developerPermissions() map[Permission]bool {
	return map[Permission]bool{
		PermViewAgents:        true,
		PermEditAgents:        true,
		PermViewExecutions:    true,
		PermSubmitExecutions:  true,
		PermCancelExecutions:  true,
		PermManageDeployments: true,
		PermManageAPIKeys:     true,
		PermViewAuditLog:      false,
		PermManageCredentials: true, // rotate provider keys, cannot view them
		PermViewCredentials:   false,
	}
}

func operatorPermissions() map[Permission]bool {
	return map[Permission]bool{
		PermViewAgents:        true,
		PermViewExecutions:    true,
		PermSubmitExecutions:  true,
		PermCancelExecutions:  true,
		PermManageDeployments: true,
		PermDecideApprovals:   true,
		PermViewAuditLog:      true,
		// Operators run the platform; they do not touch secrets or code.
		PermEditAgents:        false,
		PermViewCredentials:   false,
		PermManageCredentials: false,
		PermManagePolicies:    false,
	}
}

func auditorPermissions() map[Permission]bool {
	return map[Permission]bool{
		PermViewAgents:     true,
		PermViewExecutions: true,
		PermViewAuditLog:   true,
		// Strictly read-only: no mutations of any kind, no credentials.
		PermViewCredentials: false,
	}
}

// Can reports whether the role grants the permission.
func (r Role) Can(p Permission) bool {
	return rolePermissions[r][p]
}
