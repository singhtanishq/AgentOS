# AgentOS

**The Enterprise Control Plane for AI Agents**

AgentOS is an enterprise infrastructure platform for registering, deploying, executing, monitoring, securing, evaluating and governing AI agents — treating every agent like an enterprise software workload, with an identity, a version, a deployment, permissions, a resource budget, execution history and an operational lifecycle.

> ⚠️ **Status: active development.** AgentOS is being built milestone by milestone.
> Sections below describe only functionality that already exists in the repository.

## Why AgentOS exists

Enterprises deploy AI agents across departments — different LLM providers, internal APIs, databases, MCP servers, external actions, autonomous runs. Without a centralized platform this produces fragmented agent management, inconsistent access control, uncontrolled model spend, limited observability, credential sprawl and no accountability.

AgentOS provides the unified operating layer: a **Go control plane** (registry, auth, governance, budgets), a **Python execution plane** (durable agent runtime), and an **operational dashboard** (Next.js) with full execution observability.

## Architecture (target)

```text
      Users / Developers
             │
   ┌─────────┴──────────┐
   │  Next.js Dashboard │     Python SDK
   └─────────┬──────────┘
             │
      GO CONTROL PLANE          (registry · auth · governance · budgets)
             │
        PostgreSQL  ──  Redis
             │
       Execution API
             │
      NATS JetStream           (durable execution queue)
             │
   PYTHON RUNTIME WORKERS     (agent loop · tool gateway · model gateway)
             │
   ┌──────────┴───────────┐
   │ Model Gateway        │  Tool Gateway + MCP
   │ OpenAI · Anthropic · │  built-in tools · MCP servers
   │ Gemini · compatible  │
   └──────────────────────┘
             │
       OpenTelemetry → Prometheus / Grafana
```

## Monorepo layout

```text
apps/web/                 Next.js dashboard (React, TypeScript strict, Tailwind, shadcn/ui)
services/control-plane/   Go HTTP API (chi, pgx, NATS, OPA)
services/agent-runtime/   Python execution plane (FastAPI, NATS, provider adapters)
packages/config/          Shared TypeScript presets
infra/                    Docker Compose service configuration
docs/                     Architecture, database, API, security documentation
scripts/                  Developer automation
```

## Quick start (current state)

Prerequisites: Docker, Go 1.27+, Node 22+ with pnpm 10, Python 3.13+ with [uv](https://docs.astral.sh/uv/).

```bash
# verify your environment
./scripts/setup.sh

# run every test suite (Go race detector, pytest, vitest)
make test

# run all linters (go vet, ruff, mypy, eslint, tsc)
make lint

# start all three applications locally
make dev
```

Service endpoints once running:

| Service            | Endpoint                    |
| ------------------ | --------------------------- |
| Dashboard          | http://localhost:3000       |
| Control plane API  | http://localhost:8080/v1    |
| Agent runtime      | http://localhost:8090/readyz|

## Verification commands

```bash
cd services/control-plane && go test -race ./...   # Go unit tests
cd services/agent-runtime && uv run pytest         # Python tests
pnpm --filter @agentos/web test                    # web unit tests
```

## Documentation

- [`CONTRIBUTING.md`](CONTRIBUTING.md) — development workflow and commit discipline
- [`SECURITY.md`](SECURITY.md) — security policy and reporting
- `docs/architecture/` — *(arriving with milestone 17)* system overview, control plane, execution plane

## License

[MIT](LICENSE)
