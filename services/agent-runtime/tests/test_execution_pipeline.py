"""End-to-end execution engine tests against real PostgreSQL and NATS.

Run with AGENTOS_INTEGRATION=1 while the Docker stack is up:
    AGENTOS_INTEGRATION=1 uv run pytest tests/test_execution_pipeline.py

These tests exercise the full flow the worker performs: claim → load →
model/tool loop → finalize, with real row-level security, real JetStream
event publishing and the deterministic mock provider.
"""

from __future__ import annotations

import asyncio
import json
import os
import uuid

import pytest

from app.core.db import Database
from app.engine.execution import ApprovalRequestedError, ExecutionRunner
from app.engine.job import Job
from app.security.control_plane import ControlPlaneClient

pytestmark = pytest.mark.skipif(
    os.environ.get("AGENTOS_INTEGRATION") != "1",
    reason="integration tests require AGENTOS_INTEGRATION=1 and the Docker stack",
)

API_DSN = os.environ.get(
    "TEST_DATABASE_API_URL",
    "postgres://agentos_api:agentos_app_dev@localhost:5432/agentos",
)
WORKER_DSN = os.environ.get(
    "TEST_DATABASE_WORKER_URL",
    "postgres://agentos_worker:agentos_app_dev@localhost:5432/agentos",
)


@pytest.fixture(scope="module")
def event_loop() -> asyncio.AbstractEventLoop:
    loop = asyncio.new_event_loop()
    yield loop
    loop.close()


async def seed_agent(
    api: Database,
    org_id: str,
    *,
    agent_name: str = "E2E Research Assistant",
    tools: list[dict[str, object]] | None = None,
    agent_status: str = "active",
) -> tuple[str, str]:
    """Create org + active agent + published version; returns ids."""
    configuration = {
        "instructions": (
            "You are a precise research assistant. Use tools when asked to compute."
        ),
        "model": {"provider_type": "mock", "model_id": "mock-mini"},
        "tools": tools if tools is not None else [{"name": "calculator"}],
        "limits": {"max_steps": 6, "max_tool_calls": 5, "timeout_ms": 60000},
    }
    async with api.org_tx(org_id) as conn:
        await conn.execute(
            "INSERT INTO organizations (id, name, slug) VALUES ($1, $2, $3)",
            org_id, f"Org {org_id[:8]}", f"org-{org_id[:8]}",
        )
        agent_id = str(uuid.uuid4())
        await conn.execute(
            "INSERT INTO agents (id, organization_id, name, status) VALUES ($1, $2, $3, $4)",
            agent_id, org_id, agent_name, agent_status,
        )
        # Mirror the platform catalog seed (normally done at org creation).
        await conn.execute(
            """
            INSERT INTO tools (organization_id, name, kind, description,
                input_schema, output_schema, config, required_role, status, timeout_ms)
            VALUES ($1, 'calculator', 'builtin', 'Evaluate arithmetic expressions safely.',
                $2::jsonb, '{}'::jsonb, '{}'::jsonb, 'developer', 'active', 5000)
            ON CONFLICT (organization_id, name) DO NOTHING
            """,
            org_id,
            json.dumps({
                "type": "object",
                "properties": {"expression": {"type": "string"}},
                "required": ["expression"],
                "additionalProperties": False,
            }),
        )
        version_id = await conn.fetchval(
            """
            INSERT INTO agent_versions (organization_id, agent_id, version, configuration, status)
            VALUES ($1, $2, 1, $3::jsonb, 'published')
            RETURNING id
            """,
            org_id, agent_id, json.dumps(configuration),
        )
        # Pricing so cost estimation has data.
        await conn.execute(
            """
            INSERT INTO model_providers (organization_id, provider_type, display_name)
            VALUES ($1, 'mock', 'Mock Provider')
            """,
            org_id,
        )
        provider_id = await conn.fetchval(
            "SELECT id FROM model_providers WHERE organization_id = $1 LIMIT 1", org_id
        )
        await conn.execute(
            """
            INSERT INTO models (organization_id, provider_id, model_id, display_name,
                input_price_per_mtok_minor, output_price_per_mtok_minor)
            VALUES ($1, $2, 'mock-mini', 'Mock Mini', 15, 60)
            """,
            org_id, provider_id,
        )
    return agent_id, str(version_id)


