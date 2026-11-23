/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
)

// stateKey is the key of PlacementsConfigMap that holds the reconciler's
// private state.
const stateKey = "stages.json"

// kindStage is a Stage in the private state.
const kindStage = "Stage"

// maxManagedObjects bounds the objects the private state records: a ConfigMap
// holds at most 1 MiB. A var so tests lower it.
// ponytail: 4000 entries; past it Pacto creates nothing more and says so.
// Shard the state over ConfigMaps if an installation needs more.
var maxManagedObjects = 4000

// managedObject is one object Pacto created from the installation ladder.
type managedObject struct {
	Kind        string    `json:"kind"`
	Namespace   string    `json:"namespace"`
	Name        string    `json:"name"`
	UID         types.UID `json:"uid"`
	Service     string    `json:"service"`
	Domain      string    `json:"domain,omitempty"`
	Environment string    `json:"environment"`
}

// managedState is the reconciler's private state: the objects it created.
// What Pacto owns is this UID list, never a label: anyone who may edit a
// Stage may label it managed-by=pacto.
type managedState struct {
	Objects []managedObject `json:"objects"`
}

func (s managedState) owns(uid types.UID) bool {
	return uid != "" && slices.ContainsFunc(s.Objects, func(o managedObject) bool { return o.UID == uid })
}

// readState reads the private state from PlacementsConfigMap; none yet is
// empty. A state that cannot be read or decoded is an error: Pacto then
// cannot tell its Stages from hand-written ones and acts on none.
func (r *EnvironmentReconciler) readState(ctx context.Context) (managedState, error) {
	var s managedState
	cm := &corev1.ConfigMap{}
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: delivery.PlacementsConfigMap}, cm)
	switch {
	case apierrors.IsNotFound(err):
		return s, nil
	case err != nil:
		return s, err
	case cm.Data[stateKey] == "":
		return s, nil
	}
	if err := json.Unmarshal([]byte(cm.Data[stateKey]), &s); err != nil {
		return s, fmt.Errorf("%s: %w", stateKey, err)
	}
	return s, nil
}

// placedEnv is one environment of a service in one pass: its row, the
// environment it takes versions from, and the manager and workload of the
// service in the namespace it was placed in.
type placedEnv struct {
	row      *delivery.PlacementRow
	upstream string
	manager  workloadmanager.Manager
	workload *delivery.WorkloadRef
}

// manageService creates or updates the managed Stage of each placed
// environment of svc whose workload an adopted applier already runs, in
// installation ladder order, so each Stage names its upstream environment's
// Stage, managed or hand-written. An environment it creates no Stage for says
// why in its row.
func (r *EnvironmentReconciler) manageService(ctx context.Context, st *placementState, svc delivery.ServiceRef, envs []placedEnv) {
	order := func(name string) int {
		return slices.IndexFunc(st.ladder, func(l delivery.LadderRung) bool { return l.Name == name })
	}
	sorted := slices.Clone(envs)
	slices.SortStableFunc(sorted, func(a, b placedEnv) int { return cmp.Compare(order(a.row.Environment), order(b.row.Environment)) })
	refs := map[string]*delivery.StageRef{}
	for _, e := range sorted {
		p := e.row.Placement
		switch {
		case p == nil:
		case p.Source == delivery.PlacementFromStage:
			refs[e.row.Environment] = handWrittenRef(st, svc, e.row.Environment)
		case p.Placed():
			if ref := r.applyManaged(ctx, st, svc, e, refs); ref != nil {
				refs[e.row.Environment] = ref
			}
		}
	}
}

// handWrittenRef names the hand-written Stage of svc in environment.
func handWrittenRef(st *placementState, svc delivery.ServiceRef, environment string) *delivery.StageRef {
	i := slices.IndexFunc(st.stages, func(s delivery.StageObject) bool { return s.Spec.Service == svc && s.Spec.Environment == environment })
	s := st.stages[i] // placeByStage found it there
	return &delivery.StageRef{Name: s.Metadata.Name, Namespace: s.Metadata.Namespace}
}

