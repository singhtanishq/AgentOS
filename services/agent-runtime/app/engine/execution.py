"""The agent execution engine.

Implements the full execution flow: job validation, organization
confirmation, agent-version loading, deployment/identity checks, context
initialization, the model/tool loop with limits enforcement, output
validation, usage recording, lifecycle events and trace persistence.

Failure semantics:
* Retryable provider failures (rate limits, overload, timeouts) raise
  ``QueueRetryableError`` so the worker NAKs and JetStream redelivers.
* Every other failure is terminal: the execution is marked FAILED with a
  structured error and the job is acknowledged.
"""

from __future__ import annotations

import asyncio
import json
import logging
import time
from dataclasses import dataclass, field
from typing import Any

from app.core.state_machine import (
    CANCELLED,
    FAILED,
    QUEUED,
    RUNNING,
    SUCCEEDED,
    TIMED_OUT,
    WAITING_APPROVAL,
    ExecutionState,
)
from app.engine import tools as toolgw
from app.engine.job import Job
from app.engine.tools import ToolResult
from app.providers import build_provider
from app.providers.base import Message, ModelRequest, ProviderError
from app.telemetry import metrics as otel_metrics
from app.telemetry.events import ExecutionTelemetry

logger = logging.getLogger(__name__)

# Requeue rather than fail: JetStream redelivers after the NAK delay.
RETRYABLE_ERROR_KINDS = frozenset({"rate_limited", "overloaded", "timeout", "network"})


class QueueRetryableError(Exception):
    """Wrap a retryable failure so the worker schedules a redelivery."""

    def __init__(self, cause: str) -> None:
        super().__init__(cause)
        self.cause = cause


class ApprovalRequestedError(Exception):
    """A tool requires human approval; the execution now waits."""

    def __init__(self, cause: str) -> None:
        super().__init__(cause)
        self.cause = cause


class TerminalExecutionError(Exception):
    """A terminal failure with a structured, user-presentable reason."""

    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


@dataclass(slots=True)
class RunStats:
    steps: int = 0
    tool_calls: int = 0
    prompt_tokens: int = 0
    completion_tokens: int = 0
    input_price_per_mtok: int = 0
    output_price_per_mtok: int = 0
    pricing_version: int = 0
    provider_name: str = "unknown"
    model_name: str = "unknown"
    trace_id: str | None = None
    open_spans: list[str] = field(default_factory=list)

    def cost_estimate_minor(self) -> int:
        """Estimated cost in minor units from versioned pricing."""
        cost = (
            self.prompt_tokens / 1_000_000 * self.input_price_per_mtok
            + self.completion_tokens / 1_000_000 * self.output_price_per_mtok
        )
        return round(cost)


@dataclass(slots=True, frozen=True)
class ToolDecision:
    action: str  # allow | deny | approval_required
    reason: str


@dataclass(slots=True)
class LoadedContext:
    """Everything the loop needs, loaded under the organization's RLS context."""

    execution: dict[str, Any]
    agent_status: str
    configuration: dict[str, Any]
    provider_type: str
    model_id: str
    tools: tuple[dict[str, Any], ...]
    tool_executors: dict[str, Any]
    tool_specs: dict[str, Any] = field(default_factory=dict)