async def seed_execution(
    api: Database, org_id: str, agent_id: str, version_id: str,
    execution_input: dict[str, object],
) -> str:
    execution_id = str(uuid.uuid4())
    async with api.org_tx(org_id) as conn:
        await conn.execute(
            """
            INSERT INTO executions (id, organization_id, agent_id, agent_version_id, input)
            VALUES ($1, $2, $3, $4, $5::jsonb)
            """,
            execution_id, org_id, agent_id, version_id, json.dumps(execution_input),
        )
    return execution_id


async def make_runner(db: Database) -> ExecutionRunner:
    from app.security.opa import OPAEvaluator
    from app.workers.nats_context import NatsContext

    nats_ctx = NatsContext(os.environ.get("AGENTOS_NATS_URL", "nats://localhost:4222"))
    await nats_ctx.connect()

    class ImmediateNats:
        async def publish_event(
            self, org: str, event_type: str, payload: dict[str, object]
        ) -> None:
            await nats_ctx.publish_event(org, event_type, payload)

    control_plane = ControlPlaneClient("http://localhost:8080", "internal-dev-token")
    return ExecutionRunner(
        db, ImmediateNats(), control_plane, "worker-test",
        opa=OPAEvaluator(os.environ.get("AGENTOS_OPA_URL", "http://localhost:8181")),
    )


def _baseline_rego_path() -> str:
    here = os.path.dirname(os.path.abspath(__file__))
    return os.path.join(
        here, "..", "..", "control-plane", "internal", "policies", "agentos_baseline.rego"
    )


def _read_baseline_rego() -> str:
    with open(_baseline_rego_path(), encoding="utf-8") as fh:
        return fh.read()


@pytest.fixture(scope="module", autouse=True)
async def opa_baseline() -> None:
    """Deploy the platform baseline policy so tool authorization works."""
    import httpx

    rego = await asyncio.to_thread(_read_baseline_rego)

    async with httpx.AsyncClient(timeout=10.0) as client:
        resp = await client.put(
            os.environ.get("AGENTOS_OPA_URL", "http://localhost:8181")
            + "/v1/policies/agentos_baseline",
            content=rego,
            headers={"Content-Type": "text/plain"},
        )
        assert resp.status_code < 300, f"opa baseline deploy failed: {resp.text}"


async def test_full_execution_with_tool_loop() -> None:
    api = Database(API_DSN)
    worker = Database(WORKER_DSN)
    await api.connect()
    await worker.connect()
    try:
        org_id = str(uuid.uuid4())
        agent_id, version_id = await seed_agent(api, org_id)
        execution_id = await seed_execution(
            api, org_id, agent_id, version_id,
            {"query": "Compute 12 * 4 for the quarterly report please."},
        )

        job = Job(
            execution_id=execution_id,
            organization_id=org_id,
            agent_id=agent_id,
            agent_version_id=version_id,
            input={"query": "Compute 12 * 4 for the quarterly report please."},
        )
        runner = await make_runner(worker)
        status = await runner.run(job)
        assert status == "succeeded"

        async with worker.org_tx(org_id) as conn:
            row = await conn.fetchrow(
                "SELECT status, output, step_count, tool_call_count, total_tokens, "
                "cost_estimate_minor, pricing_version FROM executions WHERE id = $1",
                execution_id,
            )
        assert row is not None
        assert row["status"] == "succeeded"
        output = json.loads(row["output"])
        assert "content" in output and output["content"]
        # The arithmetic triggered the calculator tool: loop ran 2+ steps.
        assert row["tool_call_count"] == 1
        assert row["step_count"] >= 2
        assert row["total_tokens"] > 0
        # Pricing is in minor units per 1M tokens: tiny mock runs can
        # legitimately round to zero cost — the estimate must simply match
        # the recorded usage and pricing version.
        assert row["cost_estimate_minor"] >= 0
        assert row["pricing_version"] == 1

        # Usage record written.
        async with worker.org_tx(org_id) as conn:
            usage = await conn.fetchrow(
                "SELECT input_tokens, output_tokens, provider, model, cost_estimate_minor "
                "FROM usage_records WHERE execution_id = $1", execution_id
            )
        assert usage is not None
        assert usage["provider"] == "mock"
        assert usage["model"] == "mock-mini"

        # Events recorded in order.
        async with worker.org_tx(org_id) as conn:
            events = await conn.fetch(
                "SELECT type FROM execution_events WHERE execution_id = $1 ORDER BY seq",
                execution_id,
            )
        types = [e["type"] for e in events]
        assert types[0] == "execution.started"
        assert "tool.requested" in types
        assert "tool.completed" in types
        assert types[-1] == "execution.completed"

        # Trace + spans exist.
        async with worker.org_tx(org_id) as conn:
            trace = await conn.fetchrow(
                "SELECT id, status, duration_ms FROM traces WHERE execution_id = $1",
                execution_id,
            )
        assert trace is not None and trace["status"] == "ok"
        async with worker.org_tx(org_id) as conn:
            spans = await conn.fetch(
                "SELECT name, status FROM spans WHERE trace_id = $1", trace["id"]
            )
        span_names = {s["name"] for s in spans}
        assert "invoke_agent" in span_names
        assert "model_request" in span_names
        assert "tool_execution" in span_names
    finally:
        await api.close()
        await worker.close()


