package connect

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestClientInterceptorErrorCode(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svc := &testService{err: connect.NewError(connect.CodeNotFound, "not found")}
	client := newInProcessClient(svc.newServer(), NewClientInterceptor())

	_, err := callUnary(context.Background(), client, "hello")
	if got := connect.CodeOf(err); got != connect.CodeNotFound {
		t.Fatalf("expected not_found, got %v", err)
	}

	spans := mt.FinishedSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if got := spans[0].Tag(tagCode); got != connect.CodeNotFound.String() {
		t.Errorf("expected connect.code not_found, got %v", got)
	}
}

// unavailableTransport fails to open every stream.
type unavailableTransport struct{}

func (unavailableTransport) NewClientStream(context.Context, connect.Spec) (connect.ClientStream, error) {
	return nil, connect.NewError(connect.CodeUnavailable, "unavailable")
}

func TestClientInterceptorOpenError(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	client := connect.NewClient(unavailableTransport{}, NewClientInterceptor())

	if _, err := callUnary(context.Background(), client, "hello"); err == nil {
		t.Fatal("expected error")
	}

	spans := mt.FinishedSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if got := spans[0].Tag(tagCode); got != connect.CodeUnavailable.String() {
		t.Errorf("expected connect.code unavailable, got %v", got)
	}
}

// sendFailingTransport opens streams whose Send fails with err.
type sendFailingTransport struct {
	err error
}

func (t sendFailingTransport) NewClientStream(context.Context, connect.Spec) (connect.ClientStream, error) {
	return sendFailingStream(t), nil
}

type sendFailingStream struct {
	err error
}

func (s sendFailingStream) SendHeaders() error { return nil }
func (s sendFailingStream) Send(any) error     { return s.err }
func (s sendFailingStream) CloseSend() error   { return nil }
func (s sendFailingStream) Receive(any) error  { return s.err }
func (s sendFailingStream) Close() error       { return nil }

func TestClientInterceptorSendError(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	sendErr := connect.NewError(connect.CodeUnavailable, "unavailable")
	client := connect.NewClient(sendFailingTransport{err: sendErr}, NewClientInterceptor())

	_, err := callUnary(context.Background(), client, "hello")
	if !errors.Is(err, sendErr) {
		t.Fatalf("expected the send error, got %v", err)
	}
	// CallUnary joins the call error with the Close result, so a Close that
	// returned the send error again would duplicate it
	if got := err.Error(); got != sendErr.Error() {
		t.Errorf("expected only the send error, got %q", got)
	}

	spans := mt.FinishedSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if got := spans[0].Tag(tagCode); got != connect.CodeUnavailable.String() {
		t.Errorf("expected connect.code unavailable, got %v", got)
	}
}

func TestClientInterceptorUntracedMethods(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svc := &testService{}
	client := newInProcessClient(svc.newServer(), NewClientInterceptor(WithUntracedMethods(unarySpec.Procedure)))

	_, err := callUnary(context.Background(), client, "hello")
	requireNoError(t, err)

	if spans := mt.FinishedSpans(); len(spans) != 0 {
		t.Errorf("expected no spans, got %d", len(spans))
	}
	if got := svc.requestHeader("X-Datadog-Trace-Id"); got != "" {
		t.Errorf("expected no trace context to be injected, got %q", got)
	}
}

func TestClientInterceptorStreamPropagatesOnly(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svc := &testService{}
	client := newInProcessClient(svc.newServer(), NewClientInterceptor())

	parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
	ctx, info := connect.NewClientContext(ctx)
	_, err := callServerStream(ctx, client, "hello")
	requireNoError(t, err)
	parent.Finish()

	if info.RequestHeader().Has("X-Datadog-Parent-Id") {
		t.Errorf("expected the propagated headers to be removed once the stream ended")
	}

	spans := mt.FinishedSpans()
	if len(spans) != 1 || spans[0].OperationName() != "parent" {
		t.Fatalf("expected only the parent span, got %v", spans)
	}
	if got := svc.requestHeader("X-Datadog-Parent-Id"); got != strconv.FormatUint(spans[0].SpanID(), 10) {
		t.Errorf("expected the parent span context to be propagated, got %q", got)
	}
}

