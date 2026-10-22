package connect

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
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

// openErrorTransport fails to open every stream with err.
type openErrorTransport struct {
	err error
}

func (t openErrorTransport) NewClientStream(context.Context, connect.Spec) (connect.ClientStream, error) {
	return nil, t.err
}

func TestClientInterceptorOpenError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantCode  connect.Code
		wantError bool
	}{
		{
			name:      "coded",
			err:       connect.NewError(connect.CodeUnavailable, "unavailable"),
			wantCode:  connect.CodeUnavailable,
			wantError: true,
		},
		{
			name:      "coded with a canceled cause",
			err:       connect.NewError(connect.CodeUnavailable, "unavailable").WithCause(context.Canceled),
			wantCode:  connect.CodeUnavailable,
			wantError: true,
		},
		{
			name:     "uncoded canceled",
			err:      fmt.Errorf("token: %w", context.Canceled),
			wantCode: connect.CodeCanceled,
		},
		{
			name:      "uncoded deadline exceeded",
			err:       fmt.Errorf("token: %w", context.DeadlineExceeded),
			wantCode:  connect.CodeDeadlineExceeded,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			client := connect.NewClient(openErrorTransport{err: tt.err}, NewClientInterceptor())

			if _, err := callUnary(context.Background(), client, "hello"); err == nil {
				t.Fatal("expected error")
			}

			spans := mt.FinishedSpans()
			if len(spans) != 1 {
				t.Fatalf("expected 1 span, got %d", len(spans))
			}
			if got := spans[0].Tag(tagCode); got != tt.wantCode.String() {
				t.Errorf("expected connect.code %s, got %v", tt.wantCode, got)
			}
			if isError := spans[0].Tag(ext.ErrorMsg) != nil; isError != tt.wantError {
				t.Errorf("expected error %v, got %v", tt.wantError, isError)
			}
		})
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
	client := connect.NewClient(
		connecthttp.NewTransport(httpServer.Client(), httpServer.URL),
		NewClientInterceptor(),
	)

	// a caller sets request headers once and reuses the CallInfo for every call
	ctx, info := connect.NewClientContext(context.Background())
	info.RequestHeader().Set("Authorization", "token")
	for range 2 {
		_, err := callUnary(ctx, client, "hello")
		requireNoError(t, err)
	}
	if got := svc.requestHeader("Authorization"); got != "token" {
		t.Errorf("expected the caller's headers to be sent, got %q", got)
	}

	// each traced call overwrites the trace context of the previous one
	spans := spansByOperation(mt)
	serverSpans := spans["connect.server.request"]
	clientSpans := spans["connect.client.request"]
	if len(serverSpans) != 2 || len(clientSpans) != 2 {
		t.Fatalf("expected 2 server spans and 2 client spans, got %v", mt.FinishedSpans())
	}
	for _, clientSpan := range clientSpans {
		var children int
		for _, serverSpan := range serverSpans {
			if serverSpan.ParentID() == clientSpan.SpanID() {
				children++
			}
		}
		if children != 1 {
			t.Errorf("expected each client span to be continued by exactly 1 server span, got %d", children)
		}
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

// abortingTransport opens streams whose method named abort panics, or exits
// the goroutine with runtime.Goexit when goexit is set.
type abortingTransport struct {
	abort  string
	goexit bool
}

func (t abortingTransport) NewClientStream(context.Context, connect.Spec) (connect.ClientStream, error) {
	return abortingStream(t), nil
}

type abortingStream struct {
	abort  string
	goexit bool
}

func (s abortingStream) maybeAbort(method string) {
	if method != s.abort {
		return
	}
	if s.goexit {
		runtime.Goexit()
	}
	panic("boom")
}

func (s abortingStream) SendHeaders() error { return nil }
func (s abortingStream) Send(any) error     { s.maybeAbort("send"); return nil }
func (s abortingStream) CloseSend() error   { s.maybeAbort("close send"); return nil }
func (s abortingStream) Receive(any) error  { s.maybeAbort("receive"); return nil }
func (s abortingStream) Close() error       { return nil }

func TestClientInterceptorStreamAbort(t *testing.T) {
	for _, method := range []string{"send", "close send", "receive"} {
		t.Run(method+" panic", func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			client := connect.NewClient(abortingTransport{abort: method}, NewClientInterceptor())
			recovered := func() (r any) {
				defer func() { r = recover() }()
				_, _ = callUnary(context.Background(), client, "hello")
				return nil
			}()
			if recovered != "boom" {
				t.Fatalf("expected the panic to reach the caller, got %v", recovered)
			}
			// the panic value shows recover() caught it; Goexit records another message
			requireInternalSpan(t, mt, "panic: boom")
		})

		t.Run(method+" goexit", func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			client := connect.NewClient(abortingTransport{abort: method, goexit: true}, NewClientInterceptor())
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = callUnary(context.Background(), client, "hello")
			}()
			<-done
			requireInternalSpan(t, mt, method+" exited without returning")
		})
	}
}

// requireInternalSpan checks that exactly one span was finished, as internal
// with an error message containing msg.
func requireInternalSpan(t *testing.T, mt mocktracer.Tracer, msg string) {
	t.Helper()
	spans := mt.FinishedSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 finished span, got %d", len(spans))
	}
	if got := spans[0].Tag(tagCode); got != connect.CodeInternal.String() {
		t.Errorf("expected connect.code internal, got %v", got)
	}
	if got, _ := spans[0].Tag(ext.ErrorMsg).(string); !strings.Contains(got, msg) {
		t.Errorf("expected the error message to contain %q, got %q", msg, got)
	}
}

func TestInterceptorsCodedErrorWithCanceledCause(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	// the handler fails with unavailable because a database call was canceled
	svc := &testService{err: connect.NewError(connect.CodeUnavailable, "db down").WithCause(context.Canceled)}
	client := newInProcessClient(svc.newServer(NewServerInterceptor()), NewClientInterceptor())

	_, err := callUnary(context.Background(), client, "hello")
	if got := connect.CodeOf(err); got != connect.CodeUnavailable {
		t.Fatalf("expected the client to receive unavailable, got %v", err)
	}

	for _, span := range mt.FinishedSpans() {
		if got := span.Tag(tagCode); got != connect.CodeUnavailable.String() {
			t.Errorf("expected connect.code unavailable on %s, got %v", span.OperationName(), got)
		}
		if got := span.Tag(ext.ErrorMsg); got == nil {
			t.Errorf("expected %s to be marked as an error", span.OperationName())
		}
	}
	if got := len(mt.FinishedSpans()); got != 2 {
		t.Errorf("expected 2 spans, got %d", got)
	}
}
