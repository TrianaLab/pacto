package fleet

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// invoicesName is the inferred service of the invoices Deployment of
// billing in a single-cluster snapshot.
const invoicesName = "k8s:billing/Deployment/invoices"

var invoicesKey = NewServiceKey("invoices")

// declOf shapes handoverRev.
type declOf struct {
	service, version, from, cluster string
	prov                            *BundleProvenance
	noDeclaration                   bool
}

// handoverRev is a registry revision of d.service whose inferred record is
// ana's declaration from d.from (invoicesName when ""), signed as d.prov
// says (anaSigned when nil; set noProv for none).
func handoverRev(t *testing.T, d declOf) RawRevision {
	t.Helper()
	rec, err := contract.ParseInferredRecord(recordOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !d.noDeclaration {
		rec.Declaration = &contract.InferredDeclaration{State: contract.DeclarationDeclared, Asker: ana, InferredFrom: d.from, Cluster: d.cluster}
	}
	svc := d.service
	if svc == "" {
		svc = "invoices"
	}
	ver := d.version
	if ver == "" {
		ver = "0.1.0"
	}
	c := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: svc, Version: ver, Owner: contract.Owner{Team: "billing"}}}
	fsys := fstest.MapFS{"deploy/app.yaml": {Data: []byte(recordManifest)}, contract.InferredRecordFile: {Data: rec.Marshal()}}
	return RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fsys}, Digest: "sha256:" + svc + "-" + ver, Provenance: d.prov}
}

// anaDecl is ana's signed declaration of invoices from the invoices
// Deployment.
func anaDecl(t *testing.T) RawRevision {
	return handoverRev(t, declOf{from: invoicesName, prov: anaSigned()})
}

// invoicesWorkload is the invoices Deployment of billing, created long
// before any Pacto.
func invoicesWorkload() RawInferredService {
	c := "spec.template.spec.containers[name=invoices]"
	return RawInferredService{
		Namespace: "billing", Kind: "Deployment", Name: "invoices", CreatedAt: hoursAgo(72),
		Image: &InferredValue{Value: "ghcr.io/acme/invoices:0.9.0", From: c + ".image"},
		Ports: []InferredPort{{Port: 8080, Name: "http", Protocol: "TCP", From: c + ".ports[0]"}},
	}
}

// invoicesTarget is a Pacto target of service in billing that runs as the
// invoices Deployment.
func invoicesTarget(service string) RawTarget {
	t := workload("billing", service)
	t.Workload = &WorkloadRef{Kind: "Deployment", Name: "invoices"}
	t.CreatedAt = hoursAgo(1)
	return t
}

// handSnap is one cluster: the registry revisions, the Pacto targets, the
// invoices workload (when withWorkload) and a kernel that saw edges.
func handSnap(t *testing.T, revs []RawRevision, targets []RawTarget, withWorkload bool, edges ...ObservedEdge) *FleetSnapshot {
	t.Helper()
	cl := clusterOf(clusterA, "eu", ClusterNameFromContext)
	var ws []RawInferredService
	if withWorkload {
		ws = append(ws, invoicesWorkload())
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, DeclarationSigners: anaPolicy()},
		NewMemorySource("oci", "oci", &Collection{Revisions: revs}),
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: cl, Targets: targets}),
		workloadsSource("k8s-workloads", cl, ws...), kernel(edges...))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// handedTo reports whether the invoices workload went to to: no inferred
// service and its observed name resolving to to.
func handedTo(snap *FleetSnapshot, name string, to ServiceKey) bool {
	k, res := snap.ObservedNameResolver()(name)
	return snap.Services[NewServiceKey(name)] == nil && k == to && res == ObservedResolved
}

// twinStays reports whether the invoices workload is still an inferred
// service and no inferred-from replacement exists.
func twinStays(snap *FleetSnapshot, name string) bool {
	s := snap.Services[NewServiceKey(name)]
	return s != nil && s.Inferred() && !slices.ContainsFunc(snap.Replacements, func(r Replacement) bool { return r.Link == linkInferredFrom })
}

