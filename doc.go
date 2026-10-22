// Package connect provides Datadog APM tracing interceptors for connect-go v2
// (connectrpc.com/connect/v2). The package name is the same as connect-go's,
// so import it under another name such as connecttrace:
//
//	server := connect.NewServer(
//		connecttrace.NewServerInterceptor(connecttrace.WithService("my-service")),
//	)
//	client := connect.NewClient(
//		connecthttp.NewTransport(httpClient, baseURL),
//		connecttrace.NewClientInterceptor(connecttrace.WithService("my-service")),
//	)
//
// The server interceptor continues the trace context sent by the client and
// traces every unary call; streaming calls and their messages are traced
// according to WithStreamCalls and WithStreamMessages (both on by default).
// The client interceptor traces unary calls and writes the trace context into
// the request headers of the call's connect.CallInfo; for streaming calls it
// only propagates the trace context.
//
// For connect-go v1 (connectrpc.com/connect), use the
// github.com/cskd8/dd-trace-go-contrib-connect module, maintained on the v1
// branch.
package connect
