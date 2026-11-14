package fleet

import (
	"context"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/lock"
)

// lcRev is a 2.4 revision of name@version declaring lifecycle l and deps.
func lcRev(name, version string, l *contract.InterfaceLifecycle, deps ...contract.Dependency) RawRevision {
	c := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: name, Version: version, Owner: contract.Owner{Team: "t"}, Lifecycle: l}, Dependencies: deps}
	return RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: "sha256:" + name + version}
}

func pgDep(compat string) contract.Dependency {
	return contract.Dependency{Name: "pg", Required: true, Compatibility: compat}
}

var deprecated16 = &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Successor: "oci://ghcr.io/acme/pg:17.0.0"}

// lifecycleRangeFleet: pg 16.1.0 is deprecated and 17.0.0 declares nothing;
// app16 accepts ^16, app17 accepts ^17 and pinned accepts ^17 but locks 16.1.0.
func lifecycleRangeFleet(t *testing.T, extra ...RawRevision) *Query {
	t.Helper()
	pinned := lcRev("pinned", "1.0.0", nil, pgDep("^17.0.0"))
	pinned.Lock = &lock.Lock{Dependencies: []lock.Entry{{Name: "pg", Digest: "sha256:pg16.1.0", Version: "16.1.0"}}}
	revs := append([]RawRevision{
		lcRev("pg", "16.1.0", deprecated16),
		lcRev("pg", "17.0.0", nil),
		lcRev("app16", "1.0.0", nil, pgDep("^16.0.0")),
		lcRev("app17", "1.0.0", nil, pgDep("^17.0.0")),
		pinned,
	}, extra...)
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", &Collection{
		Revisions: revs,
		Targets:   []RawTarget{{Scope: "secret", Kind: SourceKindKubernetes, Name: "pg", Service: "pg", Compliance: StatusCompliant}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func lifecycleItems(t *testing.T, q *Query) []AttentionItem {
	t.Helper()
	l, err := q.Attention(AttentionFilter{Category: categoryLifecycle, Limit: MaxAttentionLimit})
	if err != nil {
		t.Fatal(err)
	}
	return l.Items
}

func itemFor(items []AttentionItem, service string) *AttentionItem {
	for i := range items {
		if items[i].Service == service {
			return &items[i]
		}
	}
	return nil
}

func TestProviderLifecycleFollowsRange(t *testing.T) {
	q := lifecycleRangeFleet(t)
	if got := lifecycleRel(t, q, "app16", "1.0.0").ProviderLifecycle; got == nil || *got != *deprecated16 {
		t.Errorf("^16 reaches the deprecated 16.1.0, got %+v", got)
	}
	if got := lifecycleRel(t, q, "app17", "1.0.0").ProviderLifecycle; got != nil {
		t.Errorf("^17 reaches 17.0.0, which declares nothing, got %+v", got)
	}
	if got := lifecycleRel(t, q, "pinned", "1.0.0").ProviderLifecycle; got == nil || *got != *deprecated16 {
		t.Errorf("a locked revision wins over the range, got %+v", got)
	}
}

func TestProviderLifecycleOutOfRangeSeesProvider(t *testing.T) {
	q := lifecycleRangeFleet(t, lcRev("old", "1.0.0", nil, pgDep("^9.0.0")))
	if got := lifecycleRel(t, q, "old", "1.0.0").ProviderLifecycle; got != nil {
		t.Errorf("a range reaching nothing falls back to the newest revision (17.0.0, no lifecycle), got %+v", got)
	}
}

// pinned locks the deprecated 16.1.0 and its ^17 range accepts the successor,
// so the deprecation warning stands (app16's ^16 excludes it: see
// TestLifecycleAttentionSuccessorOutOfRange).
func TestLifecycleAttentionDeprecatedWarning(t *testing.T) {
	items := lifecycleItems(t, lifecycleRangeFleet(t))
	it := itemFor(items, "pinned")
	if it == nil {
		t.Fatalf("no item for pinned: %+v", items)
	}
	want := AttentionItem{
		Entity: it.Entity, Service: "pinned", Label: "pinned uses pg", Severity: severityWarning,
		Code: "DEPENDENCY_SERVICE_DEPRECATED", Category: categoryLifecycle, Summary: "Uses a deprecated provider",
		Reason:   "pg@16.1.0 is deprecated since 2026-01-01; successor oci://ghcr.io/acme/pg:17.0.0",
		NextStep: "Move pinned to oci://ghcr.io/acme/pg:17.0.0",
	}
	if *it != want || it.Entity.Kind != KindService || it.Entity.Key != "pinned" {
		t.Errorf("item = %+v\nwant %+v", *it, want)
	}
	if itemFor(items, "app17") != nil {
		t.Error("app17 reaches a supported revision and has no item")
	}
}

func TestLifecycleAttentionSunsetError(t *testing.T) {
	q := buildDomainSnap(t, "local", &Collection{Revisions: []RawRevision{
		lcRev("pg", "1.0.0", &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Sunset: "2026-06-30"}),
		lcRev("redis", "1.0.0", &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Sunset: "2027-06-30"}),
		lcRev("app", "1.0.0", nil, pgDep("")),
		lcRev("cache", "1.0.0", nil, contract.Dependency{Name: "redis", Required: true}),
	}})
	items := lifecycleItems(t, q)
	app := itemFor(items, "app")
	if app == nil || app.Severity != severityError || app.Code != "DEPENDENCY_SERVICE_SUNSET" || app.Summary != "Uses a provider past its sunset" ||
		app.Reason != "pg@1.0.0 is deprecated since 2026-01-01 and reached its sunset 2026-06-30: it may be retired in any release" ||
		app.NextStep != "Move app to a supported version of pg" {
		t.Errorf("sunset item = %+v", app)
	}
	cache := itemFor(items, "cache")
	if cache == nil || cache.Severity != severityWarning || cache.Reason != "redis@1.0.0 is deprecated since 2026-01-01, sunset 2027-06-30" {
		t.Errorf("future sunset item = %+v", cache)
	}
}

