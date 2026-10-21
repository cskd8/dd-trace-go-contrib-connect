# dd-trace-go-contrib-connect

Datadog APM tracing interceptors for [connect-go](https://connectrpc.com/).

| Module | connect-go | Branch |
|--------|------------|--------|
| `github.com/cskd8/dd-trace-go-contrib-connect/v2` | `connectrpc.com/connect/v2` | `main` |
| `github.com/cskd8/dd-trace-go-contrib-connect` | `connectrpc.com/connect` (v1) | [`v1`](https://github.com/cskd8/dd-trace-go-contrib-connect/tree/v1) |

## Usage

```go
import (
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	connecttrace "github.com/cskd8/dd-trace-go-contrib-connect/v2"
)

// Server side
server := connect.NewServer(
	connecttrace.NewServerInterceptor(connecttrace.WithService("my-service")),
)
examplev1connect.RegisterExampleServiceHandler(server, svc)
connecthttp.Mount(mux, server)

// Client side
client := examplev1connect.NewExampleServiceClient(connect.NewClient(
	connecthttp.NewTransport(httpClient, baseURL),
	connecttrace.NewClientInterceptor(connecttrace.WithService("my-service")),
))
```

The server interceptor always traces unary calls; streaming calls and their
messages are traced according to `WithStreamCalls` and `WithStreamMessages`.
The client interceptor traces unary calls and injects the trace context into the
request headers. For streaming client calls it only propagates the trace context.

### Migrating from v1

The options are unchanged. `NewServerInterceptor` and `NewClientInterceptor`
now return `connect.ServerInterceptor` and `connect.ClientInterceptor`, which
are passed to `connect.NewServer` and `connect.NewClient` instead of
`connect.WithInterceptors`.

connect-go v2 runs server interceptors before the request is unmarshaled, so
the `connect.request` tag of `WithRequestTags()` is set when the handler
receives the request rather than when the span starts. The finished spans carry
the same tags as in v1.

## Span tags

Tags set on every span:

| Tag | Example |
|-----|---------|
| `connect.method.name` | `/example.v1.ExampleService/Get` |
| `connect.method.kind` | `unary` |
| `connect.code` | `ok`, `internal`, ... |
| `connect.protocol` | `connect`, `grpc`, `grpcweb` |
| `connect.peer.addr` | client address (server spans) / server host (client spans) |
| `component` | `connectrpc.com/connect` |
| `rpc.system` | `connect` |
| `rpc.service` | `example.v1.ExampleService` |
| `rpc.method` | `Get` |
| `rpc.grpc.full_method` | `/example.v1.ExampleService/Get` |
| `span.kind` | `server` / `client` |

Opt-in tags:

- `WithMetadataTags()` — request headers as `connect.metadata.*` tags
  (propagation headers are excluded by default; add more exclusions with
  `WithIgnoredMetadata(...)`)
- `WithRequestTags()` — the request message serialized as JSON in the
  `connect.request` tag

Note: request messages and headers may contain sensitive or high-cardinality
data. Prefer enabling these options selectively, or tag specific fields
yourself via `tracer.SpanFromContext` in your handler.

## License

This project is licensed under the BSD-3-Clause License - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

This project is a fork and continuation of the original dd-trace-go-contrib-connect project by Coxwave. We thank the original author for their foundational work.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
