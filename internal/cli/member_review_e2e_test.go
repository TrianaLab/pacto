package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/viper"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/memberlink"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const (
	memberIssuer = "https://login.acme.dev"
	usUID        = "uid-us"
)

// memberToken is an unsigned JWT from memberIssuer, valid for 30 minutes:
// what a hub reads and a member link forwards once exchanged.
func memberToken(sub string) string {
	b, _ := json.Marshal(map[string]any{"iss": memberIssuer, "sub": sub, "exp": time.Now().Add(30 * time.Minute).Unix()})
	return "e30." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
}

// signedInAs is a request context as the dashboard's sign-in middleware
// leaves it for a person whose bearer token the cluster accepted.
func signedInAs(t *testing.T, p authn.Person, token string) context.Context {
	t.Helper()
	review := func(context.Context, string) (authn.Reviewed, error) {
		return authn.Reviewed{Person: p, Bearer: true}, nil
	}
	var ctx context.Context
	h := authn.NewShared(nil, authn.NewTokenLogin(review, authn.Lifetime{})).AcceptBearer(review).Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctx = r.Context()
	}))
	r := httptest.NewRequest(http.MethodGet, "/api/backstage/fleet", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if ctx == nil || !authn.StateFrom(ctx).SignedIn {
		t.Fatal("the middleware did not sign the person in")
	}
	return ctx
}

// memberEnv is a dashboard's member hub behind a TLS server, with member
// clusters linked to it whose reviews answer runs.
type memberEnv struct {
	t   *testing.T
	m   *memberHub
	srv *httptest.Server

	mu       sync.Mutex
	answers  map[string]func(memberlink.Review) memberlink.Answer
	sent     map[string][]memberlink.Review
	keys     map[string]ed25519.PrivateKey
	mappings map[string][]delivery.GroupMappingState
	stops    map[string]context.CancelFunc
}

func newMemberEnv(t *testing.T, exchange authn.MemberExchange, now func() time.Time) *memberEnv {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	host := srv.Listener.Addr().String()
	m, err := newMemberHub(host, exchange, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if now != nil {
		m.hub, _ = memberlink.NewHub(memberlink.HubOptions{Host: host, Approved: m.approvals.lookup, Now: now, Logger: m.logger})
	}
	srv.Config.Handler = m.hub
	srv.StartTLS()
	e := &memberEnv{t: t, m: m, srv: srv, answers: map[string]func(memberlink.Review) memberlink.Answer{}, sent: map[string][]memberlink.Review{},
		keys: map[string]ed25519.PrivateKey{}, mappings: map[string][]delivery.GroupMappingState{}, stops: map[string]context.CancelFunc{}}
	t.Cleanup(func() {
		e.mu.Lock()
		for _, stop := range e.stops {
			stop()
		}
		e.mu.Unlock()
		srv.Close()
	})
	return e
}

// approve puts uid in the hub's approved list in memory, with mappings.
func (e *memberEnv) approve(uid string, mappings ...delivery.GroupMappingState) {
	e.mu.Lock()
	if e.keys[uid] == nil {
		_, e.keys[uid], _ = ed25519.GenerateKey(rand.Reader)
	}
	e.mappings[uid] = mappings
	var l delivery.ClusterJoinList
	l.Completeness = fleet.CompletenessComplete
	for id, k := range e.keys {
		der, _ := x509.MarshalPKIXPublicKey(k.Public())
		l.Clusters = append(l.Clusters, delivery.ClusterJoinState{UID: id, State: delivery.JoinApproved,
			ApprovedKeyID: signing.Hint(k.Public()), ApprovedPublicKey: base64.StdEncoding.EncodeToString(der), Mappings: e.mappings[id]})
	}
	e.mu.Unlock()
	e.m.approvals.update(l)
}

// link runs member uid's operator half, answering with answer, until its
// link is live; it approves uid first when nothing did.
func (e *memberEnv) link(uid string, answer func(memberlink.Review) memberlink.Answer) {
	e.t.Helper()
	e.mu.Lock()
	known := e.keys[uid] != nil
	e.mu.Unlock()
	if !known {
		e.approve(uid)
	}
	e.mu.Lock()
	e.answers[uid] = answer
	k := e.keys[uid]
	e.mu.Unlock()
	ctx, stop := context.WithCancel(context.Background())
	m := memberlink.NewMember(memberlink.MemberOptions{URL: e.srv.URL, RootCAs: e.srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs,
		Cluster: uid, KeyID: signing.Hint(k.Public()), Key: k, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Answer: func(_ context.Context, r memberlink.Review) memberlink.Answer {
			e.mu.Lock()
			e.sent[uid] = append(e.sent[uid], r)
			a := e.answers[uid]
			e.mu.Unlock()
			out := a(r)
			out.Type, out.ID = "answer", r.ID
			return out
		}})
	go func() { _ = m.Run(ctx) }()
	e.mu.Lock()
	e.stops[uid] = stop
	e.mu.Unlock()
	e.waitLive(uid, true)
}

// unlink stops member uid's operator half and waits until the hub sees it.
func (e *memberEnv) unlink(uid string) {
	e.mu.Lock()
	stop := e.stops[uid]
	delete(e.stops, uid)
	e.mu.Unlock()
	stop()
	e.waitLive(uid, false)
}

func (e *memberEnv) waitLive(uid string, live bool) {
	e.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if slices.Contains(e.m.hub.Live(), uid) == live {
			return
		}
	}
	e.t.Fatalf("member %s never became live=%v", uid, live)
}

