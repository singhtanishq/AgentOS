-- AgentOS 000006 — execution plane: executions, lifecycle events, traces and
-- spans. Append-heavy tables; UPDATE paths are restricted to explicit,
-- guarded state transitions performed by workers.

CREATE TABLE executions (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id      uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    agent_id             uuid NOT NULL REFERENCES agents (id) ON DELETE RESTRICT,
    agent_version_id     uuid NOT NULL REFERENCES agent_versions (id) ON DELETE RESTRICT,
    deployment_id        uuid,
    identity_id          uuid,
    status               text NOT NULL DEFAULT 'queued',
    input                jsonb NOT NULL DEFAULT '{}'::jsonb,
    output               jsonb,
    error                jsonb,
    -- Client-supplied dedupe key; unique per organization.
    idempotency_key      text,
    trace_id             uuid,
    worker_id            text NOT NULL DEFAULT '',
    step_count           integer NOT NULL DEFAULT 0,
    tool_call_count      integer NOT NULL DEFAULT 0,
    prompt_tokens        bigint NOT NULL DEFAULT 0,
    completion_tokens    bigint NOT NULL DEFAULT 0,
    total_tokens         bigint NOT NULL DEFAULT 0,
    -- Computed from the pricing table at usage time; flagged as estimate.
    cost_estimate_minor  bigint NOT NULL DEFAULT 0,
    pricing_version      integer NOT NULL DEFAULT 0,
    cancel_requested     boolean NOT NULL DEFAULT false,
    started_at           timestamptz,
    completed_at         timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT executions_status_check CHECK (
        status IN ('queued', 'running', 'waiting_approval', 'succeeded', 'failed', 'cancelled', 'timed_out')
    ),
    CONSTRAINT executions_terminal_consistency CHECK (
        (status IN ('succeeded', 'failed', 'cancelled', 'timed_out')) = (completed_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX executions_org_idempotency_key
    ON executions (organization_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX executions_org_created_idx ON executions (organization_id, created_at DESC);
CREATE INDEX executions_org_status_idx ON executions (organization_id, status);
CREATE INDEX executions_agent_created_idx ON executions (agent_id, created_at DESC);
CREATE INDEX executions_queued_idx ON executions (created_at) WHERE status = 'queued';
CREATE INDEX executions_trace_idx ON executions (trace_id) WHERE trace_id IS NOT NULL;

CREATE TRIGGER executions_set_updated_at
    BEFORE UPDATE ON executions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON COLUMN executions.agent_version_id IS 'Exact agent version used — immutable historical reference.';
COMMENT ON COLUMN executions.cost_estimate_minor IS 'Estimated cost in minor units; never presented as a provider-reported amount.';

CREATE TABLE execution_events (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    execution_id    uuid NOT NULL REFERENCES executions (id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    -- 0-based ordering within the execution.
    seq             integer NOT NULL,
    type            text NOT NULL,
    -- Redacted event payload (never credentials or raw prompts).
    payload         jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT execution_events_seq_nonnegative CHECK (seq >= 0)
);

CREATE UNIQUE INDEX execution_events_execution_seq_key ON execution_events (execution_id, seq);
CREATE INDEX execution_events_org_created_idx ON execution_events (organization_id, created_at DESC);

COMMENT ON TABLE execution_events IS 'Append-only structured execution event stream: lifecycle, model calls, tool calls, policy decisions, usage.';

CREATE TABLE traces (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    execution_id    uuid NOT NULL REFERENCES executions (id) ON DELETE CASCADE,
    root_span_name  text NOT NULL DEFAULT 'invoke_agent',
    status          text NOT NULL DEFAULT 'unset',
    started_at      timestamptz NOT NULL,
    ended_at        timestamptz,
    duration_ms     integer,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT traces_status_check CHECK (status IN ('unset', 'ok', 'error'))
);

CREATE INDEX traces_org_created_idx ON traces (organization_id, created_at DESC);
CREATE INDEX traces_execution_idx ON traces (execution_id);

CREATE TABLE spans (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    trace_id        uuid NOT NULL REFERENCES traces (id) ON DELETE CASCADE,
    parent_span_id  uuid,
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    execution_id    uuid NOT NULL REFERENCES executions (id) ON DELETE CASCADE,
    name            text NOT NULL,
    kind            text NOT NULL DEFAULT 'internal',
    status          text NOT NULL DEFAULT 'unset',
    started_at      timestamptz NOT NULL,
    ended_at        timestamptz,
    duration_ms     integer,
    -- Redacted telemetry attributes (model ids, tool names, token counts).
    attributes      jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT spans_kind_check CHECK (kind IN ('internal', 'client', 'server', 'producer', 'consumer')),
    CONSTRAINT spans_status_check CHECK (status IN ('unset', 'ok', 'error'))
);

CREATE INDEX spans_trace_idx ON spans (trace_id, started_at);
CREATE INDEX spans_execution_idx ON spans (execution_id);

COMMENT ON TABLE spans IS 'OpenTelemetry span records forming the execution timeline shown in the trace explorer.';
