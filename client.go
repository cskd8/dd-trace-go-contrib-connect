package connect

import (
	"context"
	"sync"

	"connectrpc.com/connect/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// clientStreamEnd runs a callback once a client stream has ended: when Receive
// fails (io.EOF included) or when the stream is closed. A Receive still in
// flight when the stream is closed returns once Close unblocks it, so it is
// left to end the stream with its own error.
type clientStreamEnd struct {
	mu        sync.Mutex
	receiving bool
	once      sync.Once
}

// startReceive marks a Receive in flight.
func (e *clientStreamEnd) startReceive() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.receiving = true
}

// endReceive marks the in-flight Receive as returned.
func (e *clientStreamEnd) endReceive() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.receiving = false
}

// closing reports whether Close should end the stream, i.e. no Receive is in
// flight.
func (e *clientStreamEnd) closing() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return !e.receiving
}

func (e *clientStreamEnd) end(fn func()) {
	e.once.Do(fn)
}

var _ connect.ClientStream = (*unaryClientStream)(nil)

// unaryClientStream finishes the span of a unary call once the response is
// received, or when the stream is closed before that, and then restores the
// request headers. connect.Client.CallUnary sends the request, receives the
// response and always closes the stream.
type unaryClientStream struct {
	connect.ClientStream
	cfg     *config
	span    *tracer.Span
	restore func()
	tagged  bool

	clientStreamEnd
	errMu sync.Mutex
	err   error
}

func (s *unaryClientStream) Send(msg any) error {
	if !s.tagged {
		s.tagged = true
		withRequestTags(s.cfg, msg, s.span)
	}
	err := s.ClientStream.Send(msg)
	s.recordError(err)
	return err
}

func (s *unaryClientStream) CloseSend() error {
	err := s.ClientStream.CloseSend()
	s.recordError(err)
	return err
}

func (s *unaryClientStream) Receive(msg any) error {
	s.startReceive()
	err := s.ClientStream.Receive(msg)
	s.endReceive()
	// a unary call ends with its single response
	s.finish(err)
	return err
}

func (s *unaryClientStream) Close() error {
	closeErr := s.ClientStream.Close()
	if s.closing() {
		// the span reports the error of the call, which is the first send
		// error when the response was never received
		s.errMu.Lock()
		err := s.err
		s.errMu.Unlock()
		if err == nil {
			err = closeErr
		}
		s.finish(err)
	}
	return closeErr
}

func (s *unaryClientStream) recordError(err error) {
	if err == nil {
		return
	}
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if s.err == nil {
		s.err = err
	}
}

func (s *unaryClientStream) finish(err error) {
	s.end(func() {
		finishWithError(s.span, err, s.cfg)
		s.restore()
	})
}

var _ connect.ClientStream = (*propagatingClientStream)(nil)

// propagatingClientStream restores the request headers once a streaming call
// whose trace context was propagated has ended.
type propagatingClientStream struct {
	connect.ClientStream
	restore func()

	clientStreamEnd
}

func (s *propagatingClientStream) Receive(msg any) error {
	s.startReceive()
	err := s.ClientStream.Receive(msg)
	s.endReceive()
	if err != nil {
		s.end(s.restore)
	}
	return err
}

func (s *propagatingClientStream) Close() error {
	err := s.ClientStream.Close()
	if s.closing() {
		s.end(s.restore)
	}
	return err
}

// NewClientInterceptor returns a connect.ClientInterceptor which traces unary
// client calls and injects the trace context into the request headers so the
// server can continue the trace. Streaming client calls are not traced as
// spans; the active span context of ctx is only propagated to the server.
// The injected headers are removed from the CallInfo once the call has ended.
func NewClientInterceptor(opts ...Option) connect.ClientInterceptor {
	cfg := new(config)
	clientDefaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			info, _ := connect.CallInfoForClientContext(ctx)
			header := requestHeader(info)
			if spec.StreamType != connect.StreamTypeUnary {
				span, ok := tracer.SpanFromContext(ctx)
				if !ok {
					return next(ctx, spec)
				}
				restore := injectSpanContext(span.Context(), header)
				stream, err := next(ctx, spec)
				if err != nil {
					restore()
					return nil, err
				}
				return &propagatingClientStream{ClientStream: stream, restore: restore}, nil
			}
			if cfg.isUntraced(spec.Procedure) {
				return next(ctx, spec)
			}
			span, ctx := startSpan(
				ctx,
				header,
				spec.Procedure,
				cfg.spanName,
				cfg.serviceName,
				false,
				cfg.startSpanOptions(tracer.Measured(),
					tracer.Tag(ext.SpanKind, ext.SpanKindClient))...,
			)
			span.SetTag(tagMethodKind, methodKindUnary)
			withMetadataTags(cfg, header, span)
			// propagate the span context to the server through the request headers
			restore := injectSpanContext(span.Context(), header)
			stream, err := next(ctx, spec)
			// the transport sets the peer address and protocol when it opens the stream
			withPeerTags(info, span)
			if err != nil {
				finishWithError(span, err, cfg)
				restore()
				return nil, err
			}
			return &unaryClientStream{ClientStream: stream, cfg: cfg, span: span, restore: restore}, nil
		}
	}
}
