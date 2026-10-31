#!/usr/bin/env bash
# Populate the local demo environment (idempotent).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if ! docker info >/dev/null 2>&1; then
  echo "error: docker daemon not running" >&2
  exit 1
fi
if ! docker compose ps --status running 2>/dev/null | grep -q postgres; then
  echo "starting infrastructure..."
  docker compose up -d --wait --wait-timeout 240 postgres
fi

export AGENTOS_DATABASE_URL="${AGENTOS_DATABASE_URL:-postgres://agentos:agentos_dev@localhost:5432/agentos?sslmode=disable}"

echo "applying migrations..."
( cd services/control-plane && go run ./cmd/migrate )

echo "seeding demo data..."
( cd services/control-plane && go run ./cmd/seed )
