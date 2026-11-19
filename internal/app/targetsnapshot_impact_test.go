package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/opencontainers/go-digest"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

// host is the registry host of e.
func (e *snapEnv) host() string {
	return strings.TrimSuffix(e.repo, "/acme/"+delivery.TargetSnapshotRepository)
}

// ordersRef is the published orders revision at version in e's registry.
func (e *snapEnv) ordersRef(version string) string {
	return "oci://" + e.host() + "/acme/orders:" + version
}

// ordersService serves orders 1.0.0 (a service) and 2.0.0 (a job: a BREAKING
// change) at any oci:// ref ending in their version.
func ordersService(t *testing.T) *Service {
	t.Helper()
	dirs := map[string]string{}
	for v, w := range map[string]string{"1.0.0": "service", "2.0.0": "job"} {
		dirs[v] = t.TempDir()
		testutil.WriteOrders(t, dirs[v], v, w)
	}
	version := func(ref string) string {
		for v := range dirs {
			if strings.HasSuffix(ref, ":"+v) || strings.HasSuffix(ref, digest.FromString(v).String()) {
				return v
			}
		}
		return ""
	}
	return NewService(&testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, ref string) (string, error) {
			return digest.FromString(version(ref)).String(), nil
		},
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			if v := version(ref); v != "" {
				return readLocalBundle(dirs[v])
			}
			return nil, &oci.ArtifactNotFoundError{Ref: ref}
		},
	}, nil)
}

// webappSnapshot is cluster's snapshot at asOf running webapp, which depends
// on orders at host/acme with compat.
func webappSnapshot(cluster, host, compat string, asOf time.Time) delivery.TargetSnapshot {
	var p delivery.PactoView
	p.Metadata.Namespace, p.Metadata.Name = "shop", "webapp"
	p.Spec.Target = &delivery.PactoTargetView{ServiceName: "webapp"}
	p.Status.StatusFormat = delivery.StatusFormatGuarantees
	p.Status.ContractStatus, p.Status.LastReconciledAt = "Compliant", asOf.Format(time.RFC3339)
	p.Status.Contract = &delivery.ContractInfoView{ServiceName: "webapp", Version: "1.0.0",
		ResolvedRef: "oci://" + host + "/acme/webapp@sha256:" + strings.Repeat("b", 64)}
	p.Status.Dependencies = []delivery.DependencyView{{Name: "orders", Ref: "oci://" + host + "/acme/orders", Required: true, Compatibility: compat}}
	return delivery.TargetSnapshot{Cluster: cluster, AsOf: asOf.Format(time.RFC3339), Completeness: fleet.CompletenessComplete,
		Publisher: "pacto-system/pacto-operator-delivery-recorder", Pactos: []delivery.PactoView{p}}
}

// autoImpact runs impact of orders 1.0.0 -> newPath with the snapshot derived.
func autoImpact(t *testing.T, svc *Service, e *snapEnv, newPath, trust string) *impact.Result {
	t.Helper()
	res, err := svc.Impact(t.Context(), ImpactOptions{OldPath: e.ordersRef("1.0.0"), NewPath: newPath, TargetSnapshotAuto: true,
		Fleet: FleetOptions{TargetSnapshotTrustFile: trust, Now: func() time.Time { return snapNow }}})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestRegistryRoots(t *testing.T) {
	for ref, want := range map[string][]string{
		"oci://ghcr.io/acme/contracts/ledger:2.3.0": {"ghcr.io/acme", "ghcr.io"},
		"ghcr.io/acme/ledger@sha256:abc":            {"ghcr.io/acme", "ghcr.io"},
		"oci://ghcr.io/ledger:1.0.0":                {"ghcr.io"},
		"oci://localhost:5000/acme/ledger":          {"localhost:5000/acme", "localhost:5000"},
	} {
		if got := registryRoots(ref); !slices.Equal(got, want) {
			t.Errorf("%s: %q, want %q", ref, got, want)
		}
	}
}

func TestFindTargetSnapshotFirstWins(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, ledgerSnapshot(snapA, snapNow))
	pushAt(t, e.host()+"/"+delivery.TargetSnapshotRepository, e.key, snapB, ledgerSnapshot(snapB, snapNow))
	found, look, failed := (&Service{}).findTargetSnapshot(t.Context(), "./local", e.ordersRef("1.0.0"))
	if found != "oci://"+e.repo || failed != nil || look.Found != e.repo || look.Clusters != 1 ||
		!slices.Equal(look.Tried, []impact.RegistryRoot{{Repository: e.repo, Outcome: impact.RegistryFound}}) {
		t.Fatalf("found %q, lookup %+v, failed %v", found, look, failed)
	}
}

