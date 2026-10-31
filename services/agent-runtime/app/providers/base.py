"""Provider abstraction: request/response contracts and error taxonomy.

The runtime never hardcodes providers; every model call goes through the
`Provider` protocol. Usage figures come from provider responses whenever
available — the runtime never invents token counts.
"""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any, ClassVar, Protocol


@dataclass(slots=True, frozen=True)
class Message:
    """One conversation message."""

    role: str  # system | user | assistant | tool
    content: str
    # Populated for assistant tool-call messages and tool results.
    tool_call_id: str | None = None
    tool_calls: tuple[dict[str, Any], ...] = field(default=())

    def as_dict(self) -> dict[str, Any]:
        out: dict[str, Any] = {"role": self.role, "content": self.content}
        if self.tool_call_id is not None:
            out["tool_call_id"] = self.tool_call_id
        if self.tool_calls:
            out["tool_calls"] = list(self.tool_calls)
        return out


@dataclass(slots=True, frozen=True)
class ToolCall:
    """A model-requested tool invocation."""

    call_id: str
    name: str
    # Raw JSON arguments produced by the model — ALWAYS validated against
    # the tool schema before execution (see security/tool_gateway.py).
    arguments: str


@dataclass(slots=True, frozen=True)
class Usage:
    """Token usage as reported by the provider."""

    input_tokens: int
    output_tokens: int

    @property
    def total_tokens(self) -> int:
        return self.input_tokens + self.output_tokens


@dataclass(slots=True, frozen=True)
class ModelRequest:
    """A normalized model completion request."""

    model: str
    messages: tuple[Message, ...]
    tools: tuple[dict[str, Any], ...] = ()
    temperature: float | None = None
    max_tokens: int | None = None
    top_p: float | None = None


@dataclass(slots=True, frozen=True)
class ModelResponse:
    """A normalized model completion response."""

    content: str
    finish_reason: str  # stop | tool_calls | length | error
    usage: Usage
    model: str
    provider: str
    tool_calls: tuple[ToolCall, ...] = ()
    provider_request_id: str | None = None


class ProviderError(Exception):
    """Classified provider failure used for retry and reporting decisions."""

    KINDS: ClassVar[tuple[str, ...]] = (
        "auth",
        "rate_limited",
        "invalid_request",
        "overloaded",
        "timeout",
        "network",
        "unknown",
    )

    def __init__(self, kind: str, message: str, retryable: bool = False) -> None:
        if kind not in self.KINDS:
            raise ValueError(f"unknown provider error kind: {kind}")
        super().__init__(message)
        self.kind = kind
        self.retryable = retryable


class Provider(Protocol):
    """The contract every model provider adapter implements."""

    name: ClassVar[str]

    async def complete(self, request: ModelRequest) -> ModelResponse:
        """Execute one completion request."""
        ...


def messages_from(payload: Sequence[Mapping[str, Any]]) -> tuple[Message, ...]:
    """Build Messages from API-shaped dicts (role/content/...)."""
    out = []
    for m in payload:
        out.append(
            Message(
                role=str(m.get("role", "user")),
                content=str(m.get("content", "")),
                tool_call_id=m.get("tool_call_id"),
                tool_calls=tuple(m.get("tool_calls") or ()),
            )
        )
    return tuple(out)
