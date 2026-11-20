package fleet

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// inferredRev is a local revision whose service.name was inferred from source.
func inferredRev(name, dir, source string) RawRevision {
	return RawRevision{
		Bundle: &contract.Bundle{
			Contract: &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: name}},
			FS:       fstest.MapFS{"pacto.yaml": {Data: []byte("")}, "Chart.yaml": {Data: []byte("name: " + dir)}},
			Inferred: []contract.Inferred{{Field: "service.name", Value: name, Source: source}},
		},
		RequestedRef: "file://" + dir,
	}
}

func declaredRev(name, ver, digest string) RawRevision {
	return RawRevision{
		Bundle: &contract.Bundle{
			Contract: &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: ver}},
			FS:       fstest.MapFS{},
		},
		Digest: digest, ResolvedRef: "oci://ghcr.io/acme/" + name + "@" + digest,
	}
}

func collisionLims(snap *FleetSnapshot) []Limitation {
	var out []Limitation
	for _, l := range snap.Limitations {
		if l.Code == LimitationServiceNameCollision {
			out = append(out, l)
		}
	}
	return out
}

func TestRevisionCarriesInferred(t *testing.T) {
	src := NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{inferredRev("pay", "/a/pay", "directory pay")}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	for _, rev := range snap.Revisions {
		if len(rev.Inferred) != 1 || rev.Inferred[0].Source != "directory pay" {
			t.Fatalf("revision inferred = %+v", rev.Inferred)
		}
		return
	}
	t.Fatal("no revision")
}

func TestBuildNameCollisionDropsInferred(t *testing.T) {
	col := &Collection{Revisions: []RawRevision{
		inferredRev("pay", "/r/a/pay", "directory pay"),
		inferredRev("pay", "/r/b/pay", "chart/Chart.yaml:2"),
		inferredRev("solo", "/r/solo", "directory solo"),
	}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	lims := collisionLims(snap)
	if len(lims) != 1 || lims[0].Source != "local" ||
		!strings.Contains(lims[0].Message, "local file:///r/a/pay (directory pay)") ||
		!strings.Contains(lims[0].Message, "local file:///r/b/pay (chart/Chart.yaml:2)") {
		t.Fatalf("limitations = %+v", lims)
	}
	if snap.Completeness != CompletenessPartial {
		t.Fatalf("completeness = %s", snap.Completeness)
	}
	if snap.Services[NewServiceKey("pay")] != nil || snap.Services[NewServiceKey("solo")] == nil {
		t.Fatalf("services = %v", snap.Services)
	}
	if len(col.Revisions) != 3 || len(col.Limitations) != 0 {
		t.Fatal("the source's own collection must not be mutated")
	}
}

func TestBuildDeclaredBeatsInferredName(t *testing.T) {
	src := NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{
		inferredRev("pay", "/r/a/pay", "directory pay"),
		declaredRev("pay", "1.0.0", "sha256:p1"),
		declaredRev("pay", "1.1.0", "sha256:p2"),
		{}, // a revision without a bundle is ignored
	}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	svc := snap.Services[NewServiceKey("pay")]
	if svc == nil || len(svc.Revisions) != 2 || len(collisionLims(snap)) != 1 {
		t.Fatalf("service = %+v, limitations = %+v", svc, snap.Limitations)
	}
	for _, rev := range snap.Revisions {
		if len(rev.Inferred) != 0 {
			t.Fatalf("an inferred revision survived: %+v", rev)
		}
	}
}

func TestBuildInferredNameCollidesWithDeclaredFromOCI(t *testing.T) {
	local := NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{inferredRev("payments", "/r/payments-api", "chart/Chart.yaml:2")}})
	oci := NewMemorySource("registry", "oci", &Collection{Revisions: []RawRevision{declaredRev("payments", "2.0.0", "sha256:d")}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, local, oci, NewMemorySource("empty", "oci", nil))
	if err != nil {
		t.Fatal(err)
	}
	lims := collisionLims(snap)
	if len(lims) != 1 || lims[0].Source != "local" ||
		!strings.Contains(lims[0].Message, "local file:///r/payments-api (chart/Chart.yaml:2)") ||
		!strings.Contains(lims[0].Message, "registry oci://ghcr.io/acme/payments@sha256:d (declared)") {
		t.Fatalf("limitations = %+v", lims)
	}
	if len(snap.Revisions) != 1 || snap.Completeness != CompletenessPartial {
		t.Fatalf("revisions = %v completeness = %s", snap.Revisions, snap.Completeness)
	}
}

