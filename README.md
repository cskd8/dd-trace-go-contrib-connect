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

Like [otelconnect](https://github.com/connectrpc/otelconnect-go), the trace
context is written into the request headers of the call's `CallInfo` and stays
there after the call. connect-go keeps those headers when a context from
`connect.NewClientContext` is reused, so a later call on that context that
writes no trace context of its own sends the previous call's trace headers.
Over `connecthttp` its server span becomes a child of the previous call; over
`connectinprocess` that only happens when the caller has no active span, since
the server then runs on the caller's context and takes the parent from it. This
happens for:

- a streaming call when the context has no active span
- a unary method excluded with `WithUntracedMethods` (or `WithIgnoredMethods`)

A traced unary call, or a streaming call with an active span, overwrites the
trace and parent IDs, but keys the new trace context does not set stay as they
were: `x-datadog-origin` (so an unrelated trace can be marked as coming from
synthetics), and the `baggage` / `ot-baggage-*` of the previous trace, which
may contain user data. These leftover keys also show up as `connect.metadata.*`
tags on later client spans and on the server spans when the client or server
interceptor uses `WithMetadataTags()`.

To avoid this, create a new context with `connect.NewClientContext` for each
call, or delete the trace headers from `CallInfo.RequestHeader()` before
reusing it.

### Migrating from v1

The options are unchanged. `NewServerInterceptor` and `NewClientInterceptor`
now return `connect.ServerInterceptor` and `connect.ClientInterceptor`, which
are passed to `connect.NewServer` and `connect.NewClient` instead of
`connect.WithInterceptors`.

connect-go v2 runs server interceptors before the request is unmarshaled, so
the `connect.request` tag of `WithRequestTags()` is set once the handler
successfully receives the request. A call that fails or is rejected before
that, for example by an interceptor placed after the tracing interceptor or by
a validation error while receiving, has no `connect.request` tag; in v1 every
traced unary call had it.

A handler that returns an error it received from another RPC sends it to the
client as `internal` in connect-go v2, and the server span records `internal`
too. In v1 the downstream code was sent to the client and recorded as is.

The `connect.code` of a call span (the server span of a handler and the client
span of a unary call) is the code the client receives: a `*connect.Error` keeps
its code even when its cause wraps `io.EOF` or `context.Canceled`, an uncoded
`context.Canceled` / `context.DeadlineExceeded` becomes `canceled` /
`deadline_exceeded`, and any other uncoded error `unknown`. In v1 any error
wrapping `io.EOF` or `context.Canceled` was recorded as `ok` and an uncoded
`context.DeadlineExceeded` as `unknown`; a unary call canceled by the client is
now recorded as `canceled` (still not an error by default). Message spans of
streaming calls still treat `io.EOF` and `context.Canceled` as `ok`, since a
stream reports its clean end that way.

An uncoded `os.ErrDeadlineExceeded` (returned by some dial and I/O timeouts) is
recorded as `deadline_exceeded`, as `connecthttp` sends it. `connectinprocess`
sends it to the client as `unknown`, so over the in-process transport the
server span of a handler returning it says `deadline_exceeded` while the client
and its span say `unknown`.

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
