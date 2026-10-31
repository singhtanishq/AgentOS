# Contributing to AgentOS

Thank you for considering a contribution. This document describes the
engineering standards every change must meet.

## Development setup

1. Install prerequisites: Docker, Go 1.27+, Node 22+ with pnpm 10, Python 3.13+ with [uv](https://docs.astral.sh/uv/).
2. Run `./scripts/setup.sh` to verify your environment.
3. Run `make test` and `make lint` — both must pass on your machine before you start.

## Engineering standards

- **Correctness first.** Every feature ships with meaningful tests (unit and,
  where applicable, integration).
- **Type safety.** TypeScript strict mode; Python passes strict mypy; Go is
  vetted and race-detector tested.
- **Security.** Never commit credentials or environment files. Validate all
  external input. Enforce tenant isolation in every data path.
- **No dead ends.** Every UI control does something; no fake data presented
  as live metrics.
- **Honesty.** Mock integrations are documented as mocks. Do not claim
  behavior you have not tested.

## Commit discipline

This project uses [Conventional Commits](https://www.conventionalcommits.org/):

```text
feat(agents): implement agent registry
fix(auth): renew sessions before expiry
test(security): add cross-tenant isolation tests
docs(readme): document system architecture
```

Allowed types: `feat`, `fix`, `refactor`, `perf`, `test`, `docs`, `build`,
`ci`, `chore`, `security`. Vague messages ("update", "changes", "final") are
rejected during review.

Commit meaningful, independently testable units of work and push them
incrementally to `main`. Do not accumulate large unreviewable changes.

## Pull requests

1. Branch from `main` with a descriptive name (`feat/tool-gateway`).
2. Ensure `make lint` and `make test` pass.
3. Describe what changed, why, and how it was tested.
4. Keep changes focused; unrelated refactors belong in separate PRs.

## Repository layout

See the root `README.md` for the monorepo structure and service map.
