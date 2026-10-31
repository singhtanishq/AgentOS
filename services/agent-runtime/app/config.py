"""Runtime configuration loaded from the environment."""

from __future__ import annotations

from functools import lru_cache

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    """Agent runtime settings with safe local-development defaults."""

    model_config = SettingsConfigDict(
        env_prefix="AGENTOS_",
        env_file=".env",
        extra="ignore",
    )

    env: str = "development"
    # Local default binds to loopback only; containers override this to 0.0.0.0.
    http_addr: str = "127.0.0.1"
    http_port: int = 8090
    log_level: str = "INFO"

    # Infrastructure endpoints (execution plane dependencies).
    control_plane_url: str = "http://localhost:8080"
    nats_url: str = "nats://localhost:4222"
    opa_url: str = "http://localhost:8181"
    database_url: str = "postgres://agentos:agentos_dev@localhost:5432/agentos"


@lru_cache
def get_settings() -> Settings:
    """Return the cached process settings."""
    return Settings()
