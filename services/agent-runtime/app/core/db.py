"""Database access helpers for the execution plane.

Workers connect with the narrow `agentos_worker` role and establish the
tenant context (agentos.org_id) for every transaction, exactly like the
control plane — row-level security applies to them too.
"""

from __future__ import annotations

from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from typing import Any

import asyncpg


class Database:
    """asyncpg pool with tenant-context helpers."""

    def __init__(self, dsn: str, min_size: int = 1, max_size: int = 6) -> None:
        self._dsn = dsn
        self._min = min_size
        self._max = max_size
        self._pool: asyncpg.Pool | None = None

    async def connect(self) -> None:
        self._pool = await asyncpg.create_pool(
            self._dsn, min_size=self._min, max_size=self._max, command_timeout=30
        )

    async def close(self) -> None:
        if self._pool is not None:
            await self._pool.close()
            self._pool = None

    @property
    def pool(self) -> asyncpg.Pool:
        if self._pool is None:
            raise RuntimeError("database pool is not connected")
        return self._pool

    @asynccontextmanager
    async def org_tx(self, organization_id: str) -> AsyncIterator[asyncpg.Connection]:
        """A transaction bound to one organization's RLS context."""
        async with self.pool.acquire() as conn:
            async with conn.transaction():
                await conn.execute(
                    "SELECT set_config('agentos.org_id', $1, true)", organization_id
                )
                yield conn

    async def healthy(self) -> bool:
        try:
            return await self.pool.fetchval("SELECT true") is True
        except Exception:  # noqa: BLE001 - readiness probe reports all failures
            return False


def record_to_dict(row: asyncpg.Record) -> dict[str, Any]:
    return dict(row)
