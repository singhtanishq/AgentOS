# Screenshot Index

Every screenshot below was captured from the **actually running AgentOS
application** (Next.js dashboard + Go control plane + Python runtime +
Docker Compose infrastructure) against the seeded demo environment
("Acme Technologies"), except the infrastructure monitoring capture,
which shows live Prometheus scraping the local stack.

Validation: `python3 scripts/validate-readme-screenshots.py` verifies
that every file under `docs/screenshots/` is referenced by the root
README and by this manifest, and that no referenced path is broken.

| # | Path | Page / Feature | Viewport | Status |
| --- | --- | --- | --- | --- |
| 1 | docs/screenshots/01-authentication/login.png | Login form (session auth) | 1440×1000 | validated |
| 2 | docs/screenshots/01-authentication/signup.png | Signup — creates user + first organization | 1440×1000 | validated |
| 3 | docs/screenshots/02-overview/main-dashboard.png | Overview: operational metrics from live API | 1440×1000 | validated |
| 4 | docs/screenshots/03-agents/agent-registry.png | Agent registry with search/filter | 1440×1000 | validated |
| 5 | docs/screenshots/03-agents/agent-builder.png | Agent builder dialog (instructions/model/limits) | 1440×1000 | validated |
| 6 | docs/screenshots/03-agents/agent-details.png | Agent detail: lifecycle actions, run submission | 1440×1000 | validated |
| 7 | docs/screenshots/03-agents/agent-configuration.png | Immutable version configuration viewer | 1440×1000 | validated |
| 8 | docs/screenshots/04-deployments/deployment-list.png | Deployment history with environments | 1440×1000 | validated |
| 9 | docs/screenshots/05-executions/execution-list.png | Execution explorer (auto-refreshing) | 1440×1000 | validated |
| 10 | docs/screenshots/05-executions/execution-details.png | Execution detail: events + span timeline | 1440×1000 | validated |
| 11 | docs/screenshots/05-executions/execution-failure.png | Failed execution with structured error | 1440×1000 | validated |
| 12 | docs/screenshots/06-observability/trace-explorer.png | Trace explorer: Gantt span timeline | 1440×1000 | validated |
| 13 | docs/screenshots/07-model-gateway/provider-registry.png | Model gateway: providers, credentials, pricing | 1440×1000 | validated |
| 14 | docs/screenshots/08-tool-registry/tool-list.png | Tool registry with schemas and configuration | 1440×1000 | validated |
| 15 | docs/screenshots/08-tool-registry/mcp-connections.png | MCP connection management | 1440×1000 | validated |
| 16 | docs/screenshots/09-identity/agent-identities.png | Agent identities: issue/suspend/revoke | 1440×1000 | validated |
| 17 | docs/screenshots/10-governance/approval-workflow.png | Approvals queue with review actions | 1440×1000 | validated |
| 18 | docs/screenshots/10-governance/policy-dashboard.png | Policy management (Rego via OPA) | 1440×1000 | validated |
| 19 | docs/screenshots/11-evaluations/evaluation-dashboard.png | Evaluation datasets, cases and runs | 1440×1000 | validated |
| 20 | docs/screenshots/12-usage-and-costs/usage-dashboard.png | Usage analytics, daily spend, budgets | 1440×1000 | validated |
| 21 | docs/screenshots/13-alerts-and-audit/alerts.png | Alerts inbox with acknowledgement | 1440×1000 | validated |
| 22 | docs/screenshots/13-alerts-and-audit/audit-logs.png | Append-only audit trail | 1440×1000 | validated |
| 23 | docs/screenshots/14-organization/members.png | Members, roles and invitations | 1440×1000 | validated |
| 24 | docs/screenshots/14-organization/organization-settings.png | Organization settings and security posture | 1440×1000 | validated |
| 25 | docs/screenshots/15-developer-platform/api-keys.png | API key management (single disclosure) | 1440×1000 | validated |
| 26 | docs/screenshots/15-developer-platform/webhooks.png | Webhook management (HMAC-signed) | 1440×1000 | validated |
| 27 | docs/screenshots/16-responsive/dashboard-mobile.png | Overview at mobile width (390×844) | 390×844 | validated |
| 28 | docs/screenshots/16-responsive/agent-details-mobile.png | Agent detail at mobile width | 390×844 | validated |
| 29 | docs/screenshots/17-system/infrastructure-monitoring.png | Prometheus scraping the live stack | 1440×1000 | validated |

Capture date: 2026-11-01 · Capture tool: browser automation against the running local stack.
