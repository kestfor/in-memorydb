package observability

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const serviceName = "Lume-db"

var noopProvider = noop.NewTracerProvider()

var (
	mu             sync.Mutex
	tracerProvider trace.TracerProvider = noopProvider
)

func InitTracerWithEndpoint(ctx context.Context, endpoint string) error {
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return err
	}

	res, _ := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
		),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	mu.Lock()
	tracerProvider = tp
	mu.Unlock()
	return nil
}

func Tracer() trace.Tracer {
	return tracerProvider.Tracer(serviceName)
}
