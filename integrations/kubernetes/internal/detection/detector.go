/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package detection is the operator side of what a cluster provides: it reads
// pods, nodes, HTTPRoutes, Ingresses, Dex Services and API discovery, infers
// with pkg/detect and writes the result into one ConfigMap. Detection is
// inert: nothing in the operator reads that ConfigMap back, and nothing it
// finds sets a value, an argument or a grant.
package detection

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/component"
	"github.com/trianalab/pacto/v4/pkg/detect"
)

// Nodes list is the one grant detection adds, cluster-wide installs only; the
// pods, Services, Ingresses and HTTPRoutes it reads are already granted.
// +kubebuilder:rbac:groups="",resources=nodes,verbs=list

// The API group versions detection asks discovery about.
const (
	gatewayGV    = "gateway.networking.k8s.io/v1"
	monitoringGV = "monitoring.coreos.com/v1"
	dexGV        = "dex.coreos.com/v1"
)

// pageSize is the List page of the paginated reads.
const pageSize = 500

// scopedNodes is the reason cloud and nodes are unknown in a namespace-list
// install, where the operator holds no ClusterRole.
const scopedNodes = "with controller.watchNamespaces the operator may not list nodes"

var httpRouteGVK = schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRouteList"}

// Detector is the leader-only runnable that detects on start and every
// Interval, writing only the ConfigMap Name in Namespace.
type Detector struct {
	// Reader must be uncached: detection adds no informer to the manager.
	Reader client.Reader
	Writer client.Client
	// Served reports whether discovery serves a group version; only NotFound
	// is false, any other failure an error.
	Served func(gv string) (bool, error)
	// Namespace and Name are the ConfigMap; DeploymentName is the operator
	// Deployment that owns it.
	Namespace, Name, DeploymentName string
	// Namespaces is the watched scope of a namespace-list install, empty for
	// cluster-wide; Skip are namespaces whose pods never vote.
	Namespaces, Skip []string
	// Cluster and ClusterName are the id and display name of the cluster.
	Cluster, ClusterName string
	Now                  func() time.Time
	Interval             time.Duration

	owner       *metav1ac.OwnerReferenceApplyConfiguration
	warnedOwner bool
}

// Start implements manager.Runnable. Runnables default to leader election,
// so only the leader writes.
func (d *Detector) Start(ctx context.Context) error {
	return component.Run(ctx, "detection", true, d.Interval, d.Sync)
}

// Sync detects once and writes the ConfigMap. It always returns nil: a failed
// read becomes an unknown entry, a failed write is logged, and without its
// owner the ConfigMap is never written (uninstall must collect it).
func (d *Detector) Sync(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("detection")
	if d.owner == nil {
		deploy := &appsv1.Deployment{}
		if err := d.Reader.Get(ctx, client.ObjectKey{Namespace: d.Namespace, Name: d.DeploymentName}, deploy); err != nil {
			if !d.warnedOwner {
				d.warnedOwner = true
				log.Info("Detection waits for the operator Deployment to own its ConfigMap", "deployment", d.DeploymentName, "reason", reason(err))
			}
			return nil
		}
		d.owner = metav1ac.OwnerReference().WithAPIVersion("apps/v1").WithKind("Deployment").WithName(deploy.Name).WithUID(deploy.UID)
	}
	data, err := detect.Encode(d.detect(ctx))
	if err == nil {
		err = d.Writer.Apply(ctx, corev1ac.ConfigMap(d.Name, d.Namespace).
			WithLabels(map[string]string{
				"app.kubernetes.io/part-of":   "pacto",
				"app.kubernetes.io/component": "detection",
				detect.ManagedByLabel:         detect.ManagedBy,
			}).
			WithOwnerReferences(d.owner).
			WithData(map[string]string{detect.ConfigMapKey: string(data)}),
			client.FieldOwner(detect.FieldManager), client.ForceOwnership)
	}
	if err != nil {
		log.Error(err, "Writing the detection ConfigMap failed", "configMap", d.Name, "reason", reason(err))
	}
	return nil
}