func (e *memberEnv) sentTo(uid string) []memberlink.Review {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.sent[uid])
}

func allowAll(memberlink.Review) memberlink.Answer {
	return memberlink.Answer{Allowed: true, Model: memberlink.ModelSharedIssuer}
}

// memberViewer is the dashboard viewer of the two-cluster fleet: eu-prod
// read through its kubeconfig context (where alice reads prod only), us-prod
// a joined member reached only through e's hub.
func memberViewer(e *memberEnv) (*authn.Viewer, *hostClusters) {
	h := &hostClusters{allow: func(host, ns string) bool { return ns == "prod" }, uids: map[string]string{"https://eu": "uid-eu"}}
	home := newHomeCluster(&rest.Config{Host: "https://eu"}, reviewDeps(h))
	return clusterViewer(entriesOf(entry("eu-prod", "https://eu", "uid-eu")), home, reviewDeps(h), reviewCluster, nil, e.m), h
}

var alicePerson = authn.Person{User: "alice@acme.dev", Groups: []string{"team-pay"}}

// usAccess is how alice's view of the two-cluster fleet treats us-prod: its
// access mark, whether its services show and whether the answer is partial.
func usAccess(t *testing.T, v *authn.Viewer, ctx context.Context) (access string, shows, partial bool) {
	t.Helper()
	q, err := v.Query(ctx, alicePerson, twoClusterQuery(t))
	if err != nil {
		t.Fatal(err)
	}
	s := q.SnapshotForSerialization()
	for _, c := range s.Clusters {
		if c.ID == usUID {
			access = c.Access
		}
	}
	return access, slices.Contains(services(q), "prod-us-prod"), s.Completeness == fleet.CompletenessPartial
}

func TestMemberReviewEndToEnd(t *testing.T) {
	e := newMemberEnv(t, func(_ context.Context, _, uid string) (string, error) { return memberToken("exchanged-" + uid), nil }, nil)
	e.link(usUID, func(r memberlink.Review) memberlink.Answer {
		return memberlink.Answer{Allowed: r.Namespace == "prod" || r.Namespace == "", Model: memberlink.ModelSharedIssuer}
	})
	v, _ := memberViewer(e)
	session := memberToken("session")
	access, shows, _ := usAccess(t, v, signedInAs(t, alicePerson, session))
	expect(t, access == "" && shows, "the member's own SAR lets alice read us-prod/prod: %q %v", access, shows)
	for _, r := range e.sentTo(usUID) {
		expect(t, r.Token != session && r.Token != "" && r.Mapped == nil, "only the exchanged token travels: %+v", r)
	}
	expect(t, e.m.hub.Links()[usUID].Issuers[memberIssuer] == memberlink.ModelSharedIssuer, "the model is noted: %+v", e.m.hub.Links())
}

