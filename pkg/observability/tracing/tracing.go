package tracing

import (
	"context"
	"in-memorydb/pkg/observability"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// StartSpan starts a new span from the global tracer.
func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return observability.Tracer().Start(ctx, name, opts...)
}

// RecordError records an error on the span, sets the span status to Error,
// and returns the original error. This is a convenience function to ensure
// that errors are consistently recorded in traces.
func RecordError(ctx context.Context, err error) error {
	if err != nil {
		span := trace.SpanFromContext(ctx)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}
