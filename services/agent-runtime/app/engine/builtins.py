"""Builtin tool implementations for the execution plane.

Every tool validates its arguments against the registry schema (enforced
by the gateway in engine/tools.py), enforces a timeout, and produces
structured output. Network-capable tools apply SSRF protections:
allowlisted hosts only, private and reserved address ranges blocked,
redirects revalidated, response size capped, strict timeouts.

Demo-backed tools (web.search, database.query, crm.lookup, document.read,
email.draft) are deterministic fixtures — documented as demo adapters, not
real integrations.
"""

from __future__ import annotations

import hashlib
import ipaddress
import json
import re
import socket
from dataclasses import dataclass
from typing import Any
from urllib.parse import urlparse

import httpx

from app.engine.tools import ToolResult


@dataclass(slots=True)
class ToolSpec:
    """Registry metadata for one tool instance."""

    name: str
    kind: str
    config: dict[str, Any]
    timeout_seconds: float


class BuiltinTool:
    """Base class: subclasses implement execute()."""

    name: ClassVar[str] = ""

    def __init__(self, spec: ToolSpec | None = None) -> None:
        self._spec = spec

    async def execute(self, arguments: dict[str, Any]) -> ToolResult:  # pragma: no cover
        raise NotImplementedError


from typing import ClassVar  # noqa: E402

# ------------------------------------------------------------------
# web.search — deterministic demo fixtures
# ------------------------------------------------------------------

_DEMO_SOURCES = [
    (
        "AgentOS documentation — Architecture overview",
        "https://docs.agentos.local/architecture",
        "The control plane coordinates registry, governance and budgets; "
        "the execution plane runs agent workloads.",
    ),
    (
        "AgentOS documentation — Tool gateway",
        "https://docs.agentos.local/tools",
        "Every tool call is validated against its schema, authorized by "
        "policy, audited and redacted.",
    ),
    (
        "AgentOS documentation — Execution observability",
        "https://docs.agentos.local/observability",
        "Executions emit structured events, spans and usage records for "
        "the trace explorer.",
    ),
    (
        "AI agent safety primer",
        "https://docs.agentos.local/safety",
        "Treat model-generated arguments as untrusted input; validate "
        "before executing.",
    ),
]


class WebSearchTool(BuiltinTool):
    """Deterministic demo search over local fixtures (mode: "demo")."""

    name = "web.search"

    def __init__(self, spec: ToolSpec) -> None:
        self._spec = spec

    async def execute(self, arguments: dict[str, Any]) -> ToolResult:
        query = str(arguments.get("query", "")).strip()
        if not query:
            return ToolResult(False, {"error": "query is required"}, "invalid_arguments")
        terms = set(query.lower().split())
        scored = []
        for title, url, snippet in _DEMO_SOURCES:
            haystack = f"{title} {snippet}".lower()
            score = len(terms & set(haystack.split()))
            if score > 0:
                scored.append((score, title, url, snippet))
        scored.sort(reverse=True)
        if not scored:
            digest = hashlib.sha256(query.encode()).hexdigest()
            fallback = _DEMO_SOURCES[int(digest[0], 16) % len(_DEMO_SOURCES)]
            scored = [(0, fallback[0], fallback[1], fallback[2])]
        results = [
            {"title": t, "url": u, "snippet": s}
            for _, t, u, s in scored[:3]
        ]
        return ToolResult(True, {"query": query, "results": results, "source": "demo_fixtures"})


# ------------------------------------------------------------------
# document.read — permitted sample documents
# ------------------------------------------------------------------

_DEMO_DOCUMENTS = {
    "doc-handbook": {
        "title": "Employee Handbook (excerpt)",
        "content": (
            "Remote work: teams may work remotely up to three days per week. "
            "Equipment requests go through the IT portal. Expenses are reimbursed "
            "monthly with receipts submitted within 30 days."
        ),
        "classification": "internal",
    },
    "doc-quarterly": {
        "title": "Q3 Quarterly Report (demo)",
        "content": (
            "Revenue grew 14% quarter over quarter. The platform team shipped "
            "the agent registry and execution runtime. Churn held steady at 2.1%."
        ),
        "classification": "internal",
    },
    "doc-policy-refs": {
        "title": "Research Reference Pack",
        "content": (
            "Approved sources for research tasks include the internal knowledge "
            "base, vendor documentation portals and published standards bodies."
        ),
        "classification": "internal",
    },
}


