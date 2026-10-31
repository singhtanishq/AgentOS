-- AgentOS 000003 — agent registry, immutable versions, deployments, identities.

CREATE TABLE agents (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    -- Lifecycle: draft → active → paused → suspended → archived.
    status          text NOT NULL DEFAULT 'draft',
    created_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    deleted_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT agents_status_check CHECK (
        status IN ('draft', 'active', 'paused', 'suspended', 'archived')
    )
);

CREATE UNIQUE INDEX agents_org_name_key ON agents (organization_id, name) WHERE deleted_at IS NULL;
-- Composite tenant index powering registry listings.
CREATE INDEX agents_org_status_idx ON agents (organization_id, status) WHERE deleted_at IS NULL;

CREATE TRIGGER agents_set_updated_at
    BEFORE UPDATE ON agents
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE agents IS 'Registered agents. The runnable definition lives in immutable agent_versions.';

CREATE TABLE agent_versions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Denormalized from agents for tenant indexing and row-level security.
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    agent_id        uuid NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    -- 1-based, monotonic per agent.
    version         integer NOT NULL,
    -- Full immutable configuration snapshot: instructions, model config,
    -- tool bindings, execution limits, budget hints.
    configuration  jsonb NOT NULL,
    changelog      text NOT NULL DEFAULT '',
    status         text NOT NULL DEFAULT 'draft',
    created_by     uuid REFERENCES users (id) ON DELETE SET NULL,
    published_at   timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT agent_versions_version_positive CHECK (version > 0),
    CONSTRAINT agent_versions_status_check CHECK (status IN ('draft', 'published', 'archived')),
    -- Published versions are immutable by construction: the configuration
    -- must be non-null and the row is never updated after publication
    -- (enforced by grants + application rules + trigger below).
    CONSTRAINT agent_versions_configuration_present CHECK (configuration IS NOT NULL)
);

CREATE UNIQUE INDEX agent_versions_agent_version_key ON agent_versions (agent_id, version);
CREATE INDEX agent_versions_agent_status_idx ON agent_versions (agent_id, status);

-- Guard: a published version never transitions back or changes config.
CREATE OR REPLACE FUNCTION agent_versions_guard() RETURNS trigger AS $$
BEGIN
    IF OLD.status = 'published' THEN
        IF NEW.status <> 'published' OR NEW.configuration IS DISTINCT FROM OLD.configuration THEN
            RAISE EXCEPTION 'agent version % is published and immutable', OLD.version;
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER agent_versions_immutable_guard
    BEFORE UPDATE ON agent_versions
    FOR EACH ROW EXECUTE FUNCTION agent_versions_guard();

COMMENT ON TABLE agent_versions IS 'Immutable agent configuration snapshots. Historical executions reference the exact version used.';

CREATE TABLE deployments (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    agent_id         uuid NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    agent_version_id uuid NOT NULL REFERENCES agent_versions (id) ON DELETE RESTRICT,
    environment      text NOT NULL,
    status           text NOT NULL DEFAULT 'active',
    created_by       uuid REFERENCES users (id) ON DELETE SET NULL,
    deactivated_at   timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT deployments_environment_check CHECK (environment IN ('development', 'staging', 'production')),
    CONSTRAINT deployments_status_check CHECK (status IN ('active', 'inactive', 'failed', 'rolled_back'))
);

-- One active deployment per agent per environment.
CREATE UNIQUE INDEX deployments_active_key
    ON deployments (agent_id, environment) WHERE status = 'active';
CREATE INDEX deployments_org_idx ON deployments (organization_id, created_at DESC);
CREATE INDEX deployments_agent_idx ON deployments (agent_id, created_at DESC);

CREATE TRIGGER deployments_set_updated_at
    BEFORE UPDATE ON deployments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE agent_identities (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id       uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    agent_id              uuid NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    name                  text NOT NULL,
    status                text NOT NULL DEFAULT 'active',
    -- SHA-256 of the identity secret; raw secret shown once at creation.
    credential_hash       text NOT NULL,
    credential_prefix     text NOT NULL,
    credential_expires_at timestamptz,
    last_used_at          timestamptz,
    revoked_at            timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT agent_identities_status_check CHECK (status IN ('active', 'suspended', 'revoked'))
);

CREATE UNIQUE INDEX agent_identities_credential_hash_key ON agent_identities (credential_hash);
CREATE INDEX agent_identities_agent_idx ON agent_identities (agent_id);
CREATE INDEX agent_identities_org_status_idx ON agent_identities (organization_id, status);

CREATE TRIGGER agent_identities_set_updated_at
    BEFORE UPDATE ON agent_identities
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE agent_identities IS 'Per-agent credentials with independent lifecycle: suspension/revocation must be possible without touching the agent row.';
