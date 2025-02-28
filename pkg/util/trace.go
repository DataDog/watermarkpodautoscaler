package util

import (
	"context"

	"github.com/go-logr/logr"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func InjectTraceIDsIntoLogger(ctx context.Context, logger logr.Logger) logr.Logger {
	// Retrieve the current span from the context
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		// No active span found in context; return the original logger
		return logger
	}

	// Extract Trace ID and Span ID from the span context
	traceID := span.Context().TraceID()
	spanID := span.Context().SpanID()
	if traceID == "" || spanID == 0 {
		// No valid Trace ID or Span ID found; return the original logger
		return logger
	}

	// Inject Trace ID and Span ID into the logger's context
	return logger.WithValues(
		ext.LogKeyTraceID, traceID,
		ext.LogKeySpanID, spanID,
	)
}