func TestJoinedClusterWithoutLinkNotReviewable(t *testing.T) {
	e := newMemberEnv(t, nil, nil)
	e.approve(usUID) // approved, never linked
	v, _ := memberViewer(e)
	access, shows, partial := usAccess(t, v, signedInAs(t, alicePerson, memberToken("s")))
	expect(t, access == fleet.ClusterNotReviewable && !shows && partial, "no link, no reviewer: %q %v %v", access, shows, partial)
}

func TestReviewsMapUsesOnlyThatClustersLink(t *testing.T) {
	e := newMemberEnv(t, func(_ context.Context, _, uid string) (string, error) { return memberToken(uid), nil }, nil)
	e.link("uid-a", allowAll)
	e.link("uid-b", allowAll)
	set := clusterReviews(entriesOf(), homeCluster{}, defaultDeps(), reviewCluster, e.m)
	m := set.Reviews(context.Background())
	expect(t, len(m) == 2 && m["uid-a"] != nil && m["uid-b"] != nil, "one reviewer per live member: %v", m)
	ok, err := m["uid-a"](signedInAs(t, alicePerson, memberToken("s")), alicePerson, "prod", authn.PactoResource)
	expect(t, ok && err == nil, "asked: %v %v", ok, err)
	expect(t, len(e.sentTo("uid-a")) == 1 && len(e.sentTo("uid-b")) == 0, "A's reviewer used A's link only")
	again := set.Reviews(context.Background())
	expect(t, len(again) == 2 && len(set.reviews) == 0, "the context map is never changed: %v", set.reviews)
}

func TestContextReviewerWinsOverMemberLink(t *testing.T) {
	e := newMemberEnv(t, func(_ context.Context, _, uid string) (string, error) { return memberToken(uid), nil }, nil)
	e.link("uid-eu", allowAll)
	v, h := memberViewer(e)
	for range 2 {
		_, _, _ = usAccess(t, v, signedInAs(t, alicePerson, memberToken("s")))
	}
	expect(t, len(e.sentTo("uid-eu")) == 0 && len(h.askedOf("https://eu")) > 0, "the context reviews eu-prod, never its link")
}

func TestMemberTimeoutHidesClusterPartial(t *testing.T) {
	if testing.Short() {
		t.Skip("waits the 5s review bound")
	}
	e := newMemberEnv(t, func(_ context.Context, _, uid string) (string, error) { return memberToken(uid), nil }, nil)
	block := make(chan struct{})
	defer close(block)
	e.link(usUID, func(memberlink.Review) memberlink.Answer { <-block; return memberlink.Answer{} })
	v, _ := memberViewer(e)
	start := time.Now()
	access, shows, partial := usAccess(t, v, signedInAs(t, alicePerson, memberToken("s")))
	expect(t, access == fleet.ClusterReviewFailed && !shows && partial && time.Since(start) < 8*time.Second, "a silent member is hidden at the bound: %q %v %v %s", access, shows, partial, time.Since(start))
}

func TestMemberDisconnectHidesClusterPartial(t *testing.T) {
	e := newMemberEnv(t, func(_ context.Context, _, uid string) (string, error) { return memberToken(uid), nil }, nil)
	var once sync.Once
	e.link(usUID, func(memberlink.Review) memberlink.Answer {
		once.Do(func() { go e.unlink(usUID) })
		time.Sleep(time.Second)
		return memberlink.Answer{Allowed: true}
	})
	v, _ := memberViewer(e)
	start := time.Now()
	access, shows, partial := usAccess(t, v, signedInAs(t, alicePerson, memberToken("s")))
	expect(t, access == fleet.ClusterReviewFailed && !shows && partial && time.Since(start) < 4*time.Second, "a dropped link hides at once: %q %v %v %s", access, shows, partial, time.Since(start))
}

