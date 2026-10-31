-- AgentOS 000009 — row-level security, least-privilege roles and grants.
--
-- Defense-in-depth tenancy: application-level enforcement remains the
-- first line of defense; these policies ensure that even a buggy query
-- cannot cross organization boundaries.
--
-- Roles:
--   agentos_api    — control plane (full application surface)
--   agentos_worker — execution plane (narrow, execution-focused surface)
--
-- Both roles are non-owners, so row-level security applies to them.
-- Context is provided per transaction via:
--   SELECT set_config('agentos.org_id',  '<uuid>', true)
--   SELECT set_config('agentos.user_id', '<uuid>', true)
-- Unset context yields NULL comparisons: no rows are visible or writable
-- on organization-scoped tables (default deny).

-- ----------------------------------------------------------------
-- Roles (local development password; rotate for non-local deploys)
-- ----------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'agentos_api') THEN
        CREATE ROLE agentos_api LOGIN PASSWORD 'agentos_app_dev';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'agentos_worker') THEN
        CREATE ROLE agentos_worker LOGIN PASSWORD 'agentos_app_dev';
    END IF;
END
$$;

REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO agentos_api, agentos_worker;

-- ----------------------------------------------------------------
-- Context helpers
-- ----------------------------------------------------------------
CREATE OR REPLACE FUNCTION agentos_current_org() RETURNS uuid
LANGUAGE sql STABLE AS $$
    SELECT current_setting('agentos.org_id', true)::uuid
$$;

CREATE OR REPLACE FUNCTION agentos_current_user_id() RETURNS uuid
LANGUAGE sql STABLE AS $$
    SELECT current_setting('agentos.user_id', true)::uuid
$$;

-- SECURITY DEFINER: membership check before an organization context exists
-- (login and workspace selection). Narrow surface, owner-executed only.
CREATE OR REPLACE FUNCTION agentos_user_is_member(target_org uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
    SELECT EXISTS (
        SELECT 1 FROM memberships m
        WHERE m.organization_id = target_org
          AND m.user_id = agentos_current_user_id()
    )
$$;

-- SECURITY DEFINER: pre-authentication org listing for a user.
CREATE OR REPLACE FUNCTION agentos_list_user_orgs(uid uuid)
RETURNS TABLE (organization_id uuid, name text, slug citext, role text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
    SELECT o.id, o.name, o.slug, m.role
    FROM memberships m
    JOIN organizations o ON o.id = m.organization_id
    WHERE m.user_id = uid AND o.deleted_at IS NULL
    ORDER BY o.name
$$;

REVOKE ALL ON FUNCTION agentos_user_is_member(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION agentos_list_user_orgs(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agentos_user_is_member(uuid), agentos_list_user_orgs(uuid)
    TO agentos_api, agentos_worker;

-- ----------------------------------------------------------------
-- Row-level security enablement and tenant isolation policies
-- ----------------------------------------------------------------
-- Standard pattern for organization-scoped tables: rows are visible and
-- writable only when their organization matches the transaction context.
DO $$
DECLARE
    t text;
    org_scoped_tables text[] := ARRAY[
        'organizations_noop', -- placeholder keeps array non-empty; skipped below
        'memberships', 'teams', 'team_members', 'org_invitations', 'api_keys',
        'agents', 'agent_versions', 'deployments', 'agent_identities',
        'model_providers', 'model_credentials', 'models', 'tools', 'mcp_connections',
        'policies', 'policy_decisions', 'approvals',
        'executions', 'execution_events', 'traces', 'spans',
        'usage_records', 'budgets', 'alerts', 'audit_logs',
        'webhooks', 'webhook_deliveries'
    ];
BEGIN
    FOREACH t IN ARRAY org_scoped_tables LOOP
        CONTINUE WHEN t = 'organizations_noop';
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format($f$
            CREATE POLICY tenant_isolation ON %I
                USING (organization_id = agentos_current_org())
                WITH CHECK (organization_id = agentos_current_org())
        $f$, t);
    END LOOP;
END
$$;

-- organizations itself: members can read their organizations; the active
-- organization context governs updates; creation is an authenticated,
-- application-controlled action.
ALTER TABLE organizations ENABLE ROW LEVEL SECURITY;

CREATE POLICY organizations_select ON organizations
    FOR SELECT
    USING (
        id = agentos_current_org()
        OR agentos_user_is_member(id)
    );

CREATE POLICY organizations_update ON organizations
    FOR UPDATE
    USING (id = agentos_current_org())
    WITH CHECK (id = agentos_current_org());

CREATE POLICY organizations_insert ON organizations
    FOR INSERT
    WITH CHECK (true);

-- users and sessions are user-scoped, not organization-scoped; they are
-- protected by grants and by the application, and never expose org data
-- before authentication.

-- ----------------------------------------------------------------
-- Grants — control plane role
-- ----------------------------------------------------------------
GRANT SELECT, INSERT, UPDATE ON
    users, sessions, organizations, memberships, teams, team_members,
    org_invitations, api_keys, agents, agent_versions, deployments,
    agent_identities, model_providers, model_credentials, models, tools,
    mcp_connections, policies, approvals, executions, traces, spans,
    budgets, alerts, webhooks, webhook_deliveries
TO agentos_api;

-- Append-only surfaces for the API role: insert and read, never mutate.
GRANT SELECT, INSERT ON
    execution_events, usage_records, policy_decisions, audit_logs
TO agentos_api;

-- ----------------------------------------------------------------
-- Grants — execution plane (worker) role: deliberately narrow
-- ----------------------------------------------------------------
GRANT SELECT ON
    agents, agent_versions, deployments, agent_identities, tools,
    model_providers, models, mcp_connections, policies, budgets, approvals
TO agentos_worker;

-- The worker owns execution state transitions and telemetry writes.
GRANT SELECT, UPDATE ON executions TO agentos_worker;
GRANT SELECT, INSERT, UPDATE ON traces, spans TO agentos_worker;
GRANT SELECT, INSERT ON execution_events, usage_records, policy_decisions TO agentos_worker;
GRANT INSERT ON alerts TO agentos_worker;

-- Explicitly no grants for the worker on: users, sessions, api_keys,
-- model_credentials, webhooks, memberships, organizations, audit_logs.
