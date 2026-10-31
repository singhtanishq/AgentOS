package organizations

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/audit"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/auth"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/tenant"
)

// Handler implements organization management endpoints.
type Handler struct {
	Store  *store.Store
	Logger *slog.Logger
}

type memberResponse struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

type invitationResponse struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	Token     string `json:"token,omitempty"` // returned once, at creation (no email transport in local mode)
	ExpiresAt string `json:"expires_at"`
}

// Create makes a new organization with the caller as owner.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	if err := tenant.ValidateName(req.Name); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	userID := auth.UserID(r.Context())

	var org tenant.OrganizationSummary
	err := h.Store.UserTx(r.Context(), userID, func(tx store.Tx) error {
		var err error
		org, err = tenant.CreateOrganizationTx(r.Context(), tx, userID, req.Name)
		return err
	})
	if err != nil {
		if strings.Contains(err.Error(), "could not derive a unique slug") {
			httpx.Error(w, r, http.StatusConflict, "slug_unavailable", err.Error())
			return
		}
		h.Logger.Error("org create failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to create organization")
		return
	}
	httpx.JSON(w, r, http.StatusCreated, org)
}

// List returns the caller's organizations.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserID(r.Context())
	rows, err := h.Store.Pool.Query(r.Context(),
		"SELECT organization_id, name, slug, role FROM agentos_list_user_orgs($1)", userID)
	if err != nil {
		h.Logger.Error("org list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list organizations")
		return
	}
	defer rows.Close()
	orgs := []tenant.OrganizationSummary{}
	for rows.Next() {
		var o tenant.OrganizationSummary
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.Role); err != nil {
			h.Logger.Error("org scan failed", slog.Any("error", err))
			httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list organizations")
			return
		}
		orgs = append(orgs, o)
	}
	if err := rows.Err(); err != nil {
		h.Logger.Error("org list rows failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list organizations")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": orgs})
}

// Get returns one organization (any member may view). The read runs inside
// the tenant transaction so row-level security scopes it correctly.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	var o tenant.OrganizationSummary
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		return tx.QueryRow(r.Context(),
			"SELECT id, name, slug FROM organizations WHERE id = $1 AND deleted_at IS NULL", orgID,
		).Scan(&o.ID, &o.Name, &o.Slug)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "organization not found")
		return
	}
	if err != nil {
		h.Logger.Error("org get failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to load organization")
		return
	}
	o.Role = string(auth.RoleFrom(r.Context()))
	httpx.JSON(w, r, http.StatusOK, o)
}

// ListMembers returns the organization roster (any member may view).
func (h *Handler) ListMembers(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	var members []memberResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT m.user_id, u.name, u.email, m.role
			FROM memberships m
			JOIN users u ON u.id = m.user_id
			WHERE m.organization_id = $1 AND u.deleted_at IS NULL
			ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END, u.name`, orgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		members = []memberResponse{}
		for rows.Next() {
			var m memberResponse
			if err := rows.Scan(&m.UserID, &m.Name, &m.Email, &m.Role); err != nil {
				return err
			}
			members = append(members, m)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("member list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list members")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": members})
}

// UpdateMemberRole changes a member's role (admins and owners).
func (h *Handler) UpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	var req struct {
		Role string `json:"role"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	role, err := auth.ParseRole(req.Role)
	if err != nil || role == auth.RoleOwner {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"role must be one of admin, developer, operator, auditor, member")
		return
	}
	targetID := chi.URLParam(r, "userID")

	// Only the owner may act on owners; admins act on everyone else.
	// The target lookup and the update share one tenant transaction.
	actorRole := auth.RoleFrom(r.Context())
	var updated memberResponse
	err = h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		var targetRole string
		scanErr := tx.QueryRow(r.Context(),
			"SELECT role FROM memberships WHERE organization_id = $1 AND user_id = $2",
			orgID, targetID).Scan(&targetRole)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return errMemberNotFound
		}
		if scanErr != nil {
			return scanErr
		}
		if targetRole == "owner" && actorRole != auth.RoleOwner {
			return errOwnerProtected
		}
		scanErr = tx.QueryRow(r.Context(), `
			UPDATE memberships SET role = $3
			WHERE organization_id = $1 AND user_id = $2
			RETURNING user_id, role`, orgID, targetID, string(role),
		).Scan(&updated.UserID, &updated.Role)
		if scanErr != nil {
			return scanErr
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "member.role_changed", ResourceType: "user", ResourceID: targetID,
			Result: "success", Metadata: map[string]any{"new_role": string(role), "old_role": targetRole},
		})
	})
	switch {
	case errors.Is(err, errMemberNotFound):
		httpx.Error(w, r, http.StatusNotFound, "not_found", "member not found")
		return
	case errors.Is(err, errOwnerProtected):
		httpx.Error(w, r, http.StatusForbidden, "forbidden", "only the owner can manage owner memberships")
		return
	case err != nil:
		h.Logger.Error("member role update failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to update member")
		return
	}
	httpx.JSON(w, r, http.StatusOK, updated)
}

