# dd-trace-go-contrib-connect

Datadog APM tracing interceptors for [connect-go](https://connectrpc.com/).

## Usage

```go
import (
	"connectrpc.com/connect"
	connecttrace "github.com/cskd8/dd-trace-go-contrib-connect"
)

// Server side
path, handler := examplev1connect.NewExampleServiceHandler(svc, connect.WithInterceptors(
	connecttrace.NewServerInterceptor(connecttrace.WithService("my-service")),
))

// Client side
client := examplev1connect.NewExampleServiceClient(httpClient, baseURL, connect.WithInterceptors(
	connecttrace.NewClientInterceptor(connecttrace.WithService("my-service")),
))
```

The client interceptor traces unary calls and injects the trace context into the
request headers. For streaming client calls it only propagates the trace context.

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
