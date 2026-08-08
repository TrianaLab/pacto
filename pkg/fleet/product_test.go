package fleet

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/finding"
)

// readyContract returns a readiness assessment that passes, so a revision built
// from it never trips the MISSING_READINESS attention item (used to keep the
// product fixture's attention set predictable).
func readyContract() *contract.Readiness {
	return &contract.Readiness{
		Expires: "2099-12-31",
		Claims:  []contract.ReadinessClaim{{ID: "dash", Type: "url", Status: contract.StatusDone, Evidence: "https://x", Weight: 10}},
	}
}

// productFleet builds one rich snapshot that exercises every product-layer branch:
// exact/inferred/ambiguous/unresolved target links; compliant/non-compliant/
// unknown/stale targets; fresh/stale/absent/ancient evidence; declared, observed
// (matched) and observed-only (shadow) relationships; an unresolved declared
// dependency; an invalid revision; and available/stale/partial/unavailable sources.
func productFleet(t *testing.T) *Query {
	t.Helper()
	fresh := ptrTime(fixedNow().Add(-1 * time.Minute))
	old := ptrTime(fixedNow().Add(-2 * time.Hour))
	ancient := ptrTime(fixedNow().Add(-48 * time.Hour))

	alpha := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "alpha", Version: "1.0.0", Owner: contract.Owner{Team: "team-a", DRI: "alice"}},
		Workload:     contract.WorkloadService,
		Dependencies: []contract.Dependency{
			{Name: "leaf-svc", Ref: "oci://x/leaf", Required: true, Compatibility: "^1.0.0"},
			{Name: "ghost", Ref: "oci://x/ghost", Required: false, Compatibility: "^1.0.0"},
		},
		// A configuration reference to leaf-svc creates a config-reference edge,
		// which the dependency neighborhood must skip (it is not a dependency).
		Configurations: []contract.Configuration{{Name: "leaf-svc", Ref: "oci://x/leaf-config"}},
		Readiness:      readyContract(),
	}
	leaf := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "leaf-svc", Version: "1.0.0", Owner: contract.Owner{Team: "leaf-team"}},
		Interfaces:   []contract.Interface{{Name: "http", Type: contract.InterfaceTypeOpenAPI, Ref: "interfaces/openapi.json"}},
		Readiness:    readyContract(),
	}
	leafFS := fstest.MapFS{"interfaces/openapi.json": {Data: []byte(smallOpenAPI)}}
	// Two same-version "beta" revisions with different content (distinct
	// content-derived keys but a colliding version) make a version-only target
	// reference ambiguous.
	beta1 := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "beta", Version: "2.0.0", Owner: contract.Owner{Team: "team-b"}}, Readiness: readyContract(), Capabilities: []contract.Capability{{Type: contract.CapabilityHealth}}}
	beta2 := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "beta", Version: "2.0.0", Owner: contract.Owner{Team: "team-b"}}, Readiness: readyContract()}
	solo := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "solo", Version: "1.0.0", Owner: contract.Owner{Team: "solo-team"}}, Readiness: readyContract()}

	local := NewMemorySource("local", "local", &Collection{
		Revisions: []RawRevision{
			{Bundle: &contract.Bundle{Contract: alpha, FS: fstest.MapFS{}}, Digest: "sha256:alpha"},
			{Bundle: &contract.Bundle{Contract: leaf, FS: leafFS}, Digest: "sha256:leaf", ResolvedRef: "oci://x/leaf:1.0.0"},
			{Bundle: &contract.Bundle{Contract: beta1, FS: fstest.MapFS{"a.txt": {Data: []byte("A")}}}, Domain: "", Digest: "sha256:beta1"},
			{Bundle: &contract.Bundle{Contract: beta2, FS: fstest.MapFS{"b.txt": {Data: []byte("B")}}}, Domain: "", Digest: "sha256:beta2"},
			{Bundle: &contract.Bundle{Contract: solo, FS: fstest.MapFS{}}, Digest: "sha256:solo"},
			{Bundle: &contract.Bundle{Contract: mustParse(t, invalidYAML), RawYAML: []byte(invalidYAML), FS: fstest.MapFS{}}},
		},
		Targets: []RawTarget{
			{Scope: "prod", Kind: "k8s", Name: "alpha-app", Service: "alpha", Digest: "sha256:alpha", Compliance: StatusCompliant, EvidenceAt: fresh, Labels: map[string]string{"tier": "gold"},
				Coverage: &Coverage{Evaluated: 3, Required: 5}, ObservedRuntime: map[string]any{"replicas": 3}},
			{Scope: "prod", Kind: "k8s", Name: "leaf-app", Service: "leaf-svc", ResolvedRef: "oci://x/leaf:1.0.0", Compliance: StatusCompliant, EvidenceAt: fresh},
			{Scope: "prod", Kind: "k8s", Name: "beta-app", Service: "beta", ResolvedRef: "reg/beta:2.0.0", Compliance: StatusNonCompliant, EvidenceAt: old,
				Findings: []finding.Finding{{Code: "DRIFT", Severity: finding.SeverityError, Subject: finding.SubjectRef{Kind: "interface", Name: "http"}, Message: "runtime drift"}}},
			{Scope: "prod", Kind: "k8s", Name: "solo-app", Service: "solo", Digest: "sha256:nomatch", Compliance: StatusUnknown},
			{Scope: "prod", Kind: "k8s", Name: "alpha-ancient", Service: "alpha", Digest: "sha256:alpha", Compliance: StatusCompliant, EvidenceAt: ancient},
		},
		Observed: []ObservedEdge{
			{From: "alpha", To: "leaf-svc", Count: 5, FirstSeen: fixedNow().Add(-2 * time.Hour), LastSeen: fixedNow().Add(-30 * time.Minute)},
			{From: "beta", To: "alpha", Count: 3, FirstSeen: fixedNow().Add(-90 * time.Minute), LastSeen: fixedNow().Add(-45 * time.Minute)},
		},
	})
	staleSrc := NewMemorySource("stale-oci", "oci", &Collection{State: &SourceState{Status: SourceStale}})
	partialSrc := NewMemorySource("partial-oci", "oci", &Collection{State: &SourceState{Status: SourcePartial}})
	downSrc := NewFailingSource("down", "oci", errors.New("registry unreachable"))

	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: time.Hour}, local, staleSrc, partialSrc, downSrc)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return NewQuery(snap)
}