// checkInvoicesWorkload checks that r carries what the invoices workload
// showed and its restriction key.
func checkInvoicesWorkload(t *testing.T, r Replacement) {
	t.Helper()
	if w := r.Workload; w == nil || w.Image == nil || w.Image.Value != "ghcr.io/acme/invoices:0.9.0" || len(w.Ports) != 1 || w.Source != "k8s-workloads" {
		t.Errorf("workload %+v", r.Workload)
	}
	if r.workload != (workloadKey{namespace: "billing", kind: "Deployment", name: "invoices"}) {
		t.Errorf("restrict key %+v", r.workload)
	}
}

func TestInferredFromHandsOver(t *testing.T) {
	before := handSnap(t, []RawRevision{handoverRev(t, declOf{noDeclaration: true})}, nil, true)
	snap := handSnap(t, []RawRevision{anaDecl(t)}, nil, true)
	if !handedTo(snap, invoicesName, invoicesKey) {
		t.Fatalf("not handed over: services %v", snap.Services)
	}
	b, a := NewQuery(before).Overview().Summary.InferredServices, NewQuery(snap).Overview().Summary.InferredServices
	if a != b-1 {
		t.Errorf("inferred services %d, before %d", a, b)
	}
	if len(snap.Replacements) != 1 {
		t.Fatalf("replacements %+v", snap.Replacements)
	}
	r := snap.Replacements[0]
	if r.From != NewServiceKey(invoicesName) || r.To != invoicesKey || r.Link != "inferred-from" || r.By != anaSubject ||
		r.At != nil || r.Target != "" || r.Revision != NewRevisionKey(invoicesKey, "sha256:invoices-0.1.0") {
		t.Errorf("replacement %+v", r)
	}
	checkInvoicesWorkload(t, r)
	if snap.Completeness != before.Completeness {
		t.Errorf("completeness %s, before %s", snap.Completeness, before.Completeness)
	}
}

func TestInferredFromObservationsAttach(t *testing.T) {
	other := rawInferred("billing", "Deployment", "mailer")
	cl := clusterOf(clusterA, "eu", ClusterNameFromContext)
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, DeclarationSigners: anaPolicy()},
		NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{anaDecl(t)}}),
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: cl, Targets: []RawTarget{workload("shop", "ledger")}}),
		workloadsSource("k8s-workloads", cl, invoicesWorkload(), other),
		kernel(call(invoicesName, "ledger"), call("k8s:billing/Deployment/mailer", invoicesName)))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(snap.Relationships, func(r Relationship) bool {
		return r.Provenance == ProvenanceObserved && r.FromService == invoicesKey && r.ToService == "ledger"
	}) {
		t.Errorf("no observed invoices -> ledger: %+v", snap.Relationships)
	}
	if slices.ContainsFunc(snap.UncontractedCallers, func(u UncontractedCaller) bool { return u.Caller == invoicesName }) {
		t.Errorf("the handed-over workload is still an uncontracted caller: %+v", snap.UncontractedCallers)
	}
	if !slices.ContainsFunc(snap.InferredRelationships, func(r Relationship) bool { return r.ToService == invoicesKey }) {
		t.Errorf("no inferred edge to invoices: %+v", snap.InferredRelationships)
	}
}

func TestInferredFromUnverifiedKeepsTwin(t *testing.T) {
	other := anaSigned()
	other.Signers[0].Subject = "eve@acme.com"
	none := anaSigned()
	none.Signers = nil
	refused := anaSigned()
	refused.Signers[0].Subject = "ana@gmail.com"
	refusedDecl := handoverRev(t, declOf{from: invoicesName, prov: refused})
	rec, _ := contract.ParseInferredRecord(refusedDecl.Bundle.FS.(fstest.MapFS)[contract.InferredRecordFile].Data)
	rec.Declaration.Asker.Subject = "ana@gmail.com"
	refusedDecl.Bundle.FS.(fstest.MapFS)[contract.InferredRecordFile] = &fstest.MapFile{Data: rec.Marshal()}
	for name, tc := range map[string]struct {
		rev  RawRevision
		want string
	}{
		"nil provenance":  {handoverRev(t, declOf{from: invoicesName}), "checks no signatures"},
		"no signers":      {handoverRev(t, declOf{from: invoicesName, prov: none}), "no verified person signer"},
		"another subject": {handoverRev(t, declOf{from: invoicesName, prov: other}), "no verified person signer"},
		"policy refuses":  {refusedDecl, "matches none of the subjects"},
	} {
		t.Run(name, func(t *testing.T) {
			// The same signer declaring from its folder alone.
			before := handSnap(t, []RawRevision{handoverRev(t, declOf{prov: tc.rev.Provenance})}, nil, true)
			snap := handSnap(t, []RawRevision{tc.rev}, nil, true)
			if !twinStays(snap, invoicesName) || len(snap.Replacements) != 0 {
				t.Fatalf("handed over: %+v", snap.Replacements)
			}
			if !limitationMentions(snap, LimitationInferredLinkIgnored, "revision invoices@0.1.0") || !limitationMentions(snap, LimitationInferredLinkIgnored, tc.want) {
				t.Errorf("limitations %+v", snap.Limitations)
			}
			if snap.Completeness != before.Completeness {
				t.Errorf("completeness %s, before %s: %+v", snap.Completeness, before.Completeness, snap.Limitations)
			}
		})
	}
}

