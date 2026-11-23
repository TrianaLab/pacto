package fleet

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// knowRev is a revision of name that leaves nothing out (summary, domain,
// readiness), changed by mut. Its digest is sha256:<name>.
func knowRev(name string, mut func(*contract.Contract, *contract.Bundle)) RawRevision {
	c := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: name, Version: "1.0.0", Summary: "Does " + name, Domain: "payments",
		Owner: contract.Owner{Team: "t"}}, Readiness: readyContract()}
	b := &contract.Bundle{Contract: c, FS: fstest.MapFS{}}
	if mut != nil {
		mut(c, b)
	}
	return RawRevision{Bundle: b, Digest: "sha256:" + name}
}

// runs is a target of name that links to knowRev(name) by digest.
func runs(name string) RawTarget {
	return RawTarget{Scope: "prod", Kind: "k8s", Name: name + "-app", Service: name, Digest: "sha256:" + name}
}

// emptyTargetSource is a complete target source that reports no target, so a
// service with no target runs nowhere instead of placement unknown.
func emptyTargetSource() Source {
	return NewMemorySource("ts", SourceKindTargetState, &Collection{})
}

func knowledgeFleet(t *testing.T, revs []RawRevision, targets []RawTarget, extra ...Source) *Query {
	t.Helper()
	src := NewMemorySource("local", "local", &Collection{Revisions: revs, Targets: targets})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, append([]Source{src}, extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func knowledgeOf(t *testing.T, q *Query, name string) ([]string, string) {
	t.Helper()
	for _, s := range q.snap.Services {
		if s.Name == name {
			return q.serviceKnowledge(s)
		}
	}
	t.Fatalf("no service %s", name)
	return nil, ""
}

func TestKnowledgeDescribed(t *testing.T) {
	q := knowledgeFleet(t, []RawRevision{knowRev("pay", nil)}, []RawTarget{runs("pay")})
	if k := q.Overview().Summary.Knowledge; knowledgeCountsOf(k) != (knowledgeCounts{Services: 1, Described: 1}) {
		t.Fatalf("knowledge = %+v", k)
	}
}

func TestKnowledgeGapsPerKind(t *testing.T) {
	noSummary := func(c *contract.Contract, _ *contract.Bundle) { c.Service.Summary = "" }
	noDomain := func(c *contract.Contract, _ *contract.Bundle) { c.Service.Domain = "" }
	cases := []struct {
		name     string
		mut      func(*contract.Contract, *contract.Bundle)
		deployed bool
		want     []string
	}{
		{"no summary", noSummary, true, []string{GapNoSummary}},
		{"no domain", noDomain, true, []string{GapNoDomain}},
		{"interface without document", func(c *contract.Contract, _ *contract.Bundle) {
			c.Interfaces = []contract.Interface{{Name: "events", Type: contract.InterfaceTypeAsyncAPI}}
		}, true, []string{GapNoInterfaceDocs}},
		{"no readiness", func(c *contract.Contract, _ *contract.Bundle) { c.Readiness = nil }, true, []string{GapNoReadiness}},
		{"runs nowhere", nil, false, []string{GapNotDeployed}},
		{"extension capability", func(c *contract.Contract, _ *contract.Bundle) {
			c.Capabilities = []contract.Capability{{Type: contract.CapabilityExtension, Ref: "acme.io/backup"}}
		}, true, []string{GapUnverifiedCapability}},
		{"two kinds", func(c *contract.Contract, b *contract.Bundle) { noSummary(c, b); noDomain(c, b) }, true, []string{GapNoSummary, GapNoDomain}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var targets []RawTarget
			if tc.deployed {
				targets = []RawTarget{runs("pay")}
			}
			q := knowledgeFleet(t, []RawRevision{knowRev("pay", tc.mut)}, targets, emptyTargetSource())
			if gaps, cause := knowledgeOf(t, q, "pay"); !slices.Equal(gaps, tc.want) || cause != "" {
				t.Fatalf("gaps = %v, cause %q, want %v", gaps, cause, tc.want)
			}
			k := q.Overview().Summary.Knowledge
			if k.WithGaps != 1 || k.Services != 1 {
				t.Fatalf("knowledge = %+v", k)
			}
			// Each kind counts the service once; two kinds count it in both.
			var g KnowledgeTally
			g.add(tc.want, "")
			if k.Gaps != g.Gaps {
				t.Errorf("gaps = %+v, want %+v", k.Gaps, g.Gaps)
			}
		})
	}
}

func TestKnowledgePartition(t *testing.T) {
	tmpl := knowRev("path", func(c *contract.Contract, _ *contract.Bundle) { c.Service.Template = &contract.Template{} })
	q := knowledgeFleet(t, []RawRevision{
		knowRev("pay", nil),
		knowRev("orders", func(c *contract.Contract, _ *contract.Bundle) { c.Service.Summary = "" }),
		{Bundle: &contract.Bundle{Contract: mustParse(t, invalidYAML), RawYAML: []byte(invalidYAML), FS: fstest.MapFS{}}},
		tmpl,
	}, []RawTarget{runs("pay"), runs("orders")})
	k := q.Overview().Summary.Knowledge
	if k.Described+k.WithGaps+k.Unknown != k.Services || k.Services != 3 || k.Described != 1 || k.WithGaps != 1 || k.Unknown != 1 {
		t.Fatalf("knowledge = %+v, want golden path left out and a full partition", k)
	}
}

func TestKnowledgeUnreadableIsUnknown(t *testing.T) {
	q := knowledgeFleet(t, []RawRevision{
		{Bundle: &contract.Bundle{Contract: mustParse(t, invalidYAML), RawYAML: []byte(invalidYAML), FS: fstest.MapFS{}}},
	}, nil)
	for _, s := range q.snap.Services {
		if gaps, cause := q.serviceKnowledge(s); cause != causeContractUnreadable || gaps != nil {
			t.Errorf("%s: gaps %v, cause %q", s.Name, gaps, cause)
		}
	}
}

func openapiRev(name string, fsys fstest.MapFS, nilFS bool) RawRevision {
	return knowRev(name, func(c *contract.Contract, b *contract.Bundle) {
		c.Interfaces = []contract.Interface{{Name: "http", Type: contract.InterfaceTypeOpenAPI, Ref: "interfaces/openapi.json"}}
		b.FS = fsys
		if nilFS {
			b.FS = nil
		}
	})
}

func TestKnowledgeBundleUnreadIsUnknown(t *testing.T) {
	q := knowledgeFleet(t, []RawRevision{openapiRev("pay", nil, true)}, []RawTarget{runs("pay")})
	if gaps, cause := knowledgeOf(t, q, "pay"); cause != causeInterfaceDocsUnread || gaps != nil {
		t.Fatalf("gaps %v, cause %q", gaps, cause)
	}
}

func TestKnowledgeUnreadableDocIsUnknown(t *testing.T) {
	corrupt := fstest.MapFS{"interfaces/openapi.json": {Data: []byte("{not json")}}
	q := knowledgeFleet(t, []RawRevision{openapiRev("pay", corrupt, false)}, []RawTarget{runs("pay")})
	if gaps, cause := knowledgeOf(t, q, "pay"); cause != causeInterfaceDocsUnread || slices.Contains(gaps, GapNoInterfaceDocs) {
		t.Fatalf("gaps %v, cause %q", gaps, cause)
	}
	read := fstest.MapFS{"interfaces/openapi.json": {Data: []byte(smallOpenAPI)}}
	q = knowledgeFleet(t, []RawRevision{openapiRev("pay", read, false)}, []RawTarget{runs("pay")})
	if gaps, cause := knowledgeOf(t, q, "pay"); cause != "" || gaps != nil {
		t.Fatalf("a read document: gaps %v, cause %q", gaps, cause)
	}
}

func TestKnowledgeInterfaceWithoutRefIsGap(t *testing.T) {
	q := knowledgeFleet(t, []RawRevision{knowRev("pay", func(c *contract.Contract, _ *contract.Bundle) {
		c.Interfaces = []contract.Interface{{Name: "http", Type: contract.InterfaceTypeOpenAPI}}
	})}, []RawTarget{runs("pay")})
	if gaps, cause := knowledgeOf(t, q, "pay"); cause != "" || !slices.Equal(gaps, []string{GapNoInterfaceDocs}) {
		t.Fatalf("gaps %v, cause %q", gaps, cause)
	}
}

func TestKnowledgeConflictingSummaryIsNotNoSummary(t *testing.T) {
	a, b := runs("pay"), runs("pay")
	b.Name = "pay-2"
	a.Summary, b.Summary = "Takes payments", "Moves money"
	q := knowledgeFleet(t, []RawRevision{knowRev("pay", func(c *contract.Contract, _ *contract.Bundle) { c.Service.Summary = "" })}, []RawTarget{a, b})
	if gaps, cause := knowledgeOf(t, q, "pay"); cause != causeSummaryUnknown || slices.Contains(gaps, GapNoSummary) {
		t.Fatalf("gaps %v, cause %q", gaps, cause)
	}
}

func TestKnowledgeSpecsReadSurvivesMerge(t *testing.T) {
	read := fstest.MapFS{"interfaces/openapi.json": {Data: []byte(smallOpenAPI)}}
	first := NewMemorySource("bare", "oci", &Collection{Revisions: []RawRevision{openapiRev("pay", nil, true)}})
	q := knowledgeFleet(t, []RawRevision{openapiRev("pay", read, false)}, []RawTarget{runs("pay")})
	if gaps, cause := knowledgeOf(t, q, "pay"); cause != "" || gaps != nil {
		t.Fatalf("read alone: gaps %v, cause %q", gaps, cause)
	}
	// The bare contribution comes first; the one that read the files fills in.
	src := NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{openapiRev("pay", read, false)}, Targets: []RawTarget{runs("pay")}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, first, src)
	if err != nil {
		t.Fatal(err)
	}
	r := snap.Revisions[snap.Services[snap.Targets[NewTargetKey("prod", "k8s", "pay-app")].ServiceKey].Revisions[0]]
	if !slices.Equal(r.SpecsRead, []string{"http"}) {
		t.Fatalf("specsRead = %v after the merge", r.SpecsRead)
	}
}