func TestOverview_Counts(t *testing.T) {
	q := productFleet(t)
	ov := q.Overview()

	if ov.Meta.SchemaVersion != ProductSchemaVersion {
		t.Errorf("schema version = %q", ov.Meta.SchemaVersion)
	}
	if q.ProductMeta().SchemaVersion != ProductSchemaVersion || q.ProductMeta().SnapshotID != ov.Meta.SnapshotID {
		t.Errorf("ProductMeta accessor disagrees with the overview meta")
	}
	if ov.Meta.Completeness != CompletenessPartial {
		t.Errorf("a failing source must make the snapshot partial, got %q", ov.Meta.Completeness)
	}
	s := ov.Summary
	checks := map[string]struct{ got, want int }{
		"services":                  {s.Services, 5},
		"servicesNeedingAttention":  {s.ServicesNeedingAttention, 4},
		"invalidRevisions":          {s.InvalidRevisions, 1},
		"exactTargetLinks":          {s.ExactTargetLinks, 2},
		"inferredTargetLinks":       {s.InferredTargetLinks, 1},
		"ambiguousTargetLinks":      {s.AmbiguousTargetLinks, 1},
		"unresolvedTargetLinks":     {s.UnresolvedTargetLinks, 1},
		"nonCompliantTargets":       {s.NonCompliantTargets, 1},
		"unknownTargets":            {s.UnknownTargets, 1},
		"staleTargets":              {s.StaleTargets, 2},
		"unresolvedRelationships":   {s.UnresolvedRelationships, 1},
		"observedOnlyRelationships": {s.ObservedOnlyRelationships, 1},
		"degradedSources":           {s.DegradedSources, 1},
		"staleSources":              {s.StaleSources, 1},
		"unavailableSources":        {s.UnavailableSources, 1},
		"recentEvidence":            {s.RecentEvidence, 3},
	}
	for name, c := range checks {
		if c.got != c.want {
			t.Errorf("summary.%s = %d, want %d", name, c.got, c.want)
		}
	}
}

func TestOverview_EntryPointsAndEvidence(t *testing.T) {
	q := productFleet(t)
	ov := q.Overview()

	// Every entry point has a non-empty route and a positive count.
	if len(ov.EntryPoints) == 0 {
		t.Fatal("expected entry points for a fleet needing attention")
	}
	for _, ep := range ov.EntryPoints {
		if ep.View == "" || ep.Count <= 0 {
			t.Errorf("entry point %q: view=%q count=%d", ep.Label, ep.View, ep.Count)
		}
	}
	// Recent evidence is an explicit preview: newest-first, bounded, each item
	// navigable, with a true total and count.
	re := ov.RecentEvidence
	if re.Total != 3 || re.Count != 3 || len(re.Items) != 3 || re.Truncated {
		t.Fatalf("recent evidence preview = {total:%d count:%d items:%d trunc:%v}, want 3/3/3/false", re.Total, re.Count, len(re.Items), re.Truncated)
	}
	for i := 1; i < len(re.Items); i++ {
		a, b := re.Items[i-1].At, re.Items[i].At
		if a != nil && b != nil && a.Before(*b) {
			t.Error("recent evidence not sorted newest-first")
		}
	}
	if re.Items[0].Target.Key == "" {
		t.Error("evidence target must carry a canonical key")
	}
	// Attention on the overview is an explicit preview bounded to the top items.
	if ov.Attention.Count > overviewAttentionLimit || len(ov.Attention.Items) > overviewAttentionLimit {
		t.Errorf("overview attention not bounded: %d", ov.Attention.Count)
	}
}

