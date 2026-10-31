"""The AgentOS API client."""

from __future__ import annotations

from typing import Any

import httpx

from agentos.errors import error_from_response
from agentos.resources import AgentsResource, ExecutionsResource

DEFAULT_BASE_URL = "http://localhost:8080"
DEFAULT_TIMEOUT = 30.0


class AgentOS:
    """Client for the AgentOS control plane API.

    Authenticates with an API key (`agos_...`) created in the dashboard.
    Every request carries `Authorization: Bearer <key>`; the key's
    organization and scopes are resolved server-side.
    """

    def __init__(
        self,
        api_key: str,
        base_url: str = DEFAULT_BASE_URL,
        *,
        timeout: float = DEFAULT_TIMEOUT,
        http_client: httpx.Client | None = None,
    ) -> None:
        if not api_key:
            raise ValueError("api_key is required")
        self._api_key = api_key
        self._base_url = base_url.rstrip("/")
        self._http = http_client or httpx.Client(
            base_url=self._base_url,
            timeout=timeout,
            headers={
                "Authorization": f"Bearer {api_key}",
                "User-Agent": "agentos-python-sdk/0.1.0",
            },
        )
        self.executions = ExecutionsResource(self)
        self.agents = AgentsResource(self)

    def close(self) -> None:
        """Release the underlying HTTP client."""
        self._http.close()

    def __enter__(self) -> AgentOS:
        return self

    def __exit__(self, *_exc: object) -> None:
        self.close()

    # -- low-level helpers -------------------------------------------------
    def get(self, path: str, params: dict[str, Any] | None = None) -> dict[str, Any]:
        return self._request("GET", path, params=params)

    def post(self, path: str, json: dict[str, Any] | None = None) -> dict[str, Any]:
        return self._request("POST", path, json_body=json)

    def _request(
        self,
        method: str,
        path: str,
        params: dict[str, Any] | None = None,
        json_body: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        # API-key principals address their own organization as "current".
        url = self._base_url + "/v1/organizations/current" + path
        response = self._http.request(
            method,
            url,
            params=params,
            json=json_body,
            headers={"Authorization": f"Bearer {self._api_key}"},
        )
        if 200 <= response.status_code < 300:
            if response.status_code == 204 or not response.content:
                return {}
            return dict(response.json())
        try:
            body: dict[str, Any] = response.json()
        except ValueError:
            body = {}
        raise error_from_response(response.status_code, body)