func TestPartialTargetSourceNotDeployedIsUnknown(t *testing.T) {
	partial := NewMemorySource("k8s", SourceKindKubernetes, &Collection{State: &SourceState{Status: SourcePartial}})
	q := knowledgeFleet(t, []RawRevision{knowRev("pay", nil)}, nil, partial)
	if gaps, cause := knowledgeOf(t, q, "pay"); cause != causePlacementUnknown || gaps != nil {
		t.Fatalf("gaps %v, cause %q", gaps, cause)
	}
	if got := entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Gap: GapNotDeployed}); len(got) != 0 {
		t.Errorf("not-deployed = %v under a partial target source", got)
	}
}

func TestRestrictedServiceWithHiddenTargetsIsNotNotDeployed(t *testing.T) {
	shop := knowRev("shop", func(c *contract.Contract, _ *contract.Bundle) { c.Service.Offering = "storage" })
	q := knowledgeFleet(t, []RawRevision{shop}, nil, emptyTargetSource())
	if gaps, _ := knowledgeOf(t, q, "shop"); !slices.Equal(gaps, []string{GapNotDeployed}) {
		t.Fatalf("unrestricted gaps = %v", gaps)
	}
	r := q.Restrict(anyCluster(func(ns string) bool { return ns != "" }), anyClusterKind(nil))
	if r == q {
		t.Fatal("the cluster-scope restriction hid nothing")
	}
	if gaps, cause := knowledgeOf(t, r, "shop"); cause != causePlacementUnknown || gaps != nil {
		t.Fatalf("restricted: gaps %v, cause %q", gaps, cause)
	}
}

