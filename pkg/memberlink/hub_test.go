package memberlink

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestNewHubRequiresHost(t *testing.T) {
	approved := (&approvals{}).approved
	if _, err := NewHub(HubOptions{Approved: approved}); err == nil {
		t.Fatal("a hub without its host was built")
	}
	if _, err := NewHub(HubOptions{Host: "hub.example"}); err == nil {
		t.Fatal("a hub without the approved list was built")
	}
	h, err := NewHub(HubOptions{Host: "hub.example", Approved: approved})
	if err != nil || h.opts.Now == nil || h.opts.Logger == nil {
		t.Fatalf("defaults not set: %v", err)
	}
}

func TestHandshakeAcceptsApprovedKey(t *testing.T) {
	f := newFixture(t)
	f.link(t)
	if got := f.hub.Live(); !reflect.DeepEqual(got, []string{"c1"}) {
		t.Fatalf("Live() = %v", got)
	}
	s := f.hub.Links()["c1"]
	if s.Channel != "live" || !s.Since.Equal(f.clock()) {
		t.Fatalf("link state = %+v", s)
	}
}

func TestHelloWrongKeyClosed(t *testing.T) {
	f := newFixture(t)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	ws, _ := f.dial(t)
	hello(t, ws, f.host, "c1", "kid-1", other)
	if !closed(ws) {
		t.Fatal("a hello signed by another key kept the link")
	}
	ws, _ = f.dial(t)
	hello(t, ws, f.host, "c1", "kid-2", f.key)
	if !closed(ws) {
		t.Fatal("a hello naming another key ID kept the link")
	}
}

func TestHelloBadSignatureClosed(t *testing.T) {
	f := newFixture(t)
	ws, _ := f.dial(t)
	var c Challenge
	_ = readFrame(ws, frameChallenge, &c)
	_ = codec.Send(ws, Hello{Type: frameHello, Cluster: "c1", KeyID: "kid-1", Signature: "not base64!"})
	if !closed(ws) {
		t.Fatal("a hello with a bad signature kept the link")
	}
}

func TestHelloPendingClusterClosed(t *testing.T) {
	f := newFixture(t)
	ws, _ := f.dial(t)
	hello(t, ws, f.host, "pending", "kid-1", f.key)
	if !closed(ws) || len(f.hub.Live()) != 0 {
		t.Fatal("an unapproved cluster linked")
	}
}

func TestHelloRelayedHostClosed(t *testing.T) {
	f := newFixture(t)
	ws, _ := f.dial(t)
	hello(t, ws, "evil.example", "c1", "kid-1", f.key)
	if !closed(ws) {
		t.Fatal("a hello signed for another host kept the link")
	}
}

func TestHelloHostIsConfiguredNotRequestHost(t *testing.T) {
	// The configured host differs from the Host header the request carries.
	f := newFixtureHost(t, "hub.example")
	ws, _ := f.dial(t)
	hello(t, ws, f.host, "c1", "kid-1", f.key)
	if !closed(ws) {
		t.Fatal("a hello signed over the request's Host header was accepted")
	}
	f.link2(t, "hub.example")
}

// link2 links c1 signing host.
func (f *fixture) link2(t *testing.T, host string) {
	t.Helper()
	ws, _ := f.dial(t)
	hello(t, ws, host, "c1", "kid-1", f.key)
	waitFor(t, func() bool { return f.current("c1") != nil })
}

