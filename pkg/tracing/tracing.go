package tracing

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// Setup initializes OTLP HTTP tracing. Falls back to noop on failure.
func Setup(ctx context.Context, service, endpoint string) (func(context.Context) error, error) {
	exp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		slog.Warn("otel exporter unavailable, using noop", "err", err)
		return func(context.Context) error { return nil }, nil
	}
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(service),
		),
	)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// Tracer returns a named tracer.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

// Start is a convenience span starter with string attributes.
func Start(ctx context.Context, name string, attrs map[string]string) (context.Context, trace.Span) {
	kvs := make([]attribute.KeyValue, 0, len(attrs))
	for k, v := range attrs {
		kvs = append(kvs, attribute.String(k, v))
	}
	return Tracer("lattice").Start(ctx, name, trace.WithAttributes(kvs...))
}

// TraceIDString extracts the W3C trace id if present.
func TraceIDString(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}

// Stage names for distributed inference path.
const (
	StageGateway   = "gateway"
	StageRouter    = "router"
	StageScheduler = "scheduler"
	StageQueue     = "queue"
	StageBackend   = "backend"
	StageGPU       = "gpu"
	StageStream    = "stream"
	StageClient    = "client"
)

// AnnotateStage adds a stage attribute for dashboard filtering.
func AnnotateStage(span trace.Span, stage string) {
	span.SetAttributes(attribute.String("lattice.stage", stage))
	span.AddEvent(fmt.Sprintf("stage:%s", stage))
}