// refFleet: cfg is deprecated; app references it by config, pol references
// it as a policy, both depends on and references cfg.
func refFleet(t *testing.T) *Query {
	t.Helper()
	ref := "oci://x/cfg@" + refDigest("cfg")
	withRefs := func(name string, configs []contract.Configuration, policies []contract.Policy, deps ...contract.Dependency) RawRevision {
		r := lcRev(name, "1.0.0", nil, deps...)
		r.Bundle.Contract.Configurations, r.Bundle.Contract.Policies = configs, policies
		return r
	}
	cfg := lcRev("cfg", "1.0.0", &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Sunset: "2026-06-30"})
	cfg.Digest = refDigest("cfg")
	return buildDomainSnap(t, "local", &Collection{Revisions: []RawRevision{
		cfg,
		withRefs("app", []contract.Configuration{{Name: "cfg", Ref: ref}}, nil),
		withRefs("pol", nil, []contract.Policy{{Name: "cfg", Ref: ref}}),
		withRefs("both", []contract.Configuration{{Name: "cfg", Ref: ref}}, nil, contract.Dependency{Name: "cfg", Required: true}),
	}})
}

func TestLifecycleAttentionConfigRef(t *testing.T) {
	if it := itemFor(lifecycleItems(t, refFleet(t)), "app"); it == nil || it.Label != "app uses cfg" || it.Severity != severityError {
		t.Errorf("config ref item = %+v", it)
	}
}

func TestLifecycleAttentionPolicyRefIgnored(t *testing.T) {
	if it := itemFor(lifecycleItems(t, refFleet(t)), "pol"); it != nil {
		t.Errorf("a policy reference consumes nothing: %+v", it)
	}
}

func TestLifecycleAttentionOnePerConsumerProvider(t *testing.T) {
	n := 0
	for _, it := range lifecycleItems(t, refFleet(t)) {
		if it.Service == "both" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("both has %d items, want one per consumer and provider", n)
	}
	// The worst wins, and equal severities settle deterministically.
	for _, tc := range []struct{ first, second, want string }{
		{severityWarning, severityError, severityError},
		{severityError, severityWarning, severityError},
	} {
		got := pickWorst(t, tc.first, tc.second)
		if got != tc.want {
			t.Errorf("worst of %s, %s = %s", tc.first, tc.second, got)
		}
	}
}

// pickWorst runs lifecycleAttention over a dependency and a configuration
// reference from app to cfg whose reached revisions grade first and second.
func pickWorst(t *testing.T, first, second string) string {
	t.Helper()
	lc := func(sev string) *contract.InterfaceLifecycle {
		if sev == severityError {
			return &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Sunset: "2026-06-30"}
		}
		return &contract.InterfaceLifecycle{Deprecated: "2026-01-01"}
	}
	q := refFleet(t)
	for i := range q.snap.Relationships {
		rel := &q.snap.Relationships[i]
		if rel.FromService == "both" {
			rel.ProviderLifecycle = lc(map[string]string{RelationshipDependency: first, RelationshipConfigRef: second}[rel.Type])
		}
	}
	return itemFor(q.lifecycleAttention(), "both").Severity
}