// applyManaged creates the managed Stage of e, or restores the fields the
// reconciler owns on the one it created (service, environment, applyRef,
// candidates, workload, its labels and annotations). startLevel and approval
// are set only at creation. It never touches a Stage whose UID the state does
// not hold. It returns the Stage, or nil with the reason in e's row.
func (r *EnvironmentReconciler) applyManaged(ctx context.Context, st *placementState, svc delivery.ServiceRef, e placedEnv, refs map[string]*delivery.StageRef) *delivery.StageRef {
	row, p := e.row, e.row.Placement
	ns, name := p.Namespace, svc.Name+"-"+row.Environment
	applier, why := r.applierOf(ctx, st, ns, svc.Name, e.manager)
	var upstream *delivery.StageRef
	if why == "" && e.upstream != "" {
		if upstream = refs[e.upstream]; upstream == nil {
			why = "upstream " + e.upstream + " has no Stage of " + svc.Name + " in this cluster yet"
		}
	}
	if why != "" {
		row.Reason = why
		return nil
	}
	spec := delivery.ManagedStageSpec(svc, row.Environment, upstream, applier, e.workload, r.guarded(ctx, ns, name, applier))
	want := &dv1.Stage{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name,
		Labels: delivery.ManagedLabels(svc, row.Environment), Annotations: delivery.PlacementAnnotations(row.Source, *p)}, Spec: spec}
	ref := &delivery.StageRef{Name: name, Namespace: ns}
	cur := st.byName[ns+"/"+name]
	switch {
	case cur == nil:
		return r.createManaged(ctx, st, svc, row, want, ref)
	case !st.state.owns(cur.UID):
		p.Refused = "a Stage " + name + " not created by Pacto exists in " + ns
		return nil
	}
	next := cur.DeepCopy()
	next.Labels, next.Annotations = merged(next.Labels, want.Labels), merged(next.Annotations, want.Annotations)
	next.Spec.Service, next.Spec.Environment, next.Spec.ApplyRef = spec.Service, spec.Environment, spec.ApplyRef
	next.Spec.Candidates, next.Spec.Workload = spec.Candidates, spec.Workload
	if !equality.Semantic.DeepEqual(next, cur) {
		if err := r.Client.Patch(ctx, next, client.MergeFrom(cur)); err != nil {
			st.unread = append(st.unread, "the Stage "+ns+"/"+name+" could not be updated: "+err.Error())
		}
	}
	row.Stage = ns + "/" + name
	return ref
}

// createManaged creates want and records its UID in the private state.
// ponytail: a Stage created whose state write then fails reads hand-written
// from the next pass: Pacto stops acting on it, never the other way round.
func (r *EnvironmentReconciler) createManaged(ctx context.Context, st *placementState, svc delivery.ServiceRef, row *delivery.PlacementRow, want *dv1.Stage, ref *delivery.StageRef) *delivery.StageRef {
	if len(st.state.Objects) >= maxManagedObjects {
		row.Reason = fmt.Sprintf("Pacto keeps at most %d objects it created; it creates no more", maxManagedObjects)
		st.unread = append(st.unread, "the private state is full")
		return nil
	}
	if err := r.Client.Create(ctx, want); err != nil {
		row.Reason = "the Stage " + ref.Namespace + "/" + ref.Name + " could not be created: " + err.Error()
		return nil
	}
	st.state.Objects = append(st.state.Objects, managedObject{Kind: kindStage, Namespace: ref.Namespace, Name: ref.Name, UID: want.UID,
		Service: svc.Name, Domain: svc.Domain, Environment: row.Environment})
	row.Stage = ref.Namespace + "/" + ref.Name
	return ref
}

// merged is have with want's keys set.
func merged(have, want map[string]string) map[string]string {
	out := maps.Clone(have)
	if out == nil {
		out = map[string]string{}
	}
	maps.Copy(out, want)
	return out
}

