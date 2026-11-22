package fleet

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// paymentsName is the inferred service of the payments Deployment of shop in
// a single-cluster snapshot.
const paymentsName = "k8s:shop/Deployment/payments"

const paymentsDigest = "sha256:ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12"

var paymentsKey = NewServiceKey("payments")

func minutesAgo(n int) *time.Time { return new(fixedNow().Add(-time.Duration(n) * time.Minute)) }

// paymentsWorkload is the payments Deployment of shop, created at created.
func paymentsWorkload(created *time.Time) RawInferredService {
	w := rawInferred("shop", "Deployment", "payments")
	w.CreatedAt = created
	return w
}

// linkedPayments is the target the operator linked to service's published
// contract: the payments Deployment of shop running its image since since.
func linkedPayments(service string, since *time.Time) RawTarget {
	t := workload("shop", service)
	t.Name = "Deployment/payments"
	t.Workload = &WorkloadRef{Kind: "Deployment", Name: "payments"}
	t.Link = &ObservedLink{Signal: LinkSignalImageDigest, Value: paymentsDigest, Since: since}
	return t
}

// paymentsTarget is a Pacto target of service in shop that runs as the
// payments Deployment.
func paymentsTarget(service string) RawTarget {
	t := workload("shop", service)
	t.Workload = &WorkloadRef{Kind: "Deployment", Name: "payments"}
	t.CreatedAt = hoursAgo(1)
	return t
}

// linkSnap is one cluster: revisions, Pacto targets, linked targets and,
// unless workload is nil, the payments workload.
func linkSnap(t *testing.T, revs []RawRevision, targets, linked []RawTarget, workload *RawInferredService) *FleetSnapshot {
	t.Helper()
	cl := clusterOf(clusterA, "eu", ClusterNameFromContext)
	var ws []RawInferredService
	if workload != nil {
		ws = append(ws, *workload)
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, DeclarationSigners: anaPolicy()},
		NewMemorySource("oci", "oci", &Collection{Revisions: revs}),
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: cl, Targets: targets}),
		NewMemorySource("k8s-linked", SourceKindKubernetes, &Collection{Cluster: cl, Targets: linked}),
		workloadsSource("k8s-workloads", cl, ws...))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func linkedTargets(snap *FleetSnapshot) []*TargetRecord {
	var out []*TargetRecord
	for _, t := range snap.Targets {
		if t.Link != nil {
			out = append(out, t)
		}
	}
	return out
}

func links(snap *FleetSnapshot) []string {
	out := []string{}
	for _, r := range snap.Replacements {
		out = append(out, r.Link)
	}
	return out
}

func TestObservedLinkHandsOver(t *testing.T) {
	since := minutesAgo(30)
	rev := handoverRev(t, declOf{service: "payments", version: "2.3.0", noDeclaration: true})
	lt := linkedPayments("payments", since)
	lt.Digest = rev.Digest
	snap := linkSnap(t, []RawRevision{rev}, nil, []RawTarget{lt}, new(paymentsWorkload(minutesAgo(50))))
	if !handedTo(snap, paymentsName, paymentsKey) {
		t.Fatalf("not handed over: services %v", slices.Collect(maps.Keys(snap.Services)))
	}
	view, err := NewQuery(snap).GetService(paymentsName)
	if err != nil || view.Service.Key != paymentsKey || view.ReplacedFrom == nil || view.ReplacedFrom.Link != linkObserved {
		t.Fatalf("view %+v err %v", view, err)
	}
	ts := linkedTargets(snap)
	if len(ts) != 1 || ts[0].ContractRevision == "" || len(snap.Replacements) != 1 || snap.Replacements[0].Workload == nil {
		t.Fatalf("linked targets %+v replacements %+v", ts, snap.Replacements)
	}
	want := Replacement{
		From: NewServiceKey(paymentsName), To: paymentsKey, Link: linkObserved, Signal: LinkSignalImageDigest, Value: paymentsDigest,
		Target: ts[0].Key, Revision: ts[0].ContractRevision, At: since, Workload: snap.Replacements[0].Workload,
		workload: workloadKey{namespace: "shop", kind: "Deployment", name: "payments"},
	}
	if !reflect.DeepEqual(snap.Replacements[0], want) {
		t.Errorf("replacement %+v\nwant %+v", snap.Replacements[0], want)
	}
}

