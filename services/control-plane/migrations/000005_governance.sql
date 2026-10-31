-- AgentOS 000005 — governance: policies, decision log, approvals.

CREATE TABLE policies (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    kind            text NOT NULL,
    -- Rego source or structured rule payload evaluated by OPA.
    content         text NOT NULL,
    version         integer NOT NULL DEFAULT 1,
    status          text NOT NULL DEFAULT 'active',
    created_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT policies_kind_check CHECK (
        kind IN ('authorization', 'approval', 'budget', 'model_usage')
    ),
    CONSTRAINT policies_status_check CHECK (status IN ('active', 'disabled'))
);

CREATE UNIQUE INDEX policies_org_name_version_key ON policies (organization_id, name, version);

CREATE TRIGGER policies_set_updated_at
    BEFORE UPDATE ON policies
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE policies IS 'Policy definitions evaluated by OPA. Content is versioned; old versions remain inspectable.';

CREATE TABLE policy_decisions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    execution_id    uuid,
    agent_id        uuid,
    principal_type  text NOT NULL,
    principal_id    uuid,
    action          text NOT NULL,
    resource_type   text NOT NULL DEFAULT '',
    resource_id     text NOT NULL DEFAULT '',
    decision        text NOT NULL,
    reason          text NOT NULL DEFAULT '',
    policy_id       uuid,
    policy_version  integer,
    -- SHA-256 of the evaluation input — auditable without storing sensitive payloads.
    input_digest    text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT policy_decisions_decision_check CHECK (decision IN ('allow', 'deny')),
    CONSTRAINT policy_decisions_principal_type_check CHECK (
        principal_type IN ('agent', 'user', 'api_key', 'system')
    )
);

CREATE INDEX policy_decisions_org_created_idx ON policy_decisions (organization_id, created_at DESC);
CREATE INDEX policy_decisions_execution_idx ON policy_decisions (execution_id) WHERE execution_id IS NOT NULL;
CREATE INDEX policy_decisions_agent_idx ON policy_decisions (agent_id) WHERE agent_id IS NOT NULL;

COMMENT ON TABLE policy_decisions IS 'Append-only authorization decision log. Every OPA decision with its inputs digest, reason and policy reference.';

CREATE TABLE approvals (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    execution_id    uuid NOT NULL,
    agent_id        uuid NOT NULL,
    action          text NOT NULL,
    resource        jsonb NOT NULL DEFAULT '{}'::jsonb,
    justification   text NOT NULL DEFAULT '',
    requested_by    uuid,
    status          text NOT NULL DEFAULT 'pending',
    assigned_role   text NOT NULL DEFAULT 'operator',
    approver_id     uuid REFERENCES users (id) ON DELETE SET NULL,
    decision_reason text NOT NULL DEFAULT '',
    expires_at      timestamptz NOT NULL DEFAULT now() + interval '24 hours',
    decided_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT approvals_status_check CHECK (
        status IN ('pending', 'approved', 'rejected', 'expired', 'cancelled')
    ),
    CONSTRAINT approvals_assigned_role_check CHECK (
        assigned_role IN ('admin', 'operator', 'developer')
    )
);

CREATE INDEX approvals_org_status_idx ON approvals (organization_id, status, created_at DESC);
CREATE INDEX approvals_execution_idx ON approvals (execution_id);
CREATE INDEX approvals_pending_expiry_idx ON approvals (expires_at) WHERE status = 'pending';

CREATE TRIGGER approvals_set_updated_at
    BEFORE UPDATE ON approvals
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE approvals IS 'Human approval requests raised by executions; the requesting execution waits in WAITING_APPROVAL until decided.';
