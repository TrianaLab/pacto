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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	"oras.land/oras-go/v2/registry/remote"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const (
	hubH      = "hub-h"
	peerAlice = "oidc:alice@acme.com"
	stageEU   = "stage-eu-uid"
	stageUS   = "stage-us-uid"
	peerRepo  = "reg.example/delivery/streams"
)

// peerWorld is a placement pass in a cluster named eu-prod (it hosts
// production, not staging), where Argo CD applies payments in
// payments-production, whose members publish to an in-process
// pacto-targets repository. Join records are opaque bundles verified by
// joins; alice may approve clusterjoins here unless rights says otherwise.
type peerWorld struct {
	t      *testing.T
	pe     *placeEnv
	p      *Peers
	repo   *remote.Repository
	now    time.Time
	n      int
	joins  map[string]delivery.VerifiedJoin
	rights map[string]bool
	revErr error
	own    ed25519.PrivateKey
	keys   map[string]ed25519.PrivateKey
}

func newPeerWorld(t *testing.T) *peerWorld {
	t.Helper()
	pe := newPlaceEnv(t, interceptor.Funcs{}, application("payments-production", "payments", nil))
	pe.adopted(t, "payments-production", nil, argoTracked("payments-production", "payments", "payments-production"))
	pe.publish(t, "payments", delivered("payments"), nil)
	pe.r.ClusterName = "eu-prod"
	targets := pe.reg.host + "/acme/pacto-targets"
	repo, _ := ocireferrer.Open(targets, ocireferrer.Options{})
	w := &peerWorld{t: t, pe: pe, repo: repo, now: time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC),
		joins: map[string]delivery.VerifiedJoin{}, rights: map[string]bool{peerAlice: true}, keys: map[string]ed25519.PrivateKey{}}
	pe.review = func(s authorizationv1.SubjectAccessReviewSpec) (authorizationv1.SubjectAccessReviewStatus, error) {
		ra := s.ResourceAttributes
		if ra.Resource != "clusterjoins" {
			return authorizationv1.SubjectAccessReviewStatus{Allowed: true}, nil
		}
		if w.revErr != nil {
			return authorizationv1.SubjectAccessReviewStatus{}, w.revErr
		}
		return authorizationv1.SubjectAccessReviewStatus{Allowed: w.rights[s.User] && ra.Group == "pacto.trianalab.io" && ra.Verb == "approve" && ra.Name != "" && len(s.Groups) == 0}, nil
	}
	w.p = &Peers{Client: pe.r.Client, Targets: targets, ClusterID: "cid", Now: func() time.Time { return w.now },
		openJoin: func(_ delivery.ClassPolicy, b []byte) delivery.VerifiedJoin { return w.joins[string(b)] }}
	pe.r.Peers = w.p
	w.own = w.snapshot("cid", "eu-prod", nil)
	return w
}

// snapshot publishes the self-signed snapshot of uid named name, its join
// block edited, and returns its key.
func (w *peerWorld) snapshot(uid, name string, edit func(*delivery.SnapshotJoin)) ed25519.PrivateKey {
	w.t.Helper()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	j := &delivery.SnapshotJoin{Name: name, NameSource: fleet.ClusterNameFromNodeLabel, KeyID: signing.Hint(pub), PublicKey: base64.StdEncoding.EncodeToString(der)}
	if edit != nil {
		edit(j)
	}
	s := delivery.TargetSnapshot{Cluster: uid, AsOf: w.now.Format(time.RFC3339), Completeness: fleet.CompletenessComplete, Publisher: "pacto-operator", Join: j}
	b, err := delivery.SignTargetSnapshot(w.t.Context(), edSigner(key), delivery.NewTargetSnapshotStatement(s))
	if err == nil {
		_, err = deliveryrecord.PushTargetSnapshot(w.t.Context(), w.repo, uid, b)
	}
	if err != nil {
		w.t.Fatal(err)
	}
	w.keys[uid] = key
	return key
}