// detect reads the cluster and runs every rule.
func (d *Detector) detect(ctx context.Context) detect.Result {
	scoped := len(d.Namespaces) > 0
	res := detect.Result{AsOf: d.Now().UTC(), Cluster: d.Cluster, ClusterName: d.ClusterName, Namespaces: d.Namespaces}

	ws, sampled, err := d.workloads(ctx)
	res.Entries = append(res.Entries, or(err, "registry", func() detect.Entry { return detect.Registries(ws, sampled, !scoped) }))

	if scoped {
		res.Entries = append(res.Entries, detect.UnknownEntry("cloud", scopedNodes), detect.UnknownEntry("nodes", scopedNodes))
	} else {
		ns, sampled, err := d.nodes(ctx)
		why := fmt.Sprintf("sampled the first %d nodes", detect.MaxNodes)
		res.Entries = append(res.Entries,
			or(err, "cloud", func() detect.Entry { return sampledEntry(detect.Cloud(ns), sampled, why) }),
			or(err, "nodes", func() detect.Entry { return sampledEntry(detect.Kernel(ns), sampled, why) }))
	}

	served, servedErr := d.Served(monitoringGV)
	res.Entries = append(res.Entries, detect.Metrics(served, classed(servedErr)))

	dexServed, dexErr := d.Served(dexGV)
	services, err := d.dexServices(ctx)
	res.Entries = append(res.Entries, sampledEntry(detect.SignIn(dexServed, services, classed(errors.Join(dexErr, err))), scoped, scopedReason))

	gwServed, gwErr := d.Served(gatewayGV)
	var routes []detect.Route
	if gwErr == nil && gwServed {
		routes, gwErr = d.routes(ctx)
	}
	ingresses, err := d.ingresses(ctx)
	res.Entries = append(res.Entries, sampledEntry(detect.Exposure(gwServed, classed(errors.Join(gwErr, err)), routes, ingresses), scoped, scopedReason))
	return res
}

// scopedReason marks the namespaced entries of a namespace-list install.
const scopedReason = "read only the watched namespaces"

// or is the unknown entry of kind when err is set, else rule's entry.
func or(err error, kind string, rule func() detect.Entry) detect.Entry {
	if err != nil {
		return detect.UnknownEntry(kind, "reading the cluster failed: "+reason(err))
	}
	return rule()
}

// sampledEntry is e, partial with why when cond holds and e is not unknown.
func sampledEntry(e detect.Entry, cond bool, why string) detect.Entry {
	if !cond || e.Completeness == detect.Unknown {
		return e
	}
	e.Completeness = detect.Partial
	if e.Reason != "" {
		why = e.Reason + "; " + why
	}
	e.Reason = why
	return e
}

// classed is err reduced to its Kubernetes reason class, so an error message
// carrying server addresses never reaches a row.
func classed(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(reason(err))
}

// reason is the Kubernetes reason class of err.
func reason(err error) string {
	if r := apierrors.ReasonForError(err); r != "" {
		return string(r)
	}
	return "the API server did not answer"
}

// scopes are the namespaces to list in: the watched ones, or all ("").
func (d *Detector) scopes() []string {
	if len(d.Namespaces) > 0 {
		return d.Namespaces
	}
	return []string{""}
}

// workloads reads running pods page by page, at most MaxPods, outside the
// skipped namespaces, grouped by controller owner.
func (d *Detector) workloads(ctx context.Context) ([]detect.Workload, bool, error) {
	byID, order, seen := map[string]*detect.Workload{}, []string{}, 0
	for _, ns := range d.scopes() {
		next := ""
		for {
			l := &corev1.PodList{}
			if err := d.Reader.List(ctx, l, client.InNamespace(ns), client.MatchingFields{"status.phase": string(corev1.PodRunning)},
				client.Limit(pageSize), client.Continue(next)); err != nil {
				return nil, false, err
			}
			for i := range l.Items {
				if seen == detect.MaxPods {
					return collect(byID, order), true, nil
				}
				seen++
				p := &l.Items[i]
				if slices.Contains(d.Skip, p.Namespace) {
					continue
				}
				id := string(p.UID)
				if o := metav1.GetControllerOf(p); o != nil {
					id = string(o.UID)
				}
				w, ok := byID[id]
				if !ok {
					w = &detect.Workload{ID: id, Namespace: p.Namespace}
					byID[id] = w
					order = append(order, id)
				}
				for _, c := range p.Spec.Containers {
					w.Images = append(w.Images, c.Image)
				}
			}
			if next = l.Continue; next == "" {
				break
			}
		}
	}
	return collect(byID, order), false, nil
}

