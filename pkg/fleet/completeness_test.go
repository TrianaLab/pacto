package fleet

import (
	"reflect"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// knowledgeCounts is the comparable part of a KnowledgeTally: the gap-kind
// partition, without the contract completeness figure.
type knowledgeCounts struct {
	Services, Described, WithGaps, Unknown int
	Gaps                                   KnowledgeGaps
}

func knowledgeCountsOf(k KnowledgeTally) knowledgeCounts {
	return knowledgeCounts{k.Services, k.Described, k.WithGaps, k.Unknown, k.Gaps}
}

func completenessOf(t *testing.T, q *Query, name string) *ServiceCompleteness {
	t.Helper()
	for _, s := range q.snap.Services {
		if s.Name == name {
			return q.serviceCompleteness(s)
		}
	}
	t.Fatalf("no service %s", name)
	return nil
}

func sectionOf(t *testing.T, sc *ServiceCompleteness, name string) CompletenessSection {
	t.Helper()
	for _, s := range sc.Sections {
		if s.Section == name {
			return s
		}
	}
	t.Fatalf("no section %s in %+v", name, sc.Sections)
	return CompletenessSection{}
}

var unreadableRev = func(t *testing.T) RawRevision {
	return RawRevision{Bundle: &contract.Bundle{Contract: mustParse(t, invalidYAML), RawYAML: []byte(invalidYAML), FS: fstest.MapFS{}}}
}

func TestKnowledgeGapsMapIntoChecklist(t *testing.T) {
	noTargetsPartial := NewMemorySource("k8s", SourceKindKubernetes, &Collection{State: &SourceState{Status: SourcePartial}})
	conflicting := func() []RawTarget {
		a, b := runs("pay"), runs("pay")
		b.Name, a.Summary, b.Summary = "pay-2", "Takes payments", "Moves money"
		return []RawTarget{a, b}
	}
	cases := []struct {
		code    string
		mut     func(*contract.Contract, *contract.Bundle)
		targets []RawTarget
		extra   []Source
		state   string // the section's state, "" when the code adds a note
	}{
		{GapNoSummary, func(c *contract.Contract, _ *contract.Bundle) { c.Service.Summary = "" }, []RawTarget{runs("pay")}, nil, contract.CompletenessMissing},
		{GapNoDomain, func(c *contract.Contract, _ *contract.Bundle) { c.Service.Domain = "" }, []RawTarget{runs("pay")}, nil, contract.CompletenessMissing},
		{GapNoInterfaceDocs, func(c *contract.Contract, _ *contract.Bundle) {
			c.Interfaces = []contract.Interface{{Name: "events", Type: contract.InterfaceTypeAsyncAPI}}
		}, []RawTarget{runs("pay")}, nil, contract.CompletenessMissing},
		{GapNoReadiness, func(c *contract.Contract, _ *contract.Bundle) { c.Readiness = nil }, []RawTarget{runs("pay")}, nil, contract.CompletenessMissing},
		{GapNotDeployed, nil, nil, []Source{emptyTargetSource()}, ""},
		{GapUnverifiedCapability, func(c *contract.Contract, _ *contract.Bundle) {
			c.Capabilities = []contract.Capability{{Type: contract.CapabilityExtension, Ref: "acme.io/backup"}}
		}, []RawTarget{runs("pay")}, nil, ""},
		{causeInterfaceDocsUnread, func(c *contract.Contract, b *contract.Bundle) {
			c.Interfaces = []contract.Interface{{Name: "http", Type: contract.InterfaceTypeOpenAPI, Ref: "interfaces/openapi.json"}}
			b.FS = nil
		}, []RawTarget{runs("pay")}, nil, contract.CompletenessUnknown},
		{causeSummaryUnknown, func(c *contract.Contract, _ *contract.Bundle) { c.Service.Summary = "" }, conflicting(), nil, contract.CompletenessUnknown},
		{causePlacementUnknown, nil, nil, []Source{noTargetsPartial}, ""},
	}
	covered := map[string]bool{causeDomainUnknown: true} // its own case below
	for _, tc := range cases {
		covered[tc.code] = true
		t.Run(tc.code, func(t *testing.T) {
			q := knowledgeFleet(t, []RawRevision{knowRev("pay", tc.mut)}, tc.targets, tc.extra...)
			checkGapLands(t, q, tc.code, tc.state)
		})
	}
	t.Run(causeDomainUnknown, func(t *testing.T) {
		q := knowledgeFleet(t, []RawRevision{knowRev("pay", nil)}, []RawTarget{runs("pay")})
		for _, s := range q.snap.Services {
			s.BusinessDomainUnknown = true // sources disagree on the business domain
		}
		checkGapLands(t, q, causeDomainUnknown, contract.CompletenessUnknown)
	})
	t.Run(causeContractUnreadable, func(t *testing.T) {
		q := knowledgeFleet(t, []RawRevision{unreadableRev(t)}, nil)
		for _, s := range q.snap.Services {
			for _, sec := range q.serviceCompleteness(s).Sections {
				if sec.State != contract.CompletenessUnknown {
					t.Errorf("%s: %+v", s.Name, sec)
				}
			}
		}
	})
	for code := range gapSection {
		if !covered[code] {
			t.Errorf("gapSection row %s has no case", code)
		}
	}
	if len(gapSection) != len(gapKinds)+len(knowledgeCauses) {
		t.Errorf("gapSection has %d rows, want every gap kind and cause", len(gapSection))
	}
}

// checkGapLands asserts that code, which holds for pay, lands in its section:
// in the section's state, or as its note when state is "".
func checkGapLands(t *testing.T, q *Query, code, state string) {
	t.Helper()
	gaps, cause := knowledgeOf(t, q, "pay")
	if !slices.Contains(gaps, code) && cause != code {
		t.Fatalf("fixture: gaps %v, cause %q, want %s", gaps, cause, code)
	}
	at := gapSection[code]
	sec := sectionOf(t, completenessOf(t, q, "pay"), at.section)
	if state == "" && (sec.Note != at.note || at.note == "") || state != "" && sec.State != state {
		t.Fatalf("%s: section %+v, want state %q or note %q", code, sec, state, at.note)
	}
}

func TestCompletenessSpecUnreadIsUnknown(t *testing.T) {
	q := knowledgeFleet(t, []RawRevision{openapiRev("pay", nil, true)}, []RawTarget{runs("pay")})
	sc := completenessOf(t, q, "pay")
	if s := sectionOf(t, sc, contract.SectionInterfaces); s.State != contract.CompletenessUnknown {
		t.Fatalf("interfaces = %+v", s)
	}
	if sc.Percent == nil || *sc.Percent == 100 {
		t.Fatalf("percent = %v, an unread document is never complete", sc.Percent)
	}
}

func TestCompletenessAttestedOwnerIsInferred(t *testing.T) {
	q := knowledgeFleet(t, []RawRevision{knowRev("pay", func(c *contract.Contract, _ *contract.Bundle) { c.Service.Owner = contract.Owner{} })}, []RawTarget{runs("pay")})
	for _, s := range q.snap.Services {
		s.OwnerProvenance = OwnerProvenanceAttested
	}
	sc := completenessOf(t, q, "pay")
	if s := sectionOf(t, sc, contract.SectionOwner); s.State != contract.CompletenessInferred {
		t.Fatalf("owner = %+v", s)
	}
	if *sc.Percent != 75 {
		t.Fatalf("percent = %d, an attested owner counts in the denominator only", *sc.Percent)
	}
}

func TestCompletenessUnreadableContractAllUnknown(t *testing.T) {
	q := knowledgeFleet(t, []RawRevision{unreadableRev(t)}, nil)
	for _, s := range q.snap.Services {
		if sc := q.serviceCompleteness(s); sc.Percent != nil || len(sc.Sections) != len(contract.SectionNames()) {
			t.Fatalf("%s: %+v", s.Name, sc)
		}
	}
	c := q.Overview().Summary.Knowledge.ContractCompleteness
	if c.Judged != 0 || c.Unknown != 1 || c.PercentComplete != nil || c.FullyDescribed != 0 {
		t.Fatalf("figure = %+v", c)
	}
}

func TestCompletenessUnknownNeverComplete(t *testing.T) {
	a, b := runs("pay"), runs("pay")
	b.Name, a.Summary, b.Summary = "pay-2", "Takes payments", "Moves money"
	q := knowledgeFleet(t, []RawRevision{knowRev("pay", func(c *contract.Contract, _ *contract.Bundle) { c.Service.Summary = "" })}, []RawTarget{a, b})
	sc := completenessOf(t, q, "pay")
	if sectionOf(t, sc, contract.SectionSummary).State != contract.CompletenessUnknown || *sc.Percent == 100 {
		t.Fatalf("got %+v", sc)
	}
	if c := q.Overview().Summary.Knowledge.ContractCompleteness; c.FullyDescribed != 0 || c.Judged != 1 {
		t.Fatalf("figure = %+v", c)
	}
}

func TestCompletenessFleetFigureAndMostMissing(t *testing.T) {
	noSum := func(c *contract.Contract, _ *contract.Bundle) { c.Service.Summary = "" }
	noReady := func(c *contract.Contract, _ *contract.Bundle) { c.Readiness = nil }
	noDom := func(c *contract.Contract, _ *contract.Bundle) { c.Service.Domain = "" }
	both := func(f, g func(*contract.Contract, *contract.Bundle)) func(*contract.Contract, *contract.Bundle) {
		return func(c *contract.Contract, b *contract.Bundle) { f(c, b); g(c, b) }
	}
	q := knowledgeFleet(t, []RawRevision{
		knowRev("pay", nil), knowRev("a", noSum), knowRev("b", noReady), knowRev("c", both(noSum, noReady)), knowRev("d", both(noDom, noReady)),
	}, []RawTarget{runs("pay"), runs("a"), runs("b"), runs("c"), runs("d")})
	c := q.Overview().Summary.Knowledge.ContractCompleteness
	want := []SectionCount{{contract.SectionReadiness, 3}, {contract.SectionSummary, 2}, {contract.SectionDomain, 1}}
	if c.PercentComplete == nil || *c.PercentComplete != 70 || c.Judged != 5 || c.FullyDescribed != 1 || !slices.Equal(c.MostMissing, want) {
		t.Fatalf("figure = %+v (percent %v)", c, c.PercentComplete)
	}
	if st := q.Status(StatusQuery{}); !reflect.DeepEqual(st.ContractCompleteness, c) {
		t.Fatalf("status figure %+v != overview %+v", st.ContractCompleteness, c)
	}
}

func TestCompletenessMostMissingKeepsThree(t *testing.T) {
	var k KnowledgeTally
	k.addCompleteness(&ServiceCompleteness{Percent: new(0), Sections: []CompletenessSection{
		{CompletenessSection: contract.CompletenessSection{Section: contract.SectionOwner, State: contract.CompletenessMissing}},
		{CompletenessSection: contract.CompletenessSection{Section: contract.SectionSummary, State: contract.CompletenessInferred}},
		{CompletenessSection: contract.CompletenessSection{Section: contract.SectionDomain, State: contract.CompletenessMissing}},
		{CompletenessSection: contract.CompletenessSection{Section: contract.SectionReadiness, State: contract.CompletenessMissing}},
		{CompletenessSection: contract.CompletenessSection{Section: contract.SectionRuntime, State: contract.CompletenessUnknown}},
	}})
	k.finish()
	if got := k.ContractCompleteness.MostMissing; len(got) != maxMostMissing || got[0].Section != contract.SectionOwner {
		t.Fatalf("most missing = %+v", got)
	}
}

func TestCompletenessFullyDescribedNeedsEverySection(t *testing.T) {
	noSchema := func(c *contract.Contract, _ *contract.Bundle) {
		c.Configurations = []contract.Configuration{{Name: "default"}}
	}
	q := knowledgeFleet(t, []RawRevision{knowRev("pay", noSchema), knowRev("full", nil), unreadableRev(t)}, []RawTarget{runs("pay"), runs("full")})
	if gaps, cause := knowledgeOf(t, q, "pay"); gaps != nil || cause != "" {
		t.Fatalf("fixture: pay has gaps %v, cause %q", gaps, cause)
	}
	k := q.Overview().Summary.Knowledge
	c := k.ContractCompleteness
	if k.Described != 2 || c.FullyDescribed != 1 || c.Judged+c.Unknown != k.Services || c.Unknown != 1 {
		t.Fatalf("described %d, figure %+v, services %d", k.Described, c, k.Services)
	}
}

func TestKnowledgeTallyFinishOnEveryCaller(t *testing.T) {
	q := knowledgeFleet(t, []RawRevision{
		knowRev("pay", nil), knowRev("a", func(c *contract.Contract, _ *contract.Bundle) { c.Service.Summary = "" }),
	}, []RawTarget{runs("pay"), runs("a")})
	ov := q.Overview().Summary.Knowledge.ContractCompleteness
	owner, err := q.EntityDetail(KindOwner, "team:t")
	if err != nil {
		t.Fatal(err)
	}
	domain, err := q.EntityDetail(KindDomain, "payments")
	if err != nil {
		t.Fatal(err)
	}
	list := entityList(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	if ov.PercentComplete == nil || *ov.PercentComplete != 87 {
		t.Fatalf("overview = %+v", ov)
	}
	for name, got := range map[string]FleetCompleteness{
		"owner": owner.Owner.Summary.Knowledge.ContractCompleteness, "domain": domain.Domain.Summary.Knowledge.ContractCompleteness,
		"list": list.Aggregate.Knowledge.ContractCompleteness,
	} {
		if !reflect.DeepEqual(got, ov) {
			t.Errorf("%s = %+v, overview %+v", name, got, ov)
		}
	}
}

func TestCompletenessRestricted(t *testing.T) {
	k := SourceKindKubernetes
	shop, secret := runs("pay"), runs("secret")
	shop.Kind, secret.Kind, secret.Scope = k, k, "secret"
	q := knowledgeFleet(t, []RawRevision{
		knowRev("pay", nil),
		knowRev("secret", func(c *contract.Contract, _ *contract.Bundle) { c.Service.Summary, c.Readiness = "", nil }),
	}, []RawTarget{shop, secret})
	if c := q.Overview().Summary.Knowledge.ContractCompleteness; c.Judged != 2 || len(c.MostMissing) != 2 {
		t.Fatalf("unrestricted figure = %+v", c)
	}
	r := q.Restrict(anyCluster(func(ns string) bool { return ns != "secret" }), anyClusterKind(nil))
	c := r.Overview().Summary.Knowledge.ContractCompleteness
	if c.Judged != 1 || *c.PercentComplete != 100 || len(c.MostMissing) != 0 || c.FullyDescribed != 1 {
		t.Fatalf("restricted figure = %+v", c)
	}
	if !reflect.DeepEqual(r.Status(StatusQuery{}).ContractCompleteness, c) {
		t.Fatal("restricted status figure differs from the overview")
	}
}

func TestCompletenessGoldenPathsExcluded(t *testing.T) {
	tmpl := knowRev("path", func(c *contract.Contract, _ *contract.Bundle) { c.Service.Template = &contract.Template{} })
	q := knowledgeFleet(t, []RawRevision{knowRev("pay", nil), tmpl}, []RawTarget{runs("pay")})
	if sc := completenessOf(t, q, "path"); sc != nil {
		t.Fatalf("golden path completeness = %+v", sc)
	}
	if c := q.Overview().Summary.Knowledge.ContractCompleteness; c.Judged+c.Unknown != 1 {
		t.Fatalf("figure = %+v", c)
	}
}

func TestCompletenessInferredServicesExcluded(t *testing.T) {
	whole := func() Source { return NewMemorySource("k8s", SourceKindKubernetes, &Collection{}) }
	with := goldenWith(t, true, whole())
	without := goldenWith(t, false, whole())
	sameJSON(t, "completeness", without.Overview().Summary.Knowledge.ContractCompleteness, with.Overview().Summary.Knowledge.ContractCompleteness)
	if sc := with.serviceCompleteness(with.snap.Services[NewServiceKey(cartName)]); sc != nil {
		t.Fatalf("inferred completeness = %+v", sc)
	}
}
