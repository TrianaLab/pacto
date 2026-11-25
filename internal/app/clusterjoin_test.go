package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	joinHub    = "hub00000-0000-4000-8000-000000000000"
	joinIssuer = "https://idp.acme.example"
	memberA    = "aaaa0000-0000-4000-8000-00000000000a"
	memberB    = "bbbb0000-0000-4000-8000-00000000000b"
)

// joinEnv is a hub's view of a pacto-targets repository: an in-process
// registry, a private Sigstore whose root the hub's approver signer trusts,
// and a reviewer that allows the users in allow.
type joinEnv struct {
	repo   string
	mode   capabilities.ReferrersMode
	ss     *sigtest.Sigstore
	policy delivery.ClassPolicy
	svc    *Service
	clock  time.Time
	// deny answers the tag listing with 403; failReferrers answers
	// referrer listings with 500; tagPage forces one tag per page.
	deny, failReferrers, tagPage atomic.Bool
	// fail answers the requests it matches with 500.
	fail      atomic.Pointer[func(*http.Request) bool]
	mu        sync.Mutex
	allow     map[string]bool
	reviewErr error
	// errFor fails the reviews of one user.
	errFor  map[string]error
	reviews int
}

func newJoinEnv(t *testing.T, mode capabilities.ReferrersMode) *joinEnv {
	t.Helper()
	e := &joinEnv{mode: mode, ss: sigtest.New(t), clock: snapNow, allow: map[string]bool{"oidc:alice@acme.com": true}}
	inner := testutil.NewReferrersRegistry(testutil.ReferrersOptions{Unsupported: mode == capabilities.ReferrersWriteOnceTags, TagsLink: true})
	srv := probedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch f := e.fail.Load(); {
		case f != nil && (*f)(r):
			http.Error(w, "down", http.StatusInternalServerError)
			return
		case e.deny.Load() && strings.HasSuffix(r.URL.Path, "/tags/list"):
			http.Error(w, `{"errors":[{"code":"DENIED"}]}`, http.StatusForbidden)
			return
		case e.failReferrers.Load() && strings.Contains(r.URL.Path, "/referrers/"):
			http.Error(w, "down", http.StatusInternalServerError)
			return
		case e.tagPage.Load() && strings.HasSuffix(r.URL.Path, "/tags/list") && r.URL.Query().Get("n") == "":
			q := r.URL.Query()
			q.Set("n", "1")
			r.URL.RawQuery = q.Encode()
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	e.repo = strings.TrimPrefix(srv.URL, "http://") + "/acme/" + delivery.TargetSnapshotRepository
	e.policy = delivery.ClassPolicy{Signers: []delivery.PersonSigner{{Issuer: joinIssuer, UsernamePrefix: "oidc:", Subjects: `.*@acme\.com|.*gserviceaccount\.com|.*\.svc\.id\.goog`,
		TrustedRoot: string(e.ss.RootJSON)}}}
	e.svc = &Service{}
	e.svc.sys.now = func() time.Time { return e.clock }
	return e
}

func (e *joinEnv) authorize(_ context.Context, p authn.Person, ns string, res authn.ResourceAttributes) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reviews++
	if ns != "" || len(p.Groups) != 0 || res.Verb != "approve" || res.Resource != "clusterjoins" || res.Name == "" {
		return false, fmt.Errorf("asked %s %+v in %q for %+v", p.User, res, ns, p.Groups)
	}
	if err := e.errFor[p.User]; err != nil {
		return false, err
	}
	return e.allow[p.User], e.reviewErr
}

func (e *joinEnv) opts() JoinOptions {
	return JoinOptions{Repository: e.repo, Hub: joinHub, Authorize: e.authorize, Delivery: &Delivery{Policy: e.policy}}
}

func (e *joinEnv) fleetOpts() FleetOptions {
	o := e.opts()
	return FleetOptions{ClusterJoins: &o, Now: func() time.Time { return e.clock }}
}

// member is a cluster that publishes to the repository with its own key.
type member struct {
	uid, name string
	key       ed25519.PrivateKey
}

func newMember(uid, name string) member {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	return member{uid: uid, name: name, key: key}
}

// join is m's join block.
func (m member) join() *delivery.SnapshotJoin {
	der, _ := x509.MarshalPKIXPublicKey(m.key.Public())
	return &delivery.SnapshotJoin{Name: m.name, NameSource: fleet.ClusterNameFromNodeLabel, KeyID: signing.Hint(m.key.Public()),
		PublicKey: base64.StdEncoding.EncodeToString(der), Environments: []delivery.AdvertisedEnvironment{{Name: "production", Source: "the cluster name " + m.name}}}
}

// publish signs m's snapshot (asOf at) with signer and tags it.
func (e *joinEnv) publish(t *testing.T, m member, signer ed25519.PrivateKey, at time.Time) {
	t.Helper()
	s := ledgerSnapshot(m.uid, at)
	s.Join = m.join()
	pushAt(t, e.repo, signer, m.uid, s)
}

