package connect

import (
	"context"
	"sync"

	"connectrpc.com/connect/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

var _ connect.ClientStream = (*unaryClientStream)(nil)

// unaryClientStream finishes the span of a unary call once the response is
// received, or when the stream is closed before that. connect.Client.CallUnary
// sends the request, receives the response and always closes the stream.
type unaryClientStream struct {
	connect.ClientStream
	cfg    *config
	span   *tracer.Span
	tagged bool

	mu sync.Mutex
	// receiveStarted is set once Receive is called. From then on Receive
	// finishes the span: a Close during it unblocks the Receive, which then
	// finishes the span with its own error.
	receiveStarted bool
	err            error
	once           sync.Once
}

func (s *unaryClientStream) Send(msg any) (err error) {
	returned := false
	defer finishIfAborted(s.finish, "send", &returned)
	if !s.tagged {
		s.tagged = true
		withRequestTags(s.cfg, msg, s.span)
	}
	err = s.ClientStream.Send(msg)
	returned = true
	s.recordError(err)
	return err
}

func (s *unaryClientStream) CloseSend() (err error) {
	returned := false
	defer finishIfAborted(s.finish, "close send", &returned)
	err = s.ClientStream.CloseSend()
	returned = true
	s.recordError(err)
	return err
}

func (s *unaryClientStream) Receive(msg any) (err error) {
	s.mu.Lock()
	s.receiveStarted = true
	s.mu.Unlock()
	returned := false
	defer finishIfAborted(s.finish, "receive", &returned)
	err = s.ClientStream.Receive(msg)
	returned = true
	// a unary call ends with its single response
	s.finish(err)
	return err
}

// finishIfAborted is deferred around a call that finishes the span when it
// returns. When the call panics or exits its goroutine with runtime.Goexit, so
// returned is still false, it finishes the span as internal through finish and
// lets the panic continue. It calls recover itself, so it must be the deferred
// function.
func finishIfAborted(finish func(error), what string, returned *bool) {
	if *returned {
		return
	}
	if r := recover(); r != nil {
		finish(connect.Errorf(connect.CodeInternal, "panic: %v", r))
		panic(r)
	}
	finish(connect.NewError(connect.CodeInternal, what+" exited without returning"))
}

func (s *unaryClientStream) Close() error {
	closeErr := s.ClientStream.Close()
	s.mu.Lock()
	receiveStarted := s.receiveStarted
	// the span reports the error of the call, which is the first send error
	// when the response was never received
	err := s.err
	s.mu.Unlock()
	if receiveStarted {
		return closeErr
	}
	if err == nil {
		err = closeErr
	}
	s.finish(err)
	return closeErr
}

func (s *unaryClientStream) recordError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = err
	}
}

func (s *unaryClientStream) finish(err error) {
	s.once.Do(func() { finishCallWithError(s.span, err, s.cfg) })
}

// NewClientInterceptor returns a connect.ClientInterceptor which traces unary
// client calls and injects the trace context into the request headers so the
// server can continue the trace. Streaming client calls are not traced as
// spans; the active span context of ctx is only propagated to the server.
//
// Like otelconnect, the trace context is written into the request headers of
// the call's CallInfo and stays there after the call.
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
				if span, ok := tracer.SpanFromContext(ctx); ok {
					_ = tracer.Inject(span.Context(), headerCarrier{header: header})
				}
				return next(ctx, spec)
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
			_ = tracer.Inject(span.Context(), headerCarrier{header: header})
			opened := false
			defer finishIfAborted(func(err error) { finishCallWithError(span, err, cfg) }, "open", &opened)
			stream, err := next(ctx, spec)
			opened = true
			// the transport sets the peer address and protocol when it opens the stream
			withPeerTags(info, span)
			if err != nil {
				finishCallWithError(span, err, cfg)
				return nil, err
			}
			return &unaryClientStream{ClientStream: stream, cfg: cfg, span: span}, nil
		}
	}
}
