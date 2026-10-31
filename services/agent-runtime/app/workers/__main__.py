"""Runtime process entrypoint: the health/admin API plus the embedded
execution worker, supervised together."""

from __future__ import annotations

import asyncio
import logging

import uvicorn

from app.config import get_settings
from app.core.db import Database
from app.security.control_plane import ControlPlaneClient
from app.workers.execution_worker import ExecutionWorker
from app.workers.nats_context import NatsContext

logging.basicConfig(level=logging.INFO, format="%(levelname)s %(name)s %(message)s")


async def run_all() -> None:
    settings = get_settings()

    from app.telemetry import metrics as otel_metrics

    otel_metrics.init_metrics(settings.otlp_endpoint or None)

    db = Database(settings.worker_database_url)
    await db.connect()

    nats_ctx = NatsContext(settings.nats_url)
    await nats_ctx.connect()

    control_plane = ControlPlaneClient(settings.control_plane_url, settings.internal_api_token)
    from app.security.opa import OPAEvaluator

    worker = ExecutionWorker(
        nats_ctx, db, control_plane, opa=OPAEvaluator(settings.opa_url)
    )

    shutdown = asyncio.Event()

    async def serve_api() -> None:
        config = uvicorn.Config(
            "app.main:app",
            host=settings.http_addr,
            port=settings.http_port,
            log_level=settings.log_level.lower(),
        )
        server = uvicorn.Server(config)
        await server.serve()

    api_task = asyncio.create_task(serve_api())
    worker_task = asyncio.create_task(worker.run_forever(shutdown))

    try:
        # Run until either task fails; then stop the sibling.
        done, pending = await asyncio.wait(
            {api_task, worker_task}, return_when=asyncio.FIRST_COMPLETED
        )
        for task in done:
            exc = task.exception()
            if exc is not None:
                logging.error("supervised task failed: %s", exc)
    finally:
        shutdown.set()
        for task in (api_task, worker_task):
            task.cancel()
        await asyncio.gather(api_task, worker_task, return_exceptions=True)
        await nats_ctx.close()
        await db.close()


def main() -> None:
    try:
        asyncio.run(run_all())
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
