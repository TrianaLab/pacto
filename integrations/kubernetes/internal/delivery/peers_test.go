/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"oras.land/oras-go/v2/registry/remote"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	peerHub = "hub00000-0000-4000-8000-000000000000"
	peerEU  = "aaaa0000-0000-4000-8000-00000000000a"
	peerUS  = "bbbb0000-0000-4000-8000-00000000000b"
)

// peerEnv is a hub promoter over a pacto-targets repository in an
// in-process registry, approver signers of a private Sigstore and an
// apiserver that lets the users in allow approve clusterjoins.
type peerEnv struct {
	reg      *testRegistry
	path     string
	ss       *sigtest.Sigstore
	policy   delivery.ClassPolicy
	now      time.Time
	c        client.Client
	requests atomic.Int64
	// down fails every registry request.
	down      atomic.Bool
	mu        sync.Mutex
	allow     map[string]bool
	reviewErr map[string]error
	reviews   int
}

func newPeerEnv(t *testing.T) *peerEnv {
	t.Helper()
	e := &peerEnv{reg: newTestRegistry(t), ss: sigtest.New(t), now: time.Now().UTC().Truncate(time.Second),
		allow: map[string]bool{"oidc:alice@acme.com": true}, reviewErr: map[string]error{}}
	e.path = e.reg.host + "/acme/" + delivery.TargetSnapshotRepository
	e.policy = withSigners(delivery.ClassPolicy{}, e.ss)
	e.reg.fault = func(*http.Request) bool { e.requests.Add(1); return e.down.Load() }
	e.c = interceptor.NewClient(fake.NewClientBuilder().WithScheme(newScheme()).Build(), interceptor.Funcs{
		Create: func(_ context.Context, _ client.WithWatch, o client.Object, _ ...client.CreateOption) error {
			sar := o.(*authorizationv1.SubjectAccessReview)
			a := sar.Spec.ResourceAttributes
			if a.Group != "pacto.trianalab.io" || a.Resource != "clusterjoins" || a.Verb != "approve" || a.Name == "" || a.Namespace != "" || len(sar.Spec.Groups) != 0 {
				return errors.New("asked the wrong review")
			}
			e.mu.Lock()
			defer e.mu.Unlock()
			e.reviews++
			if err := e.reviewErr[sar.Spec.User]; err != nil {
				return err
			}
			sar.Status.Allowed = e.allow[sar.Spec.User]
			return nil
		}})
	return e
}

func (e *peerEnv) options(member bool) PeerOptions {
	return PeerOptions{Repository: e.path, ClusterID: peerHub, Member: member, Policy: e.policy, Client: e.c, Now: func() time.Time { return e.now }}
}

func (e *peerEnv) reader(member bool) *peerReader { return &peerReader{o: e.options(member)} }

// peer is a cluster that publishes to the repository with its own key.
type peer struct {
	uid, name string
	key       ed25519.PrivateKey
}

func newPeer(uid, name string) peer {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	return peer{uid: uid, name: name, key: key}
}

func (p peer) join() *delivery.SnapshotJoin {
	der, _ := x509.MarshalPKIXPublicKey(p.key.Public())
	return &delivery.SnapshotJoin{Name: p.name, NameSource: fleet.ClusterNameFromNodeLabel, KeyID: signing.Hint(p.key.Public()),
		PublicKey: base64.StdEncoding.EncodeToString(der)}
}

// publish tags p's snapshot, taken at, holding pactos.
func (e *peerEnv) publish(t *testing.T, p peer, at time.Time, pactos ...delivery.PactoView) {
	t.Helper()
	s := delivery.TargetSnapshot{Cluster: p.uid, AsOf: at.Format(time.RFC3339), Completeness: fleet.CompletenessComplete,
		Publisher: "pacto-system/pacto-operator-delivery-recorder", Join: p.join(), Pactos: pactos}
	kp, _ := signing.FromPrivateKey(p.key)
	b, err := delivery.SignTargetSnapshot(t.Context(), signing.Keyed(kp, ""), delivery.NewTargetSnapshotStatement(s))
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := ocireferrer.Open(e.path, ocireferrer.Options{})
	if _, err := deliveryrecord.PushTargetSnapshot(t.Context(), repo, p.uid, b); err != nil {
		t.Fatal(err)
	}
}