// signRecord signs a join record as email from issuer.
func (e *joinEnv) signRecord(t *testing.T, email, issuer string, j delivery.ClusterJoin) []byte {
	t.Helper()
	s, err := signing.Keyless(e.ss.Fulcio, e.ss.TSA, "", func(context.Context) (string, error) { return sigtest.Token(email, issuer), nil }, e.ss.Root)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(delivery.NewClusterJoinStatement(j))
	b, err := s.Sign(t.Context(), delivery.PayloadType, payload)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// pushRecord writes bundle on uid's join stream.
func (e *joinEnv) pushRecord(t *testing.T, uid string, bundle []byte, ann map[string]string) {
	t.Helper()
	repo, _ := ocireferrer.Open(e.repo, ocireferrer.Options{})
	stream := delivery.ClusterJoinStream(uid)
	anchor := deliveryrecord.Anchor(stream)
	if err := ocireferrer.Push(t.Context(), repo, anchor); err != nil {
		t.Fatal(err)
	}
	if _, err := deliveryrecord.Push(t.Context(), repo, stream, e.mode, delivery.PredicateClusterJoin, bundle, ann); err != nil {
		t.Fatal(err)
	}
}

// record is m's join record deciding dec, approved as alice.
func (m member) record(dec delivery.JoinDecision) delivery.ClusterJoin {
	j := m.join()
	return delivery.ClusterJoin{Cluster: m.uid, Hub: joinHub, KeyID: j.KeyID, PublicKey: j.PublicKey, Decision: dec, Approver: "oidc:alice@acme.com", At: "2026-11-17T09:00:00Z"}
}

// decide writes m's record deciding dec, signed by alice.
func (e *joinEnv) decide(t *testing.T, m member, dec delivery.JoinDecision) {
	t.Helper()
	e.pushRecord(t, m.uid, e.signRecord(t, "alice@acme.com", joinIssuer, m.record(dec)), nil)
}

func stateOfUID(t *testing.T, l delivery.ClusterJoinList, uid string) delivery.ClusterJoinState {
	t.Helper()
	for _, c := range l.Clusters {
		if c.UID == uid {
			return c
		}
	}
	t.Fatalf("no cluster %s in %+v", uid, l)
	return delivery.ClusterJoinState{}
}

func (e *joinEnv) build(t *testing.T) *fleet.FleetSnapshot {
	t.Helper()
	snap, err := e.svc.Fleet(t.Context(), e.fleetOpts())
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// clustersOf are the cluster ids of snap's targets.
func clustersOf(snap *fleet.FleetSnapshot) map[string]bool {
	out := map[string]bool{}
	for _, tg := range snap.Targets {
		if tg.Cluster != "" {
			out[tg.Cluster] = true
		}
	}
	return out
}

var bothModes = []capabilities.ReferrersMode{capabilities.ReferrersNative, capabilities.ReferrersWriteOnceTags}

func TestPendingMemberIsNoSource(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	l := e.svc.ClusterJoins(t.Context(), e.opts())
	if st := stateOfUID(t, l, m.uid); st.State != delivery.JoinPending || st.Reason != "no approval yet" || st.Name != "eu-prod" || st.KeyID != m.join().KeyID ||
		len(st.Environments) != 1 || l.Completeness != fleet.CompletenessComplete {
		t.Fatalf("%+v %+v", st, l)
	}
	snap := e.build(t)
	if len(snap.Targets) != 0 || len(snap.Sources) != 0 {
		t.Fatalf("a pending member reached the fleet: %d targets, %+v", len(snap.Targets), snap.Sources)
	}
}

func TestApprovedMemberIsVerifiedScopedSource(t *testing.T) {
	t.Parallel()
	for _, mode := range bothModes {
		t.Run(string(mode), func(t *testing.T) {
			e := newJoinEnv(t, mode)
			m := newMember(memberA, "eu-prod")
			e.publish(t, m, m.key, snapNow)
			e.decide(t, m, delivery.JoinApprove)
			l := e.svc.ClusterJoins(t.Context(), e.opts())
			if st := stateOfUID(t, l, m.uid); st.State != delivery.JoinApproved || st.Approver != "oidc:alice@acme.com" || st.Record == "" {
				t.Fatalf("%+v", st)
			}
			snap := e.build(t)
			st := stateOf(t, snap, "targets-"+m.uid)
			if st.Status != fleet.SourceAvailable || st.Signer != "member:eu-prod" || st.SignerUnscoped || st.Cluster == nil || st.Cluster.Name != "eu-prod" ||
				st.Cluster.NameSource != fleet.ClusterNameFromNodeLabel || !clustersOf(snap)[m.uid] {
				t.Fatalf("%+v", st)
			}
			for _, l := range snap.Limitations {
				if l.Code == fleet.LimitationTargetSnapshotUnverified || l.Code == fleet.LimitationTargetSnapshotUnscoped {
					t.Fatalf("limitation %+v", l)
				}
			}
		})
	}
}

func TestRemovedMemberDisappearsNextBuild(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	if !clustersOf(e.build(t))[m.uid] {
		t.Fatal("the approved member is not in the fleet")
	}
	e.decide(t, m, delivery.JoinRemove)
	if st := stateOfUID(t, e.svc.ClusterJoins(t.Context(), e.opts()), m.uid); st.State != delivery.JoinRemoved {
		t.Fatalf("%+v", st)
	}
	if snap := e.build(t); len(snap.Targets) != 0 {
		t.Fatalf("a removed member is in the fleet: %d targets", len(snap.Targets))
	}
}

func TestRotatedKeyReadsPending(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.decide(t, m, delivery.JoinApprove)
	rotated := m
	_, rotated.key, _ = ed25519.GenerateKey(rand.Reader)
	e.publish(t, rotated, rotated.key, snapNow)
	st := stateOfUID(t, e.svc.ClusterJoins(t.Context(), e.opts()), m.uid)
	if st.State != delivery.JoinPending || !strings.Contains(st.Reason, "the key changed from sha256:"+m.join().KeyID[:12]) {
		t.Fatalf("%+v", st)
	}
	if len(e.build(t).Targets) != 0 {
		t.Fatal("a rotated key reached the fleet")
	}
}

func TestRebuiltClusterMayReplace(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	old, rebuilt := newMember(memberA, "eu-prod"), newMember(memberB, "eu-prod")
	e.publish(t, old, old.key, snapNow)
	e.decide(t, old, delivery.JoinApprove)
	e.publish(t, rebuilt, rebuilt.key, snapNow)
	st := stateOfUID(t, e.svc.ClusterJoins(t.Context(), e.opts()), rebuilt.uid)
	if st.State != delivery.JoinPending || st.MayReplace == nil || st.MayReplace.UID != old.uid || st.MayReplace.Name != "eu-prod" {
		t.Fatalf("%+v", st)
	}
}

// approvedBy publishes m and an approval of it signed as email from issuer
// claiming approver, and returns m's state.
func (e *joinEnv) approvedBy(t *testing.T, email, issuer, approver string) delivery.ClusterJoinState {
	t.Helper()
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	j := m.record(delivery.JoinApprove)
	j.Approver = approver
	e.pushRecord(t, m.uid, e.signRecord(t, email, issuer, j), nil)
	return stateOfUID(t, e.svc.ClusterJoins(t.Context(), e.opts()), m.uid)
}

func TestJoinApproverIssuerBound(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	st := e.approvedBy(t, "alice@acme.com", "https://other-idp.example", "oidc:alice@acme.com")
	if st.State != delivery.JoinPending || !strings.Contains(st.Reason, "not signed by a certificate from "+joinIssuer) {
		t.Fatalf("%+v", st)
	}
}

func TestJoinApproverMachineIdentityRefused(t *testing.T) {
	t.Parallel()
	for _, sa := range []string{"deployer@acme.iam.gserviceaccount.com", "ns.svc.id.goog@acme.com"} {
		e := newJoinEnv(t, capabilities.ReferrersNative)
		e.allow["oidc:"+sa] = true
		st := e.approvedBy(t, sa, joinIssuer, "oidc:"+sa)
		if st.State != delivery.JoinPending || !strings.Contains(st.Reason, "agents cannot approve") {
			t.Fatalf("%s: %+v", sa, st)
		}
	}
}

func TestApprovalByUserWhoMayNotApproveNotCounted(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	st := e.approvedBy(t, "bob@acme.com", joinIssuer, "oidc:bob@acme.com")
	if st.State != delivery.JoinPending || !strings.Contains(st.Reason, "an approval by oidc:bob@acme.com was not counted: oidc:bob@acme.com may not approve clusterjoins") {
		t.Fatalf("%+v", st)
	}
}

func TestApprovalClaimingAnotherUserNotCounted(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	st := e.approvedBy(t, "bob@acme.com", joinIssuer, "oidc:alice@acme.com")
	if st.State != delivery.JoinPending || !strings.Contains(st.Reason, "claiming approver oidc:alice@acme.com is signed as oidc:bob@acme.com") {
		t.Fatalf("%+v", st)
	}
}

func TestApprovalForAnotherHubIgnored(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	j := m.record(delivery.JoinApprove)
	j.Hub = "another-hub"
	e.pushRecord(t, m.uid, e.signRecord(t, "alice@acme.com", joinIssuer, j), nil)
	l := e.svc.ClusterJoins(t.Context(), e.opts())
	if st := stateOfUID(t, l, m.uid); st.State != delivery.JoinPending || l.OtherHubs != 1 || e.reviews != 0 {
		t.Fatalf("%+v, other hubs %d, reviews %d", st, l.OtherHubs, e.reviews)
	}
}

func TestSelfSignatureMismatchNeverApprovable(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	e.publish(t, m, other, snapNow) // names m's key, signed by another
	e.decide(t, m, delivery.JoinApprove)
	st := stateOfUID(t, e.svc.ClusterJoins(t.Context(), e.opts()), m.uid)
	if st.State != delivery.JoinPending || !strings.HasPrefix(st.Reason, "the snapshot is not signed by the key it names") {
		t.Fatalf("%+v", st)
	}
	if len(e.build(t).Targets) != 0 {
		t.Fatal("a snapshot its own key did not sign reached the fleet")
	}
}

func TestNoSignersConfiguredKeepsPending(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	e.policy = delivery.ClassPolicy{}
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	l := e.svc.ClusterJoins(t.Context(), e.opts())
	// The approval cannot be judged, so the member it may admit reads unknown: partial.
	if st := stateOfUID(t, l, m.uid); st.State != delivery.JoinPending || st.Reason != noSigners || l.Completeness != fleet.CompletenessPartial || len(l.Limitations) != 2 {
		t.Fatalf("%+v %+v", st, l)
	}
}

func TestSignerPolicyFailureKeepsPending(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	o := e.opts()
	o.Delivery = &Delivery{Policy: e.policy, PolicyNamespace: "pacto-delivery", Dynamic: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())}
	l := e.svc.ClusterJoins(t.Context(), o)
	if st := stateOfUID(t, l, m.uid); st.State != delivery.JoinPending || !strings.Contains(st.Reason, "approver signers could not be read") || l.Completeness != fleet.CompletenessPartial {
		t.Fatalf("%+v %+v", st, l)
	}
}

func TestReviewErrorKeepsPendingNotCached(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	e.reviewErr = errors.New("apiserver down")
	l := e.svc.ClusterJoins(t.Context(), e.opts())
	if st := stateOfUID(t, l, m.uid); st.State != delivery.JoinPending || !strings.Contains(st.Reason, "could not be checked: apiserver down") || l.Completeness != fleet.CompletenessPartial {
		t.Fatalf("%+v %+v", st, l)
	}
	e.reviewErr = nil
	if st := stateOfUID(t, e.svc.ClusterJoins(t.Context(), e.opts()), m.uid); st.State != delivery.JoinApproved || e.reviews != 2 {
		t.Fatalf("%+v after %d reviews", st, e.reviews)
	}
	e.svc.ClusterJoins(t.Context(), e.opts())
	if e.reviews != 2 {
		t.Fatalf("an allowed review was not remembered: %d reviews", e.reviews)
	}
}

func TestRevokedGrantTurnsApprovalPending(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	if !clustersOf(e.build(t))[m.uid] {
		t.Fatal("not approved")
	}
	e.allow = map[string]bool{}
	e.clock = e.clock.Add(joinVerdictTTL + time.Second)
	e.publish(t, m, m.key, e.clock)
	if st := stateOfUID(t, e.svc.ClusterJoins(t.Context(), e.opts()), m.uid); st.State != delivery.JoinPending || !strings.Contains(st.Reason, "may not approve") {
		t.Fatalf("%+v", st)
	}
	if len(e.build(t).Targets) != 0 {
		t.Fatal("a revoked approver's member stayed in the fleet")
	}
}

func TestRecordListingFailureIsPartial(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	e.failReferrers.Store(true)
	l := e.svc.ClusterJoins(t.Context(), e.opts())
	if st := stateOfUID(t, l, m.uid); st.State != delivery.JoinPending || !strings.Contains(st.Reason, "could not be read") || l.Completeness != fleet.CompletenessPartial {
		t.Fatalf("%+v %+v", st, l)
	}
	snap := e.build(t)
	if len(snap.Targets) != 0 || snap.Completeness == fleet.CompletenessComplete || stateOf(t, snap, clusterJoinsID).Status != fleet.SourcePartial {
		t.Fatalf("%d targets, %s", len(snap.Targets), snap.Completeness)
	}
}

func TestJoinRecordsCutKeepsPending(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	approve := e.signRecord(t, "alice@acme.com", joinIssuer, m.record(delivery.JoinApprove))
	for i := range delivery.MaxJoinRecords {
		j := m.record(delivery.JoinApprove)
		j.At = fmt.Sprintf("2026-11-17T09:%02d:00Z", i)
		e.pushRecord(t, m.uid, e.signRecord(t, "alice@acme.com", joinIssuer, j), nil)
	}
	e.pushRecord(t, m.uid, approve, nil)
	l := e.svc.ClusterJoins(t.Context(), e.opts())
	if st := stateOfUID(t, l, m.uid); st.State != delivery.JoinPending || !strings.Contains(st.Reason, "more than 20 join records") || l.Completeness != fleet.CompletenessPartial {
		t.Fatalf("%+v", st)
	}
}

func TestJoinRecordFloodKeepsPending(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersWriteOnceTags)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	approve := e.signRecord(t, "alice@acme.com", joinIssuer, m.record(delivery.JoinApprove))
	e.pushRecord(t, m.uid, approve, nil)
	e.decide(t, m, delivery.JoinRemove)
	for i := range 25 {
		e.pushRecord(t, m.uid, approve, map[string]string{"copy": fmt.Sprint(i)})
	}
	if st := stateOfUID(t, e.svc.ClusterJoins(t.Context(), e.opts()), m.uid); st.State != delivery.JoinRemoved {
		t.Fatalf("a replayed approval resurrected the member: %+v", st)
	}
}

