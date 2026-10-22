package connect

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestNewServerInterceptor(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
	}{
		{
			name: "default options",
			opts: nil,
		},
		{
			name: "with service name",
			opts: []Option{WithService("test-service")},
		},
		{
			name: "with multiple options",
			opts: []Option{
				WithService("test-service"),
				WithStreamCalls(false),
				NoDebugStack(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interceptor := NewServerInterceptor(tt.opts...)
			if interceptor == nil {
				t.Error("expected interceptor to be created")
			}
		})
	}
}

func TestServerInterceptorUntracedMethods(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
	}{
		{
			name: "with ignored methods",
			opt:  WithIgnoredMethods(unarySpec.Procedure, serverStreamSpec.Procedure),
		},
		{
			name: "with untraced methods",
			opt:  WithUntracedMethods(unarySpec.Procedure, serverStreamSpec.Procedure),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			svc := &testService{}
			client := newInProcessClient(svc.newServer(NewServerInterceptor(tt.opt)))

			_, err := callUnary(context.Background(), client, "hello")
			requireNoError(t, err)
			_, err = callServerStream(context.Background(), client, "hello")
			requireNoError(t, err)

			if spans := mt.FinishedSpans(); len(spans) != 0 {
				t.Errorf("expected no spans, got %d", len(spans))
			}
		})
	}
}

func TestServerInterceptorStream(t *testing.T) {
	tests := []struct {
		name             string
		opts             []Option
		wantCallSpans    int
		wantMessageSpans int
	}{
		{
			name:             "default options",
			wantCallSpans:    1,
			wantMessageSpans: 3, // 1 receive + 2 sends
		},
		{
			name:             "stream calls disabled",
			opts:             []Option{WithStreamCalls(false)},
			wantCallSpans:    0,
			wantMessageSpans: 3,
		},
		{
			name:             "stream messages disabled",
			opts:             []Option{WithStreamMessages(false)},
			wantCallSpans:    1,
			wantMessageSpans: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			svc := &testService{}
			client := newInProcessClient(svc.newServer(NewServerInterceptor(tt.opts...)))

			n, err := callServerStream(context.Background(), client, "hello")
			requireNoError(t, err)
			if n != 2 {
				t.Fatalf("expected 2 messages, got %d", n)
			}

			spans := spansByOperation(mt)
			callSpans := spans["connect.server.request"]
			messageSpans := spans["connect.message"]
			if len(callSpans) != tt.wantCallSpans {
				t.Fatalf("expected %d call spans, got %d", tt.wantCallSpans, len(callSpans))
			}
			if len(messageSpans) != tt.wantMessageSpans {
				t.Fatalf("expected %d message spans, got %d", tt.wantMessageSpans, len(messageSpans))
			}
			if len(callSpans) == 1 {
				if got := callSpans[0].Tag(tagMethodKind); got != methodKindServerStream {
					t.Errorf("expected connect.method.kind server_streaming, got %v", got)
				}
				if got := callSpans[0].Tag(tagCode); got != codeOK {
					t.Errorf("expected connect.code ok, got %v", got)
				}
				for _, span := range messageSpans {
					if span.ParentID() != callSpans[0].SpanID() {
						t.Errorf("expected message spans to be children of the call span")
					}
				}
			}
		})
	}
}

func TestServerInterceptorStreamRequestTags(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svc := &testService{}
	client := newInProcessClient(svc.newServer(NewServerInterceptor(WithRequestTags())))

	_, err := callServerStream(context.Background(), client, "hello")
	requireNoError(t, err)

	var tagged int
	for _, span := range spansByOperation(mt)["connect.message"] {
		if span.Tag(tagRequest) != nil {
			tagged++
		}
	}
	// only the received request message is tagged
	if tagged != 1 {
		t.Errorf("expected 1 message span with the request tag, got %d", tagged)
	}
}

func TestServerInterceptorCanceledIsNotError(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svc := &testService{err: connect.NewError(connect.CodeCanceled, "canceled")}
	client := newInProcessClient(svc.newServer(NewServerInterceptor()))

	if _, err := callUnary(context.Background(), client, "hello"); err == nil {
		t.Fatal("expected error")
	}

	span := mt.FinishedSpans()[0]
	if got := span.Tag(tagCode); got != connect.CodeCanceled.String() {
		t.Errorf("expected connect.code canceled, got %v", got)
	}
	if got := span.Tag(ext.ErrorMsg); got != nil {
		t.Errorf("expected canceled not to be an error, got %v", got)
	}
}

