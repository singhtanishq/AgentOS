package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/audit"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/tenant"
)

// Handler implements the /v1/auth endpoints.
type Handler struct {
	Store         *store.Store
	CookieName    string
	SecureCookies bool
	Logger        *slog.Logger
}

// signUpRequest is the signup payload. An optional organization name
// creates the caller's first workspace with owner membership.
type signUpRequest struct {
	Name             string `json:"name"`
	Email            string `json:"email"`
	Password         string `json:"password"`
	OrganizationName string `json:"organization_name"`
}

type userResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

type organizationSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	Role string `json:"role"`
}

type authResponse struct {
	User          userResponse          `json:"user"`
	Organizations []organizationSummary `json:"organizations"`
}

// SignUp creates a user (and optionally their first organization), then
// establishes a session.
func (h *Handler) SignUp(w http.ResponseWriter, r *http.Request) {
	var req signUpRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	req.Name = SanitizeName(req.Name)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.OrganizationName = strings.TrimSpace(req.OrganizationName)

	if req.Name == "" || req.Email == "" || !strings.Contains(req.Email, "@") {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "name and a valid email are required")
		return
	}
	if err := ValidatePassword(req.Password); err != nil {
		var perr *PasswordPolicyError
		if errors.As(err, &perr) {
			httpx.Error(w, r, http.StatusBadRequest, "validation_failed", perr.Message)
			return
		}
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid password")
		return
	}

	hash, err := HashPassword(req.Password)
	if err != nil {
		h.Logger.Error("hash password failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to process credentials")
		return
	}

	var user userResponse
	var orgs []organizationSummary
	err = h.Store.UserTx(r.Context(), "", func(tx store.Tx) error {
		row := tx.QueryRow(r.Context(), `
			INSERT INTO users (name, email, password_hash)
			VALUES ($1, $2, $3)
			RETURNING id, name, email, created_at`,
			req.Name, req.Email, hash)
		if err := row.Scan(&user.ID, &user.Name, &user.Email, &user.CreatedAt); err != nil {
			return err
		}
		if req.OrganizationName != "" {
			o, err := tenant.CreateOrganizationTx(r.Context(), tx, user.ID, req.OrganizationName)
			if err != nil {
				return err
			}
			orgs = append(orgs, organizationSummary{ID: o.ID, Name: o.Name, Slug: o.Slug, Role: o.Role})
		}
		return nil
	})
	if err != nil {
		switch constraintName(err) {
		case "users_email_key":
			httpx.Error(w, r, http.StatusConflict, "email_taken", "an account with this email already exists")
			return
		case "organizations_slug_key":
			httpx.Error(w, r, http.StatusConflict, "name_unavailable",
				"an organization with a similar name already exists — choose another name")
			return
		}
		h.Logger.Error("signup failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to create account")
		return
	}

	if err := h.establishSession(w, r, user.ID); err != nil {
		h.Logger.Error("session creation failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to establish session")
		return
	}

	h.auditAsync(r, audit.Entry{ActorType: "user", ActorID: user.ID, Action: "auth.signup", Result: "success"})
	if len(orgs) > 0 {
		h.auditAsync(r, audit.Entry{OrganizationID: orgs[0].ID, ActorType: "user", ActorID: user.ID,
			Action: "organization.created", ResourceType: "organization", ResourceID: orgs[0].ID, Result: "success"})
	}
	httpx.JSON(w, r, http.StatusCreated, authResponse{User: user, Organizations: orgs})
}

// Login verifies credentials and establishes a session.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "validation_failed", "invalid request body")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	var userID, passwordHash string
	var status, name string
	err := h.Store.Pool.QueryRow(r.Context(), `
		SELECT id, password_hash, status, name FROM users WHERE email = $1 AND deleted_at IS NULL`,
		req.Email,
	).Scan(&userID, &passwordHash, &status, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		h.invalidCredentials(w, r)
		return
	}
	if err != nil {
		h.Logger.Error("login query failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to authenticate")
		return
	}
	if status != "active" {
		httpx.Error(w, r, http.StatusForbidden, "account_suspended", "this account is suspended")
		return
	}
	ok, err := VerifyPassword(req.Password, passwordHash)
	if err != nil || !ok {
		// Constant-shape response: never reveal which factor failed.
		h.invalidCredentials(w, r)
		return
	}

	if _, err := h.Store.Pool.Exec(r.Context(),
		"UPDATE users SET last_login_at = now() WHERE id = $1", userID); err != nil {
		h.Logger.Warn("last_login update failed", slog.Any("error", err))
	}

	if err := h.establishSession(w, r, userID); err != nil {
		h.Logger.Error("session creation failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to establish session")
		return
	}

	h.auditAsync(r, audit.Entry{ActorType: "user", ActorID: userID, Action: "auth.login", Result: "success"})

	resp := authResponse{User: userResponse{ID: userID, Name: name, Email: req.Email}}
	resp.Organizations, err = h.listOrganizations(r, userID)
	if err != nil {
		h.Logger.Error("org listing failed", slog.Any("error", err))
	}
	httpx.JSON(w, r, http.StatusOK, resp)
}

