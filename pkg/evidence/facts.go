package evidence

// The fact vocabularies: the optional details an observation payload can carry. The operator reference
// documents each value with its trailing comment (cmd/gendocs registry).

// Protocol is the wire protocol family a producer saw on an interface's port (InterfaceObservation.Protocol).
type Protocol string

const (
	ProtocolHTTP1 Protocol = "http1" // the port answered with an HTTP/1.x status line
	ProtocolHTTP2 Protocol = "http2" // the port answered with an HTTP/2 SETTINGS frame
	ProtocolTLS   Protocol = "tls"   // encrypted: the protocol inside cannot be read, so it proves nothing
	ProtocolOther Protocol = "other" // the port answered with bytes that are neither HTTP nor TLS
)

// Exposure is how far an interface is reachable from (InterfaceObservation.Exposure). The values mirror
// contract.VisibilityPublic/VisibilityInternal; how a platform maps its resources onto them is the
// producer's concern.
type Exposure string

const (
	ExposureExternal Exposure = "external" // reachable from outside the platform's private network
	ExposureInternal Exposure = "internal" // reachable from inside the platform's private network only
)
