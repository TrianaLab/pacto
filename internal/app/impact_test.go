package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// writeImpactService writes a minimal valid bundle for `name` at `version` into
// dir, so old/new revisions can be resolved as local paths.
func writeImpactService(t *testing.T, dir, name, version string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "pactoVersion: \"2.0\"\nservice:\n  name: " + name + "\n  version: \"" + version + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestService_Impact(t *testing.T) {
	oldDir := t.TempDir()
	newDir := t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	writeImpactService(t, newDir, "orders", "2.0.0")

	// A fleet root carrying the changed service so it resolves in the graph.
	fleetRoot := t.TempDir()
	writeImpactService(t, filepath.Join(fleetRoot, "orders"), "orders", "2.0.0")

	svc := NewService(nil, nil)
	res, err := svc.Impact(context.Background(), ImpactOptions{
		OldPath: oldDir,
		NewPath: newDir,
		Fleet:   FleetOptions{LocalRoots: []string{fleetRoot}},
	})
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if res.Service != "orders" {
		t.Errorf("Service = %q, want orders", res.Service)
	}
	if res.SchemaVersion != impact.SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", res.SchemaVersion, impact.SchemaVersion)
	}
	if res.OldVersion != "1.0.0" || res.NewVersion != "2.0.0" {
		t.Errorf("versions = %s -> %s, want 1.0.0 -> 2.0.0", res.OldVersion, res.NewVersion)
	}
}

