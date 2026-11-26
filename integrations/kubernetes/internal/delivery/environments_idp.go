/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v7/api/delivery/v1"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
)

// The kinds of the objects other than Stages Pacto creates from the
// installation ladder, as the private state records them.
const (
	kindNamespace      = "Namespace"
	kindServiceAccount = "ServiceAccount"
	kindRoleBinding    = "RoleBinding"
)

// fluxKinds are the Flux objects Pacto creates for a service no applier runs.
var fluxKinds = []string{delivery.ApplierFlux, "Kustomization", delivery.ApplierHelmRelease}

// unmanaged reports whether m says no applier runs the service's workload:
// none was found, or it has no manager.
func unmanaged(m workloadmanager.Manager) bool {
	return m.Name == "" || m.Name == workloadmanager.None
}

// deployInput is what the Flux objects of the service c describes would
// apply: the manifests or chart artifact its publish.deploy names, verified
// against the keyless identity its accepted version was signed by. Nil when
// it names no such artifact or the version carries no keyless identity.
func deployInput(c *contract.Contract, id *delivery.PublisherIdentity) *delivery.FluxApplierInput {
	if id == nil || id.Issuer == "" || id.Subject == "" {
		return nil
	}
	for _, a := range c.Publish.Artifacts {
		if a.Name == c.Publish.Deploy && (a.Type == contract.ArtifactManifests || a.Type == contract.ArtifactChart) {
			return &delivery.FluxApplierInput{Service: c.Service.Name, URL: a.Ref, Chart: a.Type == contract.ArtifactChart, Publisher: *id}
		}
	}
	return nil
}

// ensureNamespace makes sure the namespace p names exists, or says why the
// environment waits. With delivery.environments.createNamespaces a declared
// or observed placement gets its namespace at once; any other only once a
// person's ladder record bound it among the candidates (pacto delivery
// ladder confirm --create-namespaces). Until then it is a candidate, listed
// only in PlacementsConfigMap. Outside profile idp nothing could apply in a
// namespace Pacto created, so it creates none.
func (r *EnvironmentReconciler) ensureNamespace(ctx context.Context, st *placementState, svc delivery.ServiceRef, environment string, p *delivery.Placement) string {
	ns := p.Namespace
	switch {
	case st.namespaces[ns] || !r.CreateNamespaces || !r.CreateAppliers:
		return "" // finishPlacement refused a missing one without createNamespaces; applierOf says none applies
	case p.Confirm && !slices.Contains(st.state.AllowedNamespaces, ns):
		st.candidates[ns] = true
		return "waiting: confirm namespace " + ns + " with pacto delivery ladder confirm --create-namespaces or create it"
	}
	n := &corev1.Namespace{}
	n.SetName(ns)
	n.SetLabels(map[string]string{delivery.ManagedLabel: delivery.ManagedBy})
	n.SetAnnotations(map[string]string{delivery.KeepAnnotation: "true"})
	if why := r.createOwned(ctx, &st.state, n, kindNamespace, svc, environment); why != "" {
		return why
	}
	st.namespaces[ns] = true
	return ""
}

// ensureAppliers creates, under profile idp, the suspended Flux objects of
// e, an environment of svc no applier runs, and its deployer identity once
// its placement is declared or a person confirmed it. It returns the
// ApplyRef of the Stage, or why there is none.
func (r *EnvironmentReconciler) ensureAppliers(ctx context.Context, st *placementState, svc delivery.ServiceRef, e placedEnv, name string) (delivery.ApplyRef, string) {
	p := e.row.Placement
	if e.deploy == nil {
		return delivery.ApplyRef{}, "nothing applies " + svc.Name + " in " + p.Namespace + " yet, and its accepted version names no manifests or chart " +
			"in publish.deploy signed by a keyless publisher identity, so Pacto creates no Flux objects for it"
	}
	if !p.Confirm || st.state.confirmed(p.Namespace, name) {
		if why := r.ensureDeployer(ctx, &st.state, svc, p.Namespace); why != "" {
			return delivery.ApplyRef{}, why
		}
	}
	in := *e.deploy
	in.Environment, in.Namespace = e.row.Environment, p.Namespace
	host, _, _ := strings.Cut(delivery.RepoFromURL(in.URL), "/")
	in.Insecure = slices.Contains(r.RecordOptions.Insecure, host)
	for _, o := range delivery.FluxApplierObjects(in) {
		if why := r.createOwned(ctx, &st.state, &unstructured.Unstructured{Object: o}, o["kind"].(string), svc, e.row.Environment); why != "" {
			return delivery.ApplyRef{}, why
		}
	}
	if in.Chart {
		return delivery.ApplyRef{Kind: delivery.ApplierHelmRelease, Name: name}, ""
	}
	return delivery.ApplyRef{Kind: delivery.ApplierFlux, Name: name}, ""
}

