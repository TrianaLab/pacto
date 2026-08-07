package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/internal/fleetsrc"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/dashboard"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// This file is the REAL-provider Product Impact integration test (requirement
// 2.6, ledger phase-1 item 7). It exercises the COMPLETE vertical with NO
// staticImpact substitute:
//
//	OCISource -> fleet.Build -> Manager/current snapshot -> dashboard Product
//	Impact handler -> impactProviderForFleet -> Service.ImpactWithSnapshot ->
//	BundleStore.Pull
//
// It proves the canonical OCI identity invariant end to end: a revision that
// originated from an OCI source and has a resolved digest carries a canonical,
// immutable, resolver-compatible reference (oci://registry/repo@<digest>), so a
// canonical Product Impact that passes the exact-content guard can actually be
// fetched by the real provider — the exact bug the static-provider tests missed.

// e2eDigest is a syntactically valid lower-case sha256 content digest, built
// programmatically so no 64-char body literal appears in source.
func e2eDigest(fill string) string {
	return "sha256:" + strings.Repeat(fill, 64/len(fill)+1)[:64]
}

// e2eStore is a fake oci.BundleStore that records every Pull so the test can
// prove the real provider pulled a digest-pinned OCI ref (and prove it was NOT
// invoked when the revision is rejected before analysis). It normalizes the
// oci:// scheme away, matching a real registry client that accepts either form.
type e2eStore struct {
	mu      sync.Mutex
	bundles map[string]*contract.Bundle // normalized ref -> bundle
	digests map[string]string           // normalized ref -> digest
	pulls   []string                    // normalized refs Pulled (in order)
}

func normRef(ref string) string { return strings.TrimPrefix(ref, "oci://") }

func (s *e2eStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", nil
}
func (s *e2eStore) ListTags(context.Context, string) ([]string, error) { return nil, nil }

func (s *e2eStore) Pull(_ context.Context, ref string) (*contract.Bundle, error) {
	n := normRef(ref)
	s.mu.Lock()
	s.pulls = append(s.pulls, n)
	s.mu.Unlock()
	if b, ok := s.bundles[n]; ok {
		return b, nil
	}
	return nil, errors.New("bundle not found: " + ref)
}

func (s *e2eStore) Resolve(_ context.Context, ref string) (string, error) {
	if d, ok := s.digests[normRef(ref)]; ok {
		return d, nil
	}
	return "", errors.New("no digest for: " + ref)
}

// resetPulls clears the recorded Pulls so a later assertion sees only the Pulls
// made during the impact request (not those made while building the snapshot).
func (s *e2eStore) resetPulls() {
	s.mu.Lock()
	s.pulls = nil
	s.mu.Unlock()
}
func (s *e2eStore) pulledDigestRef() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.pulls {
		if strings.Contains(p, "@sha256:") {
			return p, true
		}
	}
	return "", false
}

func e2eBundle(name string) *contract.Bundle {
	return &contract.Bundle{
		Contract: &contract.Contract{Service: contract.Service{Name: name, Version: "1.0.0"}},
		FS:       fstest.MapFS{},
	}
}

// ociManager builds a Manager whose snapshot is produced by the REAL OCISource
// over refs (the same source the dashboard wires), then publishes it.
func ociManager(t *testing.T, store *e2eStore, refs []string) *fleet.Manager {
	t.Helper()
	build := func(ctx context.Context) (*fleet.FleetSnapshot, error) {
		return fleet.Build(ctx, fleet.BuildOptions{}, fleetsrc.NewOCISource("oci", store, refs))
	}
	mgr := fleet.NewManager(build, fleet.ManagerOptions{})
	if err := mgr.Refresh(context.Background()); err != nil {
		t.Fatalf("manager refresh: %v", err)
	}
	return mgr
}

// singleRevKey returns the sole revision key in a snapshot.
func singleRevKey(t *testing.T, snap *fleet.FleetSnapshot) string {
	t.Helper()
	for k := range snap.Revisions {
		return string(k)
	}
	t.Fatal("snapshot has no revisions")
	return ""
}

// serveImpact starts a real dashboard server exposing the product impact endpoint
// backed by the given manager (fleet query) and service (real provider).
func serveImpact(t *testing.T, mgr *fleet.Manager, svc *app.Service, fleetOverride func(context.Context) (*fleet.Query, error)) string {
	t.Helper()
	resolved := dashboard.BuildResolvedSource(map[string]dashboard.DataSource{})
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := dashboard.NewResolvedServer(resolved, ui, []dashboard.SourceInfo{}, nil)
	if fleetOverride != nil {
		srv.SetFleetProvider(fleetOverride)
	} else {
		srv.SetFleetProvider(managerFleetProvider(mgr))
	}
	srv.SetImpactProvider(impactProviderForFleet(svc, mgr))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)
	return "http://" + ln.Addr().String()
}

