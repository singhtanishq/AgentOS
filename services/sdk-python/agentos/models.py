"""Response models."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


@dataclass(slots=True)
class Execution:
    """One agent execution."""

    id: str
    organization_id: str
    agent_id: str
    agent_version_id: str
    status: str
    input: dict[str, Any] = field(default_factory=dict)
    output: dict[str, Any] | None = None
    error: dict[str, Any] | None = None
    total_tokens: int = 0
    cost_estimate_minor: int = 0
    raw: dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_api(cls, data: dict[str, Any]) -> Execution:
        return cls(
            id=str(data.get("id", "")),
            organization_id=str(data.get("organization_id", "")),
            agent_id=str(data.get("agent_id", "")),
            agent_version_id=str(data.get("agent_version_id", "")),
            status=str(data.get("status", "")),
            input=dict(data.get("input") or {}),
            output=data.get("output"),
            error=data.get("error"),
            total_tokens=int(data.get("total_tokens", 0)),
            cost_estimate_minor=int(data.get("cost_estimate_minor", 0)),
            raw=data,
        )

    @property
    def is_terminal(self) -> bool:
        return self.status in ("succeeded", "failed", "cancelled", "timed_out")


@dataclass(slots=True)
class Agent:
    """A registered agent."""

    id: str
    name: str
    description: str
    status: str
    latest_version: int
    version_count: int
    raw: dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_api(cls, data: dict[str, Any]) -> Agent:
        return cls(
            id=str(data.get("id", "")),
            name=str(data.get("name", "")),
            description=str(data.get("description", "")),
            status=str(data.get("status", "")),
            latest_version=int(data.get("latest_version", 0)),
            version_count=int(data.get("version_count", 0)),
            raw=data,
        )
