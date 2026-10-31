"""MCP client integration using the official Model Context Protocol SDK.

Tools bound to an MCP connection are invoked through this adapter. The
server URL is validated with the same SSRF policy as the HTTP tool
(private/reserved addresses blocked), external tool descriptions are
treated as untrusted input, and every invocation is audited upstream by
the execution engine.

A session is opened per invocation: simple, correct, and adequate for the
local control-plane scope. Long-lived session pooling is a documented
future optimization.
"""

from __future__ import annotations

import json
import logging
from typing import Any

from app.engine.builtins import ToolSpec, validate_public_url
from app.engine.tools import ToolResult

logger = logging.getLogger(__name__)


class MCPError(Exception):
    """An MCP transport or protocol failure."""


class MCPTool:
    """Executes one remote tool on an MCP server."""

    name: str = ""  # bound registry name; set at construction

    def __init__(self, spec: ToolSpec, connection: dict[str, Any]) -> None:
        self.name = spec.name
        self._spec = spec
        self._connection = connection
        self._remote_tool = str(spec.config.get("remote_tool", spec.name))
        self._arguments: dict[str, Any] = {}

    async def execute(self, arguments: dict[str, Any]) -> ToolResult:
        self._arguments = arguments
        server_url = str(self._connection.get("url", ""))
        try:
            validate_public_url(server_url, [])
        except Exception as exc:  # noqa: BLE001 - SSRF violations are tool results
            return ToolResult(False, {"error": f"MCP server url rejected: {exc}"}, "ssrf_blocked")

        headers = self._connection.get("headers") or {}
        try:
            return await self._invoke(server_url, headers)
        except MCPError as exc:
            return ToolResult(False, {"error": str(exc)}, "mcp_error")
        except Exception as exc:  # noqa: BLE001 - tool failures are results
            return ToolResult(False, {"error": f"MCP call failed: {exc}"}, "mcp_error")

    async def _invoke(self, server_url: str, headers: dict[str, Any]) -> ToolResult:
        from mcp import ClientSession
        from mcp.client.streamable_http import streamable_http_client

        # The engine enforces the per-tool timeout around execute(); the
        # transport itself relies on httpx defaults.
        async with streamable_http_client(server_url) as streams:
            read, write = streams[0], streams[1]
            async with ClientSession(read, write) as session:
                await session.initialize()
                result = await session.call_tool(self._remote_tool, self._arguments)
        text_parts: list[str] = []
        for block in result.content:
            text = getattr(block, "text", None)
            if text is not None:
                text_parts.append(text)
        payload: dict[str, Any] = {
            "is_error": bool(result.is_error),
            "content": "\n".join(text_parts),
            "server": server_url,
            "tool": self._remote_tool,
        }
        if result.is_error:
            return ToolResult(False, payload, "mcp_tool_error")
        return ToolResult(True, payload)


async def describe_server(url: str, headers: dict[str, Any] | None = None) -> list[dict[str, Any]]:
    """List the tools an MCP server exposes (for the registry UI and
    connection health checks). The URL is SSRF-validated first."""
    validate_public_url(url, [])
    from mcp import ClientSession
    from mcp.client.streamable_http import streamable_http_client

    async with streamable_http_client(url) as streams:
        read, write = streams[0], streams[1]
        async with ClientSession(read, write) as session:
            await session.initialize()
            listing = await session.list_tools()
    return [
        {
            "name": tool.name,
            "description": tool.description or "",
            "input_schema": json.loads(json.dumps(tool.input_schema)),
        }
        for tool in listing.tools
    ]
