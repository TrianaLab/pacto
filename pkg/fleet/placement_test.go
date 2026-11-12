package fleet

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/validation"
)

func TestPlacementCounts(t *testing.T) {
	d, err := envTargetFleet(t).EntityDetail(KindService, "env-svc")
	if err != nil {
		t.Fatal(err)
	}
	p := d.Service.Placement
	type row struct {
		env                                    string
		targets, declared, notDeclared, unknwn int
		revs                                   int
	}
	var got []row
	for _, it := range p.Items {
		got = append(got, row{it.Environment, it.Targets, it.Declared, it.NotDeclared, it.Unknown, it.RevisionsTotal})
	}
	// prod: not declared; staging: two declared plus one with no linked revision;
	// unlabelled and conflicting labels share the empty group, listed last.
	// production is declared by the current revision and reported by no target.
	want := []row{{"prod", 1, 0, 1, 0, 1}, {"production", 0, 0, 0, 0, 0}, {"staging", 3, 2, 0, 1, 1}, {"", 2, 0, 0, 2, 1}}
	if !slices.Equal(got, want) || p.Total != 4 || p.Count != 4 || p.Truncated {
		t.Errorf("placement = %+v (%+v)", got, p)
	}
	if len(p.Items[2].Revisions) != 1 || p.Items[2].Revisions[0].Kind != KindRevision {
		t.Errorf("revisions = %+v", p.Items[2].Revisions)
	}
}

// observedPlacement is placement with no current revision, owner or offering
// use: the groups the targets report, as #466 listed them.
func observedPlacement(q *Query, targets []*TargetRecord) PlacementPreview {
	return q.placement("svc", targets, nil, nil, nil)
}

// placementQuery builds a query over n revisions of svc, newest highest.
func placementQuery(n int) *Query {
	snap := &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{}}
	for i := 1; i <= n; i++ {
		k := RevisionKey(fmt.Sprintf("svc@%d", i))
		snap.Revisions[k] = &ContractRevision{Key: k, Service: "svc", Version: fmt.Sprintf("v1.%d.0", i)}
	}
	return NewQuery(snap)
}

func TestPlacementOrderAndBounds(t *testing.T) {
	q := placementQuery(1)
	var targets []*TargetRecord
	for _, env := range []string{"env-10", "", "env-2", "Prod", "env-2"} {
		targets = append(targets, &TargetRecord{Environment: env, ContractRevision: "svc@1"})
	}
	p := observedPlacement(q, targets)
	var envs []string
	for _, it := range p.Items {
		envs = append(envs, it.Environment)
	}
	if !slices.Equal(envs, []string{"Prod", "env-2", "env-10", ""}) || p.Items[1].Targets != 2 || p.Items[1].RevisionsTotal != 1 {
		t.Errorf("order = %q (%+v)", envs, p.Items)
	}
	targets = targets[:0]
	for i := range MaxDetailPreview + 1 {
		targets = append(targets, &TargetRecord{Environment: fmt.Sprintf("e%d", i)})
	}
	p = observedPlacement(q, targets)
	if p.Total != MaxDetailPreview+1 || p.Count != MaxDetailPreview || !p.Truncated || len(p.Items[0].Revisions) != 0 || p.Items[0].Revisions == nil {
		t.Errorf("bounds: total %d count %d truncated %v", p.Total, p.Count, p.Truncated)
	}
	if empty := observedPlacement(q, nil); empty.Items == nil || empty.Total != 0 {
		t.Errorf("empty = %+v", empty)
	}
}

// TestPlacementInferredLink: a target linked only by a mutable tag is
// unknown and names no revision.
func TestPlacementInferredLink(t *testing.T) {
	q := placementQuery(1)
	p := observedPlacement(q, []*TargetRecord{{Environment: "prod", ContractRevision: "svc@1", RevisionMatch: revisionMatchInferred}})
	if it := p.Items[0]; it.Unknown != 1 || it.Declared+it.NotDeclared != 0 || it.RevisionsTotal != 0 {
		t.Errorf("inferred = %+v", it)
	}
}

func TestPlacementRevisionsTruncated(t *testing.T) {
	q := placementQuery(11)
	targets := func(n int) []*TargetRecord {
		var out []*TargetRecord
		for i := 1; i <= n; i++ {
			// Two targets per revision: still one distinct revision.
			k := RevisionKey(fmt.Sprintf("svc@%d", i))
			out = append(out, &TargetRecord{Environment: "prod", ContractRevision: k}, &TargetRecord{Environment: "prod", ContractRevision: k})
		}
		return out
	}
	it := observedPlacement(q, targets(11)).Items[0]
	if len(it.Revisions) != MaxPlacementRevisions || it.RevisionsTotal != 11 || !it.RevisionsTruncated || it.Revisions[0].Key != "svc@11" || it.Revisions[7].Key != "svc@4" {
		t.Errorf("11 revisions: %d listed, total %d, truncated %v, first %+v", len(it.Revisions), it.RevisionsTotal, it.RevisionsTruncated, it.Revisions[0])
	}
	it = observedPlacement(q, targets(3)).Items[0]
	if len(it.Revisions) != 3 || it.RevisionsTotal != 3 || it.RevisionsTruncated || it.Targets != 6 {
		t.Errorf("3 revisions: %+v", it)
	}
}