func TestObservedLinkFreshInstallRecordsNothing(t *testing.T) {
	snap := linkSnap(t, nil, nil, []RawTarget{linkedPayments("payments", minutesAgo(30))}, new(paymentsWorkload(minutesAgo(32))))
	if !handedTo(snap, paymentsName, paymentsKey) || len(snap.Replacements) != 0 {
		t.Errorf("replacements %+v", snap.Replacements)
	}
	// No link time: a resolver-only hand-over too.
	snap = linkSnap(t, nil, nil, []RawTarget{linkedPayments("payments", nil)}, new(paymentsWorkload(hoursAgo(72))))
	if !handedTo(snap, paymentsName, paymentsKey) || len(snap.Replacements) != 0 {
		t.Errorf("replacements %+v", snap.Replacements)
	}
}

func TestObservedLinkWithoutWorkloadSource(t *testing.T) {
	snap := linkSnap(t, nil, nil, []RawTarget{linkedPayments("payments", minutesAgo(30))}, nil)
	if !handedTo(snap, paymentsName, paymentsKey) || len(snap.Replacements) != 0 {
		t.Errorf("replacements %+v", snap.Replacements)
	}
}

func TestObservedLinkLosesToPactoTarget(t *testing.T) {
	snap := linkSnap(t, nil, []RawTarget{paymentsTarget("orders")}, []RawTarget{linkedPayments("payments", minutesAgo(30))}, new(paymentsWorkload(hoursAgo(72))))
	if len(linkedTargets(snap)) != 0 || !handedTo(snap, paymentsName, NewServiceKey("orders")) || !slices.Equal(links(snap), []string{linkPactoTarget}) {
		t.Errorf("linked %+v replacements %+v", linkedTargets(snap), snap.Replacements)
	}
}

func TestObservedLinkLosesToAmbiguousPactoTarget(t *testing.T) {
	snap := linkSnap(t, nil, []RawTarget{paymentsTarget("orders"), paymentsTarget("refunds")}, []RawTarget{linkedPayments("payments", minutesAgo(30))}, new(paymentsWorkload(hoursAgo(72))))
	s := snap.Services[NewServiceKey(paymentsName)]
	if len(linkedTargets(snap)) != 0 || s == nil || !s.Inferred() || len(snap.Replacements) != 0 {
		t.Errorf("linked %+v replacements %+v", linkedTargets(snap), snap.Replacements)
	}
}

func TestObservedLinkLosesToUnknownClusterTarget(t *testing.T) {
	a, b := clusterOf(clusterA, "eu", ClusterNameFromContext), clusterOf(clusterB, "us", ClusterNameFromContext)
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{paymentsTarget("orders")}}),
		NewMemorySource("k8s-linked", SourceKindKubernetes, &Collection{Cluster: a, Targets: []RawTarget{linkedPayments("payments", minutesAgo(30))}}),
		workloadsSource("wa", a, paymentsWorkload(hoursAgo(72))), workloadsSource("wb", b, paymentsWorkload(hoursAgo(72))))
	if err != nil {
		t.Fatal(err)
	}
	if len(linkedTargets(snap)) != 0 || len(snap.Replacements) != 0 {
		t.Errorf("linked %+v replacements %+v", linkedTargets(snap), snap.Replacements)
	}
}

