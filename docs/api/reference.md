# API Reference

Base URL: `http://localhost:8080` · All endpoints are versioned under
`/v1` and return JSON. The OpenAPI document is served by the runtime at
`http://localhost:8090/v1/openapi.json` (internal surface).

## Authentication

| Mode | Credential | Header/Cookie |
| --- | --- | --- |
| Browser session | Argon2id login | `Cookie: agentos_session=…` (HttpOnly) |
| Machine | API key | `Authorization: Bearer agos_…` |

API-key principals address their organization as `organizations/current`.
Scopes map to permissions (`executions.submit`, `executions.read`,
`agents.read`, `agents.edit`, `executions.cancel`).

Error envelope (all non-2xx):

```json
{"error": {"code": "validation_failed", "message": "…", "request_id": "…"}}
```

## Endpoints

### Authentication

| Method | Path | Permission | Notes |
| --- | --- | --- | --- |
| POST | `/v1/auth/signup` | public | Creates user (+optional first org as owner); rate limited |
| POST | `/v1/auth/login` | public | Sets session cookie; rate limited |
| POST | `/v1/auth/logout` | authenticated | Revokes the session |
| GET | `/v1/auth/session` | authenticated | User + organizations |

### Organizations & members

| Method | Path | Permission |
| --- | --- | --- |
| POST | `/v1/organizations` | authenticated |
| GET | `/v1/organizations` | authenticated |
| GET | `/v1/organizations/{orgId}` | member |
| GET | `/v1/organizations/{orgId}/members` | member |
| PATCH | `/v1/organizations/{orgId}/members/{userId}` | members.manage |
| DELETE | `/v1/organizations/{orgId}/members/{userId}` | members.manage |
| POST | `/v1/organizations/{orgId}/invitations` | members.manage |
| GET | `/v1/organizations/{orgId}/invitations` | members.manage |
| DELETE | `/v1/organizations/{orgId}/invitations/{id}` | members.manage |
| POST | `/v1/invitations/accept` | authenticated (body: `{token}`) |

### Agents & versions

| Method | Path | Permission |
| --- | --- | --- |
| GET / POST | `/v1/organizations/{orgId}/agents` | agents.read / agents.edit |
| GET / PATCH / DELETE | `/v1/organizations/{orgId}/agents/{agentId}` | read / edit |
| PUT | `/v1/organizations/{orgId}/agents/{agentId}/status` | agents.edit |
| POST | `/v1/organizations/{orgId}/agents/{agentId}/versions` | agents.edit |
| POST | `/v1/organizations/{orgId}/agents/{agentId}/versions/{version}/publish` | agents.edit |
| POST/GET | `/v1/organizations/{orgId}/agents/{agentId}/identities` | credentials.manage / read |
| POST | `…/identities/{id}/suspend` · `…/revoke` | credentials.manage |

### Deployments

| Method | Path | Permission |
| --- | --- | --- |
| POST | `/v1/organizations/{orgId}/agents/{agentId}/deploy` | deployments.manage |
| GET | `/v1/organizations/{orgId}/deployments` | agents.read |
| POST | `…/deployments/{deploymentId}/deactivate` | deployments.manage |
| POST | `…/deployments/{deploymentId}/rollback` | deployments.manage |

### Executions & observability

| Method | Path | Permission |
| --- | --- | --- |
| POST | `/v1/organizations/{orgId}/executions` | executions.submit |
| GET | `/v1/organizations/{orgId}/executions` | executions.read |
| GET | `/v1/organizations/{orgId}/executions/{id}` | executions.read |
| GET | `/v1/organizations/{orgId}/executions/{id}/events` | executions.read |
| POST | `/v1/organizations/{orgId}/executions/{id}/cancel` | executions.cancel |
| GET / GET | `/v1/organizations/{orgId}/traces[/{traceId}]` | executions.read |

### Model gateway

| Method | Path | Permission |
| --- | --- | --- |
| GET / POST | `/v1/organizations/{orgId}/model-providers` | read / credentials.manage |
| GET / POST | `…/model-providers/{id}/credentials` | read / credentials.manage |
| DELETE | `…/model-providers/{id}/credentials/{cid}` | credentials.manage |
| GET / POST | `/v1/organizations/{orgId}/models` | read / credentials.manage |

### Tools & MCP

| Method | Path | Permission |
| --- | --- | --- |
| GET | `/v1/organizations/{orgId}/tools` | agents.read |
| PATCH | `/v1/organizations/{orgId}/tools/{toolId}` | tools.manage |
| GET / POST | `/v1/organizations/{orgId}/mcp-connections` | read / tools.manage |
| PUT | `…/mcp-connections/{id}/status` | tools.manage |
| DELETE | `…/mcp-connections/{id}` | tools.manage |

### Governance

| Method | Path | Permission |
| --- | --- | --- |
| GET / POST | `/v1/organizations/{orgId}/policies` | read / policies.manage |
| DELETE | `/v1/organizations/{orgId}/policies/{id}` | policies.manage |
| GET | `/v1/organizations/{orgId}/approvals` | executions.read |
| POST | `/v1/organizations/{orgId}/approvals/{id}/decide` | approvals.decide |

### Costs, ops & platform

| Method | Path | Permission |
| --- | --- | --- |
| GET / POST | `/v1/organizations/{orgId}/budgets` | read / budgets.manage |
| PATCH / DELETE | `…/budgets/{id}` | budgets.manage |
| GET | `/v1/organizations/{orgId}/usage` | executions.read |
| GET | `/v1/organizations/{orgId}/alerts` | executions.read |
| POST | `…/alerts/{id}/ack` | executions.read |
| GET | `/v1/organizations/{orgId}/audit-logs` | audit.read |
| GET / POST | `/v1/organizations/{orgId}/api-keys` | api_keys.manage |
| DELETE | `…/api-keys/{id}` | api_keys.manage |
| GET / POST | `/v1/organizations/{orgId}/webhooks` | webhooks.manage |
| DELETE | `…/webhooks/{id}` | webhooks.manage |
| GET/POST | `/v1/organizations/{orgId}/evaluations/datasets[...]` | read / agents.edit |
| POST | `/v1/organizations/{orgId}/evaluations/datasets/{id}/runs` | agents.edit |

### Infrastructure

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/healthz` | liveness |
| GET | `/readyz` | component readiness (postgres, nats) |
| GET | `/metrics` | Prometheus scrape |
| GET | `/internal/credentials/{id}?organization_id=…` | service-token only (runtime) |
| POST | `/internal/evaluations/run` (runtime :8090) | service-token only |

## Example: submit an execution (curl)

```bash
curl -sS -X POST http://localhost:8080/v1/organizations/current/executions \
  -H "Authorization: Bearer agos_YOUR_KEY" \
  -H "Content-Type: application/json" \
  -d '{"agent_id":"…","input":{"query":"What is 12 * 4?"},"idempotency_key":"run-1"}'
```

## Example: Python SDK

```python
from agentos import AgentOS

client = AgentOS(api_key="agos_YOUR_KEY", base_url="http://localhost:8080")
execution = client.executions.create(
    agent_id="…", input={"query": "What is 12 * 4?"}
)
execution = client.executions.wait(execution.id)
print(execution.status, execution.output)
```
