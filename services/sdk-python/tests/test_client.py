"""SDK tests with a mocked HTTP transport."""

from __future__ import annotations

import json

import httpx
import pytest

from agentos import AgentOS, APIError, AuthenticationError, Execution


def make_client(handler: httpx.Callback) -> AgentOS:
    transport = httpx.MockTransport(handler)
    http = httpx.Client(transport=transport)
    return AgentOS(api_key="agos_test_key", base_url="http://api.test", http_client=http)


def test_create_execution_round_trip() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.method == "POST"
        assert request.url.path == "/v1/organizations/current/executions"
        auth_header = request.headers["Authorization"]
        assert auth_header == "Bearer agos_test_key"
        body = json.loads(request.content)
        assert body["agent_id"] == "agent-1"
        assert body["input"] == {"query": "hello"}
        return httpx.Response(201, json={
            "id": "exec-1", "organization_id": "org-1", "agent_id": "agent-1",
            "agent_version_id": "ver-1", "status": "queued", "input": body["input"],
        })

    client = make_client(handler)
    execution = client.executions.create("agent-1", {"query": "hello"})
    assert isinstance(execution, Execution)
    assert execution.id == "exec-1"
    assert execution.status == "queued"
    assert not execution.is_terminal


def test_error_envelope_maps_to_typed_error() -> None:
    def handler(_request: httpx.Request) -> httpx.Response:
        return httpx.Response(403, json={
            "error": {
                "code": "forbidden",
                "message": "API key scopes do not grant executions.submit",
                "request_id": "req-9",
            }
        })

    client = make_client(handler)
    with pytest.raises(AuthenticationError) as excinfo:
        client.executions.create("agent-1", {})
    assert excinfo.value.code == "forbidden"
    assert excinfo.value.request_id == "req-9"


def test_rate_limit_maps() -> None:
    def handler(_request: httpx.Request) -> httpx.Response:
        return httpx.Response(429, json={"error": {"code": "rate_limited", "message": "slow down"}})

    client = make_client(handler)
    with pytest.raises(APIError) as excinfo:
        client.executions.list()
    assert excinfo.value.status_code == 429


def test_list_and_get() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/v1/organizations/current/executions":
            return httpx.Response(200, json={"items": [
                {"id": "exec-1", "status": "succeeded", "total_tokens": 42}
            ]})
        if request.url.path == "/v1/organizations/current/executions/exec-1":
            return httpx.Response(200, json={
                "id": "exec-1", "status": "succeeded", "output": {"content": "done"},
                "total_tokens": 42,
            })
        return httpx.Response(404, json={"error": {"code": "not_found", "message": "?"}})

    client = make_client(handler)
    items = client.executions.list(status="succeeded")
    assert len(items) == 1 and items[0].is_terminal
    detail = client.executions.get("exec-1")
    assert detail.output == {"content": "done"}


def test_wait_polls_until_terminal() -> None:
    calls = {"n": 0}

    def handler(request: httpx.Request) -> httpx.Response:
        calls["n"] += 1
        status = "running" if calls["n"] < 3 else "succeeded"
        return httpx.Response(200, json={"id": "exec-1", "status": status})

    client = make_client(handler)
    execution = client.executions.wait("exec-1", timeout_seconds=5, poll_interval=0.01)
    assert execution.status == "succeeded"
    assert calls["n"] >= 3


def test_missing_api_key_rejected() -> None:
    with pytest.raises(ValueError):
        AgentOS(api_key="")
