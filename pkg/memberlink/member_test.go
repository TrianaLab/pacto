package memberlink

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/sha1" //nolint:gosec // the WebSocket accept key is defined over SHA-1
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// fakeHub is a TLS WebSocket server that runs serve for each member.
func fakeHub(t *testing.T, serve func(ws *websocket.Conn)) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	srv := httptest.NewTLSServer(websocket.Server{
		Handshake: func(*websocket.Config, *http.Request) error { return nil },
		Handler:   func(ws *websocket.Conn) { serve(ws) },
	})
	t.Cleanup(srv.Close)
	p := x509.NewCertPool()
	p.AddCert(srv.Certificate())
	return srv, p
}

// challenge sends a challenge and reads the hello.
func challenge(ws *websocket.Conn) (Hello, error) {
	_ = codec.Send(ws, Challenge{Type: frameChallenge, Nonce: "nonce"})
	var h Hello
	err := readFrame(ws, frameHello, &h)
	return h, err
}

func newTestMember(url string, roots *x509.CertPool, answer func(context.Context, Review) Answer) *Member {
	_, key, _ := ed25519.GenerateKey(nil)
	return NewMember(MemberOptions{URL: url, RootCAs: roots, Cluster: "c1", KeyID: "kid-1", Key: key, Answer: answer, Logger: quiet})
}

// runSession runs one member session until the test ends.
func runSession(t *testing.T, m *Member, raw string) {
	t.Helper()
	u := hubURL(t, raw)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _, _ = m.session(ctx, u) }()
	t.Cleanup(func() { cancel(); <-done })
}

func hubURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := ParseHubURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestMemberRefusesPlainHTTPURL(t *testing.T) {
	for _, bad := range []string{"http://hub.example", "ws://hub.example", "https://", "::"} {
		if _, err := ParseHubURL(bad); err == nil {
			t.Errorf("ParseHubURL(%q) accepted", bad)
		}
	}
	if err := NewMember(MemberOptions{URL: "http://hub.example"}).Run(context.Background()); err == nil {
		t.Fatal("a member ran against a plain HTTP hub")
	}
	for raw, want := range map[string]string{
		"https://hub.example":         "wss://hub.example" + Path,
		"wss://hub.example:8443/":     "wss://hub.example:8443" + Path,
		"https://hub.example/pacto/":  "wss://hub.example/pacto" + Path,
		"https://hub.example/pacto//": "wss://hub.example/pacto/" + Path,
	} {
		if got := hubURL(t, raw).String(); got != want {
			t.Errorf("ParseHubURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestMemberSignsDialledHost(t *testing.T) {
	f := newFixture(t)
	f.member(t, func(context.Context, Review) Answer { return Answer{} })
}

func TestMemberVerifiesHubCertificate(t *testing.T) {
	srv, roots := fakeHub(t, func(ws *websocket.Conn) { _, _ = challenge(ws) })
	m := newTestMember(srv.URL, nil, nil)
	if _, err := m.session(context.Background(), hubURL(t, srv.URL)); err == nil {
		t.Fatal("a hub with an unknown CA was trusted")
	}
	m = newTestMember(srv.URL, roots, nil)
	wrongName := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	if _, err := m.session(context.Background(), hubURL(t, wrongName)); err == nil {
		t.Fatal("a certificate for another name was trusted")
	}
}

func TestMemberDialHonoursContext(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		var held []net.Conn
		defer func() {
			for _, c := range held {
				_ = c.Close()
			}
		}()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			held = append(held, c)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	m := newTestMember("https://"+ln.Addr().String(), nil, nil)
	if _, err := m.session(ctx, hubURL(t, "https://"+ln.Addr().String())); err == nil {
		t.Fatal("a hung TLS handshake succeeded")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancelling the context did not stop the dial")
	}
}

func unexpectedFrameClosesMemberChannel(t *testing.T) {
	for name, frame := range unexpectedFrames(frameReview) {
		t.Run(name, func(t *testing.T) {
			srv, roots := fakeHub(t, func(ws *websocket.Conn) {
				if _, err := challenge(ws); err != nil {
					return
				}
				_ = websocket.Message.Send(ws, frame)
				_ = readFrame(ws, frameAnswer, &Answer{})
			})
			m := newTestMember(srv.URL, roots, func(context.Context, Review) Answer { return Answer{} })
			linked, err := m.session(context.Background(), hubURL(t, srv.URL))
			if !linked || err == nil {
				t.Fatalf("linked = %v, err = %v", linked, err)
			}
		})
	}
}

func TestMemberRefusesBadChallenge(t *testing.T) {
	srv, roots := fakeHub(t, func(ws *websocket.Conn) { _ = websocket.Message.Send(ws, `{"type":"review","id":1}`) })
	m := newTestMember(srv.URL, roots, nil)
	if linked, err := m.session(context.Background(), hubURL(t, srv.URL)); linked || err == nil {
		t.Fatalf("linked = %v, err = %v", linked, err)
	}
}

// reviewHub completes a handshake, then sends each review and collects
// every answer until the member has answered n.
func reviewHub(t *testing.T, reviews []Review, n int) (*httptest.Server, *x509.CertPool, chan []Answer) {
	t.Helper()
	out := make(chan []Answer, 1)
	srv, roots := fakeHub(t, func(ws *websocket.Conn) {
		if _, err := challenge(ws); err != nil {
			return
		}
		for _, r := range reviews {
			r.Type = frameReview
			_ = codec.Send(ws, r)
		}
		var got []Answer
		for len(got) < n {
			var a Answer
			if readFrame(ws, frameAnswer, &a) != nil {
				break
			}
			got = append(got, a)
		}
		out <- got
	})
	return srv, roots, out
}

func TestMemberRefusesUnlistedResourceFrame(t *testing.T) {
	var called atomic.Bool
	srv, roots, out := reviewHub(t, []Review{{ID: 7, Group: "", Resource: "secrets", Token: "t"}}, 1)
	m := newTestMember(srv.URL, roots, func(context.Context, Review) Answer { called.Store(true); return Answer{Allowed: true} })
	runSession(t, m, srv.URL)
	got := <-out
	if len(got) != 1 || got[0].ID != 7 || got[0].Allowed || got[0].Refused != RefusedReviewFailed || called.Load() {
		t.Fatalf("answers = %+v, Answer called = %v", got, called.Load())
	}
}

func TestMemberBoundsInFlight(t *testing.T) {
	reviews := make([]Review, maxInFlight+1)
	for i := range reviews {
		reviews[i] = Review{ID: uint64(i + 1), Group: "apps", Resource: "deployments"}
	}
	release := make(chan struct{})
	srv, roots, out := reviewHub(t, reviews, maxInFlight+1)
	m := newTestMember(srv.URL, roots, func(context.Context, Review) Answer { <-release; return Answer{Allowed: true} })
	runSession(t, m, srv.URL)
	time.AfterFunc(200*time.Millisecond, func() { close(release) })
	got := <-out
	if len(got) != maxInFlight+1 || got[0].ID != maxInFlight+1 || got[0].Refused != RefusedReviewFailed {
		t.Fatalf("the review over the bound was not refused first: %+v", got)
	}
	for _, a := range got[1:] {
		if !a.Allowed {
			t.Fatalf("a review within the bound was not answered: %+v", a)
		}
	}
}

func TestMemberAnswerAfterDropClosesQuietly(t *testing.T) {
	answered := make(chan struct{})
	srv, roots, out := reviewHub(t, []Review{{ID: 1, Group: "apps", Resource: "deployments"}}, 0)
	m := newTestMember(srv.URL, roots, func(ctx context.Context, _ Review) Answer {
		<-ctx.Done()
		defer close(answered)
		return Answer{}
	})
	done := make(chan struct{})
	go func() { defer close(done); _, _ = m.session(context.Background(), hubURL(t, srv.URL)) }()
	<-out
	<-done
	<-answered
	time.Sleep(50 * time.Millisecond) // the late answer's failed send closes the socket
}

func TestPingIntervalIs30s(t *testing.T) {
	if pingInterval != 30*time.Second {
		t.Fatalf("pingInterval = %v", pingInterval)
	}
}

// rawHub is a hand-rolled WebSocket server that reports the opcode of every
// frame the member sends, Ping control frames included.
func rawHub(t *testing.T, opcodes chan<- byte) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11")) //nolint:gosec // RFC 6455
		conn, rw, _ := w.(http.Hijacker).Hijack()
		defer func() { _ = conn.Close() }()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Protocol: " + Subprotocol + "\r\nSec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n")
		msg := `{"type":"challenge","nonce":"n"}`
		_, _ = rw.Write(append([]byte{0x81, byte(len(msg))}, msg...))
		_ = rw.Flush()
		for {
			op, err := readRawFrame(rw.Reader)
			if err != nil {
				return
			}
			opcodes <- op
		}
	}))
	t.Cleanup(srv.Close)
	p := x509.NewCertPool()
	p.AddCert(srv.Certificate())
	return srv, p
}

// readRawFrame reads one masked client frame and returns its opcode.
func readRawFrame(r *bufio.Reader) (byte, error) {
	var head [2]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return 0, err
	}
	n := uint64(head[1] & 0x7f)
	switch n {
	case 126:
		var ext [2]byte
		_, _ = io.ReadFull(r, ext[:])
		n = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		_, _ = io.ReadFull(r, ext[:])
		n = binary.BigEndian.Uint64(ext[:])
	}
	if _, err := io.CopyN(io.Discard, r, int64(n)+4); err != nil {
		return 0, err
	}
	return head[0] & 0x0f, nil
}