func TestRejectedRecordsStopTakingTheBound(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	for i := range delivery.MaxJoinRecords {
		e.pushRecord(t, m.uid, e.signRecord(t, fmt.Sprintf("u%d@acme.com", i), "https://other-idp.example", m.record(delivery.JoinApprove)), nil)
	}
	e.decide(t, m, delivery.JoinApprove)
	e.svc.ClusterJoins(t.Context(), e.opts())
	if st := stateOfUID(t, e.svc.ClusterJoins(t.Context(), e.opts()), m.uid); st.State != delivery.JoinApproved {
		t.Fatalf("%+v", st)
	}
}

func TestJoinListTruncatedIsPartial(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	for i := range 12 {
		m := newMember(fmt.Sprintf("cccc%04d", i), "c")
		e.publish(t, m, m.key, snapNow)
	}
	e.tagPage.Store(true)
	l := e.svc.ClusterJoins(t.Context(), e.opts())
	if l.Completeness != fleet.CompletenessPartial || len(l.Limitations) == 0 || l.Limitations[0].Code != fleet.LimitationTargetSnapshotBound {
		t.Fatalf("%+v", l)
	}
}

func TestTagSquattingNeverEvictsApprovedMember(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	for i := range 40 {
		m := newMember(fmt.Sprintf("0000%04d", i), "squat")
		e.publish(t, m, m.key, snapNow)
	}
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	l := e.svc.ClusterJoins(t.Context(), e.opts())
	if st := stateOfUID(t, l, m.uid); st.State != delivery.JoinApproved {
		t.Fatalf("%+v", st)
	}
	if len(l.Clusters) != delivery.MaxTargetSnapshots+1 || !strings.Contains(fmt.Sprint(l.Limitations), "8 pending clusters were not read") {
		t.Fatalf("%d rows, %+v", len(l.Clusters), l.Limitations)
	}
	if !clustersOf(e.build(t))[m.uid] {
		t.Fatal("squatting evicted the approved member")
	}
}

func TestJoinListUnauthorizedNamesCredential(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	e.deny.Store(true)
	l := e.svc.ClusterJoins(t.Context(), e.opts())
	if l.Completeness != fleet.CompletenessPartial || len(l.Clusters) != 0 || !strings.Contains(l.Limitations[0].Message, "dashboard.ociSecrets") {
		t.Fatalf("%+v", l)
	}
	e.deny.Store(false)
	o := e.opts()
	o.Repository = "127.0.0.1:1/acme/pacto-targets"
	if l := e.svc.ClusterJoins(t.Context(), o); l.Completeness != fleet.CompletenessPartial || !strings.Contains(l.Limitations[0].Message, "could not be listed") {
		t.Fatalf("%+v", l)
	}
}