func TestObservedLinkOfUnknownClusterHandsNothingOver(t *testing.T) {
	a, b := clusterOf(clusterA, "eu", ClusterNameFromContext), clusterOf(clusterB, "us", ClusterNameFromContext)
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("k8s-linked", SourceKindKubernetes, &Collection{Targets: []RawTarget{linkedPayments("payments", minutesAgo(30))}}),
		workloadsSource("wa", a, paymentsWorkload(hoursAgo(72))), workloadsSource("wb", b, paymentsWorkload(hoursAgo(72))))
	if err != nil {
		t.Fatal(err)
	}
	if len(linkedTargets(snap)) != 1 || len(snap.Replacements) != 0 || !limitationMentions(snap, LimitationInferredReplacementAmbiguous, "in an unknown cluster") {
		t.Errorf("replacements %+v limitations %+v", snap.Replacements, snap.Limitations)
	}
}

func paymentsDecl(t *testing.T, service string) RawRevision {
	return handoverRev(t, declOf{service: service, from: paymentsName, prov: anaSigned()})
}

func TestObservedLinkAfterInferredFromSameService(t *testing.T) {
	snap := linkSnap(t, []RawRevision{paymentsDecl(t, "payments")}, nil, []RawTarget{linkedPayments("payments", minutesAgo(30))}, new(paymentsWorkload(hoursAgo(72))))
	if len(linkedTargets(snap)) != 1 || !handedTo(snap, paymentsName, paymentsKey) || !slices.Equal(links(snap), []string{linkInferredFrom}) ||
		len(limitationsWith(snap, LimitationInferredReplacementAmbiguous)) != 0 {
		t.Errorf("replacements %+v limitations %+v", snap.Replacements, snap.Limitations)
	}
}

func TestObservedLinkAfterInferredFromOtherServiceAmbiguous(t *testing.T) {
	snap := linkSnap(t, []RawRevision{paymentsDecl(t, "ledger")}, nil, []RawTarget{linkedPayments("payments", minutesAgo(30))}, new(paymentsWorkload(hoursAgo(72))))
	if len(linkedTargets(snap)) != 1 || !handedTo(snap, paymentsName, NewServiceKey("ledger")) || !slices.Equal(links(snap), []string{linkInferredFrom}) ||
		!limitationMentions(snap, LimitationInferredReplacementAmbiguous, "so it stays with ledger") {
		t.Errorf("replacements %+v limitations %+v", snap.Replacements, snap.Limitations)
	}
}

func TestObservedLinkIsNeverAPactoTarget(t *testing.T) {
	snap := linkSnap(t, nil, nil, []RawTarget{linkedPayments("payments", minutesAgo(30))}, nil)
	targeting, unknown := targetsByWorkload(snap)
	if len(targeting) != 0 || len(unknown) != 0 {
		t.Errorf("targeting %v unknown %v", targeting, unknown)
	}
}

func TestObservedLinkAttentionAgesOut(t *testing.T) {
	rev := handoverRev(t, declOf{service: "payments", version: "2.3.0", noDeclaration: true})
	lt := linkedPayments("payments", minutesAgo(30))
	lt.Digest = rev.Digest
	items := func(snap *FleetSnapshot) []AttentionItem {
		list, _ := NewQuery(snap).Attention(AttentionFilter{})
		return slices.DeleteFunc(list.Items, func(it AttentionItem) bool { return it.Code != attentionInferredReplaced })
	}
	got := items(linkSnap(t, []RawRevision{rev}, nil, []RawTarget{lt}, new(paymentsWorkload(hoursAgo(72)))))
	if len(got) != 1 || !strings.Contains(got[0].Reason, "runs image sha256:ab12cd34ef56…, which payments 2.3.0 published") ||
		!strings.Contains(got[0].NextStep, "set pacto.trianalab.io/contract on the workload") {
		t.Fatalf("items %+v", got)
	}
	ann := linkedPayments("payments", minutesAgo(30))
	ann.Link.Signal, ann.Link.Value = LinkSignalAnnotation, "oci://ghcr.io/acme/contracts/payments:2.3.0"
	got = items(linkSnap(t, nil, nil, []RawTarget{ann}, new(paymentsWorkload(hoursAgo(72)))))
	if len(got) != 1 || !strings.Contains(got[0].Reason, "carries pacto.trianalab.io/contract naming oci://ghcr.io/acme/contracts/payments:2.3.0") {
		t.Fatalf("items %+v", got)
	}
	old := linkedPayments("payments", hoursAgo(48))
	if got = items(linkSnap(t, nil, nil, []RawTarget{old}, new(paymentsWorkload(hoursAgo(72))))); len(got) != 0 {
		t.Errorf("an old observed link raised %+v", got)
	}
}

