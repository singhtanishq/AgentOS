# Execution Plane (Python Runtime)

The agent runtime consumes durable jobs and executes the agent loop: model
invocation, tool execution, authorization, validation, metering and
telemetry.

## Layout

```text
services/agent-runtime/
├── app/
│   ├── core/         # asyncpg pool + execution state machine
│   ├── engine/       # job contract, execution loop, tools, builtins, evaluations
│   ├── providers/    # provider contract + OpenAI/Anthropic/Gemini/compatible/mock
│   ├── mcp/          # MCP client adapter (official SDK)
│   ├── security/     # control-plane client, OPA evaluator
│   ├── telemetry/    # events/spans (DB + JetStream), OTLP metrics
│   ├── workers/      # JetStream consumer + supervised process
│   └── api/          # internal evaluation endpoints
└── tests/
```

## The execution state machine

```text
queued ──▶ running ──▶ succeeded
   │          │  ├──▶ failed
   │          │  ├──▶ timed_out
   │          │  └──▶ cancelled
   │          └──▶ waiting_approval ──▶ running (resume) / failed / cancelled
   └──▶ cancelled
```

- Transitions are validated in code **and** enforced by a guarded
  conditional `UPDATE … WHERE status = $previous` — one writer wins.
- Terminal states are absorbing: a redelivered job that finds a terminal
  row acknowledges without reprocessing.
- Redelivery of a `running` row resumes; approval-approved rows transition
  `waiting_approval → running` when the control plane re-publishes the job.

## The agent loop

For each step (bounded by `limits.max_steps`):

1. Poll `cancel_requested` (cooperative cancellation at step boundaries).
2. Check wall-clock deadline and token budget.
3. Model request (span + event recorded; usage accumulated).
4. If the model emits tool calls:
   - validate arguments against the tool's schema (model output is never
     trusted),
   - consult OPA (`tool.execute`, fail-closed) — deny returns a structured
     tool error; `requires_approval` parks the execution,
   - execute inside a per-tool timeout, record span + events,
   - feed results back as `tool` messages.
5. Otherwise validate non-empty output and finalize.

## Limits and failure semantics

| Limit | Default | Hard ceiling |
| --- | --- | --- |
| max_steps | 12 | 50 |
| max_tool_calls | 25 | 100 |
| max_total_tokens | 120k | 1M |
| timeout_ms | 300k | 900k |

- **Retryable** provider errors (rate limit, overload, timeout, network)
  → NAK with backoff (2s/5s/15s/30s/60s); JetStream redelivers; after 6
  attempts the job is dead-lettered and the execution failed.
- **Terminal** errors (auth, invalid request, policy denial, budget
  exhausted, limits) → execution marked failed, job acknowledged.
- Malformed jobs are dead-lettered immediately.

## Provider adapters

A normalized contract (`Message / ModelRequest / ModelResponse / Usage`)
with wire adapters for OpenAI Chat Completions (also compatible
endpoints), Anthropic Messages and Google Gemini. Usage comes verbatim
from provider responses; the runtime never invents token counts.

The **mock provider** is deterministic: scripted calculator calls for
arithmetic, explicit `[[call:tool {json}]]` directives, simulated token
accounting. It keeps the whole platform functional without paid APIs.

Credentials are fetched from the control plane's token-protected
internal endpoint at execution time — keys never touch NATS or storage.

## Tool gateway

- Registry-driven: a bound tool that is missing, disabled or unknown is a
  terminal configuration error.
- Argument validation: JSON parse, size cap, schema checks (types,
  required, additionalProperties), string length caps.
- `calculator` — AST-sandboxed arithmetic (numeric literals and + - * / %
  // ** only; names/calls rejected; depth and exponent limits).
- `http.request` — GET-only, host allowlist, SSRF guards (private,
  reserved, loopback, link-local addresses blocked at resolution),
  revalidated redirects, response size cap, strict timeout.
- Demo fixtures: `web.search`, `document.read`, `database.query`,
  `crm.lookup`; `email.draft` is a mock that never sends.
- `mcp` tools execute over the official SDK against SSRF-validated
  streamable-HTTP servers.

## Telemetry

- `execution_events` — ordered, append-only lifecycle stream (started,
  model.response, tool.requested/completed, policy.denied,
  execution.completed/failed) with sensitive-key redaction; also
  published to JetStream `EVENTS` for webhook fan-out.
- `traces` + `spans` — DB-backed OpenTelemetry-shaped timeline
  (`invoke_agent`, `model_request`, `tool_execution`).
- OTLP metrics — `agentos.executions.total`, duration histogram, token
  counter; exported to the collector and scraped by Prometheus.
