# Development Guide

## Prerequisites

- Docker (Compose v2)
- Go 1.27+
- Node 22+ with pnpm 10
- Python 3.13+ with [uv](https://docs.astral.sh/uv/)
- k6 (optional, load tests)

`./scripts/setup.sh` verifies the toolchain.

## Daily workflow

```bash
make infra-up      # PostgreSQL, Redis, NATS, storage, OPA, telemetry (waits for health)
make migrate       # apply schema migrations
make dev           # runs web :3000, control plane :8080, runtime :8090 together
make test          # go test -race, pytest, vitest
make lint          # go vet, ruff+mypy, eslint+tsc
make health        # probe every service
make infra-down    # stop infrastructure
```

Useful extras:

```bash
make test-go / test-python / test-web
make seed          # populate the demo environment (once implemented below)
./scripts/reset-local.sh   # destructive: wipes volumes — asks for confirmation
```

## Integration tests

Integration tests run against real PostgreSQL/NATS/OPA and are excluded
from the default `go test ./...` run:

```bash
# Go: creates schema in a dedicated agentos_test database
go test -C services/control-plane -tags=integration -count=1 ./tests/

# Python pipeline: expects migrations applied (run the Go suite first)
AGENTOS_INTEGRATION=1 uv run --project services/agent-runtime \
  pytest tests/test_execution_pipeline.py
```

The Go suite recreates `agentos_test` from scratch each run; the Python
pipeline tests share that database and the roles it provisions.

## Testing conventions

- Unit tests are fast and hermetic (no network, no DB).
- Integration tests cover what mocks cannot: RLS tenancy, state-machine
  guards, queue delivery, approval round trips.
- Every bug fix lands with a regression test.
- The race detector runs on all Go CI tests.

## Code conventions

- Go: `gofmt`, `go vet` clean; errors wrapped with context; no bare
  `any` in public APIs without justification.
- Python: ruff (E,W,F,I,B,UP,S + bandit rules) and strict mypy; async
  hygiene enforced (no blocking calls in async paths).
- TypeScript: strict mode from the shared preset; no dead buttons.
- Conventional Commits; meaningful, independently testable units.

## Troubleshooting

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| `readyz` shows postgres down | stack not up | `make infra-up` |
| Migration refuses to start (advisory lock) | another migrate is running | wait or stop it |
| Python pipeline tests skip | `AGENTOS_INTEGRATION` unset | export it |
| Python pipeline tests fail on roles | Go migrations not applied | run the Go integration suite once |
| 429 on auth endpoints | per-IP limiter in tests | pace requests or use distinct servers |
| OPA denies everything | baseline not deployed | start the control plane (deploys baseline) or run the test fixture |

## Environment variables

See [`.env.example`](../../.env.example) for the full list with
local-development defaults. Production requires `AGENTOS_SECRETS_MASTER_KEY`
and a strong `AGENTOS_INTERNAL_API_TOKEN`.