func TestInferredFromCISignedVerifiedKeepsTwin(t *testing.T) {
	ci := &BundleProvenance{Signature: BundleSignature{Status: SignatureVerified}, Source: "oci",
		Signers: []VerifiedSigner{{Subject: "https://github.com/acme/invoices/.github/workflows/release.yml@refs/heads/main",
			Issuer: "https://token.actions.githubusercontent.com", Class: "ci"}}}
	snap := handSnap(t, []RawRevision{handoverRev(t, declOf{from: invoicesName, prov: ci})}, nil, true)
	if !twinStays(snap, invoicesName) || !limitationMentions(snap, LimitationInferredLinkIgnored, "no verified person signer") {
		t.Errorf("a CI signature handed over: replacements %+v limitations %+v", snap.Replacements, snap.Limitations)
	}
}

func TestInferredFromNoSignerRuleNamesFix(t *testing.T) {
	other := anaSigned()
	other.Signers[0].Issuer = "https://login.microsoftonline.com"
	for name, tc := range map[string]struct {
		prov *BundleProvenance
		fix  string
	}{
		"no signature check": {nil, "read the registry with signature checks"},
		"no matching signer": {other, "delivery.identities.signers"},
	} {
		t.Run(name, func(t *testing.T) {
			snap := handSnap(t, []RawRevision{handoverRev(t, declOf{from: invoicesName, prov: tc.prov})}, nil, true)
			if !limitationMentions(snap, LimitationInferredLinkIgnored, tc.fix) || !limitationMentions(snap, LimitationInferredLinkIgnored, declareDocs) {
				t.Errorf("limitations %+v", snap.Limitations)
			}
		})
	}
}

func TestInferredFromAttentionItem(t *testing.T) {
	snap := handSnap(t, []RawRevision{anaDecl(t)}, nil, true)
	list, err := NewQuery(snap).Attention(AttentionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var items []AttentionItem
	for _, it := range list.Items {
		if it.Code == attentionInferredReplaced {
			items = append(items, it)
		}
	}
	if len(items) != 1 || items[0].Reason != snap.Replacements[0].Line(snap.GeneratedAt) || strings.Contains(items[0].Reason, "Pacto target ") ||
		!strings.HasPrefix(items[0].NextStep, "Confirm that "+invoicesName+" is invoices") {
		t.Errorf("attention %+v", items)
	}
	target := handSnap(t, nil, []RawTarget{invoicesTarget("invoices")}, true)
	list, _ = NewQuery(target).Attention(AttentionFilter{})
	if !slices.ContainsFunc(list.Items, func(it AttentionItem) bool {
		return it.Code == attentionInferredReplaced && strings.HasPrefix(it.Reason, "Pacto target ")
	}) {
		t.Errorf("the pacto-target item changed: %+v", list.Items)
	}
}

func TestInferredFromSkipsAmbiguousTargetWorkload(t *testing.T) {
	snap := handSnap(t, []RawRevision{anaDecl(t)}, []RawTarget{invoicesTarget("orders"), invoicesTarget("payments")}, true)
	if !twinStays(snap, invoicesName) || len(snap.Replacements) != 0 || !limitationMentions(snap, LimitationInferredLinkIgnored, "a Pacto target wins") {
		t.Errorf("replacements %+v limitations %+v", snap.Replacements, snap.Limitations)
	}
}

// twoClusterSnap is the invoices workload in clusters a and b, the given
// Pacto targets (of unknown cluster) and revisions.
func twoClusterSnap(t *testing.T, revs []RawRevision, targets ...RawTarget) *FleetSnapshot {
	t.Helper()
	a, b := clusterOf(clusterA, "eu", ClusterNameFromContext), clusterOf(clusterB, "us", ClusterNameFromContext)
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, DeclarationSigners: anaPolicy()},
		NewMemorySource("oci", "oci", &Collection{Revisions: revs}),
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: targets}),
		workloadsSource("wa", a, invoicesWorkload()), workloadsSource("wb", b, invoicesWorkload()))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestInferredFromSkipsUnknownClusterTarget(t *testing.T) {
	snap := twoClusterSnap(t, []RawRevision{handoverRev(t, declOf{from: invoicesName, cluster: clusterA, prov: anaSigned()})}, invoicesTarget("orders"))
	name := K8sCaller(clusterA, "billing", "Deployment", "invoices")
	if !twinStays(snap, name) || len(snap.Replacements) != 0 || !limitationMentions(snap, LimitationInferredLinkIgnored, "a Pacto target wins") {
		t.Errorf("replacements %+v limitations %+v", snap.Replacements, snap.Limitations)
	}
}

