#!/usr/bin/env bash
# Run every test suite for the monorepo.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

status=0

echo "== go: control plane (race detector) =="
(cd services/control-plane && go test -race ./...) || status=$?

echo ""
echo "== python: agent runtime =="
(cd services/agent-runtime && uv run pytest) || status=$?

echo ""
echo "== web: unit tests =="
pnpm --filter @agentos/web test || status=$?

if [[ "$status" -ne 0 ]]; then
  echo ""
  echo "ONE OR MORE TEST SUITES FAILED" >&2
  exit "$status"
fi
echo ""
echo "all test suites passed."
