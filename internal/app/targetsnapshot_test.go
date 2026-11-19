package app

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// snapNow is the clock of every snapshot test.
var snapNow = time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

const (
	snapA = "6f1e0000-aaaa-4000-8000-000000000001"
	snapB = "6f1e0000-bbbb-4000-8000-000000000002"
)

// snapEnv is an in-process registry holding a pacto-targets repository, and
// the recorder key that signs its snapshots.
type snapEnv struct {
	repo     string
	key      ed25519.PrivateKey
	pub      string
	blobGets atomic.Int64
	hang     atomic.Bool
	// fail answers every request whose path holds it with 403.
	fail atomic.Value
}

func newSnapEnv(t *testing.T) *snapEnv {
	t.Helper()
	t.Setenv(TrustEnv, "")
	e := &snapEnv{}
	e.fail.Store("")
	inner := testutil.NewReferrersRegistry(testutil.ReferrersOptions{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f := e.fail.Load().(string); f != "" && strings.Contains(r.URL.Path, f) {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/blobs/") {
			e.blobGets.Add(1)
			if e.hang.Load() {
				<-r.Context().Done()
				return
			}
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	e.repo = strings.TrimPrefix(srv.URL, "http://") + "/acme/" + delivery.TargetSnapshotRepository
	_, e.key, _ = ed25519.GenerateKey(rand.Reader)
	e.pub = pemOf(t, e.key)
	return e
}

func pemOf(t *testing.T, key ed25519.PrivateKey) string {
	t.Helper()
	pub, err := delivery.PublicKeyPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

// ledgerSnapshot is cluster's snapshot taken at asOf: a Pacto for ledger in
// billing that targets the ledger Deployment, which the workloads list too,
// beside a cron Deployment no Pacto targets.
func ledgerSnapshot(cluster string, asOf time.Time) delivery.TargetSnapshot {
	var p delivery.PactoView
	p.Metadata.Namespace, p.Metadata.Name = "billing", "ledger"
	p.Spec.Target = &delivery.PactoTargetView{ServiceName: "ledger"}
	p.Status.ContractStatus, p.Status.LastReconciledAt = "Compliant", asOf.Format(time.RFC3339)
	p.Status.Contract = &delivery.ContractInfoView{ServiceName: "ledger", Version: "1.0.0",
		ResolvedRef: "oci://reg.example/acme/ledger@sha256:" + strings.Repeat("a", 64)}
	return delivery.TargetSnapshot{Cluster: cluster, AsOf: asOf.Format(time.RFC3339), Completeness: fleet.CompletenessComplete,
		Publisher: "pacto-system/pacto-operator-delivery-recorder", Pactos: []delivery.PactoView{p},
		Workloads: []fleet.RawInferredService{{Namespace: "billing", Kind: "Deployment", Name: "ledger"}, {Namespace: "billing", Kind: "Deployment", Name: "cron"}}}
}

// push signs s with key and tags it cluster-<tagCluster>.
func (e *snapEnv) push(t *testing.T, key ed25519.PrivateKey, tagCluster string, s delivery.TargetSnapshot) ocispec.Descriptor {
	t.Helper()
	kp, _ := signing.FromPrivateKey(key)
	bundle, err := delivery.SignTargetSnapshot(t.Context(), signing.Keyed(kp, ""), delivery.NewTargetSnapshotStatement(s))
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := ocireferrer.Open(e.repo, ocireferrer.Options{})
	desc, err := deliveryrecord.PushTargetSnapshot(t.Context(), repo, tagCluster, bundle)
	if err != nil {
		t.Fatal(err)
	}
	return desc
}

// trustFile writes a trust config of keys and returns its path.
func trustFile(t *testing.T, keys ...delivery.TrustedKey) string {
	t.Helper()
	raw, _ := yaml.Marshal(delivery.TrustConfig{Keys: keys})
	p := filepath.Join(t.TempDir(), "trust.yaml")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// scoped is a trust entry for e's key scoped to clusters.
func (e *snapEnv) scoped(name string, clusters ...string) delivery.TrustedKey {
	k := delivery.TrustedKey{Name: name, PublicKey: e.pub}
	for _, c := range clusters {
		k.Streams = append(k.Streams, c+"/*/*")
	}
	return k
}

func snapOpts(trust string, refs ...string) FleetOptions {
	return FleetOptions{TargetSnapshot: refs, TargetSnapshotTrustFile: trust, Now: func() time.Time { return snapNow }}
}

// readSnap resolves opts on svc and builds a fleet of what it read.
func readSnap(t *testing.T, svc *Service, opts FleetOptions) *fleet.FleetSnapshot {
	t.Helper()
	srcs, err := svc.targetSnapshotSources(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := fleet.Build(t.Context(), fleet.BuildOptions{Now: opts.Now}, srcs...)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func stateOf(t *testing.T, snap *fleet.FleetSnapshot, id string) fleet.SourceState {
	t.Helper()
	for _, s := range snap.Sources {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no source %s in %+v", id, snap.Sources)
	return fleet.SourceState{}
}

func hasLim(snap *fleet.FleetSnapshot, id, code string) bool {
	return slices.ContainsFunc(snap.Limitations, func(l fleet.Limitation) bool { return l.Source == id && l.Code == code })
}

// wantUnused fails unless source id is unavailable with code and contributed
// no target.
func wantUnused(t *testing.T, snap *fleet.FleetSnapshot, id, code string) {
	t.Helper()
	if st := stateOf(t, snap, id); st.Status != fleet.SourceUnavailable || st.TargetCount != 0 || !hasLim(snap, id, code) {
		t.Fatalf("%s = %+v, limitations %+v", id, st, snap.Limitations)
	}
	if snap.Completeness == fleet.CompletenessComplete {
		t.Fatal("an unused snapshot read complete")
	}
}

func TestTargetSnapshotSourcesReadEveryCluster(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, ledgerSnapshot(snapA, snapNow))
	e.push(t, e.key, snapB, ledgerSnapshot(snapB, snapNow))
	e.push(t, e.key, "abc", ledgerSnapshot("abc", snapNow))
	svc := &Service{}
	srcs, err := svc.targetSnapshotSources(t.Context(), snapOpts(trustFile(t, e.scoped("rec", snapA, snapB, "abc")), "oci://"+e.repo))
	if err != nil || len(srcs) != 6 {
		t.Fatalf("sources %d, %v", len(srcs), err)
	}
	for _, src := range srcs {
		col, _ := src.Collect(t.Context())
		id := strings.TrimSuffix(strings.TrimPrefix(src.ID(), "targets-"), workloadsSuffix)
		if col.Cluster == nil || col.Cluster.ID != id || col.Cluster.Name != id[:min(8, len(id))] || col.Cluster.NameSource != fleet.ClusterNameFromUID {
			t.Errorf("%s: cluster %+v", src.ID(), col.Cluster)
		}
	}
	snap := readSnap(t, svc, snapOpts(trustFile(t, e.scoped("rec", snapA, snapB, "abc")), "oci://"+e.repo))
	if snap.Completeness != fleet.CompletenessComplete || len(snap.Targets) != 3 {
		t.Fatalf("completeness %s, targets %d, limitations %+v", snap.Completeness, len(snap.Targets), snap.Limitations)
	}
	if st := stateOf(t, snap, "targets-abc"); st.Cluster.Name != "abc" || st.Kind != fleet.SourceKindTargetSnapshot {
		t.Errorf("short cluster = %+v", st)
	}
}

// TestTargetSnapshotTwoClustersHideDeclaredWorkloads: in each cluster the
// Deployment a Pacto targets is not inferred; the other one is.
func TestTargetSnapshotTwoClustersHideDeclaredWorkloads(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, ledgerSnapshot(snapA, snapNow))
	e.push(t, e.key, snapB, ledgerSnapshot(snapB, snapNow))
	snap := readSnap(t, &Service{}, snapOpts(trustFile(t, e.scoped("rec", snapA, snapB)), "oci://"+e.repo))
	var inferred []string
	for _, s := range snap.Services {
		if s.Inferred() {
			inferred = append(inferred, s.Workload.Cluster+"/"+s.Workload.Name)
		}
	}
	slices.Sort(inferred)
	if !slices.Equal(inferred, []string{snapA + "/cron", snapB + "/cron"}) {
		t.Fatalf("inferred %v", inferred)
	}
}

// TestTargetSnapshotInferredDeclarationVerified: an available scoped snapshot
// verifies the declaration of its workloads; an unverified one does not.
func TestTargetSnapshotInferredDeclarationVerified(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, ledgerSnapshot(snapA, snapNow))
	snap := readSnap(t, &Service{}, snapOpts(trustFile(t, e.scoped("rec", snapA)), "oci://"+e.repo))
	if len(snap.Services) == 0 || slices.ContainsFunc(snap.Limitations, func(l fleet.Limitation) bool { return l.Code == fleet.LimitationDeclarationUnverified }) {
		t.Fatalf("limitations %+v", snap.Limitations)
	}
	snap = readSnap(t, &Service{}, snapOpts("", "oci://"+e.repo))
	if !slices.ContainsFunc(snap.Limitations, func(l fleet.Limitation) bool { return l.Code == fleet.LimitationDeclarationUnverified }) {
		t.Fatalf("an unverified snapshot verified a declaration: %+v", snap.Limitations)
	}
}

func TestTargetSnapshotUnscopedKeyIsPartial(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, ledgerSnapshot(snapA, snapNow))
	bare := filepath.Join(t.TempDir(), "recorder.pub.pem")
	_ = os.WriteFile(bare, []byte(e.pub), 0o600)
	snap := readSnap(t, &Service{}, snapOpts(bare, "oci://"+e.repo))
	id := "targets-" + snapA
	if st := stateOf(t, snap, id); st.Status != fleet.SourcePartial || !strings.HasPrefix(st.Signer, "key:") || !hasLim(snap, id, fleet.LimitationTargetSnapshotUnscoped) {
		t.Fatalf("source %+v, limitations %+v", st, snap.Limitations)
	}
	if snap.Completeness == fleet.CompletenessComplete {
		t.Fatal("an unscoped snapshot read complete")
	}
}

func TestTargetSnapshotSignerIsTrustKeyNotClaim(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, ledgerSnapshot(snapA, snapNow))
	id := "targets-" + snapA
	snap := readSnap(t, &Service{}, snapOpts(trustFile(t, e.scoped("ci-demo", snapA)), "oci://"+e.repo))
	if st := stateOf(t, snap, id); st.Signer != "ci-demo" || st.Status != fleet.SourceAvailable {
		t.Fatalf("source %+v", st)
	}
	snap = readSnap(t, &Service{}, snapOpts(trustFile(t, e.scoped("", snapA)), "oci://"+e.repo))
	if st := stateOf(t, snap, id); st.Signer != "key:"+signing.Hint(e.key.Public()) {
		t.Fatalf("source %+v", st)
	}
}

func TestTargetSnapshotPinnedClusterMissingIsPartial(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, ledgerSnapshot(snapA, snapNow))
	snap := readSnap(t, &Service{}, snapOpts(trustFile(t, e.scoped("rec", snapA, snapB)),
		"oci://"+e.repo+":cluster-"+snapA, "oci://"+e.repo+":cluster-"+snapB))
	if st := stateOf(t, snap, "targets-"+snapA); st.Status != fleet.SourceAvailable {
		t.Fatalf("pinned present = %+v", st)
	}
	wantUnused(t, snap, "targets-"+snapB, fleet.LimitationTargetSnapshotInvalid)
}

func TestTargetSnapshotUnverifiedIsPartial(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, ledgerSnapshot(snapA, snapNow))
	snap := readSnap(t, &Service{}, snapOpts("", "oci://"+e.repo))
	id := "targets-" + snapA
	if st := stateOf(t, snap, id); st.Status != fleet.SourcePartial || st.Signer != "" || !hasLim(snap, id, fleet.LimitationTargetSnapshotUnverified) || len(snap.Targets) != 1 {
		t.Fatalf("source %+v, limitations %+v", st, snap.Limitations)
	}
	if snap.Completeness == fleet.CompletenessComplete {
		t.Fatal("an unverified snapshot read complete")
	}
}

func TestTargetSnapshotBadSignatureUnused(t *testing.T) {
	e := newSnapEnv(t)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	e.push(t, other, snapA, ledgerSnapshot(snapA, snapNow))
	snap := readSnap(t, &Service{}, snapOpts(trustFile(t, e.scoped("rec", snapA)), "oci://"+e.repo))
	wantUnused(t, snap, "targets-"+snapA, fleet.LimitationTargetSnapshotInvalid)
}

// TestTargetSnapshotClusterMismatchUnused: cluster A's snapshot under the tag
// of cluster B is refused, even signed by a key scoped to both.
func TestTargetSnapshotClusterMismatchUnused(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapB, ledgerSnapshot(snapA, snapNow))
	snap := readSnap(t, &Service{}, snapOpts(trustFile(t, e.scoped("rec", snapA, snapB)), "oci://"+e.repo))
	wantUnused(t, snap, "targets-"+snapB, fleet.LimitationTargetSnapshotInvalid)
}