func TestInferredFromNewerDeclarationRetractsOlderClaim(t *testing.T) {
	mailer := "k8s:billing/Deployment/mailer"
	cl := clusterOf(clusterA, "eu", ClusterNameFromContext)
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, DeclarationSigners: anaPolicy()},
		NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{
			handoverRev(t, declOf{from: invoicesName, prov: anaSigned()}),
			handoverRev(t, declOf{version: "0.2.0", from: mailer, prov: anaSigned()}),
		}}),
		workloadsSource("k8s-workloads", cl, invoicesWorkload(), rawInferred("billing", "Deployment", "mailer")))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Services[NewServiceKey(invoicesName)] == nil {
		t.Errorf("the retracted claim handed over")
	}
	if len(snap.Replacements) != 1 || snap.Replacements[0].From != NewServiceKey(mailer) {
		t.Errorf("replacements %+v", snap.Replacements)
	}
}

func TestInferredFromStaleClaimNotAmbiguous(t *testing.T) {
	snap := handSnap(t, []RawRevision{
		handoverRev(t, declOf{service: "a", from: invoicesName, prov: anaSigned()}),
		handoverRev(t, declOf{service: "a", version: "0.2.0", prov: anaSigned()}),
		handoverRev(t, declOf{service: "b", from: invoicesName, prov: anaSigned()}),
	}, nil, true)
	if !handedTo(snap, invoicesName, "b") || len(limitationsWith(snap, LimitationInferredReplacementAmbiguous)) != 0 {
		t.Errorf("replacements %+v limitations %+v", snap.Replacements, snap.Limitations)
	}
}

func TestInferredFromCIPublishKeepsClaim(t *testing.T) {
	snap := handSnap(t, []RawRevision{anaDecl(t), handoverRev(t, declOf{version: "0.2.0", noDeclaration: true})}, nil, true)
	if !handedTo(snap, invoicesName, invoicesKey) || snap.Replacements[0].Revision != NewRevisionKey(invoicesKey, "sha256:invoices-0.1.0") {
		t.Errorf("replacements %+v", snap.Replacements)
	}
}

func TestInferredFromNewestRevisionNames(t *testing.T) {
	snap := handSnap(t, []RawRevision{anaDecl(t), handoverRev(t, declOf{version: "0.2.0", from: invoicesName, prov: anaSigned()})}, nil, true)
	if len(snap.Replacements) != 1 || snap.Replacements[0].Revision != NewRevisionKey(invoicesKey, "sha256:invoices-0.2.0") {
		t.Errorf("replacements %+v", snap.Replacements)
	}
}

func TestInferredFromCrossNamespaceClaimIgnored(t *testing.T) {
	away := workload("payments", "invoices")
	snap := handSnap(t, []RawRevision{anaDecl(t)}, []RawTarget{away}, true)
	if !twinStays(snap, invoicesName) || !limitationMentions(snap, LimitationInferredLinkIgnored, "runs in namespaces payments") {
		t.Errorf("replacements %+v limitations %+v", snap.Replacements, snap.Limitations)
	}
	here := workload("billing", "invoices")
	if snap := handSnap(t, []RawRevision{anaDecl(t)}, []RawTarget{here}, true); !handedTo(snap, invoicesName, invoicesKey) {
		t.Errorf("a target in the workload's namespace blocked the claim: %+v", snap.Limitations)
	}
	if snap := handSnap(t, []RawRevision{anaDecl(t)}, nil, true); !handedTo(snap, invoicesName, invoicesKey) {
		t.Errorf("a contract-only declaration was not handed over")
	}
}

