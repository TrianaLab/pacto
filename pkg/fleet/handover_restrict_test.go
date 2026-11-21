package fleet

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// inBilling sees only namespace billing; noDeployments may list every kind
// but Deployment.
func inBilling(_, ns string) bool           { return ns == "billing" }
func noDeployments(_, _, kind string) bool  { return kind != "Deployment" }
func everyKind(string, string, string) bool { return true }

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
		ns             func(string, string) bool
		kinds          func(string, string, string) bool
		shown, hidden  int
		serviceVisible bool
	}{
		"both sides":            {inBillingTarget, inBilling, everyKind, 1, 0, true},
		"service, not workload": {inBillingTarget, inBilling, noDeployments, 0, 1, true},
		"workload, not service": {nil, inBilling, everyKind, 0, 0, false},
		"neither":               {nil, func(string, string) bool { return false }, noDeployments, 0, 0, false},
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
		kinds func(string, string, string) bool
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
	if !declarationSourceHidden(q.snap, rev("billing/Deployment/invoices"), func(string, string, string) bool { return false }) {
		t.Error("an unreadable inferredFrom left its cluster facts visible")
	}
	if declarationSourceHidden(q.snap, rev(""), func(string, string, string) bool { return true }) {
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
	want := WorkloadScope{"billing", "Deployment", ""}
	for _, twin := range []bool{true, false} {
		if sc := NewQuery(handSnap(t, []RawRevision{anaDecl(t)}, nil, twin)).InferredWorkloadScopes(); !slices.Contains(sc, want) {
			t.Errorf("twin %v: scopes %v", twin, sc)
		}
	}
	// A limitation that names a workload is reviewed too.
	q := NewQuery(handSnap(t, []RawRevision{handoverRev(t, declOf{from: "k8s:payments/StatefulSet/ledger"})}, nil, false))
	if sc := q.InferredWorkloadScopes(); !slices.Contains(sc, WorkloadScope{"payments", "StatefulSet", ""}) {
		t.Errorf("scopes %v", sc)
	}
}

// The twin's repository is another object's content: after the hand-over a
// viewer who may not list that object sees it hidden, as before it.
func TestRestrictHandoverHidesTwinRepository(t *testing.T) {
	cl := clusterOf(clusterA, "eu", ClusterNameFromContext)
	w := invoicesWorkload()
	w.Repositories = []InferredRepository{{URL: "https://github.com/acme/billing", From: "Application argocd/invoices spec.source.repoURL", Namespace: "argocd", Kind: "Application"}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, DeclarationSigners: anaPolicy()},
		NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{anaDecl(t)}}),
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: cl, Targets: []RawTarget{workload("billing", "invoices")}}),
		workloadsSource("k8s-workloads", cl, w))
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	if len(snap.Replacements) != 1 || !strings.Contains(handoverAnswers(t, q), "github.com/acme/billing") {
		t.Fatalf("the fixture must hand over and show the twin's repository: %+v", snap.Replacements)
	}
	if sc := q.InferredWorkloadScopes(); !slices.Contains(sc, WorkloadScope{"argocd", "Application", clusterA}) {
		t.Errorf("scopes %v", sc)
	}
	got := handoverAnswers(t, q.Restrict(inBilling, func(_, _, kind string) bool { return kind != "Application" }))
	if strings.Contains(got, "github.com/acme/billing") || strings.Contains(got, "argocd/invoices") || !strings.Contains(got, "hidden by your permissions") {
		t.Errorf("a restricted answer shows the twin's repository: %s", got)
	}
}

// A Pacto target's takeover the viewer may not see is counted, and the
// answer keeps the shape an unrestricted viewer gets: no field provenance.
func TestRestrictPactoTargetTakeoverKeepsNoFields(t *testing.T) {
	q := NewQuery(handSnap(t, []RawRevision{handoverRev(t, declOf{noDeclaration: true})}, []RawTarget{invoicesTarget("invoices")}, true))
	if len(q.snap.Replacements) != 1 || q.snap.Replacements[0].Link != linkPactoTarget {
		t.Fatalf("replacements %+v", q.snap.Replacements)
	}
	for name, r := range map[string]*Query{"unrestricted": q, "restricted": q.Restrict(inBilling, noDeployments)} {
		v, err := r.GetService("invoices")
		if err != nil {
			t.Fatal(err)
		}
		if v.Fields != nil {
			t.Errorf("%s: fields %+v", name, v.Fields)
		}
	}
}
