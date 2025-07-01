package connect

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func TestDefaults(t *testing.T) {
	cfg := &config{}
	defaults(cfg)

	if !cfg.traceStreamCalls {
		t.Error("expected traceStreamCalls to be true by default")
	}

	if !cfg.traceStreamMessages {
		t.Error("expected traceStreamMessages to be true by default")
	}

	if !cfg.nonErrorCodes[connect.CodeCanceled] {
		t.Error("expected CodeCanceled to be in nonErrorCodes by default")
	}

	if cfg.ignoredMetadata == nil {
		t.Error("expected ignoredMetadata to be initialized")
	}

	expectedIgnored := []string{
		"x-datadog-trace-id",
		"x-datadog-parent-id",
		"x-datadog-sampling-priority",
	}

	for _, key := range expectedIgnored {
		if _, exists := cfg.ignoredMetadata[key]; !exists {
			t.Errorf("expected %s to be in ignoredMetadata", key)
		}
	}
}

func TestServerDefaults(t *testing.T) {
	cfg := &config{}
	serverDefaults(cfg)

	if cfg.serviceName == nil {
		t.Error("expected serviceName to be set")
	}

	if cfg.serviceName() != defaultServerServiceName {
		t.Errorf("expected serviceName to be %s, got %s", defaultServerServiceName, cfg.serviceName())
	}

	if cfg.spanName != "connect.server.request" {
		t.Errorf("expected spanName to be connect.server.request, got %s", cfg.spanName)
	}

	// Check that defaults are also applied
	if !cfg.traceStreamCalls {
		t.Error("expected traceStreamCalls to be true after serverDefaults")
	}
}

func TestWithServiceName(t *testing.T) {
	cfg := &config{}
	option := WithServiceName("test-service")
	option(cfg)

	if cfg.serviceName == nil {
		t.Error("expected serviceName to be set")
	}

	if cfg.serviceName() != "test-service" {
		t.Errorf("expected serviceName to be test-service, got %s", cfg.serviceName())
	}
}

func TestWithStreamCalls(t *testing.T) {
	tests := []struct {
		name     string
		enabled  bool
		expected bool
	}{
		{
			name:     "enable stream calls",
			enabled:  true,
			expected: true,
		},
		{
			name:     "disable stream calls",
			enabled:  false,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config{}
			option := WithStreamCalls(tt.enabled)
			option(cfg)

			if cfg.traceStreamCalls != tt.expected {
				t.Errorf("expected traceStreamCalls to be %v, got %v", tt.expected, cfg.traceStreamCalls)
			}
		})
	}
}

func TestWithStreamMessages(t *testing.T) {
	tests := []struct {
		name     string
		enabled  bool
		expected bool
	}{
		{
			name:     "enable stream messages",
			enabled:  true,
			expected: true,
		},
		{
			name:     "disable stream messages",
			enabled:  false,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config{}
			option := WithStreamMessages(tt.enabled)
			option(cfg)

			if cfg.traceStreamMessages != tt.expected {
				t.Errorf("expected traceStreamMessages to be %v, got %v", tt.expected, cfg.traceStreamMessages)
			}
		})
	}
}

func TestNoDebugStack(t *testing.T) {
	cfg := &config{}
	option := NoDebugStack()
	option(cfg)

	if !cfg.noDebugStack {
		t.Error("expected noDebugStack to be true")
	}
}

func TestNonErrorCodes(t *testing.T) {
	codes := []connect.Code{connect.CodeUnavailable, connect.CodeInternal}
	cfg := &config{}
	option := NonErrorCodes(codes...)
	option(cfg)

	if len(cfg.nonErrorCodes) != len(codes) {
		t.Errorf("expected %d non-error codes, got %d", len(codes), len(cfg.nonErrorCodes))
	}

	for _, code := range codes {
		if !cfg.nonErrorCodes[code] {
			t.Errorf("expected %v to be in nonErrorCodes", code)
		}
	}

	// Check that default codes are overridden
	if cfg.nonErrorCodes[connect.CodeCanceled] {
		t.Error("expected CodeCanceled to be overridden and not present")
	}
}

func TestWithAnalytics(t *testing.T) {
	t.Run("enable analytics", func(t *testing.T) {
		cfg := &config{}
		option := WithAnalytics(true)
		option(cfg)

		if len(cfg.spanOpts) == 0 {
			t.Error("expected span options to be added when analytics is enabled")
		}
	})

	t.Run("disable analytics", func(t *testing.T) {
		cfg := &config{}
		option := WithAnalytics(false)
		option(cfg)

		// Should not add any span options
		if len(cfg.spanOpts) != 0 {
			t.Error("expected no span options when analytics is disabled")
		}
	})
}

