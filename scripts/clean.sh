#!/usr/bin/env bash
# Remove disposable build artifacts. Never touches source, data or configuration.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

removed=0
remove() {
  local path="$1"
  if [[ -e "$path" ]]; then
    rm -rf "$path"
    echo "removed $path"
    removed=$((removed + 1))
  fi
}

remove apps/web/.next
remove services/control-plane/server
remove .pytest_cache
remove services/agent-runtime/.pytest_cache
remove services/agent-runtime/.ruff_cache
remove services/agent-runtime/.mypy_cache

echo ""
echo "clean complete ($removed artifact paths removed)."
