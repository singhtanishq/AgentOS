#!/usr/bin/env bash
# Apply AgentOS PostgreSQL migrations (run as the migration owner).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# shellcheck disable=SC1091
[[ -f .env ]] && set -a && source .env && set +a

export AGENTOS_DATABASE_URL="${AGENTOS_DATABASE_URL:-postgres://agentos:agentos_dev@localhost:5432/agentos?sslmode=disable}"

# The migrate command runs as the schema owner role, not the application roles.
MIGRATE_URL="${MIGRATE_DATABASE_URL:-$AGENTOS_DATABASE_URL}"

if ! command -v go >/dev/null 2>&1; then
  echo "error: go is required to run migrations" >&2
  exit 1
fi

echo "applying migrations to ${MIGRATE_URL%%\?*} (credentials omitted)..."
cd services/control-plane && go run ./cmd/migrate --url "$MIGRATE_URL"