// RemoveMember removes a membership (admins and owners; owners protected).
func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	targetID := chi.URLParam(r, "userID")
	if targetID == actorID {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "use organization leave flow instead")
		return
	}

	actorRole := auth.RoleFrom(r.Context())
	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		var targetRole string
		scanErr := tx.QueryRow(r.Context(),
			"SELECT role FROM memberships WHERE organization_id = $1 AND user_id = $2",
			orgID, targetID).Scan(&targetRole)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return errMemberNotFound
		}
		if scanErr != nil {
			return scanErr
		}
		if targetRole == "owner" && actorRole != auth.RoleOwner {
			return errOwnerProtected
		}
		tag, err := tx.Exec(r.Context(),
			"DELETE FROM memberships WHERE organization_id = $1 AND user_id = $2", orgID, targetID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errMemberNotFound
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "member.removed", ResourceType: "user", ResourceID: targetID, Result: "success",
		})
	})
	switch {
	case errors.Is(err, errMemberNotFound):
		httpx.Error(w, r, http.StatusNotFound, "not_found", "member not found")
		return
	case errors.Is(err, errOwnerProtected):
		httpx.Error(w, r, http.StatusForbidden, "forbidden", "only the owner can manage owner memberships")
		return
	case err != nil:
		h.Logger.Error("member removal failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to remove member")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]string{"status": "removed"})
}

// CreateInvitation invites an email with a role (admins and owners).
func (h *Handler) CreateInvitation(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if !strings.Contains(req.Email, "@") {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "a valid email is required")
		return
	}
	role, err := auth.ParseRole(req.Role)
	if err != nil || role == auth.RoleOwner {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed",
			"role must be one of admin, developer, operator, auditor, member")
		return
	}

	token, tokenHash, err := auth.NewInvitationToken()
	if err != nil {
		h.Logger.Error("invitation token generation failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to create invitation")
		return
	}

	var inv invitationResponse
	err = h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		err := tx.QueryRow(r.Context(), `
			INSERT INTO org_invitations (organization_id, email, role, token_hash, invited_by, expires_at)
			VALUES ($1, $2, $3, $4, $5, now() + interval '7 days')
			RETURNING id, email, role, status, expires_at::text`,
			orgID, req.Email, string(role), tokenHash, actorID,
		).Scan(&inv.ID, &inv.Email, &inv.Role, &inv.Status, &inv.ExpiresAt)
		if err != nil {
			return err
		}
		inv.Token = token // single disclosure — there is no email transport locally
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "invitation.created", ResourceType: "invitation", ResourceID: inv.ID,
			Result: "success", Metadata: map[string]any{"email": req.Email, "role": string(role)},
		})
	})
	if err != nil {
		if isUniqueViolation(err) {
			httpx.Error(w, r, http.StatusConflict, "invitation_exists", "an open invitation for this email already exists")
			return
		}
		h.Logger.Error("invitation creation failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to create invitation")
		return
	}
	httpx.JSON(w, r, http.StatusCreated, inv)
}