func TestUnreadableSnapshotIsPendingWithWhy(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	stale, wrong, bad := newMember(memberA, "eu-prod"), newMember(memberB, "us-west"), newMember("dddd0000", "bad")
	e.publish(t, stale, stale.key, snapNow.Add(-time.Hour))
	s := ledgerSnapshot("someone-else", snapNow)
	s.Join = wrong.join()
	pushAt(t, e.repo, wrong.key, wrong.uid, s)
	repo, _ := ocireferrer.Open(e.repo, ocireferrer.Options{})
	if _, err := deliveryrecord.PushTargetSnapshot(t.Context(), repo, bad.uid, []byte("not a bundle")); err != nil {
		t.Fatal(err)
	}
	l := e.svc.ClusterJoins(t.Context(), e.opts())
	if st := stateOfUID(t, l, stale.uid); st.Status != fleet.SourceStale || st.State != delivery.JoinPending {
		t.Fatalf("stale %+v", st)
	}
	if st := stateOfUID(t, l, wrong.uid); !strings.Contains(st.Reason, "names cluster someone-else") || st.Status != fleet.SourceUnavailable {
		t.Fatalf("wrong %+v", st)
	}
	if st := stateOfUID(t, l, bad.uid); !strings.Contains(st.Reason, "its snapshot was not used") {
		t.Fatalf("bad %+v", st)
	}
	e.failSnapshot(t, stale.uid)
	if st := stateOfUID(t, e.svc.ClusterJoins(t.Context(), e.opts()), stale.uid); !strings.Contains(st.Reason, "its snapshot could not be read") {
		t.Fatalf("gone %+v", st)
	}
}

// failSnapshot moves uid's snapshot tag onto a manifest that is no snapshot.
func (e *joinEnv) failSnapshot(t *testing.T, uid string) {
	t.Helper()
	repo, _ := ocireferrer.Open(e.repo, ocireferrer.Options{})
	raw := []byte(`{"schemaVersion":2,"mediaType":"` + ocispec.MediaTypeImageManifest + `","annotations":{"t":"x"}}`)
	if err := repo.Manifests().PushReference(t.Context(), ocireferrer.Descriptor(ocispec.MediaTypeImageManifest, raw), bytes.NewReader(raw), delivery.TargetSnapshotTagPrefix+uid); err != nil {
		t.Fatal(err)
	}
}

func TestFleetWithoutJoinOptionsUnchanged(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	base, err := e.svc.Fleet(t.Context(), FleetOptions{Now: func() time.Time { return snapNow }})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.build(t); got.SnapshotID != base.SnapshotID {
		t.Fatalf("a pending member changed the snapshot: %s, base %s", got.SnapshotID, base.SnapshotID)
	}
}

