"""Service-to-service client for control-plane internal endpoints."""

from __future__ import annotations

from typing import Any

import httpx


class ControlPlaneClient:
    """Calls the control plane's token-protected internal surface."""

    def __init__(self, base_url: str, internal_token: str) -> None:
        self._base_url = base_url.rstrip("/")
        self._token = internal_token

    async def reveal_credential(self, credential_id: str, organization_id: str) -> dict[str, Any]:
        """Fetch the decrypted provider credential for an execution.

        The credential travels only over this authenticated, in-memory call
        at execution time — never over the message bus or into storage.
        """
        async with httpx.AsyncClient(timeout=10.0) as client:
            resp = await client.get(
                f"{self._base_url}/internal/credentials/{credential_id}",
                params={"organization_id": organization_id},
                headers={"Authorization": f"Bearer {self._token}"},
            )
        if resp.status_code != 200:
            raise RuntimeError(
                f"credential reveal failed ({resp.status_code}); "
                "verify the provider credential is active"
            )
        return dict(resp.json())

    async def healthy(self) -> bool:
        try:
            async with httpx.AsyncClient(timeout=5.0) as client:
                resp = await client.get(f"{self._base_url}/healthz")
            return resp.status_code == 200
        except Exception:  # noqa: BLE001 - readiness probe
            return False
