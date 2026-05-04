package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const (
	defaultServiceName       = "imgsrv"
	defaultHTTPOperationName = "imgsrv.http"
)

// Config configures process-local telemetry providers.
type Config struct {
	// ServiceName is the OpenTelemetry service.name resource attribute.
	ServiceName string

	// ServiceVersion is the OpenTelemetry service.version resource attribute.
	ServiceVersion string

	// MetricsPath is the HTTP path that serves Prometheus metrics.
	MetricsPath string
}

// Telemetry holds process-local OpenTelemetry providers and scrape handlers.
type Telemetry struct {
	// MeterProvider creates meters for application and HTTP metrics.
	MeterProvider metric.MeterProvider

	// TracerProvider creates tracers for HTTP instrumentation.
	TracerProvider trace.TracerProvider

	// MetricsHandler serves Prometheus-formatted metrics.
	MetricsHandler http.Handler

	// MetricsPath is the path registered on MetricsHandler.
	MetricsPath string

	propagators   propagation.TextMapPropagator
	meterProvider *sdkmetric.MeterProvider
}

// New constructs process-local telemetry providers and a Prometheus handler.
func New(config Config) (*Telemetry, error) {
	if config.ServiceName == "" {
		config.ServiceName = defaultServiceName
	}
	if !strings.HasPrefix(config.MetricsPath, "/") {
		return nil, fmt.Errorf("metrics path %q must start with /", config.MetricsPath)
	}

	registerer := prometheus.NewRegistry()
	exporter, err := otelprometheus.New(otelprometheus.WithRegisterer(registerer))
	if err != nil {
		return nil, fmt.Errorf("create prometheus exporter: %w", err)
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithResource(newResource(config)),
	)

	mux := http.NewServeMux()
	mux.Handle(config.MetricsPath, promhttp.HandlerFor(registerer, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	}))

	return &Telemetry{
		MeterProvider:  meterProvider,
		TracerProvider: noop.NewTracerProvider(),
		MetricsHandler: mux,
		MetricsPath:    config.MetricsPath,
		propagators: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
		meterProvider: meterProvider,
	}, nil
}

// Meter returns a meter from the configured meter provider.
func (t *Telemetry) Meter(name string, opts ...metric.MeterOption) metric.Meter {
	return t.MeterProvider.Meter(name, opts...)
}

// WrapHTTPHandler instruments handler with OpenTelemetry HTTP metrics.
func (t *Telemetry) WrapHTTPHandler(handler http.Handler) http.Handler {
	if t == nil {
		return handler
	}

	return otelhttp.NewHandler(
		handler,
		defaultHTTPOperationName,
		otelhttp.WithMeterProvider(t.MeterProvider),
		otelhttp.WithTracerProvider(t.TracerProvider),
		otelhttp.WithPropagators(t.propagators),
		otelhttp.WithSpanNameFormatter(formatSpanName),
	)
}

// Shutdown releases telemetry provider resources.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t == nil || t.meterProvider == nil {
		return nil
	}
	if err := t.meterProvider.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown meter provider: %w", err)
	}
	return nil
}

func newResource(config Config) *resource.Resource {
	attrs := []attribute.KeyValue{
		attribute.String("service.name", config.ServiceName),
	}
	if config.ServiceVersion != "" {
		attrs = append(attrs, attribute.String("service.version", config.ServiceVersion))
	}

	return resource.NewWithAttributes("", attrs...)
}

func formatSpanName(_ string, r *http.Request) string {
	if r.Pattern != "" {
		return r.Method + " " + r.Pattern
	}
	return r.Method + " " + r.URL.Path
}
