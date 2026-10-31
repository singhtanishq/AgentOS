-- AgentOS 000002 — identity, organizations, memberships, sessions, API keys.
-- Conventions: uuid PKs, timestamptz UTC timestamps, text + CHECK for enums,
-- soft deletion via deleted_at where marked, explicit named indexes.

CREATE TABLE organizations (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    slug        citext NOT NULL,
    settings    jsonb NOT NULL DEFAULT '{}'::jsonb,
    deleted_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT organizations_slug_format CHECK (slug ~ '^[a-z0-9][a-z0-9-]*$')
);

CREATE UNIQUE INDEX organizations_slug_key ON organizations (slug) WHERE deleted_at IS NULL;

CREATE TRIGGER organizations_set_updated_at
    BEFORE UPDATE ON organizations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE organizations IS 'Tenant root: every organization-owned resource references this row.';

CREATE TABLE users (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email          citext NOT NULL,
    name           text NOT NULL,
    password_hash  text NOT NULL,
    status         text NOT NULL DEFAULT 'active',
    last_login_at  timestamptz,
    deleted_at     timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_status_check CHECK (status IN ('active', 'suspended'))
);

CREATE UNIQUE INDEX users_email_key ON users (email) WHERE deleted_at IS NULL;

CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON COLUMN users.password_hash IS 'Argon2id encoded hash — never a plaintext password.';

CREATE TABLE memberships (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role            text NOT NULL DEFAULT 'member',
    invited_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT memberships_role_check CHECK (
        role IN ('owner', 'admin', 'developer', 'operator', 'auditor', 'member')
    )
);

-- A user holds at most one role per organization.
CREATE UNIQUE INDEX memberships_org_user_key ON memberships (organization_id, user_id);
CREATE INDEX memberships_user_idx ON memberships (user_id);

CREATE TRIGGER memberships_set_updated_at
    BEFORE UPDATE ON memberships
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE teams (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX teams_org_name_key ON teams (organization_id, name);

CREATE TRIGGER teams_set_updated_at
    BEFORE UPDATE ON teams
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE team_members (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    team_id         uuid NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX team_members_team_user_key ON team_members (team_id, user_id);
CREATE INDEX team_members_user_idx ON team_members (user_id);
CREATE INDEX team_members_org_idx ON team_members (organization_id);

CREATE TABLE sessions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- SHA-256 of the opaque session token; the raw token exists only in the cookie.
    token_hash    text NOT NULL,
    user_agent    text NOT NULL DEFAULT '',
    ip_address    inet,
    expires_at    timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    revoked_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX sessions_token_hash_key ON sessions (token_hash);
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expires_idx ON sessions (expires_at) WHERE revoked_at IS NULL;

CREATE TABLE org_invitations (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    email           citext NOT NULL,
    role            text NOT NULL,
    token_hash      text NOT NULL,
    invited_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    status          text NOT NULL DEFAULT 'pending',
    expires_at      timestamptz NOT NULL,
    accepted_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT org_invitations_role_check CHECK (
        role IN ('admin', 'developer', 'operator', 'auditor', 'member')
    ),
    CONSTRAINT org_invitations_status_check CHECK (
        status IN ('pending', 'accepted', 'revoked', 'expired')
    )
);

-- One open invitation per email per organization.
CREATE UNIQUE INDEX org_invitations_open_key
    ON org_invitations (organization_id, email) WHERE status = 'pending';
CREATE INDEX org_invitations_token_idx ON org_invitations (token_hash);

CREATE TRIGGER org_invitations_set_updated_at
    BEFORE UPDATE ON org_invitations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE api_keys (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name            text NOT NULL,
    -- SHA-256 of the full key; the raw key is shown exactly once at creation.
    key_hash        text NOT NULL,
    -- Non-secret prefix used for display and lookup, e.g. "agos_live_ab12".
    key_prefix      text NOT NULL,
    scopes          text[] NOT NULL DEFAULT '{}',
    status          text NOT NULL DEFAULT 'active',
    created_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    expires_at      timestamptz,
    last_used_at    timestamptz,
    revoked_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT api_keys_status_check CHECK (status IN ('active', 'revoked'))
);

CREATE UNIQUE INDEX api_keys_key_hash_key ON api_keys (key_hash);
CREATE INDEX api_keys_org_idx ON api_keys (organization_id, created_at DESC);
CREATE INDEX api_keys_prefix_idx ON api_keys (key_prefix);

CREATE TRIGGER api_keys_set_updated_at
    BEFORE UPDATE ON api_keys
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
