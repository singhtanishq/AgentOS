# Control Plane (Go)

The control plane is a modular monolith serving the versioned REST API,
managing the durable queue and enforcing governance.

## Layout

```text
services/control-plane/
├── cmd/
│   ├── server/        # HTTP server entrypoint (API + webhook dispatcher)
│   └── migrate/       # Transactional schema migration runner
├── internal/
│   ├── server/        # Route assembly, middleware chain
│   ├── config/        # Environment-driven configuration
│   ├── httpx/         # JSON envelopes, errors, request IDs, rate limiting
│   ├── store/         # Pool + tenant-context transactions
│   ├── auth/          # Argon2id, sessions, API keys, RBAC matrix
│   ├── tenant/        # Organization creation (slug, owner, tool seeding)
│   ├── organizations/ # Members, invitations, roles
│   ├── agents/        # Registry CRUD, versions, lifecycle
│   ├── models/        # Model gateway, encrypted credentials, internal reveal
│   ├── tools/         # Tool registry API
│   ├── toolcatalog/   # Builtin catalog seeded per organization
│   ├── executions/    # Submission (idempotent), listing, cancel
│   ├── deployments/   # Environments, history, rollback
│   ├── identities/    # Agent identity lifecycle
│   ├── policies/      # Rego management + OPA deployment
│   ├── approvals/     # Human decisions + resume
│   ├── queue/         # JetStream topology + publishers
│   ├── traces/        # Trace/span query surface
│   ├── budgets/       # Budgets, usage analytics, alerts
│   ├── evaluations/   # Dataset/case CRUD, run triggering
│   ├── auditlogs/     # Append-only audit queries
│   ├── apikeys/       # Developer API keys
│   ├── webhooks/      # Signed delivery + JetStream consumer
│   ├── secrets/       # AES-256-GCM versioned envelopes
│   ├── telemetry/     # OTel + Prometheus wiring
│   └── migrations/    # Embedded SQL (14 migrations)
```

## Request pipeline

1. **RequestID** — 128-bit correlation id, propagated or generated.
2. **Recover** — panics become 500 envelopes, never crashes.
3. **Logging** — one structured line per request.
4. **Timeout** — 30s server-side budget.
5. **Auth** (`RequireAuth`) — session cookie *or* bearer API key → Principal.
6. **Tenancy** (`RequireOrg`) — membership via RLS-backed lookup (users) or
   org binding (API keys, `current` alias), then permission check against
   the RBAC matrix or key scopes.

## Tenant-context discipline

Every organization-scoped query runs inside `store.OrgTx`, which sets
`agentos.org_id` (and `agentos.user_id`) for the transaction. The
row-level security policies see the context; a query that forgets it
reads zero rows rather than another tenant's data. This turned several
early bugs into visible test failures instead of silent leaks.

## Error contract

```json
{
  "error": {
    "code": "not_found",
    "message": "execution not found",
    "request_id": "…"
  }
}
```

Unknown fields in request bodies are rejected; trailing JSON is rejected.

## Migration runner

- Embedded, versioned SQL files (`000001`…`000014`).
- One transaction per migration plus its bookkeeping row.
- Advisory lock serializes concurrent runs.
- History-conflict detection (renamed applied migrations fail loudly).
- Run: `make migrate` → `go run ./cmd/migrate`.

## Key packages in detail

### auth
- Argon2id (64 MiB, 3 iterations) password hashing with a validated policy.
- Opaque 256-bit session tokens; only sha256 hashes stored; sliding
  renewal at T-6h; revocation on logout.
- API keys: `agos_` prefix, sha256-hashed, scoped, expirable.
- Role matrix (`owner > admin > developer > operator > auditor > member`)
  expressed as a single canonical map.

### secrets
- Envelope: `[keyVersion byte][12B nonce][AES-256-GCM ciphertext+tag]`.
- Multiple key versions load simultaneously for rotation; the highest
  version seals new secrets.
- Production refuses to start without an explicit master key; development
  uses a documented deterministic key.

### queue
- Stream `EXECUTIONS` (jobs + DLQ, 7d retention) and `EVENTS`
  (domain events, 24h retention).
- Durable pull consumer `execution-workers`: explicit ack, 2 min ack wait,
  max 6 deliveries.
- Job publishing deduplicates on `executionId:attempt`.
- Webhook dispatcher consumes `EVENTS` with its own durable consumer.
