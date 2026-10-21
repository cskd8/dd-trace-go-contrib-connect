package connect

import (
	"context"
	"errors"
	"io"
	"net/textproto"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
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

// headerCarrier adapts a *connect.Header to the tracer's TextMapReader so span
// contexts can be extracted from RPC headers. A nil header carries nothing.
type headerCarrier struct {
	header *connect.Header
}

var _ tracer.TextMapReader = headerCarrier{}

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

// injectCarrier is a tracer.TextMapWriter over a *connect.Header which
// remembers the previous values of the keys it writes so they can be restored.
type injectCarrier struct {
	header *connect.Header
	// prev holds the values each written key had before, nil if it was unset.
	prev map[string][]string
}

var _ tracer.TextMapWriter = (*injectCarrier)(nil)

// Set implements tracer.TextMapWriter.
func (c *injectCarrier) Set(key, val string) {
	key = textproto.CanonicalMIMEHeaderKey(key)
	if _, ok := c.prev[key]; !ok {
		c.prev[key] = slices.Clone(c.header.Values(key))
	}
	c.header.Set(key, val)
}

// injectSpanContext injects sctx into the client request headers and returns
// a function which restores the headers it wrote. The request headers belong
// to the caller's CallInfo, which connect-go keeps when a context from
// connect.NewClientContext is reused for later calls, so the injected trace
// context must not outlive the call. Restore once the headers have been sent.
func injectSpanContext(sctx *tracer.SpanContext, header *connect.Header) (restore func()) {
	if header == nil {
		return func() {}
	}
	c := &injectCarrier{header: header, prev: make(map[string][]string)}
	_ = tracer.Inject(sctx, c)
	return func() {
		for key, vals := range c.prev {
			if len(vals) == 0 {
				header.Delete(key)
			} else {
				header.SetValues(key, vals)
			}
		}
	}
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
	_, im := cfg.ignoredMethods[procedure]
	_, um := cfg.untracedMethods[procedure]
	return im || um
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

// finishWithError applies finish option and a tag with gRPC status code, disregarding OK, EOF and Canceled errors.
func finishWithError(span *tracer.Span, err error, cfg *config) {
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		err = nil
	}
	code := codeOK
	if err != nil {
		errcode := connect.CodeOf(err)
		if cfg.nonErrorCodes[errcode] {
			err = nil
		}
		code = errcode.String()
	}
	span.SetTag(tagCode, code)

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
