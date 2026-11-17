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
// pacto-delivery image and installed by the operator chart's opt-in delivery
// section with its own ServiceAccount, so the operator keeps its promise of
// never changing what runs. For the same reason it carries no kubebuilder RBAC
// markers: they would be merged into the operator's generated role. Its RBAC
// lives in the chart's templates/delivery.
package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// Flux and Pacto kinds the promoter reads through unstructured objects, so the
// image carries no Flux or operator API dependency it does not need.
var (
	gvkOCIRepository = schema.GroupVersionKind{Group: "source.toolkit.fluxcd.io", Version: "v1", Kind: "OCIRepository"}
	gvkKustomization = schema.GroupVersionKind{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Kind: "KustomizationList"}
	gvkPactoList     = schema.GroupVersionKind{Group: "pacto.trianalab.io", Version: "v1alpha1", Kind: "PactoList"}
	gvkApplication   = schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"}
)

// Cluster is the promoter's read and write access to everything that is not a
// delivery object.
type Cluster struct {
	// Client writes, and reads live: the promoter's manager client reads
	// unstructured objects from the apiserver, never from a cache.
	Client client.Client
	// Cache reads the OCIRepositories and Kustomizations the stage reconciler
	// watches (and the Applications, when it watches them), for every read no
	// pointer decision rests on. Nil reads them through Client.
	Cache client.Reader
	// CacheApplications says Cache holds the Argo CD Applications too.
	CacheApplications bool
	// NoFlux says Stages apply through no Flux OCIRepository or
	// Kustomization: the promoter neither watches nor reads them. It is
	// inverted so the zero value keeps Flux.
	NoFlux bool
	// FluxHelm says the promoter watches Flux HelmReleases, so Cache holds
	// them too.
	FluxHelm bool
	// Registry returns a read-only registry client that reaches insecureHosts
	// over plain HTTP.
	Registry func(insecureHosts ...string) delivery.Registry
	// Upstream reads what a Stage in another cluster verified from its signed
	// records (deliveryrecord.Upstream with the promoter's upstream trust
	// config), refusing a stream that no longer holds seen and skipping the
	// stand-ins in acked (status.upstreamAcknowledged). Nil when the
	// promoter trusts no upstream key: such a Stage is not Ready and offers
	// nothing.
	Upstream func(ctx context.Context, r delivery.RemoteStage, seen delivery.RecordMark, acked []string) (*delivery.StageStatus, string)
}

// remote reads the upstream Stage in another cluster that stage follows, or
// says why it cannot: no trusted key, records that cannot be read or verified,
// a stream that is not whole, or one that no longer holds the newest record
// the Stage already read (status.upstreamSeen).
func (c Cluster) remote(ctx context.Context, stage *dv1.Stage) (*delivery.StageStatus, string) {
	if c.Upstream == nil {
		return nil, noUpstreamTrust
	}
	return c.Upstream(ctx, *stage.Spec.Candidates.UpstreamRecords, stage.Status.UpstreamSeen, stage.Status.UpstreamAcknowledged)
}

const noUpstreamTrust = "the promoter trusts no key for the records of other clusters (promoter.upstreamTrust)"

// source is an OCI repository the promoter reads versions from: the apply
// repository of a Stage's applier or a candidate OCIRepository.
type source struct {
	obj      *unstructured.Unstructured
	repo     string
	insecure bool
	digest   string // spec.ref.digest; spec.source.targetRevision on Argo CD
	revision string // status.artifact.revision; status.sync.revision on Argo CD
	// verified is true when spec.verify names the publisher's identity and
	// Flux reports SourceVerified for the current artifact revision.
	verified bool
}

// applier is the Stage's applier object (a Flux OCIRepository, an Argo CD
// Application or a Flux HelmRelease) as the promoter sees it, with what
// differs between kinds.
type applier interface {
	// object is the object the Stage's applyRef names.
	object() *unstructured.Unstructured
	// src is the repository the applier takes versions from.
	src() *source
	// target names where the promoter writes the version, for messages.
	target() string
	// setDigest writes digest and asks the applier to reconcile now.
	setDigest(ctx context.Context, c client.Client, digest string, now time.Time) error
	// applied reports whether the applier applied digest (Cluster.applied).
	applied(ctx context.Context, c Cluster, ns, digest string) (ok, failed bool, msg string, err error)
	// mediated is the Mediated condition of a labelled applier.
	mediated(ctx context.Context, c Cluster, ns string) (delivery.Condition, error)
	// notReady says what keeps a labelled applier from taking a version,
	// nil when nothing does; what names it in the message.
	notReady(what string) *delivery.Condition
}