func TestLifecycleAttentionNewestConsumerRevisionSpeaks(t *testing.T) {
	// app 2.0.0 moves to ^17: its newest revision reaches no deprecation.
	q := lifecycleRangeFleet(t, lcRev("app16", "2.0.0", nil, pgDep("^17.0.0")))
	if it := itemFor(lifecycleItems(t, q), "app16"); it != nil {
		t.Errorf("an older consumer revision raised an item: %+v", it)
	}
}

func TestLifecycleAttentionHiddenProviderSilent(t *testing.T) {
	r := lifecycleRangeFleet(t).Restrict(func(ns string) bool { return ns != "secret" })
	if items := lifecycleItems(t, r); len(items) != 0 {
		t.Errorf("a hidden provider raised lifecycle items: %+v", items)
	}
	if r.Overview().Summary.LifecycleConsumers != 0 {
		t.Error("the hidden provider's consumers reach the tile")
	}
}

func TestLifecycleAttentionGoldenPathSkipped(t *testing.T) {
	tmpl := lcRev("starter", "1.0.0", nil, pgDep("^16.0.0"))
	tmpl.Bundle.Contract.Service.Template = &contract.Template{}
	q := lifecycleRangeFleet(t, tmpl)
	if it := itemFor(lifecycleItems(t, q), "starter"); it != nil {
		t.Errorf("a golden path raised a lifecycle item: %+v", it)
	}
	if n, _ := q.lifecycleTally(); n != 2 {
		t.Errorf("tally = %d, want app16 and pinned only", n)
	}
}

func TestLifecycleTileEqualsList(t *testing.T) {
	for _, q := range []*Query{lifecycleRangeFleet(t), refFleet(t)} {
		items := lifecycleItems(t, q)
		services := map[string]bool{}
		worst := severityWarning
		for _, it := range items {
			services[it.Service] = true
			if it.Severity == severityError {
				worst = severityError
			}
		}
		ov := q.Overview()
		var tile *EntryPoint
		for i := range ov.EntryPoints {
			if ov.EntryPoints[i].Category == categoryLifecycle {
				tile = &ov.EntryPoints[i]
			}
		}
		if tile == nil || tile.Count != len(services) || tile.Count != ov.Summary.LifecycleConsumers || tile.Severity != worst {
			t.Errorf("tile = %+v, list has %d services, worst %s", tile, len(services), worst)
		}
		if tile.Label != "Services using a deprecated provider" || tile.View != EntryPointAttention {
			t.Errorf("tile = %+v", tile)
		}
	}
}

func TestServicesNeedingAttentionCountsLifecycle(t *testing.T) {
	q := lifecycleRangeFleet(t)
	if got := q.servicesNeedingAttention(); got < 2 {
		t.Errorf("servicesNeedingAttention = %d, want app16 and pinned at least", got)
	}
}

func TestStatusLifecycleCategory(t *testing.T) {
	q := lifecycleRangeFleet(t)
	res := q.Status(StatusQuery{Lifecycle: true})
	if res.Total != 2 {
		t.Fatalf("status lifecycle total = %d, want 2: %+v", res.Total, res.Items)
	}
	codes := map[string]string{}
	for _, it := range res.Items {
		codes[it.Name] = it.Code
	}
	if codes["pinned"] != "DEPENDENCY_SERVICE_DEPRECATED" || codes["app16"] != CodeSuccessorOutOfRange {
		t.Errorf("status items = %+v", res.Items)
	}
	if res := q.Status(StatusQuery{Unowned: true}); slices.ContainsFunc(res.Items, func(it StatusItem) bool { return it.Code == "DEPENDENCY_SERVICE_DEPRECATED" }) {
		t.Error("another category reports lifecycle items")
	}
}

func TestAttentionCategoryOrderHasLifecycle(t *testing.T) {
	order := AttentionCategoriesList()
	i := slices.Index(order, categoryLifecycle)
	if i < 1 || order[i-1] != categoryOutOfRange || order[i+1] != categoryUndeclared {
		t.Errorf("lifecycle is not right after out-of-range: %v", order)
	}
	if _, err := lifecycleRangeFleet(t).Attention(AttentionFilter{Category: categoryLifecycle}); err != nil {
		t.Errorf("lifecycle is not a valid filter: %v", err)
	}
}
