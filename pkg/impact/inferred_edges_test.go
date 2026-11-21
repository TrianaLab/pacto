package impact

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const legacy = "k8s:pay/Deployment/legacy"

// inferredEdgeSnaps is auth-service, api-gateway depending on it and an
// uncontracted legacy caller of auth-service, without and with the legacy
// workload and the inferred edges it ends (legacy to and from auth-service,
// api-gateway to legacy).
func inferredEdgeSnaps(t *testing.T) (without, with *fleet.FleetSnapshot) {
	t.Helper()
	build := func(observed []fleet.ObservedEdge, other ...fleet.Source) *fleet.FleetSnapshot {
		revs := []fleet.RawRevision{{Bundle: mkBundle(svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"}))}, gwRev(authDep)}
		srcs := append([]fleet.Source{fleet.NewMemorySource("test", "memory", &fleet.Collection{Revisions: revs, Observed: observed})}, other...)
		snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow }}, srcs...)
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	caller := fleet.ObservedEdge{From: legacy, To: "auth-service", Count: 1}
	without = build([]fleet.ObservedEdge{caller})
	with = build([]fleet.ObservedEdge{caller, {From: "auth-service", To: legacy, Count: 1}, {From: "api-gateway", To: legacy, Count: 1}}, legacyWorkloads())
	if len(with.InferredRelationships) != 3 {
		t.Fatalf("inferred relationships %+v", with.InferredRelationships)
	}
	return without, with
}

// sameAnswer fails when b's consumers, gate or completeness differ from a's.
func sameAnswer(t *testing.T, a, b *Result) {
	t.Helper()
	if !reflect.DeepEqual(a.Consumers, b.Consumers) || a.Gate != b.Gate || a.Completeness != b.Completeness {
		t.Errorf("consumers %+v gate %s %s\nwant %+v gate %s %s", b.Consumers, b.Gate, b.Completeness, a.Consumers, a.Gate, a.Completeness)
	}
}

func TestImpactIgnoresInferredRelationships(t *testing.T) {
	without, with := inferredEdgeSnaps(t)
	a, b := analyzeAuth(without, Options{IncludeObserved: true}), analyzeAuth(with, Options{IncludeObserved: true})
	sameAnswer(t, a, b)
	if !slices.ContainsFunc(b.Consumers, func(c AffectedConsumer) bool { return c.Uncontracted }) {
		t.Errorf("the uncontracted legacy caller is no consumer: %+v", b.Consumers)
	}
}

func TestEventIgnoresInferredRelationships(t *testing.T) {
	without, with := inferredEdgeSnaps(t)
	auth := string(fleet.NewServiceKey("auth-service"))
	a, b := event(without, EventUnavailable, auth, Options{IncludeObserved: true}), event(with, EventUnavailable, auth, Options{IncludeObserved: true})
	sameAnswer(t, a, b)
}

func TestImpactTracesIgnoreInferredCaller(t *testing.T) {
	_, snap := inferredEdgeSnaps(t)
	base := analyzeAuth(snap, Options{IncludeObserved: true})
	res := analyzeAuth(snap, Options{IncludeObserved: true, ObservedEdges: []ObservedEdge{{Consumer: legacy, Provider: "auth-service"}}})
	if !reflect.DeepEqual(base.Consumers, res.Consumers) {
		t.Errorf("a trace from the inferred legacy changed the consumers: %+v\nwant %+v", res.Consumers, base.Consumers)
	}
	if slices.ContainsFunc(res.Consumers, func(c AffectedConsumer) bool {
		return !c.Uncontracted && (c.Service == legacy || c.Service == string(fleet.NewServiceKey(legacy)))
	}) {
		t.Errorf("an inferred service is a contracted consumer: %+v", res.Consumers)
	}
	if res.Gate == GateClear {
		t.Errorf("a trace no contract explains cleared the gate: %s", res.GateReason)
	}
	if l := limitation(res, ObservedIdentityUnresolved); l == nil || !strings.Contains(l.Message, legacy+" is an inferred service no contract describes") {
		t.Errorf("the inferred caller is not disclosed: %+v", l)
	}
	if res.Completeness != fleet.CompletenessPartial {
		t.Errorf("completeness %s with a caller no contract describes", res.Completeness)
	}
}

func TestImpactTracesFromReplacedWorkload(t *testing.T) {
	revs := []fleet.RawRevision{{Bundle: mkBundle(svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"}))}, gwRev(authDep)}
	gw := fleet.RawTarget{Scope: "pay", Kind: "kubernetes", Name: "api-gateway", Service: "api-gateway", Workload: &fleet.WorkloadRef{Kind: "Deployment", Name: "legacy"}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow }},
		fleet.NewMemorySource("test", "memory", &fleet.Collection{Revisions: revs, Targets: []fleet.RawTarget{gw}}), legacyWorkloads())
	if err != nil {
		t.Fatal(err)
	}
	res := analyzeAuth(snap, Options{IncludeObserved: true, ObservedEdges: []ObservedEdge{{Consumer: legacy, Provider: "auth-service"}}})
	if l := limitation(res, ObservedIdentityUnresolved); l != nil {
		t.Errorf("the replaced workload is unresolved: %s", l.Message)
	}
	if slices.ContainsFunc(res.Consumers, func(c AffectedConsumer) bool { return c.Service == legacy || c.Uncontracted }) {
		t.Errorf("the replaced workload is an uncontracted consumer: %+v", res.Consumers)
	}
	if gwc := consumerNamed(t, res, "api-gateway"); gwc.Service == "" {
		t.Errorf("consumers %+v", res.Consumers)
	}
}

func TestEventOnReplacedKeyDisclosesAlias(t *testing.T) {
	created, pacto := evidenceNow.Add(-72*time.Hour), evidenceNow.Add(-time.Hour)
	revs := []fleet.RawRevision{{Bundle: mkBundle(svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"}))}, gwRev(authDep)}
	auth := fleet.RawTarget{Scope: "pay", Kind: "kubernetes", Name: "auth-service", Service: "auth-service", Workload: &fleet.WorkloadRef{Kind: "Deployment", Name: "legacy"}, CreatedAt: &pacto}
	w := fleet.NewMemorySource("k8s-workloads", fleet.SourceKindKubernetesWorkloads, &fleet.Collection{
		InferredServices: []fleet.RawInferredService{{Namespace: "pay", Kind: "Deployment", Name: "legacy", CreatedAt: &created}},
	})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow }},
		fleet.NewMemorySource("test", "memory", &fleet.Collection{Revisions: revs, Targets: []fleet.RawTarget{auth}}), w)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Replacements) != 1 {
		t.Fatalf("replacements %+v", snap.Replacements)
	}
	res := event(snap, EventUnavailable, string(fleet.NewServiceKey(legacy)))
	l := limitation(res, LimitationSubjectReplaced)
	if l == nil {
		t.Fatalf("no %s in %+v", LimitationSubjectReplaced, res.Limitations)
	}
	for _, want := range []string{legacy + " was replaced by auth-service", "(linked by Pacto pay/auth-service target)"} {
		if !strings.Contains(l.Message, want) {
			t.Errorf("message %q lacks %q", l.Message, want)
		}
	}
	if consumerNamed(t, res, "api-gateway").Service == "" {
		t.Errorf("consumers %+v", res.Consumers)
	}
	// The successor's inferred name still holds the gate.
	snap.Services[fleet.NewServiceKey("auth-service")].NameInferred = &contract.Inferred{Field: "service.name", Value: "auth-service", Source: "chart/Chart.yaml:2"}
	if res := event(snap, EventUnavailable, string(fleet.NewServiceKey(legacy))); limitation(res, LimitationServiceNameInferred) == nil {
		t.Errorf("no %s in %+v", LimitationServiceNameInferred, res.Limitations)
	}
}

func TestImpactSubjectReplacedByInferredFrom(t *testing.T) {
	ana := contract.InferredSigner{Subject: "ana@acme.com", Issuer: "https://accounts.google.com", Class: contract.SignerClassHuman}
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Declaration: &contract.InferredDeclaration{
		State: contract.DeclarationDeclared, Asker: ana, InferredFrom: legacy}}
	auth := svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"})
	auth.PactoVersion = "2.5"
	prov := &fleet.BundleProvenance{Source: "test", Signers: []fleet.VerifiedSigner{{Subject: ana.Subject, Issuer: ana.Issuer, Class: ana.Class}}}
	revs := []fleet.RawRevision{
		{Bundle: &contract.Bundle{Contract: auth, FS: fstest.MapFS{contract.InferredRecordFile: {Data: rec.Marshal()}}}, Digest: "sha256:auth", Provenance: prov},
		gwRev(authDep),
	}
	policy := &fleet.DeclarationSignerPolicy{Signers: []fleet.PolicySigner{{Issuer: ana.Issuer, Subjects: `[^@]+@acme\.com`}}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow }, DeclarationSigners: policy},
		fleet.NewMemorySource("test", "memory", &fleet.Collection{Revisions: revs}), legacyWorkloads())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Replacements) != 1 || snap.Replacements[0].Link != "inferred-from" {
		t.Fatalf("replacements %+v limitations %+v", snap.Replacements, snap.Limitations)
	}
	l := limitation(event(snap, EventUnavailable, string(fleet.NewServiceKey(legacy))), LimitationSubjectReplaced)
	if l == nil || !strings.Contains(l.Message, "Declared by ana@acme.com from inferred "+legacy) ||
		!strings.HasSuffix(l.Message, "so this answer is about auth-service") || strings.Contains(l.Message, "Pacto target )") {
		t.Errorf("limitation %+v", l)
	}
}