func TestJoinsAndTargetSnapshotShareOneReader(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	a, b := newMember(memberA, "eu-prod"), newMember(memberB, "us-west")
	e.publish(t, a, a.key, snapNow)
	e.publish(t, b, b.key, snapNow)
	e.decide(t, a, delivery.JoinApprove)
	e.decide(t, b, delivery.JoinApprove)
	o := e.fleetOpts()
	o.TargetSnapshot = []string{"oci://" + e.repo + ":" + delivery.TargetSnapshotTagPrefix + a.uid}
	snap, err := e.svc.Fleet(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range snap.Sources {
		if s.Kind == fleet.SourceKindTargetSnapshot {
			n++
		}
	}
	if n != 2 || !clustersOf(snap)[a.uid] || !clustersOf(snap)[b.uid] {
		t.Fatalf("%d snapshot sources, clusters %v", n, clustersOf(snap))
	}
	o.TargetSnapshot = []string{"oci://" + e.repo}
	if _, err := e.svc.Fleet(t.Context(), o); err != nil {
		t.Fatalf("the repository and the joins collided: %v", err)
	}
}

// TestPendingMemberOnlyAddsUnreadCount: with a member approved, a pending
// cluster adds one count for gates and nothing else: no source, target,
// service or completeness change.
func TestPendingMemberOnlyAddsUnreadCount(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	a := newMember(memberA, "eu-prod")
	e.publish(t, a, a.key, snapNow)
	e.decide(t, a, delivery.JoinApprove)
	before := e.build(t)
	p := newMember(memberB, "us-west")
	e.publish(t, p, p.key, snapNow)
	after := e.build(t)
	bm, am := fleet.NewQuery(before).ProductMeta(), fleet.NewQuery(after).ProductMeta()
	if len(after.Sources) != len(before.Sources) || len(after.Targets) != len(before.Targets) || len(after.Services) != len(before.Services) ||
		after.Completeness != before.Completeness || !reflect.DeepEqual(am.SourceCounts, bm.SourceCounts) || !reflect.DeepEqual(am.Clusters, bm.Clusters) {
		t.Fatalf("a pending member changed the fleet: %d/%d sources, %d/%d targets, %+v/%+v", len(after.Sources), len(before.Sources),
			len(after.Targets), len(before.Targets), am.SourceCounts, bm.SourceCounts)
	}
	if before.UnreadClusters != 0 || after.UnreadClusters != 1 || !hasLimitation(after.Limitations, fleet.LimitationClustersUnread, delivery.UnreadClustersSourceID) {
		t.Fatalf("unread %d -> %d, %+v", before.UnreadClusters, after.UnreadClusters, after.Limitations)
	}
}

// TestNoHubRoleNoUnreadCount: clusters that share the repository while no
// person ever approved or removed one leave the answer as it was.
func TestNoHubRoleNoUnreadCount(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	a, b := newMember(memberA, "eu-prod"), newMember(memberB, "us-west")
	e.publish(t, a, a.key, snapNow)
	e.publish(t, b, b.key, snapNow)
	base, err := e.svc.Fleet(t.Context(), FleetOptions{Now: func() time.Time { return snapNow }})
	if err != nil {
		t.Fatal(err)
	}
	got := e.build(t)
	gj, _ := json.Marshal(got)
	bj, _ := json.Marshal(base)
	if got.UnreadClusters != 0 || !bytes.Equal(gj, bj) {
		t.Fatalf("pending clusters with no hub role changed the answer:\n%s\n%s", gj, bj)
	}
}

// TestRemovedMemberNotCounted: a removed cluster is a person's decision, so
// it is neither read nor counted; it still makes this installation a hub.
func TestRemovedMemberNotCounted(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	a := newMember(memberA, "eu-prod")
	e.publish(t, a, a.key, snapNow)
	e.decide(t, a, delivery.JoinApprove)
	e.decide(t, a, delivery.JoinRemove)
	if snap := e.build(t); snap.UnreadClusters != 0 || len(snap.Targets) != 0 || hasLimitation(snap.Limitations, fleet.LimitationClustersUnread, delivery.UnreadClustersSourceID) {
		t.Fatalf("a removed member is counted or read: %d unread, %d targets", snap.UnreadClusters, len(snap.Targets))
	}
	p := newMember(memberB, "us-west")
	e.publish(t, p, p.key, snapNow)
	if snap := e.build(t); snap.UnreadClusters != 1 {
		t.Fatalf("a removal is no hub role: %d unread", snap.UnreadClusters)
	}
}

// TestUnreadCountNamesNoCluster: the count's limitation names neither the
// pending cluster's UID nor its name.
func TestUnreadCountNamesNoCluster(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	a, p := newMember(memberA, "eu-prod"), newMember(memberB, "us-west")
	e.publish(t, a, a.key, snapNow)
	e.decide(t, a, delivery.JoinApprove)
	e.publish(t, p, p.key, snapNow)
	snap := e.build(t)
	n := 0
	for _, l := range snap.Limitations {
		if l.Code != fleet.LimitationClustersUnread {
			continue
		}
		n++
		if strings.Contains(l.Message, p.uid) || strings.Contains(l.Message, p.name) || l.Source != delivery.UnreadClustersSourceID {
			t.Fatalf("%+v", l)
		}
	}
	if n != 1 {
		t.Fatalf("%d unread limitations", n)
	}
}

// publishSnap signs s, m's snapshot carrying m's join block, and tags it.
func (e *joinEnv) publishSnap(t *testing.T, m member, s delivery.TargetSnapshot) {
	t.Helper()
	s.Join = m.join()
	pushAt(t, e.repo, m.key, m.uid, s)
}

// joinImpact is the impact of orders 1.0.0 -> 2.0.0 (BREAKING) over e's
// hub fleet.
func (e *joinEnv) joinImpact(t *testing.T) *impact.Result {
	t.Helper()
	svc := ordersService(t)
	svc.sys.now = e.svc.sys.now
	e.svc = svc
	host := strings.TrimSuffix(e.repo, "/acme/"+delivery.TargetSnapshotRepository)
	o := e.fleetOpts()
	o.Index = IndexNone
	res, err := svc.Impact(t.Context(), ImpactOptions{OldPath: "oci://" + host + "/acme/orders:1.0.0", NewPath: "oci://" + host + "/acme/orders:2.0.0", Fleet: o})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestImpactBlocksOnMemberConsumer: a consumer in an approved member whose
// range refuses the change blocks it, attributed to the member's cluster.
func TestImpactBlocksOnMemberConsumer(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	a, b := newMember(memberA, "eu-prod"), newMember(memberB, "us-west")
	host := strings.TrimSuffix(e.repo, "/acme/"+delivery.TargetSnapshotRepository)
	e.publishSnap(t, a, webappSnapshot(a.uid, host, "^1", snapNow))
	e.publish(t, b, b.key, snapNow)
	e.decide(t, a, delivery.JoinApprove)
	e.decide(t, b, delivery.JoinApprove)
	res := e.joinImpact(t)
	i := slices.IndexFunc(res.Consumers, func(c impact.AffectedConsumer) bool { return c.Service == "webapp" })
	if res.Gate != impact.GateBlocked || i < 0 || !slices.Equal(res.Consumers[i].Clusters, []string{a.uid}) || len(res.Clusters) != 2 || res.UnreadClusters != 0 {
		t.Fatalf("gate %s (%s), consumers %+v, clusters %+v", res.Gate, res.GateReason, res.Consumers, res.Clusters)
	}
}

// TestImpactUnverifiedWithPendingMember: a BREAKING change with no consumer
// read is unverified while a cluster that publishes to the hub is not a
// member.
func TestImpactUnverifiedWithPendingMember(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	a, p := newMember(memberA, "eu-prod"), newMember(memberB, "us-west")
	e.publish(t, a, a.key, snapNow)
	e.decide(t, a, delivery.JoinApprove)
	e.publish(t, p, p.key, snapNow)
	res := e.joinImpact(t)
	if res.Gate != impact.GateUnverified || res.GateCode() != impact.GateCodeClustersUnread || res.UnreadClusters != 1 {
		t.Fatalf("gate %s %s (%s), unread %d %+v", res.Gate, res.GateCode(), res.GateReason, res.UnreadClusters, res.Limitations)
	}
}

// TestNoGateReadsJoinState: no gate, impact or plan code names a join type,
// so a pending member or an inferred environment never clears a gate. The
// one allowed reader, pkg/delivery/members.go, names only the list, its
// states and the three state values: never an advertised environment, a
// snapshot join block, a candidate or a verified record.
func TestNoGateReadsJoinState(t *testing.T) {
	t.Parallel()
	banned := map[string]bool{"SnapshotJoin": true, "AdvertisedEnvironment": true, "ClusterJoinState": true, "JoinState": true,
		"ClusterJoinList": true, "JoinCandidate": true, "VerifiedJoin": true}
	allowed := map[string]bool{"ClusterJoinList": true, "ClusterJoinState": true, "JoinApproved": true, "JoinPending": true, "JoinRemoved": true}
	files, _ := filepath.Glob("../../pkg/impact/*.go")
	plans, _ := filepath.Glob("../../pkg/delivery/plan*.go")
	files = append(files, plans...)
	if len(files) < 2 || len(plans) == 0 {
		t.Fatalf("found %d files", len(files))
	}
	for _, path := range append(files, "../../pkg/delivery/members.go") {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		reader := strings.HasSuffix(path, "/members.go")
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && reader {
				x, _ := sel.X.(*ast.Ident)
				return x == nil || x.Name != "fleet" // fleet names no join type
			}
			id, ok := n.(*ast.Ident)
			switch {
			case !ok:
			case reader && (banned[id.Name] || strings.Contains(id.Name, "Join")) && !allowed[id.Name]:
				t.Errorf("%s names %s", path, id.Name)
			case !reader && banned[id.Name]:
				t.Errorf("%s names %s", path, id.Name)
			}
			return true
		})
	}
}

// TestMovedJoinTagIsALimitation: a join tag moved onto another record is no
// record, and the list names it.
func TestMovedJoinTagIsALimitation(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersWriteOnceTags)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	repo, _ := ocireferrer.Open(e.repo, ocireferrer.Options{})
	stream := delivery.ClusterJoinStream(m.uid)
	anchor := deliveryrecord.Anchor(stream).ManifestDesc
	art := ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, []byte("{}"), &anchor, map[string]string{deliveryrecord.AnnPredicateType: delivery.PredicateClusterJoin})
	if err := ocireferrer.Push(t.Context(), repo, art); err != nil {
		t.Fatal(err)
	}
	if err := repo.Tag(t.Context(), art.ManifestDesc, deliveryrecord.KindTag(stream, deliveryrecord.KindJoin, digest.FromString("elsewhere"))); err != nil {
		t.Fatal(err)
	}
	l := e.svc.ClusterJoins(t.Context(), e.opts())
	if st := stateOfUID(t, l, m.uid); st.State != delivery.JoinApproved || !strings.Contains(st.Reason, "does not name the manifest") ||
		!strings.Contains(fmt.Sprint(l.Limitations), "1 cluster have tags that do not name their record") {
		t.Fatalf("%+v %+v", st, l.Limitations)
	}
}