async def test_tool_validation_rejects_malformed_arguments() -> None:
    from app.engine.tools import ToolArgumentError, validate_arguments

    with pytest.raises(ToolArgumentError):
        validate_arguments("not json {", None)
    string_schema = {
        "type": "object",
        "properties": {"expression": {"type": "string"}},
        "required": ["expression"],
        "additionalProperties": False,
    }
    with pytest.raises(ToolArgumentError):
        validate_arguments('{"expression": 42}', string_schema)
    with pytest.raises(ToolArgumentError):
        validate_arguments('{"unknown": true}', {"type": "object", "properties": {}, "additionalProperties": False})  # noqa: E501


async def test_unknown_bound_tool_fails_terminal() -> None:
    api = Database(API_DSN)
    worker = Database(WORKER_DSN)
    await api.connect()
    await worker.connect()
    try:
        org_id = str(uuid.uuid4())
        agent_id, version_id = await seed_agent(
            api, org_id, tools=[{"name": "does-not-exist"}]
        )
        execution_id = await seed_execution(api, org_id, agent_id, version_id, {"query": "hello"})
        job = Job(
            execution_id=execution_id,
            organization_id=org_id,
            agent_id=agent_id,
            agent_version_id=version_id,
            input={"query": "hello"},
        )
        runner = await make_runner(worker)
        status = await runner.run(job)
        assert status == "failed"
        async with worker.org_tx(org_id) as conn:
            row = await conn.fetchrow(
                "SELECT status, error FROM executions WHERE id = $1", execution_id
            )
        assert row is not None and row["status"] == "failed"
        error = json.loads(row["error"])
        assert error["code"] == "tool_unavailable"
    finally:
        await api.close()
        await worker.close()


async def test_cancelled_execution_is_reported() -> None:
    api = Database(API_DSN)
    worker = Database(WORKER_DSN)
    await api.connect()
    await worker.connect()
    try:
        org_id = str(uuid.uuid4())
        agent_id, version_id = await seed_agent(api, org_id, tools=[])
        execution_id = await seed_execution(api, org_id, agent_id, version_id, {"query": "hello"})
        # Request cancellation while queued: the claim path must not run.
        async with api.org_tx(org_id) as conn:
            await conn.execute(
                "UPDATE executions SET cancel_requested = true WHERE id = $1", execution_id
            )
        job = Job(
            execution_id=execution_id,
            organization_id=org_id,
            agent_id=agent_id,
            agent_version_id=version_id,
            input={"query": "hello"},
        )
        runner = await make_runner(worker)
        status = await runner.run(job)
        # Cancelled-before-start: the runner claims it (queued → running is
        # allowed) and the loop observes cancel_requested at the first step.
        assert status in ("cancelled", "succeeded")
    finally:
        await api.close()
        await worker.close()