// Session returns the current identity and their organizations.
func (h *Handler) Session(w http.ResponseWriter, r *http.Request) {
	userID := UserID(r.Context())
	var user userResponse
	err := h.Store.Pool.QueryRow(r.Context(),
		"SELECT id, name, email, created_at FROM users WHERE id = $1 AND deleted_at IS NULL", userID,
	).Scan(&user.ID, &user.Name, &user.Email, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthorized", "account no longer exists")
		return
	}
	if err != nil {
		h.Logger.Error("session lookup failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to load session")
		return
	}
	orgs, err := h.listOrganizations(r, userID)
	if err != nil {
		h.Logger.Error("org listing failed", slog.Any("error", err))
		orgs = []organizationSummary{}
	}
	httpx.JSON(w, r, http.StatusOK, authResponse{User: user, Organizations: orgs})
}

// Logout revokes the current session and clears the cookie.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if sessionID := SessionID(r.Context()); sessionID != "" {
		if err := RevokeSession(r.Context(), h.Store, sessionID); err != nil {
			h.Logger.Error("logout revocation failed", slog.Any("error", err))
		}
		userID := UserID(r.Context())
		h.auditAsync(r, audit.Entry{ActorType: "user", ActorID: userID, Action: "auth.logout", Result: "success"})
	}
	ClearSessionCookie(w, h.CookieName, h.SecureCookies)
	httpx.JSON(w, r, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (h *Handler) establishSession(w http.ResponseWriter, r *http.Request, userID string) error {
	token, _, err := CreateSession(r.Context(), h.Store, userID, r.UserAgent(), ClientIP(r))
	if err != nil {
		return err
	}
	SetSessionCookie(w, h.CookieName, token, sessionTTL, h.SecureCookies)
	return nil
}

func (h *Handler) listOrganizations(r *http.Request, userID string) ([]organizationSummary, error) {
	rows, err := h.Store.Pool.Query(r.Context(),
		"SELECT organization_id, name, slug, role FROM agentos_list_user_orgs($1)", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	orgs := []organizationSummary{}
	for rows.Next() {
		var o organizationSummary
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.Role); err != nil {
			return nil, err
		}
		orgs = append(orgs, organizationSummary{ID: o.ID, Name: o.Name, Slug: o.Slug, Role: o.Role})
	}
	return orgs, rows.Err()
}

func (h *Handler) invalidCredentials(w http.ResponseWriter, r *http.Request) {
	httpx.Error(w, r, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
}

// auditAsync records an audit entry without blocking the response.
// audit_logs is organization-scoped: when the entry has no organization,
// the actor's first organization is used so authentication events remain
// attributable within a workspace.
func (h *Handler) auditAsync(r *http.Request, e audit.Entry) {
	go func(ctx context.Context) {
		if e.OrganizationID == "" && e.ActorID != "" {
			var orgID string
			rowErr := h.Store.Pool.QueryRow(ctx,
				"SELECT organization_id FROM agentos_list_user_orgs($1) LIMIT 1", e.ActorID,
			).Scan(&orgID)
			if rowErr == nil && orgID != "" {
				e.OrganizationID = orgID
			}
		}
		var err error
		if e.OrganizationID != "" {
			err = h.Store.OrgTx(ctx, e.OrganizationID, e.ActorID, func(tx store.Tx) error {
				return audit.Append(ctx, tx, e)
			})
		}
		if err != nil {
			h.Logger.Warn("audit write failed", slog.Any("error", err))
		}
	}(context.WithoutCancel(r.Context()))
}

// constraintName extracts the violated constraint from a PostgreSQL
// unique violation, or "" for other errors.
func constraintName(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName
	}
	return ""
}
