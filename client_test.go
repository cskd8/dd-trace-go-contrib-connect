package connect

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
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
	_, err := callServerStream(ctx, client, "hello")
	requireNoError(t, err)
	parent.Finish()

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
}
