package app

// The catalog fleet source is proved against a real in-process registry and
// real directories on disk, like the catalog adapter it wraps: its whole reason
// to exist is that a closure crosses from a local bundle into a registry, and a
// mocked resolver would be a test of the mock.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/trianalab/pacto/v3/pkg/catalog"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// catSrcFixture publishes payments to a registry and writes an orders bundle on
// disk that depends on it, returning the orders directory. The dependency
// crosses from disk into a registry on purpose: that is the edge a local scan
// or an --oci list cannot follow on its own.
func catSrcFixture(t *testing.T) (*Service, string) {
	t.Helper()
	host := mxPlainRegistry(t)
	svc, client := mxService(authn.DefaultKeychain)
	mxPush(t, client, host, "acme/payments", "1.0.0", mxBundle(t, "payments", "1.0.0"))
	dir := catBundleDir(t, filepath.Join(t.TempDir(), "orders"), "orders", "1.0.0",
		mxDep{name: "payments", ref: "oci://" + host + "/acme/payments:1.0.0", compat: "^1.0.0"})
	return svc, dir
}

func TestCatalogSourceIDAndKind(t *testing.T) {
	src := &catalogSource{id: "catalog"}
	if src.ID() != "catalog" || src.Kind() != "catalog" {
		t.Errorf("id/kind = %q/%q, want catalog/catalog", src.ID(), src.Kind())
	}
}

// catSrcCollect runs the source over dir and indexes what came back by service
// name, failing if the collection reported any gap: every assertion below is
// about a walk that had nothing to apologise for.
func catSrcCollect(t *testing.T, svc *Service, dir string) map[string]fleet.RawRevision {
	t.Helper()
	col, err := (&catalogSource{id: "catalog", svc: svc, roots: []string{dir}}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Limitations) != 0 {
		t.Fatalf("limitations = %+v, want none", col.Limitations)
	}
	byName := map[string]fleet.RawRevision{}
	for _, rev := range col.Revisions {
		if rev.Bundle == nil || rev.Bundle.Contract == nil {
			t.Fatal("a revision arrived without the bundle the catalog resolved it from")
		}
		byName[rev.Bundle.Contract.Service.Name] = rev
	}
	return byName
}

// TestCatalogSourceCollectsTheClosureNotJustTheRoots is the whole point: one
// root goes in, and the revision it declares comes out with it, resolved
// through the registry.
func TestCatalogSourceCollectsTheClosureNotJustTheRoots(t *testing.T) {
	svc, dir := catSrcFixture(t)
	byName := catSrcCollect(t, svc, dir)

	if len(byName) != 2 || byName["orders"].Bundle == nil || byName["payments"].Bundle == nil {
		t.Fatalf("collected %v, want orders and payments", keysOf(byName))
	}
	// The bundle is what the fleet reads interfaces, schemas and readiness out
	// of, so a projection that dropped it would leave the snapshot describing
	// services it cannot say anything about.
	if len(byName["payments"].Bundle.Contract.Interfaces) == 0 {
		t.Error("the recorded bundle lost the contract body")
	}
}

// TestCatalogSourceProjectsTheCatalogIdentity covers what each revision carries
// across the projection: the catalog's content identity, its own fetch time and
// the references it was reached by.
func TestCatalogSourceProjectsTheCatalogIdentity(t *testing.T) {
	svc, dir := catSrcFixture(t)
	byName := catSrcCollect(t, svc, dir)

	for name, rev := range byName {
		if rev.Digest == "" {
			t.Errorf("%s: empty digest; the catalog's content identity did not survive the projection", name)
		}
		if rev.FetchedAt == nil {
			t.Errorf("%s: nil FetchedAt", name)
		}
	}
	if byName["orders"].FetchedAt == byName["payments"].FetchedAt {
		t.Error("revisions share one FetchedAt pointer; a later write would move both")
	}
	if !strings.Contains(byName["payments"].ResolvedRef, "@sha256:") {
		t.Errorf("payments resolved ref = %q, want a digest pin", byName["payments"].ResolvedRef)
	}
	// A local revision has no immutable reference to report -- its content hash
	// is already the immutable identity -- so the field stays empty rather than
	// echoing the path back as if it were one.
	if byName["orders"].ResolvedRef != "" {
		t.Errorf("orders resolved ref = %q, want empty", byName["orders"].ResolvedRef)
	}
	if !strings.HasSuffix(byName["orders"].RequestedRef, "orders") {
		t.Errorf("orders requested ref = %q, want the root as asked for", byName["orders"].RequestedRef)
	}
}

