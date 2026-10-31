"""HTTP model provider adapters.

Each adapter converts the normalized ModelRequest into the provider's wire
format and parses the response back into normalized form. Error responses
are classified into the shared ProviderError taxonomy (auth, rate_limited,
invalid_request, overloaded, timeout, network) driving retry policy.

All adapters share one httpx client with strict timeouts; usage figures are
taken from provider responses verbatim.
"""

from __future__ import annotations

import json
from typing import Any, ClassVar

import httpx

from app.providers.base import (
    Message,
    ModelRequest,
    ModelResponse,
    ProviderError,
    ToolCall,
    Usage,
)

# Provider timeouts are deliberate: fast failure beats silent hangs.
_CONNECT_TIMEOUT = 10.0
_READ_TIMEOUT = 120.0


def _classify_status(status: int, body: str) -> ProviderError:
    if status in (401, 403):
        return ProviderError("auth", f"provider rejected credentials ({status}): {body[:200]}")
    if status == 429:
        return ProviderError("rate_limited", f"provider rate limited: {body[:200]}", retryable=True)
    if status in (500, 502, 503, 504):
        return ProviderError(
            "overloaded", f"provider unavailable ({status}): {body[:200]}", retryable=True
        )
    return ProviderError("invalid_request", f"provider rejected request ({status}): {body[:200]}")


def _network_error(exc: Exception) -> ProviderError:
    if isinstance(exc, httpx.TimeoutException):
        return ProviderError("timeout", f"provider timed out: {exc}", retryable=True)
    return ProviderError("network", f"provider network failure: {exc}", retryable=True)


class OpenAICompatibleProvider:
    """OpenAI Chat Completions wire format (also serves compatible endpoints).

    ``name`` distinguishes plain OpenAI from self-hosted compatible
    endpoints; the wire protocol is identical.
    """

    name: ClassVar[str] = "openai"

    def __init__(self, api_key: str, base_url: str = "https://api.openai.com/v1") -> None:
        self._api_key = api_key
        self._base_url = base_url.rstrip("/")

    # -- wire format -------------------------------------------------
    def build_payload(self, request: ModelRequest) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "model": request.model,
            "messages": [m.as_dict() for m in request.messages],
        }
        if request.temperature is not None:
            payload["temperature"] = request.temperature
        if request.max_tokens is not None:
            payload["max_tokens"] = request.max_tokens
        if request.top_p is not None:
            payload["top_p"] = request.top_p
        if request.tools:
            payload["tools"] = list(request.tools)
        return payload

    def parse_response(self, data: dict[str, Any], request_id: str | None) -> ModelResponse:
        try:
            choice = data["choices"][0]
            message = choice.get("message", {})
            content = message.get("content") or ""
            tool_calls = tuple(
                ToolCall(
                    call_id=tc["id"],
                    name=tc["function"]["name"],
                    arguments=tc["function"].get("arguments", "{}"),
                )
                for tc in message.get("tool_calls", [])
            )
            usage = data.get("usage", {})
            return ModelResponse(
                content=content,
                finish_reason=choice.get("finish_reason", "stop"),
                usage=Usage(
                    input_tokens=int(usage.get("prompt_tokens", 0)),
                    output_tokens=int(usage.get("completion_tokens", 0)),
                ),
                model=str(data.get("model", "")),
                provider=self.name,
                tool_calls=tool_calls,
                provider_request_id=request_id,
            )
        except (KeyError, IndexError, TypeError, ValueError) as exc:
            raise ProviderError("unknown", f"malformed provider response: {exc}") from exc

    # -- execution ---------------------------------------------------
    async def complete(self, request: ModelRequest) -> ModelResponse:
        headers = {"Authorization": f"Bearer {self._api_key}"}
        try:
            async with httpx.AsyncClient(
                timeout=httpx.Timeout(_READ_TIMEOUT, connect=_CONNECT_TIMEOUT)
            ) as client:
                resp = await client.post(
                    f"{self._base_url}/chat/completions",
                    json=self.build_payload(request),
                    headers=headers,
                )
        except Exception as exc:  # noqa: BLE001 - classified below
            raise _network_error(exc) from exc
        if resp.status_code != 200:
            raise _classify_status(resp.status_code, resp.text)
        return self.parse_response(resp.json(), resp.headers.get("x-request-id"))