// personSigner is alice's keyless signer under e's private Sigstore.
func (e *joinEnv) personSigner(t *testing.T) delivery.Signer {
	t.Helper()
	s, err := signing.Keyless(e.ss.Fulcio, e.ss.TSA, "", func(context.Context) (string, error) { return sigtest.Token("alice@acme.com", joinIssuer), nil }, e.ss.Root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestWriteClusterJoinApprovesAndRemoves: in either form a written approval
// makes the member approved, and a written removal removes it.
func TestWriteClusterJoinApprovesAndRemoves(t *testing.T) {
	t.Parallel()
	for _, mode := range bothModes {
		t.Run(string(mode), func(t *testing.T) {
			e := newJoinEnv(t, mode)
			m := newMember(memberA, "eu-prod")
			e.publish(t, m, m.key, snapNow)
			ref, got, err := e.svc.WriteClusterJoin(t.Context(), m.record(delivery.JoinApprove), e.personSigner(t), e.repo)
			if err != nil || got != mode || !strings.HasPrefix(ref, e.repo+"@sha256:") {
				t.Fatalf("%q %q %v", ref, got, err)
			}
			if st := stateOfUID(t, e.svc.ClusterJoins(t.Context(), e.opts()), m.uid); st.State != delivery.JoinApproved || !strings.HasSuffix(ref, "@"+st.Record) {
				t.Fatalf("%+v", st)
			}
			e.clock = e.clock.Add(time.Second)
			if _, _, err := e.svc.WriteClusterJoin(t.Context(), m.record(delivery.JoinRemove), e.personSigner(t), e.repo); err != nil {
				t.Fatal(err)
			}
			if st := stateOfUID(t, e.svc.ClusterJoins(t.Context(), e.opts()), m.uid); st.State != delivery.JoinRemoved {
				t.Fatalf("%+v", st)
			}
		})
	}
}

// TestWriteClusterJoinRefuses: a record Check refuses, a repository that
// cannot be opened or read, a snapshot that does not open, names another
// cluster, carries no join block or names another key, and a signer that
// fails get nothing written.
func TestWriteClusterJoinRefuses(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m, b := newMember(memberA, "eu-prod"), newMember(memberB, "us-west")
	e.publish(t, m, m.key, snapNow)
	signed := 0
	ok := signerFunc(func(context.Context, string, []byte) ([]byte, error) { signed++; return nil, errors.New("signer down") })
	rotated := m.record(delivery.JoinApprove)
	rotated.KeyID, rotated.PublicKey = b.join().KeyID, b.join().PublicKey
	noHub := m.record(delivery.JoinApprove)
	noHub.Hub = ""
	other := ledgerSnapshot(memberA, snapNow)
	other.Join = m.join()
	pushAt(t, e.repo, m.key, memberB, other) // tagged for B, names A
	bare := ledgerSnapshot("cccc0000-0000-4000-8000-00000000000c", snapNow)
	pushAt(t, e.repo, m.key, bare.Cluster, bare)
	bareRec := m.record(delivery.JoinApprove)
	bareRec.Cluster = bare.Cluster
	for _, c := range []struct {
		rec  delivery.ClusterJoin
		repo string
		want string
	}{
		{noHub, e.repo, "no hub"},
		{m.record(delivery.JoinApprove), "Not A Repo", "invalid"},
		{newMember("dddd0000-0000-4000-8000-00000000000d", "x").record(delivery.JoinApprove), e.repo, "reading the snapshot of dddd0000-0000-4000-8000-00000000000d again"},
		{rotated, e.repo, "ClusterKeyMismatch: the snapshot of " + memberA + " is now signed by sha256:" + m.join().KeyID[:12] + "...; you checked sha256:" + b.join().KeyID[:12]},
		{bareRec, e.repo, "ClusterKeyMismatch: the snapshot of " + bare.Cluster + " now carries no join block"},
		{m.record(delivery.JoinApprove), e.repo, "signer down"},
	} {
		if _, _, err := e.svc.WriteClusterJoin(t.Context(), c.rec, ok, c.repo); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("want %q, got %v", c.want, err)
		}
	}
	if _, _, err := e.svc.WriteClusterJoin(t.Context(), b.record(delivery.JoinApprove), ok, e.repo); err == nil || !strings.Contains(err.Error(), "names cluster "+memberA) {
		t.Fatalf("a snapshot naming another cluster: %v", err)
	}
	pushAt(t, e.repo, m.key, memberA, delivery.TargetSnapshot{Cluster: memberA})
	if _, _, err := e.svc.WriteClusterJoin(t.Context(), m.record(delivery.JoinApprove), ok, e.repo); err == nil || !strings.Contains(err.Error(), "the snapshot of "+memberA+":") {
		t.Fatalf("a snapshot that does not open: %v", err)
	}
	forged := newMember("eeee0000-0000-4000-8000-00000000000e", "forged")
	e.publish(t, forged, m.key, snapNow) // names forged's key, signed by m's
	if _, _, err := e.svc.WriteClusterJoin(t.Context(), forged.record(delivery.JoinApprove), ok, e.repo); err == nil || !strings.Contains(err.Error(), "not signed by the key it names") {
		t.Fatalf("a snapshot its own key did not sign: %v", err)
	}
	if signed != 1 {
		t.Fatalf("signed %d times", signed)
	}
}

// detectedTargets is an operator-written detection ConfigMap in pacto/det
// advertising targets (none when empty), dated at.
func detectedTargets(t *testing.T, targets string, at time.Time) *unstructured.Unstructured {
	t.Helper()
	b, _ := json.Marshal(detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: at, Cluster: joinHub, Installation: &detect.Installation{Registry: "r", Targets: targets}})
	cm := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "det", "namespace": "pacto", "labels": map[string]any{detect.ManagedByLabel: detect.ManagedBy}},
		"data":     map[string]any{detect.ConfigMapKey: string(b)}}}
	cm.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: detect.FieldManager, FieldsV1: metav1.NewFieldsV1(`{"f:data":{"f:` + detect.ConfigMapKey + `":{}}}`)}})
	return cm
}

func TestAdvertisedTargets(t *testing.T) {
	now := func() time.Time { return detectedNow }
	read := func(cm *unstructured.Unstructured) (string, error) {
		return AdvertisedTargets(t.Context(), dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), cm), "k8s:pacto/det", now)
	}
	if got, err := read(detectedTargets(t, "ghcr.io/acme/pacto-targets", detectedNow)); err != nil || got != "ghcr.io/acme/pacto-targets" {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := read(detectedTargets(t, "", detectedNow)); err != nil || got != "" {
		t.Fatalf("no registry: %q %v", got, err)
	}
	if _, err := read(detectedTargets(t, "x", detectedNow.Add(-48*time.Hour))); err == nil || !strings.Contains(err.Error(), "names no registry: stale") {
		t.Fatalf("stale: %v", err)
	}
	if _, err := AdvertisedTargets(t.Context(), nil, filepath.Join(t.TempDir(), "none.json"), now); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing file: %v", err)
	}
	t.Setenv("PACTO_REGISTRY_TOKEN", "t")
	if _, err := read(detectedTargets(t, "evil.example/pacto-targets", detectedNow)); err == nil || !strings.Contains(err.Error(), "pass --registry evil.example/pacto-targets") {
		t.Fatalf("env credentials: %v", err)
	}
}

func TestDeliveryClassPolicy(t *testing.T) {
	t.Parallel()
	want := delivery.ClassPolicy{Signers: []delivery.PersonSigner{{Issuer: joinIssuer}}}
	if got, err := (&Delivery{Policy: want}).ClassPolicy(t.Context()); err != nil || len(got.Signers) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
}

// decideAs writes m's record deciding dec, signed by email (user oidc:email).
func (e *joinEnv) decideAs(t *testing.T, m member, dec delivery.JoinDecision, email string) {
	t.Helper()
	j := m.record(dec)
	j.Approver = "oidc:" + email
	e.pushRecord(t, m.uid, e.signRecord(t, email, joinIssuer, j), nil)
}