// join pushes a join record of uid naming hub, signed by user at at, that
// approves (or removes) key.
func (w *peerWorld) join(uid, hub string, dec delivery.JoinDecision, user string, key ed25519.PrivateKey, at time.Time) {
	w.t.Helper()
	w.n++
	b := fmt.Appendf(nil, `{"join":%d}`, w.n)
	w.joins[string(b)] = delivery.VerifiedJoin{Record: delivery.ClusterJoin{Cluster: uid, Hub: hub, KeyID: signing.Hint(key.Public()), Decision: dec},
		User: user, SignedAt: at, Verified: true}
	stream := delivery.ClusterJoinStream(uid)
	caps, err := deliveryrecord.EnsureAnchor(w.t.Context(), w.repo, stream)
	if err == nil {
		_, err = deliveryrecord.Push(w.t.Context(), w.repo, stream, caps.Referrers, delivery.PredicateClusterJoin, b, nil)
	}
	if err != nil {
		w.t.Fatal(err)
	}
}

// approvedHere approves this cluster's own key by hub H.
func (w *peerWorld) approvedHere() {
	w.join("cid", hubH, delivery.JoinApprove, "oidc:hub-admin@acme.com", w.own, w.now)
}

// recorder is a fresh recorder key and its trust entry.
func recorder(t *testing.T) (ed25519.PrivateKey, *delivery.TrustedKey) {
	t.Helper()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	p, _ := delivery.PublicKeyPEM(pub)
	return key, &delivery.TrustedKey{Name: "claimed", PublicKey: p, Streams: []string{"*/*/*"}}
}

func stagingStream(uid string) delivery.JoinStream {
	return delivery.JoinStream{Service: delivery.ServiceRef{Domain: "", Name: "payments"}, Environment: "staging", Stream: uid + "/payments-staging/payments-staging"}
}

// member publishes a member named name that runs the payments staging
// stream, recorded under rk, approved by alice for hub.
func (w *peerWorld) member(uid, name, hub string, rk *delivery.TrustedKey, edit func(*delivery.SnapshotJoin)) {
	w.t.Helper()
	key := w.snapshot(uid, name, func(j *delivery.SnapshotJoin) {
		j.RecorderKey, j.StreamsRepository = rk, peerRepo
		s := stagingStream(uid)
		s.Service.Domain = w.pe.root
		j.Streams = []delivery.JoinStream{s}
		if edit != nil {
			edit(j)
		}
	})
	w.join(uid, hub, delivery.JoinApprove, peerAlice, key, w.now)
}

// production passes and returns the production row.
func (w *peerWorld) production() delivery.PlacementRow {
	w.t.Helper()
	w.now = w.now.Add(time.Minute) // a new read of the members
	w.pe.pass(w.t)
	return rowOf(w.t, w.pe.rows(w.t), "payments", "production")
}

// wantUnplaced wants production unplaced with why and no Stage.
func (w *peerWorld) wantUnplaced(why string) {
	w.t.Helper()
	if r := w.production(); r.Stage != "" || r.Placement != nil || r.Reason != why {
		w.t.Fatalf("production %+v, want %q", r, why)
	}
	if err := w.pe.c.Get(w.t.Context(), client.ObjectKey{Namespace: "payments-production", Name: "payments-production"}, &dv1.Stage{}); err == nil {
		w.t.Fatal("a Stage was created")
	}
}

const noPeer = "upstream staging runs on no member this cluster trusts: ask an approver who may approve clusterjoins here to approve the member that runs it"

func TestFollowUpstreamOnMember(t *testing.T) {
	w := newPeerWorld(t)
	w.approvedHere()
	rkey, rk := recorder(t)
	w.member(stageEU, "stage-eu", hubH, rk, nil)
	r := w.production()
	if r.Stage != "payments-production/payments-production" || r.Reason != "" {
		t.Fatalf("production %+v %+v", r, r.Placement)
	}
	s := w.pe.stage(t, "payments-production", "payments-production")
	stream := stageEU + "/payments-staging/payments-staging"
	if up := s.Spec.Candidates.UpstreamRecords; up == nil || *up != (delivery.RemoteStage{Repository: peerRepo, Stream: stream}) ||
		s.Spec.Candidates.Registry != nil || s.Spec.Candidates.UpstreamStage != nil {
		t.Fatalf("candidates %+v", s.Spec.Candidates)
	}
	k := w.p.Key(stream)
	if k == nil || k.Name != "member-recorder:stage-eu" || len(k.Streams) != 1 || k.Streams[0] != stream {
		t.Fatalf("key %+v", k)
	}
	verify, err := delivery.TrustConfig{Keys: []delivery.TrustedKey{*k}}.Verifier(signing.OpenKey)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := edSigner(rkey).Sign(t.Context(), delivery.PayloadType, []byte(`{}`))
	if err := verify(stream, 1, b); err != nil {
		t.Fatalf("a record on the followed stream: %v", err)
	}
	// The members are read once per peersTTL.
	w.pe.pass(t)
	if w.p.Key(stream) == nil {
		t.Fatal("the binding was lost")
	}
}

