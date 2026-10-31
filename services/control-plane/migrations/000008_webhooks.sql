-- AgentOS 000008 — outbound webhooks with delivery tracking and retry state.

CREATE TABLE webhooks (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    url              text NOT NULL,
    description      text NOT NULL DEFAULT '',
    -- Event types delivered, e.g. execution.completed, approval.requested.
    events           text[] NOT NULL DEFAULT '{}',
    -- HMAC-SHA256 signing secret, AES-256-GCM encrypted at rest.
    secret_encrypted bytea NOT NULL,
    secret_nonce     bytea NOT NULL,
    status           text NOT NULL DEFAULT 'active',
    created_by       uuid REFERENCES users (id) ON DELETE SET NULL,
    last_delivery_at timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT webhooks_url_format CHECK (url ~ '^https?://'),
    CONSTRAINT webhooks_status_check CHECK (status IN ('active', 'disabled')),
    CONSTRAINT webhooks_secret_nonce_len CHECK (octet_length(secret_nonce) = 12)
);

CREATE INDEX webhooks_org_idx ON webhooks (organization_id, created_at DESC);

CREATE TRIGGER webhooks_set_updated_at
    BEFORE UPDATE ON webhooks
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON COLUMN webhooks.secret_encrypted IS 'Encrypted webhook signing secret; never returned to the frontend.';

CREATE TABLE webhook_deliveries (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    webhook_id      uuid NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    event_type      text NOT NULL,
    event_id        uuid NOT NULL,
    status          text NOT NULL DEFAULT 'pending',
    attempt         integer NOT NULL DEFAULT 0,
    response_status integer,
    response_body   text NOT NULL DEFAULT '',
    next_retry_at   timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    delivered_at    timestamptz,
    CONSTRAINT webhook_deliveries_status_check CHECK (
        status IN ('pending', 'delivered', 'failed', 'exhausted')
    )
);

CREATE INDEX webhook_deliveries_webhook_idx ON webhook_deliveries (webhook_id, created_at DESC);
CREATE INDEX webhook_deliveries_pending_idx
    ON webhook_deliveries (next_retry_at) WHERE status = 'pending';

COMMENT ON TABLE webhook_deliveries IS 'Delivery attempts with exponential backoff state; exhausted deliveries are marked for operator attention.';