// ensureDeployer creates the deployer identity of svc in ns: the account
// its Flux objects apply as and its RoleBinding to DeployerRole.
func (r *EnvironmentReconciler) ensureDeployer(ctx context.Context, s *managedState, svc delivery.ServiceRef, ns string) string {
	for _, o := range delivery.DeployerObjects(svc.Name, ns, r.DeployerRole) {
		if why := r.createOwned(ctx, s, &unstructured.Unstructured{Object: o}, o["kind"].(string), svc, ""); why != "" {
			return why
		}
	}
	return ""
}

// createOwned creates o, of kind, and records its UID in s, unless it
// exists: one s records is Pacto's and is left as it is, any other was not
// created by Pacto and is never touched. It says why o is not there.
// ponytail: an object created whose state write then fails reads as not
// created by Pacto from the next pass: Pacto stops acting on it, never the
// other way round.
func (r *EnvironmentReconciler) createOwned(ctx context.Context, s *managedState, o client.Object, kind string, svc delivery.ServiceRef, environment string) string {
	what := kind + " " + o.GetName()
	if o.GetNamespace() != "" {
		what += " in " + o.GetNamespace()
	}
	cur := o.DeepCopyObject().(client.Object)
	err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(o), cur)
	switch {
	case err == nil && s.owns(cur.GetUID()):
		return ""
	case err == nil:
		return "a " + what + " not created by Pacto exists"
	case !apierrors.IsNotFound(err):
		return "the " + what + " could not be read: " + err.Error()
	case len(s.Objects) >= maxManagedObjects:
		return fmt.Sprintf("Pacto keeps at most %d objects it created; it creates no more", maxManagedObjects)
	}
	if err := r.Client.Create(ctx, o); err != nil {
		return "the " + what + " could not be created: " + err.Error()
	}
	s.Objects = append(s.Objects, managedObject{Kind: kind, Namespace: o.GetNamespace(), Name: o.GetName(), UID: o.GetUID(),
		Service: svc.Name, Domain: svc.Domain, Environment: environment})
	return ""
}

