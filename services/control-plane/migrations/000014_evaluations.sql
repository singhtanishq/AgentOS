-- AgentOS 000014 — evaluation datasets, cases, runs and results.

CREATE TABLE evaluation_datasets (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    created_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    deleted_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX evaluation_datasets_org_name_key
    ON evaluation_datasets (organization_id, name) WHERE deleted_at IS NULL;

CREATE TRIGGER evaluation_datasets_set_updated_at
    BEFORE UPDATE ON evaluation_datasets
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE evaluation_datasets IS 'Reusable test case collections for agent evaluation.';

CREATE TABLE evaluation_cases (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    dataset_id      uuid NOT NULL REFERENCES evaluation_datasets (id) ON DELETE CASCADE,
    name            text NOT NULL,
    -- {"query": "..."} or {"messages": [...]}
    input           jsonb NOT NULL,
    -- Reference data available to rules (e.g. {"keywords": [...]}).
    expected        jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Rule set: [{"type": "contains_any", "values": [...]}, {"type": "regex", "pattern": "..."}]
    rules           jsonb NOT NULL DEFAULT '[]'::jsonb,
    position        integer NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX evaluation_cases_dataset_idx ON evaluation_cases (dataset_id, position);

CREATE TRIGGER evaluation_cases_set_updated_at
    BEFORE UPDATE ON evaluation_cases
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE evaluation_runs (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    dataset_id       uuid NOT NULL REFERENCES evaluation_datasets (id) ON DELETE CASCADE,
    agent_id         uuid NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    agent_version_id uuid NOT NULL REFERENCES agent_versions (id) ON DELETE CASCADE,
    status           text NOT NULL DEFAULT 'running',
    pass_rate        numeric(5, 4),
    total_cases      integer NOT NULL DEFAULT 0,
    passed_cases     integer NOT NULL DEFAULT 0,
    error            jsonb,
    started_at       timestamptz NOT NULL DEFAULT now(),
    completed_at     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT evaluation_runs_status_check CHECK (status IN ('running', 'completed', 'failed'))
);

CREATE INDEX evaluation_runs_org_idx ON evaluation_runs (organization_id, started_at DESC);
CREATE INDEX evaluation_runs_dataset_idx ON evaluation_runs (dataset_id, started_at DESC);

CREATE TABLE evaluation_results (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    run_id         uuid NOT NULL REFERENCES evaluation_runs (id) ON DELETE CASCADE,
    case_id        uuid NOT NULL REFERENCES evaluation_cases (id) ON DELETE CASCADE,
    passed         boolean NOT NULL,
    score          numeric(5, 4) NOT NULL,
    output         text NOT NULL DEFAULT '',
    rule_results   jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX evaluation_results_run_idx ON evaluation_results (run_id);

COMMENT ON TABLE evaluation_results IS 'Per-case evaluation outcomes with rule-by-rule results. Scores from deterministic rules are objective; any future LLM-judge score must be labeled as probabilistic.';

-- Grants: the runtime executes evaluation runs.
GRANT SELECT ON evaluation_datasets, evaluation_cases TO agentos_worker;
GRANT SELECT, INSERT, UPDATE ON evaluation_runs TO agentos_worker;
GRANT SELECT, INSERT ON evaluation_results TO agentos_worker;
GRANT SELECT, INSERT, UPDATE, DELETE ON evaluation_datasets, evaluation_cases TO agentos_api;
GRANT SELECT, INSERT ON evaluation_runs, evaluation_results TO agentos_api;