func TestPactoTargetWinsOverInferredFrom(t *testing.T) {
	for name, tc := range map[string]struct {
		targets  []RawTarget
		link, to string
		ignored  bool
	}{
		"another service's target": {[]RawTarget{invoicesTarget("orders")}, linkPactoTarget, "orders", true},
		"its own target":           {[]RawTarget{invoicesTarget("invoices")}, linkPactoTarget, "invoices", false},
		"targets of two services":  {[]RawTarget{invoicesTarget("orders"), invoicesTarget("payments")}, "", "", true},
	} {
		t.Run(name, func(t *testing.T) {
			snap := handSnap(t, []RawRevision{anaDecl(t)}, tc.targets, true)
			if tc.link == "" {
				if !twinStays(snap, invoicesName) || len(snap.Replacements) != 0 {
					t.Errorf("replacements %+v", snap.Replacements)
				}
			} else if len(snap.Replacements) != 1 || snap.Replacements[0].Link != tc.link || snap.Replacements[0].To != ServiceKey(tc.to) {
				t.Errorf("replacements %+v", snap.Replacements)
			}
			if got := len(limitationsWith(snap, LimitationInferredLinkIgnored)) > 0; got != tc.ignored {
				t.Errorf("ignored %v, limitations %+v", got, snap.Limitations)
			}
		})
	}
	snap := twoClusterSnap(t, []RawRevision{handoverRev(t, declOf{from: invoicesName, cluster: clusterA, prov: anaSigned()})}, invoicesTarget("orders"))
	if len(snap.Replacements) != 0 || snap.Services[NewServiceKey(K8sCaller(clusterA, "billing", "Deployment", "invoices"))] == nil {
		t.Errorf("an unknown-cluster target let the claim through: %+v", snap.Replacements)
	}
}

func TestInferredFromTwoServicesAmbiguous(t *testing.T) {
	snap := handSnap(t, []RawRevision{
		handoverRev(t, declOf{service: "a", from: invoicesName, prov: anaSigned()}),
		handoverRev(t, declOf{service: "b", from: invoicesName, prov: anaSigned()}),
	}, nil, true)
	if !twinStays(snap, invoicesName) || !limitationMentions(snap, LimitationInferredReplacementAmbiguous, "claimed by the declarations of a, b") {
		t.Errorf("replacements %+v limitations %+v", snap.Replacements, snap.Limitations)
	}
	if len(snap.inferredLinkNotes) != 1 || len(snap.inferredLinkNotes[0].services) != 2 {
		t.Errorf("notes %+v", snap.inferredLinkNotes)
	}
}

func TestInferredFromInvalidKeyIgnored(t *testing.T) {
	if snap := handSnap(t, []RawRevision{anaDecl(t)}, nil, true); !handedTo(snap, invoicesName, invoicesKey) {
		t.Fatal("the raw name did not hand over")
	}
	// The record's parser accepts only the K8sCaller form, so a value that
	// passes it can still hold a control character.
	bad := "k8s:billing/Deployment/in\u0001voices"
	snap := handSnap(t, []RawRevision{handoverRev(t, declOf{from: bad, prov: anaSigned()})}, nil, true)
	lims := limitationsWith(snap, LimitationInferredLinkIgnored)
	if !twinStays(snap, invoicesName) || len(lims) != 1 || strings.Contains(lims[0].Message, "in\u0001voices") ||
		!strings.Contains(lims[0].Message, "revision invoices@0.1.0") {
		t.Errorf("limitations %+v", snap.Limitations)
	}
	// A declaration from a folder alone claims nothing and says nothing.
	snap = handSnap(t, []RawRevision{handoverRev(t, declOf{prov: anaSigned()})}, nil, true)
	if !twinStays(snap, invoicesName) || len(limitationsWith(snap, LimitationInferredLinkIgnored)) != 0 {
		t.Errorf("limitations %+v", snap.Limitations)
	}
}