class DocumentReadTool(BuiltinTool):
    """Reads a sample document by id (demo fixture set)."""

    name = "document.read"

    def __init__(self, spec: ToolSpec) -> None:
        self._spec = spec

    async def execute(self, arguments: dict[str, Any]) -> ToolResult:
        document_id = str(arguments.get("document_id", ""))
        doc = _DEMO_DOCUMENTS.get(document_id)
        if doc is None:
            return ToolResult(
                False,
                {"error": f"document {document_id!r} not found in permitted set"},
                "not_found",
            )
        return ToolResult(True, {"document_id": document_id, **doc})


# ------------------------------------------------------------------
# database.query — read-only demo dataset
# ------------------------------------------------------------------

_DEMO_CUSTOMERS = [
    {"id": "C-1001", "name": "Aurora Labs", "plan": "enterprise", "seats": 240, "health": "healthy"},  # noqa: E501
    {"id": "C-1002", "name": "Bluepeak Systems", "plan": "growth", "seats": 64, "health": "healthy"},  # noqa: E501
    {"id": "C-1003", "name": "Cinder Fox Media", "plan": "starter", "seats": 8, "health": "at_risk"},  # noqa: E501
    {"id": "C-1004", "name": "Duneline Robotics", "plan": "growth", "seats": 45, "health": "healthy"},  # noqa: E501
    {"id": "C-1005", "name": "Emberly Analytics", "plan": "enterprise", "seats": 310, "health": "healthy"},  # noqa: E501
]


class DatabaseQueryTool(BuiltinTool):
    """Read-only lookup over the demo customer dataset."""

    name = "database.query"

    def __init__(self, spec: ToolSpec) -> None:
        self._spec = spec

    async def execute(self, arguments: dict[str, Any]) -> ToolResult:
        name = str(arguments.get("name", "")).strip().lower()
        limit_arg = arguments.get("limit", 10)
        limit = limit_arg if isinstance(limit_arg, int) and 1 <= limit_arg <= 50 else 10
        records = [c for c in _DEMO_CUSTOMERS if name in str(c["name"]).lower()]
        return ToolResult(
            True, {"records": records[:limit], "dataset": "demo_customers", "readonly": True}
        )


# ------------------------------------------------------------------
# crm.lookup — demo CRM
# ------------------------------------------------------------------

_DEMO_CRM = {
    "jordan.reyes@auroralabs.test": {
        "name": "Jordan Reyes", "company": "Aurora Labs", "role": "VP Engineering",
        "tier": "enterprise", "owner": "sales-west",
    },
    "sam.oakley@bluepeak.test": {
        "name": "Sam Oakley", "company": "Bluepeak Systems", "role": "Platform Lead",
        "tier": "growth", "owner": "sales-east",
    },
    "priya.nair@cinderfox.test": {
        "name": "Priya Nair", "company": "Cinder Fox Media", "role": "CTO",
        "tier": "starter", "owner": "sales-east",
    },
}


class CrmLookupTool(BuiltinTool):
    """Looks up a contact in the demo CRM by email."""

    name = "crm.lookup"

    def __init__(self, spec: ToolSpec) -> None:
        self._spec = spec

    async def execute(self, arguments: dict[str, Any]) -> ToolResult:
        email = str(arguments.get("email", "")).strip().lower()
        contact = _DEMO_CRM.get(email)
        if contact is None:
            return ToolResult(True, {"found": False, "email": email, "dataset": "demo_crm"})
        return ToolResult(True, {"found": True, "email": email, **contact, "dataset": "demo_crm"})


# ------------------------------------------------------------------
# email.draft — mock adapter
# ------------------------------------------------------------------

_DRAFT_ID = re.compile(r"^[\w.+-]+@[\w.-]+\.[a-z]{2,}$", re.IGNORECASE)


class EmailDraftTool(BuiltinTool):
    """Creates an email draft. Mock adapter: nothing is ever sent."""

    name = "email.draft"

    def __init__(self, spec: ToolSpec) -> None:
        self._spec = spec

    async def execute(self, arguments: dict[str, Any]) -> ToolResult:
        to = str(arguments.get("to", "")).strip()
        subject = str(arguments.get("subject", "")).strip()
        body = str(arguments.get("body", "")).strip()

        if not _DRAFT_ID.match(to):
            return ToolResult(
                False, {"error": "a valid recipient address is required"}, "invalid_recipient"
            )
        if not subject or not body:
            return ToolResult(False, {"error": "subject and body are required"}, "missing_content")
        if len(body) > 20_000:
            return ToolResult(False, {"error": "body exceeds the length limit"}, "too_long")
        draft_id = "draft-" + hashlib.sha256(f"{to}:{subject}:{body}".encode()).hexdigest()[:12]
        return ToolResult(True, {
            "draft_id": draft_id,
            "status": "drafted (mock adapter — no email was sent)",
            "to": to,
            "subject": subject,
            "mode": "mock",
        })


