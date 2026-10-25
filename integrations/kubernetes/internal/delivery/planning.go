/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"cmp"
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

func planRefusal(p delivery.Plan) string {
	for _, v := range p.Verdicts {
		switch {
		case v.Result == delivery.Fail && v.Gate == delivery.GatePublished:
			return "NotPublished: " + v.Message
		case v.Result == delivery.Fail:
			return "GateFailed: " + v.Gate + ": " + v.Message
		}
	}
	return p.Conflicts[0]
}

// newerPromote returns the name of a newer promote on the same Stage that has
// not moved the pointer, ordered by (creationTimestamp, uid).
func (r *OperationReconciler) newerPromote(ctx context.Context, op *dv1.Operation) (string, error) {
	ops, err := stageOps(ctx, r.Client, op.Namespace, op.Spec.Stage)
	if err != nil {
		return "", err
	}
	for _, o := range ops {
		if o.Spec.Type != delivery.TypePromote || o.Status.Applied || o.Status.Phase == delivery.PhaseConcluded || o.UID == op.UID {
			continue
		}
		if c := o.CreationTimestamp.Compare(op.CreationTimestamp.Time); c > 0 || c == 0 && o.UID > op.UID {
			return o.Name, nil
		}
	}
	return "", nil
}

// plan gathers plan()'s inputs from the cluster and the registry and runs it.
// A refusal is final, so it is never decided on a cached Stage that lags the
// apiserver: a refused plan reads the Stage uncached and, if it moved, plans
// again on it, updating stage in place.
func (r *OperationReconciler) plan(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (delivery.Plan, error) {
	p, err := r.planOn(ctx, op, stage)
	if err != nil || p.Predicted != delivery.PredictRejected {
		return p, err
	}
	fresh := &dv1.Stage{}
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(stage), fresh); err != nil {
		return delivery.Plan{}, err
	}
	if fresh.ResourceVersion == stage.ResourceVersion {
		return p, nil
	}
	*stage = *fresh
	return r.planOn(ctx, op, stage)
}

func (r *OperationReconciler) planOn(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (delivery.Plan, error) {
	in, err := delivery.AssemblePlanInput(ctx, planSources{r}, op.Spec, stageObject(stage), op.Status.ActorClass, r.now())
	if err != nil {
		return delivery.Plan{}, err
	}
	in.RequireOwnerTeam = r.Policy.RequireOwnerTeam
	return delivery.PlanFor(ctx, in), nil
}

func stageObject(s *dv1.Stage) delivery.StageObject {
	return delivery.StageObject{Metadata: delivery.ObjectMeta{Name: s.Name, Namespace: s.Namespace, UID: string(s.UID)}, Spec: s.Spec, Status: s.Status}
}

// planSources is delivery.PlanSources over the promoter's own clients.
type planSources struct{ r *OperationReconciler }

func (p planSources) Revisions(ctx context.Context, stage delivery.StageObject, from, to string) (delivery.Revision, delivery.Revision) {
	a, err := p.r.Cluster.applier(ctx, stage.Metadata.Namespace, stage.Spec.ApplyRef)
	if err != nil {
		why := "the apply OCIRepository: " + err.Error()
		return delivery.Revision{Digest: from, Err: why}, delivery.Revision{Digest: to, Err: why}
	}
	reg := p.r.Cluster.registryFor(a)
	return delivery.ResolveRevision(ctx, reg, a.repo, from), delivery.ResolveRevision(ctx, reg, a.repo, to)
}

func (p planSources) Pactos(ctx context.Context) ([]delivery.PactoView, error) {
	return p.r.Cluster.pactos(ctx)
}

func (p planSources) Kustomizations(ctx context.Context) ([]delivery.KustomizationView, error) {
	return p.r.Cluster.kustomizations(ctx)
}

func (p planSources) Describe(ctx context.Context, stage delivery.StageObject, digest string) delivery.Revision {
	return p.r.Cluster.describe(ctx, stage, digest)
}

// Stages lists every Stage, so a blocked plan can place its consumers.
func (p planSources) Stages(ctx context.Context) ([]delivery.StageObject, error) {
	list := &dv1.StageList{}
	if err := p.r.List(ctx, list); err != nil {
		return nil, err
	}
	out := make([]delivery.StageObject, 0, len(list.Items))
	for i := range list.Items {
		s := stageObject(&list.Items[i])
		s.Metadata.UID = ""
		out = append(out, s)
	}
	return out, nil
}

// Upstream reads the status of the Stage this one takes its candidates from.
// It is nil when there is none, and nil with why when it does not exist or
// the promoter may not read it: the published gate then refuses, as nothing
// it offers is known. Any other read error is returned, so the operation is
// planned again. A Stage in another cluster is read from its signed records,
// and why says why that gave nothing.
func (p planSources) Upstream(ctx context.Context, stage delivery.StageObject) (*delivery.StageStatus, string, error) {
	if stage.Spec.Candidates.UpstreamRecords != nil {
		s, why := p.r.Cluster.remote(ctx, &dv1.Stage{Spec: stage.Spec, Status: stage.Status})
		return s, why, nil
	}
	up := stage.Spec.Candidates.UpstreamStage
	if up == nil {
		return nil, "", nil
	}
	ns := cmp.Or(up.Namespace, stage.Metadata.Namespace)
	s := &dv1.Stage{}
	if err := p.r.Get(ctx, types.NamespacedName{Namespace: ns, Name: up.Name}, s); err != nil {
		if apierrors.IsNotFound(err) || apierrors.IsForbidden(err) {
			return nil, "the upstream Stage " + ns + "/" + up.Name + " cannot be read: " + err.Error(), nil
		}
		return nil, "", err
	}
	return &s.Status, "", nil
}
