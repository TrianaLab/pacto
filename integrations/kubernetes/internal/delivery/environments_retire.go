/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// DefaultRetireAfter is how long an environment that stopped being desired
// keeps its managed Stage (delivery.environments.retireAfter).
const DefaultRetireAfter = 7 * 24 * time.Hour

// envEvent is an environment event one pass reports from the private state:
// a managed Stage first seen with its stream id, or one gone.
type envEvent struct {
	stage   *dv1.Stage
	created bool
}

// envKey names environment of the service domain/name in desired.
func envKey(domain, name, environment string) string {
	return domain + "/" + name + "/" + environment
}

// noteStages compares the Stages the state records with live, the Stages
// that exist by UID. A managed Stage first seen with a stream id records it
// and reports EnvironmentCreated; one gone leaves the state and, when it was
// reported created, reports EnvironmentDeleted, a delete made while the
// promoter was down too.
func (st *placementState) noteStages(live map[types.UID]*dv1.Stage) {
	for _, o := range slices.Clone(st.state.Objects) {
		if o.Kind != kindStage {
			continue
		}
		s := live[o.UID]
		switch {
		case s == nil:
			st.state.drop(o)
			if o.Stream != "" {
				st.events = append(st.events, envEvent{stage: o.stage()})
			}
		case o.Stream == "" && s.Status.Stream.ID != "":
			i := slices.IndexFunc(st.state.Objects, func(m managedObject) bool { return m.UID == o.UID })
			st.state.Objects[i].Stream = s.Status.Stream.ID
			st.state.Objects[i].CreatedAt = s.CreationTimestamp.UTC().Format(time.RFC3339)
			st.events = append(st.events, envEvent{stage: s, created: true})
		}
	}
}

// stage rebuilds the managed Stage o records, for an event about it once it
// is gone.
func (o managedObject) stage() *dv1.Stage {
	at, _ := time.Parse(time.RFC3339, o.CreatedAt) // written by noteStages
	s := &dv1.Stage{ObjectMeta: metav1.ObjectMeta{Namespace: o.Namespace, Name: o.Name, UID: o.UID, CreationTimestamp: metav1.NewTime(at)}}
	s.Spec.Service, s.Spec.Environment = delivery.ServiceRef{Domain: o.Domain, Name: o.Service}, o.Environment
	s.Status.Stream.ID = o.Stream
	return s
}

// drop removes gone from the state and, for a Stage, the confirmation of its
// placement.
func (s *managedState) drop(gone managedObject) {
	s.Objects = slices.DeleteFunc(s.Objects, func(o managedObject) bool { return o.Kind == gone.Kind && o.UID == gone.UID })
	if gone.Kind == kindStage {
		s.Confirmations = slices.DeleteFunc(s.Confirmations, func(c placementConfirmation) bool { return c.Stage == gone.UID })
	}
}

// emitFromState reports events, once the state that records them is written:
// the Kubernetes Event on the Stage, or on its namespace when the Stage is
// gone, and the CDEvent.
// ponytail: a promoter that stops between the state write and here loses
// those events; it never reports one twice.
func (r *EnvironmentReconciler) emitFromState(ctx context.Context, events []envEvent) {
	for _, e := range events {
		if e.created {
			event(r.Events, e.stage, false, "EnvironmentCreated", createdMessage(e.stage))
		} else {
			event(r.Events, &corev1.Namespace{TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"},
				ObjectMeta: metav1.ObjectMeta{Name: e.stage.Namespace}}, false, "EnvironmentDeleted", deletedMessage(e.stage))
		}
		r.CDEvents.emitEnvironment(ctx, e.stage, e.created, r.Now())
	}
}

// retiringSince is when the managed Stage o started to retire: never while
// its environment is desired, the expiry of an expired temporary one, else
// when a pass first found it no longer desired. A pass that cannot tell (a
// partial index or installation ladder, or a service whose accepted contract
// could not be read) leaves it as it was.
func (st *placementState) retiringSince(o managedObject, now time.Time) string {
	from, desired := st.desired[envKey(o.Domain, o.Service, o.Environment)]
	switch {
	case desired:
		return from
	case !st.complete || st.unsure[envKey(o.Domain, o.Service, "")]:
		return o.RetiringSince
	}
	return cmp.Or(o.RetiringSince, now.UTC().Format(time.RFC3339))
}