// fluxApplier is a Flux OCIRepository applied by Kustomizations.
type fluxApplier struct{ *source }

// argoApplier is an Argo CD Application with one OCI spec.source.
type argoApplier struct{ *source }

func (a fluxApplier) object() *unstructured.Unstructured { return a.obj }
func (a fluxApplier) src() *source                       { return a.source }
func (a argoApplier) object() *unstructured.Unstructured { return a.obj }
func (a argoApplier) src() *source                       { return a.source }

// ApplyRefLabel marks the object a Stage applies through. Admission guards
// the pointer of a labelled object only, and lets the promoter write nothing
// else.
const ApplyRefLabel = "delivery.pacto.trianalab.io/apply-ref"

// cached is the reader of the watched kinds: Cache, or Client without one.
func (c Cluster) cached(kind string) client.Reader {
	if c.Cache == nil || kind == delivery.ApplierArgoCD && !c.CacheApplications || kind == delivery.ApplierHelmRelease && !c.FluxHelm {
		return c.Client
	}
	return c.Cache
}

// applier reads the Stage's applier object from the cache, which can lag the
// apiserver by a watch event. A decision on its digest reads freshApplier.
func (c Cluster) applier(ctx context.Context, ns string, ref delivery.ApplyRef) (applier, error) {
	return c.applierFrom(ctx, c.cached(ref.Kind), ns, ref)
}

// freshApplier reads the Stage's applier object from the apiserver: the
// foreign-writer check, the witnessed override and the ship path decide on
// its digest, and a stale one would freeze a Stage or record a move falsely.
func (c Cluster) freshApplier(ctx context.Context, ns string, ref delivery.ApplyRef) (applier, error) {
	return c.applierFrom(ctx, c.Client, ns, ref)
}

func (c Cluster) applierFrom(ctx context.Context, r client.Reader, ns string, ref delivery.ApplyRef) (applier, error) {
	if ref.Kind == delivery.ApplierHelmRelease {
		return helmRelease(ctx, r, ns, ref.Name)
	}
	if ref.Kind != delivery.ApplierArgoCD {
		s, err := ociRepository(ctx, r, ns, ref.Name)
		if err != nil {
			return nil, err
		}
		return fluxApplier{s}, nil
	}
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvkApplication)
	if err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, u); err != nil {
		return nil, err
	}
	// ponytail: one OCI spec.source; multi-source Applications (spec.sources)
	// are not supported until a team needs one.
	url, _, _ := unstructured.NestedString(u.Object, "spec", "source", "repoURL")
	digest, _, _ := unstructured.NestedString(u.Object, "spec", "source", "targetRevision")
	revision, _, _ := unstructured.NestedString(u.Object, "status", "sync", "revision")
	return argoApplier{&source{obj: u, repo: delivery.RepoFromURL(url), digest: digest, revision: revision}}, nil
}

func (a fluxApplier) target() string { return "spec.ref.digest of OCIRepository " + a.obj.GetName() }
func (a argoApplier) target() string {
	return "spec.source.targetRevision of Application " + a.obj.GetName()
}

func ociRepository(ctx context.Context, r client.Reader, ns, name string) (*source, error) {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvkOCIRepository)
	if err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, u); err != nil {
		return nil, err
	}
	url, _, _ := unstructured.NestedString(u.Object, "spec", "url")
	insecure, _, _ := unstructured.NestedBool(u.Object, "spec", "insecure")
	digest, _, _ := unstructured.NestedString(u.Object, "spec", "ref", "digest")
	revision, _, _ := unstructured.NestedString(u.Object, "status", "artifact", "revision")
	return &source{obj: u, repo: delivery.RepoFromURL(url), insecure: insecure, digest: digest, revision: revision,
		verified: pinsIdentity(u.Object) && sourceVerified(u.Object, delivery.RevisionDigest(revision))}, nil
}

// pinsIdentity reports whether spec.verify names whose signature counts: a
// secretRef holding the publisher's public key, or a matchOIDCIdentity for
// keyless signatures. Without either, Flux accepts a keyless signature from
// any identity, so anyone who can push could sign their own artifact. Flux
// reads issuer and subject as regular expressions, so each must be anchored
// (^...$) and must not match a foreign identity: ".*", or a subject left open
// at the end, would admit any keyless signer.
func pinsIdentity(obj map[string]any) bool {
	provider, _, _ := unstructured.NestedString(obj, "spec", "verify", "provider")
	secret, _, _ := unstructured.NestedString(obj, "spec", "verify", "secretRef", "name")
	ids, _, _ := unstructured.NestedSlice(obj, "spec", "verify", "matchOIDCIdentity")
	if provider == "" {
		return false
	}
	if secret != "" {
		return true
	}
	for _, id := range ids {
		m, _ := id.(map[string]any)
		issuer, _ := m["issuer"].(string)
		subject, _ := m["subject"].(string)
		if !pinned(issuer) || !pinned(subject) {
			return false
		}
	}
	return len(ids) > 0
}

