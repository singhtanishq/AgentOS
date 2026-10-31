"""Tests for the runtime API application."""

from __future__ import annotations

import pytest
from fastapi.testclient import TestClient

from app.main import create_app


@pytest.fixture
def client() -> TestClient:
    return TestClient(create_app())


def test_liveness(client: TestClient) -> None:
    response = client.get("/healthz")
    assert response.status_code == 200
    assert response.json() == {"status": "alive"}


def test_readiness_empty_is_ready(client: TestClient) -> None:
    response = client.get("/readyz")
    assert response.status_code == 200
    body = response.json()
    assert body["status"] == "ready"
    assert body["components"] == {}


def test_readiness_reports_failures(client: TestClient) -> None:
    app = create_app()

    async def broken() -> None:
        msg = "connection refused"
        raise ConnectionError(msg)

    app.state.readiness_checks["nats"] = broken
    client = TestClient(app)
    response = client.get("/readyz")
    assert response.status_code == 503
    body = response.json()
    assert body["status"] == "degraded"
    assert "nats" in body["components"]


def test_runtime_info(client: TestClient) -> None:
    response = client.get("/v1/runtime")
    assert response.status_code == 200
    body = response.json()
    assert body["name"] == "agentos-runtime"
    assert body["version"]


def test_security_headers(client: TestClient) -> None:
    response = client.get("/healthz")
    assert response.headers.get("X-Response-Time-Ms") is not None
