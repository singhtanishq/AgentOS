"""Execution job contract (mirrors the Go control plane queue.Job)."""

from __future__ import annotations

from datetime import datetime
from typing import Any

from pydantic import BaseModel, Field


class Job(BaseModel):
    """One execution job delivered over NATS JetStream."""

    execution_id: str
    organization_id: str
    agent_id: str
    agent_version_id: str
    deployment_id: str | None = None
    identity_id: str | None = None
    input: dict[str, Any] = Field(default_factory=dict)
    submitted_at: datetime | None = None
    attempt: int = 1
