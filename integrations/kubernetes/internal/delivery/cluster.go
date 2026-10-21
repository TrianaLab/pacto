/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package delivery runs the Pacto delivery promoter: the operation reconciler
// that carries one Operation from admission to conclusion, and the stage
// reconciler that raises automatic promotes and witnesses edits it did not
// make. Every decision comes from the pure core in pkg/delivery; this package
// only reads the cluster and the registry and writes status.
//
// It is NOT part of the observing operator. It is built into the separate
// pacto-delivery image and installed by the opt-in pacto-delivery chart, so the
// operator keeps its promise of never changing what runs. For the same reason
// it carries no kubebuilder RBAC markers: they would be merged into the
// operator's generated role. Its RBAC lives in the delivery chart.
package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// Flux and Pacto kinds the promoter reads through unstructured objects, so the
// image carries no Flux or operator API dependency it does not need.
var (
	gvkOCIRepository = schema.GroupVersionKind{Group: "source.toolkit.fluxcd.io", Version: "v1", Kind: "OCIRepository"}
	gvkKustomization = schema.GroupVersionKind{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Kind: "KustomizationList"}
	gvkPactoList     = schema.GroupVersionKind{Group: "pacto.trianalab.io", Version: "v1alpha1", Kind: "PactoList"}
)

// Cluster is the promoter's read and write access to everything that is not a
// delivery object.
type Cluster struct {
	Client client.Client
	// Registry returns a read-only registry client that reaches insecureHosts
	// over plain HTTP.
	Registry func(insecureHosts ...string) delivery.Registry
}

// applyRef is the Stage's OCIRepository as the promoter sees it.
type applyRef struct {
	obj      *unstructured.Unstructured
	repo     string
	insecure bool
	digest   string // spec.ref.digest
	revision string // status.artifact.revision
	// verified is true when spec.verify names the publisher's identity and
	// Flux reports SourceVerified for the current artifact revision.
	verified bool
}

func (c Cluster) ociRepository(ctx context.Context, ns, name string) (*applyRef, error) {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvkOCIRepository)
	if err := c.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, u); err != nil {
		return nil, err
	}
	url, _, _ := unstructured.NestedString(u.Object, "spec", "url")
	insecure, _, _ := unstructured.NestedBool(u.Object, "spec", "insecure")
	digest, _, _ := unstructured.NestedString(u.Object, "spec", "ref", "digest")
	revision, _, _ := unstructured.NestedString(u.Object, "status", "artifact", "revision")
	return &applyRef{obj: u, repo: delivery.RepoFromURL(url), insecure: insecure, digest: digest, revision: revision,
		verified: pinsIdentity(u.Object) && sourceVerified(u.Object, delivery.RevisionDigest(revision))}, nil
}

// pinsIdentity reports whether spec.verify names whose signature counts: a
// secretRef holding the publisher's public key, or a matchOIDCIdentity for
// keyless signatures. Without either, Flux accepts a keyless signature from
// any identity, so anyone who can push could sign their own artifact.
func pinsIdentity(obj map[string]any) bool {
	provider, _, _ := unstructured.NestedString(obj, "spec", "verify", "provider")
	secret, _, _ := unstructured.NestedString(obj, "spec", "verify", "secretRef", "name")
	ids, _, _ := unstructured.NestedSlice(obj, "spec", "verify", "matchOIDCIdentity")
	return provider != "" && (secret != "" || len(ids) > 0)
}

// sourceVerified reports whether Flux's SourceVerified condition is True for
// digest: Flux names the revision it verified in the condition's message, and
// a revision whose signature fails never becomes the artifact.
func sourceVerified(obj map[string]any, digest string) bool {
	conds, _, _ := unstructured.NestedSlice(obj, "status", "conditions")
	for _, c := range conds {
		m, _ := c.(map[string]any)
		msg, _ := m["message"].(string)
		if m["type"] == "SourceVerified" && m["status"] == "True" && digest != "" && strings.Contains(msg, digest) {
			return true
		}
	}
	return false
}

// host is the registry host of the apply repository.
func (a *applyRef) host() string {
	host, _, _ := strings.Cut(a.repo, "/")
	return host
}

