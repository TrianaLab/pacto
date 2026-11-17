/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"slices"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
)

// detectApplier reads, and never writes, who manages the workloads the Stage
// delivers: the workload each Pacto of the Stage's service in the Stage's own
// namespace evaluated, as verify reads them. A workload that is gone is no
// target; one Pacto cannot read is named in Unread, which reads Unknown.
// ponytail: one namespaced Pacto list and one GET per workload per reconcile;
// an indexed cache if perf_envtest shows it.
func (c Cluster) detectApplier(ctx context.Context, stage *dv1.Stage, now time.Time) delivery.ApplierStatus {
	in := delivery.ApplierInput{Namespace: stage.Namespace, Ref: stage.Spec.ApplyRef, Now: now}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvkPactoList)
	if err := c.Client.List(ctx, list, client.InNamespace(stage.Namespace)); err != nil {
		in.Unread = append(in.Unread, "the Pactos in "+stage.Namespace+": "+err.Error())
		return delivery.AdoptApplier(in)
	}
	b, _ := list.MarshalJSON()
	pactos, _ := delivery.DecodePactos(b) // ponytail: the apiserver's own JSON decodes
	for _, p := range delivery.PactosFor(pactos, stage.Namespace, stage.Spec.Service.Name) {
		e := p.Status.EvaluatedRevision
		if e == nil || e.Workload.Name == "" {
			continue
		}
		what := e.Workload.Kind + " " + e.Workload.Name
		o, err := c.workloadObject(ctx, stage.Namespace, e.Workload.Kind, e.Workload.Name)
		switch {
		case apierrors.IsNotFound(err):
		case err != nil:
			in.Unread = append(in.Unread, what+": "+err.Error())
		default:
			in.Targets = append(in.Targets, delivery.ApplierTarget{Workload: what, Manager: workloadmanager.Detect(o)})
		}
	}
	if slices.ContainsFunc(in.Targets, func(t delivery.ApplierTarget) bool { return t.Manager.Name == workloadmanager.FluxKustomization }) {
		in.Kustomizations, in.Unread = c.detectKustomizations(ctx, in.Unread)
	}
	return delivery.AdoptApplier(in)
}

// detectKustomizations lists the Kustomizations a Flux-labelled workload
// names, or says why it cannot.
func (c Cluster) detectKustomizations(ctx context.Context, unread []string) ([]delivery.KustomizationView, []string) {
	if c.NoFlux {
		return nil, append(unread, "the Kustomizations: the promoter runs with --flux=false")
	}
	ks, err := c.kustomizations(ctx)
	if err != nil {
		return nil, append(unread, "the Kustomizations: "+err.Error())
	}
	return ks, unread
}

// workloadObject reads the metadata of the workload kind/name in ns, the
// same kinds workload() reads.
func (c Cluster) workloadObject(ctx context.Context, ns, kind, name string) (workloadmanager.Object, error) {
	group := "apps"
	if kind == "Job" || kind == "CronJob" {
		group = "batch"
	}
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{Group: group, Version: "v1", Kind: kind})
	if err := c.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, u); err != nil {
		return workloadmanager.Object{}, err
	}
	o := workloadmanager.Object{Group: group, Kind: kind, Namespace: ns, Name: name, Labels: u.GetLabels(), Annotations: u.GetAnnotations()}
	for _, f := range u.GetManagedFields() {
		o.Managers = append(o.Managers, workloadmanager.FieldManager{Manager: f.Manager, Subresource: f.Subresource})
	}
	for _, r := range u.GetOwnerReferences() {
		if r.Controller != nil && *r.Controller {
			o.Controller = &workloadmanager.OwnerRef{Kind: r.Kind, Name: r.Name}
		}
	}
	return o, nil
}
