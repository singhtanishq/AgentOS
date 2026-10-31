"""Execution state machine.

Transitions are enforced twice: here (validation) and in the database
(guarded conditional UPDATE). Terminal states can never become active
again — a redelivered job that loses the transition race acknowledges
without reprocessing.
"""

from __future__ import annotations

import json
from typing import Any

QUEUED = "queued"
RUNNING = "running"
WAITING_APPROVAL = "waiting_approval"
SUCCEEDED = "succeeded"
FAILED = "failed"
CANCELLED = "cancelled"
TIMED_OUT = "timed_out"

TERMINAL = frozenset({SUCCEEDED, FAILED, CANCELLED, TIMED_OUT})

_TRANSITIONS: dict[str, frozenset[str]] = {
    QUEUED: frozenset({RUNNING, CANCELLED}),
    RUNNING: frozenset({WAITING_APPROVAL, SUCCEEDED, FAILED, CANCELLED, TIMED_OUT}),
    WAITING_APPROVAL: frozenset({RUNNING, FAILED, CANCELLED}),
}


def can_transition(current: str, new: str) -> bool:
    """Whether the state machine permits current → new."""
    if current in TERMINAL:
        return False
    return new in _TRANSITIONS.get(current, frozenset())


class TransitionError(Exception):
    """Raised when a transition is invalid."""


def assert_transition(current: str, new: str) -> None:
    if not can_transition(current, new):
        raise TransitionError(f"cannot transition execution from {current} to {new}")


class ExecutionState:
    """Database-backed execution state with guarded transitions."""

    def __init__(self, pool: Any, execution_id: str) -> None:
        self._pool = pool
        self.execution_id = execution_id

    async def load(self, conn: Any) -> dict[str, Any] | None:
        """Load the execution row (org context must already be set)."""
        row = await conn.fetchrow(
            """
            SELECT id, organization_id, agent_id, agent_version_id, status,
                   input, cancel_requested, worker_id
            FROM executions WHERE id = $1
            """,
            self.execution_id,
        )
        return dict(row) if row else None

    async def transition(
        self,
        conn: Any,
        new_status: str,
        *,
        current: str,
        output: dict[str, Any] | None = None,
        error: dict[str, Any] | None = None,
        started_at: bool = False,
        step_count: int | None = None,
        tool_call_count: int | None = None,
        prompt_tokens: int = 0,
        completion_tokens: int = 0,
        cost_estimate_minor: int = 0,
        pricing_version: int = 0,
        worker_id: str | None = None,
    ) -> bool:
        """Apply a guarded transition; returns False when the row moved on."""
        assert_transition(current, new_status)
        sets = ["status = $3", "updated_at = now()"]
        args: list[Any] = [self.execution_id, current, new_status]
        if output is not None:
            sets.append(f"output = ${len(args) + 1}::jsonb")
            args.append(json.dumps(output))
        if error is not None:
            sets.append(f"error = ${len(args) + 1}::jsonb")
            args.append(json.dumps(error))
        if started_at:
            sets.append("started_at = COALESCE(started_at, now())")
        if step_count is not None:
            sets.append(f"step_count = ${len(args) + 1}")
            args.append(step_count)
        if tool_call_count is not None:
            sets.append(f"tool_call_count = ${len(args) + 1}")
            args.append(tool_call_count)
        if prompt_tokens or completion_tokens:
            sets.append(f"prompt_tokens = ${len(args) + 1}")
            args.append(prompt_tokens)
            sets.append(f"completion_tokens = ${len(args) + 1}")
            args.append(completion_tokens)
            sets.append(f"total_tokens = ${len(args) + 1}")
            args.append(prompt_tokens + completion_tokens)
        if cost_estimate_minor:
            sets.append(f"cost_estimate_minor = ${len(args) + 1}")
            args.append(cost_estimate_minor)
        if pricing_version:
            sets.append(f"pricing_version = ${len(args) + 1}")
            args.append(pricing_version)
        if worker_id is not None:
            sets.append(f"worker_id = ${len(args) + 1}")
            args.append(worker_id)
        if new_status in TERMINAL:
            sets.append("completed_at = now()")

        # The SET clause is assembled from fixed internal column fragments
        # (never user input); all values are bound parameters.
        tag = await conn.execute(
            "UPDATE executions SET " + ", ".join(sets) + " WHERE id = $1 AND status = $2",
            *args,
        )
        return str(tag).endswith("1")
