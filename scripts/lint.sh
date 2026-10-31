#!/usr/bin/env bash
# Run all linters and static analysis for the monorepo.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

status=0

echo "== go: vet =="
(cd services/control-plane && go vet ./...) || status=$?

echo ""
echo "== python: ruff + mypy =="
(cd services/agent-runtime && uv run ruff check . && uv run mypy app) || status=$?

echo ""
echo "== web: eslint + tsc =="
pnpm --filter @agentos/web lint || status=$?
pnpm --filter @agentos/web typecheck || status=$?

if [[ "$status" -ne 0 ]]; then
  echo ""
  echo "LINT FAILED" >&2
  exit "$status"
fi
echo ""
echo "all linters passed."
