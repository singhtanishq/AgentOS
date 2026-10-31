-- AgentOS 000011 — SECURITY DEFINER invitation resolver.
-- Invitation redemption happens before any organization context exists
-- (the caller is not yet a member), so the lookup must bypass row-level
-- security through a narrow, auditable function.

CREATE OR REPLACE FUNCTION agentos_resolve_invitation(token_hash text)
RETURNS TABLE (organization_id uuid, name text, slug citext, role text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public AS $$
    SELECT i.organization_id, o.name, o.slug, i.role
    FROM org_invitations i
    JOIN organizations o ON o.id = i.organization_id
    WHERE i.token_hash = $1
      AND i.status = 'pending'
      AND i.expires_at > now()
      AND o.deleted_at IS NULL
$$;

REVOKE ALL ON FUNCTION agentos_resolve_invitation(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agentos_resolve_invitation(text)
    TO agentos_api, agentos_worker;
