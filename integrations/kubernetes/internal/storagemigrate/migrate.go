/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package storagemigrate rewrites every stored object of a CRD in its current
// storage version and then drops the old version from the CRD's
// status.storedVersions, so a later CRD can stop serving it.
package storagemigrate

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/retry"
)

// maxNamed bounds how many failed objects one log line names.
const maxNamed = 20

var crdGVR = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

// Target is one CRD to migrate. Subresource is the write path ("status" or
// "" for the main resource): the one the host already may patch.
type Target struct {
	CRD         string
	Resource    schema.GroupVersionResource
	Kind        string
	Subresource string
}

// Migrator is a manager Runnable that migrates its Targets from From to To
// and retries with Backoff until none still stores From. It runs only on the
// elected leader: controller-runtime elects every Runnable that does not opt
// out through NeedLeaderElection.
type Migrator struct {
	Client    dynamic.Interface
	Discovery discovery.DiscoveryInterface
	Targets   []Target
	From, To  string
	Log       logr.Logger
	// Backoff between passes over the pending targets; zero is 1 minute
	// doubling up to 10 minutes.
	Backoff wait.Backoff
	// HashPoll, HashReads and HashTimeout bound the wait for discovery to
	// report the To storage version hash; zero is 5 seconds, 3 reads and 2 minutes.
	HashPoll    time.Duration
	HashReads   int
	HashTimeout time.Duration
}

// Start migrates every target, retrying the pending ones with backoff. It
// returns nil once none is pending or when ctx is cancelled.
func (m *Migrator) Start(ctx context.Context) error {
	m.defaults()
	pending := slices.Clone(m.Targets)
	for {
		pending = slices.DeleteFunc(pending, func(t Target) bool { return m.migrateCRD(ctx, t) })
		if len(pending) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(m.Backoff.Step()):
		}
	}
}

func (m *Migrator) defaults() {
	if m.Backoff.Duration == 0 {
		m.Backoff = wait.Backoff{Duration: time.Minute, Factor: 2, Cap: 10 * time.Minute, Steps: math.MaxInt32}
	}
	if m.HashPoll == 0 {
		m.HashPoll = 5 * time.Second
	}
	if m.HashReads == 0 {
		m.HashReads = 3
	}
	if m.HashTimeout == 0 {
		m.HashTimeout = 2 * time.Minute
	}
}

// migrateCRD reports whether t needs nothing more. storedVersions changes
// only after every object was listed and rewritten.
func (m *Migrator) migrateCRD(ctx context.Context, t Target) bool {
	crd, err := m.Client.Resource(crdGVR).Get(ctx, t.CRD, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return true // the provider is optional
	}
	if err != nil {
		m.logf("storage migration: %s could not be read: %v; will retry", t.CRD, err)
		return false
	}
	stored, _, _ := unstructured.NestedStringSlice(crd.Object, "status", "storedVersions")
	if !slices.Contains(stored, m.From) {
		return true
	}
	if storageVersion(crd) != m.To {
		m.logf("storage migration: %s is not served with %s as storage yet; will retry", t.CRD, m.To)
		return false
	}
	if err := m.waitStorageHash(ctx, t); err != nil {
		m.logf("storage migration: %s: discovery did not report %s as the storage version: %v; will retry", t.CRD, m.To, err)
		return false
	}
	n, failed, err := m.rewriteAll(ctx, t)
	if err != nil {
		m.logf("storage migration: %s incomplete after %d objects: %v; storedVersions unchanged", t.CRD, n, err)
		return false
	}
	if len(failed) > 0 {
		m.logf("storage migration: %s could not rewrite %d objects (%v); see MIGRATION.md", t.CRD, len(failed), failed[:min(len(failed), maxNamed)])
		return false
	}
	old, next, err := m.patchStored(ctx, t.CRD)
	if err != nil {
		m.logf("storage migration: %s rewrote %d objects as %s but storedVersions could not be patched: %v; will retry", t.CRD, n, m.To, err)
		return false
	}
	m.logf("storage migration: %s rewrote %d objects as %s; storedVersions %v -> %v", t.CRD, n, m.To, old, next)
	return true
}

func (m *Migrator) logf(format string, args ...any) {
	m.Log.Info(fmt.Sprintf(format, args...))
}

// storageVersion is the name of the CRD version marked storage.
func storageVersion(crd *unstructured.Unstructured) string {
	versions, _, _ := unstructured.NestedSlice(crd.Object, "spec", "versions")
	for _, v := range versions {
		if v, ok := v.(map[string]any); ok && v["storage"] == true {
			name, _ := v["name"].(string)
			return name
		}
	}
	return ""
}

// waitStorageHash waits until discovery reports the To storage version hash
// for t on HashReads consecutive reads.
//
// ponytail: each discovery read reaches one apiserver, so consecutive matches
// lower the risk that another apiserver of an HA control plane still writes
// From after the pass, without removing it. Upgrade path: the StorageVersion
// API (internal.apiserver.k8s.io) once it is generally available.
func (m *Migrator) waitStorageHash(ctx context.Context, t Target) error {
	gv := t.Resource.Group + "/" + m.To
	want := storageHash(t.Resource.Group, m.To, t.Kind)
	matches := 0
	return wait.PollUntilContextTimeout(ctx, m.HashPoll, m.HashTimeout, true, func(context.Context) (bool, error) {
		list, err := m.Discovery.ServerResourcesForGroupVersion(gv)
		if err == nil && hasHash(list, t.Resource.Resource, want) {
			matches++
		} else {
			matches = 0
		}
		return matches >= m.HashReads, nil
	})
}

