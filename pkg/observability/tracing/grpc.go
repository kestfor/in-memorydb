package tracing

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UnaryServerInterceptor returns a new unary server interceptor that traces requests.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		start := time.Now()
		ctx, span := StartSpan(ctx, info.FullMethod, trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()

		resp, err := handler(ctx, req)

		span.SetAttributes(
			attribute.String("rpc.method", info.FullMethod),
			attribute.Int64("rpc.duration_ms", time.Since(start).Milliseconds()),
		)

		if err != nil {
			s, _ := status.FromError(err)
			span.SetStatus(codes.Error, s.Message())
		} else {
			span.SetStatus(codes.Ok, "")
		}

		return resp, err
	}
}

// UnaryPanicRecoveryInterceptor returns a new unary server interceptor that recovers from panics.
func UnaryPanicRecoveryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp interface{}, err error) {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				slog.ErrorContext(ctx, "grpc.PanicRecovery: recovered from panic",
					"method", info.FullMethod,
					"panic", r,
					"stack", string(stack),
				)

				// Record error in span if tracing is enabled
				span := trace.SpanFromContext(ctx)
				if span.IsRecording() {
					span.SetStatus(codes.Error, "panic recovered")
					span.SetAttributes(
						attribute.String("panic.value", fmt.Sprintf("%v", r)),
						attribute.String("panic.stack", string(stack)),
					)
				}

				err = status.Errorf(grpccodes.Internal, "internal server error")
			}
		}()
		return handler(ctx, req)
	}
}

// ChainUnaryInterceptors chains multiple unary server interceptors into one.
func ChainUnaryInterceptors(interceptors ...grpc.UnaryServerInterceptor) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		chain := handler
		for i := len(interceptors) - 1; i >= 0; i-- {
			interceptor := interceptors[i]
			next := chain
			chain = func(ctx context.Context, req interface{}) (interface{}, error) {
				return interceptor(ctx, req, info, next)
			}
		}
		return chain(ctx, req)
	}
}