func TestMemberRefusalHidesCluster(t *testing.T) {
	e := newMemberEnv(t, func(_ context.Context, _, uid string) (string, error) { return memberToken(uid), nil }, nil)
	e.link(usUID, func(memberlink.Review) memberlink.Answer {
		return memberlink.Answer{Refused: memberlink.RefusedReviewFailed}
	})
	v, _ := memberViewer(e)
	access, shows, partial := usAccess(t, v, signedInAs(t, alicePerson, memberToken("s")))
	expect(t, access == fleet.ClusterReviewFailed && !shows && partial, "a refusal hides: %q %v %v", access, shows, partial)
}

func TestMemberFailureNotCached(t *testing.T) {
	e := newMemberEnv(t, func(_ context.Context, _, uid string) (string, error) { return memberToken(uid), nil }, nil)
	var refuse atomic.Bool
	refuse.Store(true)
	e.link(usUID, func(memberlink.Review) memberlink.Answer {
		if refuse.Load() {
			return memberlink.Answer{Refused: memberlink.RefusedReviewFailed}
		}
		return allowAll(memberlink.Review{})
	})
	v, _ := memberViewer(e)
	ctx := signedInAs(t, alicePerson, memberToken("s"))
	access, _, _ := usAccess(t, v, ctx)
	expect(t, access == fleet.ClusterReviewFailed, "refused first: %q", access)
	refuse.Store(false)
	access, shows, _ := usAccess(t, v, ctx)
	expect(t, access == "" && shows, "the failure was not kept: %q %v", access, shows)
}

func TestMemberVerdictCachedOneMinute(t *testing.T) {
	e := newMemberEnv(t, func(_ context.Context, _, uid string) (string, error) { return memberToken(uid), nil }, nil)
	e.link(usUID, allowAll)
	v, _ := memberViewer(e)
	ctx := signedInAs(t, alicePerson, memberToken("s"))
	_, _, _ = usAccess(t, v, ctx)
	n := len(e.sentTo(usUID))
	access, shows, _ := usAccess(t, v, ctx)
	expect(t, n > 0 && len(e.sentTo(usUID)) == n && access == "" && shows, "the verdict is kept: %d then %d", n, len(e.sentTo(usUID)))
}

func TestServiceAccountNeverReviewedInMember(t *testing.T) {
	e := newMemberEnv(t, func(_ context.Context, _, uid string) (string, error) { return memberToken(uid), nil }, nil)
	e.link(usUID, allowAll)
	v, _ := memberViewer(e)
	sa := authn.Person{User: "system:serviceaccount:pacto:reader", Groups: []string{"system:serviceaccounts"}}
	q, err := v.Query(signedInAs(t, sa, memberToken("sa")), sa, twoClusterQuery(t))
	expect(t, err == nil && !slices.Contains(services(q), "prod-us-prod"), "a ServiceAccount reads no member: %v", err)
	expect(t, len(e.sentTo(usUID)) == 0, "the member is never asked about it")
}

// TestSingle401DoesNotMarkIssuerRefused: one person's refused token hides
// the member for that person only and marks nothing; a second distinct
// person within a minute marks the issuer, and the mapping answers.
func TestSingle401DoesNotMarkIssuerRefused(t *testing.T) {
	e := newMemberEnv(t, func(_ context.Context, _, uid string) (string, error) { return memberToken(uid), nil }, nil)
	e.approve(usUID, delivery.GroupMappingState{Issuer: memberIssuer, Rules: []delivery.GroupRule{{From: "team-pay", To: "viewers"}}, Record: "sha256:aa"})
	e.link(usUID, func(r memberlink.Review) memberlink.Answer {
		if r.Token != "" {
			return memberlink.Answer{Refused: memberlink.RefusedIssuerNotAccepted}
		}
		return memberlink.Answer{Allowed: true, Model: memberlink.ModelGroupMapping}
	})
	review := e.m.add(nil)[usUID]
	bob := authn.Person{User: "bob@acme.dev", Groups: []string{"team-pay"}}
	_, err := review(signedInAs(t, alicePerson, memberToken("a")), alicePerson, "prod", authn.PactoResource)
	expect(t, err != nil && !e.m.hub.IssuerRefused(usUID, memberIssuer), "one refusal hides for alice and marks nothing: %v", err)
	expect(t, e.m.hub.Links()[usUID].Issuers[memberIssuer] == "", "no model is shown yet: %+v", e.m.hub.Links())
	ok, err := review(signedInAs(t, bob, memberToken("b")), bob, "prod", authn.PactoResource)
	expect(t, ok && err == nil && e.m.hub.IssuerRefused(usUID, memberIssuer), "a second person marks it and the mapping answers: %v %v", ok, err)
	sent := e.sentTo(usUID)
	expect(t, len(sent) == 3 && sent[2].Mapped != nil && sent[2].Mapped.Record == "sha256:aa", "token, token, mapped: %+v", sent)
}