func TestFindTargetSnapshotFallsBackToHost(t *testing.T) {
	e := newSnapEnv(t)
	hostRepo := e.host() + "/" + delivery.TargetSnapshotRepository
	pushAt(t, hostRepo, e.key, snapB, ledgerSnapshot(snapB, snapNow))
	found, look, _ := (&Service{}).findTargetSnapshot(t.Context(), e.ordersRef("1.0.0"))
	if found != "oci://"+hostRepo || !slices.Equal(look.Tried, []impact.RegistryRoot{{Repository: e.repo, Outcome: impact.RegistryNotFound}, {Repository: hostRepo, Outcome: impact.RegistryFound}}) {
		t.Fatalf("found %q, lookup %+v", found, look)
	}
}

// TestFindTargetSnapshotNotFound: a 404 and an anonymous 401 both read as
// no repository there, and the walk tries every root.
func TestFindTargetSnapshotNotFound(t *testing.T) {
	e := newSnapEnv(t)
	for _, code := range []int64{0, http.StatusUnauthorized} {
		e.status.Store(code)
		found, look, failed := (&Service{}).findTargetSnapshot(t.Context(), e.ordersRef("1.0.0"))
		if found != "" || failed != nil || len(look.Tried) != 2 || look.Tried[0].Outcome != impact.RegistryNotFound || look.Tried[1].Outcome != impact.RegistryNotFound {
			t.Fatalf("%d: found %q, lookup %+v, failed %v", code, found, look, failed)
		}
	}
	if found, look, failed := (&Service{}).findTargetSnapshot(t.Context(), "./a", "./b"); found != "" || look != nil || failed != nil {
		t.Fatal("a local change looked for a snapshot")
	}
}

func TestFindTargetSnapshotDeniedIsNamed(t *testing.T) {
	e := newSnapEnv(t)
	t.Setenv("PACTO_REGISTRY_TOKEN", "ci-token")
	e.status.Store(http.StatusForbidden)
	_, look, failed := (&Service{}).findTargetSnapshot(t.Context(), e.ordersRef("1.0.0"))
	if failed != nil || look.Tried[0].Outcome != impact.RegistryDenied {
		t.Fatalf("lookup %+v, failed %v", look, failed)
	}
}

// TestFindTargetSnapshotNoClusterTag: a repository with tags but no
// cluster-<uid> tag is no snapshot there.
func TestFindTargetSnapshotNoClusterTag(t *testing.T) {
	e := newSnapEnv(t)
	repo, _ := ocireferrer.Open(e.repo, ocireferrer.Options{})
	desc, err := oras.PackManifest(t.Context(), repo, oras.PackManifestVersion1_1, "application/vnd.example", oras.PackManifestOptions{})
	if err == nil {
		err = repo.Tag(t.Context(), desc, "latest")
	}
	if err != nil {
		t.Fatal(err)
	}
	_, look, _ := (&Service{}).findTargetSnapshot(t.Context(), e.ordersRef("1.0.0"))
	if look.Tried[0].Outcome != impact.RegistryNoClusterTag {
		t.Fatalf("lookup %+v", look)
	}
}

func TestLookupOutcome(t *testing.T) {
	nameUnknown := &errcode.ErrorResponse{StatusCode: http.StatusBadRequest, Errors: errcode.Errors{{Code: errcode.ErrorCodeNameUnknown}}}
	for _, c := range []struct {
		err          error
		credentialed bool
		want         string
	}{
		{nameUnknown, false, impact.RegistryNotFound},
		{&errcode.ErrorResponse{StatusCode: http.StatusUnauthorized}, true, impact.RegistryDenied},
		{&errcode.ErrorResponse{StatusCode: http.StatusInternalServerError}, false, ""},
		{errors.New("dial tcp: refused"), false, ""},
	} {
		if got := lookupOutcome(c.err, 0, c.credentialed); got != c.want {
			t.Errorf("%v: %q, want %q", c.err, got, c.want)
		}
	}
	if credentialed(authn.DefaultKeychain, "BAD HOST/acme") {
		t.Error("a host that does not parse holds credentials")
	}
}

