# Security Architecture and Threat Model

## Assets

| Asset | Sensitivity | Notes |
| ----- | ----------- | ----- |
| User credentials (Argon2id hashes) | Critical | Never logged, never returned |
| Session tokens | Critical | Opaque 256-bit, hashed at rest, HTTP-only cookies |
| Provider API keys | Critical | AES-256-GCM sealed with versioned master keys; decrypted only in memory at call time |
| Agent identity credentials | High | sha256-hashed at rest; single disclosure at creation |
| API keys (developer platform) | High | sha256-hashed at rest; scoped; revocable |
| Webhook signing secrets | High | AES-256-GCM sealed; used for HMAC signatures |
| Agent definitions & versions | High | Tenant-confidential IP |
| Execution inputs/outputs | High | May contain customer data; payloads redacted in events/telemetry |
| Usage and cost data | Medium | Business-sensitive analytics |
| Audit and policy decision logs | Medium | Tamper-evident history (append-only grants) |

## Trust boundaries

```text
[Browser] ──HTTPS──▶ [Control plane API]
[Python SDK / machines] ──Bearer API key──▶ [Control plane API]
[Control plane] ──NATS (internal)──▶ [Runtime workers]
[Runtime workers] ──PostgreSQL (RLS-scoped roles)──▶ [Datastore]
[Runtime workers] ──HTTPS + policy──▶ [Model providers / MCP servers]
[Control plane] ──Rego modules──▶ [OPA]
```

Everything east of the public API is internal infrastructure. The control
plane and workers connect to PostgreSQL with distinct least-privilege
roles (`agentos_api`, `agentos_worker`) that are subject to row-level
security; the migration owner role is separate again.

## Threat actors

1. **Unauthenticated internet attacker** — probing the public API.
2. **Malicious tenant** — an authenticated organization attempting to read
   or mutate another tenant's data.
3. **Compromised end-user session** — stolen cookie or CSRF.
4. **Malicious model output** — prompt-injected tool arguments.
5. **Malicious MCP server / remote tool** — hostile tool descriptions and
   responses; SSRF bait.
6. **Compromised internal service** — a worker or the control plane
   itself (defense-in-depth applies).

## STRIDE analysis

### Spoofing
- **Session spoofing:** tokens are 256-bit random; only sha256 hashes are
  stored; cookies are HttpOnly + SameSite=Lax (+ Secure outside dev).
- **API-key spoofing:** keys carry a prefix + sha256 hash; constant-scope
  lookups; revocation is immediate.
- **Agent identity spoofing:** identities are hashed, expire, and can be
  suspended/revoked independently of the agent.
- **Internal service spoofing:** control-plane↔runtime internal endpoints
  require a shared bearer token (constant-time compare).

### Tampering
- **Cross-tenant writes:** PostgreSQL RLS `WITH CHECK` rejects rows whose
  `organization_id` differs from the transaction context (verified by
  integration tests, including the worker role).
- **History falsification:** published agent versions are guarded by a
  trigger; execution events, usage records, policy decisions and audit
  logs are append-only by grants — no UPDATE/DELETE exists for the app
  roles.
- **Webhook payloads:** HMAC-SHA256 signatures over `timestamp.body`
  allow receivers to reject tampering and replays.

### Repudiation
- Audit logs capture actor, action, resource, result and request id for
  every sensitive operation (auth, membership, keys, policies, deploys).
- Policy decisions are logged with decision, reason and a digest of the
  evaluation input.

### Information disclosure
- **Tenant isolation:** every organization-scoped query runs under
  `agentos.org_id` transaction context; RLS default-deny without it
  (proven by the "unset context means default deny" test).
- **Credential leakage:** provider keys are decrypted only at execution
  time via the token-protected internal endpoint; they never traverse
  NATS or appear in logs/UIs. Telemetry payloads are key-redacted.
- **Error responses:** uniform envelopes without stack traces or SQL.
- **Migrations/CI:** secret scanning (gitleaks) and dependency/container
  scanning (trivy, govulncheck) run in CI.

### Denial of service
- Auth endpoints are per-IP rate limited; HTTP server timeouts are set;
  JetStream caps payload size and redelivery; the OTel collector enforces
  a memory ceiling; container memory limits are configured.

### Elevation of privilege
- RBAC is enforced server-side from a single permission matrix; auditors
  cannot mutate, operators cannot view credentials, developers cannot
  manage members. Admin-gated tools require human approval through the
  approval workflow.

## Model-output and tool security (particular to agents)

1. Model-generated tool arguments are JSON-parsed and schema-validated
   before execution; malformed payloads fail without side effects.
2. The `http.request` tool is GET-only, allowlist-gated, and blocks
   private/reserved/loopback/link-local addresses (DNS resolution
   checked), revalidates every redirect hop, caps response size and
   enforces timeouts.
3. MCP server URLs pass the same SSRF validation; remote tool content is
   treated as untrusted data, never as instructions.
4. OPA is consulted before **every** tool execution with fail-closed
   semantics; every decision is recorded.
5. Sensitive tools can require human approval; the execution parks in
   `waiting_approval` until an authorized role decides.

## Residual risks and limitations

- The default deployment is **local development**: TLS termination is
  expected in front of any non-local deployment (cookie `Secure` flips
  with `AGENTOS_ENV=production`).
- The in-memory rate limiter is per-process; a Redis-backed limiter is
  the production-grade path.
- Webhook deliveries are dispatched in-process with bounded retries;
  a dedicated delivery service would isolate crash domains further.
- LLM-as-judge evaluation is intentionally absent: probabilistic scores
  must be clearly labeled and are not ground truth.
- Compliance frameworks (SOC 2, ISO 27001, HIPAA) are **not** claimed.
  The controls documented here are engineering controls, not
  certifications.
