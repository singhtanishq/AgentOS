#!/usr/bin/env bash
# Start the AgentOS local infrastructure stack and wait for health.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if ! command -v docker >/dev/null 2>&1; then
  echo "error: docker is required (https://docs.docker.com/get-docker/)" >&2
  exit 1
fi

if ! docker info >/dev/null 2>&1; then
  echo "error: docker daemon is not running — start Docker Desktop first" >&2
  exit 1
fi

PROFILE="${1:-}"
if [[ -n "$PROFILE" && "$PROFILE" != "apps" ]]; then
  echo "usage: infra-up.sh [apps]" >&2
  exit 2
fi

echo "starting AgentOS infrastructure..."
if [[ "$PROFILE" == "apps" ]]; then
  docker compose --profile apps up -d --build --wait --wait-timeout 300
else
  docker compose up -d --wait --wait-timeout 240
fi

echo ""
echo "service status:"
docker compose ps --format "table {{.Name}}\t{{.Service}}\t{{.Status}}"
echo ""
echo "endpoints:"
echo "  postgres       localhost:5432"
echo "  redis          localhost:6379"
echo "  nats           localhost:4222 (monitoring :8222)"
echo "  s3 storage     localhost:9000 (master ui :9333)"
echo "  opa            localhost:8181"
echo "  otel collector localhost:4317 (grpc) / :4318 (http) / health :13133"
echo "  prometheus     localhost:9090"
echo "  grafana        localhost:3001"

if [[ "$PROFILE" == "apps" ]]; then
  echo "  control plane  localhost:8080"
  echo "  agent runtime  localhost:8090"
  echo "  dashboard      localhost:3000"
fi

echo ""
echo "run './scripts/health-check.sh' to verify service health."
