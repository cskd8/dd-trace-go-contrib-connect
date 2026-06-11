package connect

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"connectrpc.com/connect"
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

// startSpan starts a span for the given method. When extractParent is true,
// the parent span context is extracted from the incoming request headers
// (server side); client spans inherit their parent from ctx instead.
func startSpan(
	ctx context.Context,
	headers http.Header,
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
		if sctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(headers)); err == nil {
			opts = append(opts, tracer.ChildOf(sctx)) //nolint:staticcheck // SA1019: tracer.ChildOf is deprecated, but kept for compatibility
		}
	}
	return tracer.StartSpanFromContext(ctx, operation, opts...)
}

// withMetadataTags tags the span with the request headers, except for the
// ones in cfg.ignoredMetadata, when the WithMetadataTags option is enabled.
func withMetadataTags(cfg *config, headers http.Header, span *tracer.Span) {
	if !cfg.withMetadataTags {
		return
	}
	for k, v := range headers {
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
func withPeerTags(peer connect.Peer, span *tracer.Span) {
	if peer.Protocol != "" {
		span.SetTag(tagProtocol, peer.Protocol)
	}
	if peer.Addr != "" {
		span.SetTag(tagPeerAddr, peer.Addr)
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