// TestNewerUncheckedRemovalKeepsPending: a newer removal whose review fails
// never lets an older approval decide (R2-1).
func TestNewerUncheckedRemovalKeepsPending(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	e.allow["oidc:bob@acme.com"] = true
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	e.ss.Advance(time.Second)
	e.decideAs(t, m, delivery.JoinRemove, "bob@acme.com")
	e.errFor = map[string]error{"oidc:bob@acme.com": errors.New("apiserver timeout")}
	l := e.svc.ClusterJoins(t.Context(), e.opts())
	st := stateOfUID(t, l, m.uid)
	if st.State != delivery.JoinPending || !strings.Contains(st.Reason, "a newer removal by oidc:bob@acme.com was not counted") ||
		!strings.Contains(st.Reason, "a removal by oidc:bob@acme.com could not be checked: apiserver timeout") || l.Completeness != fleet.CompletenessPartial {
		t.Fatalf("%+v %+v", st, l)
	}
	if len(e.build(t).Targets) != 0 {
		t.Fatal("a member whose removal could not be checked reached the fleet")
	}
}

// TestRevokedRemoverKeepsPending: a removal whose signer may no longer
// approve keeps the older approval from deciding (R2-2).
func TestRevokedRemoverKeepsPending(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	e.ss.Advance(time.Second)
	e.decideAs(t, m, delivery.JoinRemove, "bob@acme.com") // bob's grant is gone
	st := stateOfUID(t, e.svc.ClusterJoins(t.Context(), e.opts()), m.uid)
	if st.State != delivery.JoinPending || !strings.Contains(st.Reason, "approve again to restore it") || st.ApprovedKeyID != m.join().KeyID {
		t.Fatalf("%+v", st)
	}
	e.ss.Advance(time.Second)
	e.decide(t, m, delivery.JoinApprove)
	if st := stateOfUID(t, e.svc.ClusterJoins(t.Context(), e.opts()), m.uid); st.State != delivery.JoinApproved {
		t.Fatalf("approving again did not restore it: %+v", st)
	}
}

// TestApprovalHistoryNeverLocksOut: approve and remove cycles past the
// record bound settle on the newest decision, as superseded records stop
// taking the bound (R4-1).
func TestApprovalHistoryNeverLocksOut(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	for range 11 {
		e.decide(t, m, delivery.JoinApprove)
		e.ss.Advance(time.Second)
		e.decide(t, m, delivery.JoinRemove)
		e.ss.Advance(time.Second)
	}
	e.decide(t, m, delivery.JoinApprove)
	var st delivery.ClusterJoinState
	for range 4 {
		if st = stateOfUID(t, e.svc.ClusterJoins(t.Context(), e.opts()), m.uid); st.State == delivery.JoinApproved {
			return
		}
	}
	t.Fatalf("23 decisions locked the member out: %+v", st)
}

// TestApprovedMemberUnreadableSnapshotIsPartial: an approved member whose
// snapshot cannot be read makes the list and the fleet partial (R2-3).
func TestApprovedMemberUnreadableSnapshotIsPartial(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	e.failSnapshot(t, m.uid)
	l := e.svc.ClusterJoins(t.Context(), e.opts())
	if st := stateOfUID(t, l, m.uid); st.State != delivery.JoinPending || l.Completeness != fleet.CompletenessPartial ||
		!strings.Contains(fmt.Sprint(l.Limitations), "the snapshots of 1 cluster an approval names could not be read") {
		t.Fatalf("%+v %+v", st, l)
	}
	snap := e.build(t)
	if snap.Completeness == fleet.CompletenessComplete || stateOf(t, snap, clusterJoinsID).Status != fleet.SourcePartial {
		t.Fatalf("the fleet says complete without the member: %s", snap.Completeness)
	}
}

// TestJoinLimitationsNameNoCluster: limitations count clusters and never
// name one or an approver, so a reader who may not approve learns neither
// (R1-1).
func TestJoinLimitationsNameNoCluster(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersWriteOnceTags)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	e.errFor = map[string]error{"oidc:alice@acme.com": errors.New("apiserver timeout")}
	l := e.svc.ClusterJoins(t.Context(), e.opts())
	snap := e.build(t)
	for _, text := range []string{fmt.Sprint(l.Limitations), fmt.Sprint(stateOf(t, snap, clusterJoinsID)), fmt.Sprint(snap.Limitations)} {
		if strings.Contains(text, m.uid) || strings.Contains(text, "alice") || !strings.Contains(fmt.Sprint(l.Limitations), "the approvers of 1 cluster could not be checked") {
			t.Fatalf("a limitation names the cluster or its approver: %s", text)
		}
	}
}

// TestReviewForbiddenNamesTheGrant: a reader whose credentials may not
// review approvers is told which grant it lacks (R3-4).
func TestReviewForbiddenNamesTheGrant(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	e.errFor = map[string]error{"oidc:alice@acme.com": apierrors.NewForbidden(schema.GroupResource{Group: "authorization.k8s.io", Resource: "subjectaccessreviews"}, "", errors.New("no"))}
	l := e.svc.ClusterJoins(t.Context(), e.opts())
	if st := stateOfUID(t, l, m.uid); st.State != delivery.JoinPending || !strings.Contains(st.Reason, "may not create subjectaccessreviews") ||
		!strings.Contains(fmt.Sprint(l.Limitations), "grant create on subjectaccessreviews in the hub") {
		t.Fatalf("%+v %+v", st, l)
	}
}

// TestNoSignersKeepsTheSnapshotReason: with no signers, a forged snapshot
// still says it is forged (R2-4).
func TestNoSignersKeepsTheSnapshotReason(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	e.policy = delivery.ClassPolicy{}
	m := newMember(memberA, "eu-prod")
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	e.publish(t, m, other, snapNow)
	st := stateOfUID(t, e.svc.ClusterJoins(t.Context(), e.opts()), m.uid)
	if !strings.HasPrefix(st.Reason, noSigners+"; the snapshot is not signed by the key it names") || st.KeyID != "" {
		t.Fatalf("%+v", st)
	}
}

// TestIncludedClusterReadPastTheBound: the cluster a person decides on is
// read past the pending bound, and without a tag when it has records
// (R3-2, R4-4, R4-2).
func TestIncludedClusterReadPastTheBound(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	for i := range delivery.MaxTargetSnapshots {
		m := newMember(fmt.Sprintf("0000%04d", i), "squat")
		e.publish(t, m, m.key, snapNow)
	}
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	o := e.opts()
	if l := e.svc.ClusterJoins(t.Context(), o); slices.ContainsFunc(l.Clusters, func(c delivery.ClusterJoinState) bool { return c.UID == m.uid }) {
		t.Fatal("the bound did not apply")
	}
	o.Include = m.uid
	if st := stateOfUID(t, e.svc.ClusterJoins(t.Context(), o), m.uid); st.State != delivery.JoinPending || st.KeyID != m.join().KeyID {
		t.Fatalf("%+v", st)
	}
	gone := newMember(memberB, "us-west")
	e.decide(t, gone, delivery.JoinApprove) // approved, its tag deleted
	o.Include = gone.uid
	if st := stateOfUID(t, e.svc.ClusterJoins(t.Context(), o), gone.uid); st.State != delivery.JoinPending || st.ApprovedKeyID != gone.join().KeyID {
		t.Fatalf("%+v", st)
	}
}