func TestKnowledgeDomainUnknownIsNotNoDomain(t *testing.T) {
	q := buildDomainSnap(t, SourceKindKubernetes, &Collection{Targets: []RawTarget{{Scope: "s", Kind: "k8s", Name: "old", Service: "old"}}})
	s := q.snap.Services[NewServiceKeyDomain("", "old")]
	if !s.BusinessDomainUnknown {
		t.Fatal("fixture: domain must be unknown")
	}
	if gaps, cause := q.serviceKnowledge(s); slices.Contains(gaps, GapNoDomain) || cause == "" {
		t.Fatalf("gaps %v, cause %q", gaps, cause)
	}
}

func TestKnowledgeNoReadinessRaisesNoItem(t *testing.T) {
	q := knowledgeFleet(t, []RawRevision{knowRev("pay", func(c *contract.Contract, _ *contract.Bundle) { c.Readiness = nil })}, []RawTarget{runs("pay")})
	if k := q.Overview().Summary; k.Knowledge.Gaps.NoReadiness != 1 || k.KnowledgeItems != 0 {
		t.Fatalf("knowledge = %+v, items %d", k.Knowledge, k.KnowledgeItems)
	}
}

func TestKnowledgeTileMatchesList(t *testing.T) {
	q := knowledgeFleet(t, []RawRevision{
		knowRev("pay", func(c *contract.Contract, _ *contract.Bundle) { c.Service.Summary, c.Service.Domain = "", "" }),
		knowRev("idle", nil),
		{Bundle: &contract.Bundle{Contract: mustParse(t, invalidYAML), RawYAML: []byte(invalidYAML), FS: fstest.MapFS{}}},
	}, []RawTarget{runs("pay")})
	ov := q.Overview()
	al := attentionOf(t, q, AttentionFilter{Category: categoryKnowledge})
	var tile *EntryPoint
	for i := range ov.EntryPoints {
		if ov.EntryPoints[i].Category == categoryKnowledge {
			tile = &ov.EntryPoints[i]
		}
	}
	if tile == nil || tile.Label != "Gaps in what the contracts say" || tile.Severity != severityInfo || tile.Count != al.Total || al.Total != 4 {
		t.Fatalf("tile %+v, list total %d", tile, al.Total)
	}
	codes := map[string]bool{}
	for _, it := range al.Items {
		codes[it.Code] = true
	}
	for _, c := range []string{"NO_SUMMARY", "NO_DOMAIN", "NOT_DEPLOYED", causeContractUnreadable} {
		if !codes[c] {
			t.Errorf("list misses %s: %v", c, codes)
		}
	}
}

