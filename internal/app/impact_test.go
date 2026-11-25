package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	newDir := t.TempDir()
	writeImpactService(t, newDir, "orders", "2.0.0")
	svc := NewService(nil, nil)
	if _, err := svc.Impact(context.Background(), ImpactOptions{OldPath: "/nonexistent/old", NewPath: newDir}); err == nil {
		t.Error("expected error for a nonexistent old path")
	}
}

// A bad ref fails before the fleet is built, so a fleet error never hides it.
func TestService_Impact_ResolvesRefsBeforeFleet(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	oldDir := t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	svc := NewService(nil, nil)
	if _, err := svc.Impact(context.Background(), ImpactOptions{OldPath: oldDir, NewPath: "/nonexistent/new"}); err == nil {
		t.Error("expected error for a nonexistent new path")
	}
}

func TestService_Impact_FleetError(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	opts.ReadRegression = func(context.Context, impact.Event) *impact.Regression {
		return &impact.Regression{Unavailable: "no cluster"}
	}
	res, err = svc.ImpactEventOnSnapshot(ctx, opts, snap)
	if err != nil || res.SnapshotID != snap.SnapshotID || !strings.Contains(res.GateReason, "not read in full") {
		t.Fatalf("regressed on the snapshot = %+v %v", res, err)
	}
	opts.Traces = []byte("{not json")
	if _, err := svc.ImpactEventOnSnapshot(ctx, opts, snap); err == nil || !strings.Contains(err.Error(), "parse traces") {
		t.Fatalf("bad traces: %v", err)
	}
	opts.Fleet = FleetOptions{MiraURLs: []string{"https://user:pw@mira.example"}}
	if _, err := svc.ImpactEvent(ctx, opts); err == nil || !strings.Contains(err.Error(), "build fleet snapshot") {
		t.Fatalf("a fleet that cannot build is an error: %v", err)
	}
}

func TestParseEventFlag(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	res, err := svc.ImpactEventOnSnapshot(context.Background(), ImpactEventOptions{Event: impact.Event{Kind: impact.EventRemoved, Service: "orders"},
		ReadRegression: func(context.Context, impact.Event) *impact.Regression {
			t.Fatal("only a regressed event reads delivery")
			return nil
		}}, snap)
	if err != nil || res.Event.Service != string(fleet.NewServiceKeyDomain("ghcr.io/shop", "orders")) || slices.ContainsFunc(res.Limitations, func(l fleet.Limitation) bool { return l.Code == "SERVICE_NOT_IN_FLEET" }) {
		t.Fatalf("bare name = %+v %v", res, err)
	}
	if _, err := svc.ImpactEventOnSnapshot(context.Background(), ImpactEventOptions{Event: impact.Event{Kind: impact.EventRemoved, Service: "cart"}}, snap); err == nil ||
		!strings.Contains(err.Error(), "is ambiguous") {
		t.Fatalf("ambiguous name: %v", err)
	}
	// Delivery is read with the resolved key: a bare name read as domain ""
	// matches no Stage of a domain and would report "no failure recorded".
	var read impact.Event
	if _, err := svc.ImpactEventOnSnapshot(context.Background(), ImpactEventOptions{
		Event: impact.Event{Kind: impact.EventRegressed, Service: "orders", Revision: "sha256:a"},
		ReadRegression: func(_ context.Context, ev impact.Event) *impact.Regression {
			read = ev
			return &impact.Regression{}
		}}, snap); err != nil || read.Service != string(fleet.NewServiceKeyDomain("ghcr.io/shop", "orders")) {
		t.Fatalf("regression read with %+v %v", read, err)
	}
}

