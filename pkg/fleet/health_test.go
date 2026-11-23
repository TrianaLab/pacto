package fleet

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
)

var healthSubject = finding.SubjectRef{Kind: "capability", Name: contract.CapabilityHealth}

func healthFinding(code finding.Code, msg string) finding.Finding {
	return finding.Finding{Code: code, Severity: finding.SeverityUnknown, Category: finding.CategoryInconclusive, Subject: healthSubject, Message: msg}
}

// healthFleet: "probed" declares the health capability (digest sha256:h) and
// "plain" declares none (sha256:p). Every target runs in prod and links by digest
// unless it names another.
func healthFleet(t *testing.T, window time.Duration, targets ...RawTarget) *Query {
	t.Helper()
	mk := func(name string, caps ...contract.Capability) *contract.Contract {
		return &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
			Readiness: readyContract(), Capabilities: caps}
	}
	for i := range targets {
		if targets[i].Scope == "" {
			targets[i].Scope = "prod"
		}
		if targets[i].Kind == "" {
			targets[i].Kind = "k8s"
		}
	}
	src := NewMemorySource("m", "memory", &Collection{
		Revisions: []RawRevision{
			{Bundle: &contract.Bundle{Contract: mk("probed", contract.Capability{Type: contract.CapabilityHealth}), FS: fstest.MapFS{}}, Digest: "sha256:h"},
			{Bundle: &contract.Bundle{Contract: mk("plain"), FS: fstest.MapFS{}}, Digest: "sha256:p"},
		},
		Targets: targets,
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: window}, src)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

var (
	freshAt   = ptrTime(fixedNow().Add(-time.Minute))
	evaluated = &Coverage{Evaluated: 2, Required: 2}
)

func probed(name string, mut func(*RawTarget)) RawTarget {
	rt := RawTarget{Name: name, Service: "probed", Digest: "sha256:h", Compliance: StatusCompliant, EvidenceAt: freshAt, Coverage: evaluated}
	if mut != nil {
		mut(&rt)
	}
	return rt
}

func onlyTarget(t *testing.T, q *Query) *TargetRecord {
	t.Helper()
	if len(q.snap.Targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(q.snap.Targets))
	}
	for _, tr := range q.snap.Targets {
		return tr
	}
	return nil
}

func TestTargetHealth(t *testing.T) {
	cases := []struct {
		name     string
		window   time.Duration
		target   RawTarget
		state    AvailabilityState
		observed bool
	}{
		{"failing beside an older Family-2 finding", time.Hour, probed("a", func(r *RawTarget) {
			r.Compliance = StatusUnknown
			r.Findings = []finding.Finding{healthFinding(finding.CodeEvidenceInsufficient, "x"), healthFinding(finding.CodeHealthFailing, "down")}
		}), AvailabilityUnavailable, true},
		{"healthy", time.Hour, probed("a", nil), AvailabilityAvailable, true},
		{"stale", time.Hour, probed("a", func(r *RawTarget) { r.EvidenceAt = ptrTime(fixedNow().Add(-2 * time.Hour)) }), AvailabilityUnknown, true},
		{"no evidence", time.Hour, probed("a", func(r *RawTarget) { r.EvidenceAt = nil }), AvailabilityUnknown, true},
		{"undeclared", time.Hour, RawTarget{Name: "p", Service: "plain", Digest: "sha256:p", Compliance: StatusCompliant, EvidenceAt: freshAt}, "", false},
		{"declared without coverage", time.Hour, probed("a", func(r *RawTarget) { r.Coverage = nil }), AvailabilityUnknown, true},
		{"zero window, old evidence", 0, probed("a", func(r *RawTarget) { r.EvidenceAt = ptrTime(fixedNow().Add(-RecentEvidenceWindow - time.Minute)) }), AvailabilityUnknown, true},
		{"zero window, fresh evidence", 0, probed("a", nil), AvailabilityAvailable, true},
		{"absent capability", time.Hour, probed("a", func(r *RawTarget) {
			r.Compliance = StatusNonCompliant
			r.Findings = []finding.Finding{{Code: finding.CodeCapabilityAbsent, Severity: finding.SeverityError, Subject: healthSubject, Message: "absent"}}
		}), AvailabilityUnknown, true},
		{"unresolved revision", time.Hour, probed("a", func(r *RawTarget) { r.Digest = "sha256:nomatch" }), AvailabilityUnknown, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := healthFleet(t, tc.window, tc.target)
			state, observed := q.targetHealth(onlyTarget(t, q))
			if state != tc.state || observed != tc.observed {
				t.Fatalf("targetHealth = (%q, %v), want (%q, %v)", state, observed, tc.state, tc.observed)
			}
		})
	}
}