func TestOverview_EmptyFleetHasNoEntryPoints(t *testing.T) {
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	ov := NewQuery(snap).Overview()
	if len(ov.EntryPoints) != 0 {
		t.Errorf("empty fleet must have no entry points, got %+v", ov.EntryPoints)
	}
	if ov.Summary.Services != 0 {
		t.Errorf("empty fleet services = %d", ov.Summary.Services)
	}
}

func TestAttention_AllAndOrdering(t *testing.T) {
	q := productFleet(t)
	list, err := q.Attention(AttentionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if list.Meta.SchemaVersion != ProductSchemaVersion {
		t.Errorf("schema version = %q", list.Meta.SchemaVersion)
	}
	if list.Total != 7 {
		t.Fatalf("total attention = %d, want 7 (items: %+v)", list.Total, codesOf(list.Items))
	}
	// Errors lead, info trails.
	if list.Items[0].Severity != severityError {
		t.Errorf("first item severity = %q, want error", list.Items[0].Severity)
	}
	if last := list.Items[len(list.Items)-1]; last.Severity != severityInfo {
		t.Errorf("last item severity = %q, want info", last.Severity)
	}
	// Every item references a real entity by canonical key and recommends a step.
	for _, it := range list.Items {
		if it.Entity.Key == "" || it.NextStep == "" {
			t.Errorf("attention item %q not fully navigable: %+v", it.Code, it)
		}
	}
}

func TestAttention_Filters(t *testing.T) {
	q := productFleet(t)
	cases := []struct {
		name   string
		filter AttentionFilter
		want   int
	}{
		{"category non-compliant", AttentionFilter{Category: categoryNonCompliant}, 1},
		{"category stale", AttentionFilter{Category: categoryStale}, 2},
		{"kind revision", AttentionFilter{Kind: string(KindRevision)}, 2}, // invalid + missing readiness
		{"kind target", AttentionFilter{Kind: string(KindTarget)}, 4},
		{"severity error", AttentionFilter{Severity: severityError}, 2},
		{"severity info", AttentionFilter{Severity: severityInfo}, 1},
		{"service beta", AttentionFilter{Service: "beta"}, 2},
		{"stale only", AttentionFilter{StaleOnly: true}, 2},
		{"owner team-a", AttentionFilter{Owner: "team-a"}, 2},                   // alpha: unresolved dep + stale alpha-ancient
		{"status NonCompliant", AttentionFilter{Status: StatusNonCompliant}, 2}, // beta-app is non-compliant AND stale
		{"key miss", AttentionFilter{Key: "does-not-exist"}, 0},
		{"source local", AttentionFilter{Source: "local"}, 6}, // all but the unresolved-dep item (no source)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			list, err := q.Attention(c.filter)
			if err != nil {
				t.Fatal(err)
			}
			if got := list.Total; got != c.want {
				t.Errorf("filter %+v: total = %d, want %d", c.filter, got, c.want)
			}
		})
	}
}

func TestAttention_LimitBounds(t *testing.T) {
	q := productFleet(t)
	list, err := q.Attention(AttentionFilter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if list.Count != 2 || len(list.Items) != 2 {
		t.Errorf("limit not applied: count=%d", list.Count)
	}
	if !list.Truncated {
		t.Error("a limited attention page over its total must report truncation")
	}
	if list.Total != 7 {
		t.Errorf("total must reflect pre-limit size, got %d", list.Total)
	}
}

func TestOverview_EvidenceTruncation(t *testing.T) {
	fresh := ptrTime(fixedNow().Add(-time.Minute))
	var revs []RawRevision
	var tgts []RawTarget
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("svc%02d", i)
		revs = append(revs, RawRevision{Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: "t"}}, Readiness: readyContract()}, FS: fstest.MapFS{}}, Digest: "sha256:" + name})
		tgts = append(tgts, RawTarget{Scope: "prod", Kind: "k8s", Name: name + "-app", Service: name, Digest: "sha256:" + name, Compliance: StatusCompliant, EvidenceAt: fresh})
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: time.Hour}, NewMemorySource("local", "local", &Collection{Revisions: revs, Targets: tgts}))
	if err != nil {
		t.Fatal(err)
	}
	ov := NewQuery(snap).Overview()
	if ov.Summary.RecentEvidence != 12 {
		t.Errorf("recent-evidence count = %d, want 12 (count is not truncated)", ov.Summary.RecentEvidence)
	}
	// The recent-evidence preview reports the TRUE total (12) while carrying only the
	// bounded top items and reporting truncation, so a consumer can tell 10-of-12
	// from 10-of-10 (requirement, item 12).
	re := ov.RecentEvidence
	if re.Total != 12 || re.Count != overviewEvidenceLimit || len(re.Items) != overviewEvidenceLimit || !re.Truncated {
		t.Errorf("recent-evidence preview = {total:%d count:%d items:%d trunc:%v}, want total=12 count=%d truncated", re.Total, re.Count, len(re.Items), re.Truncated, overviewEvidenceLimit)
	}
}

func codesOf(items []AttentionItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Code
	}
	return out
}
