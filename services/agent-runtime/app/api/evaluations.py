"""Internal evaluation endpoints: run datasets against agent versions.

Reached by the control plane over the service-token-protected internal
surface. Evaluation runs are synchronous (deterministic rules + bounded
case counts keep them fast).
"""

from __future__ import annotations

import logging
from typing import Any

from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse
from pydantic import BaseModel

from app.core.db import Database
from app.engine.evaluations import EvaluationError, EvaluationRunner
from app.providers import build_provider

logger = logging.getLogger(__name__)


class RunRequest(BaseModel):
    dataset_id: str
    agent_id: str
    agent_version_id: str
    organization_id: str


def evaluation_router(db: Database) -> APIRouter:
    router = APIRouter(prefix="/internal/evaluations", tags=["internal"])

    @router.post("/run")
    async def run_evaluation(payload: RunRequest, _request: Request) -> Any:
        def provider_factory(org_id: str, provider_type: str, model_id: str) -> Any:
            # Evaluations resolve providers the same way executions do;
            # credentials flow through the control plane at run time.
            return build_provider(provider_type, "", "")

        runner = EvaluationRunner(db, provider_factory)
        try:
            summary = await runner.run(
                payload.organization_id,
                payload.dataset_id,
                payload.agent_id,
                payload.agent_version_id,
            )
        except EvaluationError as exc:
            return JSONResponse(
                status_code=422,
                content={"error": {"code": "evaluation_error", "message": str(exc)}},
            )
        return summary

    return router


def register_internal_routes(app: Any, db: Database) -> None:
    """Attach evaluation routes to the runtime application."""
    app.include_router(evaluation_router(db))
