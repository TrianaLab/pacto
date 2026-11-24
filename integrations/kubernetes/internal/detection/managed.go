/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package detection

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/managedres"
	"github.com/trianalab/pacto/v4/pkg/workloadshape"
)

// xrdGVK is the CompositeResourceDefinition list the claim and composite
// kinds come from: the chart grants its list when the API is served.
var xrdGVK = schema.GroupVersionKind{Group: "apiextensions.crossplane.io", Version: "v1", Kind: "CompositeResourceDefinition"}

// errUnread is a list the operator may not run: its kind becomes an unread
// detail, never none found.
var errUnread = errors.New("forbidden")

// managedRun is what one managed-resources read collects.
type managedRun struct {
	resources         []fleet.RawManagedResource
	total, unverified int
	unread            []string
	// composites are the composites by uid; claimSecrets the connection
	// Secret of each claim by its ref.
	composites   map[string]managedres.ComposedClaim
	claimSecrets map[string]string
}

// managed reads the managed resources of the catalog kinds discovery serves,
// with the uncached reader, and returns its entry: a kind the operator may
// not list (a SelfSubjectAccessReview says no) is unread, and a review that
// fails makes the entry unknown; a resource's claim
// is kept only when its composite confirms it. Nothing is read without
// Readers.Managed or in a namespace-list install.
func (d *Detector) managed(ctx context.Context, res *detect.Result) {
	if d.Readers.Managed == nil {
		return
	}
	const kind = fleet.ProviderManagedResources
	if len(d.Namespaces) > 0 {
		res.Entries = append(res.Entries, detect.ManagedEntry(nil, nil, 0, 0, true))
		return
	}
	served, err := d.Readers.Managed(ctx)
	if err != nil {
		res.Entries = append(res.Entries, detect.UnknownEntry(kind, "API discovery failed: "+reason(err)))
		return
	}
	run := &managedRun{composites: map[string]managedres.ComposedClaim{}, claimSecrets: map[string]string{}}
	if err := d.claims(ctx, run); err != nil {
		res.Entries = append(res.Entries, detect.UnknownEntry(kind, "reading the cluster failed: "+reason(err)))
		return
	}
	var groups []string
	for _, gv := range slices.Sorted(maps.Keys(served)) {
		group, version, _ := strings.Cut(gv, "/")
		groups = append(groups, group)
		for _, r := range served[gv] {
			allowed, err := d.mayList(ctx, group, r.Name)
			if err != nil {
				res.Entries = append(res.Entries, detect.UnknownEntry(kind, "the access review failed: "+reason(err)))
				return
			}
			if !allowed {
				run.unread = append(run.unread, group+"/"+r.Name)
				continue
			}
			gvk := schema.GroupVersionKind{Group: group, Version: version, Kind: r.Kind}
			if err := d.listAll(ctx, gvk, func(u *unstructured.Unstructured) { d.addManaged(run, gvk, u) }); err != nil {
				res.Entries = append(res.Entries, detect.UnknownEntry(kind, "reading the cluster failed: "+reason(err)))
				return
			}
		}
	}
	res.ManagedResources, res.ManagedResourcesTotal = run.resources, run.total
	res.Entries = append(res.Entries, detect.ManagedEntry(slices.Compact(groups), run.unread, run.total, run.unverified, false))
}

// addManaged counts u, a managed resource of gvk, and keeps it while fewer
// than [detect.MaxManagedResources] are kept, with its verified claim and
// that claim's connection Secret.
func (d *Detector) addManaged(run *managedRun, gvk schema.GroupVersionKind, u *unstructured.Unstructured) {
	r, c, ok := managedres.FromObject(gvk.Group, gvk.Kind, u.Object, externalcatalog.Default(), d.Now().UTC())
	if !ok {
		return // discovery offered a kind no catalog row names
	}
	if run.total++; len(run.resources) == detect.MaxManagedResources {
		return
	}
	r.Cluster, r.ClusterName = d.Cluster, d.ClusterName
	if c.Name != "" {
		if ref, ok := managedres.VerifiedClaim(u.Object, c, run.composites); ok {
			r.Claim = ref
			if s := run.claimSecrets[ref]; s != "" {
				r.ConnectionSecrets = append(r.ConnectionSecrets, s)
			}
		} else {
			run.unverified++
		}
	}
	run.resources = append(run.resources, r)
}

