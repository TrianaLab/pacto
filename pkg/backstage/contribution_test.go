package backstage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const (
	ctbRoot     = "ghcr.io/acme"
	ctbProducer = "acme-portal"
	ctbFile     = "export.json"
	ctbData     = `{"legacy-billing":["payments"]}`
)

// ctbKey is the service key of name contributed by acme-portal.
func ctbKey(name string) string {
	return string(fleet.NewServiceKeyDomain(ctbRoot+"/pacto-inferred/"+ctbProducer, name))
}

// contributed is name's contribution by acme-portal, made asOf, with an
// inferred dependency on each of deps, naming offering when it is set.
func contributed(name, offering string, asOf time.Time, deps ...string) fleet.RawRevision {
	sum := sha256.Sum256([]byte(ctbData))
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1", Sources: []contract.InferredSourceFile{{Path: ctbFile, Digest: "sha256:" + hex.EncodeToString(sum[:])}}},
		Fields: []contract.Inferred{}}
	for _, d := range deps {
		rec.Fields = append(rec.Fields, contract.Inferred{Field: "dependencies[" + d + "]", Value: d, Source: ctbFile + ":1", Confidence: contract.ConfidenceLikely})
	}
	raw := "pactoVersion: \"2.5\"\nservice:\n  name: " + name + "\n  version: 0.1.0\n"
	if offering != "" {
		raw += "  offering: " + offering + "\n"
	}
	c := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: name, Version: "0.1.0", Offering: offering}}
	repo := ctbRoot + "/pacto-inferred/" + ctbProducer + "/services"
	digest := "sha256:" + name
	return fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, RawYAML: []byte(raw), FS: fstest.MapFS{ctbFile: {Data: []byte(ctbData)}, contract.InferredRecordFile: {Data: rec.Marshal()}}},
		ResolvedRef: repo + "@" + digest, Digest: digest,
		Contributor: &fleet.Contributor{Producer: ctbProducer, KeyID: "key-1", ReadAt: fixed, AsOf: asOf}}
}

// ctbMap exports a fleet of the declared revisions and the contributions,
// read from an Evidence Server, with a 24h freshness window.
func ctbMap(t *testing.T, declared []fleet.RawRevision, contributions ...fleet.RawRevision) []Entity {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixed }, FreshnessWindow: 24 * time.Hour},
		fleet.NewMemorySource("registry", "local", &fleet.Collection{Revisions: declared}),
		fleet.NewMemorySource("evidence", fleet.SourceKindEvidenceHTTP, &fleet.Collection{Revisions: contributions}))
	if err != nil {
		t.Fatal(err)
	}
	return mapSnap(snap)
}

func TestContributedServiceExportsInferred(t *testing.T) {
	es := ctbMap(t, []fleet.RawRevision{rev(ctbRoot, "payments", "1.0.0")}, contributed("legacy-billing", "", fixed.Add(-time.Hour), "payments"))
	c := byKey(t, es, ctbKey("legacy-billing"))
	ann := c.Metadata.Annotations
	if ann[AnnotationProvenance] != provenanceInferred || !slices.Equal(c.Metadata.Tags, []string{TagInferred}) || ann[AnnotationContributedBy] != ctbProducer {
		t.Errorf("component = %+v", c.Metadata)
	}
	if ann[AnnotationInferredDependsOn] != "component:default/payments" || len(c.Spec.DependsOn) != 0 {
		t.Errorf("inferred-depends-on = %q, dependsOn = %v", ann[AnnotationInferredDependsOn], c.Spec.DependsOn)
	}
	if ann[AnnotationCompleteness] != string(fleet.CompletenessComplete) {
		t.Errorf("fresh contribution completeness = %q", ann[AnnotationCompleteness])
	}
	if p := byKey(t, es, string(fleet.NewServiceKeyDomain(ctbRoot, "payments"))); p.Metadata.Annotations[AnnotationContributedBy] != "" {
		t.Errorf("declared service names a producer: %v", p.Metadata.Annotations)
	}
}

// A contributed service a running target shows reads observed, as a
// workload-inferred service does: the target is the fact, and the
// contribution never makes it declared.
func TestContributedServiceWithTargetExportsObserved(t *testing.T) {
	c0 := contributed("legacy-billing", "", fixed.Add(-time.Hour))
	running := target("legacy-billing", c0.Digest, contract.Owner{})
	running.Domain, running.ResolvedRef = ctbRoot+"/pacto-inferred/"+ctbProducer, c0.ResolvedRef
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixed }, FreshnessWindow: 24 * time.Hour},
		fleet.NewMemorySource("evidence", fleet.SourceKindEvidenceHTTP, &fleet.Collection{Revisions: []fleet.RawRevision{c0}}),
		fleet.NewMemorySource("cluster", fleet.SourceKindKubernetes, &fleet.Collection{Targets: []fleet.RawTarget{running}}))
	if err != nil {
		t.Fatal(err)
	}
	c := byKey(t, mapSnap(snap), ctbKey("legacy-billing"))
	if ann := c.Metadata.Annotations; ann[AnnotationProvenance] != fleet.ProvenanceObserved || !slices.Equal(c.Metadata.Tags, []string{TagObserved}) || ann[AnnotationContributedBy] != ctbProducer {
		t.Errorf("component = %+v", c.Metadata)
	}
}

func TestStaleContributionExportsPartial(t *testing.T) {
	refused := contributed("refused", "", fixed.Add(-time.Hour))
	refused.Contributor.NewerRefused = "2.0.0"
	es := ctbMap(t, []fleet.RawRevision{rev(ctbRoot, "payments", "1.0.0")}, refused, contributed("undated", "", time.Time{}))
	for _, name := range []string{"refused", "undated"} {
		if got := byKey(t, es, ctbKey(name)).Metadata.Annotations[AnnotationCompleteness]; got != string(fleet.CompletenessPartial) {
			t.Errorf("%s completeness = %q", name, got)
		}
	}
	for _, e := range es {
		if k := e.Metadata.Annotations[AnnotationServiceKey]; k == ctbKey("refused") || k == ctbKey("undated") {
			continue
		}
		if got := e.Metadata.Annotations[AnnotationCompleteness]; got != string(fleet.CompletenessComplete) {
			t.Errorf("%s %s completeness = %q", e.Kind, e.Metadata.Name, got)
		}
	}
}

func TestOfferingAnnotation(t *testing.T) {
	es := mapCols(t, &fleet.Collection{Revisions: []fleet.RawRevision{
		rev(ctbRoot, "pg", "1.0.0", func(c *contract.Contract) { c.Service.Offering = "postgresql" }),
		rev(ctbRoot, "checkout", "1.0.0"),
	}})
	if got := byKey(t, es, string(fleet.NewServiceKeyDomain(ctbRoot, "pg"))).Metadata.Annotations[AnnotationOffering]; got != "postgresql" {
		t.Errorf("offering = %q", got)
	}
	if _, ok := byKey(t, es, string(fleet.NewServiceKeyDomain(ctbRoot, "checkout"))).Metadata.Annotations[AnnotationOffering]; ok {
		t.Error("a service that provides no offering names one")
	}
}