class ExecutionRunner:
    """Executes one job end to end."""

    def __init__(
        self,
        db: Any,
        publisher: Any,
        control_plane: Any,
        worker_id: str,
        opa: Any | None = None,
        started_at: float | None = None,
    ) -> None:
        self._db = db
        self._publisher = publisher
        self._control_plane = control_plane
        self._worker_id = worker_id
        self._started_at = started_at if started_at is not None else time.monotonic()
        from app.security.opa import OPAEvaluator

        self._opa = opa or OPAEvaluator("http://localhost:8181")

    async def run(self, job: Job) -> str:
        """Process one job; returns the final execution status."""
        started = time.monotonic()
        state = ExecutionState(self._db.pool, job.execution_id)

        # Claim the execution: only one redelivery wins queued → running.
        async with self._db.org_tx(job.organization_id) as conn:
            row = await state.load(conn)
            if row is None:
                return "not_found"
            if row["status"] in (RUNNING, WAITING_APPROVAL):
                # Redelivery of an in-flight execution (retryable provider
                # failure) or an approved-resume: continue processing.
                pass
            elif row["status"] == QUEUED:
                claimed = await state.transition(
                    conn,
                    RUNNING,
                    current=QUEUED,
                    started_at=True,
                    worker_id=self._worker_id,
                )
                if not claimed:
                    return "lost_race"
            else:
                # Terminal (e.g. cancelled while queued) — nothing to do.
                return str(row["status"])

        telemetry = ExecutionTelemetry(self._db, self._publisher, self._worker_id)
        stats = RunStats()
        try:
            async with self._db.org_tx(job.organization_id) as conn:
                ctx = await self._load_context(conn, job)
            stats.trace_id = await telemetry.start_trace(
                job.organization_id, job.execution_id
            )
            await telemetry.event(
                job.organization_id,
                job.execution_id,
                "execution.started",
                {"worker_id": self._worker_id, "agent_id": job.agent_id},
            )
            await self._check_budgets(job)
            output = await self._run_loop(job, ctx, stats, telemetry, started)
            await self._finalize_success(job, ctx, stats, telemetry, output)
            return SUCCEEDED
        except QueueRetryableError as exc:
            # Leave the execution in RUNNING; the redelivery re-enters via
            # resume path (the state guard accepts running → running? no).
            # Redelivery of a RUNNING execution is handled by _resume.
            logger.warning(
                "retryable failure — awaiting redelivery",
                extra={"execution_id": job.execution_id, "cause": exc.cause},
            )
            raise
        except TerminalExecutionError as exc:
            await self._finalize_failure(
                job, stats, telemetry,
                {"code": exc.code, "message": exc.message, "classification": "terminal"},
                FAILED,
            )
            return FAILED
        except TimeoutError:
            await self._finalize_failure(
                job, stats, telemetry,
                {"code": "execution_timeout", "message": "execution exceeded its time limit",
                 "classification": "timeout"},
                TIMED_OUT,
            )
            return TIMED_OUT
        except asyncio.CancelledError:
            await self._finalize_cancelled(job, stats, telemetry)
            raise

    # ------------------------------------------------------------------
    # Context loading
    # ------------------------------------------------------------------
    async def _load_context(self, conn: Any, job: Job) -> LoadedContext:
        execution = await ExecutionState(self._db.pool, job.execution_id).load(conn)
        if execution is None:  # pragma: no cover - claimed above
            raise TerminalExecutionError("execution_missing", "execution row disappeared")

        agent = await conn.fetchrow(
            "SELECT status FROM agents WHERE id = $1 AND deleted_at IS NULL", job.agent_id
        )
        if agent is None:
            raise TerminalExecutionError("agent_missing", "agent not found")
        if agent["status"] == "suspended":
            raise TerminalExecutionError("agent_suspended", "the agent is suspended")

        version = await conn.fetchrow(
            "SELECT status, configuration FROM agent_versions WHERE id = $1",
            job.agent_version_id,
        )
        if version is None or version["status"] != "published":
            raise TerminalExecutionError(
                "version_not_published", "the agent version is not published"
            )
        try:
            configuration = json.loads(version["configuration"])
        except json.JSONDecodeError as exc:
            raise TerminalExecutionError("configuration_invalid", str(exc)) from exc

        # Identity: when the execution carries one it must be active.
        if job.identity_id:
            identity = await conn.fetchrow(
                """
                SELECT status, credential_expires_at FROM agent_identities
                WHERE id = $1 AND agent_id = $2
                """,
                job.identity_id,
                job.agent_id,
            )
            if identity is None:
                raise TerminalExecutionError("identity_missing", "agent identity not found")
            if identity["status"] == "revoked":
                raise TerminalExecutionError("identity_revoked", "the agent identity was revoked")
            if identity["status"] == "suspended":
                raise TerminalExecutionError(
                    "identity_suspended", "the agent identity is suspended"
                )
            if (
                identity["credential_expires_at"] is not None
                and identity["credential_expires_at"].timestamp() < time.time()
            ):
                raise TerminalExecutionError("identity_expired",
                    "the agent identity credential expired")

        # Tool resolution: only registry tools bound in the configuration.
        bound_names = [t.get("name", "") for t in configuration.get("tools", [])]
        tool_executors, tool_specs = await self._load_tool_registry(conn, bound_names)

        model_cfg = configuration.get("model", {})
        provider_type = str(model_cfg.get("provider_type", "mock"))
        model_id = str(model_cfg.get("model_id", ""))

        # Pricing for cost estimation (absent pricing estimates at zero and
        # records pricing_version 0 — always surfaced as an estimate).
        pricing = await conn.fetchrow(
            """
            SELECT m.input_price_per_mtok_minor, m.output_price_per_mtok_minor, m.pricing_version
            FROM models m
            JOIN model_providers p ON p.id = m.provider_id
            WHERE m.model_id = $1 AND p.provider_type = $2 AND m.organization_id = $3
            LIMIT 1
            """,
            model_id,
            provider_type,
            job.organization_id,
        )
        stats_pricing = None
        if pricing is not None:
            stats_pricing = pricing

        ctx = LoadedContext(
            execution=execution,
            agent_status=str(agent["status"]),
            configuration=configuration,
            provider_type=provider_type,
            model_id=model_id,
            tools=tuple(),
            tool_executors=tool_executors,
            tool_specs=tool_specs,
        )
        if stats_pricing is not None:
            ctx.configuration["_pricing"] = {
                "input": stats_pricing["input_price_per_mtok_minor"],
                "output": stats_pricing["output_price_per_mtok_minor"],
                "version": stats_pricing["pricing_version"],
            }
        # Pricing + provider identity ride on the context; the loop copies
        # them into stats (stats outlives the context object).
        ctx.configuration["_identity"] = {
            "provider": provider_type,
            "model": model_id,
        }
        # Build OpenAI-style tool descriptors for the provider.
        ctx.tools = tuple(
            {
                "type": "function",
                "function": {"name": name, "parameters": {}},
            }
            for name in tool_executors
        )
        return ctx

    async def _load_tool_registry(
        self, conn: Any, bound_names: list[str]
    ) -> tuple[dict[str, Any], dict[str, Any]]:
        """Build executors for the bound tools from the org's registry.

        A bound tool that is missing, disabled or unknown is a terminal
        configuration error: the agent cannot run with a gap in its
        declared capabilities.
        """
        if not bound_names:
            return {}, {}
        rows = await conn.fetch(
            """
            SELECT name, kind, config, timeout_ms FROM tools
            WHERE name = ANY($1) AND status = 'active'
            """,
            bound_names,
        )
        from app.engine.builtins import ToolSpec, build_registry
        from app.mcp.client import MCPTool

        registry: dict[str, Any] = {}
        tool_specs: dict[str, Any] = {}
        for row in rows:
            spec = ToolSpec(
                name=row["name"],
                kind=row["kind"],
                config=json.loads(row["config"] or "{}"),
                timeout_seconds=max(1.0, row["timeout_ms"] / 1000.0),
            )
            tool_specs[spec.name] = spec
            if spec.kind == "mcp":
                connection = await conn.fetchrow(
                    """
                    SELECT url, auth FROM mcp_connections
                    WHERE organization_id = current_setting('agentos.org_id')::uuid
                      AND name = $1 AND status = 'active'
                    """,
                    spec.config.get("connection", ""),
                )
                if connection is None:
                    continue
                registry[spec.name] = MCPTool(spec, dict(connection))
            else:
                registry.update(build_registry([spec]))
        for name in bound_names:
            if name not in registry:
                raise TerminalExecutionError(
                    "tool_unavailable",
                    f"tool {name!r} is not active in the registry",
                )
        return registry, tool_specs

    # ------------------------------------------------------------------
    # The model/tool loop
    # ------------------------------------------------------------------
    async def _run_loop(
        self,
        job: Job,
        ctx: LoadedContext,
        stats: RunStats,
        telemetry: ExecutionTelemetry,
        started: float,
    ) -> dict[str, Any]:
        limits = ctx.configuration.get("limits", {})
        max_steps = int(limits.get("max_steps", 12))
        max_tool_calls = int(limits.get("max_tool_calls", 25))
        max_total_tokens = int(limits.get("max_total_tokens", 120_000))
        timeout_ms = int(limits.get("timeout_ms", 300_000))
        model_cfg = ctx.configuration.get("model", {})
        pricing = ctx.configuration.get("_pricing", {})
        stats.input_price_per_mtok = int(pricing.get("input", 0))
        stats.output_price_per_mtok = int(pricing.get("output", 0))
        stats.pricing_version = int(pricing.get("version", 0))
        identity = ctx.configuration.get("_identity", {})
        stats.provider_name = str(identity.get("provider", "unknown"))
        stats.model_name = str(identity.get("model", "unknown"))

        provider = await self._resolve_provider(job, ctx)

        messages = self._build_messages(ctx.configuration, job.input)

        async def deadline_exceeded() -> bool:
            return (time.monotonic() - started) * 1000 > timeout_ms

        content = ""
        for step in range(1, max_steps + 1):
            stats.steps = step
            if await self._cancel_requested(job):
                raise asyncio.CancelledError()
            if await deadline_exceeded():
                raise TimeoutError()
            if stats.prompt_tokens + stats.completion_tokens > max_total_tokens:
                raise TerminalExecutionError(
                    "token_limit_exceeded", "the execution exceeded its total token budget"
                )

            span = await telemetry.span(
                job.organization_id, job.execution_id, "model_request",
                attributes={"model": ctx.model_id, "provider": ctx.provider_type, "step": step},
            )
            try:
                response = await asyncio.wait_for(
                    provider.complete(
                        ModelRequest(
                            model=ctx.model_id,
                            messages=tuple(messages),
                            tools=ctx.tools,
                            temperature=model_cfg.get("temperature"),
                            max_tokens=model_cfg.get("max_tokens"),
                            top_p=model_cfg.get("top_p"),
                        )
                    ),
                    timeout=180.0,
                )
            except ProviderError as exc:
                await telemetry.end_span(job.organization_id, span, "error")
                if exc.kind in RETRYABLE_ERROR_KINDS:
                    raise QueueRetryableError(f"{exc.kind}: {exc}") from exc
                raise TerminalExecutionError(
                    "provider_error", f"{exc.kind}: {exc}"
                ) from exc
            except TimeoutError as exc:
                await telemetry.end_span(job.organization_id, span, "error")
                raise QueueRetryableError("provider request timed out") from exc
            await telemetry.end_span(job.organization_id, span, "ok")

            stats.prompt_tokens += response.usage.input_tokens
            stats.completion_tokens += response.usage.output_tokens
            await telemetry.event(
                job.organization_id,
                job.execution_id,
                "model.response",
                {
                    "step": step,
                    "provider": response.provider,
                    "model": response.model,
                    "finish_reason": response.finish_reason,
                    "input_tokens": response.usage.input_tokens,
                    "output_tokens": response.usage.output_tokens,
                },
            )

            if response.tool_calls:
                messages.append(
                    Message(
                        role="assistant",
                        content=response.content,
                        tool_calls=tuple(
                            {
                                "id": tc.call_id,
                                "type": "function",
                                "function": {"name": tc.name, "arguments": tc.arguments},
                            }
                            for tc in response.tool_calls
                        ),
                    )
                )
                for call in response.tool_calls:
                    if stats.tool_calls >= max_tool_calls:
                        raise TerminalExecutionError(
                            "tool_call_limit_exceeded",
                            "the execution exceeded its tool call budget",
                        )
                    stats.tool_calls += 1
                    result = await self._execute_tool(
                        job, ctx, stats, telemetry, call.name, call.arguments
                    )
                    messages.append(
                        Message(
                            role="tool",
                            tool_call_id=call.call_id,
                            content=json.dumps(
                                result.output if result.ok else {"error": result.output}
                            ),
                        )
                    )
                content = response.content
                continue

            content = response.content
            break
        else:
            raise TerminalExecutionError(
                "step_limit_exceeded", "the execution exceeded its maximum number of steps"
            )

        if not content.strip():
            raise TerminalExecutionError(
                "empty_output", "the model produced no final content"
            )
        return {"content": content, "steps": stats.steps}

    async def _execute_tool(
        self,
        job: Job,
        ctx: LoadedContext,
        stats: RunStats,
        telemetry: ExecutionTelemetry,
        name: str,
        raw_arguments: str,
    ) -> ToolResult:
        span = await telemetry.span(
            job.organization_id, job.execution_id, "tool_execution",
            attributes={"tool": name},
        )
        executor = ctx.tool_executors.get(name)
        if executor is None:
            await telemetry.end_span(job.organization_id, span, "error")
            return ToolResult(False, {"error": "tool is not bound to this agent"}, "tool_unbound")
        try:
            schema = getattr(executor, "input_schema", None)
            arguments = toolgw.validate_arguments(raw_arguments, schema)
        except toolgw.ToolArgumentError as exc:
            await telemetry.end_span(job.organization_id, span, "error")
            await telemetry.event(
                job.organization_id, job.execution_id, "tool.validation_failed",
                {"tool": name, "reason": str(exc)},
            )
            return ToolResult(False, {"error": str(exc)}, "invalid_arguments")

        # Governance: every tool execution passes the policy engine.
        decision = await self._authorize_tool(job, ctx, name)
        if decision.action == "deny":
            await telemetry.end_span(job.organization_id, span, "error")
            await telemetry.event(
                job.organization_id, job.execution_id, "policy.denied",
                {"tool": name, "reason": decision.reason},
            )
            return ToolResult(
                False, {"error": f"denied by policy: {decision.reason}"}, "policy_denied"
            )
        if decision.action == "approval_required":
            await telemetry.end_span(job.organization_id, span, "unset")
            await self._request_approval(job, name, arguments)
            raise ApprovalRequestedError(name)

        await telemetry.event(
            job.organization_id, job.execution_id, "tool.requested",
            {"tool": name, "arguments": arguments},
        )
        try:
            result = await asyncio.wait_for(
                executor.execute(arguments), timeout=toolgw.TOOL_TIMEOUT_SECONDS
            )
        except TimeoutError:
            await telemetry.end_span(job.organization_id, span, "error")
            return ToolResult(False, {"error": "tool timed out"}, "tool_timeout")
        except Exception as exc:  # noqa: BLE001 - tool failures are results, not crashes
            await telemetry.end_span(job.organization_id, span, "error")
            return ToolResult(False, {"error": f"tool failed: {exc}"}, "tool_error")
        status = "ok" if result.ok else "error"
        await telemetry.end_span(job.organization_id, span, status)
        await telemetry.event(
            job.organization_id, job.execution_id, "tool.completed",
            {"tool": name, "ok": result.ok, "error_code": result.error_code},
        )
        return ToolResult(result.ok, result.output, result.error_code)

    async def _authorize_tool(self, job: Job, ctx: LoadedContext, name: str) -> ToolDecision:
        """Consult OPA for this tool execution. Fail-closed on errors."""
        spec = ctx.tool_specs.get(name)
        tool_cfg = spec.config if spec is not None else {}
        approval_required_cfg = tool_cfg.get("approval") == "required"
        approval_state = await self._approval_state(job, name)

        policy_input = {
            "action": "tool.execute",
            "organization_id": job.organization_id,
            "agent": {"id": job.agent_id, "status": ctx.agent_status},
            "tool": {
                "name": name,
                "kind": spec.kind if spec is not None else "unknown",
                "required_role": "developer",
                "status": "active",
                "approval_required": approval_required_cfg,
            },
            "identity": {"status": ""},
            "approval": {"state": approval_state},
        }
        result = await self._opa.evaluate(policy_input)

        # Order matters: allow=false WITH requires_approval is the normal
        # pre-approval state, not a denial.
        if result.requires_approval and approval_state != "approved":
            return ToolDecision("approval_required", "human approval required by policy")
        if not result.allow:
            reason = (
                result.reason
                if result.reason != "policy_evaluated"
                else "policy denied this action"
            )
            await self._log_decision(job, name, "deny", reason)
            return ToolDecision("deny", reason)
        await self._log_decision(job, name, "allow", "policy allowed this action")
        return ToolDecision("allow", "allowed")

    async def _approval_state(self, job: Job, tool_name: str) -> str:
        async with self._db.org_tx(job.organization_id) as conn:
            row = await conn.fetchrow(
                """
                SELECT status FROM approvals
                WHERE execution_id = $1 AND action = $2
                ORDER BY created_at DESC LIMIT 1
                """,
                job.execution_id,
                f"tool.execute:{tool_name}",
            )
        if row is None:
            return "none"
        return str(row["status"])

    async def _log_decision(
        self, job: Job, tool_name: str, decision: str, reason: str
    ) -> None:
        try:
            async with self._db.org_tx(job.organization_id) as conn:
                await conn.execute(
                    """
                    INSERT INTO policy_decisions
                        (organization_id, execution_id, agent_id, principal_type,
                         principal_id, action, resource_type, resource_id, decision, reason)
                    VALUES ($1, $2, $3, 'agent', $3, 'tool.execute', 'tool', $4, $5, $6)
                    """,
                    job.organization_id,
                    job.execution_id,
                    job.agent_id,
                    tool_name,
                    decision,
                    reason,
                )
        except Exception:  # noqa: BLE001 - decision logging must not crash runs
            logger.warning(
                "policy decision logging failed",
                extra={"execution_id": job.execution_id},
            )

    async def _request_approval(
        self, job: Job, tool_name: str, arguments: dict[str, Any]
    ) -> None:
        """Create the approval request and park the execution."""
        async with self._db.org_tx(job.organization_id) as conn:
            existing = await conn.fetchval(
                """
                SELECT status FROM approvals
                WHERE execution_id = $1 AND action = $2 AND status = 'pending'
                LIMIT 1
                """,
                job.execution_id,
                f"tool.execute:{tool_name}",
            )
            if existing is None:
                await conn.execute(
                    """
                    INSERT INTO approvals
                        (organization_id, execution_id, agent_id, action, resource, justification)
                    VALUES ($1, $2, $3, $4, $5::jsonb, $6)
                    """,
                    job.organization_id,
                    job.execution_id,
                    job.agent_id,
                    f"tool.execute:{tool_name}",
                    json.dumps({"tool": tool_name, "arguments": arguments}),
                    "requested automatically by policy during execution",
                )
            state = ExecutionState(self._db.pool, job.execution_id)
            await state.transition(
                conn,
                WAITING_APPROVAL,
                current=RUNNING,
                worker_id=self._worker_id,
            )
        await self._publisher.publish_event(
            job.organization_id,
            "approval.requested",
            {
                "execution_id": job.execution_id,
                "tool": tool_name,
                "agent_id": job.agent_id,
            },
        )

    def _record_metrics(self, status: str, stats: RunStats) -> None:
        duration_ms = int((time.monotonic() - self._started_at) * 1000)
        otel_metrics.record_execution_outcome(
            status=status,
            provider=stats.provider_name,
            model=stats.model_name,
            duration_ms=duration_ms,
            total_tokens=stats.prompt_tokens + stats.completion_tokens,
        )

    async def _check_budgets(self, job: Job) -> None:
        """Block the execution when a blocking budget is already exhausted.

        Enforcement semantics: budget-blocked organizations/agents refuse
        NEW executions; executions already running complete (grace).
        """
        async with self._db.org_tx(job.organization_id) as conn:
            rows = await conn.fetch(
                """
                SELECT b.id, b.name, b.limit_minor, b.enforcement_action,
                       (SELECT COALESCE(SUM(u.cost_estimate_minor), 0)
                        FROM usage_records u
                        WHERE u.organization_id = $1
                          AND (b.agent_id IS NULL OR u.agent_id = b.agent_id)
                          AND u.created_at >= b.period_started_at) AS spent
                FROM budgets b
                WHERE b.organization_id = $1
                  AND b.status = 'active'
                  AND b.enforcement_action = 'block'
                  AND (b.agent_id IS NULL OR b.agent_id = $2)
                """,
                job.organization_id,
                job.agent_id,
            )
        for row in rows:
            if row["spent"] >= row["limit_minor"]:
                async with self._db.org_tx(job.organization_id) as conn:
                    await conn.execute(
                        """
                        INSERT INTO alerts
                            (organization_id, type, severity, title, message,
                             resource_type, resource_id, execution_id)
                        VALUES ($1, 'budget_exceeded', 'critical', $2, $3,
                                'budget', $4::text, $5)
                        """,
                        job.organization_id,
                        f"Budget {row['name']!r} exhausted",
                        "execution blocked: budget spend has reached the configured limit",
                        str(row["id"]),
                        job.execution_id,
                    )
                raise TerminalExecutionError(
                    "budget_exceeded",
                    f"execution blocked: budget {row['name']!r} spend reached its limit",
                )

    async def _record_budget_spend(
        self, job: Job, stats: RunStats
    ) -> None:
        """Refresh budget spend aggregates and raise threshold alerts."""
        cost = stats.cost_estimate_minor()
        if cost <= 0:
            return
        async with self._db.org_tx(job.organization_id) as conn:
            rows = await conn.fetch(
                """
                SELECT id, name, limit_minor, thresholds, agent_id,
                       (SELECT COALESCE(SUM(u.cost_estimate_minor), 0)
                        FROM usage_records u
                        WHERE u.organization_id = $1
                          AND (b.agent_id IS NULL OR u.agent_id = b.agent_id)
                          AND u.created_at >= b.period_started_at) AS spent
                FROM budgets b
                WHERE b.organization_id = $1 AND b.status = 'active'
                  AND (b.agent_id IS NULL OR b.agent_id = $2)
                """,
                job.organization_id,
                job.agent_id,
            )
        for row in rows:
            spent = int(row["spent"])
            limit = int(row["limit_minor"])
            if limit <= 0:
                continue
            pct = spent * 100 // limit
            thresholds = row["thresholds"] or {}
            for key in ("50", "80", "100"):
                threshold = int(key)
                if pct >= threshold and thresholds.get(key) in ("alert", "enforce"):
                    severity = "critical" if threshold >= 100 else "warning"
                    async with self._db.org_tx(job.organization_id) as conn:
                        await conn.execute(
                            """
                            INSERT INTO alerts
                                (organization_id, type, severity, title, message,
                                 resource_type, resource_id)
                            SELECT $1, 'budget_threshold', $2, $3, $4, 'budget', $5::text
                            WHERE NOT EXISTS (
                                SELECT 1 FROM alerts
                                WHERE organization_id = $1 AND type = 'budget_threshold'
                                  AND resource_id = $5::text
                                  AND severity = $2
                                  AND created_at > now() - interval '1 hour'
                            )
                            """,
                            job.organization_id,
                            severity,
                            f"Budget {row['name']!r} at {pct}%",
                            f"spend {spent} of {limit} minor units in the current period",
                            str(row["id"]),
                        )

    async def _cancel_requested(self, job: Job) -> bool:
        row = await self._db.pool.fetchrow(
            "SELECT cancel_requested FROM executions WHERE id = $1", job.execution_id
        )
        return bool(row and row["cancel_requested"])

    def _build_messages(
        self, configuration: dict[str, Any], execution_input: dict[str, Any]
    ) -> list[Message]:
        messages = [Message(role="system", content=str(configuration.get("instructions", "")))]
        # The input contract: {"query": "..."} or {"messages": [...]}.
        if "messages" in execution_input and isinstance(execution_input["messages"], list):
            for m in execution_input["messages"]:
                if isinstance(m, dict) and "role" in m and "content" in m:
                    messages.append(Message(role=str(m["role"]), content=str(m["content"])))
        query = execution_input.get("query")
        if query is None and len(messages) == 1:
            query = json.dumps(execution_input)
        if query is not None:
            messages.append(Message(role="user", content=str(query)))
        return messages

    async def _resolve_provider(self, job: Job, ctx: LoadedContext) -> Any:
        """Build the provider adapter; fetch credentials via the internal API."""
        if ctx.provider_type == "mock":
            return build_provider("mock", "")
        async with self._db.org_tx(job.organization_id) as conn:
            cred = await conn.fetchrow(
                """
                SELECT c.id FROM model_credentials c
                JOIN model_providers p ON p.id = c.provider_id
                WHERE p.provider_type = $1 AND p.organization_id = $2
                  AND c.status = 'active'
                ORDER BY c.created_at DESC LIMIT 1
                """,
                ctx.provider_type,
                job.organization_id,
            )
        if cred is None:
            raise TerminalExecutionError(
                "credential_missing",
                f"no active credential for provider {ctx.provider_type!r}",
            )
        revealed = await self._control_plane.reveal_credential(
            str(cred["id"]), job.organization_id
        )
        return build_provider(
            revealed["provider_type"], revealed["api_key"], revealed.get("base_url", "")
        )

    # ------------------------------------------------------------------
    # Finalization
    # ------------------------------------------------------------------
    async def _finalize_success(
        self,
        job: Job,
        ctx: LoadedContext,
        stats: RunStats,
        telemetry: ExecutionTelemetry,
        output: dict[str, Any],
    ) -> None:
        pricing = stats.pricing_version
        async with self._db.org_tx(job.organization_id) as conn:
            state = ExecutionState(self._db.pool, job.execution_id)
            await state.transition(
                conn,
                SUCCEEDED,
                current=RUNNING,
                output=output,
                step_count=stats.steps,
                tool_call_count=stats.tool_calls,
                prompt_tokens=stats.prompt_tokens,
                completion_tokens=stats.completion_tokens,
                cost_estimate_minor=stats.cost_estimate_minor(),
                pricing_version=pricing,
                worker_id=self._worker_id,
            )
            await conn.execute(
                """
                INSERT INTO usage_records
                    (organization_id, execution_id, agent_id, provider, model,
                     input_tokens, output_tokens, total_tokens, tool_calls,
                     duration_ms, cost_estimate_minor, pricing_version)
                VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
                """,
                job.organization_id,
                job.execution_id,
                job.agent_id,
                stats.provider_name,
                stats.model_name,
                stats.prompt_tokens,
                stats.completion_tokens,
                stats.prompt_tokens + stats.completion_tokens,
                stats.tool_calls,
                0,
                stats.cost_estimate_minor(),
                pricing,
            )
        await self._record_budget_spend(job, stats)
        await telemetry.finish_trace(job.organization_id, job.execution_id, "ok")
        await telemetry.event(
            job.organization_id, job.execution_id, "execution.completed",
            {
                "status": SUCCEEDED,
                "steps": stats.steps,
                "tool_calls": stats.tool_calls,
                "total_tokens": stats.prompt_tokens + stats.completion_tokens,
                "cost_estimate_minor": stats.cost_estimate_minor(),
            },
        )
        self._record_metrics(SUCCEEDED, stats)

    async def _finalize_failure(
        self,
        job: Job,
        stats: RunStats,
        telemetry: ExecutionTelemetry,
        error: dict[str, Any],
        status: str,
    ) -> None:
        try:
            async with self._db.org_tx(job.organization_id) as conn:
                state = ExecutionState(self._db.pool, job.execution_id)
                await state.transition(
                    conn,
                    status,
                    current=RUNNING,
                    error=error,
                    step_count=stats.steps,
                    tool_call_count=stats.tool_calls,
                    worker_id=self._worker_id,
                )
            await telemetry.finish_trace(job.organization_id, job.execution_id, "error")
            await telemetry.event(
                job.organization_id, job.execution_id, "execution.failed",
                {"status": status, "error": error},
            )
            self._record_metrics(status, stats)
        except Exception:  # noqa: BLE001 - finalization must never mask the cause
            logger.exception(
                "failure finalization errored", extra={"execution_id": job.execution_id}
            )

    async def _finalize_cancelled(
        self, job: Job, stats: RunStats, telemetry: ExecutionTelemetry
    ) -> None:
        try:
            async with self._db.org_tx(job.organization_id) as conn:
                state = ExecutionState(self._db.pool, job.execution_id)
                await state.transition(
                    conn,
                    CANCELLED,
                    current=RUNNING,
                    step_count=stats.steps,
                    tool_call_count=stats.tool_calls,
                    worker_id=self._worker_id,
                )
            await telemetry.finish_trace(job.organization_id, job.execution_id, "unset")
            await telemetry.event(
                job.organization_id, job.execution_id, "execution.cancelled",
                {"status": CANCELLED, "steps": stats.steps},
            )
        except Exception:  # noqa: BLE001
            logger.exception(
                "cancellation finalization errored", extra={"execution_id": job.execution_id}
            )


def build_builtin_registry() -> dict[str, Any]:
    """The builtin tool registry available to the execution plane."""
    return {"calculator": toolgw.CalculatorTool()}
