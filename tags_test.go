package connect

import "testing"

func TestTagConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{
			name:     "tagMethodName",
			constant: tagMethodName,
			expected: "connect.method.name",
		},
		{
			name:     "tagMethodKind",
			constant: tagMethodKind,
			expected: "connect.method.kind",
		},
		{
			name:     "tagCode",
			constant: tagCode,
			expected: "connect.code",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("expected %s to be %s, got %s", tt.name, tt.expected, tt.constant)
			}
		})
	}
}

func TestMethodKindConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{
			name:     "methodKindUnary",
			constant: methodKindUnary,
			expected: "unary",
		},
		{
			name:     "methodKindClientStream",
			constant: methodKindClientStream,
			expected: "client_streaming",
		},
		{
			name:     "methodKindServerStream",
			constant: methodKindServerStream,
			expected: "server_streaming",
		},
		{
			name:     "methodKindBidiStream",
			constant: methodKindBidiStream,
			expected: "bidi_streaming",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("expected %s to be %s, got %s", tt.name, tt.expected, tt.constant)
			}
		})
	}
}

func TestExtRPCSystemConstant(t *testing.T) {
	if extRPCSystemConnect != "connect" {
		t.Errorf("expected extRPCSystemConnect to be connect, got %s", extRPCSystemConnect)
	}
}

func TestDefaultServerServiceName(t *testing.T) {
	if defaultServerServiceName != "connect.server" {
		t.Errorf("expected defaultServerServiceName to be connect.server, got %s", defaultServerServiceName)
	}
}
