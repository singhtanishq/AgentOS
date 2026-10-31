package tenant

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/audit"
)

// OrganizationSummary is the org projection used across APIs.
type OrganizationSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	Role string `json:"role"`
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Slugify derives a URL-safe slug from an organization name.
func Slugify(name string) string {
	var b strings.Builder
	previousDash := true // suppress leading dash
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			previousDash = false
		case r == ' ' || r == '-' || r == '_' || r == '.':
			if !previousDash {
				b.WriteRune('-')
				previousDash = true
			}
		default:
			// Drop other characters (emoji, punctuation, non-ascii).
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "org"
	}
	return out
}

// ValidateName enforces basic organization naming rules.
func ValidateName(name string) error {
	trimmed := strings.TrimSpace(name)
	if len(trimmed) < 2 || len(trimmed) > 100 {
		return fmt.Errorf("organization name must be between 2 and 100 characters")
	}
	return nil
}

// CreateOrganizationTx creates an organization and its owner membership
// inside an existing transaction that already carries the user context.
// The new organization's RLS context is established before the membership
// insert so the row-level security WITH CHECK passes.
func CreateOrganizationTx(ctx context.Context, tx pgx.Tx, userID, name string) (OrganizationSummary, error) {
	name = strings.TrimSpace(name)
	base := Slugify(name)

	// Find a unique slug among non-deleted organizations.
	var slug string
	for attempt := 0; attempt < 50; attempt++ {
		candidate := base
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", base, attempt+1)
		}
		if !slugRe.MatchString(candidate) {
			continue
		}
		var exists bool
		if err := tx.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM organizations WHERE slug = $1 AND deleted_at IS NULL)",
			candidate).Scan(&exists); err != nil {
			return OrganizationSummary{}, err
		}
		if !exists {
			slug = candidate
			break
		}
	}
	if slug == "" {
		return OrganizationSummary{}, fmt.Errorf("could not derive a unique slug for %q", name)
	}

	// The id is generated client-side so the tenant context can be bound
	// before the insert: INSERT ... RETURNING must satisfy the SELECT row
	// security policy, which requires organization_id = agentos_current_org().
	orgID := uuid.New()
	var o OrganizationSummary
	if _, err := tx.Exec(ctx,
		"SELECT set_config('agentos.org_id', $1, true)", orgID); err != nil {
		return OrganizationSummary{}, fmt.Errorf("set org context: %w", err)
	}
	err := tx.QueryRow(ctx, `
		INSERT INTO organizations (id, name, slug) VALUES ($1, $2, $3)
		RETURNING id, name, slug`, orgID, name, slug,
	).Scan(&o.ID, &o.Name, &o.Slug)
	if err != nil {
		return OrganizationSummary{}, fmt.Errorf("insert organization: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO memberships (organization_id, user_id, role)
		VALUES ($1, $2, 'owner')`, o.ID, userID); err != nil {
		return OrganizationSummary{}, fmt.Errorf("insert owner membership: %w", err)
	}
	o.Role = string("owner")

	if err := audit.Append(ctx, tx, audit.Entry{
		OrganizationID: o.ID,
		ActorType:      "user",
		ActorID:        userID,
		Action:         "organization.created",
		ResourceType:   "organization",
		ResourceID:     o.ID,
		Result:         "success",
	}); err != nil {
		return OrganizationSummary{}, err
	}
	return o, nil
}
