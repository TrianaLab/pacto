package delivery

import (
	"context"
	"errors"
	"testing"
)

// fakeSources is PlanSources from fixed answers.
type fakeSources struct {
	from, to  Revision
	upstream  *StageStatus
	why       string
	upErr     error
	stages    []StageObject
	described []string
}

func (f *fakeSources) Revisions(context.Context, StageObject, string, string) (Revision, Revision) {
	return f.from, f.to
}
func (f *fakeSources) Upstream(context.Context, StageObject) (*StageStatus, string, error) {
	return f.upstream, f.why, f.upErr
}
func (f *fakeSources) Pactos(context.Context) ([]PactoView, error)   { return nil, nil }
func (f *fakeSources) Stages(context.Context) ([]StageObject, error) { return f.stages, nil }
func (f *fakeSources) Kustomizations(context.Context) ([]KustomizationView, error) {
	return nil, errors.New("no kustomizations")
}
func (f *fakeSources) Describe(_ context.Context, _ StageObject, d string) Revision {
	f.described = append(f.described, d)
	return Revision{Digest: d}
}

// TestAssemblePlanInput: a promote reads both sides, the upstream, the Pactos,
// Stages and Kustomizations into one input; an upstream read that fails stops
// planning; any other pointer move reads only its To side, so its plan
// carries the version a version-matching verify gate checks.
func TestAssemblePlanInput(t *testing.T) {
	stage := StageObject{Metadata: ObjectMeta{Namespace: "prod", Name: "payments"}, Spec: stageSpec(), Status: StageStatus{Pointer: dA}}
	src := &fakeSources{from: rev(dA, c1, bundle("payments", "1.0.0")), to: rev(dB, c2, bundle("payments", "1.1.0")),
		upstream: &StageStatus{Offered: []string{dB}}, why: "", stages: []StageObject{stage}}
	in, err := AssemblePlanInput(context.Background(), src, promote(dA, dB), stage, ClassAgent, now)
	if err != nil || in.To.Bundle == nil || in.Upstream == nil || len(in.Stages) != 1 || in.Snapshot == nil || in.Class != ClassAgent || in.Status.Pointer != dA {
		t.Fatalf("promote input: %v %+v", err, in)
	}
	if in.Candidates(context.Background(), stage, dC); len(src.described) != 1 {
		t.Fatal("candidates are read through the sources")
	}
	src.upErr = errors.New("apiserver down")
	if _, err := AssemblePlanInput(context.Background(), src, promote(dA, dB), stage, ClassAgent, now); err == nil {
		t.Fatal("a failed upstream read must stop planning")
	}
	rollback := promote(dB, dA)
	rollback.Type = TypeRollback
	src.upErr, src.to = nil, rev(dA, c1, bundle("payments", "1.0.0"))
	in, err = AssemblePlanInput(context.Background(), src, rollback, stage, ClassAgent, now)
	if err != nil || in.To.Bundle == nil || in.From.Bundle != nil || in.Snapshot != nil || in.Upstream != nil {
		t.Fatalf("a rollback reads only its To side: %v %+v", err, in)
	}
	if p := PlanFor(context.Background(), in); p.ToVersion != "1.0.0" {
		t.Fatalf("a rollback's plan carries the version it rolls back to, got %q", p.ToVersion)
	}
	freeze := OperationSpec{Type: TypeFreeze, Facet: FacetFreeze}
	if in, err := AssemblePlanInput(context.Background(), src, freeze, stage, ClassAgent, now); err != nil || in.To.Bundle != nil {
		t.Fatalf("a freeze reads nothing: %v %+v", err, in)
	}
}