func TestStatusIncludesKnowledge(t *testing.T) {
	q := knowledgeFleet(t, []RawRevision{knowRev("idle", nil)}, nil, emptyTargetSource())
	l := attentionOf(t, q, AttentionFilter{})
	st := q.Status(StatusQuery{Limit: 1000})
	if st.Total != l.Total || !slices.ContainsFunc(st.Items, func(it StatusItem) bool { return it.Code == "NOT_DEPLOYED" }) {
		t.Fatalf("status %+v, attention %d", st.Items, l.Total)
	}
}

func TestEvaluationCoverage(t *testing.T) {
	a, b, c := runs("pay"), runs("pay"), runs("pay")
	b.Name, c.Name = "pay-2", "pay-3"
	a.Coverage, b.Coverage = &Coverage{Evaluated: 2, Required: 3}, &Coverage{Evaluated: 0, Required: 4}
	q := knowledgeFleet(t, []RawRevision{knowRev("pay", nil)}, []RawTarget{a, b, c})
	if got := q.Overview().Summary.Coverage; got != (EvaluationCoverage{Evaluated: 2, Required: 7, Reporting: 2, NotReporting: 1}) {
		t.Fatalf("coverage = %+v", got)
	}
}

func TestEntityFilterGap(t *testing.T) {
	q := knowledgeFleet(t, []RawRevision{
		knowRev("pay", nil),
		knowRev("nosum", func(c *contract.Contract, _ *contract.Bundle) { c.Service.Summary = "" }),
		knowRev("nodom", func(c *contract.Contract, _ *contract.Bundle) { c.Service.Domain = "" }),
		knowRev("ifc", func(c *contract.Contract, _ *contract.Bundle) {
			c.Interfaces = []contract.Interface{{Name: "e", Type: contract.InterfaceTypeGRPC}}
		}),
		knowRev("noready", func(c *contract.Contract, _ *contract.Bundle) { c.Readiness = nil }),
		knowRev("idle", nil),
		knowRev("ext", func(c *contract.Contract, _ *contract.Bundle) {
			c.Capabilities = []contract.Capability{{Type: contract.CapabilityExtension, Ref: "acme.io/x"}}
		}),
		{Bundle: &contract.Bundle{Contract: mustParse(t, invalidYAML), RawYAML: []byte(invalidYAML), FS: fstest.MapFS{}}},
		knowRev("path", func(c *contract.Contract, _ *contract.Bundle) { c.Service.Template = &contract.Template{} }),
	}, []RawTarget{runs("pay"), runs("nosum"), runs("nodom"), runs("ifc"), runs("noready"), runs("ext")})
	want := map[string]string{
		GapNoSummary: "nosum", GapNoDomain: "nodom", GapNoInterfaceDocs: "ifc", GapNoReadiness: "noready",
		GapNotDeployed: "idle", GapUnverifiedCapability: "ext",
	}
	for gap, name := range want {
		l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}, Gap: gap})
		if err != nil || l.Total != 1 || l.Entities[0].Label != name {
			t.Errorf("gap %s = %+v, %v; want %s", gap, l, err, name)
			continue
		}
		if l.Aggregate.Knowledge.WithGaps != 1 {
			t.Errorf("gap %s aggregate = %+v", gap, l.Aggregate.Knowledge)
		}
	}
	if l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}, Gap: GapUnknown}); err != nil || l.Total != 1 || l.Aggregate.Knowledge.Unknown != 1 {
		t.Errorf("gap unknown = %+v, %v", l, err)
	}
	var iq *InvalidQueryError
	if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}, Gap: "bogus"}); !errors.As(err, &iq) {
		t.Errorf("an unknown gap = %v, want InvalidQueryError", err)
	}
	if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindTarget}, Gap: GapNoSummary}); !errors.As(err, &iq) {
		t.Errorf("gap on targets = %v, want InvalidQueryError", err)
	}
	if q.matchGap(EntityRef{Kind: KindTarget}, GapNoSummary) {
		t.Error("a target matched a gap")
	}
}

