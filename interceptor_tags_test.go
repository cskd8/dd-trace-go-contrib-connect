package connect

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

func TestServerInterceptorSpanTags(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svc := &testService{}
	client := newInProcessClient(svc.newServer(NewServerInterceptor(WithMetadataTags(), WithRequestTags())))

	ctx, info := connect.NewClientContext(context.Background())
	info.RequestHeader().Set("X-Custom-Header", "abc")
	info.RequestHeader().Set("X-Datadog-Trace-Id", "123")
	info.RequestHeader().Set("X-Test-Bin", "binary")
	_, err := callUnary(ctx, client, "hello")
	requireNoError(t, err)

	spans := mt.FinishedSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	span := spans[0]

	if got := span.OperationName(); got != "connect.server.request" {
		t.Errorf("expected operation name connect.server.request, got %v", got)
	}
	if got := span.Tag(ext.ResourceName); got != unarySpec.Procedure {
		t.Errorf("expected resource %q, got %v", unarySpec.Procedure, got)
	}
	if got := span.Tag(ext.Component); got != "connectrpc.com/connect" {
		t.Errorf("expected component tag, got %v", got)
	}
	if got := span.Tag(ext.RPCSystem); got != "connect" {
		t.Errorf("expected rpc.system connect, got %v", got)
	}
	if got := span.Tag(ext.RPCService); got != "test.v1.TestService" {
		t.Errorf("expected rpc.service test.v1.TestService, got %v", got)
	}
	if got := span.Tag(ext.RPCMethod); got != "Unary" {
		t.Errorf("expected rpc.method Unary, got %v", got)
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

	svc := &testService{}
	client := newInProcessClient(svc.newServer(NewServerInterceptor()))

	ctx, info := connect.NewClientContext(context.Background())
	info.RequestHeader().Set("X-Custom-Header", "abc")
	_, err := callUnary(ctx, client, "hello")
	requireNoError(t, err)

	span := mt.FinishedSpans()[0]
	if got := span.Tag(tagMetadataPrefix + "x-custom-header.0"); got != nil {
		t.Errorf("expected no metadata tags by default, got %v", got)
	}
	if got := span.Tag(tagRequest); got != nil {
		t.Errorf("expected no request tag by default, got %v", got)
	}
}

func TestServerInterceptorErrorCode(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svc := &testService{err: connect.NewError(connect.CodeInternal, "boom")}
	client := newInProcessClient(svc.newServer(NewServerInterceptor()))

	if _, err := callUnary(context.Background(), client, "hello"); err == nil {
		t.Fatal("expected error")
	}

	span := mt.FinishedSpans()[0]
	if got := span.Tag(tagCode); got != connect.CodeInternal.String() {
		t.Errorf("expected connect.code internal, got %v", got)
	}
	if got := span.Tag(ext.ErrorMsg); got == nil {
		t.Error("expected the span to be marked as an error")
	}
}

func TestClientInterceptorSpanTagsAndPropagation(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svc := &testService{}
	client := newInProcessClient(svc.newServer(), NewClientInterceptor(WithRequestTags()))

	_, err := callUnary(context.Background(), client, "hello")
	requireNoError(t, err)

	if svc.requestHeader("X-Datadog-Trace-Id") == "" {
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
	if got := span.Tag(tagMethodKind); got != methodKindUnary {
		t.Errorf("expected connect.method.kind unary, got %v", got)
	}
	reqTag, _ := span.Tag(tagRequest).(string)
	if !strings.Contains(reqTag, "hello") {
		t.Errorf("expected connect.request tag to contain the message, got %q", reqTag)
	}
}

func TestInterceptorsOverHTTP(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svc := &testService{}
	httpServer := newHTTPServer(svc.newServer(NewServerInterceptor(WithService("test-server"))))
	defer httpServer.Close()

	client := connect.NewClient(
		connecthttp.NewTransport(httpServer.Client(), httpServer.URL),
		NewClientInterceptor(WithService("test-client")),
	)
	_, err := callUnary(context.Background(), client, "hello")
	requireNoError(t, err)

	spans := spansByOperation(mt)
	if len(spans["connect.server.request"]) != 1 || len(spans["connect.client.request"]) != 1 {
		t.Fatalf("expected 1 server span and 1 client span, got %v", mt.FinishedSpans())
	}
	serverSpan := spans["connect.server.request"][0]
	clientSpan := spans["connect.client.request"][0]

	if serverSpan.TraceID() != clientSpan.TraceID() {
		t.Errorf("expected the server span to continue the client trace")
	}
	if serverSpan.ParentID() != clientSpan.SpanID() {
		t.Errorf("expected the server span to be a child of the client span")
	}
	for _, span := range []*mocktracer.Span{serverSpan, clientSpan} {
		if got := span.Tag(tagProtocol); got != connect.ProtocolNameConnect {
			t.Errorf("expected connect.protocol %q on %s, got %v", connect.ProtocolNameConnect, span.OperationName(), got)
		}
		if got, _ := span.Tag(tagPeerAddr).(string); got == "" {
			t.Errorf("expected connect.peer.addr on %s", span.OperationName())
		}
	}
	if got := serverSpan.Tag(ext.ServiceName); got != "test-server" {
		t.Errorf("expected server service test-server, got %v", got)
	}
	if got := clientSpan.Tag(ext.ServiceName); got != "test-client" {
		t.Errorf("expected client service test-client, got %v", got)
	}
}

func TestServerInterceptorIgnoredMetadataCase(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svc := &testService{}
	client := newInProcessClient(svc.newServer(NewServerInterceptor(WithMetadataTags(), WithIgnoredMetadata("X-Secret"))))

	ctx, info := connect.NewClientContext(context.Background())
	info.RequestHeader().Set("X-Secret", "s")
	_, err := callUnary(ctx, client, "hello")
	requireNoError(t, err)

	if got := mt.FinishedSpans()[0].Tag(tagMetadataPrefix + "x-secret.0"); got != nil {
		t.Errorf("expected X-Secret to be ignored, got %v", got)
	}
}

func TestServerInterceptorClientStreamRequestTags(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svc := &testService{}
	client := newInProcessClient(svc.newServer(NewServerInterceptor(WithRequestTags())))

	n, err := callClientStream(context.Background(), client, "a", "b")
	requireNoError(t, err)
	if n != 2 {
		t.Fatalf("expected the server to receive 2 messages, got %d", n)
	}

	messages := spansByOperation(mt)["connect.message"]
	var tagged int
	for _, span := range messages {
		if span.Tag(tagRequest) != nil {
			tagged++
		}
	}
	// the handler receives 2 messages and then the end of the stream, and
	// sends 1 response; only the received messages are tagged
	if len(messages) != 4 {
		t.Fatalf("expected 4 message spans, got %d", len(messages))
	}
	if tagged != 2 {
		t.Errorf("expected 2 message spans with the request tag, got %d", tagged)
	}
}
