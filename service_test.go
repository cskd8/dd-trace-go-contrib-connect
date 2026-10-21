package connect

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var (
	unarySpec = connect.Spec{
		Procedure:  "/test.v1.TestService/Unary",
		StreamType: connect.StreamTypeUnary,
	}
	serverStreamSpec = connect.Spec{
		Procedure:  "/test.v1.TestService/ServerStream",
		StreamType: connect.StreamTypeServer,
	}
)

// testService serves unarySpec and serverStreamSpec. Unary echoes the request
// value, or fails with err when it is set. ServerStream sends the request
// value twice. Both record the request headers they receive.
type testService struct {
	err error

	mu     sync.Mutex
	header map[string]string
}

func (s *testService) recordHeader(ctx context.Context) {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.header = make(map[string]string)
	for k := range info.RequestHeader().All() {
		s.header[k] = info.RequestHeader().Get(k)
	}
}

// requestHeader returns the value of the request header key seen by the last call.
func (s *testService) requestHeader(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.header[key]
}

func (s *testService) newServer(interceptors ...connect.ServerInterceptor) *connect.Server {
	server := connect.NewServer(interceptors...)
	server.Register(
		connect.Method{
			Spec: unarySpec,
			Handler: func(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
				s.recordHeader(ctx)
				var req wrapperspb.StringValue
				if err := stream.Receive(&req); err != nil {
					return err
				}
				if s.err != nil {
					return s.err
				}
				return stream.Send(wrapperspb.String(req.GetValue()))
			},
		},
		connect.Method{
			Spec: serverStreamSpec,
			Handler: func(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
				s.recordHeader(ctx)
				var req wrapperspb.StringValue
				if err := stream.Receive(&req); err != nil {
					return err
				}
				for range 2 {
					if err := stream.Send(wrapperspb.String(req.GetValue())); err != nil {
						return err
					}
				}
				return nil
			},
		},
	)
	return server
}

// newInProcessClient returns a client calling server in-process.
func newInProcessClient(server *connect.Server, interceptors ...connect.ClientInterceptor) *connect.Client {
	return connect.NewClient(connectinprocess.New(server), interceptors...)
}

func callUnary(ctx context.Context, client *connect.Client, value string) (*wrapperspb.StringValue, error) {
	res := &wrapperspb.StringValue{}
	if err := client.CallUnary(ctx, unarySpec, wrapperspb.String(value), res); err != nil {
		return nil, err
	}
	return res, nil
}

// callServerStream calls serverStreamSpec and returns the number of received messages.
func callServerStream(ctx context.Context, client *connect.Client, value string) (int, error) {
	stream, err := client.CallServerStream(ctx, serverStreamSpec, wrapperspb.String(value))
	if err != nil {
		return 0, err
	}
	defer func() { _ = stream.Close() }()
	var n int
	for {
		var res wrapperspb.StringValue
		if err := stream.Receive(&res); err != nil {
			if errors.Is(err, io.EOF) {
				return n, nil
			}
			return n, err
		}
		n++
	}
}

// spansByOperation groups the finished spans by operation name.
func spansByOperation(mt mocktracer.Tracer) map[string][]*mocktracer.Span {
	spans := make(map[string][]*mocktracer.Span)
	for _, span := range mt.FinishedSpans() {
		spans[span.OperationName()] = append(spans[span.OperationName()], span)
	}
	return spans
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