func TestReplacementLineObserved(t *testing.T) {
	r := Replacement{From: NewServiceKey(paymentsName), To: paymentsKey, Link: linkObserved, Signal: LinkSignalImageDigest, Value: paymentsDigest, At: minutesAgo(3)}
	if got := r.Line(fixedNow()); got != paymentsName+" is linked to payments by its running image digest sha256:ab12cd34ef56… (observed, 3m ago)" {
		t.Errorf("line %q", got)
	}
	if got := r.ReplacedLine(fixedNow()); got != r.Line(fixedNow()) {
		t.Errorf("replaced line %q", got)
	}
	r.Signal, r.Value, r.At = LinkSignalAnnotation, "oci://ghcr.io/acme/contracts/payments:2.3.0", nil
	if got := r.Line(fixedNow()); got != paymentsName+" is linked to payments by its pacto.trianalab.io/contract annotation (observed)" {
		t.Errorf("line %q", got)
	}
	l := &ObservedLink{Signal: LinkSignalImageDigest, Value: paymentsDigest, Since: minutesAgo(3)}
	if got := l.Line(fixedNow()); got != "observed: image digest sha256:ab12cd34ef56… since 3m ago; no Pacto" {
		t.Errorf("link line %q", got)
	}
	l = &ObservedLink{Signal: LinkSignalAnnotation, Value: "oci://r/p:1"}
	if got := l.Line(fixedNow()); got != "observed: pacto.trianalab.io/contract oci://r/p:1; no Pacto" {
		t.Errorf("link line %q", got)
	}
	if got := shortDigestOf("sha256:ab"); got != "sha256:ab" {
		t.Errorf("short %q", got)
	}
}

func allVisible(string, string) bool           { return true }
func allWorkloads(string, string, string) bool { return true }

func TestRestrictLinkedTargetByNamespace(t *testing.T) {
	q := NewQuery(linkSnap(t, nil, nil, []RawTarget{linkedPayments("payments", minutesAgo(30))}, new(paymentsWorkload(hoursAgo(72)))))
	if len(q.snap.Replacements) != 1 {
		t.Fatalf("replacements %+v", q.snap.Replacements)
	}
	r := q.Restrict(anyCluster(func(ns string) bool { return ns != "shop" }), allWorkloads)
	if len(linkedTargets(r.snap)) != 0 || len(r.snap.Replacements) != 0 {
		t.Errorf("linked %+v replacements %+v", linkedTargets(r.snap), r.snap.Replacements)
	}
	if _, res := r.snap.ObservedNameResolver()(paymentsName); res != ObservedUnknown {
		t.Errorf("a hidden linked workload resolves: %v", res)
	}
	if r = q.Restrict(allVisible, allWorkloads); len(linkedTargets(r.snap)) != 1 || len(r.snap.Replacements) != 1 {
		t.Errorf("visible: linked %+v replacements %+v", linkedTargets(r.snap), r.snap.Replacements)
	}
}

func TestRestrictLinkedTargetPerCluster(t *testing.T) {
	a, b := clusterOf(clusterA, "eu", ClusterNameFromContext), clusterOf(clusterB, "us", ClusterNameFromContext)
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("k8s-linked", SourceKindKubernetes, &Collection{Cluster: a, Targets: []RawTarget{linkedPayments("payments", minutesAgo(30))}}),
		workloadsSource("wa", a, paymentsWorkload(hoursAgo(72))), workloadsSource("wb", b, paymentsWorkload(hoursAgo(72))))
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	if len(q.snap.Replacements) != 1 {
		t.Fatalf("replacements %+v", q.snap.Replacements)
	}
	hideA := q.Restrict(func(c, ns string) bool { return c != clusterA }, allWorkloads)
	if len(linkedTargets(hideA.snap)) != 0 || len(hideA.snap.Replacements) != 0 {
		t.Errorf("cluster a hidden: linked %+v replacements %+v", linkedTargets(hideA.snap), hideA.snap.Replacements)
	}
	hideB := q.Restrict(func(c, ns string) bool { return c != clusterB }, allWorkloads)
	if len(linkedTargets(hideB.snap)) != 1 || len(hideB.snap.Replacements) != 1 {
		t.Errorf("cluster b hidden: linked %+v replacements %+v", linkedTargets(hideB.snap), hideB.snap.Replacements)
	}
}

