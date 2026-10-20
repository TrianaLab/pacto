/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
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
		delivery.GateRuntime:  clusterGate{c: c, judge: delivery.RuntimeVerdict},
	}
	for name, g := range providers {
		gates[name] = g
	}
	return gates
}

// clusterGate judges the Pacto evaluation of the version under check.
type clusterGate struct {
	c     Cluster
	judge func(delivery.ContractCheck) delivery.Verdict
}

// Check implements delivery.Gate.
func (g clusterGate) Check(ctx context.Context, in delivery.GateInput, _ map[string]string) delivery.Verdict {
	unknown := delivery.Verdict{Result: delivery.Unknown}
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
	return g.judge(check)
}

// contractCheck gathers what the Pacto gates read about digest on the Stage:
// the contract the artifact names, whether Flux applied it, the service's
// Pactos and the live workloads they evaluated. msg says why it could not.
func (c Cluster) contractCheck(ctx context.Context, stage *dv1.Stage, digest string) (delivery.ContractCheck, string) {
	a, err := c.ociRepository(ctx, stage.Namespace, stage.Spec.ApplyRef.Name)
	if err != nil {
		return delivery.ContractCheck{}, "reading the apply OCIRepository: " + err.Error()
	}
	ann, err := c.registryFor(a).Annotations(ctx, a.repo+"@"+digest)
	if err != nil {
		return delivery.ContractCheck{}, "reading the artifact: " + err.Error()
	}
	ok, _, _, _ := c.applied(ctx, stage.Namespace, a, digest)
	pactos, err := c.pactos(ctx)
	if err != nil {
		return delivery.ContractCheck{}, "listing Pactos: " + err.Error()
	}
	return delivery.ContractCheck{
		ContractRef: ann[delivery.ContractAnnotation], Applied: ok,
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
	return delivery.GateInput{OperationUID: string(op.UID), Stage: delivery.StageRef{Namespace: stage.Namespace, Name: stage.Name},
		Service: stage.Spec.Service, Facet: op.Spec.Facet, Subjects: []string{op.Spec.Params.To},
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