// setDigest sets spec.ref.digest and asks Flux to reconcile now. It is the only
// write the promoter ever makes to an object it does not own.
func (c Cluster) setDigest(ctx context.Context, a *applyRef, digest string, now time.Time) error {
	patch := map[string]any{
		"metadata": map[string]any{"annotations": map[string]string{"reconcile.fluxcd.io/requestedAt": now.Format(time.RFC3339Nano)}},
		"spec":     map[string]any{"ref": map[string]any{"digest": digest}},
	}
	b, _ := json.Marshal(patch)
	return c.Client.Patch(ctx, a.obj, client.RawPatch(types.MergePatchType, b))
}

// applied reports whether Flux applied digest from the apply repository:
// failed is a confirmed failure (Flux attempted digest and reports it not
// Ready for a reason other than progress), ok is a confirmed success, and
// neither means "not yet".
func (c Cluster) applied(ctx context.Context, ns string, a *applyRef, digest string) (ok, failed bool, msg string, err error) {
	if delivery.RevisionDigest(a.revision) != digest {
		return false, false, "the OCIRepository has not fetched " + digest + " yet", nil
	}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvkKustomization)
	if err := c.Client.List(ctx, list, client.InNamespace(ns)); err != nil {
		return false, false, "", err
	}
	found := 0
	for _, k := range list.Items {
		kind, _, _ := unstructured.NestedString(k.Object, "spec", "sourceRef", "kind")
		name, _, _ := unstructured.NestedString(k.Object, "spec", "sourceRef", "name")
		if kind != "OCIRepository" || name != a.obj.GetName() {
			continue
		}
		found++
		ready, reason, message := readyCondition(k.Object)
		applied, _, _ := unstructured.NestedString(k.Object, "status", "lastAppliedRevision")
		attempted, _, _ := unstructured.NestedString(k.Object, "status", "lastAttemptedRevision")
		switch {
		case ready == "True" && delivery.RevisionDigest(applied) == digest:
		case ready == "False" && delivery.RevisionDigest(attempted) == digest && reason != "Progressing" && reason != "DependencyNotReady":
			return false, true, fmt.Sprintf("Kustomization %s failed to apply %s: %s: %s", k.GetName(), digest, reason, message), nil
		default:
			return false, false, "Kustomization " + k.GetName() + " has not applied " + digest + " yet", nil
		}
	}
	if found == 0 {
		return false, false, "no Kustomization applies OCIRepository " + a.obj.GetName(), nil
	}
	return true, false, "", nil
}

func readyCondition(obj map[string]any) (status, reason, message string) {
	conds, _, _ := unstructured.NestedSlice(obj, "status", "conditions")
	for _, c := range conds {
		m, _ := c.(map[string]any)
		if m["type"] == "Ready" {
			status, _ = m["status"].(string)
			reason, _ = m["reason"].(string)
			message, _ = m["message"].(string)
		}
	}
	return status, reason, message
}

// describe reads what digest carries on a Stage's own apply repository. The
// read is bounded by ResolveRevision.
func (c Cluster) describe(ctx context.Context, stage delivery.StageObject, digest string) delivery.Revision {
	a, err := c.ociRepository(ctx, stage.Metadata.Namespace, stage.Spec.ApplyRef.Name)
	if err != nil {
		return delivery.Revision{Digest: digest, Err: "the apply OCIRepository: " + err.Error()}
	}
	return delivery.ResolveRevision(ctx, c.registryFor(a), a.repo, digest)
}

// pactos lists every Pacto in the cluster through the stable read contract.
func (c Cluster) pactos(ctx context.Context) ([]delivery.PactoView, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvkPactoList)
	if err := c.Client.List(ctx, list); err != nil {
		return nil, err
	}
	b, _ := list.MarshalJSON()
	return delivery.DecodePactos(b)
}

// workload reads the live state of a workload a Pacto evaluated.
func (c Cluster) workload(ctx context.Context, ns string) func(kind, name string) *delivery.WorkloadState {
	return func(kind, name string) *delivery.WorkloadState {
		group, version := "apps", "v1"
		if kind == "Job" || kind == "CronJob" {
			group = "batch"
		}
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(schema.GroupVersionKind{Group: group, Version: version, Kind: kind})
		if err := c.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, u); err != nil {
			return nil
		}
		observed, found, _ := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
		if !found {
			// ponytail: a kind with no observedGeneration (CronJob) counts as observed.
			observed = u.GetGeneration()
		}
		return &delivery.WorkloadState{UID: string(u.GetUID()), Generation: u.GetGeneration(), ObservedGeneration: observed}
	}
}
