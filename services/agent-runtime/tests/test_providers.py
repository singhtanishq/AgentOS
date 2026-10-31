"""Provider abstraction tests: deterministic mock behavior and wire-format
parsing for HTTP adapters."""

from __future__ import annotations

import json

import pytest

from app.providers.adapters import AnthropicProvider, GoogleProvider, OpenAICompatibleProvider
from app.providers.base import Message, ModelRequest, ProviderError
from app.providers.mock import MockProvider


def make_request(**overrides) -> ModelRequest:
    defaults = {
        "model": "mock-mini",
        "messages": (Message(role="system", content="You are terse."), Message(role="user",
            content="Summarize the report")),
    }
    defaults.update(overrides)
    return ModelRequest(**defaults)


class TestMockProvider:
    async def test_deterministic_answer(self) -> None:
        provider = MockProvider()
        r1 = await provider.complete(make_request())
        r2 = await provider.complete(make_request())
        assert r1.content == r2.content
        assert r1.finish_reason == "stop"
        assert r1.provider == "mock"
        assert r1.usage.input_tokens > 0
        assert r1.usage.total_tokens == r1.usage.input_tokens + r1.usage.output_tokens

    async def test_arithmetic_triggers_calculator_tool_call(self) -> None:
        provider = MockProvider()
        response = await provider.complete(
            make_request(
                tools=(
                    {"type": "function", "function": {"name": "calculator", "parameters": {}}},
                ),
                messages=(
                    Message(role="user", content="What is 12 * 4 exactly?"),
                ),
            )
        )
        assert response.finish_reason == "tool_calls"
        assert len(response.tool_calls) == 1
        call = response.tool_calls[0]
        assert call.name == "calculator"
        assert json.loads(call.arguments)["expression"] == "12 * 4"

    async def test_explicit_tool_directive(self) -> None:
        provider = MockProvider()
        response = await provider.complete(
            make_request(
                tools=({"type": "function", "function": {"name": "web.search"}},),
                messages=(Message(role="user",
                    content='[[call:web.search {"query": "agentos"}]]'),),
            )
        )
        assert response.tool_calls[0].name == "web.search"
        assert json.loads(response.tool_calls[0].arguments) == {"query": "agentos"}

    async def test_no_tool_available_means_plain_answer(self) -> None:
        provider = MockProvider()
        response = await provider.complete(
            make_request(messages=(Message(role="user", content="What is 12 * 4?"),))
        )
        assert response.finish_reason == "stop"
        assert response.tool_calls == ()


class TestOpenAIAdapter:
    def test_payload_shape(self) -> None:
        p = OpenAICompatibleProvider(api_key="sk-test")
        payload = p.build_payload(make_request(temperature=0.3, max_tokens=128))
        assert payload["model"] == "mock-mini"
        assert payload["messages"][0]["role"] == "system"
        assert payload["temperature"] == 0.3
        assert payload["max_tokens"] == 128

    def test_parse_response_with_tool_calls(self) -> None:
        p = OpenAICompatibleProvider(api_key="sk-test")
        data = {
            "model": "gpt-x",
            "choices": [
                {
                    "finish_reason": "tool_calls",
                    "message": {
                        "content": None,
                        "tool_calls": [
                            {
                                "id": "call_1",
                                "type": "function",
                                "function": {"name": "calculator",
                                    "arguments": "{\"expression\": \"1+1\"}"},
                            }
                        ],
                    },
                }
            ],
            "usage": {"prompt_tokens": 11, "completion_tokens": 7},
        }
        resp = p.parse_response(data, "req-1")
        assert resp.finish_reason == "tool_calls"
        assert resp.tool_calls[0].name == "calculator"
        assert resp.usage.input_tokens == 11
        assert resp.usage.output_tokens == 7
        assert resp.provider_request_id == "req-1"

    def test_parse_malformed_raises_classified_error(self) -> None:
        p = OpenAICompatibleProvider(api_key="sk-test")
        with pytest.raises(ProviderError) as excinfo:
            p.parse_response({"nope": True}, None)
        assert excinfo.value.kind == "unknown"


class TestAnthropicAdapter:
    def test_payload_separates_system(self) -> None:
        p = AnthropicProvider(api_key="ak-test")
        payload = p.build_payload(make_request())
        assert payload["system"] == "You are terse."
        assert all(m["role"] in ("user", "assistant") for m in payload["messages"])
        assert payload["max_tokens"] == 1024

    def test_parse_tool_use_blocks(self) -> None:
        p = AnthropicProvider(api_key="ak-test")
        data = {
            "model": "claude-x",
            "stop_reason": "tool_use",
            "content": [
                {"type": "text", "text": "Let me check."},
                {"type": "tool_use", "id": "toolu_1", "name": "calculator",
                    "input": {"expression": "2+2"}},
            ],
            "usage": {"input_tokens": 9, "output_tokens": 4},
        }
        resp = p.parse_response(data, "req-9")
        assert resp.content == "Let me check."
        assert resp.finish_reason == "tool_use"
        assert resp.tool_calls[0].arguments == json.dumps({"expression": "2+2"}, sort_keys=True)


class TestGoogleAdapter:
    def test_payload_shape(self) -> None:
        p = GoogleProvider(api_key="g-test")
        payload = p.build_payload(make_request())
        assert payload["systemInstruction"]["parts"][0]["text"] == "You are terse."
        assert payload["contents"][0]["role"] == "user"

    def test_parse_usage_metadata(self) -> None:
        p = GoogleProvider(api_key="g-test")
        data = {
            "modelVersion": "gemini-x",
            "candidates": [
                {
                    "finishReason": "STOP",
                    "content": {"parts": [{"text": "Answer"}]},
                }
            ],
            "usageMetadata": {"promptTokenCount": 5, "candidatesTokenCount": 2},
        }
        resp = p.parse_response(data, None)
        assert resp.content == "Answer"
        assert resp.usage.input_tokens == 5
        assert resp.usage.output_tokens == 2