func TestMemberPingsEvery30s(t *testing.T) {
	shorten(t, &pingInterval, 20*time.Millisecond)
	opcodes := make(chan byte, 64)
	srv, roots := rawHub(t, opcodes)
	m := newTestMember(srv.URL, roots, nil)
	runSession(t, m, srv.URL)
	if op := <-opcodes; op != websocket.TextFrame {
		t.Fatalf("first frame opcode = %d, want the hello", op)
	}
	for range 2 {
		select {
		case op := <-opcodes:
			if op != websocket.PingFrame {
				t.Fatalf("opcode = %d, want a Ping", op)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no Ping frame")
		}
	}
}

func TestFailedPingDropsLink(t *testing.T) {
	srv, _ := fakeHub(t, func(*websocket.Conn) {})
	u := hubURL(t, srv.URL)
	origin := *u
	origin.Scheme = "https"
	ws, err := (&websocket.Config{Location: u, Origin: &origin, Version: websocket.ProtocolVersionHybi13,
		TlsConfig: srv.Client().Transport.(*http.Transport).TLSClientConfig}).DialContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = ws.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		newTestMember(srv.URL, nil, nil).ping(context.Background(), &writer{ws: ws}, time.Millisecond)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a failed ping did not end the pinger")
	}
}

// logRecorder keeps the messages a member logs.
type logRecorder struct {
	mu      sync.Mutex
	msgs    []string
	reasons []string
}

func (l *logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (l *logRecorder) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "reason" {
			l.reasons = append(l.reasons, a.Value.String())
		}
		return true
	})
	l.msgs = append(l.msgs, r.Message)
	return nil
}
func (l *logRecorder) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logRecorder) WithGroup(string) slog.Handler      { return l }
func (l *logRecorder) count(msg string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, m := range l.msgs {
		if m == msg {
			n++
		}
	}
	return n
}

func TestMemberReconnectsWithBackoff(t *testing.T) {
	shorten(t, &backoffBase, time.Millisecond)
	shorten(t, &backoffMax, 5*time.Millisecond)
	var dials atomic.Int32
	srv, roots := fakeHub(t, func(ws *websocket.Conn) {
		if dials.Add(1)%2 == 0 {
			_, _ = challenge(ws)
		}
	})
	rec := &logRecorder{}
	m := newTestMember(srv.URL, roots, nil)
	m.opts.Logger = slog.New(rec)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- m.Run(ctx) }()
	waitFor(t, func() bool { return dials.Load() >= 6 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if rec.count("member link up") == 0 || rec.count("member link down") == 0 {
		t.Fatalf("logs = %v", rec.msgs)
	}
}

func TestMemberLogsEachStateOnce(t *testing.T) {
	shorten(t, &backoffBase, time.Millisecond)
	shorten(t, &backoffMax, time.Millisecond)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	rec := &logRecorder{}
	m := newTestMember("https://"+addr, nil, nil)
	m.opts.Logger = slog.New(rec)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	_ = m.Run(ctx)
	if n := rec.count("member link down"); n != 1 {
		t.Fatalf("the same failure was logged %d times: %q", n, rec.reasons)
	}
}

func TestMemberStopsDuringBackoff(t *testing.T) {
	shorten(t, &backoffBase, time.Hour)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	rec := &logRecorder{}
	m := newTestMember("https://"+addr, nil, nil)
	m.opts.Logger = slog.New(rec)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- m.Run(ctx) }()
	waitFor(t, func() bool { return rec.count("member link down") == 1 })
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run kept waiting after its context ended")
	}
}

func TestBackoffBounds(t *testing.T) {
	if backoffBase != time.Second || backoffMax != 60*time.Second {
		t.Fatalf("backoff = %v..%v", backoffBase, backoffMax)
	}
	within := func(d, around time.Duration) bool {
		return d >= around*8/10 && d <= around*12/10
	}
	for range 50 {
		if d := backoff(0); !within(d, time.Second) {
			t.Fatalf("backoff(0) = %v", d)
		}
		if d := backoff(3); !within(d, 8*time.Second) {
			t.Fatalf("backoff(3) = %v", d)
		}
		for _, n := range []int{6, 29, 30, 1000} {
			if d := backoff(n); !within(d, time.Minute) {
				t.Fatalf("backoff(%d) = %v", n, d)
			}
		}
	}
}

func TestNewMemberDefaultsLogger(t *testing.T) {
	if NewMember(MemberOptions{}).opts.Logger == nil {
		t.Fatal("no logger")
	}
}