func TestVerifyHello(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	sig := base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, SignedMessage("h", "n", "c1")))
	good := Hello{Cluster: "c1", KeyID: "k", Signature: sig}
	cases := []struct {
		name     string
		hello    Hello
		pub      ed25519.PublicKey
		approved bool
		want     string
	}{
		{"approved", good, pub, true, ""},
		{"not approved", good, pub, false, "not approved"},
		{"key id", Hello{Cluster: "c1", KeyID: "x", Signature: sig}, pub, true, "approved key"},
		{"short key", good, pub[:3], true, "approved key"},
		{"bad base64", Hello{Cluster: "c1", KeyID: "k", Signature: "!"}, pub, true, "bad signature"},
		{"other cluster", Hello{Cluster: "c2", KeyID: "k", Signature: sig}, pub, true, "bad signature"},
	}
	for _, c := range cases {
		err := verifyHello("h", "n", c.hello, c.pub, "k", c.approved)
		if (c.want == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

func TestHelloTimeoutIs10s(t *testing.T) {
	if helloTimeout != 10*time.Second {
		t.Fatalf("helloTimeout = %v", helloTimeout)
	}
}

func TestHelloTimeoutClosed(t *testing.T) {
	shorten(t, &helloTimeout, 50*time.Millisecond)
	f := newFixture(t)
	ws, _ := f.dial(t)
	var c Challenge
	_ = readFrame(ws, frameChallenge, &c)
	if !closed(ws) {
		t.Fatal("a member that never said hello kept the socket")
	}
}

func TestPendingHandshakesCapped(t *testing.T) {
	f := newFixture(t)
	var held []*websocket.Conn
	for range maxPendingHandshakes {
		ws, err := f.dial(t)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, ws)
	}
	resp, err := f.srv.Client().Get(f.srv.URL + Path)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("handshake %d answered %d", maxPendingHandshakes+1, resp.StatusCode)
	}
	for _, ws := range held {
		_ = ws.Close()
	}
	waitFor(t, func() bool { return f.hub.pending.Load() == 0 })
	f.link(t)
}

func TestHandshakeRequiresSubprotocol(t *testing.T) {
	f := newFixture(t)
	if _, err := f.dial(t, func(c *websocket.Config) { c.Protocol = nil }); err == nil {
		t.Fatal("a socket without the member subprotocol was upgraded")
	}
}

func TestMemberLinkIgnoresCookies(t *testing.T) {
	f := newFixture(t)
	cookie := func(c *websocket.Config) {
		c.Header.Set("Cookie", "pacto_session=valid")
		c.Origin.Host = "evil.example"
	}
	ws, _ := f.dial(t, cookie)
	hello(t, ws, f.host, "c1", "kid-1", ed25519.NewKeyFromSeed(make([]byte, 32)))
	if !closed(ws) {
		t.Fatal("a session cookie stood in for the signed nonce")
	}
	ws, _ = f.dial(t, cookie)
	hello(t, ws, f.host, "c1", "kid-1", f.key)
	waitFor(t, func() bool { return f.current("c1") != nil })
}

func TestReviewFrameHasNoVerb(t *testing.T) {
	typ := reflect.TypeFor[Review]()
	for i := range typ.NumField() {
		fld := typ.Field(i)
		if strings.EqualFold(fld.Name, "verb") || strings.Contains(strings.ToLower(string(fld.Tag)), "verb") {
			t.Fatalf("Review carries a verb field %q", fld.Name)
		}
	}
}

func TestReviewTimeoutIs5s(t *testing.T) {
	if ReviewTimeout != 5*time.Second {
		t.Fatalf("ReviewTimeout = %v", ReviewTimeout)
	}
}

func TestHubReviewTimesOutAt5s(t *testing.T) {
	f := newFixture(t)
	f.member(t, func(ctx context.Context, _ Review) Answer { <-ctx.Done(); return Answer{} })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := f.hub.Review(ctx, "c1", Review{Group: "pacto.trianalab.io", Resource: "pactos"}); !errors.Is(err, ErrReviewTimeout) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("the review outlived its deadline")
	}
}

func TestHubReviewAnswered(t *testing.T) {
	f := newFixture(t)
	f.member(t, func(_ context.Context, r Review) Answer {
		return Answer{Allowed: r.Namespace == "payments", Model: ModelSharedIssuer}
	})
	a, err := f.hub.Review(context.Background(), "c1", Review{Namespace: "payments", Group: "apps", Resource: "deployments", Token: "t"})
	if err != nil || !a.Allowed || a.Model != ModelSharedIssuer {
		t.Fatalf("answer = %+v, %v", a, err)
	}
}

