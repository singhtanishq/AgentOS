#!/usr/bin/env bash
# Verify that the local environment has every tool AgentOS needs.
set -euo pipefail

fail=0
require() {
  local name="$1" min="${2:-}"
  if command -v "$name" >/dev/null 2>&1; then
    local version
    version="$("$name" --version 2>&1 | head -1)"
    echo "ok      $name ($version)"
  else
    echo "MISSING $name  -- install instructions in README"
    fail=1
  fi
  if [[ -n "$min" && -n "${version:-}" ]]; then
    echo "        (expected >= $min)"
  fi
  return 0
}

require git
require docker
require go
require node
require pnpm
require uv
require python3

if [[ "$fail" -ne 0 ]]; then
  echo ""
  echo "environment incomplete — install the missing tools and re-run." >&2
  exit 1
fi

echo ""
echo "environment ready."