class AnthropicProvider:
    """Anthropic Messages API adapter."""

    name: ClassVar[str] = "anthropic"

    def __init__(self, api_key: str, base_url: str = "https://api.anthropic.com") -> None:
        self._api_key = api_key
        self._base_url = base_url.rstrip("/")

    def build_payload(self, request: ModelRequest) -> dict[str, Any]:
        system_parts = [m.content for m in request.messages if m.role == "system"]
        messages = [
            {"role": m.role if m.role != "tool" else "user", "content": m.content}
            for m in request.messages
            if m.role in ("user", "assistant", "tool")
        ]
        payload: dict[str, Any] = {
            "model": request.model,
            "messages": messages,
            "max_tokens": request.max_tokens or 1024,
        }
        if system_parts:
            payload["system"] = "\n\n".join(system_parts)
        if request.temperature is not None:
            payload["temperature"] = request.temperature
        if request.top_p is not None:
            payload["top_p"] = request.top_p
        if request.tools:
            payload["tools"] = [
                {
                    "name": t.get("function", {}).get("name") or t.get("name", ""),
                    "description": (t.get("function", {}) or {}).get("description", ""),
                    "input_schema": (t.get("function", {}) or {}).get("parameters", {}),
                }
                for t in request.tools
            ]
        return payload

    def parse_response(self, data: dict[str, Any], request_id: str | None) -> ModelResponse:
        try:
            content_parts = [c for c in data.get("content", []) if c.get("type") == "text"]
            content = "".join(c.get("text", "") for c in content_parts)
            tool_calls = tuple(
                ToolCall(
                    call_id=block["id"],
                    name=block["name"],
                    arguments=json.dumps(block.get("input", {}), sort_keys=True),
                )
                for block in data.get("content", [])
                if block.get("type") == "tool_use"
            )
            usage = data.get("usage", {})
            return ModelResponse(
                content=content,
                finish_reason=data.get("stop_reason", "stop") or "stop",
                usage=Usage(
                    input_tokens=int(usage.get("input_tokens", 0)),
                    output_tokens=int(usage.get("output_tokens", 0)),
                ),
                model=str(data.get("model", "")),
                provider=self.name,
                tool_calls=tool_calls,
                provider_request_id=request_id,
            )
        except (KeyError, TypeError, ValueError) as exc:
            raise ProviderError("unknown", f"malformed provider response: {exc}") from exc

    async def complete(self, request: ModelRequest) -> ModelResponse:
        headers = {"x-api-key": self._api_key, "anthropic-version": "2023-06-01"}
        try:
            async with httpx.AsyncClient(
                timeout=httpx.Timeout(_READ_TIMEOUT, connect=_CONNECT_TIMEOUT)
            ) as client:
                resp = await client.post(
                    f"{self._base_url}/v1/messages",
                    json=self.build_payload(request),
                    headers=headers,
                )
        except Exception as exc:  # noqa: BLE001 - classified below
            raise _network_error(exc) from exc
        if resp.status_code != 200:
            raise _classify_status(resp.status_code, resp.text)
        return self.parse_response(resp.json(), resp.headers.get("request-id"))


class GoogleProvider:
    """Google Gemini generateContent adapter."""

    name: ClassVar[str] = "google"

    DEFAULT_BASE = "https://generativelanguage.googleapis.com"

    def __init__(self, api_key: str, base_url: str = DEFAULT_BASE) -> None:
        self._api_key = api_key
        self._base_url = base_url.rstrip("/")

    def build_payload(self, request: ModelRequest) -> dict[str, Any]:
        contents = [
            {
                "role": "user" if m.role in ("user", "tool") else "model",
                "parts": [{"text": m.content}],
            }
            for m in request.messages
            if m.role in ("user", "assistant", "tool")
        ]
        payload: dict[str, Any] = {"contents": contents}
        system_parts = [m.content for m in request.messages if m.role == "system"]
        if system_parts:
            payload["systemInstruction"] = {"parts": [{"text": "\n\n".join(system_parts)}]}
        generation: dict[str, Any] = {}
        if request.temperature is not None:
            generation["temperature"] = request.temperature
        if request.max_tokens is not None:
            generation["maxOutputTokens"] = request.max_tokens
        if request.top_p is not None:
            generation["topP"] = request.top_p
        if generation:
            payload["generationConfig"] = generation
        return payload

    def parse_response(self, data: dict[str, Any], request_id: str | None) -> ModelResponse:
        try:
            candidate = data["candidates"][0]
            parts = candidate.get("content", {}).get("parts", [])
            content = "".join(p.get("text", "") for p in parts)
            metadata = data.get("usageMetadata", {})
            return ModelResponse(
                content=content,
                finish_reason=candidate.get("finishReason", "STOP").lower(),
                usage=Usage(
                    input_tokens=int(metadata.get("promptTokenCount", 0)),
                    output_tokens=int(metadata.get("candidatesTokenCount", 0)),
                ),
                model=str(data.get("modelVersion", "")),
                provider=self.name,
                provider_request_id=request_id,
            )
        except (KeyError, IndexError, TypeError, ValueError) as exc:
            raise ProviderError("unknown", f"malformed provider response: {exc}") from exc

    async def complete(self, request: ModelRequest) -> ModelResponse:
        try:
            async with httpx.AsyncClient(
                timeout=httpx.Timeout(_READ_TIMEOUT, connect=_CONNECT_TIMEOUT)
            ) as client:
                resp = await client.post(
                    f"{self._base_url}/v1beta/models/{request.model}:generateContent",
                    params={"key": self._api_key},
                    json=self.build_payload(request),
                )
        except Exception as exc:  # noqa: BLE001 - classified below
            raise _network_error(exc) from exc
        if resp.status_code != 200:
            raise _classify_status(resp.status_code, resp.text)
        return self.parse_response(resp.json(), resp.headers.get("x-request-id"))


__all__ = [
    "AnthropicProvider",
    "GoogleProvider",
    "Message",
    "OpenAICompatibleProvider",
]
