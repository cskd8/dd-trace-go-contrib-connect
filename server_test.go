package connect

import (
	"context"
	"testing"

	"connectrpc.com/connect"
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
			opts: []Option{WithServiceName("test-service")},
		},
		{
			name: "with multiple options",
			opts: []Option{
				WithServiceName("test-service"),
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

			// Verify it implements the interface
			var _ connect.Interceptor = interceptor
		})
	}
}

func TestServerInterceptor_WrapUnary(t *testing.T) {
	tests := []struct {
		name            string
		ignoredMethods  []string
		untracedMethods []string
	}{
		{
			name:           "with ignored methods",
			ignoredMethods: []string{"/test.Service/IgnoredMethod"},
		},
		{
			name:            "with untraced methods",
			untracedMethods: []string{"/test.Service/UntracedMethod"},
		},
		{
			name: "default config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var options []Option
			if len(tt.ignoredMethods) > 0 {
				options = append(options, WithIgnoredMethods(tt.ignoredMethods...))
			}
			if len(tt.untracedMethods) > 0 {
				options = append(options, WithUntracedMethods(tt.untracedMethods...))
			}

			interceptor := NewServerInterceptor(options...).(*serverInterceptor)

			// Test that the wrapped function is created without error
			unaryFunc := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				return nil, nil
			}

			wrappedFunc := interceptor.WrapUnary(unaryFunc)
			if wrappedFunc == nil {
				t.Error("expected wrapped function to be created")
			}
		})
	}
}

func TestServerInterceptor_WrapStreamingHandler(t *testing.T) {
	tests := []struct {
		name           string
		traceStream    bool
		ignoredMethods []string
	}{
		{
			name:        "stream tracing enabled",
			traceStream: true,
		},
		{
			name:        "stream tracing disabled",
			traceStream: false,
		},
		{
			name:           "with ignored methods",
			traceStream:    true,
			ignoredMethods: []string{"/test.Service/IgnoredStream"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var options []Option
			options = append(options, WithStreamCalls(tt.traceStream))
			if len(tt.ignoredMethods) > 0 {
				options = append(options, WithIgnoredMethods(tt.ignoredMethods...))
			}

			interceptor := NewServerInterceptor(options...).(*serverInterceptor)

			handlerFunc := func(ctx context.Context, conn connect.StreamingHandlerConn) error {
				return nil
			}

			wrappedHandler := interceptor.WrapStreamingHandler(handlerFunc)
			if wrappedHandler == nil {
				t.Error("expected wrapped handler to be created")
			}
		})
	}
}

func TestServerInterceptor_WrapStreamingClient(t *testing.T) {
	interceptor := NewServerInterceptor().(*serverInterceptor)

	// Mock client function
	clientFunc := func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		return nil
	}

	// Should return the same function (no wrapping for client)
	wrappedClient := interceptor.WrapStreamingClient(clientFunc)

	if wrappedClient == nil {
		t.Error("expected wrapped client function to be returned")
	}
}

func TestWrappedStreamingHandlerConn_Creation(t *testing.T) {
	cfg := &config{}
	serverDefaults(cfg)

	// Test that wrappedStreamingHandlerConn can be created
	wrapped := &wrappedStreamingHandlerConn{
		cfg: cfg,
		ctx: context.Background(),
	}

	if wrapped.cfg == nil {
		t.Error("expected cfg to be set")
	}

	if wrapped.ctx == nil {
		t.Error("expected ctx to be set")
	}
}