func hasHash(list *metav1.APIResourceList, resource, want string) bool {
	for _, r := range list.APIResources {
		if r.Name == resource {
			return r.StorageVersionHash == want
		}
	}
	return false
}

// storageHash is the hash the apiserver reports in discovery for a resource
// stored as group/version/kind.
func storageHash(group, version, kind string) string {
	sum := sha256.Sum256([]byte(group + "/" + version + "/" + kind))
	return base64.StdEncoding.EncodeToString(sum[:8])
}

// rewriteAll rewrites every object of t in every namespace and returns how
// many it rewrote and "namespace/name: reason" of each it could not, so a
// transient Conflict or timeout reads apart from an Invalid object. A list error
// aborts the pass.
func (m *Migrator) rewriteAll(ctx context.Context, t Target) (int, []string, error) {
	res := m.Client.Resource(t.Resource)
	n, failed := 0, []string(nil)
	opts := metav1.ListOptions{Limit: 500}
	for {
		list, err := res.List(ctx, opts)
		if err != nil {
			return n, failed, err
		}
		for i := range list.Items {
			obj := &list.Items[i]
			done, err := rewrite(ctx, res.Namespace(obj.GetNamespace()), t.Subresource, obj)
			if err != nil {
				failed = append(failed, obj.GetNamespace()+"/"+obj.GetName()+": "+string(apierrors.ReasonForError(err)))
			} else if done {
				n++
			}
		}
		if opts.Continue = list.GetContinue(); opts.Continue == "" {
			return n, failed, nil
		}
	}
}

// rewrite writes obj back with unchanged content: the merge patch carries only
// its resourceVersion, so a stale read fails with a Conflict and is re-read,
// and the apiserver re-encodes the object in the storage version. A deleted
// object is skipped (false, nil).
func rewrite(ctx context.Context, ri dynamic.ResourceInterface, sub string, obj *unstructured.Unstructured) (bool, error) {
	var subs []string
	if sub != "" {
		subs = []string{sub}
	}
	rv := obj.GetResourceVersion()
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		body := fmt.Sprintf(`{"metadata":{"resourceVersion":%q}}`, rv)
		_, err := ri.Patch(ctx, obj.GetName(), types.MergePatchType, []byte(body), metav1.PatchOptions{}, subs...)
		if apierrors.IsConflict(err) {
			cur, gerr := ri.Get(ctx, obj.GetName(), metav1.GetOptions{})
			if gerr != nil {
				return gerr
			}
			rv = cur.GetResourceVersion()
		}
		return err
	})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// patchStored sets the CRD's storedVersions to nextStored of the current ones.
func (m *Migrator) patchStored(ctx context.Context, name string) (old, next []string, err error) {
	crds := m.Client.Resource(crdGVR)
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		crd, err := crds.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		old, _, _ = unstructured.NestedStringSlice(crd.Object, "status", "storedVersions")
		next = nextStored(old, m.From, m.To)
		body, _ := json.Marshal(map[string]any{ // strings only: it cannot fail
			"metadata": map[string]any{"resourceVersion": crd.GetResourceVersion()},
			"status":   map[string]any{"storedVersions": next},
		})
		_, err = crds.Patch(ctx, name, types.MergePatchType, body, metav1.PatchOptions{}, "status")
		return err
	})
	return old, next, err
}

// nextStored is old without from, with to added when missing.
func nextStored(old []string, from, to string) []string {
	next := slices.DeleteFunc(slices.Clone(old), func(v string) bool { return v == from })
	if !slices.Contains(next, to) {
		next = append(next, to)
	}
	return next
}

func gvr(group, resource string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: group, Version: "v1", Resource: resource}
}

// The operator reads the pacto.trianalab.io CRDs by name and patches their
// storedVersions, and nothing else on any CRD.
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,resourceNames=pactos.pacto.trianalab.io;pactorevisions.pacto.trianalab.io;noderuntimeobservations.pacto.trianalab.io,verbs=get
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions/status,resourceNames=pactos.pacto.trianalab.io;pactorevisions.pacto.trianalab.io;noderuntimeobservations.pacto.trianalab.io,verbs=patch
// +kubebuilder:rbac:groups=pacto.trianalab.io,resources=noderuntimeobservations/status,verbs=patch

// PactoTargets are the pacto.trianalab.io CRDs, written through the status
// subresource the operator patches.
func PactoTargets() []Target {
	const g = "pacto.trianalab.io"
	return []Target{
		{CRD: "pactos." + g, Resource: gvr(g, "pactos"), Kind: "Pacto", Subresource: "status"},
		{CRD: "pactorevisions." + g, Resource: gvr(g, "pactorevisions"), Kind: "PactoRevision", Subresource: "status"},
		{CRD: "noderuntimeobservations." + g, Resource: gvr(g, "noderuntimeobservations"), Kind: "NodeRuntimeObservation", Subresource: "status"},
	}
}

// DeliveryTargets are the delivery.pacto.trianalab.io CRDs. Approvals use the
// main resource: the promoter may patch it, and only the recorder writes
// their status.
func DeliveryTargets() []Target {
	const g = "delivery.pacto.trianalab.io"
	return []Target{
		{CRD: "stages." + g, Resource: gvr(g, "stages"), Kind: "Stage", Subresource: "status"},
		{CRD: "operations." + g, Resource: gvr(g, "operations"), Kind: "Operation", Subresource: "status"},
		{CRD: "approvals." + g, Resource: gvr(g, "approvals"), Kind: "Approval"},
	}
}