func TestTargetSnapshotStaleIsStale(t *testing.T) {
	e := newSnapEnv(t)
	asOf := snapNow.Add(-16 * time.Minute)
	e.push(t, e.key, snapA, ledgerSnapshot(snapA, asOf))
	snap := readSnap(t, &Service{}, snapOpts(trustFile(t, e.scoped("rec", snapA)), "oci://"+e.repo))
	id := "targets-" + snapA
	if st := stateOf(t, snap, id); st.Status != fleet.SourceStale || !st.LastSuccessfulSync.Equal(asOf) || !hasLim(snap, id, fleet.LimitationTargetSnapshotStale) {
		t.Fatalf("source %+v", st)
	}
	if snap.Completeness == fleet.CompletenessComplete {
		t.Fatal("a stale snapshot read complete")
	}
}

// TestTargetSnapshotReplayedOldIsStale: the tag re-pointed to an older signed
// snapshot reads stale.
func TestTargetSnapshotReplayedOldIsStale(t *testing.T) {
	e := newSnapEnv(t)
	old := e.push(t, e.key, snapA, ledgerSnapshot(snapA, snapNow.Add(-time.Hour)))
	e.push(t, e.key, snapA, ledgerSnapshot(snapA, snapNow))
	repo, _ := ocireferrer.Open(e.repo, ocireferrer.Options{})
	if err := repo.Tag(t.Context(), old, "cluster-"+snapA); err != nil {
		t.Fatal(err)
	}
	snap := readSnap(t, &Service{}, snapOpts(trustFile(t, e.scoped("rec", snapA)), "oci://"+e.repo))
	if st := stateOf(t, snap, "targets-"+snapA); st.Status != fleet.SourceStale || st.Ref != e.repo+"@"+old.Digest.String() {
		t.Fatalf("source %+v", st)
	}
}