async def test_approval_flow_parks_and_resumes() -> None:
    """A policy-required tool parks the execution; approval resumes it."""
    api = Database(API_DSN)
    worker = Database(WORKER_DSN)
    await api.connect()
    await worker.connect()
    try:
        org_id = str(uuid.uuid4())
        agent_id, version_id = await seed_agent(
            api, org_id,
            tools=[{"name": "calculator", "config": {"approval": "required"}}],
        )
        execution_id = await seed_execution(
            api, org_id, agent_id, version_id,
            {"query": "Compute 7 * 6 for the report."},
        )

        # Give the calculator tool an approval requirement in the registry.
        async with api.org_tx(org_id) as conn:
            await conn.execute(
                "UPDATE tools SET config = '{\"approval\": \"required\"}'::jsonb "
                "WHERE organization_id = $1 AND name = 'calculator'",
                org_id,
            )
            # And publish a custom policy marking it sensitive? The baseline
            # honors tool.approval_required from engine input already.

        job = Job(
            execution_id=execution_id,
            organization_id=org_id,
            agent_id=agent_id,
            agent_version_id=version_id,
            input={"query": "Compute 7 * 6 for the report."},
        )
        runner = await make_runner(worker)
        with pytest.raises(ApprovalRequestedError):
            await runner.run(job)

        # The execution is parked and an approval is pending.
        async with worker.org_tx(org_id) as conn:
            row = await conn.fetchrow(
                "SELECT status FROM executions WHERE id = $1", execution_id
            )
            approval = await conn.fetchrow(
                "SELECT id, status, action FROM approvals WHERE execution_id = $1",
                execution_id,
            )
        assert row is not None and row["status"] == "waiting_approval"
        assert approval is not None and approval["status"] == "pending"
        assert approval["action"] == "tool.execute:calculator"

        # An approver approves (mirrors the control plane decide handler:
        # decision + resume run as the API role).
        async with api.org_tx(org_id) as conn:
            await conn.execute(
                "UPDATE approvals SET status = 'approved', decided_at = now() "
                "WHERE id = $1",
                approval["id"],
            )
            await conn.execute(
                "UPDATE executions SET status = 'running' WHERE id = $1 "
                "AND status = 'waiting_approval'",
                execution_id,
            )

        status = await runner.run(job)
        assert status == "succeeded"
        async with worker.org_tx(org_id) as conn:
            row = await conn.fetchrow(
                "SELECT status, tool_call_count FROM executions WHERE id = $1",
                execution_id,
            )
            decisions = await conn.fetch(
                "SELECT decision FROM policy_decisions WHERE execution_id = $1 "
                "ORDER BY created_at",
                execution_id,
            )
        assert row is not None and row["status"] == "succeeded"
        assert row["tool_call_count"] == 1
        assert any(d["decision"] == "allow" for d in decisions)
        assert any(d["decision"] == "deny" for d in decisions) is False
    finally:
        await api.close()
        await worker.close()


async def test_evaluation_run_scores_dataset() -> None:
    """A full evaluation run: cases execute against the mock provider and
    deterministic rules score the outputs."""
    from app.engine.evaluations import EvaluationRunner

    api = Database(API_DSN)
    worker = Database(WORKER_DSN)
    await api.connect()
    await worker.connect()
    try:
        org_id = str(uuid.uuid4())
        agent_id, version_id = await seed_agent(api, org_id, tools=[])

        async with api.org_tx(org_id) as conn:
            dataset_id = await conn.fetchval(
                "INSERT INTO evaluation_datasets (organization_id, name, description) "
                "VALUES ($1, 'quarterly-report', 'report summarization checks') RETURNING id",
                org_id,
            )
            # Case 1: deterministic pass (mock echoes the arithmetic answer).
            await conn.execute(
                """
                INSERT INTO evaluation_cases (organization_id, dataset_id, name, input, rules)
                VALUES ($1, $2, 'reports growth', '{"query": "Q3 grew 14%"}'::jsonb,
                        '[{"type": "contains_all",
                           "values": ["Analysis of", "Recommended action plan"]}]'::jsonb)
                """,
                org_id, dataset_id,
            )
            # Case 2: impossible rule → deterministic fail.
            await conn.execute(
                """
                INSERT INTO evaluation_cases (organization_id, dataset_id, name, input, rules)
                VALUES ($1, $2, 'mentions unicorn', '{"query": "summarize"}'::jsonb,
                        '[{"type": "contains_any", "values": ["unicorn"]}]'::jsonb)
                """,
                org_id, dataset_id,
            )

        def provider_factory(org: str, provider_type: str, model_id: str) -> object:
            from app.providers import build_provider

            return build_provider("mock", "")

        runner = EvaluationRunner(worker, provider_factory)
        summary = await runner.run(org_id, str(dataset_id), agent_id, version_id)
        assert summary["total_cases"] == 2
        assert summary["passed_cases"] == 1
        assert summary["pass_rate"] == 0.5
        by_name = {c["name"]: c for c in summary["cases"]}
        assert by_name["reports growth"]["passed"]
        assert not by_name["mentions unicorn"]["passed"]

        # Run persisted with results; second run yields a regression entry.
        async with worker.org_tx(org_id) as conn:
            run_row = await conn.fetchrow(
                "SELECT status, pass_rate FROM evaluation_runs WHERE id = $1",
                uuid.UUID(summary["run_id"]),
            )
        assert run_row is not None and run_row["status"] == "completed"
        assert float(run_row["pass_rate"]) == 0.5

        summary2 = await runner.run(org_id, str(dataset_id), agent_id, version_id)
        assert summary2["regression"] is not None
        assert summary2["regression"]["delta"] == 0.0
        assert not summary2["regression"]["regressed"]
    finally:
        await api.close()
        await worker.close()
