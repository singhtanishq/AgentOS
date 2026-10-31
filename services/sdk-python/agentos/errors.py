"""Typed API errors for the AgentOS SDK."""

from __future__ import annotations


class APIError(Exception):
    """Base error for non-2xx API responses."""

    def __init__(self, status_code: int, code: str, message: str, request_id: str = "") -> None:
        super().__init__(f"{code}: {message}")
        self.status_code = status_code
        self.code = code
        self.message = message
        self.request_id = request_id


class AuthenticationError(APIError):
    """401/403 — invalid credentials or insufficient scopes."""


class NotFoundError(APIError):
    """404 — the resource does not exist (or is not visible)."""


class RateLimitError(APIError):
    """429 — too many requests."""


def error_from_response(status_code: int, body: dict[str, object]) -> APIError:
    """Map an error envelope to a typed exception."""
    raw_error = body.get("error")
    err = raw_error if isinstance(raw_error, dict) else {}
    code = str(err.get("code", "unknown"))
    message = str(err.get("message", ""))
    request_id = str(err.get("request_id", ""))
    cls = APIError
    if status_code in (401, 403):
        cls = AuthenticationError
    elif status_code == 404:
        cls = NotFoundError
    elif status_code == 429:
        cls = RateLimitError
    return cls(status_code, code, message, request_id)
