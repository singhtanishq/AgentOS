# Security Policy

## Reporting a vulnerability

Do **not** open a public GitHub issue for security vulnerabilities.

Report security issues by opening a private GitHub security advisory on this
repository (Security → Report a vulnerability), or contact the maintainers
directly through GitHub.

Please include:

- A description of the vulnerability and its impact.
- Steps to reproduce or a proof of concept.
- Affected components (control plane, agent runtime, dashboard, infrastructure).

You can expect an initial response within a reasonable timeframe for a
community-maintained project. Please allow maintainers time to produce and
release a fix before any public disclosure.

## Supported scope

AgentOS is designed for **local and private enterprise deployment**. The
current development focus includes, but is not limited to:

- Multi-tenant isolation (organization-scoped data access)
- Authentication and session security (Argon2id, HTTP-only cookies)
- Agent identity management and credential revocation
- Provider credential encryption and secret redaction
- SSRF protections for network-capable tools
- Audit logging of security-relevant events

## Out of scope

- Vulnerabilities in third-party dependencies should be reported upstream;
  we track them via automated dependency scanning.
- Reports about the demo/seed data being fictional — it is, by design.

## Security posture statement

AgentOS implements defense-in-depth controls, but no claim of compliance with
SOC 2, ISO 27001, HIPAA or any other certification framework is made or
implied. See `docs/security/threat-model.md` (added during the security
milestone) for the documented threat model.
