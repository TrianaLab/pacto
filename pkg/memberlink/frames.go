// Package memberlink is the wire protocol between a hub and the member
// clusters that joined it. A member dials the hub over WebSocket, proves its
// approved installation key by signing a hub nonce and then answers one kind
// of question: may this person list this resource in this namespace. After
// the handshake only review and answer frames exist; anything else closes the
// channel.
package memberlink

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	"github.com/trianalab/pacto/v4/pkg/strictjson"
)

const (
	// Path is the hub route a member dials.
	Path = "/api/members/connect"
	// Subprotocol is the WebSocket subprotocol both sides require.
	Subprotocol = "pacto-member-link.v1"
	// MaxFrameBytes bounds every frame either side reads.
	MaxFrameBytes = 64 << 10
	// ReviewTimeout bounds one review, end to end.
	ReviewTimeout = 5 * time.Second
	// MaxTokenLifetime is the longest remaining lifetime a forwarded token
	// may have.
	MaxTokenLifetime = time.Hour
)

// Identity models a member answers under.
const (
	ModelSharedIssuer = "shared-issuer"
	ModelGroupMapping = "group-mapping"
	// ModelIssuerNotAccepted is the hub's mark for an issuer the member's
	// apiserver refuses; it never appears in an Answer.
	ModelIssuerNotAccepted = "issuer-not-accepted"
)

// Reasons a member refuses to answer.
const (
	RefusedIssuerNotAccepted = "issuer-not-accepted"
	RefusedTokenExpired      = "token-expired"
	RefusedClusterLocal      = "cluster-local-identity"
	RefusedMappingInvalid    = "mapping-invalid"
	RefusedReviewFailed      = "review-failed"
	RefusedNoIdentity        = "no-identity"
)

// Frame types.
const (
	frameChallenge, frameHello = "challenge", "hello"
	frameReview, frameAnswer   = "review", "answer"
)

// Challenge is the hub's nonce, sent first.
type Challenge struct {
	Type  string `json:"type"`
	Nonce string `json:"nonce"`
}

// Hello is the member's proof: the signature of SignedMessage by the key
// KeyID names.
type Hello struct {
	Type      string `json:"type"`
	Cluster   string `json:"cluster"`
	KeyID     string `json:"keyId"`
	Signature string `json:"signature"`
}

// Review asks whether one person may list Group/Resource in Namespace (""
// for every namespace). Exactly one of Token and Mapped is set. It names no
// verb: the member always reviews list.
type Review struct {
	Type      string  `json:"type"`
	ID        uint64  `json:"id"`
	Namespace string  `json:"namespace"`
	Group     string  `json:"group"`
	Resource  string  `json:"resource"`
	Token     string  `json:"token,omitempty"`
	Mapped    *Mapped `json:"mapped,omitempty"`
}

// Mapped is an identity the hub mapped through a person-approved group
// mapping record, whose digest is Record.
type Mapped struct {
	User   string   `json:"user"`
	Groups []string `json:"groups"`
	Record string   `json:"record"`
}

// Answer is the member's verdict for the review with the same ID.
type Answer struct {
	Type    string `json:"type"`
	ID      uint64 `json:"id"`
	Allowed bool   `json:"allowed"`
	Model   string `json:"model,omitempty"`
	Refused string `json:"refused,omitempty"`
}

// helloTimeout bounds the handshake on both sides.
var helloTimeout = 10 * time.Second

// pingInterval is how often a member sends a WebSocket Ping control frame,
// so a TLS-terminating proxy in front of the hub keeps the idle link open.
var pingInterval = 30 * time.Second

var errBinaryFrame = errors.New("binary frame")

// codec sends JSON text frames and receives text frames only.
var codec = websocket.Codec{
	Marshal: func(v any) ([]byte, byte, error) {
		b, err := json.Marshal(v)
		return b, websocket.TextFrame, err
	},
	Unmarshal: func(data []byte, payloadType byte, v any) error {
		if payloadType != websocket.TextFrame {
			return errBinaryFrame
		}
		*v.(*[]byte) = data
		return nil
	},
}

// readFrame reads one text frame of type want into dst, strictly: any other
// type, an unknown field, a binary frame or a frame over MaxFrameBytes is an
// error.
func readFrame(ws *websocket.Conn, want string, dst any) error {
	var raw []byte
	if err := codec.Receive(ws, &raw); err != nil {
		return err
	}
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return err
	}
	if head.Type != want {
		return fmt.Errorf("unexpected %.32q frame, want %q", head.Type, want)
	}
	return strictjson.Unmarshal(raw, dst)
}

// writer serializes the writes of one connection.
type writer struct {
	ws *websocket.Conn
	mu sync.Mutex
}

// send writes v as one text frame, bounded by ReviewTimeout.
func (w *writer) send(v any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.ws.SetWriteDeadline(time.Now().Add(ReviewTimeout))
	return codec.Send(w.ws, v)
}

// SignedMessage is what a member signs: the hub host it dialled, the hub's
// nonce and its own cluster UID.
func SignedMessage(host, nonce, cluster string) []byte {
	return []byte("pacto-member-link/v1\n" + host + "\n" + nonce + "\n" + cluster)
}