// claims reads every CompositeResourceDefinition, then the composites (for
// the claim each names) and the claims (for the connection Secret each
// writes to) of each. A list the operator may not run is an unread detail;
// an API that is not served reads none.
// ponytail: composites and claims are held whole for one run; bound them if a
// cluster runs hundreds of thousands.
func (d *Detector) claims(ctx context.Context, run *managedRun) error {
	var xrds []*unstructured.Unstructured
	err := d.listAll(ctx, xrdGVK, func(u *unstructured.Unstructured) { xrds = append(xrds, u) })
	if errors.Is(err, errUnread) {
		run.unread = append(run.unread, xrdGVK.Group+"/compositeresourcedefinitions")
		return nil
	}
	if err != nil {
		return err
	}
	for _, xrd := range xrds {
		group, version := str(xrd, "spec", "group"), xrdVersion(xrd)
		lists := []struct{ kind, plural string }{
			{str(xrd, "spec", "names", "kind"), str(xrd, "spec", "names", "plural")},
			{str(xrd, "spec", "claimNames", "kind"), str(xrd, "spec", "claimNames", "plural")},
		}
		for i, l := range lists {
			if l.kind == "" || version == "" {
				continue
			}
			composite := i == 0
			err := d.listAll(ctx, schema.GroupVersionKind{Group: group, Version: version, Kind: l.kind}, func(u *unstructured.Unstructured) {
				if composite {
					run.composites[string(u.GetUID())] = composedClaim(u)
					return
				}
				if s, ref := managedres.FromClaim(l.kind, u.Object); s != "" {
					run.claimSecrets[ref] = s
				}
			})
			switch {
			case errors.Is(err, errUnread):
				run.unread = append(run.unread, group+"/"+l.plural)
			case err != nil:
				return err
			}
		}
	}
	return nil
}

// xrdVersion is the version of xrd's kinds to read: the referenceable one,
// else the first served, empty when none is.
func xrdVersion(xrd *unstructured.Unstructured) string {
	versions, _, _ := unstructured.NestedSlice(xrd.Object, "spec", "versions")
	served := ""
	for _, v := range versions {
		m, _ := v.(map[string]any)
		name, _ := m["name"].(string)
		if ref, _ := m["referenceable"].(bool); ref {
			return name
		}
		if ok, _ := m["served"].(bool); ok && served == "" {
			served = name
		}
	}
	return served
}

// composedClaim is what composite u says about its claim, and its owners.
func composedClaim(u *unstructured.Unstructured) managedres.ComposedClaim {
	c := managedres.ComposedClaim{Kind: str(u, "spec", "claimRef", "kind"), Namespace: str(u, "spec", "claimRef", "namespace"), Name: str(u, "spec", "claimRef", "name")}
	for _, o := range u.GetOwnerReferences() {
		c.Owners = append(c.Owners, string(o.UID))
	}
	return c
}

// str is the string at path in u, empty when missing.
func str(u *unstructured.Unstructured, path ...string) string {
	s, _, _ := unstructured.NestedString(u.Object, path...)
	return s
}

// mayList asks the API server whether the operator may list resource of
// group cluster-wide. A review that fails is its error: whether the
// operator may list it is then unknown, never no.
func (d *Detector) mayList(ctx context.Context, group, resource string) (bool, error) {
	r := &authorizationv1.SelfSubjectAccessReview{Spec: authorizationv1.SelfSubjectAccessReviewSpec{
		ResourceAttributes: &authorizationv1.ResourceAttributes{Verb: "list", Group: group, Resource: resource}}}
	if err := d.Writer.Create(ctx, r); err != nil {
		return false, err
	}
	return r.Status.Allowed, nil
}

// listAll lists every object of gvk cluster-wide in pages of pageSize and
// hands each to fn. A kind that is not served lists nothing; a Forbidden
// list is errUnread.
func (d *Detector) listAll(ctx context.Context, gvk schema.GroupVersionKind, fn func(*unstructured.Unstructured)) error {
	gvk.Kind += "List"
	next := ""
	for {
		l := &unstructured.UnstructuredList{}
		l.SetGroupVersionKind(gvk)
		switch err := d.Reader.List(ctx, l, client.Limit(pageSize), client.Continue(next)); {
		case meta.IsNoMatchError(err):
			return nil
		case apierrors.IsForbidden(err):
			return errUnread
		case err != nil:
			return err
		}
		for i := range l.Items {
			fn(&l.Items[i])
		}
		if next = l.GetContinue(); next == "" {
			return nil
		}
	}
}

// secretRefs collects the Secret names each workload's pods reference, once
// per workload and Secret, on one cluster; total counts them past the bound.
type secretRefs struct {
	seen          map[fleet.RawSecretRef]bool
	out           []fleet.RawSecretRef
	total         int
	cluster, name string
}

// add records the Secret names p references for its owner, p itself when it
// has none, keeping them while fewer than detect.MaxSecretRefs are kept and
// counting every one. Only the names in the pod spec are read, never a
// Secret.
func (s *secretRefs) add(owner detect.Object, p *corev1.Pod) {
	if owner.Kind == "" {
		owner = detect.Object{Kind: "Pod", Namespace: p.Namespace, Name: p.Name}
	}
	spec, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&p.Spec)
	for _, secret := range workloadshape.SecretNames("Pod", map[string]any{"spec": map[string]any{"template": map[string]any{"spec": spec}}}) {
		ref := fleet.RawSecretRef{Cluster: s.cluster, ClusterName: s.name, Namespace: p.Namespace, Kind: owner.Kind, Name: owner.Name, Secret: secret}
		if s.seen[ref] {
			continue
		}
		s.seen[ref], s.total = true, s.total+1
		if len(s.out) < detect.MaxSecretRefs {
			s.out = append(s.out, ref)
		}
	}
}