// retireManaged marks each managed Stage retiring or desired again, mirrored
// in RetiringSinceAnnotation, and, on a complete pass only, retires those
// whose grace (RetireAfter) has passed.
func (r *EnvironmentReconciler) retireManaged(ctx context.Context, st *placementState) {
	now := r.Now()
	for i, o := range st.state.Objects {
		if o.Kind != kindStage {
			continue
		}
		if since := st.retiringSince(o, now); since != o.RetiringSince {
			st.state.Objects[i].RetiringSince = since
			var v any
			if since != "" {
				v = since
			}
			if err := mergePatch(ctx, r.Client, st.byName[o.Namespace+"/"+o.Name], map[string]any{"metadata": map[string]any{
				"annotations": map[string]any{delivery.RetiringSinceAnnotation: v}}}); err != nil {
				st.missed = append(st.missed, "the Stage "+o.Namespace+"/"+o.Name+" could not be marked: "+err.Error())
			}
		}
	}
	if !st.complete {
		return
	}
	for _, o := range slices.Clone(st.state.Objects) {
		since, _ := time.Parse(time.RFC3339, o.RetiringSince)
		if o.Kind == kindStage && o.RetiringSince != "" && !now.Before(delivery.RetireAt(since, r.RetireAfter)) {
			r.retire(ctx, st, o)
		}
	}
}

// retire deletes the managed Stage s with the Flux objects Pacto created for
// it and, when no other Stage of its service is left in its namespace, its
// deployer identity: only objects whose UID the state records, never a
// namespace or a workload. Flux's deletionPolicy Orphan keeps what they
// applied. A delete that fails keeps s for the next pass.
func (r *EnvironmentReconciler) retire(ctx context.Context, st *placementState, s managedObject) {
	for _, o := range append(st.state.dependents(s), s) {
		if err := r.deleteOwned(ctx, o); err != nil {
			st.missed = append(st.missed, "the "+o.Kind+" "+o.Namespace+"/"+o.Name+" could not be retired: "+err.Error())
			return
		}
		st.state.drop(o)
	}
	if s.Stream != "" {
		st.events = append(st.events, envEvent{stage: s.stage()})
	}
}

// dependents are the objects Pacto created for the managed Stage s: its Flux
// objects and, when s is its service's last Stage in its namespace, the
// deployer identity there.
func (s managedState) dependents(stage managedObject) []managedObject {
	same := func(o managedObject) bool {
		return o.Namespace == stage.Namespace && o.Service == stage.Service && o.Domain == stage.Domain
	}
	last := !slices.ContainsFunc(s.Objects, func(o managedObject) bool { return o.Kind == kindStage && o.UID != stage.UID && same(o) })
	out := s.fluxObjects(stage.Namespace, stage.Name)
	for _, o := range s.Objects {
		if last && same(o) && (o.Kind == kindServiceAccount || o.Kind == kindRoleBinding) {
			out = append(out, o)
		}
	}
	return out
}

// deleteOwned deletes o when the object of that name is still the one Pacto
// created. One gone, or replaced by another, is no longer Pacto's.
func (r *EnvironmentReconciler) deleteOwned(ctx context.Context, o managedObject) error {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(ownedGVK(o.Kind))
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: o.Namespace, Name: o.Name}, u)
	switch {
	case apierrors.IsNotFound(err):
		return nil
	case err != nil:
		return err
	case u.GetUID() != o.UID:
		return nil
	}
	uid := o.UID
	if err := r.Client.Delete(ctx, u, client.Preconditions{UID: &uid}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// ownedGVK is the kind of an object the private state records, other than a
// namespace, which Pacto never deletes.
func ownedGVK(kind string) schema.GroupVersionKind {
	switch kind {
	case kindStage:
		return dv1.GroupVersion.WithKind(kindStage)
	case kindServiceAccount:
		return corev1.SchemeGroupVersion.WithKind(kind)
	case kindRoleBinding:
		return rbacv1.SchemeGroupVersion.WithKind(kind)
	}
	return fluxGVK(kind)
}

// bound keeps the state within maxManagedObjects: past it, the retiring
// Stages retiring longest are forgotten first (Pacto then never deletes
// them) and the pass is partial.
func (st *placementState) bound() {
	n := len(st.state.Objects)
	if n <= maxManagedObjects {
		return
	}
	retiring := slices.DeleteFunc(slices.Clone(st.state.Objects), func(o managedObject) bool { return o.RetiringSince == "" })
	slices.SortStableFunc(retiring, func(a, b managedObject) int { return cmp.Compare(a.RetiringSince, b.RetiringSince) })
	for _, o := range retiring[:min(n-maxManagedObjects, len(retiring))] {
		st.state.drop(o)
	}
	st.unread = append(st.unread, fmt.Sprintf("the private state holds %d objects, over its bound of %d; the Stages retiring longest are forgotten", n, maxManagedObjects))
}