# ------------------------------------------------------------------
# http.request — controlled outbound fetch with SSRF protections
# ------------------------------------------------------------------

class SSRFError(Exception):
    """The requested URL violates the outbound policy."""


def validate_public_url(
    raw_url: str,
    allowed_hosts: list[str],
    *,
    allowed_schemes: tuple[str, ...] = ("https", "http"),
) -> str:
    """Validate a URL against the outbound policy and resolve its host.

    Blocks: non-http(s) schemes, hosts outside the allowlist, and any
    resolved address that is private, reserved, loopback or link-local —
    the classic SSRF defense. Callers must revalidate after redirects.
    """
    parsed = urlparse(raw_url)
    if parsed.scheme not in allowed_schemes:
        raise SSRFError(f"scheme {parsed.scheme!r} is not permitted")
    host = (parsed.hostname or "").lower()
    if not host:
        raise SSRFError("a host is required")
    if allowed_hosts and host not in [h.lower() for h in allowed_hosts]:
        raise SSRFError(f"host {host!r} is not on the allowlist")
    port = parsed.port or (443 if parsed.scheme == "https" else 80)
    try:
        infos = socket.getaddrinfo(host, port, type=socket.SOCK_STREAM)
    except socket.gaierror as exc:
        raise SSRFError(f"could not resolve host: {host}") from exc
    for info in infos:
        addr = ipaddress.ip_address(info[4][0])
        if (
            addr.is_private
            or addr.is_loopback
            or addr.is_reserved
            or addr.is_link_local
            or addr.is_multicast
            or addr.is_unspecified
        ):
            raise SSRFError(f"address {addr} is not a permitted destination")
    return raw_url


class HttpRequestTool(BuiltinTool):
    """GET-only fetch restricted to the configured host allowlist."""

    name = "http.request"

    def __init__(self, spec: ToolSpec) -> None:
        self._spec = spec
        cfg = spec.config
        self._allowed_hosts = [h for h in cfg.get("allowed_hosts", []) if isinstance(h, str)]
        self._max_bytes = int(cfg.get("max_response_bytes", 65536))

    async def execute(self, arguments: dict[str, Any]) -> ToolResult:
        url = str(arguments.get("url", "")).strip()
        try:
            current = validate_public_url(url, self._allowed_hosts)
        except SSRFError as exc:
            return ToolResult(False, {"error": str(exc)}, "ssrf_blocked")

        transport = httpx.AsyncHTTPTransport(retries=0)
        request_timeout = (
            self._spec.timeout_seconds if self._spec is not None else 10.0
        )
        async with httpx.AsyncClient(
            timeout=request_timeout,
            transport=transport,
            follow_redirects=False,  # redirects are revalidated manually
        ) as client:
            for _hop in range(3):  # bounded redirect chain
                try:
                    resp = await client.get(current)
                except httpx.TimeoutException:
                    return ToolResult(False, {"error": "request timed out"}, "timeout")
                except Exception as exc:  # noqa: BLE001 - reported as tool result
                    return ToolResult(False, {"error": f"request failed: {exc}"}, "network_error")
                if resp.is_redirect:
                    location = resp.headers.get("location", "")
                    try:
                        current = validate_public_url(
                            str(resp.next_request.url) if resp.next_request else location,
                            self._allowed_hosts,
                        )
                    except SSRFError as exc:
                        return ToolResult(
                            False, {"error": f"redirect blocked: {exc}"}, "ssrf_blocked"
                        )
                    continue
                break

        body = resp.content[: self._max_bytes]
        content_type = resp.headers.get("content-type", "")
        if "application/json" in content_type:
            try:
                parsed_body: Any = json.loads(body)
            except json.JSONDecodeError:
                parsed_body = body.decode(errors="replace")
        else:
            parsed_body = body.decode(errors="replace")
        return ToolResult(True, {
            "status": resp.status_code,
            "content_type": content_type,
            "truncated": len(resp.content) > self._max_bytes,
            "body": parsed_body,
        })


def build_registry(specs: list[ToolSpec]) -> dict[str, Any]:
    """Construct executors for the given specs (unknown kinds are skipped)."""
    builders: dict[str, type[BuiltinTool]] = {
        "web.search": WebSearchTool,
        "document.read": DocumentReadTool,
        "database.query": DatabaseQueryTool,
        "crm.lookup": CrmLookupTool,
        "email.draft": EmailDraftTool,
        "http.request": HttpRequestTool,
    }
    registry: dict[str, Any] = {}
    for spec in specs:
        if spec.name == "calculator":
            from app.engine.tools import CalculatorTool

            registry["calculator"] = CalculatorTool()
            continue
        builder = builders.get(spec.name)
        if builder is not None:
            registry[spec.name] = builder(spec)
    return registry