// TestFindTargetSnapshotServerErrorIsPartial: a 500 is no answer, so the
// walk stops with a failing source and a BREAKING change with a consumer
// never clears.
func TestFindTargetSnapshotServerErrorIsPartial(t *testing.T) {
	e := newSnapEnv(t)
	e.status.Store(http.StatusInternalServerError)
	found, look, failed := (&Service{}).findTargetSnapshot(t.Context(), e.ordersRef("1.0.0"))
	if found != "" || look != nil || failed == nil || failed.Kind() != fleet.SourceKindTargetSnapshot {
		t.Fatalf("found %q, lookup %+v, failed %v", found, look, failed)
	}
	root, _ := testutil.ImpactFleet(t, ">=1.0.0")
	res, err := ordersService(t).Impact(t.Context(), ImpactOptions{OldPath: e.ordersRef("1.0.0"), NewPath: e.ordersRef("2.0.0"), TargetSnapshotAuto: true,
		Fleet: FleetOptions{LocalRoots: []string{root}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Completeness == fleet.CompletenessComplete || res.Gate == impact.GateClear || res.Registry != nil || len(res.TargetSnapshots) != 1 {
		t.Fatalf("gate %s, completeness %s, registry %+v, snapshots %+v", res.Gate, res.Completeness, res.Registry, res.TargetSnapshots)
	}
	// A provider ref whose root is no repository name is no answer either.
	if _, _, failed := (&Service{}).findTargetSnapshot(t.Context(), "oci://reg.example/ACME/orders:1"); failed == nil {
		t.Fatal("a bad repository read as not found")
	}
}

func TestImpactAutoReadsSnapshot(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, webappSnapshot(snapA, e.host(), "^1.0.0", snapNow))
	res := autoImpact(t, ordersService(t), e, e.ordersRef("2.0.0"), trustFile(t, e.scoped("rec", snapA)))
	if res.Gate != impact.GateBlocked || res.Registry == nil || res.Registry.Found != e.repo || len(res.TargetSnapshots) != 1 ||
		res.TargetSnapshots[0].Signer != "rec" || res.TargetSnapshots[0].Publisher != "pacto-system/pacto-operator-delivery-recorder" {
		t.Fatalf("gate %s (%s), registry %+v, snapshots %+v, limitations %+v", res.Gate, res.GateReason, res.Registry, res.TargetSnapshots, res.Limitations)
	}
}

// TestImpactAutoSnapshotLocalNewRevision: the change is a local folder of a
// published service, so it takes the old ref's domain and the snapshot's
// consumer is found.
func TestImpactAutoSnapshotLocalNewRevision(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, webappSnapshot(snapA, e.host(), "^1.0.0", snapNow))
	newDir := t.TempDir()
	testutil.WriteOrders(t, newDir, "2.0.0", "job")
	res := autoImpact(t, ordersService(t), e, newDir, trustFile(t, e.scoped("rec", snapA)))
	if res.Gate != impact.GateBlocked || len(res.Consumers) != 1 || res.Consumers[0].Service != "webapp" {
		t.Fatalf("gate %s, consumers %+v", res.Gate, res.Consumers)
	}
}

// TestImpactLocalNewRevisionKeepsLocalConsumer: with no target snapshot read,
// a local new revision of a published old one stays in the default domain,
// where a --local consumer resolves it, with derivation on or off.
func TestImpactLocalNewRevisionKeepsLocalConsumer(t *testing.T) {
	e := newSnapEnv(t)
	root := t.TempDir()
	testutil.WriteOrders(t, filepath.Join(root, "orders"), "2.0.0", "job")
	testutil.WriteConsumer(t, filepath.Join(root, "webapp"), "^1.0.0")
	for _, auto := range []bool{false, true} {
		res, err := ordersService(t).Impact(t.Context(), ImpactOptions{OldPath: e.ordersRef("1.0.0"), NewPath: filepath.Join(root, "orders"),
			TargetSnapshotAuto: auto, Fleet: FleetOptions{LocalRoots: []string{root}}})
		if err != nil {
			t.Fatal(err)
		}
		if res.Gate == impact.GateClear || res.NewRevisionAdded || len(res.Consumers) != 1 || res.Consumers[0].Service != "webapp" {
			t.Fatalf("auto %v: gate %s, added %v, consumers %+v", auto, res.Gate, res.NewRevisionAdded, res.Consumers)
		}
	}
}

// TestImpactAutoNotFoundKeepsGate: with no pacto-targets anywhere the answer
// is the answer of the same sources without the lookup, plus where it looked.
func TestImpactAutoNotFoundKeepsGate(t *testing.T) {
	e := newSnapEnv(t)
	root, _ := testutil.ImpactFleet(t, ">=1.0.0")
	svc := ordersService(t)
	opts := ImpactOptions{OldPath: e.ordersRef("1.0.0"), NewPath: e.ordersRef("2.0.0"), Fleet: FleetOptions{LocalRoots: []string{root}}}
	base, err := svc.Impact(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.TargetSnapshotAuto = true
	auto, err := svc.Impact(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if auto.Gate != base.Gate || auto.Completeness != base.Completeness || len(auto.Consumers) != len(base.Consumers) ||
		len(auto.Limitations) != len(base.Limitations) || auto.Registry == nil || len(auto.Registry.Tried) != 2 || base.Registry != nil {
		t.Fatalf("base %s/%s %+v, auto %s/%s %+v registry %+v", base.Gate, base.Completeness, base.Limitations, auto.Gate, auto.Completeness, auto.Limitations, auto.Registry)
	}
}

func TestImpactAutoSkippedWithTargetInput(t *testing.T) {
	e := newSnapEnv(t)
	root, ev := testutil.ImpactFleet(t, "^1.0.0")
	res, err := ordersService(t).Impact(t.Context(), ImpactOptions{OldPath: e.ordersRef("1.0.0"), NewPath: e.ordersRef("2.0.0"), TargetSnapshotAuto: true,
		Fleet: FleetOptions{LocalRoots: []string{root}, TargetStateFiles: []string{ev}}})
	if err != nil || res.Registry != nil || e.reqs.Load() != 0 {
		t.Fatalf("registry %+v, requests %d, %v", res.Registry, e.reqs.Load(), err)
	}
}

// TestImpactAutoKeepsSnapshotOnAddMissingRebuild: no source holds orders, so
// the new revision is added in a rebuild that keeps the snapshot.
func TestImpactAutoKeepsSnapshotOnAddMissingRebuild(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, webappSnapshot(snapA, e.host(), "^1.0.0", snapNow))
	res := autoImpact(t, ordersService(t), e, e.ordersRef("2.0.0"), trustFile(t, e.scoped("rec", snapA)))
	if !res.NewRevisionAdded || len(res.TargetSnapshots) != 1 || len(res.Consumers) != 1 {
		t.Fatalf("added %v, snapshots %+v, consumers %+v", res.NewRevisionAdded, res.TargetSnapshots, res.Consumers)
	}
	// ImpactOnSnapshot derives and rebuilds only for a caller that may add.
	svc := ordersService(t)
	opts := ImpactOptions{OldPath: e.ordersRef("1.0.0"), NewPath: e.ordersRef("2.0.0"), TargetSnapshotAuto: true,
		Fleet: FleetOptions{TargetSnapshotTrustFile: trustFile(t, e.scoped("rec", snapA)), Now: func() time.Time { return snapNow }}}
	empty, _ := fleet.Build(t.Context(), fleet.BuildOptions{})
	added, err := svc.ImpactOnSnapshot(t.Context(), opts, empty, true)
	if err != nil || added.Gate != impact.GateBlocked || added.Registry.Found != e.repo {
		t.Fatalf("addMissing %+v, %v", added, err)
	}
	bound, err := svc.ImpactOnSnapshot(t.Context(), opts, empty, false)
	if err != nil || bound.Registry != nil || len(bound.TargetSnapshots) != 0 {
		t.Fatalf("bound %+v, %v", bound, err)
	}
	// A rebuild that fails is the call's error.
	opts.Fleet.ObservationSources = []ObservationSourceSpec{{ID: "targets-" + snapA, Path: filepath.Join(t.TempDir(), "absent.json")}}
	if _, err := svc.ImpactOnSnapshot(t.Context(), opts, empty, true); err == nil {
		t.Fatal("a rebuild with a duplicate source id succeeded")
	}
}

// TestSnapshotInferredNeverClearsGate: a snapshot workload named like the
// only consumer, which no Pacto targets, adds no consumer and leaves the gate
// as it was.
func TestSnapshotInferredNeverClearsGate(t *testing.T) {
	e := newSnapEnv(t)
	svc := ordersService(t)
	trust := trustFile(t, e.scoped("rec", snapA))
	run := func(workloads []fleet.RawInferredService) *impact.Result {
		s := webappSnapshot(snapA, e.host(), ">=1.0.0", snapNow)
		s.Workloads = workloads
		e.push(t, e.key, snapA, s)
		return autoImpact(t, svc, e, e.ordersRef("2.0.0"), trust)
	}
	without := run(nil)
	with := run([]fleet.RawInferredService{{Namespace: "edge", Kind: "Deployment", Name: "webapp"}})
	if with.Gate != without.Gate || with.Gate == impact.GateClear || len(with.Consumers) != 1 || with.Consumers[0].Provenance == "inferred" {
		t.Fatalf("without %s, with %s %+v", without.Gate, with.Gate, with.Consumers)
	}
}

// TestSnapshotWorkloadSourceHealthNeverGates: a workload source that is not
// available leaves a complete answer complete.
func TestSnapshotWorkloadSourceHealthNeverGates(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, webappSnapshot(snapA, e.host(), "^2.0.0", snapNow))
	fo := FleetOptions{TargetSnapshot: []string{"oci://" + e.repo}, TargetSnapshotTrustFile: trustFile(t, e.scoped("rec", snapA)), Now: func() time.Time { return snapNow }}
	fo.extraSources = []fleet.Source{fleet.NewMemorySource("more"+workloadsSuffix, fleet.SourceKindKubernetesWorkloads, &fleet.Collection{
		State: &fleet.SourceState{Status: fleet.SourcePartial}, Limitations: []fleet.Limitation{{Code: "WORKLOADS_UNREADABLE", Message: "no list"}}})}
	res, err := ordersService(t).Impact(t.Context(), ImpactOptions{OldPath: e.ordersRef("1.0.0"), NewPath: e.ordersRef("2.0.0"), Fleet: fo})
	if err != nil || res.Completeness != fleet.CompletenessComplete {
		t.Fatalf("completeness %s, limitations %+v, %v", res.Completeness, res.Limitations, err)
	}
}

func TestImpactEventReadsExplicitSnapshot(t *testing.T) {
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, webappSnapshot(snapA, e.host(), "^1.0.0", snapNow))
	res, err := (&Service{}).ImpactEvent(t.Context(), ImpactEventOptions{Event: impact.Event{Kind: impact.EventUnavailable, Service: "webapp"},
		Fleet: FleetOptions{TargetSnapshot: []string{"oci://" + e.repo}, Now: func() time.Time { return snapNow }}})
	if err != nil || len(res.TargetSnapshots) != 1 || res.TargetSnapshots[0].Status != fleet.SourcePartial {
		t.Fatalf("snapshots %+v, %v", res, err)
	}
}