// TestService_ImpactWithSnapshot_BindsGivenSnapshot proves section 2.2: an impact answer
// produced against an already-published snapshot carries THAT snapshot's id, so a
// dashboard can guarantee the impact result matches the graph the user is viewing
// rather than a freshly rebuilt, divergent one.
func TestService_ImpactWithSnapshot_BindsGivenSnapshot(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	writeImpactService(t, newDir, "orders", "2.0.0")
	fleetRoot := t.TempDir()
	writeImpactService(t, filepath.Join(fleetRoot, "orders"), "orders", "2.0.0")

	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{LocalRoots: []string{fleetRoot}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ImpactWithSnapshot(context.Background(), ImpactOptions{OldPath: oldDir, NewPath: newDir}, snap)
	if err != nil {
		t.Fatalf("ImpactWithSnapshot: %v", err)
	}
	if snap.SnapshotID == "" || res.SnapshotID != snap.SnapshotID {
		t.Errorf("impact snapshotId %q must equal the given snapshot %q", res.SnapshotID, snap.SnapshotID)
	}
}

func TestService_Impact_OldPathError(t *testing.T) {
	newDir := t.TempDir()
	writeImpactService(t, newDir, "orders", "2.0.0")
	svc := NewService(nil, nil)
	if _, err := svc.Impact(context.Background(), ImpactOptions{OldPath: "/nonexistent/old", NewPath: newDir}); err == nil {
		t.Error("expected error for a nonexistent old path")
	}
}

// A bad ref fails before the fleet is built, so a fleet error never hides it.
func TestService_Impact_ResolvesRefsBeforeFleet(t *testing.T) {
	newDir := t.TempDir()
	writeImpactService(t, newDir, "orders", "2.0.0")
	dup := []ObservationSourceSpec{{ID: "x"}, {ID: "x"}}
	_, err := NewService(nil, nil).Impact(context.Background(), ImpactOptions{
		OldPath: "/nonexistent/old", NewPath: newDir, Fleet: FleetOptions{ObservationSources: dup},
	})
	if err == nil || !strings.HasPrefix(err.Error(), "old revision") {
		t.Errorf("err = %v, want the old revision error", err)
	}
}

func TestService_Impact_NewPathError(t *testing.T) {
	oldDir := t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	svc := NewService(nil, nil)
	if _, err := svc.Impact(context.Background(), ImpactOptions{OldPath: oldDir, NewPath: "/nonexistent/new"}); err == nil {
		t.Error("expected error for a nonexistent new path")
	}
}

func TestService_Impact_FleetError(t *testing.T) {
	oldDir := t.TempDir()
	newDir := t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	writeImpactService(t, newDir, "orders", "2.0.0")

	// A cancelled context is fatal in fleet.Build, so snapshot assembly fails.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc := NewService(nil, nil)
	if _, err := svc.Impact(ctx, ImpactOptions{
		OldPath: oldDir,
		NewPath: newDir,
		Fleet:   FleetOptions{LocalRoots: []string{oldDir}},
	}); err == nil {
		t.Error("expected error when the fleet build context is cancelled")
	}
}

func TestService_Impact_WithTraces(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	writeImpactService(t, newDir, "orders", "2.0.0")
	fleetRoot := t.TempDir()
	writeImpactService(t, filepath.Join(fleetRoot, "orders"), "orders", "2.0.0")
	// checkout is a REGISTERED fleet service that does not declare orders — so the
	// observed edge makes it a domain-qualified observed-only (shadow) consumer. An
	// unregistered caller name would instead be preserved as an unresolved
	// limitation, never a phantom default-domain consumer.
	writeImpactService(t, filepath.Join(fleetRoot, "checkout"), "checkout", "1.0.0")

	// A trace where checkout calls orders → an observed-only (shadow) consumer.
	trace := `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},
	  "scopeSpans":[{"spans":[{"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"orders"}}]}]}]}]}`

	svc := NewService(nil, nil)
	res, err := svc.Impact(context.Background(), ImpactOptions{
		OldPath: oldDir, NewPath: newDir,
		Fleet:           FleetOptions{LocalRoots: []string{fleetRoot}},
		IncludeObserved: true,
		Traces:          []byte(trace),
	})
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	found := false
	for _, c := range res.Consumers {
		if c.Service == "checkout" && c.Confidence == impact.ConfidenceObserved {
			found = true
		}
	}
	if !found {
		t.Errorf("expected observed-only consumer checkout, got %+v", res.Consumers)
	}
}

func TestService_Impact_BadTraces(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	writeImpactService(t, newDir, "orders", "2.0.0")
	fleetRoot := t.TempDir()
	writeImpactService(t, filepath.Join(fleetRoot, "orders"), "orders", "2.0.0")

	svc := NewService(nil, nil)
	_, err := svc.Impact(context.Background(), ImpactOptions{
		OldPath: oldDir, NewPath: newDir,
		Fleet:  FleetOptions{LocalRoots: []string{fleetRoot}},
		Traces: []byte("{bad"),
	})
	if err == nil {
		t.Fatal("expected trace parse error")
	}
}

func TestRevisionDomain(t *testing.T) {
	tests := []struct {
		name, ref, want string
	}{
		// The bug: a bare multi-segment path resolves as a local bundle but
		// OciDomain alone reads "payments-service" as the artifact under domain
		// "examples/demo", so the changed service never joins the fleet.
		{"bare multi-segment path is local", "examples/demo/payments-service", ""},
		{"file URL is local", "file:///abs/path/payments-service", ""},
		{"dot-relative path is local", "./payments-service/v2.0.0", ""},
		{"absolute path is local", "/abs/payments-service", ""},
		{"oci ref keeps its domain", "oci://ghcr.io/acme/payments:1.0", "ghcr.io/acme"},
		{"oci ref with digest keeps its domain", "oci://localhost:5000/acme/payments@sha256:abc", "localhost:5000/acme"},
		{"single-segment oci ref has the default domain", "oci://payments:1.0", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := revisionDomain(tt.ref); got != tt.want {
				t.Errorf("revisionDomain(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

// TestService_ImpactWithSnapshot_RefusesHiddenService: on a snapshot narrowed
// to the caller, a raw-ref impact on a service the snapshot hides, on either
// side, reads as not found instead of diffing it with the server's
// credentials. A visible service still answers.
func TestService_ImpactWithSnapshot_RefusesHiddenService(t *testing.T) {
	root := t.TempDir()
	write := func(name, version string) string {
		dir := filepath.Join(root, name+"-"+version)
		writeImpactService(t, dir, name, version)
		return dir
	}
	ordersOld, ordersNew := write("orders", "1.0.0"), write("orders", "2.0.0")
	ledgerOld, ledgerNew := write("ledger", "2.0.0"), write("ledger", "2.1.0")
	fleetRoot := t.TempDir()
	writeImpactService(t, filepath.Join(fleetRoot, "orders"), "orders", "2.0.0")

	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{LocalRoots: []string{fleetRoot}})
	if err != nil {
		t.Fatal(err)
	}
	snap.Limitations = append(snap.Limitations, fleet.Limitation{Code: fleet.LimitationHiddenByPermissions})

	for name, o := range map[string]ImpactOptions{
		"hidden new": {OldPath: ledgerOld, NewPath: ledgerNew},
		"hidden old": {OldPath: ledgerOld, NewPath: ordersNew},
	} {
		var nf *oci.ArtifactNotFoundError
		if _, err := svc.ImpactWithSnapshot(context.Background(), o, snap); !errors.As(err, &nf) {
			t.Errorf("%s: err = %v, want not found", name, err)
		}
	}
	if _, err := svc.ImpactWithSnapshot(context.Background(), ImpactOptions{OldPath: ordersOld, NewPath: ordersNew}, snap); err != nil {
		t.Errorf("visible service: %v", err)
	}
}

// writeImpactConsumer writes a bundle `name` declaring a dependency on
// provider with the given range.
func writeImpactConsumer(t *testing.T, dir, name, provider, compat string) {
	t.Helper()
	writeImpactService(t, dir, name, "1.0.0")
	f, err := os.OpenFile(filepath.Join(dir, "pacto.yaml"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString("dependencies:\n  - name: " + provider + "\n    compatibility: \"" + compat + "\"\n"); err != nil {
		t.Fatal(err)
	}
}

// TestService_Impact_AddsNewRevisionWhenNoSourceHoldsIt proves the evaluation
// trap is closed: a fleet root holding only the consumers used to leave the
// changed service out of the graph and answer with no consumers at all.
func TestService_Impact_AddsNewRevisionWhenNoSourceHoldsIt(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	writeImpactService(t, newDir, "orders", "2.0.0")
	fleetRoot := t.TempDir()
	writeImpactConsumer(t, filepath.Join(fleetRoot, "webapp"), "webapp", "orders", "^1.0.0")

	res, err := NewService(nil, nil).Impact(context.Background(), ImpactOptions{
		OldPath: oldDir, NewPath: newDir, Fleet: FleetOptions{LocalRoots: []string{fleetRoot}},
	})
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if !res.NewRevisionAdded {
		t.Error("NewRevisionAdded = false, want true")
	}
	if len(res.Consumers) != 1 || res.Consumers[0].Service != "webapp" || res.Consumers[0].CompatibilityVerdict != impact.CompatibilityIncompatible {
		t.Fatalf("consumers = %+v, want webapp incompatible", res.Consumers)
	}
	for _, l := range res.Limitations {
		if l.Code == "SERVICE_NOT_IN_FLEET" {
			t.Errorf("the added revision still reads as missing: %+v", l)
		}
	}
}

// TestService_Impact_SecondBuildError covers the rebuild failing after the first
// build succeeded: a configured source already claims the added source's id.
func TestService_Impact_SecondBuildError(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	writeImpactService(t, newDir, "orders", "2.0.0")
	_, err := NewService(nil, nil).Impact(context.Background(), ImpactOptions{
		OldPath: oldDir, NewPath: newDir,
		Fleet: FleetOptions{ObservationSources: []ObservationSourceSpec{{ID: "new-revision", Path: filepath.Join(t.TempDir(), "absent.json")}}},
	})
	if err == nil {
		t.Fatal("expected the rebuild to refuse a duplicate source id")
	}
}

func TestService_ImpactWithSnapshot_ResolveError(t *testing.T) {
	newDir := t.TempDir()
	writeImpactService(t, newDir, "orders", "2.0.0")
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(nil, nil).ImpactWithSnapshot(context.Background(), ImpactOptions{OldPath: "/nonexistent/old", NewPath: newDir}, snap); err == nil {
		t.Error("expected error for a nonexistent old path")
	}
}

// TestService_ImpactOnSnapshot: on a snapshot without the changed service,
// addMissing adds the new revision; without it the answer stays bound to the
// given snapshot and says SERVICE_NOT_IN_FLEET.
func TestService_ImpactOnSnapshot(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	writeImpactService(t, newDir, "orders", "2.0.0")
	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	opts := ImpactOptions{OldPath: oldDir, NewPath: newDir}
	added, err := svc.ImpactOnSnapshot(context.Background(), opts, snap, true)
	if err != nil || !added.NewRevisionAdded {
		t.Fatalf("addMissing: added=%v err=%v", added != nil && added.NewRevisionAdded, err)
	}
	bound, err := svc.ImpactOnSnapshot(context.Background(), opts, snap, false)
	if err != nil {
		t.Fatal(err)
	}
	if bound.NewRevisionAdded || bound.SnapshotID != snap.SnapshotID {
		t.Errorf("without addMissing: added=%v snapshot %q, want %q", bound.NewRevisionAdded, bound.SnapshotID, snap.SnapshotID)
	}
	missing := false
	for _, l := range bound.Limitations {
		missing = missing || l.Code == "SERVICE_NOT_IN_FLEET"
	}
	if !missing {
		t.Errorf("limitations %+v lack SERVICE_NOT_IN_FLEET", bound.Limitations)
	}
}

// A trace edge keeps the end time of its latest span, so impact can tell a
// call seen today from one seen last week; a span with no time gives none.
func TestObservedEdgesFromTracesKeepLastSeen(t *testing.T) {
	span := func(callee, end string) string {
		return `{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},"scopeSpans":[{"spans":[{"kind":3,` +
			`"startTimeUnixNano":"` + end + `","endTimeUnixNano":"` + end + `","attributes":[{"key":"peer.service","value":{"stringValue":"` + callee + `"}}]}]}]}`
	}
	edges, err := observedEdgesFromTraces([]byte(`{"resourceSpans":[` + span("orders", "5000") + `,` + span("stock", "") + `]}`))
	if err != nil || len(edges) != 2 {
		t.Fatalf("edges = %+v, err %v", edges, err)
	}
	for _, e := range edges {
		switch e.Provider {
		case "orders":
			if e.LastSeen == nil || !e.LastSeen.Equal(time.Unix(0, 5000)) {
				t.Errorf("orders lastSeen = %v", e.LastSeen)
			}
		default:
			if e.LastSeen != nil {
				t.Errorf("%s lastSeen = %v, want none", e.Provider, e.LastSeen)
			}
		}
	}
}

// eventFleet is a fleet root with orders and webapp, which requires it.
func eventFleet(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeImpactService(t, filepath.Join(root, "orders"), "orders", "1.0.0")
	dir := filepath.Join(root, "webapp")
	writeImpactService(t, dir, "webapp", "1.0.0")
	body := "pactoVersion: \"2.0\"\nservice:\n  name: webapp\n  version: \"1.0.0\"\ndependencies:\n  - name: orders\n    ref: oci://x/orders\n    required: true\n    compatibility: \"^1.0.0\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestImpactEventOnSnapshot(t *testing.T) {
	svc := NewService(nil, nil)
	ctx := context.Background()
	opts := ImpactEventOptions{Event: impact.Event{Kind: impact.EventUnavailable, Service: "orders"}, Fleet: FleetOptions{LocalRoots: []string{eventFleet(t)}}}
	res, err := svc.ImpactEvent(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Event == nil || res.Event.Kind != impact.EventUnavailable || len(res.Consumers) != 1 ||
		res.Consumers[0].Service != "webapp" || res.Consumers[0].Effect != impact.EffectBreaks || res.Gate != impact.GateUnverified {
		t.Fatalf("unavailable orders = %+v", res)
	}
	snap, _ := svc.Fleet(ctx, opts.Fleet)
	opts.Event = impact.Event{Kind: impact.EventRegressed, Service: "orders", Revision: "sha256:a"}
	opts.Regression = &impact.Regression{Unavailable: "no cluster"}
	res, err = svc.ImpactEventOnSnapshot(ctx, opts, snap)
	if err != nil || res.SnapshotID != snap.SnapshotID || !strings.Contains(res.GateReason, "not read in full") {
		t.Fatalf("regressed on the snapshot = %+v %v", res, err)
	}
	opts.Traces = []byte("{not json")
	if _, err := svc.ImpactEventOnSnapshot(ctx, opts, snap); err == nil || !strings.Contains(err.Error(), "parse traces") {
		t.Fatalf("bad traces: %v", err)
	}
	opts.Fleet = FleetOptions{LocalRoots: []string{"/nonexistent/root"}}
	if _, err := svc.ImpactEvent(ctx, opts); err == nil {
		t.Fatal("a fleet that cannot build is an error")
	}
}

func TestParseEventFlag(t *testing.T) {
	for _, tc := range []struct {
		kind, value string
		want        impact.Event
		err         string
	}{
		{impact.EventUnavailable, "redis", impact.Event{Kind: "unavailable", Service: "redis"}, ""},
		{impact.EventRemoved, "shop/postgres", impact.Event{Kind: "removed", Service: "shop/postgres"}, ""},
		{impact.EventRegressed, "payments@sha256:ab", impact.Event{Kind: "regressed", Service: "payments", Revision: "sha256:ab"}, ""},
		{impact.EventRegressed, "a@b@1.0.0", impact.Event{Kind: "regressed", Service: "a@b", Revision: "1.0.0"}, ""},
		{impact.EventRegressed, "payments", impact.Event{}, "<service>@<revision>"},
		{impact.EventRegressed, "@1.0.0", impact.Event{}, "<service>@<revision>"},
		{impact.EventRegressed, "payments@", impact.Event{}, "<service>@<revision>"},
		{impact.EventUnavailable, "redis@1.0.0", impact.Event{}, "takes a service key"},
		{impact.EventRemoved, "", impact.Event{}, "takes a service key"},
		{"exploded", "redis", impact.Event{}, "unknown event"},
	} {
		got, err := ParseEventFlag(tc.kind, tc.value)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s %q: err %v, want %q", tc.kind, tc.value, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s %q = %+v %v, want %+v", tc.kind, tc.value, got, err, tc.want)
		}
	}
}

// A bare name resolves to the one service of that name in any domain; a name
// two domains share is refused with their keys.
func TestImpactEventResolvesABareName(t *testing.T) {
	raw := func(domain, name string) fleet.RawRevision {
		return fleet.RawRevision{Domain: domain, Bundle: &contract.Bundle{FS: fstest.MapFS{},
			Contract: &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: "1.0.0"}}}}
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("m", "memory", &fleet.Collection{
		Revisions: []fleet.RawRevision{raw("ghcr.io/shop", "orders"), raw("ghcr.io/shop", "cart"), raw("ghcr.io/pay", "cart")}}))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(nil, nil)
	res, err := svc.ImpactEventOnSnapshot(context.Background(), ImpactEventOptions{Event: impact.Event{Kind: impact.EventRemoved, Service: "orders"}}, snap)
	if err != nil || res.Event.Service != string(fleet.NewServiceKeyDomain("ghcr.io/shop", "orders")) || slices.ContainsFunc(res.Limitations, func(l fleet.Limitation) bool { return l.Code == "SERVICE_NOT_IN_FLEET" }) {
		t.Fatalf("bare name = %+v %v", res, err)
	}
	if _, err := svc.ImpactEventOnSnapshot(context.Background(), ImpactEventOptions{Event: impact.Event{Kind: impact.EventRemoved, Service: "cart"}}, snap); err == nil ||
		!strings.Contains(err.Error(), "is ambiguous") {
		t.Fatalf("ambiguous name: %v", err)
	}
}
