# Event Architecture

## Streams

| Stream | Subjects | Retention | Consumers |
| --- | --- | --- | --- |
| `EXECUTIONS` | `agentos.executions.jobs`, `agentos.executions.dlq` | 7 days / 1M msgs | `execution-workers` (durable, explicit ack) |
| `EVENTS` | `agentos.events.<org_id>.<event_type…>` | 24 hours / 2M msgs | `webhook-dispatcher` (durable, explicit ack) |

## Delivery semantics

- **Jobs** use a durable pull consumer: explicit acks, 2-minute ack wait,
  6 max deliveries. The worker NAKs retryable failures with backoff
  (2s → 5s → 15s → 30s → 60s); exhausted jobs are published to the DLQ
  subject with the failure reason and the execution is failed.
- **Idempotency**: job messages carry `Nats-Msg-Id: <executionId>:<attempt>`
  so JetStream deduplicates; execution submission accepts client
  idempotency keys enforced by a partial unique index (replays return the
  original row without re-enqueueing).
- **Duplicate delivery**: the guarded state machine means only one claim of
  `queued → running` succeeds; losers acknowledge without side effects.
  Resumes of `running` rows are safe because builtin tools are pure.
- **Domain events** are append-only in `execution_events` (the
  authoritative record) and *additionally* published to JetStream for
  fan-out; webhook delivery loss never corrupts execution state.
- **Webhook dispatch** is HMAC-signed (`v1=<hex hmac>` over
  `timestamp.body`) with 5 bounded attempts and backoff; failures are
  recorded in `webhook_deliveries` with `next_retry_at` and eventually
  marked `exhausted` for operator attention.

## Event catalog

| Event | Emitted by | Payload highlights |
| --- | --- | --- |
| `execution.started` | runtime | worker_id, agent_id |
| `model.response` | runtime | step, provider, model, finish_reason, tokens |
| `tool.requested` / `tool.completed` | runtime | tool, arguments, ok, error_code |
| `tool.validation_failed` | runtime | tool, reason |
| `policy.denied` | runtime | tool, reason |
| `approval.requested` | runtime | execution_id, tool, agent_id |
| `execution.completed` / `failed` / `cancelled` | runtime | status, steps, tool_calls, tokens, cost |
| `audit.*` writes | control plane | append-only audit rows (not JetStream) |

## Dead-letter handling

Dead-lettered jobs land on `agentos.executions.dlq` with
`{"reason": …, "job": …}` for operator inspection. The execution row is
marked failed with a structured error; no silent loss is possible without
both a DLQ entry and a terminal execution record.
