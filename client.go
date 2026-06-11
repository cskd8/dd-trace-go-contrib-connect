package connect

import (
	"context"

	"connectrpc.com/connect"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

var _ connect.Interceptor = (*clientInterceptor)(nil)

type clientInterceptor struct {
	cfg *config
}

func (c clientInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		spec := req.Spec()
		_, im := c.cfg.ignoredMethods[spec.Procedure]
		_, um := c.cfg.untracedMethods[spec.Procedure]
		if im || um {
			return next(ctx, req)
		}
		span, ctx := startSpan(
			ctx,
			req.Header(),
			spec.Procedure,
			c.cfg.spanName,
			c.cfg.serviceName,
			false,
			c.cfg.startSpanOptions(tracer.Measured(),
				tracer.Tag(ext.SpanKind, ext.SpanKindClient))...,
		)
		span.SetTag(tagMethodKind, methodKindUnary)
		withPeerTags(req.Peer(), span)
		withMetadataTags(c.cfg, req.Header(), span)
		withRequestTags(c.cfg, req.Any(), span)
		// propagate the span context to the server through the request headers
		_ = tracer.Inject(span.Context(), tracer.HTTPHeadersCarrier(req.Header()))
		resp, err := next(ctx, req)
		finishWithError(span, err, c.cfg)
		return resp, err
	}
}

// WrapStreamingClient propagates the active span context to the server through
// the request headers. Streaming client calls are not traced as spans.
func (c clientInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		if span, ok := tracer.SpanFromContext(ctx); ok {
			_ = tracer.Inject(span.Context(), tracer.HTTPHeadersCarrier(conn.RequestHeader()))
		}
		return conn
	}
}

func (c clientInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// NewClientInterceptor returns a connect.Interceptor which traces unary client
// calls and injects the trace context into the request headers so the server
// can continue the trace.
func NewClientInterceptor(opts ...Option) connect.Interceptor {
	cfg := new(config)
	clientDefaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}
	return &clientInterceptor{cfg: cfg}
}
