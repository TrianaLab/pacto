package memberlink

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/websocket"
)

var (
	// ErrNotApproved is a review for a cluster whose key is not approved.
	ErrNotApproved = errors.New("member cluster not approved")
	// ErrLinkDown is a review for a cluster with no live link, or whose link
	// dropped before it answered.
	ErrLinkDown = errors.New("member link down")
	// ErrReviewTimeout is a review the member did not answer in time.
	ErrReviewTimeout = errors.New("member review timed out")
)

// maxPendingHandshakes bounds the connections that have not proven a key.
const maxPendingHandshakes = 64

// Issuer refusal: marked after refusals for refusalPersons distinct persons
// within refusalWindow, and kept for refusalTTL.
const (
	refusalPersons = 2
	refusalWindow  = time.Minute
	refusalTTL     = 10 * time.Minute
)

// HubOptions configures a Hub.
type HubOptions struct {
	// Host is the hub's configured external host, the one members dial and
	// sign. Required: a request's Host header is never trusted.
	Host string
	// Approved returns the approved installation key of a cluster and its
	// key ID. It must read memory only: anonymous clients reach it.
	Approved func(ctx context.Context, uid string) (ed25519.PublicKey, string, bool)
	Now      func() time.Time
	Logger   *slog.Logger
}

// LinkState is what the hub knows about one cluster's link: live or down
// since when, and the identity model in use per token issuer.
type LinkState struct {
	Channel string
	Since   time.Time
	Issuers map[string]string
}

// Hub is the server half: it accepts member links and sends them reviews.
type Hub struct {
	opts    HubOptions
	pending atomic.Int32

	mu     sync.Mutex
	links  map[string]*link
	states map[string]*LinkState
	// refusals is, per cluster and issuer, who was refused when, and the
	// mark's expiry once enough distinct persons were.
	refusals map[[2]string]*refusal
}

type refusal struct {
	persons map[string]time.Time
	until   time.Time
}

// NewHub returns a hub for opts; Host and Approved are required.
func NewHub(opts HubOptions) (*Hub, error) {
	if opts.Host == "" || opts.Approved == nil {
		return nil, errors.New("member hub needs its external host and the approved clusters")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Hub{opts: opts, links: map[string]*link{}, states: map[string]*LinkState{}, refusals: map[[2]string]*refusal{}}, nil
}

// ServeHTTP upgrades a member's connection. Authentication is the signed
// nonce: cookies and Origin are ignored.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.pending.Add(1) > maxPendingHandshakes {
		h.pending.Add(-1)
		http.Error(w, "too many member handshakes", http.StatusServiceUnavailable)
		return
	}
	release := sync.OnceFunc(func() { h.pending.Add(-1) })
	defer release()
	websocket.Server{
		Handshake: requireSubprotocol,
		Handler:   func(ws *websocket.Conn) { h.serve(ws, release) },
	}.ServeHTTP(w, r)
}

// requireSubprotocol accepts only the member link subprotocol, from any
// Origin.
func requireSubprotocol(cfg *websocket.Config, _ *http.Request) error {
	if !slices.Contains(cfg.Protocol, Subprotocol) {
		return errors.New("member link subprotocol required")
	}
	cfg.Protocol = []string{Subprotocol}
	return nil
}

func (h *Hub) serve(ws *websocket.Conn, release func()) {
	defer func() { _ = ws.Close() }()
	ws.MaxPayloadBytes = MaxFrameBytes
	l, err := h.handshake(ws)
	release()
	if err != nil {
		h.opts.Logger.Warn("member link refused", "cluster", l.uid, "reason", err.Error())
		return
	}
	// The server's read timeout would cut every link: a link lives until
	// either side closes it.
	_ = ws.SetDeadline(time.Time{})
	h.add(l)
	err = l.readAnswers()
	h.remove(l)
	h.opts.Logger.Info("member link closed", "cluster", l.uid, "reason", err.Error())
}