// TestTargetSnapshotDigestPinUnverifiable: a snapshot pinned by digest that
// does not verify is unused, under no cluster.
func TestTargetSnapshotDigestPinUnverifiable(t *testing.T) {
	e := newSnapEnv(t)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	desc := e.push(t, other, snapA, ledgerSnapshot(snapA, snapNow))
	snap := readSnap(t, &Service{}, snapOpts(trustFile(t, e.scoped("rec", snapA)), e.repo+"@"+desc.Digest.String()))
	id := "targets-" + desc.Digest.Encoded()[:12]
	wantUnused(t, snap, id, fleet.LimitationTargetSnapshotInvalid)
	if stateOf(t, snap, id).Cluster != nil {
		t.Fatal("an unverifiable digest pin named a cluster")
	}
}

func TestSnapshotSignerKeyless(t *testing.T) {
	v := delivery.TargetSnapshotVerdict{Verified: true, Key: delivery.TrustedKey{Identity: "ci@acme", Issuer: "https://issuer"}}
	if got := snapshotSigner(v); got != "ci@acme" {
		t.Fatal(got)
	}
}

func TestTargetSnapshotFutureAsOfInvalid(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, ledgerSnapshot(snapA, snapNow.Add(5*time.Minute)))
	snap := readSnap(t, &Service{}, snapOpts(trustFile(t, e.scoped("rec", snapA)), "oci://"+e.repo))
	wantUnused(t, snap, "targets-"+snapA, fleet.LimitationTargetSnapshotInvalid)
}