// ownerBucketFleet: owned, ownerless (read, no owner) and unread (an invalid
// contract that claims no owner either).
func ownerBucketFleet(t *testing.T) *Query {
	bad := mustParse(t, invalidYAML)
	bad.Service.Owner = contract.Owner{}
	return knowledgeFleet(t, []RawRevision{
		knowRev("owned", nil),
		knowRev("ownerless", func(c *contract.Contract, _ *contract.Bundle) { c.Service.Owner = contract.Owner{} }),
		{Bundle: &contract.Bundle{Contract: bad, RawYAML: []byte(invalidYAML), FS: fstest.MapFS{}}},
	}, nil)
}

func TestOwnershipUnknownNotUnowned(t *testing.T) {
	o := ownerBucketFleet(t).Overview().Summary.Ownership
	if o != (OwnershipTally{Consistent: 1, Unowned: 1, Unknown: 1}) {
		t.Fatalf("ownership = %+v", o)
	}
}

func TestOwnershipPartitionHasUnknown(t *testing.T) {
	sum := ownerBucketFleet(t).Overview().Summary
	if sum.Ownership.Total() != sum.Services {
		t.Fatalf("ownership %+v does not partition %d services", sum.Ownership, sum.Services)
	}
	var o OwnershipTally
	for _, b := range []string{OwnershipConsistent, OwnershipConflicting, OwnershipUnowned, OwnershipUnknown} {
		o.add(b)
	}
	if o != (OwnershipTally{Consistent: 1, Conflicting: 1, Unowned: 1, Unknown: 1}) {
		t.Errorf("add = %+v", o)
	}
}

func TestOwnershipFilterUnknown(t *testing.T) {
	q := ownerBucketFleet(t)
	unknown := entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Ownership: OwnershipUnknown})
	unowned := entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Ownership: OwnershipUnowned})
	unidentified := entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Ownership: OwnershipUnidentified})
	if len(unknown) != 1 || slices.Contains(unknown, "ownerless") || !slices.Equal(unowned, []string{"ownerless"}) || len(unidentified) != 2 {
		t.Fatalf("unknown %v, unowned %v, unidentified %v", unknown, unowned, unidentified)
	}
}