func TestInferredFromClusterQualification(t *testing.T) {
	inA := K8sCaller(clusterA, "billing", "Deployment", "invoices")
	t.Run("multi-cluster with no cluster", func(t *testing.T) {
		snap := twoClusterSnap(t, []RawRevision{anaDecl(t)})
		if len(snap.Replacements) != 0 || !limitationMentions(snap, LimitationInferredReplacementAmbiguous, "unknown cluster") {
			t.Errorf("replacements %+v limitations %+v", snap.Replacements, snap.Limitations)
		}
	})
	t.Run("multi-cluster with the declaration's cluster", func(t *testing.T) {
		snap := twoClusterSnap(t, []RawRevision{handoverRev(t, declOf{from: invoicesName, cluster: clusterA, prov: anaSigned()})})
		if !handedTo(snap, inA, invoicesKey) || snap.Services[NewServiceKey(K8sCaller(clusterB, "billing", "Deployment", "invoices"))] == nil {
			t.Errorf("replacements %+v", snap.Replacements)
		}
	})
	t.Run("multi-cluster with a 4-part name", func(t *testing.T) {
		snap := twoClusterSnap(t, []RawRevision{handoverRev(t, declOf{from: inA, prov: anaSigned()})})
		if !handedTo(snap, inA, invoicesKey) {
			t.Errorf("replacements %+v", snap.Replacements)
		}
	})
	for name, cluster := range map[string]string{"single cluster, equal": clusterA, "single cluster, empty": ""} {
		t.Run(name, func(t *testing.T) {
			snap := handSnap(t, []RawRevision{handoverRev(t, declOf{from: invoicesName, cluster: cluster, prov: anaSigned()})}, nil, true)
			if !handedTo(snap, invoicesName, invoicesKey) {
				t.Errorf("limitations %+v", snap.Limitations)
			}
		})
	}
	t.Run("single cluster, known mismatch", func(t *testing.T) {
		snap := handSnap(t, []RawRevision{handoverRev(t, declOf{from: invoicesName, cluster: clusterB, prov: anaSigned()})}, nil, true)
		if !twinStays(snap, invoicesName) || !limitationMentions(snap, LimitationInferredLinkIgnored, "this fleet's cluster is "+clusterA) {
			t.Errorf("limitations %+v", snap.Limitations)
		}
	})
	t.Run("single cluster, no workload, cluster from the snapshot", func(t *testing.T) {
		snap := handSnap(t, []RawRevision{handoverRev(t, declOf{from: invoicesName, cluster: clusterB, prov: anaSigned()})}, nil, false)
		if len(snap.Replacements) != 0 || !limitationMentions(snap, LimitationInferredLinkIgnored, "this fleet's cluster is "+clusterA) {
			t.Errorf("limitations %+v", snap.Limitations)
		}
	})
}

func TestInferredFromWithoutWorkloadSource(t *testing.T) {
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, DeclarationSigners: anaPolicy()},
		NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{anaDecl(t)}}))
	if err != nil {
		t.Fatal(err)
	}
	if !handedTo(snap, invoicesName, invoicesKey) || len(snap.Replacements) != 1 || snap.Replacements[0].Workload != nil {
		t.Errorf("replacements %+v", snap.Replacements)
	}
}

func TestResolveOldKeyAfterInferredFrom(t *testing.T) {
	q := NewQuery(handSnap(t, []RawRevision{anaDecl(t)}, nil, true))
	sv, err := q.GetService(invoicesName)
	if err != nil || sv.Service.Key != invoicesKey || sv.ReplacedFrom == nil || sv.ReplacedFrom.Link != "inferred-from" {
		t.Fatalf("get %+v %v", sv, err)
	}
	var nf *NotFoundError
	if _, err := q.Explain(invoicesName); !errors.As(err, &nf) {
		t.Errorf("explain of the old key: %v", err)
	}
}

// observedPass stands in for 509-S10's pass: it hands wk to to through a
// third link unless a Pacto target may run as it or it already changed
// hands.
func observedPass(snap *FleetSnapshot, wk workloadKey, to ServiceKey) {
	targeting, unknown := targetsByWorkload(snap)
	if _, done := snap.replacedNames[K8sCaller(wk.cluster, wk.namespace, wk.kind, wk.name)]; done || len(targetsOf(wk, targeting, unknown)) > 0 {
		return
	}
	handOver(snap, wk, to, Replacement{Link: linkObserved}, nil)
}

