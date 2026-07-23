// Package observability sets up OpenTelemetry tracing. Spans (and their trace
// IDs) are always generated so every request and every API error carries a
// trace_id; export is opt-in via PSYCHOSPACE_OTLP_ENDPOINT (off by default).
package observability

import (
	"context"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"net/http"
)

// Init installs a TracerProvider that always samples (so trace IDs exist) and
// only exports when otlpEndpoint is non-empty. Returns a shutdown func.
func Init(ctx context.Context, serviceName, otlpEndpoint string) (func(context.Context) error, error) {
	// Schemaless avoids schema-URL conflicts with resource.Default(); we only
	// need the service name (and we're not exporting by default anyway).
	res := resource.NewSchemaless(semconv.ServiceName(serviceName))

	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
	}
	if otlpEndpoint != "" {
		exp, err := otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(otlpEndpoint),
			otlptracehttp.WithInsecure(),
		)
		if err != nil {
			return nil, err
		}
		opts = append(opts, sdktrace.WithBatcher(exp))
	}

	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))
	return tp.Shutdown, nil
}

// WrapHandler adds a server span (and trace-context propagation) around h.
func WrapHandler(h http.Handler, op string) http.Handler {
	return otelhttp.NewHandler(h, op)
}

// TraceID returns the current trace id, or "" if none.
func TraceID(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if sc.HasTraceID() {
		return sc.TraceID().String()
	}
	return ""
}
