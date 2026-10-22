package connect

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectinprocess"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var (
	unarySpec = connect.Spec{
		Procedure:  "/test.v1.TestService/Unary",
		StreamType: connect.StreamTypeUnary,
	}
	clientStreamSpec = connect.Spec{
		Procedure:  "/test.v1.TestService/ClientStream",
		StreamType: connect.StreamTypeClient,
	}
	serverStreamSpec = connect.Spec{
		Procedure:  "/test.v1.TestService/ServerStream",
		StreamType: connect.StreamTypeServer,
	}
)

// testService serves unarySpec, clientStreamSpec and serverStreamSpec. Unary
// echoes the request value, or fails with err when it is set. ClientStream
// replies with the number of messages it received. ServerStream sends the
// request value twice. All record the request headers they receive.
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
			Spec: clientStreamSpec,
			Handler: func(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
				s.recordHeader(ctx)
				var n int
				for {
					var req wrapperspb.StringValue
					if err := stream.Receive(&req); err != nil {
						if errors.Is(err, io.EOF) {
							break
						}
						return err
					}
					n++
				}
				return stream.Send(wrapperspb.Int64(int64(n)))
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

// newHTTPServer serves server over HTTP. Close it when done.
func newHTTPServer(server *connect.Server) *httptest.Server {
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	return httptest.NewServer(mux)
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

// callClientStream calls clientStreamSpec with values and returns the number
// of messages the server received.
func callClientStream(ctx context.Context, client *connect.Client, values ...string) (int64, error) {
	stream, err := client.CallClientStream(ctx, clientStreamSpec)
	if err != nil {
		return 0, err
	}
	defer func() { _ = stream.Close() }()
	for _, value := range values {
		if err := stream.Send(wrapperspb.String(value)); err != nil {
			return 0, err
		}
	}
	if err := stream.CloseSend(); err != nil {
		return 0, err
	}
	var res wrapperspb.Int64Value
	if err := stream.Receive(&res); err != nil {
		return 0, err
	}
	return res.GetValue(), nil
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
