# infra/docker — Docker service configuration for AgentOS.

The canonical local stack definition is the root `docker-compose.yml`.
This directory holds Docker-related configuration that is shared across
services as the platform grows (base images, build helpers).

## Service map

| Service        | Image                       | Ports (host)     | Purpose                          |
| -------------- | --------------------------- | ---------------- | -------------------------------- |
| postgres       | postgres:18                 | 5432             | Primary datastore                |
| redis          | redis:8-alpine              | 6379             | Cache / rate limiting            |
| nats           | nats:2.11-alpine            | 4222, 8222       | Durable execution queue + events |
| seaweedfs      | chrislusf/seaweedfs (pinned)| 9000, 9333       | S3-compatible object storage     |
| opa            | openpolicyagent/opa         | 8181             | Authorization decisions          |
| otel-collector | otel/opentelemetry-collector-contrib | 4317, 4318 | Telemetry ingestion         |
| prometheus     | prom/prometheus             | 9090             | Metrics storage and queries      |
| grafana        | grafana/grafana             | 3001             | Metrics dashboards               |

> **Object storage note:** MinIO withdrew anonymous public access to its
> container images, so the local stack uses SeaweedFS in S3 gateway mode
> (pinned by image digest). It exposes a standard S3-compatible API on
> port 9000; applications use ordinary S3 clients against it.

All credentials in the compose file are **local-only development defaults**.
Never reuse them outside your machine.
