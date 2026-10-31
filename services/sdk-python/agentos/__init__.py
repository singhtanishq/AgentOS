"""Official Python SDK for AgentOS.

Example:

    from agentos import AgentOS

    client = AgentOS(api_key="YOUR_AGENTOS_API_KEY", base_url="http://localhost:8080")

    execution = client.executions.create(
        agent_id="YOUR_AGENT_ID",
        input={"query": "Summarize the approved project documents."},
    )
    print(execution.id)
"""

from agentos.client import AgentOS
from agentos.errors import APIError, AuthenticationError, NotFoundError, RateLimitError
from agentos.models import Agent, Execution

__all__ = [
    "APIError",
    "Agent",
    "AgentOS",
    "AuthenticationError",
    "Execution",
    "NotFoundError",
    "RateLimitError",
]

__version__ = "0.1.0"
