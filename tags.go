package connect

const (
	tagMethodName     = "connect.method.name"
	tagMethodKind     = "connect.method.kind"
	tagCode           = "connect.code"
	tagMetadataPrefix = "connect.metadata."
	tagRequest        = "connect.request"
	tagProtocol       = "connect.protocol"
	tagPeerAddr       = "connect.peer.addr"
)

const (
	methodKindUnary        = "unary"
	methodKindClientStream = "client_streaming"
	methodKindServerStream = "server_streaming"
	methodKindBidiStream   = "bidi_streaming"
)

const (
	extRPCSystemConnect = "connect"

	// componentName identifies this integration in the ext.Component tag.
	componentName = "connectrpc.com/connect"

	// codeOK is the connect.code tag value for successful calls. connect-go
	// has no explicit OK code, so this mirrors gRPC's codes.OK in connect's
	// lowercase code style.
	codeOK = "ok"
)