// confirmPlacement runs on every reconcile of a running operation of a
// managed Stage once its digest is written, before the applier is asked
// whether it applied it. For a Stage whose Flux objects Pacto created and
// still holds suspended, it confirms the placement with the operation's
// counted approval (ApprovalRecord.By: an approval the promoter did not
// count never reaches it), creates the deployer identity, unsuspends the
// objects whose UIDs the private state records and records the confirmation
// there, then shows it on the Stage. A placement that needs a person and has
// no counted approval unsuspends nothing: the operation waits, and runs out
// of time. A declared placement needs no person. Nothing is read from an
// annotation.
func (r *EnvironmentReconciler) confirmPlacement(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) error {
	if r == nil || r.Off || stage.Labels[delivery.ManagedLabel] != delivery.ManagedBy {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, rows, err := r.readPrivate(ctx)
	if err != nil {
		return err
	}
	appliers := s.fluxObjects(stage.Namespace, stage.Name)
	if !s.owns(stage.UID) || len(appliers) == 0 || s.confirmed(stage.Namespace, stage.Name) {
		return nil
	}
	by := ""
	if n := len(op.Status.Approvals); n > 0 && op.Status.Approvals[n-1].Decision != delivery.Reject {
		by = op.Status.Approvals[n-1].By
	}
	i := slices.IndexFunc(rows, func(row delivery.PlacementRow) bool { return row.Stage == stage.Namespace+"/"+stage.Name })
	switch {
	case i < 0 || rows[i].Placement == nil:
		return errors.New("the placement of Stage " + stage.Name + " is not known: nothing it applies through is unsuspended")
	case rows[i].Placement.Confirm && (by == "" || !strings.HasPrefix(op.Spec.Reason, delivery.PlacementQuestion(stage.Spec.Service.Name, *rows[i].Placement))):
		// Only an approval of a promote that asked this very placement
		// confirms it: one that did not name it, or named another, never does.
		return errors.New(delivery.PlacementQuestion(stage.Spec.Service.Name, *rows[i].Placement) +
			" A person's approval of a promote that asks it confirms it: until then nothing it applies through is unsuspended")
	case by == "":
		by = op.Spec.Actor.User
	}
	err = r.unlock(ctx, &s, stage, appliers)
	if err == nil {
		s.Confirmations = append(s.Confirmations, placementConfirmation{Stage: stage.UID, Operation: op.Name, By: by, At: r.Now().UTC().Format(time.RFC3339)})
	}
	// The deployer objects it created are recorded whatever else failed.
	if err := errors.Join(err, r.writeState(ctx, s)); err != nil {
		return err
	}
	return mergePatch(ctx, r.Client, stage, map[string]any{"metadata": map[string]any{"annotations": map[string]string{delivery.PlacementConfirmedByAnnotation: by}}})
}

// unlock creates the deployer identity of the managed Stage's service and
// unsuspends appliers, the Flux objects Pacto created for it.
func (r *EnvironmentReconciler) unlock(ctx context.Context, s *managedState, stage *dv1.Stage, appliers []managedObject) error {
	if why := r.ensureDeployer(ctx, s, stage.Spec.Service, stage.Namespace); why != "" {
		return errors.New(why)
	}
	for _, o := range appliers {
		if err := r.unsuspend(ctx, o); err != nil {
			return err
		}
	}
	return nil
}

// fluxObjects are the Flux objects the state records Pacto created for the
// managed Stage ns/name.
func (s managedState) fluxObjects(ns, name string) []managedObject {
	var out []managedObject
	for _, o := range s.Objects {
		if o.Namespace == ns && o.Name == name && slices.Contains(fluxKinds, o.Kind) {
			out = append(out, o)
		}
	}
	return out
}

// unsuspend sets spec.suspend false on o, the Flux object Pacto created,
// when the object of that name is still the one it created.
func (r *EnvironmentReconciler) unsuspend(ctx context.Context, o managedObject) error {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(fluxGVK(o.Kind))
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: o.Namespace, Name: o.Name}, u); err != nil {
		return fmt.Errorf("reading %s %s: %w", o.Kind, o.Name, err)
	}
	if u.GetUID() != o.UID {
		return fmt.Errorf("%s %s in %s is not the one Pacto created: it is left suspended", o.Kind, o.Name, o.Namespace)
	}
	return mergePatch(ctx, r.Client, u, map[string]any{"spec": map[string]any{"suspend": false}})
}

// fluxGVK is the kind of a Flux object Pacto creates.
func fluxGVK(kind string) schema.GroupVersionKind {
	switch kind {
	case "Kustomization":
		return gvkKustomization.GroupVersion().WithKind(kind)
	case delivery.ApplierHelmRelease:
		return gvkHelmRelease
	}
	return gvkOCIRepository
}

// writeState merge-patches the private state alone into
// PlacementsConfigMap, which the pass wrote before.
func (r *EnvironmentReconciler) writeState(ctx context.Context, s managedState) error {
	cm := &corev1.ConfigMap{}
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: delivery.PlacementsConfigMap}, cm); err != nil {
		return err
	}
	b, _ := json.Marshal(s) // ponytail: plain strings: Marshal cannot fail
	patch := client.MergeFrom(cm.DeepCopy())
	cm.Data[stateKey] = string(b)
	return r.Client.Patch(ctx, cm, patch)
}

// question is the placement sentence a promote or trust question of the
// managed Stage starts with while its placement waits for a person, "" when
// it does not: the Stage is not one the private state records, its
// placement needs no person or one confirmed it. A private state that cannot
// be read is an error, so no question is raised without the sentence.
func (r *EnvironmentReconciler) question(ctx context.Context, stage *dv1.Stage) (string, error) {
	if r == nil || r.Off || stage.Labels[delivery.ManagedLabel] != delivery.ManagedBy {
		return "", nil
	}
	s, rows, err := r.readPrivate(ctx)
	if err != nil {
		return "", fmt.Errorf("whether the placement of Stage %s waits for a person could not be read: %w", stage.Name, err)
	}
	if !s.owns(stage.UID) || s.confirmed(stage.Namespace, stage.Name) {
		return "", nil
	}
	i := slices.IndexFunc(rows, func(row delivery.PlacementRow) bool { return row.Stage == stage.Namespace+"/"+stage.Name })
	if i < 0 || rows[i].Placement == nil || !rows[i].Placement.Confirm {
		return "", nil
	}
	return delivery.PlacementQuestion(stage.Spec.Service.Name, *rows[i].Placement), nil
}