// TestImpactCrossClusterForgeryNeverClears: cluster A's recorder key signs an
// empty, complete snapshot claiming cluster B, over B's own snapshot that
// runs a consumer the change breaks. Trusted for A's streams or as a bare
// PEM, the forgery never clears the change.
func TestImpactCrossClusterForgeryNeverClears(t *testing.T) {
	e := newSnapEnv(t)
	_, keyB, _ := ed25519.GenerateKey(rand.Reader)
	pushAt(t, e.repo, keyB, snapB, webappSnapshot(snapB, e.host(), "^1.0.0", snapNow))
	e.push(t, e.key, snapB, delivery.TargetSnapshot{Cluster: snapB, AsOf: snapNow.Format(time.RFC3339), Completeness: fleet.CompletenessComplete})
	bare := filepath.Join(t.TempDir(), "a.pub.pem")
	if err := os.WriteFile(bare, []byte(e.pub), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, trust := range []string{trustFile(t, e.scoped("recorder-a", snapA)), bare} {
		res := autoImpact(t, ordersService(t), e, e.ordersRef("2.0.0"), trust)
		if res.Gate == impact.GateClear || res.Completeness == fleet.CompletenessComplete {
			t.Fatalf("%s: gate %s, completeness %s", trust, res.Gate, res.Completeness)
		}
	}
}