// TestHandoverInferredFieldsNeverClearGate: the declaration that took over
// legacy leaves auth-service's range unknown and a cluster read proposes
// ^2.0.0. A breaking change to 2.0.0, inside the proposal, still finds the
// merged service's compatibility unknown and never clears the gate.
func TestHandoverInferredFieldsNeverClearGate(t *testing.T) {
	ana := contract.InferredSigner{Subject: "ana@acme.com", Issuer: "https://accounts.google.com", Class: contract.SignerClassHuman}
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1",
			Cluster: &contract.InferredClusterInput{AsOf: "2026-11-14T10:02:00Z", Object: "pay/Deployment/legacy", CallWindow: "24h", MaxItems: 10000}},
		Fields: []contract.Inferred{{Field: "dependencies[auth-service].compatibility", Value: "compatibility unknown", Proposed: "^2.0.0",
			Source: "cluster:fleet reported versions 2.0.0", Confidence: contract.ConfidenceLikely, AsOf: "2026-11-14T09:58:12Z", Completeness: "complete"}},
		Declaration: &contract.InferredDeclaration{State: contract.DeclarationDeclared, Asker: ana, InferredFrom: legacy}}
	dep := authDep
	dep.Compatibility = contract.CompatibilityUnknown
	inv := svcContract("invoices", "0.1.0", contract.Owner{Team: "billing"}, dep)
	inv.PactoVersion = "2.5"
	prov := &fleet.BundleProvenance{Source: "test", Signers: []fleet.VerifiedSigner{{Subject: ana.Subject, Issuer: ana.Issuer, Class: ana.Class}}}
	revs := []fleet.RawRevision{
		{Bundle: mkBundle(svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"}))},
		{Bundle: &contract.Bundle{Contract: inv, FS: fstest.MapFS{contract.InferredRecordFile: {Data: rec.Marshal()}}}, Digest: "sha256:inv", Provenance: prov},
	}
	policy := &fleet.DeclarationSignerPolicy{Signers: []fleet.PolicySigner{{Issuer: ana.Issuer, Subjects: `[^@]+@acme\.com`}}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow }, DeclarationSigners: policy},
		fleet.NewMemorySource("test", "memory", &fleet.Collection{Revisions: revs}), legacyWorkloads())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Replacements) != 1 {
		t.Fatalf("replacements %+v limitations %+v", snap.Replacements, snap.Limitations)
	}
	v, err := fleet.NewQuery(snap).GetService("invoices")
	if err != nil || v.Fields == nil || !slices.ContainsFunc(v.Fields.Items, func(f fleet.FieldProvenance) bool { return f.Proposed == "^2.0.0" }) {
		t.Fatalf("the proposal must reach the fields: %+v %v", v, err)
	}
	res := analyzeAuth(snap, Options{})
	if c := consumerNamed(t, res, "invoices"); c.CompatibilityVerdict != CompatibilityUnknown {
		t.Errorf("the proposal read as a range: %+v", c)
	}
	if res.Gate == GateClear {
		t.Errorf("an inferred proposal cleared the gate: %s", res.GateReason)
	}
}