// TestFollowUpstreamSynonymStream: a member whose Stage records stage is
// followed for this cluster's rung staging.
func TestFollowUpstreamSynonymStream(t *testing.T) {
	w := newPeerWorld(t)
	w.approvedHere()
	_, rk := recorder(t)
	w.member(stageEU, "stage-eu", hubH, rk, func(j *delivery.SnapshotJoin) { j.Streams[0].Environment = "stage" })
	if r := w.production(); r.Stage != "payments-production/payments-production" || r.Reason != "" {
		t.Fatalf("production %+v", r)
	}
}

func TestFollowUpstreamUsesOwnHub(t *testing.T) {
	w := newPeerWorld(t)
	_, rk := recorder(t)
	w.member(stageEU, "stage-eu", hubH, rk, nil)
	w.wantUnplaced("upstream staging runs on another member, and no hub approved this cluster's own key " +
		"(no hub approved this cluster's key by a record its approver signers verify): approve this cluster on its hub first")
	w.approvedHere()
	w.join("cid", "hub-2", delivery.JoinApprove, "oidc:other@acme.com", w.own, w.now)
	w.wantUnplaced("upstream staging runs on another member, and no hub approved this cluster's own key " +
		"(more than one hub approved this cluster (hub-2, hub-h)): approve this cluster on its hub first")
}

func TestFollowUpstreamIgnoresOtherHubApprovals(t *testing.T) {
	w := newPeerWorld(t)
	w.approvedHere()
	_, rk := recorder(t)
	w.member(stageEU, "stage-eu", "hub-2", rk, nil)
	w.wantUnplaced(noPeer)
}

func TestFollowUpstreamRecordsForAnotherHubIgnored(t *testing.T) {
	w := newPeerWorld(t)
	w.approvedHere()
	_, rk := recorder(t)
	w.member(stageEU, "stage-eu", hubH, rk, nil)
	// A newer removal for another hub does not remove it for this one.
	w.join(stageEU, "hub-2", delivery.JoinRemove, peerAlice, w.keys[stageEU], w.now.Add(time.Hour))
	if r := w.production(); r.Stage == "" {
		t.Fatalf("production %+v", r)
	}
}

func TestFollowUpstreamApproverNotGrantedHere(t *testing.T) {
	w := newPeerWorld(t)
	w.approvedHere()
	_, rk := recorder(t)
	w.member(stageEU, "stage-eu", hubH, rk, nil)
	w.rights[peerAlice] = false
	w.wantUnplaced(noPeer)
	// A review that fails counts no approval either, and is asked again.
	w.rights[peerAlice], w.revErr = true, errors.New("apiserver down")
	w.p.verdicts = nil
	w.wantUnplaced(noPeer)
	w.revErr = nil
	if r := w.production(); r.Stage == "" {
		t.Fatalf("production %+v", r)
	}
}

func TestFollowUpstreamTwoMembersHostIt(t *testing.T) {
	w := newPeerWorld(t)
	w.approvedHere()
	_, rk := recorder(t)
	w.member(stageEU, "stage-eu", hubH, rk, nil)
	w.member(stageUS, "stage-us", hubH, rk, nil)
	w.wantUnplaced("upstream staging runs on stage-eu and stage-us: name one in a placement rule (clusters, by uid)")
	w.pe.r.Placement = func(context.Context) (*delivery.PlacementPolicy, string) {
		return &delivery.PlacementPolicy{Rules: []delivery.PlacementRule{
			{Name: "other", Services: []string{"orders"}, Environments: []string{"staging"}, Namespace: "x", ClusterUIDs: []string{stageEU}},
			{Name: "us", Environments: []string{"staging"}, Namespace: "{service}-{environment}", ClusterUIDs: []string{stageUS}}}}, ""
	}
	w.production()
	if up := w.pe.stage(t, "payments-production", "payments-production").Spec.Candidates.UpstreamRecords; up == nil || !strings.HasPrefix(up.Stream, stageUS+"/") {
		t.Fatalf("upstream %+v", up)
	}
}