// K8sSource and the target-state source send targets only, never revisions: a
// target naming payments is a declared claim the inferred local name collides
// with, so the guess never joins payments and inherits its prod target.
func TestBuildInferredNameDoesNotTakeTargets(t *testing.T) {
	for _, kind := range []string{"kubernetes", "target-state"} {
		local := NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{inferredRev("payments", "/r/payments-api", "chart/Chart.yaml:2")}})
		targets := NewMemorySource("targets", kind, &Collection{
			Targets: []RawTarget{{Scope: "prod", Kind: "k8s", Name: "payments", Service: "payments", Compliance: StatusCompliant}},
		})
		snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, local, targets)
		if err != nil {
			t.Fatal(err)
		}
		if svc := snap.Services[NewServiceKey("payments")]; svc == nil || len(svc.Revisions) != 0 {
			t.Fatalf("%s: service = %+v", kind, svc)
		}
		lims := collisionLims(snap)
		if len(lims) != 1 || !strings.Contains(lims[0].Message, "targets target prod/k8s/payments (declared)") ||
			snap.Completeness != CompletenessPartial {
			t.Fatalf("%s: limitations = %+v completeness = %s", kind, lims, snap.Completeness)
		}
	}
}

// One bundle read by two sources is one claimant: it is no collision. Two
// folders with identical content are still two claimants.
func TestBuildSameBundleTwoSourcesIsNoCollision(t *testing.T) {
	a, b := inferredRev("my-svc", "my-svc", "directory my-svc"), inferredRev("my-svc", "my-svc", "directory my-svc")
	b.RequestedRef = "./my-svc" // a catalog root names the folder without file://
	a.Digest, b.Digest = "sha256:same", "sha256:same"
	twin := inferredRev("my-svc", "/elsewhere/my-svc", "directory my-svc")
	twin.Digest = "sha256:same"
	if snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{a, twin}})); err != nil || len(collisionLims(snap)) != 1 {
		t.Fatalf("two folders, same content: %v %+v", err, snap.Limitations)
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{a}}),
		NewMemorySource("catalog", "local", &Collection{Revisions: []RawRevision{b}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(collisionLims(snap)) != 0 || snap.Services[NewServiceKey("my-svc")] == nil {
		t.Fatalf("limitations = %+v services = %v", snap.Limitations, snap.Services)
	}
}

// A folder pacto ci planned from its published baseline infers the name the
// baseline declares: it is that service, so nothing collides and the answer
// stays complete. A Baseline naming a digest no declared claim of that name
// holds confirms nothing, and a third inferred claimant still collides
// without taking the confirmed folder with it.
func TestBuildInferredNameConfirmedByOwnBaseline(t *testing.T) {
	planned := func(dir, baseline string) RawRevision {
		r := inferredRev("payments", dir, "chart/Chart.yaml:2")
		r.Baseline = baseline
		return r
	}
	build := func(revs ...RawRevision) *FleetSnapshot {
		t.Helper()
		local := NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{inferredRev("payments", "/r/payments", "chart/Chart.yaml:2")}})
		changes := NewMemorySource("ci-changes", "argument", &Collection{Revisions: append([]RawRevision{declaredRev("payments", "1.4.2", "sha256:base")}, revs...)})
		snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, local, changes)
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	snap := build(planned("/r/payments", "sha256:base"))
	if lims := collisionLims(snap); len(lims) != 0 || snap.Completeness != CompletenessComplete {
		t.Fatalf("confirmed: limitations = %+v, completeness = %s", lims, snap.Completeness)
	}
	if svc := snap.Services[NewServiceKey("payments")]; svc == nil || len(svc.Revisions) != 2 {
		t.Fatalf("confirmed: service = %+v", svc)
	}
	if snap := build(planned("/r/payments", "sha256:other")); len(collisionLims(snap)) == 0 || snap.Completeness != CompletenessPartial {
		t.Fatalf("unconfirmed: limitations = %+v", snap.Limitations)
	}
	snap = build(planned("/r/payments", "sha256:base"), inferredRev("payments", "/r/payments-copy", "directory payments"))
	if lims := collisionLims(snap); len(lims) != 1 || !strings.Contains(lims[0].Message, "/r/payments-copy") {
		t.Fatalf("third claimant: limitations = %+v", lims)
	}
	if svc := snap.Services[NewServiceKey("payments")]; svc == nil || len(svc.Revisions) != 2 {
		t.Fatalf("third claimant: service = %+v", svc)
	}
}