// decide writes p's join record deciding dec, signed keyless by email.
func (e *peerEnv) decide(t *testing.T, p peer, dec delivery.JoinDecision, email string) {
	t.Helper()
	j := p.join()
	rec := delivery.ClusterJoin{Cluster: p.uid, Hub: peerHub, KeyID: j.KeyID, PublicKey: j.PublicKey, Decision: dec,
		Approver: "oidc:" + email, At: e.now.Add(-time.Hour).Format(time.RFC3339)}
	s, err := signing.Keyless(e.ss.Fulcio, e.ss.TSA, "", func(context.Context) (string, error) { return sigtest.Token(email, personIssuer), nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := delivery.SignClusterJoin(t.Context(), s, delivery.NewClusterJoinStatement(rec))
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := ocireferrer.Open(e.path, ocireferrer.Options{})
	if _, _, err := deliveryrecord.PushClusterJoin(t.Context(), repo, b); err != nil {
		t.Fatal(err)
	}
}

// ledgerView is a reconciled Pacto of ledger, in environment env when set.
func ledgerView(t *testing.T, env string) delivery.PactoView {
	t.Helper()
	u := pactoObj("ledger", "ledger", c3, "Compliant", 1)
	if env != "" {
		u.SetLabels(map[string]string{delivery.EnvironmentLabel: env})
	}
	return pactoView(t, u.Object)
}

// pactoView decodes obj, a Pacto, as the promoter reads it.
func pactoView(t *testing.T, obj map[string]any) delivery.PactoView {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"items": []any{obj}})
	v, err := delivery.DecodePactos(b)
	if err != nil || len(v) != 1 {
		t.Fatalf("%v %v", v, err)
	}
	return v[0]
}

