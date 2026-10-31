-- AgentOS 000004 — model gateway (providers, encrypted credentials, model
-- registry with versioned pricing) and the tool registry (built-in, HTTP,
-- MCP).

CREATE TABLE model_providers (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    provider_type   text NOT NULL,
    display_name    text NOT NULL,
    -- Custom/self-hosted endpoints (OpenAI-compatible); empty for hosted APIs.
    base_url        text NOT NULL DEFAULT '',
    status          text NOT NULL DEFAULT 'active',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT model_providers_type_check CHECK (
        provider_type IN ('openai', 'anthropic', 'google', 'compatible', 'mock')
    ),
    CONSTRAINT model_providers_status_check CHECK (status IN ('active', 'disabled'))
);

CREATE UNIQUE INDEX model_providers_org_type_url_key
    ON model_providers (organization_id, provider_type, base_url);

CREATE TRIGGER model_providers_set_updated_at
    BEFORE UPDATE ON model_providers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE model_credentials (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    provider_id     uuid NOT NULL REFERENCES model_providers (id) ON DELETE CASCADE,
    label           text NOT NULL DEFAULT '',
    -- AES-256-GCM authenticated encryption of the provider API key.
    ciphertext      bytea NOT NULL,
    nonce           bytea NOT NULL,
    -- Non-secret fingerprint for identification in UIs and logs.
    key_fingerprint text NOT NULL,
    status          text NOT NULL DEFAULT 'active',
    rotated_at      timestamptz,
    revoked_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT model_credentials_nonce_len CHECK (octet_length(nonce) = 12),
    CONSTRAINT model_credentials_status_check CHECK (status IN ('active', 'revoked'))
);

CREATE INDEX model_credentials_provider_idx ON model_credentials (provider_id);
CREATE INDEX model_credentials_org_idx ON model_credentials (organization_id, created_at DESC);

CREATE TRIGGER model_credentials_set_updated_at
    BEFORE UPDATE ON model_credentials
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON COLUMN model_credentials.ciphertext IS 'Encrypted provider key (AES-256-GCM). Plaintext keys never touch the database.';

CREATE TABLE models (
    id                          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id             uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    provider_id                 uuid NOT NULL REFERENCES model_providers (id) ON DELETE CASCADE,
    -- Provider-native identifier, e.g. "gpt-4o-mini".
    model_id                    text NOT NULL,
    display_name                text NOT NULL,
    capabilities                jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Pricing in integer minor units (e.g. cents) per 1M tokens, versioned.
    input_price_per_mtok_minor  bigint NOT NULL DEFAULT 0,
    output_price_per_mtok_minor bigint NOT NULL DEFAULT 0,
    pricing_version             integer NOT NULL DEFAULT 1,
    status                      text NOT NULL DEFAULT 'available',
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT models_pricing_nonnegative CHECK (
        input_price_per_mtok_minor >= 0 AND output_price_per_mtok_minor >= 0
    ),
    CONSTRAINT models_status_check CHECK (status IN ('available', 'deprecated', 'disabled'))
);

CREATE UNIQUE INDEX models_provider_model_key ON models (provider_id, model_id);
CREATE INDEX models_org_idx ON models (organization_id, status);

CREATE TRIGGER models_set_updated_at
    BEFORE UPDATE ON models
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON COLUMN models.capabilities IS 'Provider-reported capability metadata: context window, tool support, streaming support.';

CREATE TABLE tools (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name            text NOT NULL,
    kind            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    input_schema    jsonb NOT NULL,
    output_schema   jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Tool-specific configuration: allowed hosts, redaction rules, fixtures.
    config          jsonb NOT NULL DEFAULT '{}'::jsonb,
    owner_id        uuid REFERENCES users (id) ON DELETE SET NULL,
    -- Permission level required for an agent to invoke this tool.
    required_role   text NOT NULL DEFAULT 'developer',
    status          text NOT NULL DEFAULT 'active',
    timeout_ms      integer NOT NULL DEFAULT 30000,
    version         integer NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tools_kind_check CHECK (
        kind IN ('builtin', 'http', 'mcp', 'database', 'email', 'crm')
    ),
    CONSTRAINT tools_timeout_positive CHECK (timeout_ms > 0),
    CONSTRAINT tools_status_check CHECK (status IN ('active', 'disabled')),
    CONSTRAINT tools_required_role_check CHECK (
        required_role IN ('operator', 'developer', 'admin')
    )
);

CREATE UNIQUE INDEX tools_org_name_key ON tools (organization_id, name);
CREATE INDEX tools_org_status_idx ON tools (organization_id, status);

CREATE TRIGGER tools_set_updated_at
    BEFORE UPDATE ON tools
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE mcp_connections (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name            text NOT NULL,
    transport       text NOT NULL DEFAULT 'streamable_http',
    url             text NOT NULL,
    -- Auth configuration metadata only; secrets live encrypted separately.
    auth            jsonb NOT NULL DEFAULT '{}'::jsonb,
    status          text NOT NULL DEFAULT 'active',
    health_status   text NOT NULL DEFAULT 'unknown',
    last_checked_at timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT mcp_connections_transport_check CHECK (transport IN ('streamable_http', 'sse')),
    CONSTRAINT mcp_connections_status_check CHECK (status IN ('active', 'disabled')),
    CONSTRAINT mcp_connections_health_check CHECK (health_status IN ('unknown', 'healthy', 'unreachable'))
);

CREATE UNIQUE INDEX mcp_connections_org_name_key ON mcp_connections (organization_id, name);

CREATE TRIGGER mcp_connections_set_updated_at
    BEFORE UPDATE ON mcp_connections
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