func TestTargetHealthQuarantinedIsUnknown(t *testing.T) {
	q := healthFleet(t, time.Hour, probed("a", nil))
	tr := onlyTarget(t, q)
	tr.Quarantined = true
	if state, observed := q.targetHealth(tr); state != AvailabilityUnknown || !observed {
		t.Fatalf("targetHealth = (%q, %v), want unknown", state, observed)
	}
}

func TestTargetHealthDeclaredNoCoverageIsUnknown(t *testing.T) {
	q := healthFleet(t, time.Hour, probed("a", func(r *RawTarget) { r.Coverage = nil; r.Compliance = StatusUnknown }))
	tr := onlyTarget(t, q)
	if code, reason := q.dominantUnknown(tr); code != string(finding.CodeEvidenceMissing) || reason != healthNotEvaluatedReason {
		t.Fatalf("cause = %q (%q), want EVIDENCE_MISSING with the health reason", code, reason)
	}
	tr.EvidenceAt = nil
	if _, reason := q.dominantUnknown(tr); reason != noEvidenceReason {
		t.Fatalf("no evidence at all: reason %q", reason)
	}
}

// TestTargetHealthInferredLinkIsUnknown: an inferred (mutable tag) link never
// answers healthy or not observed, but a failing health check still reads failing.
func TestTargetHealthInferredLinkIsUnknown(t *testing.T) {
	for _, tc := range []struct {
		target RawTarget
		want   AvailabilityState
	}{
		{probed("a", nil), AvailabilityUnknown},
		{RawTarget{Name: "p", Service: "plain", Digest: "sha256:p", Compliance: StatusCompliant, EvidenceAt: freshAt, Coverage: evaluated}, AvailabilityUnknown},
		{probed("a", func(r *RawTarget) { r.Findings = []finding.Finding{healthFinding(finding.CodeHealthFailing, "down")} }), AvailabilityUnavailable},
	} {
		q := healthFleet(t, time.Hour, tc.target)
		tr := onlyTarget(t, q)
		tr.RevisionMatch = revisionMatchInferred
		if state, observed := q.targetHealth(tr); state != tc.want || !observed {
			t.Errorf("%s: targetHealth = (%q, %v), want (%q, true)", tr.Service, state, observed, tc.want)
		}
	}
}

// fiveHealthTargets: one failing, one healthy, one stale, one undeclared, and
// with extra one more that declares health with no coverage.
func fiveHealthTargets(extra bool) []RawTarget {
	ts := []RawTarget{
		probed("failing", func(r *RawTarget) {
			r.Compliance = StatusUnknown
			r.Findings = []finding.Finding{healthFinding(finding.CodeHealthFailing, `capability "health" answered but reports it is not healthy`)}
		}),
		probed("healthy", nil),
		probed("stale", func(r *RawTarget) { r.EvidenceAt = ptrTime(fixedNow().Add(-2 * time.Hour)) }),
		{Name: "plain", Service: "plain", Digest: "sha256:p", Compliance: StatusCompliant, EvidenceAt: freshAt},
	}
	if extra {
		ts = append(ts, probed("uncovered", func(r *RawTarget) { r.Coverage = nil }))
	}
	return ts
}

