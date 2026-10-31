"""Builtin tool tests with emphasis on SSRF protections."""

from __future__ import annotations

import json
from typing import Any

import pytest

from app.engine.builtins import (
    DatabaseQueryTool,
    DocumentReadTool,
    EmailDraftTool,
    SSRFError,
    ToolSpec,
    WebSearchTool,
    validate_public_url,
)
from app.engine.tools import CalculatorTool, validate_arguments


def spec(name: str, config: dict[str, Any] | None = None) -> ToolSpec:
    return ToolSpec(name=name, kind="builtin", config=config or {}, timeout_seconds=10.0)


class TestSSRFValidation:
    def test_blocks_loopback(self) -> None:
        with pytest.raises(SSRFError):
            validate_public_url("http://127.0.0.1/secret", [])

    def test_blocks_private_ranges(self) -> None:
        for url in (
            "http://10.0.0.1/x",
            "http://192.168.1.1/x",
            "http://172.16.0.9/x",
            "http://169.254.169.254/latest/meta-data",
            "http://[::1]/x",
            "http://localhost/x",
        ):
            with pytest.raises(SSRFError):
                validate_public_url(url, [])

    def test_blocks_non_http_schemes(self) -> None:
        with pytest.raises(SSRFError):
            validate_public_url("file:///etc/passwd", [])
        with pytest.raises(SSRFError):
            validate_public_url("ftp://example.com/x", [])

    def test_allowlist_enforced(self) -> None:
        # docs.example.com resolves publicly in this environment only if DNS
        # works; allowlist rejection happens before resolution for unknown
        # hosts, so assert the ordering with a definitely-non-allowed host.
        with pytest.raises(SSRFError):
            validate_public_url("https://evil.example.com/x", ["allowed.example.com"])

    def test_missing_host_rejected(self) -> None:
        with pytest.raises(SSRFError):
            validate_public_url("https:///path", [])


class TestTools:
    async def test_web_search_matches_and_ranks(self) -> None:
        tool = WebSearchTool(spec("web.search"))
        result = await tool.execute({"query": "tool gateway validation"})
        assert result.ok
        assert result.output["source"] == "demo_fixtures"
        assert any("Tool gateway" in r["title"] for r in result.output["results"])

    async def test_document_read_unknown(self) -> None:
        tool = DocumentReadTool(spec("document.read"))
        result = await tool.execute({"document_id": "nope"})
        assert not result.ok
        known = await tool.execute({"document_id": "doc-handbook"})
        assert known.ok and known.output["title"]

    async def test_database_query_filters(self) -> None:
        tool = DatabaseQueryTool(spec("database.query"))
        result = await tool.execute({"name": "aurora"})
        assert result.ok
        assert result.output["records"][0]["name"] == "Aurora Labs"
        all_rows = await tool.execute({"limit": 2})
        assert len(all_rows.output["records"]) == 2

    async def test_email_draft_mock_never_sends(self) -> None:
        tool = EmailDraftTool(spec("email.draft"))
        result = await tool.execute({
            "to": "jordan.reyes@auroralabs.test",
            "subject": "Q3 follow-up",
            "body": "Hello Jordan,",
        })
        assert result.ok
        assert "no email was sent" in result.output["status"]
        bad = await tool.execute({"to": "not-an-email", "subject": "s", "body": "b"})
        assert not result.ok or bad.error_code == "invalid_recipient"

    async def test_calculator_safe_evaluation(self) -> None:
        tool = CalculatorTool()
        ok = await tool.execute({"expression": "12 * 4 + 2"})
        assert ok.ok and ok.output["result"] == 50
        for evil in (
            "__import__('os').system('id')",
            "(lambda: 1)()",
            "2 ** 100000",
            "().__class__",
        ):
            result = await tool.execute({"expression": evil})
            assert not result.ok, evil

    def test_argument_validator_enforces_schema(self) -> None:
        schema = {
            "type": "object",
            "properties": {"expression": {"type": "string"}},
            "required": ["expression"],
            "additionalProperties": False,
        }
        assert validate_arguments('{"expression": "1+1"}', schema) == {"expression": "1+1"}
        from app.engine.tools import ToolArgumentError

        with pytest.raises(ToolArgumentError):
            validate_arguments('{"expression": 1}', schema)
        with pytest.raises(ToolArgumentError):
            validate_arguments("[]", schema)
        with pytest.raises(ToolArgumentError):
            validate_arguments("x" * 9000, schema)

    def test_tool_spec_json_roundtrip(self) -> None:
        tool_spec = spec("web.search", {"mode": "demo"})
        assert json.loads(json.dumps(tool_spec.config)) == {"mode": "demo"}