// ListInvitations returns open invitations (admins and owners).
func (h *Handler) ListInvitations(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	var invitations []invitationResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT id, email, role, status, expires_at::text
			FROM org_invitations
			WHERE organization_id = $1 AND status = 'pending'
			ORDER BY created_at DESC`, orgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		invitations = []invitationResponse{}
		for rows.Next() {
			var inv invitationResponse
			if err := rows.Scan(&inv.ID, &inv.Email, &inv.Role, &inv.Status, &inv.ExpiresAt); err != nil {
				return err
			}
			invitations = append(invitations, inv)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("invitation list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to list invitations")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": invitations})
}

// RevokeInvitation marks an open invitation revoked (admins and owners).
func (h *Handler) RevokeInvitation(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	actorID := auth.UserID(r.Context())
	invitationID := chi.URLParam(r, "invitationID")

	err := h.Store.OrgTx(r.Context(), orgID, actorID, func(tx store.Tx) error {
		tag, err := tx.Exec(r.Context(), `
			UPDATE org_invitations SET status = 'revoked'
			WHERE id = $1 AND organization_id = $2 AND status = 'pending'`,
			invitationID, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: orgID, ActorType: "user", ActorID: actorID,
			Action: "invitation.revoked", ResourceType: "invitation", ResourceID: invitationID, Result: "success",
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "invitation not found or already resolved")
		return
	}
	if err != nil {
		h.Logger.Error("invitation revocation failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to revoke invitation")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]string{"status": "revoked"})
}

// AcceptInvitation redeems an invitation token for the current user.
func (h *Handler) AcceptInvitation(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserID(r.Context())
	var req struct {
		Token string `json:"token"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil || strings.TrimSpace(req.Token) == "" {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invitation token is required")
		return
	}
	tokenHash := auth.TokenHash(strings.TrimSpace(req.Token))

	// The lookup runs as a SECURITY DEFINER function: the caller has no
	// organization context yet (they are not a member until redemption).
	var org tenant.OrganizationSummary
	err := h.Store.Pool.QueryRow(r.Context(),
		"SELECT organization_id, name, slug, role FROM agentos_resolve_invitation($1)",
		tokenHash,
	).Scan(&org.ID, &org.Name, &org.Slug, &org.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "invitation is invalid, expired or already used")
		return
	}
	if err != nil {
		h.Logger.Error("invitation lookup failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to accept invitation")
		return
	}

	err = h.Store.OrgTx(r.Context(), org.ID, userID, func(tx store.Tx) error {
		// Redeem atomically; idempotent for the same user.
		tag, err := tx.Exec(r.Context(), `
			UPDATE org_invitations SET status = 'accepted', accepted_at = now()
			WHERE token_hash = $1 AND status = 'pending'`, tokenHash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		_, err = tx.Exec(r.Context(), `
			INSERT INTO memberships (organization_id, user_id, role)
			VALUES ($1, $2, $3)
			ON CONFLICT (organization_id, user_id)
			DO UPDATE SET role = EXCLUDED.role, updated_at = now()`,
			org.ID, userID, org.Role)
		if err != nil {
			return err
		}
		return audit.Append(r.Context(), tx, audit.Entry{
			OrganizationID: org.ID, ActorType: "user", ActorID: userID,
			Action: "invitation.accepted", ResourceType: "invitation", Result: "success",
			Metadata: map[string]any{"role": org.Role},
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusNotFound, "not_found", "invitation is invalid, expired or already used")
		return
	}
	if err != nil {
		h.Logger.Error("invitation acceptance failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to accept invitation")
		return
	}
	httpx.JSON(w, r, http.StatusOK, org)
}

// Sentinel membership-management errors mapped to HTTP responses.
var (
	errMemberNotFound = errors.New("member not found")
	errOwnerProtected = errors.New("owner membership is protected")
)

// isUniqueViolation reports PostgreSQL unique constraint violations.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