// foreignIdentity is an issuer and subject no publisher has: a pattern that
// matches it matches anyone.
const foreignIdentity = "https://example.invalid/x"

// pinned reports whether the Flux identity pattern p is anchored at both
// ends, is a valid expression and does not match foreignIdentity.
func pinned(p string) bool {
	re, err := regexp.Compile(p)
	return err == nil && strings.HasPrefix(p, "^") && strings.HasSuffix(p, "$") && !re.MatchString(foreignIdentity)
}

// sourceVerified reports whether Flux's SourceVerified condition is True for
// digest: Flux names the revision it verified in the condition's message, and
// a revision whose signature fails never becomes the artifact.
func sourceVerified(obj map[string]any, digest string) bool {
	conds, _, _ := unstructured.NestedSlice(obj, "status", "conditions")
	for _, c := range conds {
		m, _ := c.(map[string]any)
		msg, _ := m["message"].(string)
		if m["type"] == "SourceVerified" && m["status"] == statusTrue && digest != "" && strings.Contains(msg, digest) {
			return true
		}
	}
	return false
}

// host is the registry host of the apply repository.
func (a *source) host() string {
	host, _, _ := strings.Cut(a.repo, "/")
	return host
}

// setDigest sets spec.ref.digest and asks Flux to reconcile now. It and
// argoApplier.setDigest are the only writes the promoter ever makes to an
// object it does not own (helmReleaseApplier.setDigest calls it on the chart
// source).
func (a fluxApplier) setDigest(ctx context.Context, c client.Client, digest string, now time.Time) error {
	return mergePatch(ctx, c, a.obj, map[string]any{
		"metadata": map[string]any{"annotations": map[string]string{"reconcile.fluxcd.io/requestedAt": now.Format(time.RFC3339Nano)}},
		"spec":     map[string]any{"ref": map[string]any{"digest": digest}},
	})
}

// setDigest sets spec.source.targetRevision and asks Argo CD to refresh.
func (a argoApplier) setDigest(ctx context.Context, c client.Client, digest string, _ time.Time) error {
	return mergePatch(ctx, c, a.obj, map[string]any{
		"metadata": map[string]any{"annotations": map[string]string{"argocd.argoproj.io/refresh": "normal"}},
		"spec":     map[string]any{"source": map[string]any{"targetRevision": digest}},
	})
}

func mergePatch(ctx context.Context, c client.Client, obj client.Object, patch map[string]any) error {
	b, _ := json.Marshal(patch)
	return c.Patch(ctx, obj, client.RawPatch(types.MergePatchType, b))
}

// applied reports whether the applier a applied digest: failed is a
// confirmed failure, ok is a confirmed success, and neither means "not yet".
func (c Cluster) applied(ctx context.Context, ns string, a applier, digest string) (ok, failed bool, msg string, err error) {
	return a.applied(ctx, c, ns, digest)
}

// applied reports whether Flux applied digest from the apply repository:
// failed is a confirmed failure (Flux attempted digest and reports it not
// Ready for a reason other than progress), ok is a confirmed success, and
// neither means "not yet". It reads the Kustomizations live, as the ship path
// reads a.
func (a fluxApplier) applied(ctx context.Context, c Cluster, ns, digest string) (ok, failed bool, msg string, err error) {
	if delivery.RevisionDigest(a.revision) != digest {
		return false, false, "the OCIRepository has not fetched " + digest + " yet", nil
	}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvkKustomization)
	if err := c.Client.List(ctx, list); err != nil {
		return false, false, "", err
	}
	found := 0
	for _, k := range list.Items {
		if !appliesRepo(&k, ns, a.obj.GetName()) {
			continue
		}
		found++
		if failed, msg := kustomizationApplied(&k, digest); msg != "" {
			return false, failed, msg, nil
		}
	}
	if found == 0 {
		return false, false, "no Kustomization applies OCIRepository " + a.obj.GetName(), nil
	}
	return true, false, "", nil
}

