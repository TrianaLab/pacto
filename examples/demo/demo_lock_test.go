package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ignore"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/lock"
)

// TestDemoLockPinsTheLocalProvider holds catalog-service 1.3.0's committed lock
// to the postgresql bundle beside it: the fleet resolves the configuration
// reference through the lock's contentHash, so an edit to postgresql 16.1.0 that
// skips the lock would silently unresolve the demo's only provisioning request.
func TestDemoLockPinsTheLocalProvider(t *testing.T) {
	l, err := lock.ReadFS(os.DirFS(filepath.Join("bundles", "catalog-service", "v1.3.0")))
	if err != nil || l == nil {
		t.Fatalf("read catalog-service 1.3.0 pacto.lock: %v", err)
	}
	if l.LockVersion < lock.RootOccurrenceLockVersion {
		t.Errorf("lockVersion %d cannot attribute a root reference", l.LockVersion)
	}
	want := hashBundle(t, filepath.Join("bundles", "postgresql", "v16.1.0"))
	for _, r := range l.References {
		if r.From == "" && r.Kind == "config" && r.Name == "database" {
			if r.ContentHash != want {
				t.Errorf("contentHash %s, want %s (lock.HashFS of postgresql 16.1.0): update the lock", r.ContentHash, want)
			}
			// The lock holds the full closure, as `pacto lock` writes it: the
			// postgresql dependency resolves to the same published revision, so a
			// live `pacto validate` reads the lock as current, not stale.
			dep, ok := l.Dependency("postgresql")
			if !ok || dep.Digest != r.Digest || dep.Version != r.Version {
				t.Errorf("dependency entry %+v (found %v) must pin the reference's revision %s %s", dep, ok, r.Version, r.Digest)
			}
			return
		}
	}
	t.Fatal("the lock has no root entry for the database configuration reference")
}

// TestDemoConfigRefEdgeResolves builds the fleet `pacto fleet --local
// examples/demo/bundles` builds and checks catalog-service 1.3.0's configuration
// reference resolves to postgresql, and that impact of postgresql 16.0.1 to
// 16.1.0 is complete and lists catalog-service.
func TestDemoConfigRefEdgeResolves(t *testing.T) {
	ctx := context.Background()
	snap, err := fleet.Build(ctx, fleet.BuildOptions{}, fleetsrc.NewLocalSource("local", "bundles"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rel := range snap.Relationships {
		if rel.Type == fleet.RelationshipConfigRef && rel.To == "database" && rel.FromService == "catalog-service" {
			found = true
			if !rel.Resolved || rel.ToService != "postgresql" {
				t.Errorf("catalog-service database reference: resolved=%v to=%q", rel.Resolved, rel.ToService)
			}
		}
	}
	if !found {
		t.Fatal("no configuration reference from catalog-service in the demo fleet")
	}

	oldC, oldFS := readBundle(t, filepath.Join("bundles", "postgresql", "v16.0.1"))
	newC, newFS := readBundle(t, filepath.Join("bundles", "postgresql", "v16.1.0"))
	res := impact.Analyze(ctx, oldC, newC, oldFS, newFS, snap, impact.Options{})
	if res.Completeness != fleet.CompletenessComplete {
		t.Errorf("impact completeness %q, want complete: %+v", res.Completeness, res.Limitations)
	}
	for _, c := range res.Consumers {
		if c.Service == "catalog-service" {
			if c.CompatibilityVerdict != "compatible" {
				t.Errorf("catalog-service verdict %q, want compatible", c.CompatibilityVerdict)
			}
			return
		}
	}
	t.Error("impact does not list catalog-service")
}

// TestDemoTargetStateProvisioningMatches holds fleet-targets.yaml's provisioning
// reports to the revisions they name: each digest is the local content identity
// of its consumer bundle, so an edit to a bundle (its pacto.lock included) that
// skips the file would drop the report as unmatched and the CLI demo would show
// no provisioning status at all.
func TestDemoTargetStateProvisioningMatches(t *testing.T) {
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleetsrc.NewLocalSource("local", "bundles"), fleetsrc.NewTargetStateFileSource("target-state", "fleet-targets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range snap.Limitations {
		if l.Code == fleet.LimitationProvisioningUnmatched || l.Code == fleet.LimitationSourceRecordInvalid {
			t.Errorf("%s: %s", l.Code, l.Message)
		}
	}
	if len(snap.Provisioning) != 2 {
		t.Errorf("provisioning records = %d, want both reports kept", len(snap.Provisioning))
	}
}

// TestDemoPlacement: the CLI demo's targets say where three services run.
// payments-service runs in production; audit-log's target names the
// environment of its own Stage (an exact match, so the page links the two);
// notification-worker runs in staging, which none of its Stages covers.
func TestDemoPlacement(t *testing.T) {
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleetsrc.NewLocalSource("local", "bundles"), fleetsrc.NewTargetStateFileSource("target-state", "fleet-targets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	placed := func(svc string) []string {
		d, err := q.EntityDetail(fleet.KindService, svc)
		if err != nil {
			t.Fatal(err)
		}
		var envs []string
		for _, p := range d.Service.Placement.Items {
			envs = append(envs, p.Environment)
		}
		return envs
	}
	d, err := newDemoDelivery(time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	stages, _ := d.Stages(t.Context(), "")
	envsOf := func(svc string) []string {
		var envs []string
		for _, st := range stages {
			if st.Spec.Service.Name == svc {
				envs = append(envs, st.Spec.Environment)
			}
		}
		return envs
	}
	if got := placed("payments-service"); !slices.Contains(got, "production") {
		t.Errorf("payments-service placement %v, want production", got)
	}
	audit := envsOf("audit-log")
	if got := placed("audit-log"); len(audit) != 1 || !slices.Contains(got, audit[0]) {
		t.Errorf("audit-log placement %v, want its Stage's environment %v", got, audit)
	}
	if got, covered := placed("notification-worker"), envsOf("notification-worker"); !slices.Contains(got, "staging") || slices.Contains(covered, "staging") {
		t.Errorf("notification-worker placement %v with Stages in %v, want staging beside them", got, covered)
	}
}

// hashBundle is the content identity the local fleet source gives a bundle dir.
func hashBundle(t *testing.T, dir string) string {
	t.Helper()
	dirFS := os.DirFS(dir)
	m, err := ignore.Load(dirFS)
	if err != nil {
		t.Fatal(err)
	}
	h, err := lock.HashFS(ignore.FS(dirFS, m))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// readBundle parses the contract in dir and returns it with the dir's FS.
func readBundle(t *testing.T, dir string) (*contract.Contract, fs.FS) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := contract.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return c, os.DirFS(dir)
}
