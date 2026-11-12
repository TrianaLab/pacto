package fleet

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// servicePlacement is the placement of svc in q's service detail.
func servicePlacement(t *testing.T, q *Query, svc string) PlacementPreview {
	t.Helper()
	d, err := q.EntityDetail(KindService, svc)
	if err != nil {
		t.Fatal(err)
	}
	return d.Service.Placement
}

// placementItem is the group for env, failing when there is none.
func placementItem(t *testing.T, p PlacementPreview, env string) EnvironmentPlacement {
	t.Helper()
	for _, it := range p.Items {
		if it.Environment == env {
			return it
		}
	}
	t.Fatalf("no group %q in %+v", env, p.Items)
	return EnvironmentPlacement{}
}

func TestPlacementDeclaredUnionObserved(t *testing.T) {
	p := servicePlacement(t, envTargetFleet(t), "env-svc")
	production := placementItem(t, p, "production")
	if d := production.Declaration; d == nil || d.Lifetime != LifetimeNotDeclared || production.Targets != 0 || production.Revisions == nil {
		t.Errorf("declared only: %+v", production)
	}
	prod := placementItem(t, p, "prod")
	if prod.Declaration != nil || prod.Targets != 1 || prod.Owner != (EnvironmentOwner{Provenance: "unknown", Reason: "not declared by the current revision"}) {
		t.Errorf("observed only: %+v", prod)
	}
	staging := placementItem(t, p, "staging")
	if d := staging.Declaration; d == nil || d.Status != EnvironmentPass || staging.Targets != 3 {
		t.Errorf("both: %+v", staging)
	}
	if o := staging.Owner; o.Provenance != "inferred" || o.Owner != "platform" {
		t.Errorf("staging owner: %+v", o)
	}
	if p.CurrentRevision == nil || p.CurrentRevision.Kind != KindRevision {
		t.Errorf("current revision = %+v", p.CurrentRevision)
	}
}

// TestPlacementKeepsObservedCounts: every field #466 had reads the same for
// the observed groups once declared environments join them.
func TestPlacementKeepsObservedCounts(t *testing.T) {
	q := envTargetFleet(t)
	view, err := q.GetService("env-svc")
	if err != nil {
		t.Fatal(err)
	}
	observed := q.placement("env-svc", view.Targets, nil, nil, nil)
	full := servicePlacement(t, q, "env-svc")
	type fields struct {
		env                                     string
		targets, declared, notDeclared, unknown int
		revisions                               []EntityRef
		total                                   int
		truncated                               bool
	}
	pick := func(e EnvironmentPlacement) fields {
		return fields{e.Environment, e.Targets, e.Declared, e.NotDeclared, e.Unknown, e.Revisions, e.RevisionsTotal, e.RevisionsTruncated}
	}
	for _, o := range observed.Items {
		if got, want := pick(placementItem(t, full, o.Environment)), pick(o); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %+v, want %+v", o.Environment, got, want)
		}
	}
}

