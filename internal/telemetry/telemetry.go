// Package telemetry sets up OpenTelemetry tracing for Stampede itself.
// Tracing is off unless the standard OTEL_EXPORTER_OTLP_ENDPOINT (or
// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT) environment variable is set; then
// spans are exported over OTLP, using gRPC when OTEL_EXPORTER_OTLP_PROTOCOL
// (or ..._TRACES_PROTOCOL) is "grpc" and HTTP/protobuf otherwise. The
// other standard variables (headers, sampler, service name, resource
// attributes) are honoured by the OpenTelemetry SDK.
package telemetry

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Enabled reports whether an OTLP endpoint is configured.
func Enabled() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != ""
}

// Protocol returns the configured OTLP protocol for traces: "grpc" or
// "http/protobuf" (the default).
func Protocol() string {
	p := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL")
	if p == "" {
		p = os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")
	}
	if strings.EqualFold(strings.TrimSpace(p), "grpc") {
		return "grpc"
	}
	return "http/protobuf"
}

// Setup installs a global tracer provider exporting over OTLP when an
// endpoint is configured, and the W3C trace context propagator. It returns
// a function that flushes and stops the exporter. Without an endpoint it
// changes nothing and the returned function is a no-op.
func Setup(ctx context.Context, service, version string) (shutdown func(context.Context) error, err error) {
	noop := func(context.Context) error { return nil }
	if !Enabled() {
		return noop, nil
	}
	var exp sdktrace.SpanExporter
	if Protocol() == "grpc" {
		exp, err = otlptracegrpc.New(ctx)
	} else {
		exp, err = otlptracehttp.New(ctx)
	}
	if err != nil {
		return noop, fmt.Errorf("OTLP trace exporter: %w", err)
	}
	// Attributes first, then the environment, so OTEL_SERVICE_NAME and
	// OTEL_RESOURCE_ATTRIBUTES override the defaults.
	res, err := resource.New(ctx,
		resource.WithAttributes(attribute.String("service.name", service), attribute.String("service.version", version)),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
	)
	if err != nil {
		return noop, fmt.Errorf("OpenTelemetry resource: %w", err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp.Shutdown, nil
}
