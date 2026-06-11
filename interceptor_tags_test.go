package connect

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestServerInterceptorSpanTags(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	interceptor := NewServerInterceptor(WithMetadataTags(), WithRequestTags())
	next := connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&wrapperspb.StringValue{Value: "world"}), nil
	})

	req := connect.NewRequest(&wrapperspb.StringValue{Value: "hello"})
	req.Header().Set("X-Custom-Header", "abc")
	req.Header().Set("X-Datadog-Trace-Id", "123")
	req.Header().Set("X-Test-Bin", "binary")

	if _, err := interceptor.WrapUnary(next)(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := mt.FinishedSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	span := spans[0]

	if got := span.Tag(ext.Component); got != "connectrpc.com/connect" {
		t.Errorf("expected component tag, got %v", got)
	}
	if got := span.Tag(ext.RPCSystem); got != "connect" {
		t.Errorf("expected rpc.system connect, got %v", got)
	}
	if got := span.Tag(ext.SpanKind); got != ext.SpanKindServer {
		t.Errorf("expected span.kind server, got %v", got)
	}
	if got := span.Tag(tagCode); got != codeOK {
		t.Errorf("expected connect.code %q, got %v", codeOK, got)
	}
	if got := span.Tag(tagMethodKind); got != methodKindUnary {
		t.Errorf("expected connect.method.kind unary, got %v", got)
	}
	// slice tag values are flattened by the tracer into indexed keys (key.0, key.1, ...)
	if got := span.Tag(tagMetadataPrefix + "x-custom-header.0"); got != "abc" {
		t.Errorf("expected connect.metadata.x-custom-header.0 tag to be set, got %v", got)
	}
	if got := span.Tag(tagMetadataPrefix + "x-datadog-trace-id.0"); got != nil {
		t.Errorf("expected x-datadog-trace-id to be ignored, got %v", got)
	}
	if got := span.Tag(tagMetadataPrefix + "x-test-bin.0"); got != nil {
		t.Errorf("expected -bin metadata to be skipped, got %v", got)
	}
	reqTag, _ := span.Tag(tagRequest).(string)
	if !strings.Contains(reqTag, "hello") {
		t.Errorf("expected connect.request tag to contain the message, got %q", reqTag)
	}
}

func TestServerInterceptorSpanTagsDisabledByDefault(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	interceptor := NewServerInterceptor()
	next := connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&wrapperspb.StringValue{}), nil
	})

	req := connect.NewRequest(&wrapperspb.StringValue{Value: "hello"})
	req.Header().Set("X-Custom-Header", "abc")

	if _, err := interceptor.WrapUnary(next)(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	span := mt.FinishedSpans()[0]
	if got := span.Tag(tagMetadataPrefix + "x-custom-header"); got != nil {
		t.Errorf("expected no metadata tags by default, got %v", got)
	}
	if got := span.Tag(tagRequest); got != nil {
		t.Errorf("expected no request tag by default, got %v", got)
	}
}

func TestServerInterceptorErrorCode(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	interceptor := NewServerInterceptor()
	next := connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, connect.NewError(connect.CodeInternal, errors.New("boom"))
	})

	req := connect.NewRequest(&wrapperspb.StringValue{})
	if _, err := interceptor.WrapUnary(next)(context.Background(), req); err == nil {
		t.Fatal("expected error")
	}

	span := mt.FinishedSpans()[0]
	if got := span.Tag(tagCode); got != connect.CodeInternal.String() {
		t.Errorf("expected connect.code internal, got %v", got)
	}
}

func TestClientInterceptorSpanTagsAndPropagation(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	interceptor := NewClientInterceptor(WithRequestTags())
	var propagated bool
	next := connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		propagated = req.Header().Get("X-Datadog-Trace-Id") != ""
		return connect.NewResponse(&wrapperspb.StringValue{}), nil
	})

	req := connect.NewRequest(&wrapperspb.StringValue{Value: "hello"})
	if _, err := interceptor.WrapUnary(next)(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !propagated {
		t.Error("expected trace context to be injected into request headers")
	}

	span := mt.FinishedSpans()[0]
	if got := span.OperationName(); got != "connect.client.request" {
		t.Errorf("expected operation name connect.client.request, got %v", got)
	}
	if got := span.Tag(ext.SpanKind); got != ext.SpanKindClient {
		t.Errorf("expected span.kind client, got %v", got)
	}
	if got := span.Tag(ext.ServiceName); got != defaultClientServiceName {
		t.Errorf("expected service %q, got %v", defaultClientServiceName, got)
	}
	if got := span.Tag(tagCode); got != codeOK {
		t.Errorf("expected connect.code ok, got %v", got)
	}
	reqTag, _ := span.Tag(tagRequest).(string)
	if !strings.Contains(reqTag, "hello") {
		t.Errorf("expected connect.request tag to contain the message, got %q", reqTag)
	}
}
