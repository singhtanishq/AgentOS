#!/usr/bin/env bash
# Health check for all AgentOS services and infrastructure.
set -euo pipefail

check() {
  local name="$1" url="$2"
  if curl -fsS -o /dev/null -m 5 "$url"; then
    echo "ok      $name  $url"
  else
    echo "DOWN    $name  $url"
    return 1
  fi
}

status=0
check "control plane liveness" http://localhost:8080/healthz || status=1
check "control plane readiness" http://localhost:8080/readyz || status=1
check "agent runtime liveness" http://localhost:8090/healthz || status=1
check "agent runtime readiness" http://localhost:8090/readyz || status=1
check "dashboard" http://localhost:3000/ || status=1
check "nats monitoring" http://localhost:8222/healthz || status=1
check "object storage (s3)" http://localhost:9333/cluster/status || status=1
check "opa" http://localhost:8181/health || status=1
check "otel collector" http://localhost:13133/ || status=1
check "prometheus" http://localhost:9090/-/healthy || status=1
check "grafana" http://localhost:3001/api/health || status=1

if [[ "$status" -ne 0 ]]; then
  echo ""
  echo "some services are not healthy" >&2
  exit "$status"
fi
echo ""
echo "all services healthy."