func TestFollowUpstreamNoStreamYet(t *testing.T) {
	w := newPeerWorld(t)
	w.approvedHere()
	_, rk := recorder(t)
	w.member(stageEU, "stage-eu", hubH, rk, func(j *delivery.SnapshotJoin) {
		j.Streams, j.Environments = nil, []delivery.AdvertisedEnvironment{{Name: "staging", Source: "cluster name"}}
	})
	// A member that advertises no environment and records no stream runs nothing here.
	w.member(stageUS, "stage-us", hubH, rk, func(j *delivery.SnapshotJoin) { j.Streams = nil })
	w.wantUnplaced("upstream staging on stage-eu records no Stage stream yet")
}

func TestFollowUpstreamTwoStreamsOnOneMember(t *testing.T) {
	w := newPeerWorld(t)
	w.approvedHere()
	_, rk := recorder(t)
	w.member(stageEU, "stage-eu", hubH, rk, func(j *delivery.SnapshotJoin) {
		s := j.Streams[0]
		s.Stream = stageEU + "/payments-staging/payments-staging-2"
		j.Streams = append(j.Streams, s)
	})
	w.wantUnplaced("upstream staging on stage-eu records 2 Stage streams of payments, so Pacto cannot tell which one to follow")
}

func TestFollowUpstreamNoRecorderKey(t *testing.T) {
	w := newPeerWorld(t)
	w.approvedHere()
	w.member(stageEU, "stage-eu", hubH, nil, nil)
	w.wantUnplaced("upstream staging on stage-eu advertises no recorder key: set delivery.recorder.signing.publicKey on stage-eu")
}

func TestFollowUpstreamKeyScopedToStream(t *testing.T) {
	w := newPeerWorld(t)
	w.approvedHere()
	rkey, rk := recorder(t)
	w.member(stageEU, "stage-eu", hubH, rk, func(j *delivery.SnapshotJoin) {
		j.Streams = append(j.Streams, delivery.JoinStream{Service: delivery.ServiceRef{Name: "orders"}, Environment: "staging", Stream: stageEU + "/orders/orders-staging"})
	})
	w.production()
	other := stageEU + "/orders/orders-staging"
	if w.p.Key(other) != nil {
		t.Fatal("a stream no placement follows has a key")
	}
	k := w.p.Key(stageEU + "/payments-staging/payments-staging")
	verify, _ := delivery.TrustConfig{Keys: []delivery.TrustedKey{*k}}.Verifier(signing.OpenKey)
	b, _ := edSigner(rkey).Sign(t.Context(), delivery.PayloadType, []byte(`{}`))
	if err := verify(other, 1, b); err == nil {
		t.Fatal("the member key verified a record on another stream")
	}
	// Once the member is removed the key verifies nothing, and the Stage
	// keeps its upstream: it offers nothing until a member is trusted again.
	w.join(stageEU, hubH, delivery.JoinRemove, peerAlice, w.keys[stageEU], w.now.Add(time.Hour))
	if r := w.production(); r.Reason != noPeer {
		t.Fatalf("production %+v", r)
	}
	if up := w.pe.stage(t, "payments-production", "payments-production").Spec.Candidates; up.UpstreamRecords == nil || up.Registry != nil {
		t.Fatalf("candidates %+v", up)
	}
	if w.p.Key(stageEU+"/payments-staging/payments-staging") != nil {
		t.Fatal("a removed member's key is still bound")
	}
}