// collect is each source of m by id.
func collect(t *testing.T, m delivery.Members) map[string]*fleet.Collection {
	t.Helper()
	out := map[string]*fleet.Collection{}
	for _, src := range m.Sources {
		col, err := src.Collect(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		out[src.ID()] = col
	}
	return out
}

// TestOperatorMembersApprovedOnly: the promoter reads the member a person
// approved, in its cluster, counts the pending one and puts its own Pactos
// in its own cluster.
func TestOperatorMembersApprovedOnly(t *testing.T) {
	e := newPeerEnv(t)
	eu, us := newPeer(peerEU, "eu-prod"), newPeer(peerUS, "us-prod")
	e.publish(t, eu, e.now, ledgerView(t, ""), ledgerView(t, "staging"))
	e.publish(t, us, e.now)
	e.decide(t, eu, delivery.JoinApprove, "alice@acme.com")
	m := e.reader(false).members(t.Context(), "prod", map[string]bool{"prod": true, "staging": true})
	cols := collect(t, m)
	member, unread := cols["member-"+peerEU], cols[delivery.UnreadClustersSourceID]
	if m.Home == nil || m.Home.ID != peerHub || len(cols) != 2 || member == nil || unread == nil {
		t.Fatalf("home %+v, sources %v", m.Home, cols)
	}
	if member.State.Status != fleet.SourceAvailable || member.Cluster.Name != "eu-prod" || len(member.Targets) != 1 || unread.UnreadClusters != 1 {
		t.Fatalf("member %+v %d targets, unread %+v", member.State, len(member.Targets), unread)
	}
}

// TestOperatorMembersSkipsPending: a cluster whose approver may not approve
// stays pending: never a source, only counted.
func TestOperatorMembersSkipsPending(t *testing.T) {
	e := newPeerEnv(t)
	eu, us := newPeer(peerEU, "eu-prod"), newPeer(peerUS, "us-prod")
	e.publish(t, eu, e.now)
	e.publish(t, us, e.now, ledgerView(t, ""))
	e.decide(t, eu, delivery.JoinApprove, "alice@acme.com")
	e.decide(t, us, delivery.JoinApprove, "mallory@acme.com")
	cols := collect(t, e.reader(false).members(t.Context(), "prod", nil))
	if cols["member-"+peerUS] != nil || cols["member-"+peerEU] == nil || cols[delivery.UnreadClustersSourceID].UnreadClusters != 1 {
		t.Fatalf("%v", cols)
	}
}

// TestOperatorReviewFailureCountsUnread: an approval the hub could not
// review leaves its cluster pending and the list partial: unknown, never a
// member.
func TestOperatorReviewFailureCountsUnread(t *testing.T) {
	e := newPeerEnv(t)
	eu, us := newPeer(peerEU, "eu-prod"), newPeer(peerUS, "us-prod")
	e.publish(t, eu, e.now)
	e.publish(t, us, e.now)
	e.decide(t, eu, delivery.JoinApprove, "alice@acme.com")
	e.decide(t, us, delivery.JoinApprove, "dave@acme.com")
	e.reviewErr["oidc:dave@acme.com"] = errors.New("apiserver down")
	cols := collect(t, e.reader(false).members(t.Context(), "prod", nil))
	if cols["member-"+peerUS] != nil || cols[delivery.UnreadClustersSourceID].UnreadClusters != 1 ||
		cols["cluster-joins"] == nil || cols["cluster-joins"].State.Status != fleet.SourcePartial {
		t.Fatalf("%v", cols)
	}
	e.reviewErr["oidc:dave@acme.com"] = apierrors.NewForbidden(schema.GroupResource{Group: "authorization.k8s.io", Resource: "subjectaccessreviews"}, "", errors.New("no"))
	cols = collect(t, e.reader(false).members(t.Context(), "prod", nil))
	if cols["member-"+peerUS] != nil || !slices.ContainsFunc(cols["cluster-joins"].Limitations, func(l fleet.Limitation) bool {
		return strings.Contains(l.Message, "may not ask the hub")
	}) {
		t.Fatalf("%v", cols["cluster-joins"].Limitations)
	}
}

// TestSARReviewCouldNotBeEvaluated: a refusal that carries an evaluation
// error is no answer, and an answer is remembered for a minute.
func TestSARReviewCouldNotBeEvaluated(t *testing.T) {
	e := newPeerEnv(t)
	c := interceptor.NewClient(fake.NewClientBuilder().WithScheme(newScheme()).Build(), interceptor.Funcs{
		Create: func(_ context.Context, _ client.WithWatch, o client.Object, _ ...client.CreateOption) error {
			o.(*authorizationv1.SubjectAccessReview).Status.EvaluationError = "webhook timed out"
			return nil
		}})
	p := &peerReader{o: PeerOptions{Client: c, Now: func() time.Time { return e.now }}}
	if _, err := p.review(t.Context(), "oidc:alice@acme.com", peerEU); err == nil || !strings.Contains(err.Error(), "webhook timed out") {
		t.Fatal(err)
	}
	r := e.reader(false)
	for range 2 {
		if ok, err := r.review(t.Context(), "oidc:alice@acme.com", peerEU); !ok || err != nil {
			t.Fatal(ok, err)
		}
	}
	if e.reviews != 1 {
		t.Fatalf("an answer is remembered: %d reviews", e.reviews)
	}
	e.now = e.now.Add(2 * time.Minute)
	maxed := map[string]peerVerdict{}
	for i := range maxPeerVerdicts {
		maxed[string(rune(i))] = peerVerdict{}
	}
	r.verdicts = maxed
	_, _ = r.review(t.Context(), "oidc:alice@acme.com", peerEU)
	if e.reviews != 2 || len(r.verdicts) != 1 {
		t.Fatalf("an expired answer is asked again and a full cache cleared: %d reviews, %d kept", e.reviews, len(r.verdicts))
	}
}

// TestOperatorMembersCached: one join list per period serves every promote,
// in any environment, and it is read again once the period passed.
func TestOperatorMembersCached(t *testing.T) {
	e := newPeerEnv(t)
	eu := newPeer(peerEU, "eu-prod")
	e.publish(t, eu, e.now, ledgerView(t, ""))
	e.decide(t, eu, delivery.JoinApprove, "alice@acme.com")
	r := e.reader(false)
	first := r.members(t.Context(), "prod", nil)
	n := e.requests.Load()
	if again := r.members(t.Context(), "prod", nil); !reflect.DeepEqual(again, first) || e.requests.Load() != n {
		t.Fatalf("a second promote in the period read the registry: %d -> %d", n, e.requests.Load())
	}
	r.members(t.Context(), "staging", nil)
	afterStaging := e.requests.Load()
	if l := joinsOf(t.Context(), r); len(l.Clusters) != 1 || afterStaging == n || e.requests.Load() != afterStaging {
		t.Fatalf("another environment reads only the member, never the list: %+v", l)
	}
	e.now = e.now.Add(peerListTTL + time.Second)
	joinsOf(t.Context(), r)
	if e.requests.Load() == afterStaging {
		t.Fatal("the list is read again once the period passed")
	}
}

// TestMemberPromoterReadsNoJoins: a member's promoter reads no join record
// and counts other clusters as unread.
func TestMemberPromoterReadsNoJoins(t *testing.T) {
	e := newPeerEnv(t)
	m := e.reader(true).members(t.Context(), "prod", nil)
	cols := collect(t, m)
	if e.requests.Load() != 0 || m.Home != nil || len(cols) != 1 || cols[delivery.MemberUnreadID].UnreadClusters != 1 {
		t.Fatalf("%d requests, %+v %v", e.requests.Load(), m, cols)
	}
}

// TestOperatorNoRegistryNoMembers: a promoter with no registry projects onto
// its own Pactos only.
func TestOperatorNoRegistryNoMembers(t *testing.T) {
	o := PeerOptions{ClusterID: peerHub}
	if m := PeerMembers(o)(t.Context(), "prod", nil); m.Home != nil || m.Sources != nil {
		t.Fatalf("%+v", m)
	}
	c := Cluster{Members: PeerMembers(PeerOptions{Member: true})}
	if m := (planSources{c}).Members(t.Context(), "prod", nil); len(m.Sources) != 1 {
		t.Fatalf("plan sources read the cluster's members: %+v", m)
	}
}

// TestNoHubRoleNoUnreadCount: until a person approved or removed a member,
// clusters that only ask to join change nothing.
func TestNoHubRoleNoUnreadCount(t *testing.T) {
	e := newPeerEnv(t)
	e.publish(t, newPeer(peerEU, "eu-prod"), e.now)
	e.publish(t, newPeer(peerUS, "us-prod"), e.now)
	if m := e.reader(false).members(t.Context(), "prod", nil); m.Sources != nil {
		t.Fatalf("%v", collect(t, m))
	}
	e.policy = delivery.ClassPolicy{}
	if l := joinsOf(t.Context(), e.reader(false)); l.Completeness != fleet.CompletenessComplete || !strings.Contains(l.Clusters[0].Reason, "accepts no signed approvals") {
		t.Fatalf("a hub with no signers: %+v", l)
	}
}

// readJoinsAsTheRootModule reads e's list with the moved core called as the
// CLI calls it, apart from its caches and credentials.
func (e *peerEnv) readJoinsAsTheRootModule(t *testing.T) (delivery.ClusterJoinList, *remote.Repository) {
	t.Helper()
	authorize := func(ctx context.Context, user, uid string) (bool, error) {
		return (&peerReader{o: e.options(false)}).review(ctx, user, uid)
	}
	return deliveryrecord.ReadJoins(t.Context(), deliveryrecord.JoinRead{Repository: e.path, Hub: peerHub, Policy: e.policy, Records: &deliveryrecord.Reader{},
		Authorize: authorize, Fetch: func(ctx context.Context, repo *remote.Repository, ref string) ([]byte, string, error) {
			b, d, err := deliveryrecord.FetchTargetSnapshot(ctx, repo, ref)
			return b, e.path + "@" + d.Digest.String(), err
		}, Forbidden: apierrors.IsForbidden, VerifyPerson: signing.VerifyPersonRecord, OpenKey: signing.OpenKey,
		Open: func(p string) (*remote.Repository, error) { return ocireferrer.Open(p, ocireferrer.Options{}) }, Now: e.now})
}

// parityFixture is an approved eu-prod with a ledger, a pending us-prod and
// a removed member.
func parityFixture(t *testing.T) *peerEnv {
	t.Helper()
	e := newPeerEnv(t)
	eu, us, gone := newPeer(peerEU, "eu-prod"), newPeer(peerUS, "us-prod"), newPeer("cccc0000-0000-4000-8000-00000000000c", "ap-dev")
	e.publish(t, eu, e.now, ledgerView(t, ""))
	e.publish(t, us, e.now)
	e.publish(t, gone, e.now)
	e.decide(t, eu, delivery.JoinApprove, "alice@acme.com")
	e.decide(t, gone, delivery.JoinRemove, "alice@acme.com")
	return e
}

// TestPeerReaderMembersEqualClusterJoins: the promoter's join list is the
// list the hub's surfaces read through the same core.
func TestPeerReaderMembersEqualClusterJoins(t *testing.T) {
	e := parityFixture(t)
	want, _ := e.readJoinsAsTheRootModule(t)
	got := joinsOf(t.Context(), e.reader(false))
	if len(got.Clusters) != 3 || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// TestPromoterMembersEqualMembersOf: the promoter's members are MembersOf
// over that list, as the client computes them.
func TestPromoterMembersEqualMembersOf(t *testing.T) {
	e := parityFixture(t)
	list, repo := e.readJoinsAsTheRootModule(t)
	trust := delivery.JoinTrust(list.Clusters)
	want := collect(t, delivery.MembersOf(list, func(uid string) (delivery.TargetSnapshot, error) {
		b, _, err := deliveryrecord.FetchTargetSnapshot(t.Context(), repo, delivery.TargetSnapshotTagPrefix+uid)
		if err != nil {
			return delivery.TargetSnapshot{}, err
		}
		return deliveryrecord.OpenMember(b, trust, signing.OpenKey, uid)
	}, "prod", nil, e.now, &fleet.Cluster{ID: peerHub}))
	got := collect(t, e.reader(false).members(t.Context(), "prod", nil))
	if len(got) != 2 || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// TestOperatorMemberSnapshotUnreadable: a member whose snapshot went away
// is unavailable, so its consumers are unknown.
func TestOperatorMemberSnapshotUnreadable(t *testing.T) {
	e := newPeerEnv(t)
	eu := newPeer(peerEU, "eu-prod")
	e.publish(t, eu, e.now)
	e.decide(t, eu, delivery.JoinApprove, "alice@acme.com")
	r := e.reader(false)
	joinsOf(t.Context(), r)
	e.down.Store(true)
	if col := collect(t, r.members(t.Context(), "prod", nil))["member-"+peerEU]; col == nil || col.State.Status != fleet.SourceUnavailable {
		t.Fatalf("%+v", col)
	}
}

// checkoutDep is checkout's required dependency on payments in rng.
func checkoutDep(rng string) map[string]any {
	return map[string]any{"name": "payments", "ref": "oci://reg.example/pactos/payments", "required": true, "compatibility": rng}
}

// TestOperatorRegistryDownHoldsBreakingPromote: a breaking promote the hub
// alone would let through asks a person while the join list cannot be read.
func TestOperatorRegistryDownHoldsBreakingPromote(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dC, bot))
	e.create(pactoObj("checkout", "checkout", c3, "Compliant", 1, checkoutDep(">=1.0.0")))
	e.create(opObj("p0", delivery.TypePromote, dA, dC, bot))
	alone, err := e.op.plan(t.Context(), e.getOp("p0"), e.getStage())
	if err != nil || alone.Floor.Human {
		t.Fatalf("the hub alone lets it through: %v %+v", err, alone.Floor)
	}
	pe := newPeerEnv(t)
	pe.down.Store(true)
	e.op.Cluster.Members = PeerMembers(pe.options(false))
	o := e.drive("p1")
	if o.Status.Phase != delivery.PhaseHeld || o.Status.Plan.Floor.Decision != delivery.AcceptUnknown {
		t.Fatalf("an unread join list holds a breaking promote: %s %+v", o.Status.Phase, o.Status.Plan.Floor)
	}
}

// TestMemberConsumerIncompatibleAsksItsOwnersOnThePromoter: a consumer in an
// approved member that does not accept the change asks its own team.
func TestMemberConsumerIncompatibleAsksItsOwnersOnThePromoter(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dC, bot))
	u := pactoObj("checkout", "checkout", c3, "Compliant", 1, checkoutDep("^1.0.0"))
	view := pactoView(t, u.Object)
	list := delivery.ClusterJoinList{Completeness: fleet.CompletenessComplete, Clusters: []delivery.ClusterJoinState{{UID: peerUS, Name: "us-prod", State: delivery.JoinApproved}}}
	e.op.Cluster.Members = func(_ context.Context, env string, _ map[string]bool) delivery.Members {
		return delivery.MembersOf(list, func(string) (delivery.TargetSnapshot, error) {
			return delivery.TargetSnapshot{Cluster: peerUS, AsOf: e.now.Format(time.RFC3339), Completeness: fleet.CompletenessComplete, Pactos: []delivery.PactoView{view}}, nil
		}, env, nil, e.now, &fleet.Cluster{ID: "cid"})
	}
	o := e.drive("p1")
	if o.Status.Phase != delivery.PhaseHeld || o.Status.Plan.Predicted != delivery.PredictHeld || o.Status.Plan.Floor.Decision != delivery.Approve ||
		strings.Join(o.Status.Plan.Floor.Owners, ",") != "team-checkout" {
		t.Fatalf("want Held for team-checkout: %s %+v", o.Status.Phase, o.Status.Plan.Floor)
	}
}

// joinsOf is r's join list, read as members reads it.
func joinsOf(ctx context.Context, r *peerReader) delivery.ClusterJoinList {
	r.mu.Lock()
	defer r.mu.Unlock()
	l, _ := r.joinsLocked(ctx)
	return l
}