func TestPlacementOwnerFallback(t *testing.T) {
	declared := &EnvironmentResult{Owner: &contract.Owner{Team: "previews"}}
	contactsOnly := &EnvironmentResult{Owner: &contract.Owner{Contacts: []contract.OwnerContact{{Type: "email", Value: "a@b.c"}}}}
	bare := &EnvironmentResult{}
	svcRef := ownerEntityRef("team:platform")
	cases := []struct {
		name        string
		d           *EnvironmentResult
		svc         *OwnershipInfo
		owner, prov string
		ref         bool
	}{
		{"declared", declared, &OwnershipInfo{Owner: "platform", Declared: true}, "previews", "declared", true},
		{"declared contacts only", contactsOnly, nil, "a@b.c", "declared", false},
		{"inferred", bare, &OwnershipInfo{Owner: "platform", Ref: &svcRef, Declared: true}, "platform", "inferred", true},
		{"inferred contacts only", bare, &OwnershipInfo{Declared: true}, "", "inferred", false},
		{"service owner unknown", bare, &OwnershipInfo{Unknown: true}, "", "unknown", false},
		{"nobody declares one", bare, &OwnershipInfo{}, "", "unknown", false},
		{"observed only", nil, &OwnershipInfo{Owner: "platform", Declared: true}, "", "unknown", false},
	}
	for _, c := range cases {
		got := environmentOwner(c.d, c.svc)
		if !strings.Contains(got.Owner, c.owner) || got.Provenance != c.prov || (got.Ref != nil) != c.ref || (got.Provenance != "declared") != (got.Reason != "") {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
	contacts := EnvironmentPlacement{Owner: environmentOwner(bare, &OwnershipInfo{Declared: true})}
	if got := contacts.ownerText(); got != "owner declared by contacts only (service owner, inferred)" {
		t.Errorf("a contacts-only service owner reads %q", got)
	}
	if got := environmentOwner(bare, &OwnershipInfo{Owner: "platform", Ref: &svcRef, Declared: true}); got.Ref == &svcRef {
		t.Error("the inferred owner ref aliases the service's")
	}
}

// declaringQuery is a one-revision query whose revision declares envs.
func declaringQuery(envs ...string) (*Query, *ContractRevision) {
	q := placementQuery(1)
	rev := q.snap.Revisions["svc@1"]
	for _, e := range envs {
		rev.Environments = append(rev.Environments, EnvironmentResult{Name: e, Status: EnvironmentPass, Lifetime: LifetimeNotDeclared})
	}
	return q, rev
}

func TestPlacementProvisioningPerEnvironment(t *testing.T) {
	q, rev := declaringQuery("production", "staging")
	uses := []OfferingUse{
		{Provisioning: []ProvisioningStatus{{Environment: "staging", Phase: "Ready"}, {Environment: "production", Phase: "Failed"}}},
		{Provisioning: []ProvisioningStatus{{Environment: "", Phase: "Ready"}, {Environment: "staging", Phase: "Unknown"}}},
	}
	p := q.placement("svc", nil, rev, nil, uses)
	staging, production := placementItem(t, p, "staging"), placementItem(t, p, "production")
	if (staging.Provisioning != PhaseTally{Ready: 1, Unknown: 1}) || !staging.ProvisioningTracked || staging.ProvisioningReason != "" {
		t.Errorf("staging = %+v", staging)
	}
	if (production.Provisioning != PhaseTally{Failed: 1}) || !production.ProvisioningTracked {
		t.Errorf("production = %+v", production)
	}
}

func TestPlacementProvisioningUntrackedForObservedOnly(t *testing.T) {
	q, rev := declaringQuery("staging")
	p := q.placement("svc", []*TargetRecord{{Environment: "prod"}, {}}, rev, nil, nil)
	for env, want := range map[string]string{
		"prod":    "the current revision does not declare prod, so no provider is asked about it there",
		"staging": noProvisioningHere,
		"":        unlabelledProvisioning,
	} {
		if it := placementItem(t, p, env); it.ProvisioningTracked || it.ProvisioningReason != want || (it.Provisioning != PhaseTally{}) {
			t.Errorf("%q: %+v", env, it)
		}
	}
	q, rev = declaringQuery()
	if it := q.placement("svc", []*TargetRecord{{Environment: "prod"}}, rev, nil, nil).Items[0]; it.ProvisioningTracked || it.ProvisioningReason != noEnvironmentsDeclared {
		t.Errorf("a revision that declares none: %+v", it)
	}
}

func TestPlacementDifferences(t *testing.T) {
	// Built through Query from a snapshot: the detail's revision is a clone
	// without bundle files, so the differences must come from the snapshot.
	p := servicePlacement(t, envTargetFleet(t), "env-svc")
	want := []validation.EnvironmentDifference{{Configuration: "app", Path: "replicas", Values: map[string]any{"staging": float64(2), "production": float64(3)}}}
	if !reflect.DeepEqual(p.Differences, want) || p.DifferencesTotal != 1 || p.DifferencesError != "" {
		t.Errorf("differences = %+v (total %d, error %q)", p.Differences, p.DifferencesTotal, p.DifferencesError)
	}
}

func TestPlacementDifferencesUnreadable(t *testing.T) {
	fsys := fstest.MapFS{
		"app.json":          {Data: []byte(`{"type":"object"}`)},
		"envs/staging.yaml": {Data: []byte("app:\n  replicas: 2\n")},
	}
	src := NewMemorySource("a", "local", &Collection{
		Revisions: []RawRevision{{Bundle: &contract.Bundle{Contract: mustParse(t, envLeafYAML), RawYAML: []byte(envLeafYAML), FS: fsys}, Digest: "sha256:env"}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	p := servicePlacement(t, NewQuery(snap), "env-svc")
	if !strings.Contains(p.DifferencesError, "production") || len(p.Differences) != 0 || p.Differences == nil {
		t.Errorf("unreadable differences = %+v, error %q", p.Differences, p.DifferencesError)
	}
}

// A contract-only revision (no document, no files) declaring two environments
// states that their values were not read, so "the same values" is never shown.
func TestPlacementDifferencesContractOnly(t *testing.T) {
	src := NewMemorySource("a", "local", &Collection{
		Revisions: []RawRevision{{Bundle: &contract.Bundle{Contract: mustParse(t, envLeafYAML)}, Digest: "sha256:env"}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	p := servicePlacement(t, NewQuery(snap), "env-svc")
	if !strings.Contains(p.DifferencesError, "no bundle files") || len(p.Differences) != 0 {
		t.Errorf("contract-only differences = %+v, error %q", p.Differences, p.DifferencesError)
	}
	// A later contribution that read the document replaces the reason.
	existing := &ContractRevision{differencesErr: "no bundle files"}
	diffs := []validation.EnvironmentDifference{{Configuration: "app", Path: "replicas"}}
	mergeRevision(existing, &ContractRevision{validated: true, differences: diffs})
	if existing.differencesErr != "" || len(existing.differences) != 1 {
		t.Errorf("a validated contribution's comparison wins: %+v %q", existing.differences, existing.differencesErr)
	}
}

func TestMergeRevisionKeepsDifferences(t *testing.T) {
	diffs := []validation.EnvironmentDifference{{Configuration: "app", Path: "replicas"}}
	existing := &ContractRevision{}
	mergeRevision(existing, &ContractRevision{differences: diffs, differencesTotal: 1})
	if len(existing.differences) != 1 || existing.differencesTotal != 1 {
		t.Errorf("filled from add: %+v", existing.differences)
	}
	mergeRevision(existing, &ContractRevision{differencesErr: "x"})
	if existing.differencesErr != "" || len(existing.differences) != 1 {
		t.Errorf("an existing answer stays: %+v %q", existing.differences, existing.differencesErr)
	}
	failed := &ContractRevision{differencesErr: "cannot read"}
	mergeRevision(failed, &ContractRevision{differences: diffs})
	if failed.differences != nil {
		t.Errorf("an existing reason stays: %+v", failed.differences)
	}
}

// restrictedEnvFleet is env-svc with a staging target in shop and a
// production target and a target labelled secret-env in the namespace secret.
func restrictedEnvFleet(t *testing.T) *Query {
	t.Helper()
	fsys := fstest.MapFS{
		"app.json":             {Data: []byte(`{"type":"object"}`)},
		"envs/staging.yaml":    {Data: []byte("app:\n  replicas: 2\n")},
		"envs/production.yaml": {Data: []byte("app:\n  replicas: 3\n")},
	}
	tgt := func(ns, name, env string) RawTarget {
		return RawTarget{Scope: ns, Kind: SourceKindKubernetes, Name: name, Service: "env-svc", Digest: "sha256:env", Compliance: StatusCompliant,
			Labels: map[string]string{TargetEnvironmentLabel: env}}
	}
	src := NewMemorySource("a", "local", &Collection{
		Revisions: []RawRevision{{Bundle: &contract.Bundle{Contract: mustParse(t, envLeafYAML), RawYAML: []byte(envLeafYAML), FS: fsys}, Digest: "sha256:env"}},
		Targets:   []RawTarget{tgt("shop", "web", "staging"), tgt("secret", "web", "production"), tgt("secret", "other", "secret-env")},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap).Restrict(func(ns string) bool { return ns != "secret" })
}

func TestPlacementDeclaredGroupTargetsHidden(t *testing.T) {
	p := servicePlacement(t, restrictedEnvFleet(t), "env-svc")
	production := placementItem(t, p, "production")
	if production.Targets != 0 || production.TargetsComplete || production.TargetsReason != targetsIncompleteReason || strings.Contains(production.TargetsReason, "production") {
		t.Errorf("production = %+v", production)
	}
	if staging := placementItem(t, p, "staging"); staging.TargetsComplete || staging.Targets != 1 {
		t.Errorf("staging = %+v", staging)
	}
	if all := servicePlacement(t, envTargetFleet(t), "env-svc"); !all.Items[0].TargetsComplete || all.Items[0].TargetsReason != "" {
		t.Errorf("unrestricted = %+v", all.Items[0])
	}
}

func TestPlacementEnvironmentsRestricted(t *testing.T) {
	p := servicePlacement(t, restrictedEnvFleet(t), "env-svc")
	var envs []string
	for _, it := range p.Items {
		envs = append(envs, it.Environment)
	}
	if strings.Join(envs, ",") != "production,staging" || p.Total != 2 {
		t.Errorf("groups = %q: a hidden target's label is never a group", envs)
	}
}

func TestTargetEnvironmentOwnerImmutable(t *testing.T) {
	owner := &contract.Owner{Team: "a", Contacts: []contract.OwnerContact{{Type: "email", Value: "a@b.c"}}}
	snap := &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{
		"svc@1": {Key: "svc@1", Environments: []EnvironmentResult{{Name: "prod", Owner: owner}}},
	}}
	got := NewQuery(snap).targetEnvironment(&TargetRecord{Environment: "prod", ContractRevision: "svc@1"})
	got.Check.Owner.Team = "b"
	got.Check.Owner.Contacts[0].Value = "x"
	if owner.Team != "a" || owner.Contacts[0].Value != "a@b.c" {
		t.Errorf("the snapshot's owner changed: %+v", owner)
	}
}

// When a newer revision is published but not running, provisioning slots are
// seeded from it: an environment only the running revision declares reads
// unknown, naming that revision, and never "no offering use provisions".
func TestPlacementProvisioningSeededFromNewerRevision(t *testing.T) {
	running := consumer("orders", "1.2.0", "postgresql")
	running.Bundle.Contract.Environments = []contract.Environment{{Name: "production", File: "p.yaml"}, {Name: "staging", File: "s.yaml"}}
	published := consumer("orders", "1.3.0", "postgresql")
	published.Bundle.Contract.Environments = []contract.Environment{{Name: "production", File: "p.yaml"}}
	snap := provisioningSnap(t, 0, &Collection{
		Revisions: []RawRevision{provider("postgresql"), running, published},
		Targets: []RawTarget{{Scope: "prod", Kind: "k8s", Name: "orders", Service: "orders", Domain: acme, Digest: testDigest(acme, "orders", "1.2.0"),
			Compliance: StatusCompliant, Labels: map[string]string{TargetEnvironmentLabel: "staging"}}},
	})
	p := servicePlacement(t, NewQuery(snap), "orders")
	if p.CurrentRevision == nil || p.CurrentRevision.Label != "orders 1.2.0" {
		t.Fatalf("current revision = %+v, want the running 1.2.0", p.CurrentRevision)
	}
	st := placementItem(t, p, "staging")
	if st.ProvisioningTracked || st.ProvisioningReason == noProvisioningHere || !strings.Contains(st.ProvisioningReason, "seeded from orders 1.3.0") {
		t.Errorf("staging = tracked %v, reason %q", st.ProvisioningTracked, st.ProvisioningReason)
	}
	if pr := placementItem(t, p, "production"); !pr.ProvisioningTracked {
		t.Errorf("production is seeded by 1.3.0 too, so it is tracked: %+v", pr)
	}
}