// TestTargetSnapshotBoundIsPartial: more cluster tags than a read takes leave
// a partial bound source; a snapshot too large is an unused one.
func TestTargetSnapshotBoundIsPartial(t *testing.T) {
	e := newSnapEnv(t)
	repo, _ := ocireferrer.Open(e.repo, ocireferrer.Options{})
	raw := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"artifactType":%q,"config":{"mediaType":%q,"digest":"sha256:%064d","size":2},`+
		`"layers":[{"mediaType":%q,"digest":"sha256:%064d","size":%d}]}`, ocispec.MediaTypeImageManifest, delivery.TargetSnapshotArtifactType,
		ocispec.MediaTypeEmptyJSON, 0, delivery.BundleMediaType, 0, delivery.MaxTargetSnapshotBytes+1))
	desc := ocireferrer.Descriptor(ocispec.MediaTypeImageManifest, raw)
	if err := repo.Manifests().PushReference(t.Context(), desc, bytes.NewReader(raw), "cluster-big"); err != nil {
		t.Fatal(err)
	}
	for i := range delivery.MaxTargetSnapshots {
		if err := repo.Tag(t.Context(), desc, fmt.Sprintf("cluster-z%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	snap := readSnap(t, &Service{}, snapOpts("", "oci://"+e.repo))
	if st := stateOf(t, snap, "targets"); st.Status != fleet.SourcePartial || !hasLim(snap, "targets", fleet.LimitationTargetSnapshotBound) {
		t.Fatalf("listing source %+v", st)
	}
	wantUnused(t, snap, "targets-big", fleet.LimitationTargetSnapshotBound)
}

func TestTargetSnapshotPinsDigest(t *testing.T) {
	e := newSnapEnv(t)
	desc := e.push(t, e.key, snapA, ledgerSnapshot(snapA, snapNow))
	trust := trustFile(t, e.scoped("rec", snapA))
	snap := readSnap(t, &Service{}, snapOpts(trust, "oci://"+e.repo))
	if st := stateOf(t, snap, "targets-"+snapA); st.Ref != e.repo+"@"+desc.Digest.String() || st.Cluster.ID != snapA {
		t.Fatalf("source %+v", st)
	}
	// Pinned by digest, the snapshot reads the same; a second pin of the same
	// cluster keeps its own source id.
	snap = readSnap(t, &Service{}, snapOpts(trust, "oci://"+e.repo+"@"+desc.Digest.String(), e.repo+":cluster-"+snapA))
	if st := stateOf(t, snap, "targets-"+snapA); st.Status != fleet.SourceAvailable {
		t.Fatalf("source %+v", st)
	}
	if st := stateOf(t, snap, "targets-"+snapA+"-2"); st.Status != fleet.SourceAvailable {
		t.Fatalf("second pin %+v", st)
	}
	missing := digest.FromString("gone")
	snap = readSnap(t, &Service{}, snapOpts(trust, e.repo+"@"+missing.String()))
	wantUnused(t, snap, "targets-"+missing.Encoded()[:12], fleet.LimitationTargetSnapshotInvalid)
}

func TestTargetSnapshotCacheByDigest(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, ledgerSnapshot(snapA, snapNow))
	svc := &Service{}
	opts := snapOpts(trustFile(t, e.scoped("rec", snapA)), "oci://"+e.repo)
	readSnap(t, svc, opts)
	first := e.blobGets.Load()
	snap := readSnap(t, svc, opts)
	if e.blobGets.Load() != first || stateOf(t, snap, "targets-"+snapA).Status != fleet.SourceAvailable {
		t.Fatalf("blob reads %d then %d", first, e.blobGets.Load())
	}
	// A full cache starts over.
	for i := range maxTargetSnapshotCache {
		svc.snapshots.put(digest.FromString(fmt.Sprint(i)), nil)
	}
	if _, ok := svc.snapshots.get(digest.FromString("0")); ok {
		t.Fatal("a full cache kept its entries")
	}
}

// TestTargetSnapshotCacheReverifiesPerTrust: one cached digest verifies with
// a trust config that holds its key, reads unverified with none and is not
// used once the key is gone.
func TestTargetSnapshotCacheReverifiesPerTrust(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, ledgerSnapshot(snapA, snapNow))
	svc := &Service{}
	id := "targets-" + snapA
	snap := readSnap(t, svc, snapOpts(trustFile(t, e.scoped("rec", snapA)), "oci://"+e.repo))
	if st := stateOf(t, snap, id); st.Status != fleet.SourceAvailable || st.Signer != "rec" {
		t.Fatalf("trusted %+v", st)
	}
	snap = readSnap(t, svc, snapOpts("", "oci://"+e.repo))
	if st := stateOf(t, snap, id); st.Status != fleet.SourcePartial || st.Signer != "" {
		t.Fatalf("no trust %+v", st)
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	snap = readSnap(t, svc, snapOpts(trustFile(t, delivery.TrustedKey{Name: "other", PublicKey: pemOf(t, other), Streams: []string{snapA + "/*/*"}}), "oci://"+e.repo))
	wantUnused(t, snap, id, fleet.LimitationTargetSnapshotInvalid)
}

func TestTargetSnapshotCacheHitTurnsStale(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, ledgerSnapshot(snapA, snapNow))
	svc := &Service{}
	opts := snapOpts(trustFile(t, e.scoped("rec", snapA)), "oci://"+e.repo)
	readSnap(t, svc, opts)
	reads := e.blobGets.Load()
	opts.Now = func() time.Time { return snapNow.Add(16 * time.Minute) }
	snap := readSnap(t, svc, opts)
	if st := stateOf(t, snap, "targets-"+snapA); st.Status != fleet.SourceStale || e.blobGets.Load() != reads {
		t.Fatalf("cache hit %+v", st)
	}
}

func TestTargetSnapshotTimeout(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, ledgerSnapshot(snapA, snapNow))
	e.hang.Store(true)
	svc := &Service{sys: sysDeps{snapshotTimeout: 200 * time.Millisecond}}
	snap := readSnap(t, svc, snapOpts("", "oci://"+e.repo))
	wantUnused(t, snap, "targets-"+snapA, fleet.LimitationTargetSnapshotBound)
}

func TestTargetSnapshotPublisherLimitationsCarry(t *testing.T) {
	e := newSnapEnv(t)
	s := ledgerSnapshot(snapA, snapNow)
	s.Completeness = fleet.CompletenessPartial
	s.Limitations = []fleet.Limitation{{Code: "PACTOS_UNREADABLE", Message: "the Pacto objects could not be listed"}}
	e.push(t, e.key, snapA, s)
	snap := readSnap(t, &Service{}, snapOpts(trustFile(t, e.scoped("rec", snapA)), "oci://"+e.repo))
	id := "targets-" + snapA
	if st := stateOf(t, snap, id); st.Status != fleet.SourcePartial || !hasLim(snap, id, "PACTOS_UNREADABLE") {
		t.Fatalf("source %+v, limitations %+v", st, snap.Limitations)
	}
	s.Limitations = nil
	e.push(t, e.key, snapA, s)
	snap = readSnap(t, &Service{}, snapOpts(trustFile(t, e.scoped("rec", snapA)), "oci://"+e.repo))
	if st := stateOf(t, snap, id); st.Status != fleet.SourcePartial {
		t.Fatalf("a partial publish read %+v", st)
	}
}

// TestTargetSnapshotMissingRepositoryFails: a repository that cannot be
// listed, or a snapshot read that fails, is a failing source.
func TestTargetSnapshotMissingRepositoryFails(t *testing.T) {
	e := newSnapEnv(t)
	snap := readSnap(t, &Service{}, snapOpts("", "oci://"+strings.TrimSuffix(e.repo, "pacto-targets")+"nothing"))
	if st := stateOf(t, snap, "targets"); st.Status != fleet.SourceUnavailable || snap.Completeness == fleet.CompletenessComplete {
		t.Fatalf("source %+v", st)
	}
	e.push(t, e.key, snapA, ledgerSnapshot(snapA, snapNow))
	e.fail.Store("/manifests/")
	snap = readSnap(t, &Service{}, snapOpts("", "oci://"+e.repo))
	if st := stateOf(t, snap, "targets-"+snapA); st.Status != fleet.SourceUnavailable {
		t.Fatalf("source %+v", st)
	}
	snap = readSnap(t, &Service{}, snapOpts("", "oci://reg.example/acme/Pacto-Targets"))
	if st := stateOf(t, snap, "targets"); st.Status != fleet.SourceUnavailable {
		t.Fatalf("source %+v", st)
	}
	e.fail.Store("")
	svc := &Service{}
	readSnap(t, svc, snapOpts("", "oci://"+e.repo))
	e.fail.Store("/blobs/")
	snap = readSnap(t, &Service{}, snapOpts("", "oci://"+e.repo))
	if st := stateOf(t, snap, "targets-"+snapA); st.Status != fleet.SourceUnavailable {
		t.Fatalf("a failed layer read %+v", st)
	}
}

func TestParseSnapshotRefs(t *testing.T) {
	refs, err := parseSnapshotRefs([]string{"oci://reg:5000/acme/pacto-targets:cluster-a", "reg:5000/acme/pacto-targets@sha256:abc"})
	if err != nil || len(refs) != 2 || refs[0] != (snapshotRef{"reg:5000/acme/pacto-targets", "cluster-a"}) || refs[1].reference != "sha256:abc" {
		t.Fatalf("refs %+v, %v", refs, err)
	}
	if refs, err := parseSnapshotRefs([]string{"reg:5000/acme/pacto-targets"}); err != nil || refs[0].reference != "" {
		t.Fatalf("repository %+v, %v", refs, err)
	}
	for _, bad := range [][]string{{"reg/acme/pacto-targets:latest"}, {"reg"}, {"reg/a", "reg/b:cluster-x"}} {
		if _, err := parseSnapshotRefs(bad); err == nil {
			t.Errorf("%v parsed", bad)
		}
	}
	if _, err := (&Service{}).targetSnapshotSources(t.Context(), FleetOptions{TargetSnapshot: []string{"reg"}}); err == nil {
		t.Fatal("a bad ref resolved")
	}
}

func TestDeliveryTrust(t *testing.T) {
	t.Setenv(TrustEnv, "")
	if trust, err := DeliveryTrust(""); trust != nil || err != nil {
		t.Fatalf("no file = %v, %v", trust, err)
	}
	if _, err := DeliveryTrust(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing file read")
	}
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	_ = os.WriteFile(bad, []byte("keys: []\n"), 0o600)
	if _, err := DeliveryTrust(bad); err == nil || !strings.Contains(err.Error(), bad) {
		t.Fatalf("err = %v", err)
	}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv(TrustEnv, trustFile(t, delivery.TrustedKey{Name: "k", PublicKey: pemOf(t, key)}))
	if trust, err := DeliveryTrust(""); err != nil || trust.Keys[0].Name != "k" {
		t.Fatalf("env = %+v, %v", trust, err)
	}
}

// TestFleetReadsTargetSnapshot: Fleet adds the snapshot sources when
// TargetSnapshot is set and fails on a trust config that does not parse.
func TestFleetReadsTargetSnapshot(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, ledgerSnapshot(snapA, snapNow))
	svc := &Service{}
	snap, err := svc.Fleet(t.Context(), snapOpts(trustFile(t, e.scoped("rec", snapA)), "oci://"+e.repo))
	if err != nil || stateOf(t, snap, "targets-"+snapA).Signer != "rec" {
		t.Fatalf("fleet %v", err)
	}
	if _, err := svc.Fleet(t.Context(), snapOpts(filepath.Join(t.TempDir(), "missing"), "oci://"+e.repo)); err == nil {
		t.Fatal("a missing trust file built")
	}
	if got := snapOpts("", "oci://"+e.repo).ConsumerSources(); !slices.Equal(got, []string{"--target-snapshot oci://" + e.repo}) {
		t.Fatalf("consumer sources %q", got)
	}
}