func TestDisconnectFailsPendingAtOnce(t *testing.T) {
	f := newFixture(t)
	ws := f.link(t)
	errc := make(chan error, 1)
	go func() {
		_, err := f.hub.Review(context.Background(), "c1", Review{Group: "apps", Resource: "deployments"})
		errc <- err
	}()
	var r Review
	if err := readFrame(ws, frameReview, &r); err != nil {
		t.Fatal(err)
	}
	_ = ws.Close()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrLinkDown) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a pending review waited for its timeout after the link dropped")
	}
	waitFor(t, func() bool { return f.hub.Links()["c1"].Channel == "down" })
}

func TestNewerLinkReplacesOlder(t *testing.T) {
	f := newFixture(t)
	old := f.link(t)
	f.advance(time.Minute)
	ws := f.link(t)
	if !closed(old) {
		t.Fatal("the older link stayed open")
	}
	go func() {
		var r Review
		_ = readFrame(ws, frameReview, &r)
		_ = codec.Send(ws, Answer{Type: frameAnswer, ID: r.ID, Allowed: true})
	}()
	if a, err := f.hub.Review(context.Background(), "c1", Review{}); err != nil || !a.Allowed {
		t.Fatalf("the newer link did not answer: %+v %v", a, err)
	}
	if s := f.hub.Links()["c1"]; s.Channel != "live" || !s.Since.Equal(f.clock()) {
		t.Fatalf("state = %+v", s)
	}
}

func TestRevokedClusterLinkClosed(t *testing.T) {
	f := newFixture(t)
	ws := f.link(t)
	f.app.revoke("c1")
	if _, err := f.hub.Review(context.Background(), "c1", Review{}); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("err = %v", err)
	}
	if !closed(ws) {
		t.Fatal("a revoked cluster kept its link")
	}
	waitFor(t, func() bool { return len(f.hub.Live()) == 0 })
	if _, err := f.hub.Review(context.Background(), "c1", Review{}); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("err = %v", err)
	}
}

func TestRotatedKeyLinkClosed(t *testing.T) {
	f := newFixture(t)
	ws := f.link(t)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	f.app.set("c1", pub, "kid-1")
	if _, err := f.hub.Review(context.Background(), "c1", Review{}); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("err = %v", err)
	}
	if !closed(ws) {
		t.Fatal("a rotated key kept its link")
	}
}

func TestReviewWithoutLinkIsDown(t *testing.T) {
	f := newFixture(t)
	if _, err := f.hub.Review(context.Background(), "c1", Review{}); !errors.Is(err, ErrLinkDown) {
		t.Fatalf("err = %v", err)
	}
}

func TestReviewOnClosedLinkIsDown(t *testing.T) {
	f := newFixture(t)
	f.link(t)
	l := f.current("c1")
	_ = l.w.ws.Close()
	if _, err := l.review(context.Background(), Review{}); !errors.Is(err, ErrLinkDown) {
		t.Fatalf("err = %v", err)
	}
}

func TestLinkOutlivesServerReadTimeout(t *testing.T) {
	f := newFixture(t, func(s *httptest.Server) { s.Config.ReadTimeout = 100 * time.Millisecond })
	f.member(t, func(context.Context, Review) Answer { return Answer{Allowed: true} })
	time.Sleep(300 * time.Millisecond)
	if a, err := f.hub.Review(context.Background(), "c1", Review{Group: "apps", Resource: "deployments"}); err != nil || !a.Allowed {
		t.Fatalf("the link did not outlive the read timeout: %+v %v", a, err)
	}
}

