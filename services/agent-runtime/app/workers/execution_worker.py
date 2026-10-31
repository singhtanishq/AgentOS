"""The execution worker: consumes durable jobs and runs agent executions.

Acknowledgment policy:
* success / terminal failure      → ack (terminal failure already recorded)
* retryable failure               → nak with backoff (JetStream redelivers)
* exhausted redelivery budget     → dead-letter + terminal failure record
* unexpected crash of the handler → nak (the job is redelivered and resumes)
"""

from __future__ import annotations

import asyncio
import logging
import socket
from typing import Any

from nats.aio.msg import Msg

from app.core.db import Database
from app.core.state_machine import FAILED, RUNNING, ExecutionState
from app.engine.execution import (
    ApprovalRequestedError,
    ExecutionRunner,
    QueueRetryableError,
)
from app.engine.job import Job
from app.security.control_plane import ControlPlaneClient
from app.workers.nats_context import MAX_DELIVER, NatsContext

logger = logging.getLogger(__name__)

# NAK backoff schedule in seconds (indexed by delivery attempt - 1).
RETRY_DELAYS = [2, 5, 15, 30, 60]


class ExecutionWorker:
    """Sequential job processor for the execution plane."""

    def __init__(
        self,
        nats_ctx: NatsContext,
        db: Database,
        control_plane: ControlPlaneClient,
        opa: Any | None = None,
    ) -> None:
        self._nats = nats_ctx
        self._db = db
        self._control_plane = control_plane
        self._opa = opa
        self._worker_id = f"worker-{socket.gethostname()}-{id(self):x}"

    async def run_forever(self, shutdown: asyncio.Event) -> None:
        logger.info("execution worker started", extra={"worker_id": self._worker_id})
        while not shutdown.is_set():
            try:
                messages = await self._nats.fetch(batch=1, fetch_timeout=5.0)
            except Exception:  # noqa: BLE001 - transient NATS failures must not kill the loop
                logger.exception("fetch failed — backing off")
                await asyncio.sleep(2)
                continue
            for msg in messages:
                await self.handle(msg)
        logger.info("execution worker stopped")

    async def handle(self, msg: Msg) -> None:
        try:
            job = Job.model_validate_json(msg.data)
        except Exception:  # noqa: BLE001 - malformed jobs are dead-lettered
            logger.exception("unparseable job payload — dead-lettering")
            await self._nats.publish_dlq(msg.data, "unparseable job payload")
            await msg.term()
            return

        meta = msg.metadata
        logger.info(
            "job received",
            extra={
                "execution_id": job.execution_id,
                "attempt": meta.num_delivered,
            },
        )

        runner = ExecutionRunner(
            self._db, self._nats, self._control_plane, self._worker_id, opa=self._opa
        )
        try:
            status = await runner.run(job)
        except ApprovalRequestedError as exc:
            # The execution is parked in waiting_approval; the control plane
            # re-publishes the job once an approver decides.
            logger.info(
                "execution waiting for approval",
                extra={"execution_id": job.execution_id, "tool": str(exc)},
            )
            await msg.ack()
            return
        except QueueRetryableError as exc:
            if meta.num_delivered >= MAX_DELIVER:
                await self._exhaust(job, msg, f"retry budget exhausted: {exc.cause}")
                return
            delay = RETRY_DELAYS[min(meta.num_delivered - 1, len(RETRY_DELAYS) - 1)]
            logger.warning(
                "retryable failure — scheduling redelivery",
                extra={"execution_id": job.execution_id, "delay_s": delay},
            )
            await msg.nak(delay=delay)
            return
        except Exception:
            # Unexpected failure: record it, then dead-letter. The state
            # machine guard keeps a terminal execution from restarting.
            logger.exception(
                "unexpected worker failure", extra={"execution_id": job.execution_id}
            )
            await self._record_unexpected(job)
            await self._nats.publish_dlq(msg.data, "unexpected worker failure")
            await msg.term()
            return

        logger.info(
            "job finished",
            extra={"execution_id": job.execution_id, "status": status},
        )
        await msg.ack()

    async def _exhaust(self, job: Job, msg: Msg, reason: str) -> None:
        """Dead-letter a job whose redelivery budget is exhausted."""
        logger.error(
            "job dead-lettered",
            extra={"execution_id": job.execution_id, "reason": reason},
        )
        await self._nats.publish_dlq(msg.data, reason)
        await self._record_unexpected(job, message=reason)
        await msg.term()

    async def _record_unexpected(
        self, job: Job, message: str = "unexpected worker failure"
    ) -> None:
        try:
            async with self._db.org_tx(job.organization_id) as conn:
                row = await ExecutionState(self._db.pool, job.execution_id).load(conn)
                if row is None or row["status"] not in (RUNNING,):
                    return
                await ExecutionState(self._db.pool, job.execution_id).transition(
                    conn,
                    FAILED,
                    current=RUNNING,
                    error={
                        "code": "worker_error",
                        "message": message,
                        "classification": "terminal",
                    },
                    worker_id=self._worker_id,
                )
        except Exception:  # noqa: BLE001 - finalization must not crash the worker
            logger.exception(
                "unexpected-failure recording errored",
                extra={"execution_id": job.execution_id},
            )