// applierOf is the adopted applier m names for service's workload in ns, as
// a Stage there may apply through it: an Argo CD Application or a Flux
// HelmRelease in ns, or the OCIRepository in ns a Flux Kustomization applies
// from. why says why there is none.
func (r *EnvironmentReconciler) applierOf(ctx context.Context, st *placementState, ns, service string, m workloadmanager.Manager) (delivery.ApplyRef, string) {
	outside := func(what string) string {
		return what + " applies " + service + " from outside " + ns + ": Pacto creates a Stage only beside its applier"
	}
	switch m.Name {
	case workloadmanager.ArgoCD, workloadmanager.FluxHelmRelease:
		kind := map[string]string{workloadmanager.ArgoCD: delivery.ApplierArgoCD, workloadmanager.FluxHelmRelease: delivery.ApplierHelmRelease}[m.Name]
		if m.OwnerName == "" || m.OwnerNamespace != ns {
			return delivery.ApplyRef{}, outside(workloadmanager.Describe(m))
		}
		return delivery.ApplyRef{Kind: kind, Name: m.OwnerName}, ""
	case workloadmanager.FluxKustomization:
		return r.kustomizationSource(ctx, st, ns, m, outside)
	}
	return delivery.ApplyRef{}, "nothing applies " + service + " in " + ns + " yet; " + delivery.NoApplierMessage
}

// kustomizationSource is the OCIRepository in ns the Kustomization m names
// applies from.
func (r *EnvironmentReconciler) kustomizationSource(ctx context.Context, st *placementState, ns string, m workloadmanager.Manager, outside func(string) string) (delivery.ApplyRef, string) {
	what := "Kustomization " + m.OwnerNamespace + "/" + m.OwnerName
	k := &unstructured.Unstructured{}
	k.SetGroupVersionKind(gvkKustomization.GroupVersion().WithKind("Kustomization"))
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: m.OwnerNamespace, Name: m.OwnerName}, k); err != nil {
		if !apierrors.IsNotFound(err) {
			st.unread = append(st.unread, what+": "+err.Error())
		}
		return delivery.ApplyRef{}, what + ", which applies the workload, could not be read: " + err.Error()
	}
	kind, _, _ := unstructured.NestedString(k.Object, "spec", "sourceRef", "kind")
	name, _, _ := unstructured.NestedString(k.Object, "spec", "sourceRef", "name")
	srcNS, _, _ := unstructured.NestedString(k.Object, "spec", "sourceRef", "namespace")
	switch {
	case kind != delivery.ApplierFlux:
		return delivery.ApplyRef{}, what + " applies from a " + kind + ": a Stage points only an OCIRepository at a version"
	case cmp.Or(srcNS, m.OwnerNamespace) != ns:
		return delivery.ApplyRef{}, outside("the OCIRepository of " + what)
	}
	return delivery.ApplyRef{Kind: delivery.ApplierFlux, Name: name}, ""
}

// guarded reports whether the applier already carries ApplyRefLabel for
// the Stage name, so admission guards its pointer. An applier that cannot be
// read is unguarded: the Stage then starts at observe, which never ships.
func (r *EnvironmentReconciler) guarded(ctx context.Context, ns, name string, ref delivery.ApplyRef) bool {
	o := &unstructured.Unstructured{}
	o.SetGroupVersionKind(applierGVK(ref.Kind))
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: ns, Name: ref.Name}, o); err != nil {
		return false
	}
	return o.GetLabels()[delivery.ApplyRefLabel] == name
}

// applierGVK is the kind an ApplyRef of kind names.
func applierGVK(kind string) schema.GroupVersionKind {
	switch kind {
	case delivery.ApplierHelmRelease:
		return gvkHelmRelease
	case delivery.ApplierArgoCD:
		return gvkApplication
	}
	return gvkOCIRepository
}