// TestRemovalNeedsNoSnapshot: a member that stopped publishing a join block
// can still be removed (R4-2).
func TestRemovalNeedsNoSnapshot(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	pushAt(t, e.repo, m.key, m.uid, ledgerSnapshot(m.uid, snapNow)) // no join block
	e.ss.Advance(time.Second)
	if _, _, err := e.svc.WriteClusterJoin(t.Context(), m.record(delivery.JoinRemove), e.personSigner(t), e.repo); err != nil {
		t.Fatal(err)
	}
	if st := stateOfUID(t, e.svc.ClusterJoins(t.Context(), e.opts()), m.uid); st.State != delivery.JoinRemoved {
		t.Fatalf("%+v", st)
	}
}

// TestResolvedJoinOptions: a reader that resolves its options per read says
// why it could not, and reads nothing from a hub without a registry (R3-3).
func TestResolvedJoinOptions(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	var err error
	repo := ""
	o := JoinOptions{Resolve: func(context.Context) (JoinOptions, error) { r := e.opts(); r.Repository = repo; return r, err }}
	if l := e.svc.ClusterJoins(t.Context(), o); l.Completeness != fleet.CompletenessComplete || len(l.Clusters) != 0 || len(l.Limitations) != 0 {
		t.Fatalf("no registry: %+v", l)
	}
	err = errors.New("the detection names no registry: unavailable")
	if l := e.svc.ClusterJoins(t.Context(), o); l.Completeness != fleet.CompletenessPartial || !strings.Contains(fmt.Sprint(l.Limitations), "unavailable") {
		t.Fatalf("unresolved: %+v", l)
	}
	err, repo = nil, e.repo
	if l := e.svc.ClusterJoins(t.Context(), o); len(l.Clusters) != 1 {
		t.Fatalf("resolved: %+v", l)
	}
	e.decide(t, m, delivery.JoinApprove)
	if snap, err := e.svc.Fleet(t.Context(), FleetOptions{ClusterJoins: &o, Now: func() time.Time { return e.clock }}); err != nil || !clustersOf(snap)[m.uid] {
		t.Fatalf("a resolved member is not in the fleet: %v", err)
	}
}

// TestClusterJoinsMatchesCandidates: candidates read at the end of a join
// read are matched to its members; a failed read leaves the candidates
// partial and says why, and the members' completeness is untouched.
func TestClusterJoinsMatchesCandidates(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	var readErr error
	o := e.opts()
	o.Candidates = func(context.Context) (detect.Clusters, error) {
		return detect.Clusters{AsOf: e.clock, Candidates: []detect.Candidate{{Source: detect.SourceArgoCD, Kind: "Secret", Namespace: "argocd", Name: "eu",
			UID: memberA, Labels: map[string]string{"env": "prod"}}}}, readErr
	}
	l := e.svc.ClusterJoins(t.Context(), o)
	if len(l.Candidates) != 1 || l.Candidates[0].Match != delivery.CandidateMatched || len(l.Clusters[0].InventoryEnvironments) != 1 {
		t.Fatalf("matched: %+v", l)
	}
	readErr = errors.New("not written by the Pacto operator")
	l = e.svc.ClusterJoins(t.Context(), o)
	if l.Candidates != nil || l.CandidatesCompleteness != fleet.CompletenessPartial || l.Completeness != fleet.CompletenessComplete ||
		len(l.Limitations) != 1 || l.Limitations[0].Message != "the candidate clusters were not read: not written by the Pacto operator" {
		t.Fatalf("unread: %+v", l)
	}
}

// TestUnresolvedJoinsKeepCandidates: a hub that sets no registry still
// shows the candidates its operator wrote, and one whose options could not be
// resolved says the candidates were not read rather than showing none.
func TestUnresolvedJoinsKeepCandidates(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	var err error
	o := JoinOptions{Resolve: func(context.Context) (JoinOptions, error) {
		r := e.opts()
		r.Repository, r.Candidates = "", func(context.Context) (detect.Clusters, error) {
			return detect.Clusters{AsOf: e.clock, Candidates: []detect.Candidate{{Source: detect.SourceArgoCD, Kind: "Secret", Namespace: "argocd", Name: "eu"}}}, nil
		}
		return r, err
	}}
	if l := e.svc.ClusterJoins(t.Context(), o); len(l.Candidates) != 1 || l.CandidatesCompleteness != fleet.CompletenessComplete || len(l.Clusters) != 0 {
		t.Fatalf("no registry: %+v", l)
	}
	err = errors.New("unavailable")
	l := e.svc.ClusterJoins(t.Context(), o)
	if l.Candidates != nil || l.CandidatesCompleteness != fleet.CompletenessPartial ||
		!slices.ContainsFunc(l.Limitations, func(lim fleet.Limitation) bool { return lim.Source == delivery.CandidatesID }) {
		t.Fatalf("unresolved: %+v", l)
	}
}

// TestFleetReadsNoCandidates: the fleet's join read never reads candidates,
// however its options resolve, so none reaches a source or a gate.
func TestFleetReadsNoCandidates(t *testing.T) {
	t.Parallel()
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	read := func(context.Context) (detect.Clusters, error) {
		t.Error("the fleet read candidates")
		return detect.Clusters{}, errors.New("read")
	}
	o := e.opts()
	o.Candidates = read
	for _, opts := range []JoinOptions{o, {Resolve: func(context.Context) (JoinOptions, error) { return o, nil }}} {
		if _, err := e.svc.Fleet(t.Context(), FleetOptions{ClusterJoins: &opts, Now: func() time.Time { return e.clock }}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAdvertisedMember(t *testing.T) {
	now := func() time.Time { return detectedNow }
	cm := detectedTargets(t, "ghcr.io/acme/pacto-targets", detectedNow)
	dyn := func() dynamic.Interface { return dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), cm) }
	if AdvertisedMember(t.Context(), dyn(), "k8s:pacto/det", now) {
		t.Fatal("a hub's detection says member")
	}
	b, _ := json.Marshal(detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: detectedNow, Cluster: joinHub, Installation: &detect.Installation{Registry: "r", Member: true}})
	_ = unstructured.SetNestedField(cm.Object, string(b), "data", detect.ConfigMapKey)
	if !AdvertisedMember(t.Context(), dyn(), "k8s:pacto/det", now) {
		t.Fatal("a member's detection does not say member")
	}
}

func TestAdvertisedChartVersion(t *testing.T) {
	t.Parallel()
	now := func() time.Time { return detectedNow }
	read := func(cm *unstructured.Unstructured) (string, error) {
		return AdvertisedChartVersion(t.Context(), dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), cm), "k8s:pacto/det", now)
	}
	cm := detectedTargets(t, "ghcr.io/acme/pacto-targets", detectedNow)
	b, _ := json.Marshal(detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: detectedNow, Cluster: joinHub, Installation: &detect.Installation{Registry: "r", ChartVersion: "4.6.0"}})
	_ = unstructured.SetNestedField(cm.Object, string(b), "data", detect.ConfigMapKey)
	if got, err := read(cm); err != nil || got != "4.6.0" {
		t.Fatalf("%q %v", got, err)
	}
	b, _ = json.Marshal(detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: detectedNow, Cluster: joinHub})
	_ = unstructured.SetNestedField(cm.Object, string(b), "data", detect.ConfigMapKey)
	if got, err := read(cm); err != nil || got != "" {
		t.Fatalf("no installation: %q %v", got, err)
	}
	if _, err := read(detectedTargets(t, "x", detectedNow.Add(-48*time.Hour))); err == nil || !strings.Contains(err.Error(), "names no chart version: stale") {
		t.Fatalf("stale: %v", err)
	}
}