var invoicesWK = workloadKey{namespace: "billing", kind: "Deployment", name: "invoices"}

func TestHandOverAnyLinkFiresReplacement(t *testing.T) {
	snap := handSnap(t, nil, nil, false)
	observedPass(snap, invoicesWK, "ledger")
	if len(snap.Replacements) != 1 || snap.Replacements[0].Link != linkObserved || snap.Replacements[0].To != "ledger" || snap.Replacements[0].From != NewServiceKey(invoicesName) {
		t.Errorf("replacements %+v", snap.Replacements)
	}
	if k, res := snap.ObservedNameResolver()(invoicesName); k != "ledger" || res != ObservedResolved {
		t.Errorf("resolved %s %v", k, res)
	}
	// An input whose identity is invalid shows nothing.
	bad := workloadKey{namespace: "billing", kind: "Deployment", name: "bad"}
	handOver(snap, bad, "ledger", Replacement{Link: linkObserved}, []inferredInput{{source: "w", raw: rawInferred("billing", "Deployment", "")}})
	if len(snap.Replacements) != 2 || snap.Replacements[1].Workload != nil {
		t.Errorf("replacements %+v", snap.Replacements)
	}
}

func TestInferredFromTwoWorkloadsTwoServices(t *testing.T) {
	cl := clusterOf(clusterA, "eu", ClusterNameFromContext)
	mailer := "k8s:billing/Deployment/mailer"
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, DeclarationSigners: anaPolicy()},
		NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{anaDecl(t), handoverRev(t, declOf{service: "mail", from: mailer, prov: anaSigned()})}}),
		workloadsSource("k8s-workloads", cl, invoicesWorkload(), rawInferred("billing", "Deployment", "mailer")))
	if err != nil {
		t.Fatal(err)
	}
	if !handedTo(snap, invoicesName, invoicesKey) || !handedTo(snap, mailer, "mail") || len(snap.Replacements) != 2 {
		t.Errorf("replacements %+v", snap.Replacements)
	}
}

func TestPactoTargetReplacementWithoutTimeRaisesNothing(t *testing.T) {
	snap := handSnap(t, nil, []RawTarget{invoicesTarget("invoices")}, true)
	snap.Replacements[0].At = nil
	list, _ := NewQuery(snap).Attention(AttentionFilter{})
	if slices.ContainsFunc(list.Items, func(it AttentionItem) bool { return it.Code == attentionInferredReplaced }) {
		t.Errorf("an untimed pacto-target replacement raised an item: %+v", list.Items)
	}
}

func TestPactoTargetWinsOverEveryLink(t *testing.T) {
	for name, tc := range map[string]struct {
		targets []RawTarget
		link    string
	}{
		"one target":              {[]RawTarget{invoicesTarget("orders")}, linkPactoTarget},
		"targets of two services": {[]RawTarget{invoicesTarget("orders"), invoicesTarget("payments")}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			snap := handSnap(t, []RawRevision{anaDecl(t)}, tc.targets, true)
			observedPass(snap, invoicesWK, "ledger")
			links := make([]string, 0, len(snap.Replacements))
			for _, r := range snap.Replacements {
				links = append(links, r.Link)
			}
			if tc.link == "" && (len(links) != 0 || snap.Services[NewServiceKey(invoicesName)] == nil) || tc.link != "" && !slices.Equal(links, []string{tc.link}) {
				t.Errorf("links %v", links)
			}
		})
	}
	t.Run("unknown-cluster target", func(t *testing.T) {
		snap := twoClusterSnap(t, []RawRevision{handoverRev(t, declOf{from: invoicesName, cluster: clusterA, prov: anaSigned()})}, invoicesTarget("orders"))
		observedPass(snap, workloadKey{cluster: clusterA, namespace: "billing", kind: "Deployment", name: "invoices"}, "ledger")
		if len(snap.Replacements) != 0 {
			t.Errorf("replacements %+v", snap.Replacements)
		}
	})
	t.Run("inferred-from before observed", func(t *testing.T) {
		snap := handSnap(t, []RawRevision{anaDecl(t)}, nil, true)
		observedPass(snap, invoicesWK, "ledger")
		if len(snap.Replacements) != 1 || snap.Replacements[0].Link != linkInferredFrom {
			t.Errorf("replacements %+v", snap.Replacements)
		}
	})
}

