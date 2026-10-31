// Package telemetry initializes OpenTelemetry and Prometheus for the
// control plane: OTLP export to the collector and a /metrics endpoint.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Providers owns the shutdown lifecycle for telemetry pipelines.
type Providers struct {
	tracer  *sdktrace.TracerProvider
	meter   *sdkmetric.MeterProvider
	mux     sync.Mutex
	stopped bool
}

// Init wires OTLP trace/metric exporters and the Prometheus registry.
// Failures degrade to local-only operation (the service still runs); the
// readiness probe does not gate on the collector.
func Init(ctx context.Context, serviceName, otlpEndpoint string, logger *slog.Logger) (*Providers, error) {
	res, err := sdkresource.Merge(
		sdkresource.Default(),
		sdkresource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("build resource: %w", err)
	}

	providers := &Providers{}
	if otlpEndpoint != "" {
		traceExporter, err := otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(trimScheme(otlpEndpoint)),
			otlptracehttp.WithInsecure(),
		)
		if err != nil {
			logger.Warn("otlp trace exporter unavailable — traces stay local",
				slog.Any("error", err))
		} else {
			providers.tracer = sdktrace.NewTracerProvider(
				sdktrace.WithBatcher(traceExporter),
				sdktrace.WithResource(res),
			)
		}

		metricExporter, err := otlpmetrichttp.New(ctx,
			otlpmetrichttp.WithEndpoint(trimScheme(otlpEndpoint)),
			otlpmetrichttp.WithInsecure(),
		)
		if err != nil {
			logger.Warn("otlp metric exporter unavailable — metrics stay local",
				slog.Any("error", err))
		} else {
			providers.meter = sdkmetric.NewMeterProvider(
				sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter,
					sdkmetric.WithInterval(15*time.Second))),
				sdkmetric.WithResource(res),
			)
		}
	}

	otel.SetTracerProvider(noopAwareTracer(providers.tracer))
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	if providers.meter != nil {
		otel.SetMeterProvider(providers.meter)
	}
	logger.Info("telemetry initialized",
		slog.Bool("otlp_traces", providers.tracer != nil),
		slog.Bool("otlp_metrics", providers.meter != nil),
	)
	return providers, nil
}

// Shutdown flushes and stops the pipelines.
func (p *Providers) Shutdown(ctx context.Context) error {
	p.mux.Lock()
	defer p.mux.Unlock()
	if p == nil || p.stopped {
		return nil
	}
	p.stopped = true
	var errs []error
	if p.tracer != nil {
		errs = append(errs, p.tracer.Shutdown(ctx))
	}
	if p.meter != nil {
		errs = append(errs, p.meter.Shutdown(ctx))
	}
	return errors.Join(errs...)
}

// MetricsHandler serves the Prometheus scrape endpoint.
func MetricsHandler() http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}

func trimScheme(endpoint string) string {
	for _, prefix := range []string{"http://", "https://"} {
		if len(endpoint) > len(prefix) && endpoint[:len(prefix)] == prefix {
			return endpoint
		}
	}
	return endpoint
}

func noopAwareTracer(p *sdktrace.TracerProvider) trace.TracerProvider {
	if p != nil {
		return p
	}
	// A provider with no processors: spans are created but never exported.
	noop := sdktrace.NewTracerProvider()
	return noop
}
