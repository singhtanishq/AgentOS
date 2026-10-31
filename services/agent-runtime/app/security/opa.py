"""Open Policy Agent evaluator with fail-closed semantics.

The engine consults OPA before every tool execution. If OPA is
unreachable the decision is DENY (fail-closed) and recorded — a broken
policy engine must never widen access.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

import httpx


@dataclass(slots=True, frozen=True)
class PolicyDecision:
    allow: bool
    requires_approval: bool
    reason: str
    decision_id: str | None = None


class OPAEvaluator:
    """Evaluates the agentos.authz package against OPA's data API."""

    def __init__(self, base_url: str, timeout_seconds: float = 5.0) -> None:
        self._base_url = base_url.rstrip("/")
        self._timeout = timeout_seconds

    async def evaluate(self, policy_input: dict[str, Any]) -> PolicyDecision:
        try:
            async with httpx.AsyncClient(timeout=self._timeout) as client:
                resp = await client.post(
                    f"{self._base_url}/v1/data/agentos/authz",
                    json={"input": policy_input},
                )
        except Exception as exc:  # noqa: BLE001 - fail closed below
            return PolicyDecision(
                allow=False,
                requires_approval=False,
                reason=f"policy_engine_unavailable: {type(exc).__name__}",
            )
        if resp.status_code != 200:
            return PolicyDecision(
                allow=False,
                requires_approval=False,
                reason=f"policy_engine_error: status {resp.status_code}",
            )
        result = resp.json().get("result") or {}
        decision_id = resp.headers.get("X-Opa-Decision-Id")
        return PolicyDecision(
            allow=bool(result.get("allow", False)),
            requires_approval=bool(result.get("requires_approval", False)),
            reason="policy_evaluated",
            decision_id=decision_id,
        )