func TestIssuerRefusalExpiresAndReprobes(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().Unix())
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	e := newMemberEnv(t, func(_ context.Context, _, uid string) (string, error) { return memberToken(uid), nil }, now)
	e.approve(usUID, delivery.GroupMappingState{Issuer: memberIssuer, Rules: []delivery.GroupRule{{From: "team-pay", To: "viewers"}}, Record: "sha256:aa"})
	e.link(usUID, func(r memberlink.Review) memberlink.Answer {
		if r.Token != "" {
			return memberlink.Answer{Refused: memberlink.RefusedIssuerNotAccepted}
		}
		return memberlink.Answer{Allowed: true, Model: memberlink.ModelGroupMapping}
	})
	review := e.m.add(nil)[usUID]
	for _, p := range []string{"a@acme.dev", "b@acme.dev"} {
		person := authn.Person{User: p, Groups: []string{"team-pay"}}
		_, _ = review(signedInAs(t, person, memberToken(p)), person, "prod", authn.PactoResource)
	}
	n := len(e.sentTo(usUID))
	_, _ = review(signedInAs(t, alicePerson, memberToken("c")), alicePerson, "prod", authn.PactoResource)
	sent := e.sentTo(usUID)
	expect(t, len(sent) == n+1 && sent[n].Token == "", "marked: straight to the mapping")
	clock.Add(int64((10*time.Minute + time.Second) / time.Second))
	_, _ = review(signedInAs(t, alicePerson, memberToken("c")), alicePerson, "prod", authn.PactoResource)
	sent = e.sentTo(usUID)
	expect(t, len(sent) >= n+2 && sent[n+1].Token != "", "after 10 minutes a token is sent again: %+v", sent[n+1:])
}

func TestApprovedNeverFetchesPerHello(t *testing.T) {
	var calls atomic.Int32
	e := newMemberEnv(t, func(_ context.Context, _, uid string) (string, error) { return memberToken(uid), nil }, nil)
	e.approve(usUID)
	e.mu.Lock()
	k := e.keys[usUID]
	e.mu.Unlock()
	list := func(context.Context) delivery.ClusterJoinList {
		calls.Add(1)
		der, _ := x509.MarshalPKIXPublicKey(k.Public())
		return delivery.ClusterJoinList{Completeness: fleet.CompletenessComplete, Clusters: []delivery.ClusterJoinState{{UID: usUID, State: delivery.JoinApproved,
			ApprovedKeyID: signing.Hint(k.Public()), ApprovedPublicKey: base64.StdEncoding.EncodeToString(der)}}}
	}
	e.m.approvals.interval = time.Hour
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go e.m.approvals.run(ctx, list)
	for calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	for range 20 {
		e.link(usUID, allowAll)
		_, _ = e.m.add(nil)[usUID](signedInAs(t, alicePerson, memberToken("a")), alicePerson, "prod", authn.PactoResource)
		e.unlink(usUID)
	}
	expect(t, calls.Load() == 1, "20 hellos and 20 reviews read the registry %d times beyond the refresher's first", calls.Load()-1)
}

func TestUnauthenticatedDashboardUnfilteredWithMembers(t *testing.T) {
	e := newMemberEnv(t, nil, nil)
	e.link(usUID, allowAll)
	v := viper.New()
	v.Set("dashboard.allow-anonymous", true)
	id, err := newDashboardIdentity(v, "0.0.0.0", nil, entriesOf(), defaultDeps(), e.m)
	expect(t, err == nil && id.viewer == nil, "no sign-in, no viewer: every member stays visible: %v", err)
}