func TestUnownedTileMatchesList(t *testing.T) {
	q := ownerBucketFleet(t)
	ov := q.Overview()
	al := attentionOf(t, q, AttentionFilter{Category: categoryUnowned})
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}, Ownership: OwnershipUnowned})
	if err != nil {
		t.Fatal(err)
	}
	if ov.Summary.Ownership.Unowned != 1 || al.Total != 1 || l.Total != 1 {
		t.Fatalf("tile %d, attention %d, list %d", ov.Summary.Ownership.Unowned, al.Total, l.Total)
	}
}

func TestUnreadableServiceIsOneKnowledgeItem(t *testing.T) {
	q := ownerBucketFleet(t)
	var unread string
	for _, s := range q.snap.Services {
		if q.ownershipUnknown(s) {
			unread = string(s.Key)
		}
	}
	al := attentionOf(t, q, AttentionFilter{Service: unread, Category: categoryKnowledge})
	if al.Total != 1 || al.Items[0].Code != causeContractUnreadable || al.Items[0].Category != categoryKnowledge ||
		al.Items[0].Summary != "Owner, domain and summary unknown: no contract of this service could be read" {
		t.Fatalf("unreadable service items = %+v", al.Items)
	}
	// A read contract whose document was not read is one item that names
	// neither owner nor domain.
	q = knowledgeFleet(t, []RawRevision{openapiRev("pay", nil, true)}, []RawTarget{runs("pay")})
	al = attentionOf(t, q, AttentionFilter{Category: categoryKnowledge})
	if al.Total != 1 || al.Items[0].Code != causeInterfaceDocsUnread || al.Items[0].Summary != "Interface document unread" {
		t.Fatalf("unread document items = %+v", al.Items)
	}
	if text := strings.ToLower(al.Items[0].Summary + al.Items[0].Reason); strings.Contains(text, "owner") || strings.Contains(text, "domain") {
		t.Errorf("unread document item names owner or domain: %+v", al.Items[0])
	}
}

// A service whose owner a source reports but whose current revision Pacto never
// read is unknown, and its item never claims the owner is unknown.
func TestUnreadCurrentRevisionWithKnownOwner(t *testing.T) {
	src := NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{
		{Scope: "a", Kind: "k8s", Name: "seen", Service: "seen", Owner: contract.Owner{Team: "ops"}, BusinessDomainKnown: true},
	}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	al := attentionOf(t, q, AttentionFilter{Category: categoryKnowledge})
	if al.Total != 1 || al.Items[0].Code != causeContractUnreadable || strings.Contains(al.Items[0].Summary, "Owner") {
		t.Fatalf("items = %+v", al.Items)
	}
}

func TestEntitiesAggregateMatchesOverviewOwnership(t *testing.T) {
	q := ownerBucketFleet(t)
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}})
	if err != nil {
		t.Fatal(err)
	}
	if ov := q.Overview().Summary; l.Aggregate.Ownership != ov.Ownership || l.Aggregate.Ownership.Unknown != 1 || !reflect.DeepEqual(l.Aggregate.Knowledge, ov.Knowledge) {
		t.Fatalf("aggregate %+v / %+v, overview %+v / %+v", l.Aggregate.Ownership, l.Aggregate.Knowledge, ov.Ownership, ov.Knowledge)
	}
}

// Every gap kind is a filter value, and unknown comes last.
func TestGapFilterValues(t *testing.T) {
	got := GapFilterValues()
	if want := append(slices.Clone(gapKinds), GapUnknown); !slices.Equal(got, want) {
		t.Errorf("GapFilterValues() = %v, want %v", got, want)
	}
	for _, v := range got {
		if !validGap(v) {
			t.Errorf("%q is a filter value the filter rejects", v)
		}
	}
}

// TestKnowledgeConflictingDomainIsNotDescribed: a readable revision declaring
// no domain whose targets report different domains is Unknown, never Described.
func TestKnowledgeConflictingDomainIsNotDescribed(t *testing.T) {
	a, b := runs("pay"), runs("pay")
	b.Name = "pay-2"
	a.BusinessDomain, b.BusinessDomain = "payments", "commerce"
	q := knowledgeFleet(t, []RawRevision{knowRev("pay", func(c *contract.Contract, _ *contract.Bundle) { c.Service.Domain = "" })}, []RawTarget{a, b})
	if gaps, cause := knowledgeOf(t, q, "pay"); cause != causeDomainUnknown || gaps != nil {
		t.Fatalf("gaps %v, cause %q", gaps, cause)
	}
	if k := q.Overview().Summary.Knowledge; k.Described != 0 || k.Unknown != 1 {
		t.Fatalf("knowledge = %+v", k)
	}
}

