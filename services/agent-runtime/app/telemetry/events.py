"""Execution telemetry: structured events and OpenTelemetry-style spans.

Events are appended to execution_events (authoritative, org-scoped) and
published to the platform events stream for webhook fan-out. Spans build
the execution timeline shown in the trace explorer.
"""

from __future__ import annotations

import json
import logging
from typing import Any, Protocol

logger = logging.getLogger(__name__)


class EventPublisher(Protocol):
    async def publish_event(
        self, organization_id: str, event_type: str, payload: dict[str, Any]
    ) -> None: ...


class ExecutionTelemetry:
    """Writes execution events and spans with per-execution ordering.

    Every write runs inside an organization-scoped transaction: telemetry
    tables carry row-level security like everything else.
    """

    def __init__(
        self, db: Any, publisher: EventPublisher | None, worker_id: str
    ) -> None:
        self._db = db
        self._publisher = publisher
        self._worker_id = worker_id
        self._trace_id: str | None = None

    async def start_trace(self, org_id: str, execution_id: str) -> str:
        """Create the trace and root span for an execution."""
        async with self._db.org_tx(org_id) as conn:
            row = await conn.fetchrow(
                """
                INSERT INTO traces
                    (organization_id, execution_id, root_span_name, status, started_at)
                VALUES ($1, $2, 'invoke_agent', 'unset', now())
                RETURNING id
                """,
                org_id,
                execution_id,
            )
        if row is None:  # pragma: no cover - insert always returns a row
            raise RuntimeError("failed to create trace")
        trace_id = str(row["id"])
        self._trace_id = trace_id
        await self.span(org_id, execution_id, "invoke_agent")
        return trace_id

    async def span(
        self,
        org_id: str,
        execution_id: str,
        name: str,
        *,
        attributes: dict[str, Any] | None = None,
        parent_span_id: str | None = None,
        started_offset_ms: int | None = None,
    ) -> str:
        """Open a span. The span is closed by ``end_span`` or auto-closed
        with an instant duration when the caller omits it."""
        async with self._db.org_tx(org_id) as conn:
            row = await conn.fetchrow(
                """
                INSERT INTO spans
                    (trace_id, parent_span_id, organization_id, execution_id,
                     name, kind, status, started_at, ended_at, duration_ms, attributes)
                VALUES (
                    (SELECT id FROM traces WHERE execution_id = $2::uuid LIMIT 1),
                    $3::uuid, $1::uuid, $2::uuid, $4, 'internal', 'unset',
                    now() - make_interval(secs => $5::float8 / 1000.0),
                    now(), $5::int, $6::jsonb)
                RETURNING id
                """,
                org_id,
                execution_id,
                parent_span_id,
                name,
                started_offset_ms if started_offset_ms is not None else 0,
                json.dumps(attributes or {}),
            )
        if row is None:  # pragma: no cover
            raise RuntimeError("failed to create span")
        return str(row["id"])

    async def end_span(self, org_id: str, span_id: str, status: str = "ok") -> None:
        async with self._db.org_tx(org_id) as conn:
            await conn.execute(
                """
                UPDATE spans
                SET status = $2, ended_at = now(),
                    duration_ms = GREATEST(0,
                        (EXTRACT(EPOCH FROM (now() - started_at)) * 1000)::int)
                WHERE id = $1
                """,
                span_id,
                status,
            )

    async def finish_trace(self, org_id: str, execution_id: str, status: str) -> None:
        if self._trace_id is None:
            return
        async with self._db.org_tx(org_id) as conn:
            await conn.execute(
                """
                UPDATE traces
                SET status = $4, ended_at = now(),
                    duration_ms = GREATEST(0,
                        (EXTRACT(EPOCH FROM (now() - started_at)) * 1000)::int)
                WHERE id = $2 AND organization_id = $1 AND execution_id = $3
                """,
                org_id,
                self._trace_id,
                execution_id,
                status,
            )

    async def event(
        self, org_id: str, execution_id: str, event_type: str, payload: dict[str, Any]
    ) -> None:
        """Append one structured execution event and publish it."""
        async with self._db.org_tx(org_id) as conn:
            row = await conn.fetchrow(
                """
                INSERT INTO execution_events
                    (execution_id, organization_id, seq, type, payload)
                VALUES ($1::uuid, $2::uuid,
                    (SELECT COALESCE(MAX(seq), -1) + 1
                     FROM execution_events WHERE execution_id = $1::uuid),
                    $3, $4::jsonb)
                RETURNING id, seq
                """,
                execution_id,
                org_id,
                event_type,
                json.dumps(_redact(payload)),
            )
        if row is None:  # pragma: no cover
            raise RuntimeError("failed to append event")
        if self._publisher is not None:
            try:
                await self._publisher.publish_event(
                    org_id,
                    event_type,
                    {
                        "execution_id": execution_id,
                        "seq": row["seq"],
                        "type": event_type,
                        "worker_id": self._worker_id,
                        **_redact(payload),
                    },
                )
            except Exception:  # noqa: BLE001 - event fan-out must not fail the run
                logger.warning(
                    "event publish failed", extra={"execution_id": execution_id, "type": event_type}
                )


# Keys that must never reach event payloads or telemetry attributes.
_REDACTED_KEYS = frozenset(
    {"api_key", "authorization", "secret", "password", "credential", "token"}
)


def _redact(value: Any) -> Any:
    """Shallow redaction of sensitive keys in nested dicts."""
    if isinstance(value, dict):
        return {
            k: ("[REDACTED]" if str(k).lower() in _REDACTED_KEYS else _redact(v))
            for k, v in value.items()
        }
    if isinstance(value, list):
        return [_redact(v) for v in value]
    return value
