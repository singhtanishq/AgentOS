#!/usr/bin/env bash
# Run all AgentOS applications in one terminal with sane signal handling.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PIDS=()

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "error: required command '$1' not found in PATH" >&2
    exit 1
  fi
}

require pnpm
require go
require uv

cleanup() {
  echo ""
  echo "stopping services..."
  for pid in "${PIDS[@]:-}"; do
    kill "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "starting next.js dashboard (http://localhost:3000)"
(cd "$ROOT" && pnpm --filter @agentos/web dev) &
PIDS+=($!)

echo "starting go control plane (http://localhost:8080)"
(cd "$ROOT/services/control-plane" && go run ./cmd/server) &
PIDS+=($!)

echo "starting python agent runtime (http://localhost:8090)"
(cd "$ROOT/services/agent-runtime" && uv run python -m app) &
PIDS+=($!)

echo ""
echo "all services running — press Ctrl-C to stop"
wait
