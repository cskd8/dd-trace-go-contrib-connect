package connect

import (
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

const (
	defaultServerServiceName = "connect.server"
	defaultClientServiceName = "connect.client"
)

// Option specifies a configuration option for the server and client interceptors. Not all
// options apply to both.
type Option func(*config)

type config struct {
	serviceName         func() string
	spanName            string
	nonErrorCodes       map[connect.Code]bool
	traceStreamCalls    bool
	traceStreamMessages bool
	noDebugStack        bool
	untracedMethods     map[string]struct{}
	withMetadataTags    bool
	ignoredMetadata     map[string]struct{}
	withRequestTags     bool
	spanOpts            []tracer.StartSpanOption
	tags                map[string]interface{}
}

func defaults(cfg *config) {
	cfg.traceStreamCalls = true
	cfg.traceStreamMessages = true
	cfg.nonErrorCodes = map[connect.Code]bool{connect.CodeCanceled: true}
	cfg.ignoredMetadata = map[string]struct{}{
		"x-datadog-trace-id":          {},
		"x-datadog-parent-id":         {},
		"x-datadog-sampling-priority": {},
		"x-datadog-tags":              {},
		"traceparent":                 {},
		"tracestate":                  {},
	}
}

func serverDefaults(cfg *config) {
	cfg.serviceName = func() string { return defaultServerServiceName }
	cfg.spanName = "connect.server.request"
	defaults(cfg)
}

func clientDefaults(cfg *config) {
	cfg.serviceName = func() string { return defaultClientServiceName }
	cfg.spanName = "connect.client.request"
	defaults(cfg)
}

// WithService sets the service name of the spans created by the interceptor.
func WithService(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = func() string { return name }
	}
}

// WithStreamCalls enables or disables the call spans of streaming RPCs on the server
// interceptor. Unary calls are always traced.
func WithStreamCalls(enabled bool) Option {
	return func(cfg *config) {
		cfg.traceStreamCalls = enabled
	}
}

// WithStreamMessages enables or disables the connect.message spans of the messages sent and
// received by streaming RPCs on the server interceptor.
func WithStreamMessages(enabled bool) Option {
	return func(cfg *config) {
		cfg.traceStreamMessages = enabled
	}
}

// NoDebugStack disables debug stacks for traces with errors. This is useful in situations
// where errors are frequent and the overhead of calling debug.Stack may affect performance.
func NoDebugStack() Option {
	return func(cfg *config) {
		cfg.noDebugStack = true
	}
}

// NonErrorCodes determines the list of codes which will not be considered errors in instrumentation.
// This call overrides the default handling of connect.CodeCanceled as a non-error.
func NonErrorCodes(cs ...connect.Code) Option {
	return func(cfg *config) {
		cfg.nonErrorCodes = make(map[connect.Code]bool, len(cs))
		for _, c := range cs {
			cfg.nonErrorCodes[c] = true
		}
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return func(cfg *config) {
		if on {
			WithSpanOptions(tracer.AnalyticsRate(1.0))(cfg)
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			WithSpanOptions(tracer.AnalyticsRate(rate))(cfg)
		}
	}
}

// WithUntracedMethods specifies full methods to be ignored by the server side and client
// side interceptors. When a request's full method is in ms, no spans will be created.
func WithUntracedMethods(ms ...string) Option {
	ums := make(map[string]struct{}, len(ms))
	for _, e := range ms {
		ums[e] = struct{}{}
	}
	return func(cfg *config) {
		cfg.untracedMethods = ums
	}
}

// WithMetadataTags specifies whether request headers (metadata) should be added
// to spans as connect.metadata.* tags.
func WithMetadataTags() Option {
	return func(cfg *config) {
		cfg.withMetadataTags = true
	}
}

// WithIgnoredMetadata specifies keys to be ignored while tracing the metadata. Keys are
// matched case-insensitively. Must be used in conjunction with WithMetadataTags.
func WithIgnoredMetadata(ms ...string) Option {
	return func(cfg *config) {
		for _, e := range ms {
			// withMetadataTags looks up the lowercased header key
			cfg.ignoredMetadata[strings.ToLower(e)] = struct{}{}
		}
	}
}

// WithRequestTags specifies whether request messages should be added to spans
// as the connect.request tag, serialized as JSON.
func WithRequestTags() Option {
	return func(cfg *config) {
		cfg.withRequestTags = true
	}
}

// WithCustomTag will attach the value to the span tagged by the key.
func WithCustomTag(key string, value interface{}) Option {
	return func(cfg *config) {
		if cfg.tags == nil {
			cfg.tags = make(map[string]interface{})
		}
		cfg.tags[key] = value
	}
}

// WithSpanOptions defines a set of additional tracer.StartSpanOption to be added
// to spans started by the integration.
func WithSpanOptions(opts ...tracer.StartSpanOption) Option {
	return func(cfg *config) {
		cfg.spanOpts = append(cfg.spanOpts, opts...)
	}
}
