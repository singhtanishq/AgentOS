"""FastAPI application exposing runtime health and administrative endpoints."""

from __future__ import annotations

import asyncio
import time
from collections.abc import Awaitable, Callable
from typing import Any

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

from app import __version__
from app.core.db import Database

Check = Callable[[], Awaitable[None]]


def create_app(db: Database | None = None) -> FastAPI:
    """Build the runtime API application.

    When a database handle is supplied (the supervised worker process),
    internal evaluation routes are registered as well.
    """
    app = FastAPI(
        title="AgentOS Agent Runtime",
        version=__version__,
        docs_url="/v1/docs",
        openapi_url="/v1/openapi.json",
        # The runtime is an internal execution-plane service; it must not
        # be indexed or embedded.
        servers=[{"url": "http://localhost:8090"}],
    )
    checks: dict[str, Check] = {}
    app.state.readiness_checks = checks

    @app.middleware("http")
    async def add_request_context(
        request: Request, call_next: Callable[[Any], Awaitable[Any]]
    ) -> Any:
        start = time.perf_counter()
        request_id = request.headers.get("X-Request-Id")
        response = await call_next(request)
        response.headers["X-Request-Id"] = request_id or response.headers.get(
            "X-Request-Id", ""
        )
        response.headers["X-Response-Time-Ms"] = (
            f"{(time.perf_counter() - start) * 1000:.1f}"
        )
        return response

    @app.exception_handler(Exception)
    async def unhandled_exception(
        request: Request, _exc: Exception
    ) -> JSONResponse:
        return JSONResponse(
            status_code=500,
            content={
                "error": {
                    "code": "internal_error",
                    "message": "an internal error occurred",
                }
            },
            headers={"X-Request-Id": request.headers.get("X-Request-Id", "")},
        )

    @app.get("/healthz")
    async def liveness() -> dict[str, str]:
        return {"status": "alive"}

    @app.get("/readyz")
    async def readiness() -> JSONResponse:
        results: dict[str, str] = {}
        failed = False
        for name, check in checks.items():
            try:
                await asyncio.wait_for(check(), timeout=5.0)
                results[name] = "ok"
            except Exception as exc:  # noqa: BLE001 - readiness reports all failures
                results[name] = f"unavailable: {type(exc).__name__}"
                failed = True
        status_code = 503 if failed else 200
        return JSONResponse(
            status_code=status_code,
            content={"status": "degraded" if failed else "ready", "components": results},
        )

    @app.get("/v1/runtime")
    async def runtime_info() -> dict[str, Any]:
        return {
            "name": "agentos-runtime",
            "version": __version__,
            "status": "initializing",
        }

    if db is not None:
        from app.api.evaluations import register_internal_routes

        register_internal_routes(app, db)

    return app


app = create_app()
