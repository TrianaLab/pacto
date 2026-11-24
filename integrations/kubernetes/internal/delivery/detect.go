/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"cmp"
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
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
// target; one Pacto cannot read is named in Unread, which reads Unknown. So
// is a Pacto the operator has not reconciled yet, which may be the service's,
// and one that evaluated another workload than the Stage runs (a decoy, as
// verify calls it).
// ponytail: one namespaced Pacto list and one GET per workload per reconcile;
// an indexed cache if perf_envtest shows it.
func (c Cluster) detectApplier(ctx context.Context, stage *dv1.Stage, now time.Time) delivery.ApplierStatus {
	in := delivery.ApplierInput{Namespace: stage.Namespace, Service: stage.Spec.Service.Name, Ref: stage.Spec.ApplyRef, Now: now}
	gvk := applierGVK(stage.Spec.ApplyRef.Kind)
	if _, err := c.Client.RESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version); meta.IsNoMatchError(err) {
		// No Flux or Argo CD serves the applyRef's kind.
		in.NoAPI = true
		return delivery.AdoptApplier(in)
	}
	in.Values = c.selectedValues(ctx, stage)
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvkPactoList)
	if err := c.Client.List(ctx, list, client.InNamespace(stage.Namespace)); err != nil {
		in.Unread = append(in.Unread, "the Pactos in "+stage.Namespace+": "+err.Error())
		return delivery.AdoptApplier(in)
	}
	b, _ := list.MarshalJSON()
	pactos, _ := delivery.DecodePactos(b) // ponytail: the apiserver's own JSON decodes
	for _, p := range pactos {
		// ponytail: only a never-reconciled Pacto counts; an Invalid one of
		// another service in the namespace would otherwise block every Stage.
		if p.Status.Contract == nil && p.Status.ContractStatus == "" {
			in.Unread = append(in.Unread, "Pacto "+p.Metadata.Name+", which the operator has not reconciled yet")
		}
	}
	runs := stage.Spec.WorkloadOf()
	for _, p := range delivery.PactosFor(pactos, stage.Namespace, stage.Spec.Service.Name) {
		e := p.Status.EvaluatedRevision
		switch {
		case e == nil:
			in.Unread = append(in.Unread, "Pacto "+p.Metadata.Name+", which has not evaluated a revision yet")
			continue
		case e.Workload.Name == "":
			continue
		case e.Workload.Kind != runs.Kind || e.Workload.Name != runs.Name:
			in.Unread = append(in.Unread, "Pacto "+p.Metadata.Name+", which evaluated "+e.Workload.Kind+" "+e.Workload.Name+
				", not "+runs.Kind+" "+runs.Name+", the workload this Stage runs (spec.workload)")
			continue
		}
		what := e.Workload.Kind + " " + e.Workload.Name
		o, err := c.workloadObject(ctx, stage.Namespace, e.Workload.Kind, e.Workload.Name)
		switch {
		case apierrors.IsNotFound(err):
			in.Missing = append(in.Missing, what)
		case err != nil:
			in.Unread = append(in.Unread, what+": "+err.Error())
		default:
			in.Targets = append(in.Targets, delivery.ApplierTarget{Workload: what, Manager: c.Managers.Detect(o)})
		}
	}
	in.Kustomizations, in.Unread = c.detectKustomizations(ctx, in.Targets, in.Unread)
	return delivery.AdoptApplier(in)
}

// selectedValues is the values file or overlay the Stage's applier selects
// for its environment, refused or not, as observed on the applyRef object
// and, for an OCIRepository, the Kustomizations applying it. It is empty when
// the applier selects none or a read fails: a values read never reaches
// Unread, so it never moves the applier state. A HelmRelease selects none.
func (c Cluster) selectedValues(ctx context.Context, stage *dv1.Stage) string {
	ref := stage.Spec.ApplyRef
	flux := cmp.Or(ref.Kind, delivery.ApplierFlux) == delivery.ApplierFlux
	if ref.Kind == delivery.ApplierHelmRelease || flux && c.NoFlux {
		return ""
	}
	a, err := c.applier(ctx, stage.Namespace, ref)
	if err != nil {
		return ""
	}
	var ks []delivery.KustomizationView
	if flux {
		if ks, err = c.kustomizations(ctx); err != nil {
			return ""
		}
	}
	return delivery.SelectedValues(ref, a.object().Object, ks, stage.Namespace, stage.Spec.Environment).File
}

// detectKustomizations reads, live, the Kustomization each Flux-labelled
// target names, or says why it cannot. A pointer decision rests on its
// sourceRef, so it never comes from the cache; a Kustomization that is gone
// is simply absent.
func (c Cluster) detectKustomizations(ctx context.Context, targets []delivery.ApplierTarget, unread []string) ([]delivery.KustomizationView, []string) {
	list := &unstructured.UnstructuredList{}
	for _, t := range targets {
		m := t.Manager
		if m.Name != workloadmanager.FluxKustomization || m.OwnerName == "" {
			continue
		}
		if c.NoFlux {
			return nil, append(unread, "the Kustomizations: the promoter runs with --flux=false")
		}
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(gvkKustomization.GroupVersion().WithKind("Kustomization"))
		err := c.Client.Get(ctx, types.NamespacedName{Namespace: m.OwnerNamespace, Name: m.OwnerName}, u)
		switch {
		case apierrors.IsNotFound(err):
		case err != nil:
			unread = append(unread, "the Kustomizations: "+err.Error())
		default:
			list.Items = append(list.Items, *u)
		}
	}
	if len(list.Items) == 0 {
		return nil, unread
	}
	b, _ := list.MarshalJSON()
	ks, err := delivery.DecodeKustomizations(b)
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
