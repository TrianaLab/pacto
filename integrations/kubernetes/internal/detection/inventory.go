/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package detection

import (
	"context"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v4/pkg/detect"
)

// The inventory reads are list only: detection is uncached and opens no
// watch. Secrets are listed only in the namespaces a person names, through
// the chart's namespaced Roles, never cluster-wide.
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=list
// +kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters,verbs=list
// +kubebuilder:rbac:groups=management.cattle.io,resources=clusters,verbs=list

// The inventory group versions: Cluster API is asked at v1beta2, then
// v1beta1.
const (
	capiGroup = "cluster.x-k8s.io"
	ocmGV     = "cluster.open-cluster-management.io/v1"
	rancherGV = "management.cattle.io/v3"
)

// argoControllers are the Argo CD control-plane workloads whose namespaces
// hold its cluster Secrets.
var argoControllers, _ = labels.Parse("app.kubernetes.io/name in (argocd-application-controller,argocd-applicationset-controller)")

// InventoryConfig is the cluster inventory a person turned on: with Enabled
// the Detector lists candidate clusters, reading Secret metadata only in
// SecretNamespaces.
type InventoryConfig struct {
	Enabled          bool
	SecretNamespaces []string
}

// inventory is the cluster-inventory entry and, with the inventory on, the
// candidates. With it off the entry only proposes the namespaces to grant,
// and there is none when there is nothing to propose.
func (d *Detector) inventory(ctx context.Context, argoServed, fluxServed, helmServed bool) ([]detect.Entry, *detect.Clusters) {
	in := detect.InventoryInput{AsOf: d.Now().UTC(), Cluster: d.Cluster, Granted: d.Inventory.SecretNamespaces, Errs: map[string]error{},
		Hardened: d.Installation != nil && slices.Contains(d.Installation.Profile, "hardened")}
	if fluxServed || helmServed {
		refs, err := d.fluxRefs(ctx, fluxServed, helmServed)
		in.Sources = append(in.Sources, detect.SourceFlux)
		in.FluxRefs, in.Errs[detect.SourceFlux] = refs, inventoryErr(err)
		for _, r := range refs {
			in.Proposed = append(in.Proposed, r.Namespace)
		}
	}
	if argoServed {
		in.Sources = append(in.Sources, detect.SourceArgoCD)
		in.Proposed = append(in.Proposed, d.argoNamespaces(ctx)...)
	}
	if !d.Inventory.Enabled {
		if e := detect.InventoryProposal(in.Proposed); e.Kind != "" {
			return []detect.Entry{e}, nil
		}
		return nil, nil
	}
	secrets, err := d.secrets(ctx)
	in.Secrets, in.Errs[detect.SourceArgoCD] = map[string]detect.Meta{}, inventoryErr(err)
	for _, m := range secrets {
		in.Secrets[m.Namespace+"/"+m.Name] = m
		if m.Labels["argocd.argoproj.io/secret-type"] == "cluster" {
			in.Argo = append(in.Argo, m)
		}
	}
	for _, src := range []struct {
		name string
		gvs  []string
		into *[]detect.Meta
		list func(context.Context, string) ([]detect.Meta, error)
	}{
		{detect.SourceCAPI, []string{capiGroup + "/v1beta2", capiGroup + "/v1beta1"}, &in.CAPI, d.clusterObjects},
		{detect.SourceOCM, []string{ocmGV}, &in.OCM, d.managedClusters},
		{detect.SourceRancher, []string{rancherGV}, &in.Rancher, d.clusterObjects},
	} {
		gv := d.servedOf(src.gvs)
		if gv == "" {
			continue
		}
		in.Sources = append(in.Sources, src.name)
		*src.into, err = src.list(ctx, gv)
		in.Errs[src.name] = inventoryErr(err)
	}
	c, e := detect.Inventory(in)
	return []detect.Entry{e}, &c
}

// servedOf is the first of gvs discovery serves, empty when none is or
// discovery fails: an inventory nobody runs gives no source.
func (d *Detector) servedOf(gvs []string) string {
	for _, gv := range gvs {
		if ok, err := d.Served(gv); err == nil && ok {
			return gv
		}
	}
	return ""
}

// inventoryErr is err for detect.Inventory: a Forbidden list wraps
// detect.ErrForbidden, any other error its reason class.
func inventoryErr(err error) error {
	switch {
	case err == nil:
		return nil
	case apierrors.IsForbidden(err):
		return detect.ErrForbidden
	}
	return classed(err)
}