func TestAnswersMatchedByID(t *testing.T) {
	f := newFixture(t)
	ws := f.link(t)
	var wg sync.WaitGroup
	got := map[string]bool{}
	var mu sync.Mutex
	for _, ns := range []string{"a", "b"} {
		wg.Go(func() {
			a, err := f.hub.Review(context.Background(), "c1", Review{Namespace: ns})
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			got[ns] = a.Allowed
			mu.Unlock()
		})
	}
	var first, second Review
	_ = readFrame(ws, frameReview, &first)
	_ = readFrame(ws, frameReview, &second)
	// An answer nobody waits for is dropped.
	_ = codec.Send(ws, Answer{Type: frameAnswer, ID: 999, Allowed: true})
	_ = codec.Send(ws, Answer{Type: frameAnswer, ID: second.ID, Allowed: second.Namespace == "b"})
	_ = codec.Send(ws, Answer{Type: frameAnswer, ID: first.ID, Allowed: first.Namespace == "b"})
	wg.Wait()
	if !reflect.DeepEqual(got, map[string]bool{"a": false, "b": true}) {
		t.Fatalf("answers = %v", got)
	}
}

// TestUnexpectedFrameClosesChannel covers both sides: after the handshake the
// hub reads only answers and the member only reviews.
func TestUnexpectedFrameClosesChannel(t *testing.T) {
	t.Run("hub", unexpectedFrameClosesHubChannel)
	t.Run("member", unexpectedFrameClosesMemberChannel)
}

func unexpectedFrameClosesHubChannel(t *testing.T) {
	for name, frame := range unexpectedFrames(frameAnswer) {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			ws := f.link(t)
			if s, ok := frame.(string); ok {
				_ = websocket.Message.Send(ws, s)
			} else {
				_ = websocket.Message.Send(ws, frame)
			}
			if !closed(ws) {
				t.Fatal("the hub kept the channel")
			}
			waitFor(t, func() bool { return len(f.hub.Live()) == 0 })
		})
	}
}

// unexpectedFrames is every frame a side expecting want must refuse.
func unexpectedFrames(want string) map[string]any {
	return map[string]any{
		"challenge":     `{"type":"challenge","nonce":"n"}`,
		"hello":         `{"type":"hello","cluster":"c1","keyId":"k","signature":"s"}`,
		"exec":          `{"type":"exec","command":"sh"}`,
		"proxy":         `{"type":"proxy","path":"/api/v1/secrets"}`,
		"unknown field": `{"type":"` + want + `","id":1,"verb":"create"}`,
		"not json":      `not json`,
		"oversize":      `{"type":"` + want + `","pad":"` + strings.Repeat("x", MaxFrameBytes) + `"}`,
		"binary":        []byte(`{"type":"` + want + `","id":1}`),
	}
}

func TestNoteIdentityThreshold(t *testing.T) {
	f := newFixture(t)
	f.hub.NoteIdentity("c1", "https://idp", ModelIssuerNotAccepted, "alice") // no link: ignored
	f.link(t)
	const iss = "https://idp"
	f.hub.NoteIdentity("c1", iss, ModelIssuerNotAccepted, "alice")
	f.hub.NoteIdentity("c1", iss, ModelIssuerNotAccepted, "alice")
	if f.hub.IssuerRefused("c1", iss) {
		t.Fatal("one person's refusals marked the issuer refused")
	}
	f.advance(2 * time.Minute)
	f.hub.NoteIdentity("c1", iss, ModelIssuerNotAccepted, "bob")
	if f.hub.IssuerRefused("c1", iss) {
		t.Fatal("refusals more than a minute apart marked the issuer refused")
	}
	f.advance(30 * time.Second)
	f.hub.NoteIdentity("c1", iss, ModelIssuerNotAccepted, "carol")
	if !f.hub.IssuerRefused("c1", iss) || f.hub.Links()["c1"].Issuers[iss] != ModelIssuerNotAccepted {
		t.Fatal("two persons within a minute did not mark the issuer refused")
	}
	f.hub.NoteIdentity("c1", iss, ModelGroupMapping, "carol")
	if !f.hub.IssuerRefused("c1", iss) || f.hub.Links()["c1"].Issuers[iss] != ModelGroupMapping {
		t.Fatal("the mapping model did not show beside the refusal")
	}
	f.advance(refusalTTL + time.Second)
	if f.hub.IssuerRefused("c1", iss) {
		t.Fatal("the refusal did not expire")
	}
	f.hub.NoteIdentity("c1", iss, ModelIssuerNotAccepted, "alice")
	f.hub.NoteIdentity("c1", iss, ModelIssuerNotAccepted, "bob")
	f.hub.NoteIdentity("c1", iss, ModelSharedIssuer, "dave")
	if f.hub.IssuerRefused("c1", iss) {
		t.Fatal("an accepted token did not clear the refusal")
	}
	f.hub.NoteIdentity("c1", iss, ModelIssuerNotAccepted, "alice")
	f.hub.NoteIdentity("c1", iss, ModelIssuerNotAccepted, "bob")
	f.link(t)
	if f.hub.IssuerRefused("c1", iss) || len(f.hub.Links()["c1"].Issuers) != 0 {
		t.Fatal("a reconnect did not clear the refusal")
	}
}

