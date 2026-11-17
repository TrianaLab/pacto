package impact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const inferredManifest = "kind: Deployment\nspec:\n  env:\n    - name: WORKER_URL\n"

// inferredRecordFor is a valid record over inferredManifest naming worker
// with value.
func inferredRecordFor(value string) []byte {
	sum := sha256.Sum256([]byte(inferredManifest))
	return (&contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1", Sources: []contract.InferredSourceFile{
			{Path: "deploy/app.yaml", Digest: "sha256:" + hex.EncodeToString(sum[:])}}},
		Fields: []contract.Inferred{{Field: "dependencies[notification-worker]", Value: value, Source: "deploy/app.yaml:4", Confidence: contract.ConfidenceLikely}},
	}).Marshal()
}

// newsletterRev is a 2.5 newsletter revision at digest whose bundle holds
// record (none when nil).
func newsletterRev(digest string, record []byte, deps ...contract.Dependency) fleet.RawRevision {
	c := svcContract("newsletter", "1.0.0", contract.Owner{Team: "news"}, deps...)
	c.PactoVersion = "2.5"
	fsys := fstest.MapFS{"deploy/app.yaml": {Data: []byte(inferredManifest)}}
	if record != nil {
		fsys[contract.InferredRecordFile] = &fstest.MapFile{Data: record}
	}
	return fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fsys}, Digest: digest}
}

// workerFleet holds notification-worker and the given newsletter revisions,
// both running.
func workerFleet(t *testing.T, news ...fleet.RawRevision) *fleet.FleetSnapshot {
	t.Helper()
	worker := svcContract("notification-worker", "1.0.0", contract.Owner{Team: "platform"})
	return buildAt(t, &fleet.Collection{
		Revisions: append([]fleet.RawRevision{{Bundle: mkBundle(worker), Digest: "sha256:worker"}}, news...),
		Targets:   []fleet.RawTarget{target("notification-worker", fresh()), target("newsletter", fresh())},
	})
}

// breakWorker analyzes a BREAKING change of notification-worker 1.0.0 to 2.0.0.
func breakWorker(snap *fleet.FleetSnapshot) *Result {
	old := svcContract("notification-worker", "1.0.0", contract.Owner{Team: "platform"})
	old.Interfaces = []contract.Interface{{Name: "jobs", Type: contract.InterfaceTypeGRPC, Ref: "jobs.proto"}}
	return Analyze(context.Background(), old, svcContract("notification-worker", "2.0.0", contract.Owner{Team: "platform"}),
		fstest.MapFS{}, fstest.MapFS{}, snap, Options{})
}

func wantInferredOnly(t *testing.T, c AffectedConsumer) {
	t.Helper()
	if c.Provenance != fleet.ProvenanceInferred || c.Confidence != ConfidenceInferred || c.CompatibilityVerdict != CompatibilityUnknown ||
		!reflect.DeepEqual(c.Via, []string{fleet.ProvenanceInferred}) || !c.Direct {
		t.Errorf("newsletter = %+v", c)
	}
	// The record source rides along, so a reader can find which file named it.
	if !slices.ContainsFunc(c.Evidence, func(e Evidence) bool {
		return e.Kind == EvidenceInferredRecord && e.Provenance == fleet.ProvenanceInferred && e.Message == "named by its inferred record, read from deploy/app.yaml:4"
	}) {
		t.Errorf("newsletter evidence = %+v", c.Evidence)
	}
}

func TestInferredOnlyConsumerNeverClears(t *testing.T) {
	res := breakWorker(workerFleet(t, newsletterRev("sha256:news", inferredRecordFor("notification-worker"))))
	if res.Classification != "BREAKING" || res.Gate != GateUnverified {
		t.Fatalf("classification %s, gate %s (%s)", res.Classification, res.Gate, res.GateReason)
	}
	// The reason never states the dependency as declared, and asks for the
	// declaration no target state could replace.
	if want := "the change is BREAKING and 1 service may depend on it, named only by its inferred record, so Pacto cannot check its compatibility: " +
		"declare the dependency with a compatibility range in that service's pacto.yaml"; res.GateReason != want {
		t.Errorf("reason = %q", res.GateReason)
	}
	wantInferredOnly(t, consumerNamed(t, res, "newsletter"))
	// Without the record nothing depends on notification-worker, so the gate
	// clears: the record is what keeps it from clearing.
	if clear := breakWorker(workerFleet(t, newsletterRev("sha256:news", nil))); clear.Gate != GateClear || len(clear.Consumers) != 0 {
		t.Errorf("without the record: gate %s, consumers %+v", clear.Gate, clear.Consumers)
	}
}

func TestUnreadableRecordNeverClears(t *testing.T) {
	res := breakWorker(workerFleet(t, newsletterRev("sha256:news", []byte("format: pacto.inferred/v0\n"))))
	if res.Gate != GateUnverified || res.Completeness != fleet.CompletenessPartial || limitation(res, fleet.LimitationInferredRecordUnreadable) == nil {
		t.Errorf("gate %s, completeness %s, limitations %+v", res.Gate, res.Completeness, res.Limitations)
	}
}

func TestInferredOnlyConsumerEventMode(t *testing.T) {
	snap := workerFleet(t, newsletterRev("sha256:news", inferredRecordFor("notification-worker")))
	for _, kind := range []string{EventRemoved, EventUnavailable} {
		c := consumerNamed(t, event(snap, kind, "notification-worker"), "newsletter")
		wantInferredOnly(t, c)
		if c.Effect != EffectUnknown {
			t.Errorf("%s: effect %s", kind, c.Effect)
		}
	}
	// The edge points one way: newsletter's own event names no consumer.
	if res := event(snap, EventUnavailable, "newsletter"); len(res.Consumers) != 0 {
		t.Errorf("newsletter consumers = %+v", res.Consumers)
	}
}

func TestInferredEdgeNeverCompatible(t *testing.T) {
	// A value a range check would accept must never reach a verdict.
	res := breakWorker(workerFleet(t, newsletterRev("sha256:news", inferredRecordFor("^2.0.0"))))
	if c := consumerNamed(t, res, "newsletter"); c.CompatibilityVerdict != CompatibilityUnknown || res.Gate == GateClear {
		t.Errorf("verdict %s, gate %s", c.CompatibilityVerdict, res.Gate)
	}
}

func TestDeclaredVerdictWinsOverInferred(t *testing.T) {
	declared := contract.Dependency{Name: "notification-worker", Ref: "ghcr.io/x/notification-worker:2.0.0", Required: true, Compatibility: "^2.0.0"}
	res := breakWorker(workerFleet(t, newsletterRev("sha256:news-a", nil, declared), newsletterRev("sha256:news-b", inferredRecordFor("notification-worker"))))
	c := consumerNamed(t, res, "newsletter")
	if c.CompatibilityVerdict != CompatibilityCompatible || c.Provenance != fleet.ProvenanceDeclared ||
		!reflect.DeepEqual(c.Via, []string{fleet.RelationshipDependency, fleet.ProvenanceInferred}) {
		t.Errorf("newsletter = %+v", c)
	}
}
