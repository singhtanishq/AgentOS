# Security Architecture

See [`threat-model.md`](threat-model.md) for the full STRIDE analysis.
This page summarizes the implemented controls and where they live.

## Identity & access

| Control | Implementation |
| --- | --- |
| Password hashing | Argon2id (64 MiB, t=3, p=1) via `internal/auth` |
| Password policy | ≥10 chars, letters + digits, ≤200 chars |
| Sessions | Opaque 256-bit tokens, sha256 at rest, HttpOnly+SameSite=Lax cookies (Secure in production), sliding 12h renewal, revocation |
| API keys | `agos_` prefix, sha256 at rest, scoped, expirable, revocable, single disclosure |
| Agent identities | sha256-hashed credentials, expiry, suspend/revoke lifecycle |
| RBAC | Owner/Admin/Developer/Operator/Auditor/Member matrix (`internal/auth/auth.go`) — a single canonical map, checked server-side |

## Multi-tenancy (defense in depth)

1. **Application layer** — every query goes through `store.OrgTx`, which
   binds `agentos.org_id`/`agentos.user_id` per transaction.
2. **Row-level security** — all 26 organization-scoped tables carry a
   `tenant_isolation` policy (`USING` + `WITH CHECK` on
   `organization_id = agentos_current_org()`); unset context is
   default-deny.
3. **Least-privilege roles** — `agentos_api` (control plane) and
   `agentos_worker` (runtime) are non-owners with explicit grants;
   append-only tables (events, usage, decisions, audit) grant no
   UPDATE/DELETE; the worker cannot read users, sessions, API keys or
   provider credentials.
4. **Verified by tests** — `tests/integration_test.go` proves tenant
   visibility, zero-row cross-tenant updates, rejected cross-tenant
   inserts, default-deny without context and worker-role restrictions.

## Secrets

- Provider API keys, webhook signing secrets: AES-256-GCM with versioned
  keys (`internal/secrets`), decrypted only at execution time through the
  token-protected internal endpoint — never on the message bus, in logs
  or in UIs (response-leak assertions in tests).
- Key rotation: envelopes carry a key version; multiple keys load
  simultaneously.
- `.env` files are git-ignored; `.env.example` documents placeholders;
  gitleaks scans the full history in CI.

## Network & tool security

- SSRF guards for `http.request` and MCP servers: scheme checks, host
  allowlist, private/reserved/loopback/link-local address blocking at DNS
  resolution, revalidated redirects, response caps, strict timeouts.
- OPA authorization before every tool execution, fail-closed; every
  decision recorded in `policy_decisions` with reason.
- Human approval flow for sensitive tools (`waiting_approval`).
- Rate limiting on authentication endpoints; HTTP server timeouts
  everywhere.

## Audit

Append-only `audit_logs` (INSERT+SELECT grants only) capture auth,
membership, key, policy, deployment and operational events with actor,
resource, result and request id. `policy_decisions` records every
authorization outcome with the evaluation digest.

## Container & pipeline

- Distroless/non-root images where possible; resource limits on every
  container.
- CI: gitleaks (full history), trivy (CRITICAL/HIGH), govulncheck,
  multi-arch builds.
- Local-only development credentials are documented as such; production
  configuration refuses known defaults.
