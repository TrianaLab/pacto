/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"maps"
	"time"

	"k8s.io/apimachinery/pkg/types"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// NewGates is every gate a Stage may name: the configured providers plus the
// two built-in gates that read the cluster, pacto.contract and pacto.runtime.
func NewGates(c Cluster, providers map[string]delivery.Gate) map[string]delivery.Gate {
	gates := map[string]delivery.Gate{
		delivery.GateContract: clusterGate{c: c, judge: delivery.ContractVerdict},
		delivery.GateRuntime:  clusterGate{c: c, judge: delivery.RuntimeVerdict, soak: true},
	}
	maps.Copy(gates, providers)
	for name, g := range providers {
		if e, ok := g.(delivery.Eval); ok {
			e.Revision = c.revision
			gates[name] = e
		}
	}
	return gates
}

// revision reads the contract the version under check ships on its Stage,
// for the eval gate to tie its runner's answer to the suite in it.
func (c Cluster) revision(ctx context.Context, in delivery.GateInput) delivery.Revision {
	stage := &dv1.Stage{}
	if err := c.Client.Get(ctx, types.NamespacedName{Namespace: in.Stage.Namespace, Name: in.Stage.Name}, stage); err != nil {
		return delivery.Revision{Err: "reading the Stage: " + err.Error()}
	}
	a, err := c.applier(ctx, stage.Namespace, stage.Spec.ApplyRef)
	if err != nil {
		return delivery.Revision{Err: "reading the apply OCIRepository: " + err.Error()}
	}
	return delivery.ResolveRevision(ctx, c.registryFor(a), a.repo, in.Subjects[0])
}

// clusterGate judges the Pacto evaluation of the version under check.
type clusterGate struct {
	c     Cluster
	judge func(delivery.ContractCheck) delivery.Verdict
	// soak holds a Pass until the version ran for the soak param.
	soak bool
}

// Check implements delivery.Gate.
func (g clusterGate) Check(ctx context.Context, in delivery.GateInput, params map[string]string) delivery.Verdict {
	unknown := delivery.Verdict{Result: delivery.Unknown}
	if len(in.Subjects) == 0 {
		unknown.Message = "no version to check"
		return unknown
	}
	stage := &dv1.Stage{}
	if err := g.c.Client.Get(ctx, types.NamespacedName{Namespace: in.Stage.Namespace, Name: in.Stage.Name}, stage); err != nil {
		unknown.Message = "reading the Stage: " + err.Error()
		return unknown
	}
	check, msg := g.c.contractCheck(ctx, stage, in.Subjects[0])
	if msg != "" {
		unknown.Message = msg
		return unknown
	}
	if g.soak {
		return delivery.RuntimeSoak(g.judge(check), in, params)
	}
	return g.judge(check)
}

// contractCheck gathers what the Pacto gates read about digest on the Stage:
// the contract the artifact names, whether it was applied and why not, the service's
// Pactos and the live workloads they evaluated. msg says why it could not.
func (c Cluster) contractCheck(ctx context.Context, stage *dv1.Stage, digest string) (delivery.ContractCheck, string) {
	a, err := c.applier(ctx, stage.Namespace, stage.Spec.ApplyRef)
	if err != nil {
		return delivery.ContractCheck{}, "reading the apply OCIRepository: " + err.Error()
	}
	ann, err := c.registryFor(a).Annotations(ctx, a.repo+"@"+digest)
	if err != nil {
		return delivery.ContractCheck{}, "reading the artifact: " + err.Error()
	}
	ok, _, why, err := c.applied(ctx, stage.Namespace, a, digest)
	if err != nil {
		why = "reading the Kustomizations: " + err.Error()
	}
	pactos, err := c.pactos(ctx)
	if err != nil {
		return delivery.ContractCheck{}, "listing Pactos: " + err.Error()
	}
	return delivery.ContractCheck{
		ContractRef: ann[delivery.ContractAnnotation], Applied: ok, AppliedWhy: why,
		Pactos:   delivery.PactosFor(pactos, stage.Namespace, stage.Spec.Service.Name),
		Workload: c.workload(ctx, stage.Namespace),
	}, ""
}

// verifyChecks is what verifies a version on the Stage: the Pacto contract
// check, then the Stage's own checks.
func verifyChecks(stage *dv1.Stage) []delivery.GateCheck {
	return append([]delivery.GateCheck{{Name: delivery.GateContract, Provider: delivery.GateContract}}, stage.Spec.Gates.Verify.Checks...)
}

// gateInput is the gate protocol input for op's version on the Stage over
// [from, to].
func gateInput(op *dv1.Operation, stage *dv1.Stage, from, to time.Time) delivery.GateInput {
	in := subjectInput(string(op.UID), op.Spec.Facet, op.Spec.Params.To, stage, from, to)
	if p := op.Status.Plan; p != nil && p.To == op.Spec.Params.To {
		in.Version = p.ToVersion
	}
	return in
}

// subjectInput is the gate protocol input for digest on the Stage over
// [from, to]; uid is the operation it is checked for, empty for a shadow
// verify of what GitOps applied.
func subjectInput(uid, facet, digest string, stage *dv1.Stage, from, to time.Time) delivery.GateInput {
	return delivery.GateInput{OperationUID: uid, Stage: delivery.StageRef{Namespace: stage.Namespace, Name: stage.Name},
		Service: stage.Spec.Service, Facet: facet, Subjects: []string{digest},
		Window: delivery.Window{From: from, To: to}}
}

// failing returns the first Fail among vs.
func failing(vs []delivery.Verdict) *delivery.Verdict {
	for i := range vs {
		if vs[i].Result == delivery.Fail {
			return &vs[i]
		}
	}
	return nil
}