// impactPost sends an impact request and returns the HTTP status and decoded body.
func impactPost(t *testing.T, base string, body map[string]any) (int, *dashboard.ProductImpact) {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(base+"/api/fleet/impact", "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	var out dashboard.ProductImpact
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.StatusCode, &out
}

// TestDashboardImpact_RealProviderVertical drives the whole OCI Product Impact
// vertical through the real provider for every identity case.
func TestDashboardImpact_RealProviderVertical(t *testing.T) {
	dgst := e2eDigest("a")

	// The three canonical happy-path input spellings all resolve to the SAME
	// digest-pinned OCI ref and pull it through the real provider.
	happy := []struct {
		name     string
		inputRef string // the ref that reaches OCISource (as the dashboard wires it)
	}{
		{"dashboard-stripped oci input (scheme removed by parseDashboardArgs)", "reg.example/pay:1.0.0"},
		{"explicit oci tag input", "oci://reg.example/pay:1.0.0"},
		{"explicit oci digest input", "oci://reg.example/pay@" + dgst},
	}
	for _, tc := range happy {
		t.Run(tc.name, func(t *testing.T) {
			store := &e2eStore{
				bundles: map[string]*contract.Bundle{
					"reg.example/pay:1.0.0":   e2eBundle("pay"),
					"reg.example/pay@" + dgst: e2eBundle("pay"),
				},
				digests: map[string]string{
					"reg.example/pay:1.0.0":   dgst,
					"reg.example/pay@" + dgst: dgst,
				},
			}
			mgr := ociManager(t, store, []string{tc.inputRef})
			snap, _ := mgr.Current()
			// The OCI source must have produced a CANONICAL immutable ResolvedRef.
			revKey := singleRevKey(t, snap)
			rev := snap.Revisions[fleet.RevisionKey(revKey)]
			if want := "oci://reg.example/pay@" + dgst; rev.ResolvedRef != want {
				t.Fatalf("ResolvedRef = %q, want canonical %q", rev.ResolvedRef, want)
			}
			svc := &app.Service{BundleStore: store}
			base := serveImpact(t, mgr, svc, nil)
			store.resetPulls()

			status, out := impactPost(t, base, map[string]any{
				"snapshotId": snap.SnapshotID, "fromRevisionKey": revKey, "toRevisionKey": revKey,
			})
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200", status)
			}
			if !out.SnapshotMatch {
				t.Error("SnapshotMatch must be true for an exact digest revision")
			}
			// The real provider must have pulled the DIGEST-PINNED ref, never a tag.
			pulled, ok := store.pulledDigestRef()
			if !ok {
				t.Fatalf("provider did not pull a digest-pinned ref; pulls=%v", store.pulls)
			}
			if pulled != "reg.example/pay@"+dgst {
				t.Errorf("provider pulled %q, want reg.example/pay@%s", pulled, dgst)
			}
		})
	}

	// A mutable-only OCI revision (the store resolves no digest) keeps a tag
	// ResolvedRef and is rejected 422 BEFORE the provider is invoked.
	t.Run("mutable-only oci revision rejected before provider", func(t *testing.T) {
		store := &e2eStore{
			bundles: map[string]*contract.Bundle{"reg.example/pay:latest": e2eBundle("pay")},
			// No digest entry -> store.Resolve fails -> ResolvedRef stays the tag.
		}
		mgr := ociManager(t, store, []string{"reg.example/pay:latest"})
		snap, _ := mgr.Current()
		revKey := singleRevKey(t, snap)
		svc := &app.Service{BundleStore: store}
		base := serveImpact(t, mgr, svc, nil)
		store.resetPulls()
		status, _ := impactPost(t, base, map[string]any{
			"snapshotId": snap.SnapshotID, "fromRevisionKey": revKey, "toRevisionKey": revKey,
		})
		if status != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", status)
		}
		if _, ok := store.pulledDigestRef(); ok {
			t.Errorf("provider must NOT pull for a mutable-only revision; pulls=%v", store.pulls)
		}
	})

	// A local revision has no canonical OCI ref and is rejected 422.
	t.Run("local revision rejected before provider", func(t *testing.T) {
		store := &e2eStore{}
		mgr := memManager(t, fleet.RawRevision{
			Bundle: e2eBundle("pay"), RequestedRef: "file:///abs/pay",
		})
		svc := &app.Service{BundleStore: store}
		snap, _ := mgr.Current()
		revKey := singleRevKey(t, snap)
		base := serveImpact(t, mgr, svc, nil)
		store.resetPulls()
		status, _ := impactPost(t, base, map[string]any{
			"snapshotId": snap.SnapshotID, "fromRevisionKey": revKey, "toRevisionKey": revKey,
		})
		if status != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", status)
		}
		if len(store.pulls) != 0 {
			t.Errorf("provider must NOT pull for a local revision; pulls=%v", store.pulls)
		}
	})

	// A revision whose recorded digest contradicts its immutable ref is rejected 422.
	t.Run("inconsistent recorded digest rejected before provider", func(t *testing.T) {
		store := &e2eStore{}
		mgr := memManager(t, fleet.RawRevision{
			Bundle: e2eBundle("pay"), ResolvedRef: "oci://reg.example/pay@" + e2eDigest("a"), Digest: e2eDigest("b"),
		})
		svc := &app.Service{BundleStore: store}
		snap, _ := mgr.Current()
		revKey := singleRevKey(t, snap)
		base := serveImpact(t, mgr, svc, nil)
		store.resetPulls()
		status, _ := impactPost(t, base, map[string]any{
			"snapshotId": snap.SnapshotID, "fromRevisionKey": revKey, "toRevisionKey": revKey,
		})
		if status != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", status)
		}
		if len(store.pulls) != 0 {
			t.Errorf("provider must NOT pull for an inconsistent revision; pulls=%v", store.pulls)
		}
	})

	// A snapshot published between validation and analysis (the provider analyzes a
	// snapshot whose id differs from the one the handler validated) is a 409.
	t.Run("snapshot refresh race is 409", func(t *testing.T) {
		store := &e2eStore{
			bundles: map[string]*contract.Bundle{
				"reg.example/pay:1.0.0":   e2eBundle("pay"),
				"reg.example/pay@" + dgst: e2eBundle("pay"),
				"reg.example/two:1.0.0":   e2eBundle("two"),
				"reg.example/two@" + dgst: e2eBundle("two"),
			},
			digests: map[string]string{
				"reg.example/pay:1.0.0": dgst, "reg.example/pay@" + dgst: dgst,
				"reg.example/two:1.0.0": dgst, "reg.example/two@" + dgst: dgst,
			},
		}
		// Snapshot A: only pay. The handler validates against A.
		snapA, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleetsrc.NewOCISource("oci", store, []string{"reg.example/pay:1.0.0"}))
		if err != nil {
			t.Fatal(err)
		}
		// Manager B: pay + two -> a DIFFERENT snapshot id. The real provider analyzes B.
		mgrB := ociManager(t, store, []string{"reg.example/pay:1.0.0", "reg.example/two:1.0.0"})
		snapB, _ := mgrB.Current()
		if snapA.SnapshotID == snapB.SnapshotID {
			t.Fatal("A and B must differ for the race test")
		}
		revKey := ""
		for k, r := range snapA.Revisions {
			if r.Service == "pay" {
				revKey = string(k)
			}
		}
		svc := &app.Service{BundleStore: store}
		// The fleet query serves A; the impact provider (over mgrB) analyzes B.
		base := serveImpact(t, mgrB, svc, func(context.Context) (*fleet.Query, error) { return fleet.NewQuery(snapA), nil })
		status, _ := impactPost(t, base, map[string]any{
			"fromRevisionKey": revKey, "toRevisionKey": revKey,
		})
		if status != http.StatusConflict {
			t.Fatalf("status = %d, want 409", status)
		}
	})
}

// memManager builds and publishes a Manager over a single in-memory revision, for
// the non-OCI rejection cases (local / inconsistent).
func memManager(t *testing.T, rev fleet.RawRevision) *fleet.Manager {
	t.Helper()
	build := func(ctx context.Context) (*fleet.FleetSnapshot, error) {
		return fleet.Build(ctx, fleet.BuildOptions{},
			fleet.NewMemorySource("m", "local", &fleet.Collection{Revisions: []fleet.RawRevision{rev}}))
	}
	mgr := fleet.NewManager(build, fleet.ManagerOptions{})
	if err := mgr.Refresh(context.Background()); err != nil {
		t.Fatalf("manager refresh: %v", err)
	}
	return mgr
}