func TestRestrictLinkAmbiguityByNamespace(t *testing.T) {
	q := NewQuery(linkSnap(t, []RawRevision{paymentsDecl(t, "ledger")}, nil, []RawTarget{linkedPayments("payments", minutesAgo(30))}, new(paymentsWorkload(hoursAgo(72)))))
	if len(limitationsWith(q.snap, LimitationInferredReplacementAmbiguous)) != 1 {
		t.Fatalf("limitations %+v", q.snap.Limitations)
	}
	r := q.Restrict(anyCluster(func(ns string) bool { return ns != "shop" }), allWorkloads)
	if n := limitationsWith(r.snap, LimitationInferredReplacementAmbiguous); len(n) != 0 {
		t.Errorf("a hidden workload's ambiguity shows: %+v", n)
	}
}

func TestObservedLinkIsDeterministic(t *testing.T) {
	cl := clusterOf(clusterA, "eu", ClusterNameFromContext)
	other := linkedPayments("ledger", minutesAgo(30))
	other.Name, other.Workload = "StatefulSet/ledger", &WorkloadRef{Kind: "StatefulSet", Name: "ledger"}
	ledger := rawInferred("shop", "StatefulSet", "ledger")
	ledger.CreatedAt = hoursAgo(90)
	srcs := []Source{
		NewMemorySource("k8s-linked", SourceKindKubernetes, &Collection{Cluster: cl, Targets: []RawTarget{linkedPayments("payments", minutesAgo(30)), other}}),
		workloadsSource("w0", nil, ledger),
		workloadsSource("w1", nil, paymentsWorkload(hoursAgo(72))),
		workloadsSource("w2", nil, paymentsWorkload(nil)),
	}
	a := buildClusterSnap(t, srcs...)
	b := buildClusterSnap(t, srcs[0], srcs[3], srcs[2], srcs[1])
	if !reflect.DeepEqual(a.Replacements, b.Replacements) || a.SnapshotID != b.SnapshotID || len(a.Replacements) != 2 {
		t.Errorf("%+v %s\n%+v %s", a.Replacements, a.SnapshotID, b.Replacements, b.SnapshotID)
	}
}

func TestObservedLinkOtherVersionsLimitation(t *testing.T) {
	lt := linkedPayments("payments", minutesAgo(30))
	lt.Link.OtherVersions = []string{"2.2.0", "2.1.0"}
	q := NewQuery(linkSnap(t, nil, nil, []RawTarget{lt}, new(paymentsWorkload(hoursAgo(72)))))
	ls := limitationsWith(q.snap, LimitationLinkVersionAmbiguous)
	ts := linkedTargets(q.snap)
	if len(ls) != 1 || !strings.Contains(ls[0].Message, "shop/Deployment/payments") || !strings.Contains(ls[0].Message, "2.2.0, 2.1.0") ||
		len(ts) != 1 || !slices.ContainsFunc(ts[0].Limitations, func(l Limitation) bool { return l.Code == LimitationLinkVersionAmbiguous }) {
		t.Fatalf("limitations %+v targets %+v", ls, ts)
	}
	r := q.Restrict(anyCluster(func(ns string) bool { return ns != "shop" }), allWorkloads)
	if n := limitationsWith(r.snap, LimitationLinkVersionAmbiguous); len(n) != 0 {
		t.Errorf("a hidden workload's version ambiguity shows: %+v", n)
	}
}
