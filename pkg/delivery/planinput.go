package delivery

import (
	"context"
	"errors"
	"time"
)

// PlanSources is what planning reads beyond the operation and its Stage. The
// promoter and every client implement it over their own clients, so all of
// them gather a plan's input the same way (AssemblePlanInput) and predict
// what the promoter does.
type PlanSources interface {
	// Revisions resolves from and to through the Stage's apply repository,
	// each with Err set when it could not be read.
	Revisions(ctx context.Context, stage StageObject, from, to string) (Revision, Revision)
	// Upstream is the status of the Stage this one takes its candidates
	// from: nil with no upstream, and nil with why when it does not exist,
	// may not be read or its records gave nothing. An error is a read that
	// failed otherwise: planning stops rather than judge without it.
	Upstream(ctx context.Context, stage StageObject) (status *StageStatus, why string, err error)
	Pactos(ctx context.Context) ([]PactoView, error)
	Stages(ctx context.Context) ([]StageObject, error)
	Kustomizations(ctx context.Context) ([]KustomizationView, error)
	// Describe reads what a digest on a consumer's Stage carries, bounded
	// (PlanInput.Candidates).
	Describe(ctx context.Context, stage StageObject, digest string) Revision
}

// AssemblePlanInput gathers everything PlanFor reads for spec on stage, raised
// by an actor of class. Only a promote reads beyond the operation itself,
// apart from the To revision of any other pointer move: its contract version
// is what a version-matching verify gate checks (Plan.ToVersion).
func AssemblePlanInput(ctx context.Context, src PlanSources, spec OperationSpec, stage StageObject, class ActorClass, now time.Time) (PlanInput, error) {
	in := PlanInput{Spec: spec, Stage: stage.Spec, Status: stage.Status, Class: class,
		From: Revision{Digest: spec.Params.From}, To: Revision{Digest: spec.Params.To}}
	if spec.Type == TypeAutonomy {
		// A raise asks the team of the contract running now: the pointer,
		// or what GitOps' source reports while the Stage observes. Level
		// names are not digests, so nothing else is read.
		in.From, in.To = Revision{}, Revision{}
		running := stage.Status.Pointer
		if running == "" && stage.Status.ObservedRevision != nil {
			running = stage.Status.ObservedRevision.Digest
		}
		if running != "" && Level(spec.Params.To).rank() > Level(spec.Params.From).rank() {
			in.From, _ = src.Revisions(ctx, stage, running, running)
		}
		return in, nil
	}
	// A rollback a person or agent raised is projected onto the graph too
	// (rollbackImpact); the promoter's own rollbacks read nothing more.
	graph := spec.Type == TypePromote || spec.Type == TypeRollback && class != ClassController
	if !graph {
		if spec.Facet == FacetPointer && spec.Params.To != "" {
			_, in.To = src.Revisions(ctx, stage, spec.Params.From, spec.Params.To)
		}
		return in, nil
	}
	var err error
	if spec.Type == TypePromote {
		if in.Upstream, in.UpstreamErr, err = src.Upstream(ctx, stage); err != nil {
			return in, err
		}
	}
	in.From, in.To = src.Revisions(ctx, stage, spec.Params.From, spec.Params.To)
	pactos, listErr := src.Pactos(ctx)
	in.Stages, in.StagesErr = src.Stages(ctx)
	ks, ksErr := src.Kustomizations(ctx)
	pactos, in.Misplaced = EnvironmentPactos(pactos, in.Stages, ks, errors.Join(in.StagesErr, ksErr), stage.Spec.Environment)
	in.Candidates = src.Describe
	in.Snapshot, err = Snapshot(ctx, pactos, listErr, []Revision{in.From, in.To}, now)
	return in, err
}