func TestClientInterceptorContinuedByServer(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svc := &testService{}
	client := newInProcessClient(svc.newServer(NewServerInterceptor()), NewClientInterceptor())

	_, err := callUnary(context.Background(), client, "hello")
	requireNoError(t, err)

	spans := spansByOperation(mt)
	if len(spans["connect.server.request"]) != 1 || len(spans["connect.client.request"]) != 1 {
		t.Fatalf("expected 1 server span and 1 client span, got %v", mt.FinishedSpans())
	}
	serverSpan := spans["connect.server.request"][0]
	clientSpan := spans["connect.client.request"][0]
	if serverSpan.TraceID() != clientSpan.TraceID() || serverSpan.ParentID() != clientSpan.SpanID() {
		t.Errorf("expected the server span to be a child of the client span")
	}
	// the in-process transport also runs the handler on the client ctx, so
	// check that the span context went through the request headers
	if got := svc.requestHeader("X-Datadog-Parent-Id"); got != strconv.FormatUint(clientSpan.SpanID(), 10) {
		t.Errorf("expected the client span context in the request headers, got %q", got)
	}
}

func TestClientInterceptorReusedCallInfo(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svc := &testService{}
	httpServer := newHTTPServer(svc.newServer(NewServerInterceptor()))
	defer httpServer.Close()
	newClient := func(opts ...Option) *connect.Client {
		return connect.NewClient(
			connecthttp.NewTransport(httpServer.Client(), httpServer.URL),
			NewClientInterceptor(opts...),
		)
	}
	traced := newClient()
	untraced := newClient(WithUntracedMethods(unarySpec.Procedure))

	// a caller sets request headers once and reuses the CallInfo for every call
	ctx, info := connect.NewClientContext(context.Background())
	info.RequestHeader().Set("Authorization", "token")

	_, err := callUnary(ctx, traced, "hello")
	requireNoError(t, err)
	for _, key := range []string{"X-Datadog-Trace-Id", "X-Datadog-Parent-Id", "Traceparent"} {
		if info.RequestHeader().Has(key) {
			t.Errorf("expected %s to be removed after the call", key)
		}
	}
	if got := info.RequestHeader().Get("Authorization"); got != "token" {
		t.Errorf("expected the caller's headers to be kept, got %q", got)
	}

	// neither of these calls propagates a trace context
	_, err = callServerStream(ctx, traced, "hello")
	requireNoError(t, err)
	_, err = callUnary(ctx, untraced, "hello")
	requireNoError(t, err)

	spans := spansByOperation(mt)
	serverSpans := spans["connect.server.request"]
	if len(serverSpans) != 3 || len(spans["connect.client.request"]) != 1 {
		t.Fatalf("expected 3 server spans and 1 client span, got %v", mt.FinishedSpans())
	}
	clientSpan := spans["connect.client.request"][0]
	var children int
	for _, span := range serverSpans {
		if span.ParentID() == clientSpan.SpanID() {
			children++
		}
	}
	if children != 1 {
		t.Errorf("expected only the traced call to continue the client span, got %d server spans", children)
	}
}

// blockingTransport opens streams whose Receive blocks until the stream is
// closed. receiving is closed once Receive has been called.
type blockingTransport struct {
	receiving chan struct{}
}

func (t blockingTransport) NewClientStream(context.Context, connect.Spec) (connect.ClientStream, error) {
	return &blockingStream{receiving: t.receiving, closed: make(chan struct{})}, nil
}

type blockingStream struct {
	receiving chan struct{}
	closed    chan struct{}
	once      sync.Once
}

func (s *blockingStream) SendHeaders() error { return nil }
func (s *blockingStream) Send(any) error     { return nil }
func (s *blockingStream) CloseSend() error   { return nil }

func (s *blockingStream) Receive(any) error {
	close(s.receiving)
	<-s.closed
	return connect.NewError(connect.CodeCanceled, "closed")
}

func (s *blockingStream) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

func TestClientInterceptorCloseDuringReceive(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	receiving := make(chan struct{})
	client := connect.NewClient(blockingTransport{receiving: receiving}, NewClientInterceptor())

	stream, err := client.CallClientStream(context.Background(), unarySpec)
	requireNoError(t, err)
	errs := make(chan error, 1)
	go func() {
		errs <- stream.Receive(&wrapperspb.StringValue{})
	}()
	<-receiving
	requireNoError(t, stream.Close())
	if err := <-errs; connect.CodeOf(err) != connect.CodeCanceled {
		t.Fatalf("expected canceled, got %v", err)
	}

	spans := mt.FinishedSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if got := spans[0].Tag(tagCode); got != connect.CodeCanceled.String() {
		t.Errorf("expected the span to report the receive error, got %v", got)
	}
}