func TestWithAnalyticsRate(t *testing.T) {
	tests := []struct {
		name          string
		rate          float64
		shouldAddOpts bool
	}{
		{
			name:          "valid rate 0.5",
			rate:          0.5,
			shouldAddOpts: true,
		},
		{
			name:          "valid rate 0.0",
			rate:          0.0,
			shouldAddOpts: true,
		},
		{
			name:          "valid rate 1.0",
			rate:          1.0,
			shouldAddOpts: true,
		},
		{
			name:          "invalid rate -0.1",
			rate:          -0.1,
			shouldAddOpts: false,
		},
		{
			name:          "invalid rate 1.1",
			rate:          1.1,
			shouldAddOpts: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config{}
			option := WithAnalyticsRate(tt.rate)
			option(cfg)

			if tt.shouldAddOpts {
				if len(cfg.spanOpts) == 0 {
					t.Error("expected span options to be added for valid rate")
				}
			} else {
				if len(cfg.spanOpts) != 0 {
					t.Error("expected no span options for invalid rate")
				}
			}
		})
	}
}

func TestWithIgnoredMethods(t *testing.T) {
	methods := []string{"/test.Service/Method1", "/test.Service/Method2"}
	cfg := &config{}
	option := WithIgnoredMethods(methods...)
	option(cfg)

	if len(cfg.ignoredMethods) != len(methods) {
		t.Errorf("expected %d ignored methods, got %d", len(methods), len(cfg.ignoredMethods))
	}

	for _, method := range methods {
		if _, exists := cfg.ignoredMethods[method]; !exists {
			t.Errorf("expected %s to be in ignoredMethods", method)
		}
	}
}

func TestWithUntracedMethods(t *testing.T) {
	methods := []string{"/test.Service/Method1", "/test.Service/Method2"}
	cfg := &config{}
	option := WithUntracedMethods(methods...)
	option(cfg)

	if len(cfg.untracedMethods) != len(methods) {
		t.Errorf("expected %d untraced methods, got %d", len(methods), len(cfg.untracedMethods))
	}

	for _, method := range methods {
		if _, exists := cfg.untracedMethods[method]; !exists {
			t.Errorf("expected %s to be in untracedMethods", method)
		}
	}
}

func TestWithMetadataTags(t *testing.T) {
	cfg := &config{}
	option := WithMetadataTags()
	option(cfg)

	if !cfg.withMetadataTags {
		t.Error("expected withMetadataTags to be true")
	}
}

func TestWithIgnoredMetadata(t *testing.T) {
	metadata := []string{"custom-header-1", "custom-header-2"}
	cfg := &config{}
	// Initialize ignoredMetadata map first
	cfg.ignoredMetadata = make(map[string]struct{})

	option := WithIgnoredMetadata(metadata...)
	option(cfg)

	for _, key := range metadata {
		if _, exists := cfg.ignoredMetadata[key]; !exists {
			t.Errorf("expected %s to be in ignoredMetadata", key)
		}
	}
}

func TestWithRequestTags(t *testing.T) {
	cfg := &config{}
	option := WithRequestTags()
	option(cfg)

	if !cfg.withRequestTags {
		t.Error("expected withRequestTags to be true")
	}
}

func TestWithCustomTag(t *testing.T) {
	cfg := &config{}
	option := WithCustomTag("test-key", "test-value")
	option(cfg)

	if cfg.tags == nil {
		t.Error("expected tags map to be initialized")
	}

	if value, exists := cfg.tags["test-key"]; !exists {
		t.Error("expected test-key to be in tags")
	} else if value != "test-value" {
		t.Errorf("expected test-key value to be test-value, got %v", value)
	}
}

func TestWithSpanOptions(t *testing.T) {
	cfg := &config{}
	opts := []tracer.StartSpanOption{
		tracer.ServiceName("test"),
		tracer.ResourceName("resource"),
	}
	option := WithSpanOptions(opts...)
	option(cfg)

	if len(cfg.spanOpts) != len(opts) {
		t.Errorf("expected %d span options, got %d", len(opts), len(cfg.spanOpts))
	}
}

func TestMultipleOptions(t *testing.T) {
	cfg := &config{}

	// Apply multiple options
	WithServiceName("multi-test")(cfg)
	WithStreamCalls(false)(cfg)
	WithCustomTag("env", "test")(cfg)
	NoDebugStack()(cfg)

	// Verify all options were applied
	if cfg.serviceName() != "multi-test" {
		t.Errorf("expected serviceName to be multi-test, got %s", cfg.serviceName())
	}

	if cfg.traceStreamCalls {
		t.Error("expected traceStreamCalls to be false")
	}

	if !cfg.noDebugStack {
		t.Error("expected noDebugStack to be true")
	}

	if cfg.tags["env"] != "test" {
		t.Errorf("expected env tag to be test, got %v", cfg.tags["env"])
	}
}
