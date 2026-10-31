-- AgentOS 000007 — usage metering, budgets with atomic enforcement, alerts,
-- and the audit trail.

CREATE TABLE usage_records (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    execution_id        uuid NOT NULL REFERENCES executions (id) ON DELETE CASCADE,
    agent_id            uuid NOT NULL,
    provider            text NOT NULL,
    model               text NOT NULL,
    input_tokens        bigint NOT NULL DEFAULT 0,
    output_tokens       bigint NOT NULL DEFAULT 0,
    total_tokens        bigint NOT NULL DEFAULT 0,
    tool_calls          integer NOT NULL DEFAULT 0,
    duration_ms         integer NOT NULL DEFAULT 0,
    -- Provider-reported cost when the provider supplies one; NULL otherwise.
    cost_reported_minor bigint,
    -- Computed from versioned pricing; always distinguishable from reported.
    cost_estimate_minor bigint NOT NULL DEFAULT 0,
    currency            text NOT NULL DEFAULT 'USD',
    pricing_version     integer NOT NULL DEFAULT 0,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT usage_records_tokens_nonnegative CHECK (
        input_tokens >= 0 AND output_tokens >= 0 AND total_tokens >= 0
    ),
    CONSTRAINT usage_records_cost_nonnegative CHECK (
        cost_estimate_minor >= 0
        AND (cost_reported_minor IS NULL OR cost_reported_minor >= 0)
    )
);

CREATE INDEX usage_records_org_created_idx ON usage_records (organization_id, created_at DESC);
CREATE INDEX usage_records_agent_idx ON usage_records (agent_id, created_at DESC);
CREATE INDEX usage_records_execution_idx ON usage_records (execution_id);

COMMENT ON TABLE usage_records IS 'Append-only normalized token/cost metering rows. One row per execution (aggregated from provider usage).';

CREATE TABLE budgets (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    -- NULL means an organization-wide budget.
    agent_id            uuid REFERENCES agents (id) ON DELETE CASCADE,
    name                text NOT NULL,
    period              text NOT NULL,
    -- Limit and spend in integer minor units (e.g. cents).
    limit_minor         bigint NOT NULL,
    spent_minor         bigint NOT NULL DEFAULT 0,
    currency            text NOT NULL DEFAULT 'USD',
    thresholds          jsonb NOT NULL DEFAULT '{"50": "alert", "80": "alert", "100": "enforce"}'::jsonb,
    enforcement_action  text NOT NULL DEFAULT 'alert_only',
    status              text NOT NULL DEFAULT 'active',
    period_started_at   timestamptz NOT NULL DEFAULT now(),
    created_by          uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT budgets_period_check CHECK (period IN ('daily', 'weekly', 'monthly', 'total')),
    CONSTRAINT budgets_limit_positive CHECK (limit_minor > 0),
    CONSTRAINT budgets_spend_nonnegative CHECK (spent_minor >= 0),
    CONSTRAINT budgets_enforcement_check CHECK (enforcement_action IN ('alert_only', 'block')),
    CONSTRAINT budgets_status_check CHECK (status IN ('active', 'disabled'))
);

CREATE INDEX budgets_org_idx ON budgets (organization_id, status);
CREATE INDEX budgets_agent_idx ON budgets (agent_id) WHERE agent_id IS NOT NULL;

CREATE TRIGGER budgets_set_updated_at
    BEFORE UPDATE ON budgets
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE budgets IS 'Spending limits enforced by an atomic conditional UPDATE — concurrent executions cannot race past the limit.';

CREATE TABLE alerts (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    type             text NOT NULL,
    severity         text NOT NULL DEFAULT 'info',
    title            text NOT NULL,
    message          text NOT NULL DEFAULT '',
    resource_type    text NOT NULL DEFAULT '',
    resource_id      text NOT NULL DEFAULT '',
    execution_id     uuid,
    acknowledged_at  timestamptz,
    acknowledged_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT alerts_severity_check CHECK (severity IN ('info', 'warning', 'critical'))
);

CREATE INDEX alerts_org_created_idx ON alerts (organization_id, created_at DESC);
CREATE INDEX alerts_open_idx ON alerts (organization_id, created_at DESC) WHERE acknowledged_at IS NULL;

COMMENT ON TABLE alerts IS 'Operational alerts: budget thresholds, execution failures, provider errors, pending approvals.';

CREATE TABLE audit_logs (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    actor_type      text NOT NULL,
    actor_id        uuid,
    action          text NOT NULL,
    resource_type   text NOT NULL DEFAULT '',
    resource_id     text NOT NULL DEFAULT '',
    result          text NOT NULL DEFAULT 'success',
    request_id      text NOT NULL DEFAULT '',
    execution_id    uuid,
    metadata        jsonb NOT NULL DEFAULT '{}'::jsonb,
    ip_address      inet,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT audit_logs_actor_type_check CHECK (actor_type IN ('user', 'api_key', 'agent', 'system')),
    CONSTRAINT audit_logs_result_check CHECK (result IN ('success', 'denied', 'error'))
);

CREATE INDEX audit_logs_org_created_idx ON audit_logs (organization_id, created_at DESC);
CREATE INDEX audit_logs_actor_idx ON audit_logs (actor_id) WHERE actor_id IS NOT NULL;
CREATE INDEX audit_logs_resource_idx ON audit_logs (resource_type, resource_id);

COMMENT ON TABLE audit_logs IS 'Append-only security-relevant action log: who did what to which resource, with which result.';
