# agentos — Official Python SDK

The official Python SDK for [AgentOS](../../..), the enterprise control
plane for AI agents.

```bash
pip install agentos  # or: uv pip install -e services/sdk-python
```

## Quick start

```python
from agentos import AgentOS

client = AgentOS(api_key="YOUR_AGENTOS_API_KEY", base_url="http://localhost:8080")

execution = client.executions.create(
    agent_id="YOUR_AGENT_ID",
    input={"query": "Summarize the approved project documents."},
)
print(execution.id)

# Wait for the run to finish (polls the API).
execution = client.executions.wait(execution.id)
print(execution.status, execution.output)
```

API keys are created in the dashboard (Organization → API Keys) and carry
scopes such as `executions.submit`, `executions.read` and `agents.read`.
The key's organization is resolved server-side — the SDK addresses it as
`organizations/current`.

## Resources

| Resource      | Methods                                        |
| ------------- | ---------------------------------------------- |
| `executions`  | `create`, `get`, `list`, `cancel`, `wait`      |
| `agents`      | `list`, `get`                                  |

Errors raise typed exceptions: `AuthenticationError`, `NotFoundError`,
`RateLimitError`, all subclasses of `APIError` carrying `status_code`,
`code`, `message` and `request_id`.