// handshake sends a nonce and verifies the member's hello; the link it
// returns names the claimed cluster even when it fails.
func (h *Hub) handshake(ws *websocket.Conn) (*link, error) {
	_ = ws.SetDeadline(time.Now().Add(helloTimeout))
	l := &link{w: &writer{ws: ws}, waiting: map[uint64]chan Answer{}, done: make(chan struct{})}
	nonce := make([]byte, 32)
	_, _ = rand.Read(nonce)
	c := Challenge{Type: frameChallenge, Nonce: base64.RawURLEncoding.EncodeToString(nonce)}
	_ = l.w.send(c) // a failed send fails the read below
	var hello Hello
	if err := readFrame(ws, frameHello, &hello); err != nil {
		return l, err
	}
	l.uid = truncate(hello.Cluster)
	pub, keyID, ok := h.opts.Approved(ws.Request().Context(), hello.Cluster)
	if err := verifyHello(h.opts.Host, c.Nonce, hello, pub, keyID, ok); err != nil {
		return l, err
	}
	l.uid, l.pub, l.keyID = hello.Cluster, pub, keyID
	return l, nil
}

// verifyHello checks a hello against the approved key of its cluster, over
// the hub's configured host.
func verifyHello(host, nonce string, hello Hello, pub ed25519.PublicKey, keyID string, approved bool) error {
	if !approved {
		return ErrNotApproved
	}
	if hello.KeyID != keyID || len(pub) != ed25519.PublicKeySize {
		return errors.New("key does not match the approved key")
	}
	sig, err := base64.RawURLEncoding.DecodeString(hello.Signature)
	if err != nil || !ed25519.Verify(pub, SignedMessage(host, nonce, hello.Cluster), sig) {
		return errors.New("bad signature")
	}
	return nil
}

// truncate bounds a peer-supplied cluster UID before it reaches a log.
func truncate(s string) string {
	return fmt.Sprintf("%.64s", s)
}

// add makes l the cluster's live link, closing an older one.
func (h *Hub) add(l *link) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if old := h.links[l.uid]; old != nil {
		old.close()
	}
	h.links[l.uid] = l
	h.states[l.uid] = &LinkState{Channel: "live", Since: h.opts.Now(), Issuers: map[string]string{}}
	for k := range h.refusals {
		if k[0] == l.uid {
			delete(h.refusals, k)
		}
	}
}

// remove marks l's cluster down, unless a newer link replaced it.
func (h *Hub) remove(l *link) {
	l.close()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.links[l.uid] == l {
		delete(h.links, l.uid)
		h.states[l.uid].Channel, h.states[l.uid].Since = "down", h.opts.Now()
	}
}

// Review asks the cluster uid's member whether r's identity may list r's
// resource, re-checking the cluster's approval first. It waits for the
// answer at most ReviewTimeout, less when ctx ends sooner.
func (h *Hub) Review(ctx context.Context, uid string, r Review) (Answer, error) {
	h.mu.Lock()
	l := h.links[uid]
	h.mu.Unlock()
	if l != nil && !h.approved(ctx, l) {
		l.close()
		return Answer{}, ErrNotApproved
	}
	if l == nil {
		if _, _, ok := h.opts.Approved(ctx, uid); !ok {
			return Answer{}, ErrNotApproved
		}
		return Answer{}, ErrLinkDown
	}
	return l.review(ctx, r)
}

// approved says whether l's cluster is still approved with l's key.
func (h *Hub) approved(ctx context.Context, l *link) bool {
	pub, keyID, ok := h.opts.Approved(ctx, l.uid)
	return ok && keyID == l.keyID && pub.Equal(l.pub)
}

// Recheck closes every link whose cluster is no longer approved, or is now
// approved with another key, so its channel reads down at once: call it
// when the approved list changes.
func (h *Hub) Recheck(ctx context.Context) {
	h.mu.Lock()
	links := slices.Collect(maps.Values(h.links))
	h.mu.Unlock()
	for _, l := range links {
		if !h.approved(ctx, l) {
			h.remove(l)
		}
	}
}

// Live is the UIDs of the clusters with a live link, sorted.
func (h *Hub) Live() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	uids := make([]string, 0, len(h.links))
	for uid := range h.links {
		uids = append(uids, uid)
	}
	slices.Sort(uids)
	return uids
}

// Links is the state of every cluster that ever linked.
func (h *Hub) Links() map[string]LinkState {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]LinkState, len(h.states))
	for uid, s := range h.states {
		c := *s
		c.Issuers = make(map[string]string, len(s.Issuers))
		for k, v := range s.Issuers {
			c.Issuers[k] = v
		}
		out[uid] = c
	}
	return out
}

