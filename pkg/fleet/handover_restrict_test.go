package fleet

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// inBilling sees only namespace billing; noDeployments may list every kind
// but Deployment.
func inBilling(ns string) bool          { return ns == "billing" }
func noDeployments(_, kind string) bool { return kind != "Deployment" }
func everyKind(string, string) bool     { return true }

// handoverAnswers is the JSON of every answer about invoices a viewer of q
// gets: the service, its page, its explanation and the snapshot.
func handoverAnswers(t *testing.T, q *Query) string {
	t.Helper()
	var out []any
	if v, err := q.GetService("invoices"); err == nil {
		out = append(out, v)
	}
	if d, err := q.EntityDetail(KindService, string(invoicesKey)); err == nil {
		out = append(out, d)
	}
	if e, err := q.Explain("invoices"); err == nil {
		out = append(out, e)
	}
	b, err := json.Marshal(append(out, q.SnapshotForSerialization()))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRestrictHandoverNeedsBothSides(t *testing.T) {
	inBillingTarget := []RawTarget{workload("billing", "invoices")}
	for name, tc := range map[string]struct {
		targets        []RawTarget
		ns             func(string) bool
		kinds          func(string, string) bool
		shown, hidden  int
		serviceVisible bool
	}{
		"both sides":            {inBillingTarget, inBilling, everyKind, 1, 0, true},
		"service, not workload": {inBillingTarget, inBilling, noDeployments, 0, 1, true},
		"workload, not service": {nil, inBilling, everyKind, 0, 0, false},
		"neither":               {nil, func(string) bool { return false }, noDeployments, 0, 0, false},
	} {
		t.Run(name, func(t *testing.T) {
			q := NewQuery(handSnap(t, []RawRevision{anaDecl(t)}, tc.targets, true)).Restrict(tc.ns, tc.kinds)
			v, err := q.GetService("invoices")
			if !tc.serviceVisible {
				if err == nil {
					t.Fatalf("a hidden service was answered: %+v", v)
				}
				if _, err := q.GetService(invoicesName); err == nil {
					t.Error("the replaced key answered for a hidden service")
				}
			} else if err != nil {
				t.Fatal(err)
			} else if len(v.Replaces) != tc.shown || v.ReplacesHidden != tc.hidden {
				t.Errorf("replaces %+v hidden %d", v.Replaces, v.ReplacesHidden)
			} else if d, _ := q.EntityDetail(KindService, string(invoicesKey)); d.Service.ReplacesHidden != tc.hidden {
				t.Errorf("detail hidden %d", d.Service.ReplacesHidden)
			}
			if tc.shown == 0 && strings.Contains(handoverAnswers(t, q), "k8s:billing") {
				t.Errorf("a restricted answer names the workload: %s", handoverAnswers(t, q))
			}
		})
	}
}

func TestRestrictHandoverHidesWorkloadShape(t *testing.T) {
	q := NewQuery(handSnap(t, []RawRevision{anaDecl(t)}, []RawTarget{workload("billing", "invoices")}, true))
	if !strings.Contains(handoverAnswers(t, q), "ghcr.io/acme/invoices") {
		t.Fatal("the fixture must show the twin's image to a viewer who may list it")
	}
	got := handoverAnswers(t, q.Restrict(inBilling, noDeployments))
	for _, s := range []string{"ghcr.io/acme/invoices", "containers[name=invoices]", `"http"`} {
		if strings.Contains(got, s) {
			t.Errorf("the twin's %q reached a viewer who may not list it", s)
		}
	}
}

func TestRestrictInferredLinkIgnoredHidesWorkload(t *testing.T) {
	revs := func(t *testing.T) []RawRevision {
		return []RawRevision{
			handoverRev(t, declOf{service: "a", from: invoicesName, prov: anaSigned()}),
			handoverRev(t, declOf{service: "b", from: invoicesName, prov: anaSigned()}),
			handoverRev(t, declOf{from: invoicesName}),
		}
	}
	targets := []RawTarget{workload("billing", "a"), workload("billing", "b"), workload("billing", "invoices")}
	for name, tc := range map[string]struct {
		twin  bool
		kinds func(string, string) bool
		shown bool
	}{
		"twin absent":                {false, noDeployments, false},
		"namespace visible, kind no": {true, noDeployments, false},
		"workload visible":           {true, everyKind, true},
	} {
		t.Run(name, func(t *testing.T) {
			full := NewQuery(handSnap(t, revs(t), targets, tc.twin))
			if !limitationMentions(full.snap, LimitationInferredLinkIgnored, invoicesName) ||
				!limitationMentions(full.snap, LimitationInferredReplacementAmbiguous, "claimed by the declarations of a, b") {
				t.Fatalf("the fixture must raise both: %+v", full.snap.Limitations)
			}
			q := full.Restrict(inBilling, tc.kinds)
			v, err := q.GetService("a")
			if err != nil {
				t.Fatal(err)
			}
			for _, lims := range [][]Limitation{q.snap.Limitations, v.Meta.Limitations} {
				named := slices.ContainsFunc(lims, func(l Limitation) bool { return strings.Contains(l.Message, invoicesName) })
				if named != tc.shown {
					t.Errorf("workload named %v, limitations %+v", named, lims)
				}
			}
		})
	}
}

func TestRestrictInferredFromHiddenOneRule(t *testing.T) {
	q := NewQuery(handSnap(t, []RawRevision{anaDecl(t)}, []RawTarget{workload("billing", "invoices")}, true)).Restrict(inBilling, noDeployments)
	rk := NewRevisionKey(invoicesKey, "sha256:invoices-0.1.0")
	d, err := q.EntityDetail(KindRevision, string(rk))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Revision.Declaration.InferredFromHidden || d.Revision.Declaration.InferredFrom != "" {
		t.Errorf("revision page %+v", d.Revision.Declaration)
	}
	// The service page reads the view's revision, which the same rule
	// stripped.
	if r := q.snap.Revisions[rk]; !r.declarationHidden || r.Record.Declaration.InferredFrom != "" {
		t.Errorf("the service page reads another rule: %+v", r.Record.Declaration)
	}
	// An inferredFrom Pacto cannot read hides the cluster facts: unknown is
	// never positive. A declaration from a folder alone names no workload.
	rev := func(from string) *ContractRevision {
		r := anaDeclRevision(t)
		r.Record.Declaration.InferredFrom = from
		return r
	}
	if !declarationSourceHidden(rev("billing/Deployment/invoices"), func(string, string) bool { return false }) {
		t.Error("an unreadable inferredFrom left its cluster facts visible")
	}
	if declarationSourceHidden(rev(""), func(string, string) bool { return true }) {
		t.Error("a folder-only declaration hid cluster facts it has none of")
	}
}

// anaDeclRevision is a copy of the invoices revision anaDecl builds.
func anaDeclRevision(t *testing.T) *ContractRevision {
	t.Helper()
	snap := handSnap(t, []RawRevision{anaDecl(t)}, nil, false)
	r := *snap.Revisions[NewRevisionKey(invoicesKey, "sha256:invoices-0.1.0")]
	rec, d := *r.Record, *r.Record.Declaration
	rec.Declaration, r.Record = &d, &rec
	return &r
}

func TestInferredWorkloadScopesIncludesInferredFrom(t *testing.T) {
	want := WorkloadScope{"billing", "Deployment"}
	for _, twin := range []bool{true, false} {
		if sc := NewQuery(handSnap(t, []RawRevision{anaDecl(t)}, nil, twin)).InferredWorkloadScopes(); !slices.Contains(sc, want) {
			t.Errorf("twin %v: scopes %v", twin, sc)
		}
	}
	// A limitation that names a workload is reviewed too.
	q := NewQuery(handSnap(t, []RawRevision{handoverRev(t, declOf{from: "k8s:payments/StatefulSet/ledger"})}, nil, false))
	if sc := q.InferredWorkloadScopes(); !slices.Contains(sc, WorkloadScope{"payments", "StatefulSet"}) {
		t.Errorf("scopes %v", sc)
	}
}