func TestPlacementRestricted(t *testing.T) {
	snap := &FleetSnapshot{
		Services: map[ServiceKey]*ServiceRecord{"a": {Key: "a", Name: "a", Targets: []TargetKey{"ta", "tb"}}},
		Targets: map[TargetKey]*TargetRecord{
			"ta": {Key: "ta", Name: "a", Kind: SourceKindKubernetes, Scope: "shop", ServiceKey: "a", Environment: "prod"},
			"tb": {Key: "tb", Name: "b", Kind: SourceKindKubernetes, Scope: "secret", ServiceKey: "a", Environment: "prod"},
		},
	}
	count := func(q *Query) int {
		t.Helper()
		d, err := q.EntityDetail(KindService, "a")
		if err != nil {
			t.Fatal(err)
		}
		return d.Service.Placement.Items[0].Targets
	}
	if n := count(NewQuery(snap)); n != 2 {
		t.Errorf("visible: %d targets", n)
	}
	if n := count(NewQuery(snap).Restrict(func(ns string) bool { return ns != "secret" })); n != 1 {
		t.Errorf("restricted: %d targets, want the hidden one uncounted", n)
	}
}

func TestPlacementLine(t *testing.T) {
	declared := &EnvironmentResult{Status: EnvironmentPass, Lifetime: "temporary", Expires: "2026-01-01T00:00:00Z", Expired: true}
	cases := []struct {
		e    EnvironmentPlacement
		want string
	}{
		{EnvironmentPlacement{Environment: "prod", Targets: 2, RevisionsTotal: 3, RevisionsTruncated: true, TargetsComplete: true,
			Revisions: []EntityRef{{Version: "1.2.0"}, {Label: "svc"}}, Declaration: declared,
			Owner:        EnvironmentOwner{Owner: "team-a", Provenance: "declared"},
			Provisioning: PhaseTally{Ready: 2, Failed: 1}, ProvisioningTracked: true},
			"prod: declared: values pass, temporary, expires 2026-01-01T00:00:00Z, expired (inferred); owner team-a (declared); " +
				"targets you can see: 2, 1.2.0, svc and 1 more; provisioning 2 ready, 1 failed of 3"},
		{EnvironmentPlacement{Environment: "staging", Declaration: &EnvironmentResult{Status: EnvironmentFail, Lifetime: LifetimeNotDeclared},
			Owner: EnvironmentOwner{Owner: "team-b", Provenance: "inferred"}, TargetsReason: targetsIncompleteReason,
			Provisioning: PhaseTally{Requested: 1, Provisioning: 1, Deleting: 1, Unknown: 1}, ProvisioningTracked: true},
			"staging: declared: values fail, lifetime not declared; owner team-b (inferred); targets you can see: 0 (incomplete); " +
				"provisioning 1 requested, 1 provisioning, 1 deleting, 1 unknown of 4"},
		{EnvironmentPlacement{Targets: 1, Unknown: 1, TargetsComplete: true, Owner: EnvironmentOwner{Provenance: "unknown", Reason: "not declared by the current revision"},
			ProvisioningReason: unlabelledProvisioning},
			"(no environment label, unknown): not declared; owner unknown: not declared by the current revision; " +
				"targets you can see: 1, no known revision; provisioning not tracked: " + unlabelledProvisioning},
	}
	for _, c := range cases {
		if got := c.e.Line(); got != c.want {
			t.Errorf("line =\n%q\nwant\n%q", got, c.want)
		}
	}
}

func TestPlacementHeading(t *testing.T) {
	if PlacementHeading(CompletenessComplete) != "Environments" || PlacementHeading(CompletenessPartial) != "Environments (targets Pacto can see)" {
		t.Errorf("headings %q %q", PlacementHeading(CompletenessComplete), PlacementHeading(CompletenessPartial))
	}
}

func TestPlacementLines(t *testing.T) {
	item := func(env string, declared bool) EnvironmentPlacement {
		e := EnvironmentPlacement{Environment: env, TargetsComplete: true, Owner: EnvironmentOwner{Reason: "r"}, ProvisioningReason: "p"}
		if declared {
			e.Declaration = &EnvironmentResult{Status: EnvironmentPass, Lifetime: LifetimeNotDeclared}
		}
		return e
	}
	p := PlacementPreview{Total: 3, Count: 2, Truncated: true, Items: []EnvironmentPlacement{item("env-10", true), item("env-2", true)},
		DifferencesTotal: 2, Differences: []validation.EnvironmentDifference{
			{Configuration: "app", Path: "replicas", Values: map[string]any{"env-10": 3, "env-2": 2}},
			{Configuration: "flags", Values: map[string]any{"env-10": []any{"a"}, "observed": true}},
		}}
	got := strings.Join(p.Lines(), "\n")
	want := strings.Join([]string{
		"  " + p.Items[0].Line(), "  " + p.Items[1].Line(), "  1 more environments",
		"Differences (2 of 2):",
		"  app.replicas:", "    env-2  2", "    env-10  3",
		"  flags:", "    env-2  not set", "    env-10  [\"a\"]", "    observed  true",
	}, "\n")
	if got != want {
		t.Errorf("lines:\n%s\nwant:\n%s", got, want)
	}
	if got := (PlacementPreview{DifferencesError: "environment b: cannot read b.yaml"}).Lines(); !slices.Equal(got, []string{"Differences unknown: environment b: cannot read b.yaml"}) {
		t.Errorf("unreadable differences: %q", got)
	}
	if got := (PlacementPreview{}).Lines(); len(got) != 0 {
		t.Errorf("nothing to list: %q", got)
	}
}