func TestServerInterceptorGoexit(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	serve := NewServerInterceptor()(func(context.Context, connect.Spec, connect.ServerStream) error {
		runtime.Goexit()
		return nil
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = serve(context.Background(), unarySpec, nil)
	}()
	<-done

	spans := mt.FinishedSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if got := spans[0].Tag(tagCode); got != connect.CodeInternal.String() {
		t.Errorf("expected connect.code internal, got %v", got)
	}
}

func TestServerInterceptorPanic(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	var recovered any
	recoverInterceptor := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) (err error) {
			defer func() {
				if r := recover(); r != nil {
					recovered = r
					err = connect.NewError(connect.CodeInternal, "internal error")
				}
			}()
			return next(ctx, spec, stream)
		}
	}
	server := connect.NewServer(recoverInterceptor, NewServerInterceptor())
	server.Register(connect.Method{
		Spec: unarySpec,
		Handler: func(context.Context, connect.Spec, connect.ServerStream) error {
			panic("boom")
		},
	})

	if _, err := callUnary(context.Background(), newInProcessClient(server), "hello"); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("expected internal, got %v", err)
	}
	if recovered != "boom" {
		t.Errorf("expected the panic to reach the outer interceptor, got %v", recovered)
	}

	spans := mt.FinishedSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if got := spans[0].Tag(tagCode); got != connect.CodeInternal.String() {
		t.Errorf("expected connect.code internal, got %v", got)
	}
	if got, _ := spans[0].Tag(ext.ErrorMsg).(string); !strings.Contains(got, "boom") {
		t.Errorf("expected the error message to contain the panic value, got %q", got)
	}
}

func TestServerInterceptorRemoteError(t *testing.T) {
	tests := []struct {
		name string
		code connect.Code
	}{
		{name: "not found", code: connect.CodeNotFound},
		// canceled is not an error by default, but the client receives internal
		{name: "canceled", code: connect.CodeCanceled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			// the gateway returns the error of its call to the downstream as is
			downstream := (&testService{err: connect.NewError(tt.code, "downstream")}).newServer()
			downstreamClient := newInProcessClient(downstream)
			gateway := connect.NewServer(NewServerInterceptor())
			gateway.Register(connect.Method{
				Spec: unarySpec,
				Handler: func(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
					var req wrapperspb.StringValue
					if err := stream.Receive(&req); err != nil {
						return err
					}
					res, err := callUnary(ctx, downstreamClient, req.GetValue())
					if err != nil {
						return fmt.Errorf("fetch: %w", err)
					}
					return stream.Send(res)
				},
			})

			// the in-process transport hands the downstream error to the
			// client as the cause of the internal error it receives
			client := newInProcessClient(gateway, NewClientInterceptor())
			_, err := callUnary(context.Background(), client, "hello")
			if got := connect.CodeOf(err); got != connect.CodeInternal {
				t.Fatalf("expected the client to receive internal, got %v", err)
			}

			spans := spansByOperation(mt)
			if len(spans["connect.server.request"]) != 1 || len(spans["connect.client.request"]) != 1 {
				t.Fatalf("expected 1 server span and 1 client span, got %v", mt.FinishedSpans())
			}
			serverSpan := spans["connect.server.request"][0]
			for _, span := range []*mocktracer.Span{serverSpan, spans["connect.client.request"][0]} {
				if got := span.Tag(tagCode); got != connect.CodeInternal.String() {
					t.Errorf("expected connect.code internal as the client receives on %s, got %v", span.OperationName(), got)
				}
				if got := span.Tag(ext.ErrorMsg); got == nil {
					t.Errorf("expected %s to be marked as an error", span.OperationName())
				}
			}
			// the message keeps the handler's wrapping and the downstream error
			if got, _ := serverSpan.Tag(ext.ErrorMsg).(string); !strings.Contains(got, "fetch: ") || !strings.Contains(got, "downstream") {
				t.Errorf("expected the error message to keep the handler's wrapping, got %q", got)
			}
		})
	}
}