func keysOf(m map[string]fleet.RawRevision) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestCatalogSourceReportsAGapRatherThanDroppingIt: a root that resolves to
// nothing leaves the closure short, and a short closure served as a whole one
// would say a service has no dependents when nobody looked.
func TestCatalogSourceReportsAGapRatherThanDroppingIt(t *testing.T) {
	svc, _ := mxService(authn.DefaultKeychain)
	missing := filepath.Join(t.TempDir(), "not-a-bundle")

	col, err := (&catalogSource{id: "catalog", svc: svc, roots: []string{missing}}).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 0 {
		t.Errorf("revisions = %d, want none", len(col.Revisions))
	}
	if len(col.Limitations) != 1 {
		t.Fatalf("limitations = %+v, want exactly one", col.Limitations)
	}
	lim := col.Limitations[0]
	if lim.Code != catalog.LimitationRootUnresolved || lim.Source != "catalog" {
		t.Errorf("limitation = %+v, want ROOT_UNRESOLVED from catalog", lim)
	}
	// The reference has to reach the message: "a root did not resolve" without
	// saying which one is not something a reader can act on.
	if !strings.Contains(lim.Message, missing) {
		t.Errorf("message %q does not name the root it is about", lim.Message)
	}
}

// TestCatalogSourceCancelledMidWalk covers the limitation that carries no
// reference: the walk stopped, and there is no one declaration to blame.
func TestCatalogSourceCancelledMidWalk(t *testing.T) {
	svc, dir := catSrcFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	col, err := (&catalogSource{id: "catalog", svc: svc, roots: []string{dir}}).Collect(ctx)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Limitations) != 1 || col.Limitations[0].Code != catalog.LimitationCancelled {
		t.Fatalf("limitations = %+v, want exactly one CANCELLED", col.Limitations)
	}
	if msg := col.Limitations[0].Message; strings.HasSuffix(msg, ")") {
		t.Errorf("message %q has an empty reference appended to it", msg)
	}
}

// TestCatalogSourceWithoutRootsFails: pkg/catalog refuses to answer a request
// with no roots at all, and an unavailable source is the honest way to carry
// that refusal into a snapshot.
func TestCatalogSourceWithoutRootsFails(t *testing.T) {
	svc, _ := mxService(authn.DefaultKeychain)
	if _, err := (&catalogSource{id: "catalog", svc: svc}).Collect(context.Background()); err == nil {
		t.Fatal("expected an error when the source has no roots")
	}
}

// TestFleetIncludesTheCatalogClosure proves the wiring end to end: no --local,
// one --root, and the snapshot holds the dependency the root declared.
func TestFleetIncludesTheCatalogClosure(t *testing.T) {
	svc, dir := catSrcFixture(t)

	snap, err := svc.Fleet(context.Background(), FleetOptions{CatalogRoots: []string{dir}})
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	names := map[string]bool{}
	for _, rec := range snap.Services {
		names[rec.Name] = true
	}
	if !names["orders"] || !names["payments"] {
		t.Errorf("snapshot holds %v, want orders and payments", names)
	}
	if len(snap.Sources) != 1 || snap.Sources[0].Kind != "catalog" {
		t.Fatalf("sources = %+v, want one catalog source", snap.Sources)
	}
	if snap.Sources[0].RevisionCount != 2 {
		t.Errorf("catalog source contributed %d revisions, want 2", snap.Sources[0].RevisionCount)
	}
}

// TestBundleRecorderKeepsWhatTheWalkResolved covers the recorder on its own,
// including the nil-recorder path [Service.CatalogResolver] takes.
func TestBundleRecorderKeepsWhatTheWalkResolved(t *testing.T) {
	rec := newBundleRecorder()
	id := catalog.ContentID{Scheme: catalog.SchemeLocal, Digest: "sha256:abc"}
	b := mxBundle(t, "payments", "1.0.0")
	rec.record(id, b)

	if rec.bundle(id) != b {
		t.Error("the recorder did not return the bundle it was handed")
	}
	if rec.bundle(catalog.ContentID{Scheme: catalog.SchemeOCI, Digest: "sha256:def"}) != nil {
		t.Error("an unrecorded identity should return nil, not a neighbour's bundle")
	}
	// A nil recorder is how the non-recording resolver shares one Resolve.
	var none *bundleRecorder
	none.record(id, b)
}

func TestFirstRefOfNothing(t *testing.T) {
	if got := firstRef(nil); got != "" {
		t.Errorf("firstRef(nil) = %q, want empty", got)
	}
}