func TestOperationalHealthPartitionsTargets(t *testing.T) {
	q := healthFleet(t, time.Hour, fiveHealthTargets(false)...)
	sum := q.Overview().Summary
	want := OperationalHealthTally{Available: 1, Unavailable: 1, Unknown: 1, NotObserved: 1}
	if sum.OperationalHealth != want {
		t.Fatalf("operationalHealth = %+v, want %+v", sum.OperationalHealth, want)
	}
	if sum.UnknownTargets != 1 {
		t.Errorf("unknownTargets = %d, want 1 (only the failing one is Unknown compliance)", sum.UnknownTargets)
	}
	h := sum.OperationalHealth
	if h.Available+h.Degraded+h.Unavailable+h.Unknown+h.NotObserved != sum.Targets {
		t.Errorf("health buckets %+v do not sum to %d targets", h, sum.Targets)
	}

	q = healthFleet(t, time.Hour, fiveHealthTargets(true)...)
	sum = q.Overview().Summary
	if sum.OperationalHealth.Unknown != 2 || sum.OperationalHealth.NotObserved != 1 {
		t.Fatalf("with an uncovered target: %+v, want unknown 2, notObserved 1", sum.OperationalHealth)
	}
	if sum.UnknownTargets != 1 {
		t.Errorf("unknownTargets moved to %d", sum.UnknownTargets)
	}
}

func TestOperationalHealthTallyDegraded(t *testing.T) {
	var h OperationalHealthTally
	h.add(AvailabilityDegraded, true)
	if h.Degraded != 1 {
		t.Fatalf("degraded = %d", h.Degraded)
	}
}

func TestHealthTileMatchesList(t *testing.T) {
	q := healthFleet(t, time.Hour, fiveHealthTargets(true)...)
	ov := q.Overview()
	var tile *EntryPoint
	for i := range ov.EntryPoints {
		if ov.EntryPoints[i].Category == categoryHealth {
			tile = &ov.EntryPoints[i]
		}
	}
	if tile == nil || tile.Label != "Operational targets failing their health check" || tile.Severity != severityWarning {
		t.Fatalf("health tile = %+v", tile)
	}
	l, err := q.Attention(AttentionFilter{Category: categoryHealth})
	if err != nil || l.Total != tile.Count || tile.Count != 1 {
		t.Fatalf("tile %d, list %+v %v", tile.Count, l, err)
	}
	// The tile sits right after the non-compliant one in the candidates order.
	if got := ov.EntryPoints[1].Category; got != categoryHealth {
		t.Errorf("second entry point = %q, want health after the lead tile", got)
	}
}

func TestHealthItemReasonIsTheFinding(t *testing.T) {
	q := healthFleet(t, time.Hour, fiveHealthTargets(false)...)
	l, _ := q.Attention(AttentionFilter{Category: categoryHealth})
	if len(l.Items) != 1 {
		t.Fatalf("items = %+v", l.Items)
	}
	it := l.Items[0]
	if it.Code != string(finding.CodeHealthFailing) || it.Severity != severityWarning || it.Entity.Kind != KindTarget ||
		it.Reason != `capability "health" answered but reports it is not healthy` || it.Source != "m" ||
		it.Summary != "Health check failing on this operational target" || !strings.Contains(it.NextStep, "compliance stays unknown") {
		t.Fatalf("health item = %+v", it)
	}
	// The same target is also an unknown-compliance item, named by its cause.
	u, _ := q.Attention(AttentionFilter{Category: categoryUnknown})
	if len(u.Items) != 1 || u.Items[0].Code != string(finding.CodeHealthFailing) || u.Items[0].Reason != "the health check is failing" {
		t.Fatalf("unknown items = %+v", u.Items)
	}
}

func TestStatusIncludesHealth(t *testing.T) {
	q := healthFleet(t, time.Hour, fiveHealthTargets(false)...)
	l, _ := q.Attention(AttentionFilter{Limit: MaxAttentionLimit})
	st := q.Status(StatusQuery{Limit: 1000})
	if st.Total != l.Total {
		t.Fatalf("status %d items, attention %d", st.Total, l.Total)
	}
	if !slices.ContainsFunc(st.Items, func(it StatusItem) bool {
		return it.Code == string(finding.CodeHealthFailing) && it.Kind == string(KindTarget)
	}) {
		t.Fatalf("status misses the health item: %+v", st.Items)
	}
}

