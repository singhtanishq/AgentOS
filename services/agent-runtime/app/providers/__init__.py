"""Provider registry: builds provider instances from control-plane config.

Providers are constructed per execution with credentials fetched from the
control plane's internal endpoint at run time — keys never rest in the
runtime or on the message bus.
"""

from __future__ import annotations

from app.providers.adapters import (
    AnthropicProvider,
    GoogleProvider,
    OpenAICompatibleProvider,
)
from app.providers.base import Provider
from app.providers.mock import MockProvider


def build_provider(
    provider_type: str,
    api_key: str,
    base_url: str = "",
) -> Provider:
    """Instantiate the provider adapter for a configured provider type."""
    ptype = provider_type.strip().lower()
    if ptype == "mock":
        return MockProvider()
    if ptype == "openai":
        return OpenAICompatibleProvider(api_key=api_key)
    if ptype == "compatible":
        if not base_url:
            raise ValueError("compatible provider requires base_url")
        return OpenAICompatibleProvider(api_key=api_key, base_url=base_url)
    if ptype == "anthropic":
        return AnthropicProvider(api_key=api_key)
    if ptype == "google":
        return GoogleProvider(api_key=api_key)
    raise ValueError(f"unsupported provider type: {provider_type}")


__all__ = ["build_provider"]
