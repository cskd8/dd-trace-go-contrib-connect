package connect

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// cache a constant option: saves one allocation per call
var spanTypeRPC = tracer.SpanType(ext.AppTypeRPC)

func (cfg *config) startSpanOptions(opts ...tracer.StartSpanOption) []tracer.StartSpanOption {
	if len(cfg.tags) == 0 && len(cfg.spanOpts) == 0 {
		return opts
	}

	ret := make([]tracer.StartSpanOption, 0, 1+len(cfg.tags)+len(opts))
	ret = append(ret, opts...)
	ret = append(ret, cfg.spanOpts...)
	for key, tag := range cfg.tags {
		ret = append(ret, tracer.Tag(key, tag))
	}
	return ret
}

// headerCarrier adapts a *connect.Header to the tracer's TextMapReader and
// TextMapWriter so span contexts can be extracted from and injected into RPC
// headers. A nil header carries nothing.
type headerCarrier struct {
	header *connect.Header
}

var (
	_ tracer.TextMapReader = headerCarrier{}
	_ tracer.TextMapWriter = headerCarrier{}
)

// ForeachKey implements tracer.TextMapReader.
func (c headerCarrier) ForeachKey(handler func(key, val string) error) error {
	if c.header == nil {
		return nil
	}
	for k, vals := range c.header.All() {
		for _, v := range vals {
			if err := handler(k, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// Set implements tracer.TextMapWriter.
func (c headerCarrier) Set(key, val string) {
	if c.header == nil {
		return
	}
	c.header.Set(key, val)
}

// requestHeader returns the request headers of the RPC described by info, or
// nil when there is no CallInfo (e.g. a ServerFunc invoked directly in tests).
func requestHeader(info *connect.CallInfo) *connect.Header {
	if info == nil {
		return nil
	}
	return info.RequestHeader()
}

// isUntraced reports whether no spans should be created for the procedure.
func (cfg *config) isUntraced(procedure string) bool {
	_, ok := cfg.untracedMethods[procedure]
	return ok
}

// methodKind returns the connect.method.kind tag value for the stream type.
func methodKind(streamType connect.StreamType) string {
	switch streamType {
	case connect.StreamTypeClient:
		return methodKindClientStream
	case connect.StreamTypeServer:
		return methodKindServerStream
	case connect.StreamTypeBidi:
		return methodKindBidiStream
	default:
		return methodKindUnary
	}
}

// startSpan starts a span for the given method. When extractParent is true,
// the parent span context is extracted from the incoming request headers
// (server side); client spans inherit their parent from ctx instead.
func startSpan(
	ctx context.Context,
	headers *connect.Header,
	method string,
	operation string,
	serviceFn func() string,
	extractParent bool,
	opts ...tracer.StartSpanOption,
) (*tracer.Span, context.Context) {
	// common stuff
	opts = append(opts,
		tracer.ServiceName(serviceFn()),
		tracer.ResourceName(method),
		tracer.Tag(tagMethodName, method),
		tracer.Tag(ext.Component, componentName),
	)

	// gRPC Spec
	methodElements := strings.SplitN(strings.TrimPrefix(method, "/"), "/", 2)
	opts = append(opts,
		spanTypeRPC,
		tracer.Tag(ext.RPCSystem, extRPCSystemConnect),
		tracer.Tag(ext.GRPCFullMethod, method),
		tracer.Tag(ext.RPCService, methodElements[0]),
	)
	if len(methodElements) > 1 {
		opts = append(opts, tracer.Tag(ext.RPCMethod, methodElements[1]))
	}

	// http Spec
	if extractParent {
		if sctx, err := tracer.Extract(headerCarrier{header: headers}); err == nil {
			opts = append(opts, tracer.ChildOf(sctx)) //nolint:staticcheck // SA1019: tracer.ChildOf is deprecated, but kept for compatibility
		}
	}
	return tracer.StartSpanFromContext(ctx, operation, opts...)
}

// withMetadataTags tags the span with the request headers, except for the
// ones in cfg.ignoredMetadata, when the WithMetadataTags option is enabled.
func withMetadataTags(cfg *config, headers *connect.Header, span *tracer.Span) {
	if !cfg.withMetadataTags || headers == nil {
		return
	}
	for k, v := range headers.All() {
		k = strings.ToLower(k)
		if _, ok := cfg.ignoredMetadata[k]; ok {
			continue
		}
		// gRPC binary metadata keys end in "-bin"; skip them for parity with
		// the dd-trace-go gRPC integration.
		if strings.HasSuffix(k, "-bin") {
			continue
		}
		span.SetTag(tagMetadataPrefix+k, v)
	}
}

// withRequestTags tags the span with the request message serialized as JSON
// when the WithRequestTags option is enabled.
func withRequestTags(cfg *config, req any, span *tracer.Span) {
	if !cfg.withRequestTags {
		return
	}
	if p, ok := req.(proto.Message); ok {
		if b, err := protojson.Marshal(p); err == nil {
			span.SetTag(tagRequest, string(b))
		}
	}
}

// withPeerTags tags the span with the RPC protocol (connect, grpc or grpcweb)
// and the peer address: the client address on server spans, the server host
// on client spans.
func withPeerTags(info *connect.CallInfo, span *tracer.Span) {
	if info == nil {
		return
	}
	if info.Protocol != "" {
		span.SetTag(tagProtocol, info.Protocol)
	}
	if info.PeerAddr != "" {
		span.SetTag(tagPeerAddr, info.PeerAddr)
	}
}

// finishWithError finishes a message span, disregarding OK, EOF and Canceled
// errors: a stream reports its clean end with an error wrapping io.EOF (coded
// unknown by connecthttp).
func finishWithError(span *tracer.Span, err error, cfg *config) {
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		err = nil
	}
	var code connect.Code
	if err != nil {
		code = connect.CodeOf(err)
	}
	finishSpan(span, err, code, cfg)
}

// finishCallWithError finishes the span of a call, the result of a server
// handler or of a unary client call, with the code the client receives for
// err: the code of a *connect.Error, even when its cause wraps io.EOF or
// context.Canceled, and otherwise the code connect-go sends for an uncoded
// error. A not-modified error (connecthttp.NewNotModifiedError) is a success
// whatever the protocol, like otelconnect, although connect-go answers it with
// HTTP 304 only to a Connect GET request and sends it to other requests as an
// error.
func finishCallWithError(span *tracer.Span, err error, cfg *config) {
	if connecthttp.IsNotModifiedError(err) {
		err = nil
	}
	var code connect.Code
	if err != nil {
		code = errorCode(err)
	}
	finishSpan(span, err, code, cfg)
}

// errorCode returns the code connect-go sends for err: the code of the first
// *connect.Error in its chain, canceled or deadline_exceeded for an uncoded
// context error, and unknown for any other error.
func errorCode(err error) connect.Code {
	if cerr, ok := errors.AsType[*connect.Error](err); ok && cerr != nil {
		return cerr.Code()
	}
	switch {
	case errors.Is(err, context.Canceled):
		return connect.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return connect.CodeDeadlineExceeded
	default:
		return connect.CodeUnknown
	}
}

// finishSpan tags the span with code, or ok when err is nil, and finishes it
// as failed unless code is one of the non-error codes.
func finishSpan(span *tracer.Span, err error, code connect.Code, cfg *config) {
	codeTag := codeOK
	if err != nil {
		if cfg.nonErrorCodes[code] {
			err = nil
		}
		codeTag = code.String()
	}
	span.SetTag(tagCode, codeTag)

	// only allocate finishOptions if needed, and allocate the exact right size
	var finishOptions []tracer.FinishOption
	if err != nil {
		if cfg.noDebugStack {
			finishOptions = []tracer.FinishOption{tracer.WithError(err), tracer.NoDebugStack()}
		} else {
			finishOptions = []tracer.FinishOption{tracer.WithError(err)}
		}
	}
	span.Finish(finishOptions...)
}