func collect(byID map[string]*detect.Workload, order []string) []detect.Workload {
	out := make([]detect.Workload, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

// nodes reads nodes page by page, at most MaxNodes.
func (d *Detector) nodes(ctx context.Context) ([]detect.Node, bool, error) {
	var out []detect.Node
	next := ""
	for {
		l := &corev1.NodeList{}
		if err := d.Reader.List(ctx, l, client.Limit(pageSize), client.Continue(next)); err != nil {
			return nil, false, err
		}
		for _, n := range l.Items {
			if len(out) == detect.MaxNodes {
				return out, true, nil
			}
			out = append(out, detect.Node{ProviderID: n.Spec.ProviderID, Labels: n.Labels, KernelVersion: n.Status.NodeInfo.KernelVersion})
		}
		if next = l.Continue; next == "" {
			return out, false, nil
		}
	}
}

// routes reads every HTTPRoute's Gateway parents, unstructured: the operator
// carries no Gateway API dependency.
// ponytail: routes, Ingresses and Dex Services are one List per scope, unpaged; page them if a cluster outgrows that.
func (d *Detector) routes(ctx context.Context) ([]detect.Route, error) {
	var out []detect.Route
	for _, ns := range d.scopes() {
		l := &unstructured.UnstructuredList{}
		l.SetGroupVersionKind(httpRouteGVK)
		if err := d.Reader.List(ctx, l, client.InNamespace(ns)); err != nil {
			if meta.IsNoMatchError(err) {
				return nil, nil
			}
			return nil, err
		}
		for _, r := range l.Items {
			refs, _, _ := unstructured.NestedSlice(r.Object, "spec", "parentRefs")
			for _, ref := range refs {
				m, _ := ref.(map[string]any)
				name, _ := m["name"].(string)
				kind, _ := m["kind"].(string)
				if name == "" || (kind != "" && kind != "Gateway") {
					continue
				}
				pns, _ := m["namespace"].(string)
				out = append(out, detect.Route{Namespace: r.GetNamespace(), ParentNamespace: cmp.Or(pns, r.GetNamespace()), ParentName: name})
			}
		}
	}
	return out, nil
}

// ingresses reads every Ingress's class (the field, else the legacy
// annotation).
func (d *Detector) ingresses(ctx context.Context) ([]detect.Ingress, error) {
	var out []detect.Ingress
	for _, ns := range d.scopes() {
		l := &networkingv1.IngressList{}
		if err := d.Reader.List(ctx, l, client.InNamespace(ns)); err != nil {
			return nil, err
		}
		for _, i := range l.Items {
			class := i.Annotations["kubernetes.io/ingress.class"]
			if i.Spec.IngressClassName != nil {
				class = *i.Spec.IngressClassName
			}
			out = append(out, detect.Ingress{Namespace: i.Namespace, Class: class})
		}
	}
	return out, nil
}

// dexServices names the Services labelled app.kubernetes.io/name=dex as
// namespace/name.
func (d *Detector) dexServices(ctx context.Context) ([]string, error) {
	var out []string
	for _, ns := range d.scopes() {
		l := &corev1.ServiceList{}
		if err := d.Reader.List(ctx, l, client.InNamespace(ns), client.MatchingLabels{"app.kubernetes.io/name": "dex"}); err != nil {
			return nil, err
		}
		for _, s := range l.Items {
			out = append(out, s.Namespace+"/"+s.Name)
		}
	}
	return out, nil
}
