# AgentOS

**The Enterprise Control Plane for AI Agents**

AgentOS is an enterprise infrastructure platform for registering, deploying, executing, monitoring, securing, evaluating and governing AI agents — treating every agent like an enterprise software workload, with an identity, a version, a deployment, permissions, a resource budget, execution history and an operational lifecycle.

> **Status: v1.0.0.** Everything documented below is implemented and verified by automated tests (Go race-detector suites, pytest, vitest, and integration tests against real PostgreSQL, NATS and OPA). Known limitations are listed explicitly — nothing here is claimed that is not in the repository.

---

## Table of contents

1. [Why AgentOS exists](#why-agentos-exists)
2. [Key capabilities](#key-capabilities)
3. [Use cases](#use-cases)
4. [Architecture](#architecture)
5. [Technology stack](#technology-stack)
6. [System components](#system-components)
7. [Repository structure](#repository-structure)
8. [Database architecture](#database-architecture)
9. [Security architecture](#security-architecture)
10. [Installation](#installation)
11. [Local environment setup](#local-environment-setup)
12. [Docker usage](#docker-usage)
13. [Environment configuration](#environment-configuration)
14. [Database migrations](#database-migrations)
15. [Seed data](#seed-data)
16. [Starting all services](#starting-all-services)
17. [Demo account](#demo-account)
18. [How to create an agent](#how-to-create-an-agent)
19. [How to execute an agent](#how-to-execute-an-agent)
20. [How to configure tools](#how-to-configure-tools)
21. [How to inspect traces](#how-to-inspect-traces)
22. [How to configure policies](#how-to-configure-policies)
23. [How to review approvals](#how-to-review-approvals)
24. [How to inspect costs](#how-to-inspect-costs)
25. [API documentation](#api-documentation)
26. [Python SDK](#python-sdk)
27. [Testing](#testing)
28. [CI/CD](#cicd)
29. [Security notes](#security-notes)
30. [Known limitations](#known-limitations)
31. [Roadmap](#roadmap)
32. [Screenshots](#screenshots)
33. [Contributing](#contributing)
34. [License](#license)

## Why AgentOS exists

Organizations deploy AI agents across departments — different LLM providers, internal APIs, databases, MCP servers, autonomous runs. Without a centralized platform this produces:

- Fragmented agent management and inconsistent access control
- Uncontrolled model expenditure
- Limited execution observability and poor failure investigation
- Version drift: silently modified prompts with no deployment history
- Credential sprawl and no accountability

AgentOS provides the unified operating layer: a **Go control plane** (registry, auth, governance, budgets), a **Python execution plane** (durable agent runtime), and an **operational dashboard** (Next.js) with full execution observability.

## Key capabilities

- **Multi-tenant isolation** — every organization-scoped table carries PostgreSQL row-level security; every query runs under a transaction-bound tenant context. Proven by dedicated cross-tenant isolation tests.
- **Authentication & RBAC** — Argon2id passwords, opaque hashed sessions, six organization roles (owner/admin/developer/operator/auditor/member) with a canonical permission matrix, plus scoped API keys for machines.
- **Agent registry with immutable versions** — strict configuration schema, draft→published lifecycle, published versions locked by a database trigger; executions pin the exact version used.
- **Model gateway** — OpenAI, Anthropic, Google Gemini and OpenAI-compatible endpoints behind provider adapters; provider credentials sealed with versioned AES-256-GCM; a deterministic **mock provider** keeps the whole platform working without any paid API.
- **Durable execution** — NATS JetStream jobs with explicit acks, bounded redelivery, backoff, dead-letter handling; guarded execution state machine (queued → running → waiting_approval → succeeded/failed/cancelled/timed_out).
- **Tool gateway & MCP** — schema-validated model arguments, SSRF-guarded HTTP tool, sandboxed calculator, demo fixtures, and MCP servers via the official SDK.
- **Governance** — OPA-evaluated authorization before every tool execution (fail-closed), human approval workflow that parks and resumes executions, append-only audit and decision logs.
- **Observability** — structured execution events, DB-backed span timelines, OTel metrics → Prometheus → Grafana.
- **Cost management** — usage metering with versioned pricing, budgets enforced on the backend (blocking budgets refuse new executions), threshold alerts.
- **Evaluations** — datasets, deterministic rule-based scoring, regression tracking between runs.

## Use cases

- **AI platform teams** operating agents for internal customers — one registry, one auth model, one bill.
- **SaaS companies embedding agents** — per-tenant budgets, approval gates, and execution histories.
- **Regulated teams** — every sensitive tool call authorized by policy, decided by humans, and audit-logged.
- **Agent developers** — iterate on versions, evaluate against datasets, roll back deployments.

## Architecture

```mermaid
flowchart TB
    U["Users / Developers"] --> W["Next.js Dashboard (:3000)"]
    S["Python SDK / machines"] -->|"Bearer API key"| CP
    W --> CP["Go Control Plane (:8080)"]
    CP --> PG[("PostgreSQL 18")]
    CP --> OPA["OPA (:8181)"]
    CP -->|"jobs"| JS["NATS JetStream (:4222)"]
    JS --> RT["Python Agent Runtime (:8090)"]
    RT -->|"state, events, spans"| PG
    RT -->|"metrics OTLP"| OTC["OTel Collector"]
    RT -->|"policies"| OPA
    RT --> MG["Model Gateway"]
    RT --> TG["Tool Gateway"]
    MG --> P1["OpenAI / Anthropic / Gemini / compatible / mock"]
    TG --> T1["Built-in tools"]
    TG --> MCP["MCP servers"]
    CP -->|"events"| JS
    CP --> WEB["Webhook dispatcher (HMAC-signed)"]
    OTC --> PROM["Prometheus (:9090)"] --> GRAF["Grafana (:3001)"]
    CP --> S3[("S3-compatible storage (:9000)")]
```

Detailed documents: [system overview](docs/architecture/system-overview.md) · [control plane](docs/architecture/control-plane.md) · [execution plane](docs/architecture/execution-plane.md) · [event architecture](docs/architecture/event-architecture.md) · [security architecture](docs/architecture/security-architecture.md) · [threat model](docs/security/threat-model.md).

One execution, end to end:

```mermaid
sequenceDiagram
    participant U as Caller (UI/SDK)
    participant CP as Control plane
    participant JS as JetStream
    participant RT as Runtime worker
    participant DB as PostgreSQL
    participant OPA as OPA
    participant M as Model provider

    U->>CP: POST /executions (idempotency key)
    CP->>DB: INSERT execution (queued) + audit
    CP->>JS: publish job (dedupe by execution+attempt)
    JS->>RT: deliver job (durable pull consumer)
    RT->>DB: guarded transition queued→running
    RT->>DB: load agent version, tools, pricing (RLS-scoped)
    loop agent loop (max steps)
        RT->>M: model request (credentials via CP internal API)
        M-->>RT: response + usage
        alt tool calls
            RT->>OPA: authorize tool.execute (fail-closed)
            OPA-->>RT: allow / requires_approval / deny
            RT->>RT: execute tool (validated args, timeouts)
        end
    end
    RT->>DB: output, usage_records, events, spans (terminal transition)
    RT->>JS: ack job
```

## Technology stack

| Layer | Technology |
| --- | --- |
| Dashboard | Next.js (App Router), React 19, TypeScript strict, Tailwind CSS v4, TanStack Query |
| Control plane | Go 1.27, chi, pgx v5, NATS JetStream, slog |
| Runtime | Python 3.13, FastAPI, asyncpg, httpx, official MCP SDK |
| SDK | Python (`agentos`), typed errors, httpx |
| Datastore | PostgreSQL 18 (row-level security), Redis, SeaweedFS (S3-compatible) |
| Governance | Open Policy Agent 1.12 |
| Observability | OpenTelemetry → Prometheus → Grafana |
| Local infra | Docker Compose (8 services + opt-in app profile) |
| Quality | go test -race, pytest, vitest, ruff, mypy, eslint, gitleaks, trivy, govulncheck, k6 |

## System components

| Component | Port | Responsibility |
| --- | --- | --- |
| Next.js dashboard | 3000 | Operations UI — every surface documented in Screenshots |
| Go control plane | 8080 | Registry, auth, governance, budgets, queue publisher, webhook dispatcher |
| Python runtime | 8090 | Job consumption, agent loop, tools, metering, evaluations |
| PostgreSQL 18 | 5432 | Authoritative state, RLS multi-tenancy |
| Redis 8 | 6379 | Cache/rate-limit store |
| NATS JetStream | 4222 (mon 8222) | Durable execution jobs + domain events |
| SeaweedFS (S3) | 9000 (ui 9333) | Object storage (MinIO image removed from public access — substituted, see infra notes) |
| OPA | 8181 | Authorization decisions |
| OTel Collector | 4317/4318 | Telemetry ingestion → Prometheus exporter |
| Prometheus | 9090 | Metrics storage |
| Grafana | 3001 | Dashboards |

## Repository structure

```text
AgentOS/
├── apps/web/                  # Next.js dashboard (all surfaces)
├── services/
│   ├── control-plane/         # Go API (14 SQL migrations embedded, seed + migrate CLIs)
│   ├── agent-runtime/         # Python execution plane (engine, providers, tools, workers)
│   └── sdk-python/            # `agentos` Python SDK
├── packages/config/           # Shared TypeScript presets
├── infra/                     # nats, otel, prometheus, grafana, docker configs
├── docs/                      # architecture/, api/, database/, security/, development/, screenshots/
├── scripts/                   # setup, dev, test, lint, migrate, seed, health-check, validate-screenshots
├── tests/load/                # k6 load test
├── .github/workflows/         # ci.yml, security.yml, docker-build.yml
├── docker-compose.yml
├── Makefile
└── README.md
```

## Database architecture

30 tables across 14 versioned migrations. Highlights (full ER diagram in [docs/database/schema.md](docs/database/schema.md)):

- **Identity**: organizations, users, memberships, teams, sessions, invitations, api_keys
- **Registry**: agents, agent_versions (immutable), deployments, agent_identities
- **Gateway**: model_providers, model_credentials (AES-GCM), models (versioned pricing), tools, mcp_connections
- **Governance**: policies, policy_decisions (append-only), approvals
- **Execution**: executions, execution_events (append-only), traces, spans
- **Metering**: usage_records (append-only), budgets, alerts, audit_logs (append-only)
- **Platform**: webhooks, webhook_deliveries, evaluation_datasets/cases/runs/results

Conventions: UUID PKs, UTC timestamptz, money as BIGINT minor units (never floats), soft deletion where appropriate, `updated_at` triggers, status machines as CHECK constraints.

**Row-level security**: every organization-scoped table carries `tenant_isolation` policies keyed on a transaction-bound `agentos.org_id`. Two application roles (`agentos_api`, `agentos_worker`) hold least-privilege grants; append-only tables grant no UPDATE/DELETE; the worker cannot read users, sessions, API keys or provider credentials. Unset tenant context is default-deny.

## Security architecture

- **Argon2id** password hashing with a validated password policy
- **Opaque sessions**: 256-bit tokens, sha256 at rest, HttpOnly cookies, sliding renewal, revocation
- **Credentials**: AES-256-GCM versioned envelopes; decrypted only at execution time via a token-protected internal endpoint; never on the message bus or in UIs
- **OPA fail-closed** authorization before every tool execution; every decision recorded with a reason
- **SSRF protections** on network tools: allowlists, private-address blocking at DNS resolution, revalidated redirects, size caps, timeouts
- **Append-only audit trail** and policy decision log (no UPDATE/DELETE grants for app roles)
- **HMAC-SHA256 signed webhooks** with timestamped payloads
- **Scanned CI**: gitleaks (full history), trivy, govulncheck

Full analysis with STRIDE, trust boundaries and residual risks: [docs/security/threat-model.md](docs/security/threat-model.md).

## Installation

Prerequisites: Docker, Go 1.27+, Node 22+ with pnpm 10, Python 3.13+ with [uv](https://docs.astral.sh/uv/).

```bash
git clone https://github.com/singhtanishq/AgentOS.git
cd AgentOS
./scripts/setup.sh   # verifies the toolchain
```

## Local environment setup

```bash
cp .env.example .env    # optional — every var has a local dev default
# Edit .env only if you need non-default ports or credentials.
```

## Docker usage

```bash
make infra-up            # postgres, redis, nats, storage, opa, otel, prometheus, grafana (waits for health)
docker compose --profile apps up -d   # optional: also run the apps in containers
make infra-down          # stop everything (volumes persist)
./scripts/reset-local.sh # DESTRUCTIVE: wipes volumes, asks for confirmation
```

## Environment configuration

All configuration is environment-driven with safe local defaults — see [`.env.example`](.env.example). Production requires `AGENTOS_SECRETS_MASTER_KEY` (base64 32 bytes: `openssl rand -base64 32`) and a strong `AGENTOS_INTERNAL_API_TOKEN`; the server refuses known dev defaults.

## Database migrations

```bash
make migrate             # applies pending migrations as the schema owner
# or directly:
cd services/control-plane && go run ./cmd/migrate
```

Applied migrations are immutable; the runner detects edited history and refuses to continue.

## Seed data

```bash
make seed                # migrations + idempotent demo seeding
```

Seeds the fictional **Acme Technologies** organization: 5 users (all roles), 3 agents with published versions and deployments, 8 demo executions across every state with events/spans/usage, policies, alerting + blocking budgets, pending and decided approvals, alerts, an evaluation dataset with a scored run, a demo API key, a disabled webhook and a disabled MCP connection. All demo data is fictional and clearly local-only; the seed is safe to re-run.

## Starting all services

```bash
make dev     # one terminal: web :3000, control plane :8080, runtime :8090
# or individually:
make dev-web / make dev-api / make dev-runtime
./scripts/health-check.sh    # probe every service
```

## Demo account

| | |
| --- | --- |
| Organization | Acme Technologies |
| Logins | `owner@acme.test`, `admin@acme.test`, `developer@acme.test`, `operator@acme.test`, `auditor@acme.test` |
| Password | `DemoPass123!` |
| Demo API key | `agos_demo_local_key_0000000000000000` |

> These credentials are for the **local demo only** — they are seeded by the public repository and must never be used outside a local environment.

## How to create an agent

1. Sign in → **Agents** → **New agent**.
2. Fill name, description, system instructions; pick the model.
3. The agent starts as **draft** with version 1. Open the detail page → **New version** to iterate (each version is an immutable snapshot once published).
4. **Publish** a version, then **Mark active**. Deploy from **Deployments** (or the agent page) to an environment.

![Agent Registry](docs/screenshots/03-agents/agent-registry.png)

![Agent Builder](docs/screenshots/03-agents/agent-builder.png)

![Agent Details](docs/screenshots/03-agents/agent-details.png)

![Agent Configuration](docs/screenshots/03-agents/agent-configuration.png)

## How to execute an agent

1. Open an **active** agent → **Run agent** → enter the input query.
2. You land on the execution detail page: live status, event stream, span timeline, tokens and cost.
3. Cancel a run from the same page (cooperative cancellation at step boundaries).

![Execution List](docs/screenshots/05-executions/execution-list.png)

![Execution Details](docs/screenshots/05-executions/execution-details.png)

![Execution Failure](docs/screenshots/05-executions/execution-failure.png)

Or from Python:

```python
from agentos import AgentOS

client = AgentOS(api_key="agos_YOUR_KEY", base_url="http://localhost:8080")
execution = client.executions.create(
    agent_id="YOUR_AGENT_ID",
    input={"query": "Summarize the approved project documents."},
)
print(execution.id)
execution = client.executions.wait(execution.id)
print(execution.status, execution.output)
```

## How to configure tools

**Tool Registry** lists the builtin catalog seeded into every organization. Configure required role, status, timeout and the JSON config per tool. Bind tools to an agent by listing their names in the version configuration's `tools` array. `http.request` ships **disabled** until you review its host allowlist.

![Tool Registry](docs/screenshots/08-tool-registry/tool-list.png)

![MCP Connections](docs/screenshots/08-tool-registry/mcp-connections.png)

## How to inspect traces

**Traces** lists recent executions; select one to see the span timeline — `invoke_agent`, `model_request`, `tool_execution` bars positioned by start offset with per-span durations and error states. The execution detail page shows the same timeline alongside the structured event stream.

![Trace Explorer](docs/screenshots/06-observability/trace-explorer.png)

## How to configure policies

**Policies** → **New policy**. Write Rego in the `agentos.authz` package; the control plane compiles it in OPA *before* persisting (invalid policies are rejected), then deploys it. Rules union with the embedded platform baseline, which defaults to deny and requires approval for admin-gated tools.

![Policy Dashboard](docs/screenshots/10-governance/policy-dashboard.png)

## How to review approvals

When policy requires human authorization, the execution parks in `waiting_approval`, an approval request is created, and an `approval.requested` event is published. Open **Approvals** → **Review** → **Approve** or **Reject**. Approval resumes the execution; rejection fails it with a structured error. Every decision is audit-logged.

![Approval Workflow](docs/screenshots/10-governance/approval-workflow.png)

## How to inspect costs

**Usage & Costs** shows daily spend, per-agent breakdown, total tokens and budget progress bars. Budgets support `alert_only` and `block` enforcement — a blocking budget refuses new executions once its live period spend (summed from usage records) reaches the limit, and threshold alerts fire at 50/80/100%.

![Usage Dashboard](docs/screenshots/12-usage-and-costs/usage-dashboard.png)

## API documentation

Complete route reference with permissions: [docs/api/reference.md](docs/api/reference.md). Highlights:

```bash
# login (cookie jar)
curl -c jar -X POST localhost:8080/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"owner@acme.test","password":"DemoPass123!"}'

# list agents
curl -b jar localhost:8080/v1/organizations/current/agents

# submit an execution with an API key
curl -X POST localhost:8080/v1/organizations/current/executions \
  -H "Authorization: Bearer agos_demo_local_key_0000000000000000" \
  -H 'Content-Type: application/json' \
  -d '{"agent_id":"…","input":{"query":"…"},"idempotency_key":"run-1"}'
```

## Python SDK

```bash
uv pip install -e services/sdk-python
```

```python
from agentos import AgentOS

client = AgentOS(api_key="agos_YOUR_KEY", base_url="http://localhost:8080")
execution = client.executions.create(
    agent_id="YOUR_AGENT_ID",
    input={"query": "Summarize the approved project documents."},
)
print(execution.id)
execution = client.executions.wait(execution.id)
print(execution.status, execution.output)
```

Typed errors: `AuthenticationError`, `NotFoundError`, `RateLimitError` (all `APIError` with `status_code`, `code`, `message`, `request_id`). See [services/sdk-python/README.md](services/sdk-python/README.md).

## Testing

```bash
make test        # everything below
make test-go     # go test -race ./... (unit) — integration: -tags=integration ./tests/
make test-python # pytest (unit) — pipeline: AGENTOS_INTEGRATION=1 pytest tests/test_execution_pipeline.py
make test-web    # vitest
make lint        # go vet, ruff+mypy, eslint+tsc
```

Integration tests run against **real** PostgreSQL, NATS and OPA: cross-tenant RLS isolation, published-version immutability, atomic budget enforcement, the full execution pipeline (claim → model/tool loop → events → spans → usage), and the complete approval round trip.

## CI/CD

- **ci.yml** — Go vet + race tests, Python runtime + SDK (ruff, mypy, pytest), web (eslint, tsc, vitest, build), and an integration job with PostgreSQL 18 / NATS / OPA services.
- **security.yml** — gitleaks secret scanning, trivy vulnerability scan, govulncheck.
- **docker-build.yml** — multi-arch image builds for all three services.

## Security notes

- Never commit `.env`; the repo's `.gitignore` covers it and CI runs gitleaks over full history.
- Rotate provider credentials from the Model Gateway (revoke + re-add); old ciphertexts stay decryptable during rotation via key-versioned envelopes.
- The demo credentials above are public by design — change them before any shared deployment.
- No compliance certification (SOC 2 / ISO 27001 / HIPAA) is claimed. See the [threat model](docs/security/threat-model.md) for controls and residual risks.

## Known limitations

- **Local-dev TLS**: run behind a TLS-terminating proxy for non-local deployment (cookies flip to `Secure` with `AGENTOS_ENV=production`).
- **Rate limiting** is per-process (in-memory); a Redis-backed limiter is the production path.
- **Webhook dispatch** runs in-process with bounded retries rather than as a dedicated delivery service.
- **Email**: no SMTP transport; invitations return tokens in the API and `email.draft` is a mock that never sends.
- **LLM-as-judge evaluation** is deliberately absent — probabilistic scores must be labeled and are not ground truth.
- **Object storage** is provisioned but the application surface (artifact upload/download) is reserved for the next release.
- **Streaming model responses** are not yet exposed end-to-end; the loop uses complete responses with usage extraction.

## Roadmap

1. Redis-backed distributed rate limiting; dedicated webhook delivery service
2. Artifact upload/download over S3 with per-tenant buckets
3. Streaming (SSE) execution updates in the dashboard
4. LLM-as-judge evaluators with explicit probabilistic labeling
5. SSO (OIDC) and SCIM provisioning
6. Kubernetes Helm chart + horizontal runtime autoscaling

## Screenshots

Every screenshot below was captured from the actually running application against the seeded demo environment. Validation status: **29 approved, 29 embedded, 0 unreferenced, 0 broken** (verified by `python3 scripts/validate-readme-screenshots.py`; manifest in [docs/screenshots/SCREENSHOT_INDEX.md](docs/screenshots/SCREENSHOT_INDEX.md)).

### Login

Session-based sign-in with Argon2id-verified credentials.

![Login](docs/screenshots/01-authentication/login.png)

### Signup

Creates the user and their first organization (owner membership) in one step.

![Signup](docs/screenshots/01-authentication/signup.png)

### Overview

Operational metrics from live API data: agents, active deployments, execution volume, success rate, average duration and model spend, plus recent executions, top agents and open alerts.

![Overview](docs/screenshots/02-overview/main-dashboard.png)

### Agent Registry

Searchable, status-filtered registry of the organization's agents with version information.

![Agent Registry](docs/screenshots/03-agents/agent-registry.png)

### Agent Builder

Create-agent dialog with system instructions, model selection and execution limits.

![Agent Builder](docs/screenshots/03-agents/agent-builder.png)

### Agent Details

Lifecycle actions (activate/pause/suspend/archive), run submission and version history.

![Agent Details](docs/screenshots/03-agents/agent-details.png)

### Agent Configuration

Immutable version configuration viewer — instructions, model parameters, tool bindings and limits.

![Agent Configuration](docs/screenshots/03-agents/agent-configuration.png)

### Deployment List

Deployment history across environments with actors and rollback/deactivate actions.

![Deployment List](docs/screenshots/04-deployments/deployment-list.png)

### Execution Explorer

Auto-refreshing list of executions with status, steps, tool calls, tokens, cost and duration.

![Execution Explorer](docs/screenshots/05-executions/execution-list.png)

### Execution Details

Single run: status, duration, token split, estimated cost, input/output, structured event stream and span timeline.

![Execution Details](docs/screenshots/05-executions/execution-details.png)

### Execution Failure

A failed execution with its structured error classification and partial telemetry.

![Execution Failure](docs/screenshots/05-executions/execution-failure.png)

### Trace Explorer

Gantt-style span timeline (model requests, tool executions, governance spans) positioned by start offset.

![Trace Explorer](docs/screenshots/06-observability/trace-explorer.png)

### Model Gateway

Providers with encrypted credential fingerprints (never key material) and the model registry with versioned pricing.

![Model Gateway](docs/screenshots/07-model-gateway/provider-registry.png)

### Tool Registry

Builtin catalog with schemas, required roles, timeouts and JSON configuration.

![Tool Registry](docs/screenshots/08-tool-registry/tool-list.png)

### MCP Connections

Register, enable/disable and remove Model Context Protocol servers (streamable HTTP).

![MCP Connections](docs/screenshots/08-tool-registry/mcp-connections.png)

### Agent Identity

Per-agent credentials: issue (single disclosure), suspend, revoke; usage and expiry tracked.

![Agent Identity](docs/screenshots/09-identity/agent-identities.png)

### Approvals

Human authorization queue: review requested tool arguments, approve to resume or reject to fail the execution.

![Approvals](docs/screenshots/10-governance/approval-workflow.png)

### Policies

Rego policy management with OPA deployment; rules union with the deny-by-default baseline.

![Policies](docs/screenshots/10-governance/policy-dashboard.png)

### Evaluations

Datasets, deterministic rule-based cases, run history with pass rates and regression tracking.

![Evaluations](docs/screenshots/11-evaluations/evaluation-dashboard.png)

### Usage & Costs

Daily spend chart, per-agent cost breakdown, token totals and budget progress with enforcement modes.

![Usage & Costs](docs/screenshots/12-usage-and-costs/usage-dashboard.png)

### Alerts

Threshold and failure alerts with severity and one-click acknowledgement.

![Alerts](docs/screenshots/13-alerts-and-audit/alerts.png)

### Audit Logs

Append-only record of security-relevant operations: actor, action, resource, result.

![Audit Logs](docs/screenshots/13-alerts-and-audit/audit-logs.png)

### Members

Organization roster, role management and single-disclosure invitation tokens.

![Members](docs/screenshots/14-organization/members.png)

### Organization Settings

Workspace profile and the active security posture summary.

![Organization Settings](docs/screenshots/14-organization/organization-settings.png)

### API Keys

Scoped machine credentials with single-disclosure secrets and revocation.

![API Keys](docs/screenshots/15-developer-platform/api-keys.png)

### Webhooks

HMAC-signed event delivery endpoints with subscription management.

![Webhooks](docs/screenshots/15-developer-platform/webhooks.png)

### Dashboard (mobile)

Overview at 390×844 — responsive layout check.

![Dashboard Mobile](docs/screenshots/16-responsive/dashboard-mobile.png)

### Agent Details (mobile)

Agent detail at 390×844.

![Agent Details Mobile](docs/screenshots/16-responsive/agent-details-mobile.png)

### Infrastructure Monitoring

Prometheus querying live metrics from the local stack (`up` targets).

![Infrastructure Monitoring](docs/screenshots/17-system/infrastructure-monitoring.png)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for engineering standards, commit discipline and the PR process.

## License

[MIT](LICENSE)