func TestReplacementLine(t *testing.T) {
	r := Replacement{From: NewServiceKey(invoicesName), Link: linkInferredFrom, By: anaSubject, Revision: "invoices@sha256:x"}
	if got := r.Line(fixedNow()); got != "Declared by ana@acme.com from inferred "+invoicesName+" (revision invoices@sha256:x); Pacto read no workload for it here" {
		t.Errorf("line %q", got)
	}
	r.Workload = &InferredWorkload{Kind: "Deployment", Namespace: "billing", Name: "invoices"}
	if got := r.Line(fixedNow()); strings.Contains(got, "no workload") {
		t.Errorf("line with a workload %q", got)
	}
	p := Replacement{From: NewServiceKey(cartName), Link: linkPactoTarget, Target: NewTargetKey("shop", "kubernetes", "cart"), At: new(fixedNow().Add(-3 * 60e9))}
	if got := p.Line(fixedNow()); got != "Replaces "+cartName+" 3m ago (linked by Pacto shop/cart target)" {
		t.Errorf("line %q", got)
	}
}

func TestInferredFromWorkloadFromLowestSource(t *testing.T) {
	cl := clusterOf(clusterA, "eu", ClusterNameFromContext)
	for _, order := range [][2]string{{"wa", "wb"}, {"wb", "wa"}} {
		snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, DeclarationSigners: anaPolicy()},
			NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{anaDecl(t)}}),
			workloadsSource(order[0], cl, invoicesWorkload()), workloadsSource(order[1], cl, invoicesWorkload()))
		if err != nil {
			t.Fatal(err)
		}
		if len(snap.Replacements) != 1 || snap.Replacements[0].Workload.Source != "wa" {
			t.Errorf("%v: replacements %+v", order, snap.Replacements)
		}
	}
}

// A newer revision whose inferred record Pacto cannot read may retract the
// older claim: the twin stays and a limitation says why.
func TestInferredFromUnreadableNewerRecordKeepsTwin(t *testing.T) {
	newer := handoverRev(t, declOf{version: "0.2.0", noDeclaration: true})
	newer.Bundle.FS.(fstest.MapFS)[contract.InferredRecordFile] = &fstest.MapFile{Data: []byte("not: [a record")}
	snap := handSnap(t, []RawRevision{anaDecl(t), newer}, nil, true)
	if !twinStays(snap, invoicesName) {
		t.Errorf("a claim an unreadable newer record may retract handed over: %+v", snap.Replacements)
	}
	if ls := limitationsWith(snap, LimitationInferredLinkIgnored); len(ls) != 1 || !strings.Contains(ls[0].Message, "invoices@0.2.0 ships an inferred record Pacto cannot read") {
		t.Errorf("limitations %+v", ls)
	}
	// An unreadable record older than the claim does not touch it.
	older := handoverRev(t, declOf{version: "0.0.1", noDeclaration: true})
	older.Bundle.FS.(fstest.MapFS)[contract.InferredRecordFile] = &fstest.MapFile{Data: []byte("not: [a record")}
	if snap := handSnap(t, []RawRevision{anaDecl(t), older}, nil, true); !handedTo(snap, invoicesName, invoicesKey) {
		t.Errorf("an older unreadable record blocked the claim: %+v", snap.Limitations)
	}
}

// The newest replacement Pacto read a workload for carries its facts; every
// other one is a single line.
func TestReplacesLines(t *testing.T) {
	bare := Replacement{From: NewServiceKey(cartName), Link: linkInferredFrom, By: anaSubject, Revision: "cart@sha256:x"}
	read := bare
	read.From, read.Workload = NewServiceKey(invoicesName), &InferredWorkload{Kind: "Deployment", Namespace: "billing", Name: "invoices", Source: "k8s"}
	got := ReplacesLines([]Replacement{bare, read, read}, fixedNow())
	if len(got) != 4 || !strings.HasSuffix(got[0], "; Pacto read no workload for it here") || got[2] != "  Workload: Deployment billing/invoices" || strings.HasPrefix(got[3], " ") {
		t.Errorf("lines %q", got)
	}
}