func TestFollowUpstreamNeverFallsBackToRegistry(t *testing.T) {
	w := newPeerWorld(t)
	w.pe.r.Peers = nil
	w.wantUnplaced(noPeer)
	w.pe.r.Peers = w.p
	w.approvedHere()
	w.wantUnplaced(noPeer) // no member at all
	w.pe.reg.fault = func(r *http.Request) bool { return strings.Contains(r.URL.Path, "pacto-targets/tags/list") }
	r := w.production()
	if r.Stage != "" || !strings.HasPrefix(r.Reason, "upstream staging runs on another member, and the members in "+w.p.Targets+" could not be read (") {
		t.Fatalf("production %+v", r)
	}
	if err := w.pe.c.Get(t.Context(), client.ObjectKey{Namespace: "payments-production", Name: "payments-production"}, &dv1.Stage{}); err == nil {
		t.Fatal("a Stage was created")
	}
}

// TestPeersSnapshotFaults: a member whose snapshot cannot be read, names
// another cluster or is not signed by its own key is never a peer.
func TestPeersSnapshotFaults(t *testing.T) {
	w := newPeerWorld(t)
	w.approvedHere()
	_, rk := recorder(t)
	w.member(stageEU, "stage-eu", hubH, rk, nil)
	// stage-us's tag holds stage-eu's snapshot.
	desc, err := w.repo.Resolve(t.Context(), delivery.TargetSnapshotTagPrefix+stageEU)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.repo.Tag(t.Context(), desc, delivery.TargetSnapshotTagPrefix+stageUS); err != nil {
		t.Fatal(err)
	}
	w.join(stageUS, hubH, delivery.JoinApprove, peerAlice, w.keys[stageEU], w.now)
	if c := w.p.candidate(t.Context(), w.repo, stageUS); c.Err != "its snapshot names cluster "+stageEU {
		t.Fatalf("%+v", c)
	}
	if c := w.p.candidate(t.Context(), w.repo, "gone"); !strings.HasPrefix(c.Err, "its snapshot could not be used: ") {
		t.Fatalf("%+v", c)
	}
	// A join block whose key did not sign the snapshot.
	w.snapshot("forged", "forged", func(j *delivery.SnapshotJoin) {
		other, _, _ := ed25519.GenerateKey(rand.Reader)
		der, _ := x509.MarshalPKIXPublicKey(other)
		j.KeyID, j.PublicKey = signing.Hint(other), base64.StdEncoding.EncodeToString(der)
	})
	if c := w.p.candidate(t.Context(), w.repo, "forged"); c.SelfSigned || c.Join == nil {
		t.Fatalf("%+v", c)
	}
	// A cut or unreadable join record listing leaves the member pending.
	w.pe.reg.fault = func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/referrers/") }
	if rs := w.p.records(t.Context(), w.repo, stageEU, hubH); rs.Err == "" {
		t.Fatalf("%+v", rs)
	}
	w.pe.reg.fault = nil
	if r := w.production(); r.Stage == "" {
		t.Fatalf("production %+v", r)
	}
}

// TestPeersCachesBounded: the verified records and the reviews are
// remembered up to maxPeerCache, then cleared whole.
func TestPeersCachesBounded(t *testing.T) {
	w := newPeerWorld(t)
	w.p.joins = map[string]delivery.VerifiedJoin{}
	w.p.verdicts = map[string]peerVerdict{}
	for i := range maxPeerCache {
		w.p.joins[fmt.Sprint(i)] = delivery.VerifiedJoin{}
		w.p.verdicts[fmt.Sprint(i)] = peerVerdict{}
	}
	w.p.verify(deliveryrecord.Fetched{Manifest: "m"})
	if _, err := w.p.review(t.Context(), peerAlice, stageEU); err != nil {
		t.Fatal(err)
	}
	if len(w.p.joins) != 1 || len(w.p.verdicts) != 1 {
		t.Fatalf("%d %d", len(w.p.joins), len(w.p.verdicts))
	}
	// The real verifier refuses an opaque record.
	w.p.openJoin = nil
	if v := w.p.verify(deliveryrecord.Fetched{Manifest: "n", Bundle: []byte("x")}); v.Verified {
		t.Fatalf("%+v", v)
	}
}