// NoteIdentity records the model a review of cluster uid used for a token
// from issuer, for person (a stable, non-secret key). ModelIssuerNotAccepted
// marks the issuer refused only once refusalPersons distinct persons were
// refused within refusalWindow, for refusalTTL; a reconnect clears it. One
// person's refusal is no evidence that issuers differ.
func (h *Hub) NoteIdentity(uid, issuer, model, person string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.states[uid]
	if s == nil || s.Channel != "live" {
		return
	}
	if model != ModelIssuerNotAccepted {
		s.Issuers[issuer] = model
		if model == ModelSharedIssuer {
			delete(h.refusals, [2]string{uid, issuer})
		}
		return
	}
	now := h.opts.Now()
	r := h.refusal(uid, issuer)
	r.persons[person] = now
	for p, at := range r.persons {
		if now.Sub(at) > refusalWindow {
			delete(r.persons, p)
		}
	}
	// An issuer marked before, and never accepted since, is confirmed again
	// by one refusal: its mark expired only to re-probe.
	if len(r.persons) >= refusalPersons || !r.until.IsZero() {
		r.until = now.Add(refusalTTL)
		s.Issuers[issuer] = ModelIssuerNotAccepted
	}
}

// RefuseIssuer marks issuer refused for cluster uid at once, with no
// threshold: the hub itself knows no member token can be made for it (it
// comes from another identity provider, or the hub exchanges none). A mark
// still in force keeps its expiry, so the re-probe still happens.
func (h *Hub) RefuseIssuer(uid, issuer string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.states[uid]
	if s == nil || s.Channel != "live" {
		return
	}
	s.Issuers[issuer] = ModelIssuerNotAccepted
	r, now := h.refusal(uid, issuer), h.opts.Now()
	if !now.Before(r.until) {
		r.until = now.Add(refusalTTL)
	}
}

// refusal is the refusal record of uid and issuer, made when missing; the
// caller holds h.mu.
func (h *Hub) refusal(uid, issuer string) *refusal {
	key := [2]string{uid, issuer}
	r := h.refusals[key]
	if r == nil {
		r = &refusal{persons: map[string]time.Time{}}
		h.refusals[key] = r
	}
	return r
}

// IssuerRefused says whether cluster uid's apiserver is marked as refusing
// tokens from issuer.
func (h *Hub) IssuerRefused(uid, issuer string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.refusals[[2]string{uid, issuer}]
	return r != nil && h.opts.Now().Before(r.until)
}

// link is one verified member connection.
type link struct {
	w          *writer
	uid, keyID string
	pub        ed25519.PublicKey

	mu      sync.Mutex
	next    uint64
	waiting map[uint64]chan Answer
	done    chan struct{}
	once    sync.Once
}

// close ends the link; every pending review fails at once.
func (l *link) close() {
	l.once.Do(func() {
		close(l.done)
		_ = l.w.ws.Close()
	})
}

// readAnswers delivers each answer to its review until the link fails.
func (l *link) readAnswers() error {
	for {
		var a Answer
		if err := readFrame(l.w.ws, frameAnswer, &a); err != nil {
			return err
		}
		l.mu.Lock()
		ch := l.waiting[a.ID]
		delete(l.waiting, a.ID)
		l.mu.Unlock()
		if ch != nil {
			ch <- a
		}
	}
}

func (l *link) review(ctx context.Context, r Review) (Answer, error) {
	ctx, cancel := context.WithTimeout(ctx, ReviewTimeout)
	defer cancel()
	ch := make(chan Answer, 1)
	l.mu.Lock()
	l.next++
	r.Type, r.ID = frameReview, l.next
	l.waiting[r.ID] = ch
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.waiting, r.ID)
		l.mu.Unlock()
	}()
	if err := l.w.send(r); err != nil {
		l.close()
		return Answer{}, ErrLinkDown
	}
	select {
	case a := <-ch:
		return a, nil
	case <-l.done:
		return Answer{}, ErrLinkDown
	case <-ctx.Done():
		return Answer{}, ErrReviewTimeout
	}
}
