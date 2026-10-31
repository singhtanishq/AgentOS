"""Resource collections for the AgentOS API."""

from __future__ import annotations

import time
from typing import TYPE_CHECKING, Any

from agentos.models import Agent, Execution

if TYPE_CHECKING:
    from agentos.client import AgentOS


class ExecutionsResource:
    """Create and inspect agent executions."""

    def __init__(self, client: AgentOS) -> None:
        self._client = client

    def create(
        self, agent_id: str, input: dict[str, Any], *, idempotency_key: str = ""
    ) -> Execution:
        """Submit an execution for an agent.

        Args:
            agent_id: The agent to run.
            input: Execution input, e.g. {"query": "..."}.
            idempotency_key: Optional dedupe key; retries return the
                original execution instead of creating a new one.
        """
        payload: dict[str, Any] = {"agent_id": agent_id, "input": input}
        if idempotency_key:
            payload["idempotency_key"] = idempotency_key
        data = self._client.post("/executions", json=payload)
        return Execution.from_api(data)

    def get(self, execution_id: str) -> Execution:
        """Fetch one execution by id."""
        return Execution.from_api(self._client.get(f"/executions/{execution_id}"))

    def list(self, *, status: str = "", limit: int = 50) -> list[Execution]:
        """List executions, newest first."""
        params: dict[str, Any] = {"limit": limit}
        if status:
            params["status"] = status
        data = self._client.get("/executions", params=params)
        return [Execution.from_api(item) for item in data.get("items", [])]

    def cancel(self, execution_id: str) -> Execution:
        """Request cooperative cancellation."""
        return Execution.from_api(self._client.post(f"/executions/{execution_id}/cancel", json={}))

    def wait(
        self,
        execution_id: str,
        *,
        timeout_seconds: float = 120.0,
        poll_interval: float = 1.0,
    ) -> Execution:
        """Poll until the execution reaches a terminal state."""
        deadline = time.monotonic() + timeout_seconds
        while True:
            execution = self.get(execution_id)
            if execution.is_terminal:
                return execution
            if time.monotonic() > deadline:
                raise TimeoutError(
                    f"execution {execution_id} did not finish within {timeout_seconds}s"
                )
            time.sleep(poll_interval)


class AgentsResource:
    """Inspect the agent registry."""

    def __init__(self, client: AgentOS) -> None:
        self._client = client

    def list(self, *, query: str = "", status: str = "") -> list[Agent]:
        """List agents with optional search and status filters."""
        params: dict[str, Any] = {}
        if query:
            params["q"] = query
        if status:
            params["status"] = status
        data = self._client.get("/agents", params=params)
        return [Agent.from_api(item) for item in data.get("items", [])]

    def get(self, agent_id: str) -> dict[str, Any]:
        """Fetch one agent with its version history."""
        return self._client.get(f"/agents/{agent_id}")
