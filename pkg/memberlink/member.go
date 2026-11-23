package memberlink

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

// Reconnect backoff: doubling from backoffBase up to backoffMax, each wait
// within 20% jitter.
var (
	backoffBase = time.Second
	backoffMax  = 60 * time.Second
)

// maxInFlight bounds the reviews a member answers at once; one more is
// refused.
const maxInFlight = 16

// MemberOptions configures a Member.
type MemberOptions struct {
	// URL is the hub, https:// or wss://.
	URL string
	// RootCAs verifies the hub's certificate; nil is the system pool.
	RootCAs *x509.CertPool
	// Cluster is this cluster's UID and KeyID the ID of Key, its approved
	// installation key.
	Cluster, KeyID string
	Key            ed25519.PrivateKey
	// Answer reviews one request in this cluster.
	Answer func(context.Context, Review) Answer
	Logger *slog.Logger
}

// Member is the client half: it keeps one link to the hub open and answers
// the hub's reviews.
type Member struct {
	opts MemberOptions
	last string
}

// NewMember returns a member for opts.
func NewMember(opts MemberOptions) *Member {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Member{opts: opts}
}

// ParseHubURL reads a hub URL: https:// or wss:// only, as the wss:// URL
// of Path.
func ParseHubURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "wss") || u.Host == "" {
		return nil, errors.New("the hub URL must be an https:// or wss:// URL")
	}
	u.Scheme = "wss"
	u.Path = strings.TrimSuffix(u.Path, "/") + Path
	return u, nil
}

// Run keeps the link open until ctx ends, reconnecting with backoff. It
// returns an error only for an invalid hub URL.
func (m *Member) Run(ctx context.Context) error {
	u, err := ParseHubURL(m.opts.URL)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		linked, err := m.session(ctx, u)
		if ctx.Err() != nil {
			return nil
		}
		if linked {
			attempt = 0
		}
		m.note("member link down", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff(attempt)):
		}
	}
}

// note logs a link state once per change.
func (m *Member) note(msg string, err error) {
	if s := err.Error(); s != m.last {
		m.last = s
		m.opts.Logger.Warn(msg, "hub", m.opts.URL, "reason", s)
	}
}

// backoff is the wait before reconnect attempt n.
func backoff(n int) time.Duration {
	d := backoffMax
	if n < 30 {
		d = min(backoffBase<<n, backoffMax)
	}
	return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
}

// session dials the hub, proves the key and answers reviews until the link
// fails; linked says whether the handshake completed.
func (m *Member) session(ctx context.Context, u *url.URL) (linked bool, err error) {
	origin := *u
	origin.Scheme = "https"
	cfg := &websocket.Config{
		Location: u, Origin: &origin, Version: websocket.ProtocolVersionHybi13,
		Protocol:  []string{Subprotocol},
		TlsConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: m.opts.RootCAs},
	}
	ws, err := cfg.DialContext(ctx)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(ctx, func() { _ = ws.Close() })()
	defer func() { _ = ws.Close() }()
	ws.MaxPayloadBytes = MaxFrameBytes
	w := &writer{ws: ws}
	if err := m.hello(w, u.Host); err != nil {
		return false, err
	}
	m.last = ""
	m.opts.Logger.Info("member link up", "hub", m.opts.URL)
	go m.ping(ctx, w, pingInterval)
	return true, m.answer(ctx, w)
}

// hello answers the hub's challenge with the signed nonce.
func (m *Member) hello(w *writer, host string) error {
	_ = w.ws.SetReadDeadline(time.Now().Add(helloTimeout))
	var c Challenge
	if err := readFrame(w.ws, frameChallenge, &c); err != nil {
		return err
	}
	_ = w.ws.SetReadDeadline(time.Time{})
	sig := ed25519.Sign(m.opts.Key, SignedMessage(host, c.Nonce, m.opts.Cluster))
	return w.send(Hello{Type: frameHello, Cluster: m.opts.Cluster, KeyID: m.opts.KeyID, Signature: base64.RawURLEncoding.EncodeToString(sig)})
}

// ping sends a Ping control frame every interval; a failed write drops the
// link.
func (m *Member) ping(ctx context.Context, w *writer, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		w.mu.Lock()
		w.ws.PayloadType = websocket.PingFrame
		err := w.bounded(func() error { _, err := w.ws.Write([]byte("ping")); return err })
		w.ws.PayloadType = websocket.TextFrame
		w.mu.Unlock()
		if err != nil {
			_ = w.ws.Close()
			return
		}
	}
}

// answer reads reviews and answers each in its own goroutine, refusing
// unlisted resources and anything over maxInFlight.
func (m *Member) answer(ctx context.Context, w *writer) error {
	slots := make(chan struct{}, maxInFlight)
	for {
		var r Review
		if err := readFrame(w.ws, frameReview, &r); err != nil {
			return err
		}
		refused := Answer{Type: frameAnswer, ID: r.ID, Refused: RefusedReviewFailed}
		if !Reviewable(r.Group, r.Resource) {
			_ = w.send(refused)
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			_ = w.send(refused)
			continue
		}
		go func() {
			defer func() { <-slots }()
			a := m.opts.Answer(ctx, r)
			a.Type, a.ID = frameAnswer, r.ID
			if w.send(a) != nil {
				_ = w.ws.Close()
			}
		}()
	}
}