// kustomizationApplied reads whether the Kustomization k applied digest: an
// empty msg is a success, failed a confirmed failure and anything else says
// why it is "not yet".
func kustomizationApplied(k *unstructured.Unstructured, digest string) (failed bool, msg string) {
	ready, reason, message := readyCondition(k.Object)
	applied, _, _ := unstructured.NestedString(k.Object, "status", "lastAppliedRevision")
	attempted, _, _ := unstructured.NestedString(k.Object, "status", "lastAttemptedRevision")
	switch {
	case ready == statusTrue && delivery.RevisionDigest(applied) == digest:
		return false, ""
	// Flux marks Ready Unknown (Progressing) at the start of every
	// reconcile, the periodic re-apply of a revision it already applied
	// included. That revision is still applied. Reading it as "not yet"
	// made a Stage, which that very status patch wakes, see "not yet" on
	// every Flux interval and never verify.
	case ready == statusUnknown && delivery.RevisionDigest(applied) == digest && delivery.RevisionDigest(attempted) == digest:
		return false, ""
	case ready == statusFalse && delivery.RevisionDigest(attempted) == digest && reason != "Progressing" && reason != "DependencyNotReady":
		return true, fmt.Sprintf("Kustomization %s failed to apply %s: %s: %s", k.GetName(), digest, reason, message)
	}
	return false, "Kustomization " + k.GetName() + " has not applied " + digest + " yet"
}

// applied reads whether Argo CD applied digest: Synced and Healthy at that
// revision is a success; a sync of digest that Failed or Errored, or the
// Application Degraded once Argo CD synced digest, is a confirmed failure;
// anything else is "not yet" (Progressing, OutOfSync or still at another
// revision). status.sync.revision is the revision Argo CD compared against,
// which moves to the target on a refresh before any sync, so Degraded counts
// only when the sync is Synced or a sync of digest Succeeded: before that the
// health still describes the previous version's workloads.
func (a argoApplier) applied(_ context.Context, _ Cluster, _, digest string) (ok, failed bool, msg string, err error) {
	name := a.obj.GetName()
	at := delivery.RevisionDigest(a.revision) == digest
	sync, _, _ := unstructured.NestedString(a.obj.Object, "status", "sync", "status")
	health, _, _ := unstructured.NestedString(a.obj.Object, "status", "health", "status")
	phase, _, _ := unstructured.NestedString(a.obj.Object, "status", "operationState", "phase")
	opRev, _, _ := unstructured.NestedString(a.obj.Object, "status", "operationState", "syncResult", "revision")
	opMsg, _, _ := unstructured.NestedString(a.obj.Object, "status", "operationState", "message")
	switch {
	case (phase == "Failed" || phase == "Error") && delivery.RevisionDigest(opRev) == digest:
		return false, true, fmt.Sprintf("Application %s failed to sync %s: %s: %s", name, digest, phase, opMsg), nil
	case !at:
		return false, false, "Application " + name + " has not synced " + digest + " yet", nil
	case health == "Degraded" && (sync == "Synced" || phase == "Succeeded" && delivery.RevisionDigest(opRev) == digest):
		healthMsg, _, _ := unstructured.NestedString(a.obj.Object, "status", "health", "message")
		return false, true, fmt.Sprintf("Application %s is Degraded at %s: %s", name, digest, healthMsg), nil
	case sync == "Synced" && health == "Healthy":
		return true, false, "", nil
	}
	return false, false, fmt.Sprintf("Application %s at %s is %s and %s, not Synced and Healthy yet", name, digest, sync, health), nil
}

func readyCondition(obj map[string]any) (status, reason, message string) {
	conds, _, _ := unstructured.NestedSlice(obj, "status", "conditions")
	for _, c := range conds {
		m, _ := c.(map[string]any)
		if m["type"] == conditionReady {
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
	a, err := c.applier(ctx, stage.Metadata.Namespace, stage.Spec.ApplyRef)
	if err != nil {
		return delivery.Revision{Digest: digest, Err: "the applier: " + err.Error()}
	}
	return delivery.ResolveRevision(ctx, c.registryFor(a.src()), a.src().repo, digest)
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

// kustomizations lists every Flux Kustomization, which places the consumers:
// where each Stage's version runs.
func (c Cluster) kustomizations(ctx context.Context) ([]delivery.KustomizationView, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvkKustomization)
	if err := c.cached(delivery.ApplierFlux).List(ctx, list); err != nil {
		return nil, err
	}
	b, _ := list.MarshalJSON()
	return delivery.DecodeKustomizations(b)
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
