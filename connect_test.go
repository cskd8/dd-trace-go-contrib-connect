package connect

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func TestStartSpanOptions(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *config
		inputOpts   []tracer.StartSpanOption
		expectedLen int
	}{
		{
			name:        "empty config",
			cfg:         &config{},
			inputOpts:   []tracer.StartSpanOption{tracer.ServiceName("test")},
			expectedLen: 1,
		},
		{
			name: "config with tags",
			cfg: &config{
				tags: map[string]interface{}{
					"tag1": "value1",
					"tag2": "value2",
				},
			},
			inputOpts:   []tracer.StartSpanOption{tracer.ServiceName("test")},
			expectedLen: 3, // 1 input + 2 tags
		},
		{
			name: "config with span options",
			cfg: &config{
				spanOpts: []tracer.StartSpanOption{
					tracer.Tag("span_tag", "span_value"),
				},
			},
			inputOpts:   []tracer.StartSpanOption{tracer.ServiceName("test")},
			expectedLen: 2, // 1 input + 1 span option
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.cfg.startSpanOptions(tt.inputOpts...)
			if len(result) != tt.expectedLen {
				t.Errorf("expected %d options, got %d", tt.expectedLen, len(result))
			}
		})
	}
}

func TestStartSpan(t *testing.T) {
	// Start tracer for testing
	if err := tracer.Start(); err != nil {
		t.Fatalf("Failed to start tracer: %v", err)
	}
	defer tracer.Stop()

	ctx := context.Background()
	headers := &connect.Header{}
	method := "/test.Service/TestMethod"
	operation := "test.operation"
	serviceFn := func() string { return "test-service" }

	span, newCtx := startSpan(ctx, headers, method, operation, serviceFn, true)

	if span == nil {
		t.Error("expected span to be created")
	}

	if newCtx == nil {
		t.Error("expected context to be returned")
	}

	// Clean up
	if span != nil {
		span.Finish()
	}
}

func TestFinishWithError(t *testing.T) {
	// Start tracer for testing
	if err := tracer.Start(); err != nil {
		t.Fatalf("Failed to start tracer: %v", err)
	}
	defer tracer.Stop()

	tests := []struct {
		name string
		err  error
		cfg  *config
	}{
		{
			name: "no error",
			err:  nil,
			cfg:  &config{},
		},
		{
			name: "context canceled error",
			err:  context.Canceled,
			cfg:  &config{},
		},
		{
			name: "connect error",
			err:  connect.NewError(connect.CodeInternal, "internal error"),
			cfg:  &config{},
		},
		{
			name: "non-error code",
			err:  connect.NewError(connect.CodeUnavailable, "unavailable"),
			cfg: &config{
				nonErrorCodes: map[connect.Code]bool{
					connect.CodeUnavailable: true,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test span
			span, _ := tracer.StartSpanFromContext(context.Background(), "test")

			// This should not panic
			finishWithError(span, tt.err, tt.cfg)
		})
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := &config{}

	// Test that startSpanOptions works with default config
	opts := cfg.startSpanOptions()

	// With empty config and no input options, should return empty slice
	if len(opts) != 0 {
		t.Errorf("expected empty options slice, got %d options", len(opts))
	}
}

func TestHeaderCarrier(t *testing.T) {
	header := &connect.Header{}
	carrier := headerCarrier{header: header}
	carrier.Set("x-datadog-trace-id", "123")
	header.Add("X-Multi", "a")
	header.Add("X-Multi", "b")

	if got := header.Get("X-Datadog-Trace-Id"); got != "123" {
		t.Errorf("expected Set to write the header, got %q", got)
	}

	got := map[string][]string{}
	if err := carrier.ForeachKey(func(key, val string) error {
		got[key] = append(got[key], val)
		return nil
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got["X-Datadog-Trace-Id"]) != 1 || len(got["X-Multi"]) != 2 {
		t.Errorf("expected every value to be visited, got %v", got)
	}

	errStop := errors.New("stop")
	if err := carrier.ForeachKey(func(string, string) error { return errStop }); !errors.Is(err, errStop) {
		t.Errorf("expected the handler error to be returned, got %v", err)
	}
}

func TestHeaderCarrierNil(t *testing.T) {
	carrier := headerCarrier{}
	carrier.Set("x-datadog-trace-id", "123")
	if err := carrier.ForeachKey(func(string, string) error {
		t.Error("expected no keys")
		return nil
	}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMethodKind(t *testing.T) {
	tests := []struct {
		streamType connect.StreamType
		expected   string
	}{
		{connect.StreamTypeUnary, methodKindUnary},
		{connect.StreamTypeClient, methodKindClientStream},
		{connect.StreamTypeServer, methodKindServerStream},
		{connect.StreamTypeBidi, methodKindBidiStream},
	}
	for _, tt := range tests {
		if got := methodKind(tt.streamType); got != tt.expected {
			t.Errorf("methodKind(%v) = %q, expected %q", tt.streamType, got, tt.expected)
		}
	}
}