// listMeta lists the objects of kind in gv as metadata only, page by page, in
// each of namespaces ("" is every namespace).
func (d *Detector) listMeta(ctx context.Context, gv, kind string, namespaces []string, opts ...client.ListOption) ([]detect.Meta, error) {
	gvk := schema.FromAPIVersionAndKind(gv, kind+"List")
	var out []detect.Meta
	for _, ns := range namespaces {
		next := ""
		for {
			l := &metav1.PartialObjectMetadataList{}
			l.SetGroupVersionKind(gvk)
			if err := d.Reader.List(ctx, l, append([]client.ListOption{client.InNamespace(ns), client.Limit(pageSize), client.Continue(next)}, opts...)...); err != nil {
				return nil, err
			}
			for _, o := range l.Items {
				out = append(out, detect.Meta{Kind: kind, Namespace: o.Namespace, Name: o.Name, Labels: o.Labels, Annotations: o.Annotations})
			}
			if next = l.Continue; next == "" {
				break
			}
		}
	}
	return out, nil
}

// secrets lists the metadata of the Secrets in the granted namespaces: no
// Secret data is ever requested.
func (d *Detector) secrets(ctx context.Context) ([]detect.Meta, error) {
	return d.listMeta(ctx, "v1", "Secret", d.Inventory.SecretNamespaces)
}

// clusterObjects lists the Cluster objects of gv (Cluster API, namespaced, or
// Rancher, cluster-scoped) across namespaces as metadata: no status, which
// older Rancher releases kept credentials in, and no spec.displayName.
func (d *Detector) clusterObjects(ctx context.Context, gv string) ([]detect.Meta, error) {
	return d.listMeta(ctx, gv, "Cluster", []string{""})
}

// managedClusters lists OCM managed clusters unstructured, since the one
// field read, status.clusterClaims, lives outside metadata; only metadata and
// the claims are kept.
func (d *Detector) managedClusters(ctx context.Context, gv string) ([]detect.Meta, error) {
	var out []detect.Meta
	err := d.listUnstructured(ctx, schema.FromAPIVersionAndKind(gv, "ManagedClusterList"), []string{""}, func(o unstructured.Unstructured) {
		m := detect.Meta{Kind: "ManagedCluster", Name: o.GetName(), Labels: o.GetLabels(), Annotations: o.GetAnnotations()}
		claims, _, _ := unstructured.NestedSlice(o.Object, "status", "clusterClaims")
		for _, c := range claims {
			cm, _ := c.(map[string]any)
			name, _ := cm["name"].(string)
			value, _ := cm["value"].(string)
			if name != "" {
				if m.Claims == nil {
					m.Claims = map[string]string{}
				}
				m.Claims[name] = value
			}
		}
		out = append(out, m)
	})
	return out, err
}

// listUnstructured lists gvk page by page in each of namespaces, handing
// every object to each.
func (d *Detector) listUnstructured(ctx context.Context, gvk schema.GroupVersionKind, namespaces []string, each func(unstructured.Unstructured)) error {
	for _, ns := range namespaces {
		next := ""
		for {
			l := &unstructured.UnstructuredList{}
			l.SetGroupVersionKind(gvk)
			if err := d.Reader.List(ctx, l, client.InNamespace(ns), client.Limit(pageSize), client.Continue(next)); err != nil {
				return err
			}
			for _, o := range l.Items {
				each(o)
			}
			if next = l.GetContinue(); next == "" {
				break
			}
		}
	}
	return nil
}

// fluxRefs are the kubeconfig refs (spec.kubeConfig.secretRef or
// configMapRef) of the Flux Kustomizations and HelmReleases in scope; the
// referenced object sits in the Flux object's namespace.
func (d *Detector) fluxRefs(ctx context.Context, kustomizations, helmReleases bool) ([]detect.Object, error) {
	var out []detect.Object
	each := func(o unstructured.Unstructured) {
		for kind, field := range map[string]string{"Secret": "secretRef", "ConfigMap": "configMapRef"} {
			if name, _, _ := unstructured.NestedString(o.Object, "spec", "kubeConfig", field, "name"); name != "" {
				out = append(out, detect.Object{Kind: kind, Namespace: o.GetNamespace(), Name: name})
			}
		}
	}
	for _, l := range []struct {
		on  bool
		gvk schema.GroupVersionKind
	}{
		{kustomizations, schema.FromAPIVersionAndKind(fluxGV, "KustomizationList")},
		{helmReleases, schema.FromAPIVersionAndKind(fluxHelmGV, "HelmReleaseList")},
	} {
		if !l.on {
			continue
		}
		if err := d.listUnstructured(ctx, l.gvk, d.scopes(), each); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// argoNamespaces are the namespaces of Argo CD's application and
// applicationset controllers, read as metadata: Argo CD keeps its cluster
// Secrets only there, while Applications may live in tenant namespaces. A
// failed read proposes nothing.
func (d *Detector) argoNamespaces(ctx context.Context) []string {
	var out []string
	for _, kind := range []string{"StatefulSet", "Deployment"} {
		ms, err := d.listMeta(ctx, "apps/v1", kind, d.scopes(),
			client.MatchingLabelsSelector{Selector: argoControllers})
		if err != nil {
			continue
		}
		for _, m := range ms {
			out = append(out, m.Namespace)
		}
	}
	return out
}
