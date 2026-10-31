"""NATS JetStream wiring for the execution plane worker.

Mirrors the control plane's topology (queue.EnsureStream) so a fresh
environment reaches the same stream/consumer configuration regardless of
which service starts first. Jobs use an explicit-ack durable pull
consumer with bounded redelivery; exhausted jobs are dead-lettered.
"""

from __future__ import annotations

import logging
from typing import Any, Protocol

import nats
import nats.js
from nats.aio.msg import Msg

logger = logging.getLogger(__name__)

SUBJECT_JOBS = "agentos.executions.jobs"
SUBJECT_DLQ = "agentos.executions.dlq"
SUBJECT_EVENTS_PREFIX = "agentos.events"
STREAM_JOBS = "EXECUTIONS"
STREAM_EVENTS = "EVENTS"
CONSUMER_WORKERS = "execution-workers"
MAX_DELIVER = 6
ACK_WAIT_SECONDS = 120


class NatsContext:
    """Owns the NATS connection, JetStream context and the pull consumer."""

    def __init__(self, url: str) -> None:
        self._url = url
        self.nc: Any = None
        self.js: Any = None
        self.sub: Any = None

    async def connect(self) -> None:
        self.nc = await nats.connect(
            servers=[self._url],
            name="agentos-agent-runtime",
            max_reconnect_attempts=-1,
            reconnect_time_wait=2,
        )
        self.js = self.nc.jetstream()
        await self.ensure_topology()
        self.sub = await self.js.pull_subscribe(
            SUBJECT_JOBS, durable=CONSUMER_WORKERS, stream=STREAM_JOBS
        )

    async def ensure_topology(self) -> None:
        for name, subjects, max_age_hours in (
            (STREAM_JOBS, [SUBJECT_JOBS, SUBJECT_DLQ], 24 * 7),
            (STREAM_EVENTS, [f"{SUBJECT_EVENTS_PREFIX}.>"], 24),
        ):
            cfg: dict[str, Any] = {
                "name": name,
                "subjects": subjects,
                "retention": "limits",
                "storage": "file",
                "max_age": max_age_hours * 3600,
            }
            try:
                await self.js.add_stream(**cfg)
            except Exception:
                try:
                    await self.js.update_stream(**cfg)
                except Exception:  # noqa: BLE001 - topology may be managed elsewhere
                    logger.warning("could not ensure stream %s", name)
        try:
            await self.js.add_consumer(
                STREAM_JOBS,
                durable_name=CONSUMER_WORKERS,
                ack_policy="explicit",
                ack_wait=ACK_WAIT_SECONDS,
                max_deliver=MAX_DELIVER,
                filter_subject=SUBJECT_JOBS,
            )
        except Exception:  # noqa: BLE001 - consumer already exists with config
            logger.info("consumer %s already configured", CONSUMER_WORKERS)

    async def fetch(self, batch: int = 1, fetch_timeout: float = 5.0) -> list[Msg]:
        if self.sub is None:
            raise RuntimeError("worker is not connected")
        try:
            fetched = await self.sub.fetch(batch, timeout=fetch_timeout)
        except TimeoutError:
            return []
        return list(fetched)

    async def publish_event(
        self, organization_id: str, event_type: str, payload: dict[str, Any]
    ) -> None:
        subject = f"{SUBJECT_EVENTS_PREFIX}.{organization_id}.{event_type}"
        import json

        await self.js.publish(subject, json.dumps(payload).encode())

    async def publish_dlq(self, job_payload: bytes, reason: str) -> None:
        import json

        await self.js.publish(
            SUBJECT_DLQ,
            json.dumps({"reason": reason, "job": json.loads(job_payload)}).encode(),
        )

    async def close(self) -> None:
        if self.nc is not None:
            await self.nc.drain()


class Publisher(Protocol):
    """The publishing surface the engine and telemetry depend on."""

    async def publish_event(
        self, organization_id: str, event_type: str, payload: dict[str, Any]
    ) -> None: ...