// Once marked, an issuer whose mark expired is confirmed by the first
// re-probe refusal: the mapping it was served by keeps answering.
func TestExpiredRefusalReconfirmedByOnePerson(t *testing.T) {
	f := newFixture(t)
	f.link(t)
	const iss = "https://idp"
	f.hub.NoteIdentity("c1", iss, ModelIssuerNotAccepted, "alice")
	f.hub.NoteIdentity("c1", iss, ModelIssuerNotAccepted, "bob")
	f.advance(refusalTTL + time.Second)
	f.hub.NoteIdentity("c1", iss, ModelIssuerNotAccepted, "alice")
	if !f.hub.IssuerRefused("c1", iss) {
		t.Fatal("the re-probe refusal of an issuer marked before did not mark it again")
	}
}

func TestRefuseIssuerMarksAtOnce(t *testing.T) {
	f := newFixture(t)
	const iss = "https://idp"
	f.hub.RefuseIssuer("c1", iss) // no link: ignored
	f.link(t)
	f.hub.RefuseIssuer("c1", iss)
	if !f.hub.IssuerRefused("c1", iss) || f.hub.Links()["c1"].Issuers[iss] != ModelIssuerNotAccepted {
		t.Fatal("a refusal the hub decides itself did not mark the issuer at once")
	}
	f.advance(refusalTTL - time.Second)
	f.hub.RefuseIssuer("c1", iss)
	f.advance(2 * time.Second)
	if f.hub.IssuerRefused("c1", iss) {
		t.Fatal("a mark in force was extended, so the issuer is never re-probed")
	}
}

// A revoked cluster's link closes when the approved list changes, before
// any review: its channel no longer reads live.
func TestRecheckClosesRevokedLink(t *testing.T) {
	f := newFixture(t)
	ws := f.link(t)
	f.hub.Recheck(context.Background())
	if len(f.hub.Live()) != 1 {
		t.Fatal("an approved link was closed")
	}
	f.app.revoke("c1")
	f.hub.Recheck(context.Background())
	if len(f.hub.Live()) != 0 || f.hub.Links()["c1"].Channel != "down" {
		t.Fatalf("a revoked link stayed live: %+v", f.hub.Links())
	}
	if !closed(ws) {
		t.Fatal("a revoked cluster kept its socket")
	}
}

// TestLinkOutlivesPingAfterReview is the regression for a link that dropped
// at the member's first Ping after a review: the review's write deadline
// stayed on the hub's connection and failed the Pong the websocket package
// writes, so every link died about 30s after it came up.
func TestLinkOutlivesPingAfterReview(t *testing.T) {
	shorten(t, &writeTimeout, 50*time.Millisecond)
	shorten(t, &pingInterval, 150*time.Millisecond)
	f := newFixture(t)
	f.member(t, func(context.Context, Review) Answer { return Answer{Allowed: true} })
	first := f.current("c1")
	review := Review{Group: "apps", Resource: "deployments"}
	if _, err := f.hub.Review(context.Background(), "c1", review); err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond) // past the write deadline, through several Pings
	if f.current("c1") != first {
		t.Fatal("a Ping after a review dropped the link and the member linked again")
	}
	if a, err := f.hub.Review(context.Background(), "c1", review); err != nil || !a.Allowed {
		t.Fatalf("review after the Pings: %+v %v", a, err)
	}
}
