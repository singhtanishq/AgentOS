"""OpenTelemetry metrics for the execution plane.

Execution outcomes and durations are exported to the OTLP collector
(Scraped by Prometheus through the collector's exporter). Telemetry
failures never affect execution results: exporters degrade silently and
the runtime operates without them.
"""

from __future__ import annotations

import logging
from typing import Any

logger = logging.getLogger(__name__)

_instruments: dict[str, Any] = {}


def init_metrics(otlp_endpoint: str | None) -> bool:
    """Initialize the OTLP metric exporter. Returns True when active.

    The endpoint is the OTLP HTTP receiver, e.g. http://localhost:4318
    (without the /v1/metrics suffix).
    """
    if not otlp_endpoint:
        return False
    try:
        from opentelemetry import metrics
        from opentelemetry.exporter.otlp.proto.http.metric_exporter import (
            OTLPMetricExporter,
        )
        from opentelemetry.sdk.metrics import MeterProvider
        from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
        from opentelemetry.sdk.resources import Resource

        resource = Resource.create({"service.name": "agentos-agent-runtime"})
        exporter = OTLPMetricExporter(endpoint=f"{otlp_endpoint.rstrip('/')}/v1/metrics")
        reader = PeriodicExportingMetricReader(exporter, export_interval_millis=15000)
        provider = MeterProvider(metric_readers=[reader], resource=resource)
        metrics.set_meter_provider(provider)

        meter = metrics.get_meter("agentos.runtime")
        _instruments["executions"] = meter.create_counter(
            "agentos.executions.total",
            description="Executions processed by the runtime",
            unit="{execution}",
        )
        _instruments["duration"] = meter.create_histogram(
            "agentos.execution.duration",
            description="Execution wall-clock duration",
            unit="ms",
        )
        _instruments["tokens"] = meter.create_counter(
            "agentos.execution.tokens",
            description="Tokens consumed by executions",
            unit="{token}",
        )
        return True
    except Exception as exc:  # noqa: BLE001 - telemetry must never break runs
        logger.warning("metrics init failed: %s", exc)
        return False


def record_execution_outcome(
    status: str,
    provider: str,
    model: str,
    duration_ms: int,
    total_tokens: int,
) -> None:
    """Record one finished execution on the metrics instruments."""
    counter = _instruments.get("executions")
    if counter is not None:
        counter.add(
            1,
            {"status": status, "provider": provider, "model": model},
        )
    histogram = _instruments.get("duration")
    if histogram is not None:
        histogram.record(
            duration_ms,
            {"status": status, "provider": provider, "model": model},
        )
    tokens = _instruments.get("tokens")
    if tokens is not None and total_tokens > 0:
        tokens.add(
            total_tokens,
            {"status": status, "provider": provider, "model": model},
        )