// TestNoTargetSourceNotDeployedIsUnknown: with no source that reports targets,
// Pacto looked at no runtime, so a service with no target is placement unknown.
func TestNoTargetSourceNotDeployedIsUnknown(t *testing.T) {
	q := knowledgeFleet(t, []RawRevision{knowRev("pay", nil)}, nil)
	if gaps, cause := knowledgeOf(t, q, "pay"); cause != causePlacementUnknown || gaps != nil {
		t.Fatalf("gaps %v, cause %q", gaps, cause)
	}
	q = knowledgeFleet(t, []RawRevision{knowRev("pay", nil)}, nil, emptyTargetSource())
	if gaps, cause := knowledgeOf(t, q, "pay"); cause != "" || !slices.Equal(gaps, []string{GapNotDeployed}) {
		t.Fatalf("with a complete target source: gaps %v, cause %q", gaps, cause)
	}
}

// TestKnowledgeToolCapKeepsSpecsRead: an interface after one that fills the
// tool cap is still recorded as read.
func TestKnowledgeToolCapKeepsSpecsRead(t *testing.T) {
	var paths []string
	for i := range maxToolsPerRevision + 1 {
		paths = append(paths, fmt.Sprintf(`"/p%d": {"get": {"operationId": "op%d", "responses": {"200": {"description": "ok"}}}}`, i, i))
	}
	big := `{"openapi": "3.1.0", "info": {"title": "Big", "version": "1.0.0"}, "paths": {` + strings.Join(paths, ",") + `}}`
	fsys := fstest.MapFS{"big.json": {Data: []byte(big)}, "small.json": {Data: []byte(smallOpenAPI)}}
	rev := knowRev("pay", func(c *contract.Contract, b *contract.Bundle) {
		c.Interfaces = []contract.Interface{
			{Name: "big", Type: contract.InterfaceTypeOpenAPI, Ref: "big.json"},
			{Name: "small", Type: contract.InterfaceTypeOpenAPI, Ref: "small.json"},
		}
		b.FS = fsys
	})
	q := knowledgeFleet(t, []RawRevision{rev}, []RawTarget{runs("pay")})
	if gaps, cause := knowledgeOf(t, q, "pay"); cause != "" || gaps != nil {
		t.Fatalf("gaps %v, cause %q", gaps, cause)
	}
	r := q.snap.Revisions[q.snap.Services[q.snap.Targets[NewTargetKey("prod", "k8s", "pay-app")].ServiceKey].Revisions[0]]
	if !slices.Equal(r.SpecsRead, []string{"big", "small"}) || len(r.Tools) != maxToolsPerRevision {
		t.Fatalf("specsRead %v, %d tools", r.SpecsRead, len(r.Tools))
	}
}

func TestMostMissingCountsOnlyMissingSections(t *testing.T) {
	sec := func(name string, state string) CompletenessSection {
		return CompletenessSection{CompletenessSection: contract.CompletenessSection{Section: name, State: state}}
	}
	var k KnowledgeTally
	k.addCompleteness(&ServiceCompleteness{Percent: new(50), Sections: []CompletenessSection{
		sec(contract.SectionConfiguration, contract.CompletenessMissing), sec(contract.SectionInterfaces, contract.CompletenessInferred)}})
	k.addCompleteness(&ServiceCompleteness{Percent: new(50), Sections: []CompletenessSection{
		sec(contract.SectionConfiguration, contract.CompletenessInferred)}})
	k.finish()
	want := []SectionCount{{contract.SectionConfiguration, 1}}
	if !reflect.DeepEqual(k.ContractCompleteness.MostMissing, want) {
		t.Errorf("mostMissing %+v, want %+v", k.ContractCompleteness.MostMissing, want)
	}
}