func TestImpactOnResolvedSeesBothBundles(t *testing.T) {
	t.Parallel()
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	writeImpactService(t, newDir, "orders", "2.0.0")
	var versions []string
	opts := ImpactOptions{OldPath: oldDir, NewPath: newDir, Fleet: FleetOptions{LocalRoots: []string{t.TempDir()}},
		OnResolved: func(o, n *contract.Bundle) {
			versions = append(versions, o.Contract.Service.Version, n.Contract.Service.Version)
		}}
	if _, err := NewService(nil, nil).Impact(context.Background(), opts); err != nil || !slices.Equal(versions, []string{"1.0.0", "2.0.0"}) {
		t.Fatalf("err %v, versions %v", err, versions)
	}
	// The snapshot paths never call it.
	snap, err := NewService(nil, nil).Fleet(context.Background(), opts.Fleet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(nil, nil).ImpactWithSnapshot(context.Background(), opts, snap); err != nil || len(versions) != 2 {
		t.Fatalf("err %v, versions %v", err, versions)
	}
}

// TestParseEventFlagOffering: offering:<name> names an offering for
// unavailable and removed; an empty or versioned name and a regressed
// offering are refused.
func TestParseEventFlagOffering(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		kind, value string
		want        impact.Event
		err         string
	}{
		{impact.EventUnavailable, "offering:postgresql", impact.Event{Kind: "unavailable", Offering: "postgresql"}, ""},
		{impact.EventRemoved, "offering:redis", impact.Event{Kind: "removed", Offering: "redis"}, ""},
		{impact.EventUnavailable, "offering:", impact.Event{}, "takes an offering name"},
		{impact.EventRemoved, "offering:pg@1", impact.Event{}, "takes an offering name"},
		{impact.EventRegressed, "offering:postgresql@1.0.0", impact.Event{}, "--regressed names one service: a regression belongs to one revision"},
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

// TestImpactEventOfferingSkipsServiceLookup: an offering event names no
// service, so nothing is resolved and impact answers about the offering.
func TestImpactEventOfferingSkipsServiceLookup(t *testing.T) {
	t.Parallel()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("m", "memory", &fleet.Collection{}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := NewService(nil, nil).ImpactEventOnSnapshot(context.Background(), ImpactEventOptions{Event: impact.Event{Kind: impact.EventUnavailable, Offering: "ghost"}}, snap)
	if err != nil || res.Event.Offering != "ghost" || res.Event.Service != "" {
		t.Fatalf("offering event = %+v %v", res, err)
	}
}

// TestService_ImpactOnSnapshot_Domain: a local edit of a service a registry
// source keys under its domain finds that service's consumers when the caller
// names the domain, and adds no default-domain copy with none.
func TestService_ImpactOnSnapshot_Domain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	oldDir, newDir, webDir := t.TempDir(), t.TempDir(), t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	writeImpactService(t, newDir, "orders", "2.0.0")
	writeImpactConsumer(t, webDir, "webapp", "orders", "^1.0.0")
	svc := NewService(nil, nil)
	var revs []fleet.RawRevision
	for _, dir := range []string{oldDir, webDir} {
		b, err := svc.resolveBundleWithOverrides(ctx, dir, ImpactOptions{}.OldOverrides)
		if err != nil {
			t.Fatal(err)
		}
		revs = append(revs, fleet.RawRevision{Bundle: b, Domain: "ghcr.io/acme", Digest: "sha256:" + b.Contract.Service.Name})
	}
	snap, err := fleet.Build(ctx, fleet.BuildOptions{}, fleet.NewMemorySource("oci", "oci", &fleet.Collection{Revisions: revs}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ImpactOnSnapshot(ctx, ImpactOptions{OldPath: oldDir, NewPath: newDir, Domain: "ghcr.io/acme"}, snap, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.NewRevisionAdded || len(res.Consumers) != 1 || res.Consumers[0].Service != "webapp" {
		t.Fatalf("added %v, consumers %+v", res.NewRevisionAdded, res.Consumers)
	}
	// Without the domain the local path keys a default-domain copy no one depends on.
	if copied, err := svc.ImpactOnSnapshot(ctx, ImpactOptions{OldPath: oldDir, NewPath: newDir}, snap, true); err != nil || !copied.NewRevisionAdded || len(copied.Consumers) != 0 {
		t.Fatalf("err %v, without a domain %+v", err, copied)
	}
}

// writeNamelessRevision writes a 2.5 contract that declares no service.name in
// <parent>/payments-api, so the name is inferred from the directory. With dep
// it depends on ledger, so dropping it is a BREAKING change.
func writeNamelessRevision(t *testing.T, parent, version string, dep bool) string {
	t.Helper()
	dir := filepath.Join(parent, "payments-api")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "pactoVersion: \"2.5\"\nservice:\n  version: \"" + version + "\"\n"
	if dep {
		body += "dependencies:\n  - name: ledger\n    ref: oci://ghcr.io/acme/ledger\n    compatibility: \"^1.0.0\"\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// An inferred name may miss every consumer, which declares the name it depends
// on: a BREAKING change over it never clears, even over a complete fleet in
// which nothing depends on the inferred name.
func TestImpactInferredNameNeverClears(t *testing.T) {
	t.Parallel()
	oldDir := writeNamelessRevision(t, t.TempDir(), "1.0.0", true)
	newDir := writeNamelessRevision(t, t.TempDir(), "2.0.0", false)
	fleetRoot := t.TempDir()
	writeImpactConsumer(t, filepath.Join(fleetRoot, "webapp"), "webapp", "payments", "^1.0.0")

	res, err := NewService(nil, nil).Impact(context.Background(), ImpactOptions{
		OldPath: oldDir, NewPath: newDir, Fleet: FleetOptions{LocalRoots: []string{fleetRoot}},
	})
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if res.Service != "payments-api" || res.Classification != "BREAKING" || res.Completeness != fleet.CompletenessComplete {
		t.Fatalf("service %q classification %s completeness %s", res.Service, res.Classification, res.Completeness)
	}
	if res.Gate != impact.GateUnverified || res.GateCode() != impact.GateCodeServiceNameInferred ||
		!strings.Contains(res.GateReason, "inferred from directory payments-api") {
		t.Fatalf("gate = %s %s (%s), want unverified SERVICE_NAME_INFERRED", res.Gate, res.GateCode(), res.GateReason)
	}
}

// An edit stages the new revision in a temporary folder: NewDirName names it
// from the folder it is a copy of, so both sides infer one name.
func TestImpactStagedCopyInfersTheRealFolderName(t *testing.T) {
	t.Parallel()
	oldDir := writeNamelessRevision(t, t.TempDir(), "1.0.0", true)
	staged := filepath.Join(t.TempDir(), ".pacto-edit-123")
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(writeNamelessRevision(t, t.TempDir(), "2.0.0", false), "pacto.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "pacto.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := NewService(nil, nil).Impact(context.Background(), ImpactOptions{
		OldPath: oldDir, NewPath: staged, NewDirName: "payments-api", Fleet: FleetOptions{LocalRoots: []string{t.TempDir()}},
	})
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if res.Service != "payments-api" || res.GateCode() != impact.GateCodeServiceNameInferred {
		t.Fatalf("service %q gate %s %s", res.Service, res.Gate, res.GateCode())
	}
}

func TestNameInferredFromPrefersNew(t *testing.T) {
	t.Parallel()
	declared := &contract.Bundle{}
	inferred := func(src string) *contract.Bundle {
		return &contract.Bundle{Inferred: []contract.Inferred{{Field: "service.name", Value: "a", Source: src}}}
	}
	for _, tc := range []struct {
		old, new *contract.Bundle
		want     string
	}{
		{declared, declared, ""},
		{inferred("old"), declared, "old"},
		{inferred("old"), inferred("new"), "new"},
	} {
		if got := nameInferredFrom(tc.old, tc.new); got != tc.want {
			t.Errorf("nameInferredFrom = %q, want %q", got, tc.want)
		}
	}
}

// An old inferred key is never renamed silently: the answer names the
// successor and carries SUBJECT_REPLACED.
func TestImpactEventOnReplacedKeyDisclosesAlias(t *testing.T) {
	t.Parallel()
	now := time.Now()
	created, pacto := now.Add(-72*time.Hour), now.Add(-time.Hour)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("m", "memory", &fleet.Collection{
			Revisions: []fleet.RawRevision{{Bundle: &contract.Bundle{FS: fstest.MapFS{},
				Contract: &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "auth-service", Version: "1.0.0"}}}}},
			Targets: []fleet.RawTarget{{Scope: "pay", Kind: "kubernetes", Name: "auth-service", Service: "auth-service",
				Workload: &fleet.WorkloadRef{Kind: "Deployment", Name: "legacy"}, CreatedAt: &pacto}},
		}),
		fleet.NewMemorySource("w", fleet.SourceKindKubernetesWorkloads, &fleet.Collection{
			InferredServices: []fleet.RawInferredService{{Namespace: "pay", Kind: "Deployment", Name: "legacy", CreatedAt: &created}},
		}))
	if err != nil || len(snap.Replacements) != 1 {
		t.Fatalf("replacements %+v %v", snap.Replacements, err)
	}
	res, err := NewService(nil, nil).ImpactEventOnSnapshot(context.Background(),
		ImpactEventOptions{Event: impact.Event{Kind: impact.EventRemoved, Service: "k8s:pay/Deployment/legacy"}}, snap)
	if err != nil || !slices.ContainsFunc(res.Limitations, func(l fleet.Limitation) bool { return l.Code == impact.LimitationSubjectReplaced }) {
		t.Fatalf("limitations %+v %v", res, err)
	}
}

// TestImpactEventExternalRefusesTraces: the external answer never reads a
// trace file, so one is refused instead of silently dropped.
func TestImpactEventExternalRefusesTraces(t *testing.T) {
	t.Parallel()
	_, err := NewService(nil, nil).ImpactEventOnSnapshot(context.Background(),
		ImpactEventOptions{Event: impact.Event{Kind: impact.EventChanged, External: "stripe"}, Traces: []byte("{}")}, &fleet.FleetSnapshot{})
	if !errors.Is(err, ErrTracesWithExternal) {
		t.Errorf("err = %v", err)
	}
}

// TestParseEventFlagExternal: external:<identity> names an external system
// for changed and unavailable, as written; removing or regressing one, a
// changed service and an identity Pacto would rewrite are refused.
func TestParseEventFlagExternal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		kind, value string
		want        impact.Event
		err         string
	}{
		{impact.EventChanged, "external:stripe", impact.Event{Kind: "changed", External: "stripe"}, ""},
		{impact.EventUnavailable, "external:aws/rds/123/eu-west-1/db", impact.Event{Kind: "unavailable", External: "aws/rds/123/eu-west-1/db"}, ""},
		{impact.EventRemoved, "external:stripe", impact.Event{}, "not yours to remove or regress"},
		{impact.EventRegressed, "external:stripe@1.0.0", impact.Event{}, "not yours to remove or regress"},
		{"exploded", "external:stripe", impact.Event{}, "unknown event"},
		{impact.EventChanged, "payments", impact.Event{}, "--changed takes external:<identity>"},
		{impact.EventChanged, "external:", impact.Event{}, "pacto fleet search"},
		{impact.EventChanged, "external:Stripe", impact.Event{}, "pacto fleet search"},
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