func TestUnknownItemNamesItsCause(t *testing.T) {
	f := func(c finding.Code) finding.Finding {
		return finding.Finding{Code: c, Severity: finding.SeverityUnknown, Subject: finding.SubjectRef{Kind: "interface", Name: "http"}}
	}
	cases := []struct {
		findings     []finding.Finding
		code, reason string
	}{
		{[]finding.Finding{f(finding.CodeEvidenceMissing), f(finding.CodeCollectionFailed)}, "COLLECTION_FAILED", "the collector failed to read interface http"},
		{[]finding.Finding{f(finding.CodeEvidenceStale), healthFinding(finding.CodeHealthFailing, "")}, "HEALTH_FAILING", "the health check is failing"},
		{[]finding.Finding{f(finding.CodeObservationUnsupported), f(finding.CodeEvidenceStale)}, "EVIDENCE_STALE", "the evidence is older than the source's freshness window"},
		{[]finding.Finding{f(finding.CodeEvidenceInsufficient), f(finding.CodeObservationUnsupported)}, "OBSERVATION_UNSUPPORTED", "interface http cannot be observed in this environment"},
		{[]finding.Finding{f(finding.CodeEvidenceMissing), f(finding.CodeEvidenceInsufficient)}, "EVIDENCE_INSUFFICIENT", "the evidence cannot confirm or contradict interface http"},
		{[]finding.Finding{f(finding.CodeEvidenceMissing)}, "EVIDENCE_MISSING", "no evidence was collected for interface http"},
	}
	for _, tc := range cases {
		q := healthFleet(t, time.Hour, RawTarget{Name: "p", Service: "plain", Digest: "sha256:p", Compliance: StatusUnknown, EvidenceAt: freshAt, Coverage: evaluated, Findings: tc.findings})
		l, _ := q.Attention(AttentionFilter{Category: categoryUnknown})
		if len(l.Items) != 1 || l.Items[0].Code != tc.code || l.Items[0].Reason != tc.reason {
			t.Errorf("%s: items = %+v", tc.code, l.Items)
		}
	}
}

func TestUnknownItemWithNoFindings(t *testing.T) {
	for _, tc := range []struct {
		at   *time.Time
		code string
	}{{nil, "EVIDENCE_MISSING"}, {freshAt, "UNKNOWN"}} {
		q := healthFleet(t, time.Hour, RawTarget{Name: "p", Service: "plain", Digest: "sha256:p", Compliance: StatusUnknown, EvidenceAt: tc.at, Coverage: evaluated})
		l, _ := q.Attention(AttentionFilter{Category: categoryUnknown})
		if len(l.Items) != 1 || l.Items[0].Code != tc.code {
			t.Errorf("evidenceAt %v: items = %+v, want %s", tc.at, l.Items, tc.code)
		}
	}
}

func TestAttentionCategoryOrderIsTheAgreedOrder(t *testing.T) {
	want := []string{"non-compliant", "invalid", "cross-environment", "health", "provisioning", "unknown", "stale", "unresolved", "out-of-range",
		"lifecycle", "undeclared", "golden-path", "readiness", "knowledge", "unowned", "cluster"}
	if got := AttentionCategoriesList(); !slices.Equal(got, want) {
		t.Fatalf("attentionCategoryOrder = %v\nwant %v", got, want)
	}
}

func TestAttentionCategoryEnumMatchesOrder(t *testing.T) {
	f, _ := reflect.TypeFor[AttentionItem]().FieldByName("Category")
	if got := strings.Split(f.Tag.Get("enum"), ","); !slices.Equal(got, AttentionCategoriesList()) {
		t.Fatalf("AttentionItem.Category enum = %v\nwant %v", got, AttentionCategoriesList())
	}
}

func TestEntityAggregateTargetHealth(t *testing.T) {
	q := healthFleet(t, time.Hour, fiveHealthTargets(true)...)
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindTarget}})
	if err != nil {
		t.Fatal(err)
	}
	if want := (OperationalHealthTally{Available: 1, Unavailable: 1, Unknown: 2, NotObserved: 1}); l.Aggregate.TargetHealth != want {
		t.Fatalf("aggregate.targetHealth = %+v, want %+v", l.Aggregate.TargetHealth, want)
	}
}
