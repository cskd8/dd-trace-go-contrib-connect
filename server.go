package connect

import (
	"context"

	"connectrpc.com/connect/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

var _ connect.ServerStream = (*requestTaggingServerStream)(nil)

// requestTaggingServerStream tags the span of a unary call with the request
// message. Server interceptors run before the request is unmarshaled, so the
// message is only available once the handler receives it.
type requestTaggingServerStream struct {
	connect.ServerStream
	cfg    *config
	span   *tracer.Span
	tagged bool
}

func (s *requestTaggingServerStream) Receive(m any) error {
	err := s.ServerStream.Receive(m)
	if err == nil && !s.tagged {
		s.tagged = true
		withRequestTags(s.cfg, m, s.span)
	}
	return err
}

var _ connect.ServerStream = (*messageTracingServerStream)(nil)

// messageTracingServerStream traces each message sent and received on a
// streaming call.
type messageTracingServerStream struct {
	connect.ServerStream
	cfg       *config
	ctx       context.Context
	procedure string
	header    *connect.Header
}

func (s *messageTracingServerStream) Receive(m any) (err error) {
	span, _ := startSpan(
		s.ctx,
		s.header,
		s.procedure,
		"connect.message",
		s.cfg.serviceName,
		true,
		s.cfg.startSpanOptions(tracer.Measured())...,
	)
	defer func() {
		withMetadataTags(s.cfg, s.header, span)
		withRequestTags(s.cfg, m, span)
		finishWithError(span, err, s.cfg)
	}()
	err = s.ServerStream.Receive(m)
	return err
}

func (s *messageTracingServerStream) Send(m any) (err error) {
	span, _ := startSpan(
		s.ctx,
		s.header,
		s.procedure,
		"connect.message",
		s.cfg.serviceName,
		true,
		s.cfg.startSpanOptions(tracer.Measured())...,
	)
	defer func() { finishWithError(span, err, s.cfg) }()
	err = s.ServerStream.Send(m)
	return err
}

// NewServerInterceptor returns a connect.ServerInterceptor which traces the
// RPCs served by a connect.Server. Unary calls are always traced; streaming
// calls and their messages are traced according to WithStreamCalls and
// WithStreamMessages.
func NewServerInterceptor(opts ...Option) connect.ServerInterceptor {
	cfg := new(config)
	serverDefaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) (err error) {
			if cfg.isUntraced(spec.Procedure) {
				return next(ctx, spec, stream)
			}
			info, _ := connect.CallInfoForServerContext(ctx)
			header := requestHeader(info)
			unary := spec.StreamType == connect.StreamTypeUnary
			if unary || cfg.traceStreamCalls {
				var span *tracer.Span
				span, ctx = startSpan(
					ctx,
					header,
					spec.Procedure,
					cfg.spanName,
					cfg.serviceName,
					true,
					cfg.startSpanOptions(tracer.Measured(),
						tracer.Tag(ext.SpanKind, ext.SpanKindServer))...,
				)
				span.SetTag(tagMethodKind, methodKind(spec.StreamType))
				withPeerTags(info, span)
				withMetadataTags(cfg, header, span)
				defer func() { finishWithError(span, err, cfg) }()
				if unary && cfg.withRequestTags {
					stream = &requestTaggingServerStream{ServerStream: stream, cfg: cfg, span: span}
				}
			}
			// trace each send and receive of a streaming call if message
			// tracing is enabled
			if !unary && cfg.traceStreamMessages {
				stream = &messageTracingServerStream{
					ServerStream: stream,
					cfg:          cfg,
					ctx:          ctx,
					procedure:    spec.Procedure,
					header:       header,
				}
			}
			err = next(ctx, spec, stream)
			return err
		}
	}
}
