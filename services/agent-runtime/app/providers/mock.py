"""Deterministic mock provider.

The mock provider lets the full platform — agent loop, tool calls, usage
metering, budget enforcement, tracing — run end to end without any paid
LLM API. It is a simulated provider: its usage figures are computed from
the conversation itself and its costs must be presented as estimates.

Behavior is deterministic and scriptable:

* If the latest user message contains arithmetic like ``12 * 4`` and a
  ``calculator`` tool is available, the model emits a calculator tool call
  (exercising the tool loop).
* If the latest user message contains ``[[call:<tool> <json>]]``, the model
  emits that tool call verbatim (used by demos and evaluations).
* Otherwise it answers with a deterministic summary derived from the input.
"""

from __future__ import annotations

import hashlib
import json
import re
from typing import ClassVar

from app.providers.base import ModelRequest, ModelResponse, ToolCall, Usage

_ARITHMETIC = re.compile(r"-?\d+(?:\.\d+)?\s*[+\-*/]\s*-?\d+(?:\.\d+)?")
_TOOL_DIRECTIVE = re.compile(r"\[\[call:([a-zA-Z0-9_.-]+)\s+(.*?)\]\]", re.DOTALL)

# Simulated tokenizer: ~4 characters per token, floored at 1.
_CHARS_PER_TOKEN = 4


def _count_tokens(text: str) -> int:
    return max(1, len(text) // _CHARS_PER_TOKEN)


class MockProvider:
    """A deterministic, offline model provider."""

    name: ClassVar[str] = "mock"

    async def complete(self, request: ModelRequest) -> ModelResponse:
        prompt_text = "\n".join(m.content for m in request.messages)
        tool_names = {
            str(t.get("function", {}).get("name") or t.get("name", ""))
            for t in request.tools
        }
        last_user = next(
            (m for m in reversed(request.messages) if m.role == "user"), None
        )
        user_text = last_user.content if last_user else ""
        # After a tool result the model answers — no repeat calls.
        saw_tool_result = any(m.role == "tool" for m in request.messages)

        # Scripted explicit tool directive.
        directive = _TOOL_DIRECTIVE.search(user_text) if not saw_tool_result else None
        if directive:
            name, raw_args = directive.group(1), directive.group(2)
            if name in tool_names or not request.tools:
                call = ToolCall(
                    call_id="call_mock_directive",
                    name=name,
                    arguments=json.dumps(json.loads(raw_args or "{}"), sort_keys=True),
                )
                return self._respond(
                    request, "", "tool_calls", (call,), prompt_text
                )

        # Implicit arithmetic → calculator tool call (once per conversation).
        arithmetic = _ARITHMETIC.search(user_text) if not saw_tool_result else None
        if arithmetic and "calculator" in tool_names:
            expression = " ".join(arithmetic.group(0).split())
            call = ToolCall(
                call_id="call_mock_calc",
                name="calculator",
                arguments=json.dumps({"expression": expression}, sort_keys=True),
            )
            return self._respond(
                request, "", "tool_calls", (call,), prompt_text
            )

        content = self._compose(request, user_text)
        return self._respond(request, content, "stop", (), prompt_text + content)

    def _compose(self, request: ModelRequest, user_text: str) -> str:
        """Produce a deterministic, useful-looking answer."""
        digest = hashlib.sha256(
            f"{request.model}:{user_text}".encode()
        ).hexdigest()[:8]
        topic = user_text.strip().splitlines()[0][:120] if user_text.strip() else "the request"
        tool_results = [
            m.content for m in request.messages if m.role == "tool"
        ]
        steps = [f"Analysis of: {topic}"]
        if tool_results:
            steps.append(
                "Tool results incorporated: " + "; ".join(tool_results[-2:])
            )
        steps.append("Key considerations identified from the available context.")
        steps.append(f"Recommended action plan (reference {digest}).")
        return "\n".join(f"{i}. {s}" for i, s in enumerate(steps, start=1))

    def _respond(
        self,
        request: ModelRequest,
        content: str,
        finish_reason: str,
        tool_calls: tuple[ToolCall, ...],
        usage_text: str,
    ) -> ModelResponse:
        return ModelResponse(
            content=content,
            finish_reason=finish_reason,
            usage=Usage(
                input_tokens=_count_tokens(
                    "\n".join(m.content for m in request.messages)
                ),
                output_tokens=_count_tokens(usage_text),
            ),
            model=request.model,
            provider=self.name,
            tool_calls=tool_calls,
        )