// TestUpstreamReaderMergesMemberKeys: the static trust config still
// verifies its own streams, a member key verifies the one stream it is
// bound to, and a stream no key covers reads as unverified.
func TestUpstreamReaderMergesMemberKeys(t *testing.T) {
	ctx := context.Background()
	s, _, keyA := testStore(t)
	_, keyB, _ := ed25519.GenerateKey(rand.Reader)
	write := func(stream string, key ed25519.PrivateKey) {
		a, err := s.anchor(ctx, stream)
		if err != nil {
			t.Fatal(err)
		}
		st := delivery.NewGap("c", delivery.ObjectMeta{Name: "st"}, 1, delivery.Chain{Stream: stream, Seq: 1}, a.desc.Digest.String(), "t")
		bundle, _, _ := delivery.SignRecord(ctx, edSigner(key), st)
		if err := s.write(ctx, bundle, st, locations{anchor: a}); err != nil {
			t.Fatal(err)
		}
	}
	write("cid-a/ns/a", keyA)
	write("m-1/ns/b", keyB)
	write("m-1/ns/c", keyB)
	pa, _ := delivery.PublicKeyPEM(keyA.Public())
	pb, _ := delivery.PublicKeyPEM(keyB.Public())
	static := &delivery.TrustConfig{Keys: []delivery.TrustedKey{{Name: "a", PublicKey: pa, Streams: []string{"cid-a/*/*"}}}}
	member := func(stream string) *delivery.TrustedKey {
		if stream != "m-1/ns/b" {
			return nil
		}
		return &delivery.TrustedKey{Name: "member-recorder:m-1", PublicKey: pb, Streams: []string{stream}}
	}
	read, err := UpstreamReader(static, member, ocireferrer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	unverified := func(why string) bool {
		return strings.Contains(why, "could not be verified") || strings.Contains(why, "no record was found")
	}
	for stream, want := range map[string]bool{"cid-a/ns/a": false, "m-1/ns/b": false, "m-1/ns/c": true} {
		for range 2 { // the second read reuses the reader
			if _, why := read(ctx, delivery.RemoteStage{Repository: s.Streams, Stream: stream}, delivery.RecordMark{}, nil); unverified(why) != want {
				t.Errorf("%s: %q", stream, why)
			}
		}
	}
	if _, why := read(ctx, delivery.RemoteStage{Repository: "Not A Repo", Stream: "m-1/ns/b"}, delivery.RecordMark{}, nil); why == "" {
		t.Error("a bad repository was read")
	}
	// No static trust: a stream no member key covers trusts no key.
	read, _ = UpstreamReader(nil, member, ocireferrer.Options{})
	if _, why := read(ctx, delivery.RemoteStage{Repository: s.Streams, Stream: "m-1/ns/c"}, delivery.RecordMark{}, nil); why != noUpstreamTrust {
		t.Errorf("%q", why)
	}
	if read, err := UpstreamReader(nil, nil, ocireferrer.Options{}); read != nil || err != nil {
		t.Error("no trust built a reader")
	}
	if _, err := UpstreamReader(&delivery.TrustConfig{Keys: []delivery.TrustedKey{{Name: "bad", PublicKey: "x"}}}, nil, ocireferrer.Options{}); err == nil {
		t.Error("a bad static key was accepted")
	}
	// A member key that cannot verify says so; readers are bounded.
	defer func(n int) { maxMemberReaders = n }(maxMemberReaders)
	maxMemberReaders = 1
	n := 0
	read, _ = UpstreamReader(nil, func(stream string) *delivery.TrustedKey {
		n++
		if stream == "bad" {
			return &delivery.TrustedKey{Name: "member-recorder:bad", PublicKey: "x", Streams: []string{stream}}
		}
		return &delivery.TrustedKey{Name: fmt.Sprint(n), PublicKey: pb, Streams: []string{stream}}
	}, ocireferrer.Options{})
	if _, why := read(ctx, delivery.RemoteStage{Repository: s.Streams, Stream: "bad"}, delivery.RecordMark{}, nil); !strings.HasPrefix(why, "the recorder key member-recorder:bad cannot verify records: ") {
		t.Errorf("%q", why)
	}
	for range 2 {
		if _, why := read(ctx, delivery.RemoteStage{Repository: s.Streams, Stream: "m-1/ns/b"}, delivery.RecordMark{}, nil); unverified(why) {
			t.Errorf("%q", why)
		}
	}
}
