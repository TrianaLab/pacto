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
// and there is none when there is nothing to propose. argoErr and fluxErr
// are why discovery could not tell whether Argo CD or Flux is served: such a
// source reads unknown, never absent.
func (d *Detector) inventory(ctx context.Context, argoServed, fluxServed, helmServed bool, argoErr, fluxErr error) ([]detect.Entry, *detect.Clusters) {
	in := detect.InventoryInput{AsOf: d.Now().UTC(), Cluster: d.Cluster, Granted: d.Inventory.SecretNamespaces, Errs: map[string]error{},
		Hardened: d.Installation != nil && slices.Contains(d.Installation.Profile, "hardened")}
	if len(d.Namespaces) > 0 {
		in.Scoped = scopedReason
	}
	if fluxServed || helmServed || fluxErr != nil {
		d.readFlux(ctx, &in, fluxServed, helmServed, fluxErr)
	}
	if argoServed || argoErr != nil {
		in.Sources = append(in.Sources, detect.SourceArgoCD)
		in.ArgoControl, in.ArgoControlErr = d.argoNamespaces(ctx)
		in.ArgoControlErr = inventoryErr(in.ArgoControlErr)
		in.Proposed = append(in.Proposed, in.ArgoControl...)
	}
	if !d.Inventory.Enabled {
		if e := detect.InventoryProposal(in.Proposed); e.Kind != "" {
			return []detect.Entry{e}, nil
		}
		return nil, nil
	}
	d.readSecrets(ctx, &in, argoErr)
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
		gv, err := d.servedOf(src.gvs)
		if gv == "" && err == nil {
			continue
		}
		in.Sources = append(in.Sources, src.name)
		if gv != "" {
			*src.into, err = src.list(ctx, gv)
		}
		in.Errs[src.name] = inventoryErr(err)
	}
	c, e := detect.Inventory(in)
	return []detect.Entry{e}, &c
}

// readFlux adds the flux source to in with its kubeconfig refs, unknown on
// fluxErr (discovery could not tell), and proposes the namespaces of the
// Secret refs: a ConfigMap kubeconfig needs no Secret grant.
func (d *Detector) readFlux(ctx context.Context, in *detect.InventoryInput, kustomizations, helmReleases bool, fluxErr error) {
	refs, err := d.fluxRefs(ctx, kustomizations, helmReleases)
	in.Sources = append(in.Sources, detect.SourceFlux)
	in.FluxRefs, in.Errs[detect.SourceFlux] = refs, inventoryErr(errors.Join(fluxErr, err))
	for _, r := range refs {
		if r.Kind == "Secret" {
			in.Proposed = append(in.Proposed, r.Namespace)
		}
	}
}

// readSecrets fills in with the granted namespaces' Secrets, the Argo CD
// cluster Secrets among them and why a namespace was not read; with none read
// (or argoErr, Argo CD's discovery error) the argocd source is unknown.
func (d *Detector) readSecrets(ctx context.Context, in *detect.InventoryInput, argoErr error) {
	secrets, failed := d.secrets(ctx)
	in.Secrets, in.SecretErrs = map[string]detect.Meta{}, failed
	in.Errs[detect.SourceArgoCD] = inventoryErr(argoErr)
	if len(failed) > 0 && len(failed) == len(d.Inventory.SecretNamespaces) && argoErr == nil {
		// No granted namespace was read: the source is unknown, not partial.
		in.Errs[detect.SourceArgoCD], in.SecretErrs = failed[slices.Sorted(maps.Keys(failed))[0]], nil
	}
	for _, m := range secrets {
		in.Secrets[m.Namespace+"/"+m.Name] = m
		if m.Labels["argocd.argoproj.io/secret-type"] == "cluster" {
			in.Argo = append(in.Argo, m)
		}
	}
}

// servedOf is the first of gvs discovery serves; empty with no error when
// discovery says none is (an inventory nobody runs gives no source), and with
// the discovery error when it could not tell.
func (d *Detector) servedOf(gvs []string) (string, error) {
	var errs []error
	for _, gv := range gvs {
		ok, err := d.Served(gv)
		if err == nil && ok {
			return gv, nil
		}
		errs = append(errs, err)
	}
	return "", errors.Join(errs...)
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

// secrets lists the metadata of the Secrets in the granted namespaces, each
// on its own so one that fails keeps the others, with why each failed one
// failed: no Secret data is ever requested.
func (d *Detector) secrets(ctx context.Context) ([]detect.Meta, map[string]error) {
	var out []detect.Meta
	var failed map[string]error
	for _, ns := range d.Inventory.SecretNamespaces {
		ms, err := d.listMeta(ctx, "v1", "Secret", []string{ns})
		if err != nil {
			if failed == nil {
				failed = map[string]error{}
			}
			failed[ns] = inventoryErr(err)
			continue
		}
		out = append(out, ms...)
	}
	return out, failed
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
// configMapRef, with their key) of the Flux Kustomizations and HelmReleases
// in scope; the referenced object sits in the Flux object's namespace.
func (d *Detector) fluxRefs(ctx context.Context, kustomizations, helmReleases bool) ([]detect.KubeConfigRef, error) {
	var out []detect.KubeConfigRef
	each := func(o unstructured.Unstructured) {
		for kind, field := range map[string]string{"Secret": "secretRef", "ConfigMap": "configMapRef"} {
			if name, _, _ := unstructured.NestedString(o.Object, "spec", "kubeConfig", field, "name"); name != "" {
				key, _, _ := unstructured.NestedString(o.Object, "spec", "kubeConfig", field, "key")
				out = append(out, detect.KubeConfigRef{Kind: kind, Namespace: o.GetNamespace(), Name: name, Key: key})
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
// failed read proposes what the other kind found and says why.
func (d *Detector) argoNamespaces(ctx context.Context) ([]string, error) {
	var out []string
	errs := make([]error, 0, 2)
	for _, kind := range []string{"StatefulSet", "Deployment"} {
		ms, err := d.listMeta(ctx, "apps/v1", kind, d.scopes(),
			client.MatchingLabelsSelector{Selector: argoControllers})
		errs = append(errs, err)
		for _, m := range ms {
			out = append(out, m.Namespace)
		}
	}
	return out, errors.Join(errs...)
}
