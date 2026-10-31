-- AgentOS 000010 — harden tenant-context helpers.
--
-- PostgreSQL keeps a session placeholder for custom GUCs once they have
-- been set inside any transaction; after the transaction ends the value
-- reverts to the empty string rather than to NULL. The original helpers
-- cast that empty string directly to uuid, producing "invalid input
-- syntax for type uuid" on any connection that previously carried tenant
-- context. NULLIF normalizes the empty string to NULL so unset context
-- remains default-deny instead of erroring.

CREATE OR REPLACE FUNCTION agentos_current_org() RETURNS uuid
LANGUAGE sql STABLE AS $$
    SELECT NULLIF(current_setting('agentos.org_id', true), '')::uuid
$$;

CREATE OR REPLACE FUNCTION agentos_current_user_id() RETURNS uuid
LANGUAGE sql STABLE AS $$
    SELECT NULLIF(current_setting('agentos.user_id', true), '')::uuid
$$;

CREATE OR REPLACE FUNCTION agentos_user_is_member(target_org uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
    SELECT EXISTS (
        SELECT 1 FROM memberships m
        WHERE m.organization_id = target_org
          AND m.user_id = NULLIF(current_setting('agentos.user_id', true), '')::uuid
    )
$$;
