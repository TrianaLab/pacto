package memberlink

import (
	"cmp"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

var quiet = slog.New(slog.DiscardHandler)

// approvals is an in-memory approved list a test can change.
type approvals struct {
	mu   sync.Mutex
	keys map[string]ed25519.PublicKey
	ids  map[string]string
}

func (a *approvals) set(uid string, pub ed25519.PublicKey, id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.keys[uid], a.ids[uid] = pub, id
}

func (a *approvals) revoke(uid string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.keys, uid)
}

func (a *approvals) approved(_ context.Context, uid string) (ed25519.PublicKey, string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	pub, ok := a.keys[uid]
	return pub, a.ids[uid], ok
}

// fixture is a TLS hub with one approved cluster, "c1".
type fixture struct {
	hub  *Hub
	srv  *httptest.Server
	host string
	app  *approvals
	pub  ed25519.PublicKey
	key  ed25519.PrivateKey
	now  time.Time
	mu   sync.Mutex
}

func (f *fixture) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func newFixture(t *testing.T, tune ...func(*httptest.Server)) *fixture {
	t.Helper()
	return newFixtureHost(t, "", tune...)
}

// newFixtureHost configures the hub with host, "" for the address members
// dial.
func newFixtureHost(t *testing.T, host string, tune ...func(*httptest.Server)) *fixture {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	f := &fixture{host: ln.Addr().String(), app: &approvals{keys: map[string]ed25519.PublicKey{}, ids: map[string]string{}},
		pub: pub, key: key, now: time.Unix(1_800_000_000, 0)}
	f.app.set("c1", pub, "kid-1")
	f.hub, err = NewHub(HubOptions{Host: cmp.Or(host, f.host), Approved: f.app.approved, Now: f.clock, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	f.srv = httptest.NewUnstartedServer(f.hub)
	_ = f.srv.Listener.Close()
	f.srv.Listener = ln
	for _, fn := range tune {
		fn(f.srv)
	}
	f.srv.StartTLS()
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fixture) roots() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(f.srv.Certificate())
	return p
}

func (f *fixture) url() string { return "https://" + f.host }

// dial opens a raw member-side socket to the hub.
func (f *fixture) dial(t *testing.T, edit ...func(*websocket.Config)) (*websocket.Conn, error) {
	t.Helper()
	loc, _ := url.Parse("wss://" + f.host + Path)
	origin, _ := url.Parse(f.url())
	cfg := &websocket.Config{Location: loc, Origin: origin, Version: websocket.ProtocolVersionHybi13,
		Protocol: []string{Subprotocol}, TlsConfig: f.srv.Client().Transport.(*http.Transport).TLSClientConfig, Header: http.Header{}}
	for _, fn := range edit {
		fn(cfg)
	}
	ws, err := websocket.DialConfig(cfg)
	if err == nil {
		t.Cleanup(func() { _ = ws.Close() })
	}
	return ws, err
}

// hello answers the challenge on ws, signing host as cluster uid with key.
func hello(t *testing.T, ws *websocket.Conn, host, uid, keyID string, key ed25519.PrivateKey) {
	t.Helper()
	var c Challenge
	if err := readFrame(ws, frameChallenge, &c); err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(key, SignedMessage(host, c.Nonce, uid))
	if err := codec.Send(ws, Hello{Type: frameHello, Cluster: uid, KeyID: keyID, Signature: base64.RawURLEncoding.EncodeToString(sig)}); err != nil {
		t.Fatal(err)
	}
}

// link opens a verified raw link for c1 and waits until it is the live one.
func (f *fixture) link(t *testing.T) *websocket.Conn {
	t.Helper()
	prev := f.current("c1")
	ws, err := f.dial(t)
	if err != nil {
		t.Fatal(err)
	}
	hello(t, ws, f.host, "c1", "kid-1", f.key)
	waitFor(t, func() bool { l := f.current("c1"); return l != nil && l != prev })
	return ws
}

func (f *fixture) current(uid string) *link {
	f.hub.mu.Lock()
	defer f.hub.mu.Unlock()
	return f.hub.links[uid]
}

// closed says whether the peer closed ws.
func closed(ws *websocket.Conn) bool {
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var raw []byte
	for {
		err := codec.Receive(ws, &raw)
		if err == io.EOF {
			return true
		}
		if err != nil {
			ne, ok := err.(net.Error)
			return !ok || !ne.Timeout()
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// member runs a member against f until the test ends.
func (f *fixture) member(t *testing.T, answer func(context.Context, Review) Answer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m := NewMember(MemberOptions{URL: f.url(), RootCAs: f.roots(), Cluster: "c1", KeyID: "kid-1", Key: f.key, Answer: answer, Logger: quiet})
	go func() { defer close(done); _ = m.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, func() bool { return len(f.hub.Live()) == 1 })
}

func shorten[T any](t *testing.T, v *T, to T) {
	old := *v
	*v = to
	t.Cleanup(func() { *v = old })
}
