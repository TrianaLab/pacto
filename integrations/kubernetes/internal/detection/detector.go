/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package detection is the operator side of what a cluster provides: it reads
// pods, nodes, HTTPRoutes, Ingresses, Services and API discovery, infers
// with pkg/detect and writes the result into one ConfigMap. Detection is
// inert: nothing in the operator reads that ConfigMap back, and nothing it
// finds sets a value, an argument or a grant. It also lists the managed
// resources of the catalog kinds the chart granted, asking a
// SelfSubjectAccessReview (which creates no object) for each kind first.
package detection

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
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
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// Nodes list is the one grant detection adds, cluster-wide installs only; the
// pods, Services, Ingresses and HTTPRoutes it reads are already granted.
// +kubebuilder:rbac:groups="",resources=nodes,verbs=list

// The API group versions detection asks discovery about.
const (
	gatewayGV    = "gateway.networking.k8s.io/v1"
	monitoringGV = "monitoring.coreos.com/v1"
	dexGV        = "dex.coreos.com/v1"
	istioGV      = "networking.istio.io/v1"
	ciliumGV     = "cilium.io/v2"
	fluxGV       = "kustomize.toolkit.fluxcd.io/v1"
	fluxHelmGV   = "helm.toolkit.fluxcd.io/v2"
	argoGV       = "argoproj.io/v1alpha1"
	// crossplaneGroup is asked at v2, then v1: the API major Crossplane serves.
	crossplaneGroup = "apiextensions.crossplane.io/"
)

// crossplaneLabels select the Crossplane core Deployment, the upstream chart's
// label first.
var crossplaneLabels = []client.MatchingLabels{{"app.kubernetes.io/name": "crossplane"}, {"app": "crossplane"}}

// pageSize is the List page of the paginated reads.
const pageSize = 500

// scopedNodes is the reason cloud and nodes are unknown in a namespace-list
// install, where the operator holds no ClusterRole.
const scopedNodes = "with controller.watchNamespaces the operator may not list nodes"

var httpRouteGVK = schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRouteList"}

// Detector is the leader-only runnable that detects on start, every Interval
// and on every Wake, writing only the ConfigMap Name in Namespace.
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
	// Installation is what the operator was configured with: the operator
	// sets it on every install with its profile, and only its registry fields
	// need --registry. LastPush reads the snapshot publisher's last
	// attempt, nil before the first one.
	Installation *detect.Installation
	LastPush     func() *detect.PushResult
	// Wake, when set, runs a detection at once: the snapshot publisher sends
	// on it after every push, so lastPush is advertised without waiting for
	// the next Interval.
	Wake <-chan struct{}
	// Readers are the egress readers the operator started with.
	Readers Readers
	// Pieces are the profile idp rows (the Evidence Server and the recorder),
	// read on every run; nil outside idp.
	Pieces func() []detect.Entry
	// HTTP is the client of the state and inventory reads and Getenv reads
	// the identity webhooks' environment; nil uses the defaults.
	HTTP   *http.Client
	Getenv func(string) string

	owner       *metav1ac.OwnerReferenceApplyConfiguration
	warnedOwner bool
	// reads are the state and inventory reads of the last Interval.
	reads map[string]remoteRead
	// last is the Result of the last run, nil before the first.
	last atomic.Pointer[detect.Result]
}

// Last is the Result of the last run, nil before the first. The inferred
// publisher reads it in process: the ConfigMap is writable by others in the
// operator namespace, so it never feeds what the operator signs.
func (d *Detector) Last() *detect.Result { return d.last.Load() }

// Readers is what the operator reads beside detection, fixed at its start: the
// controller.egressGatewayLogs mode (auto, on or off), whether kernel evidence
// is on (gateway logs are read with it) and whether the ServiceEntry and
// Cilium policy readers run (each needs its API served at startup). Managed,
// when set, reports the catalog's managed kinds discovery serves (see
// [ManagedFor]): the managed-resources entry is read only with it.
// TerraformStates are the controller.terraformStates locations,
// CloudInventory the cloud controller.cloudInventory names ("off" or empty
// for none) and WorkloadIdentity the --workload-identity <cloud>=<value> the
// chart renders from the ServiceAccount annotation.
type Readers struct {
	GatewayLogs                                    string
	KernelEvidence, ServiceEntries, CiliumPolicies bool
	Managed                                        func(context.Context) (map[string][]metav1.APIResource, error)
	TerraformStates                                []string
	CloudInventory, WorkloadIdentity               string
}

// readerOff is e partial when its API is served but the operator's reader did
// not start: the API came after the operator did.
func readerOff(e detect.Entry, running bool) detect.Entry {
	return detect.ReaderOff(e, running, "served since the operator started, so it is not read yet",
		"restart the operator, after a helm upgrade so the chart grants the read")
}

// Start implements manager.Runnable. Runnables default to leader election,
// so only the leader writes. Sync never fails, so neither does Start.
func (d *Detector) Start(ctx context.Context) error {
	_ = d.Sync(ctx)
	t := time.NewTicker(cmp.Or(d.Interval, component.DefaultInterval))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case <-d.Wake: // a nil Wake never fires
		}
		_ = d.Sync(ctx)
	}
}

// Sync detects once and writes the ConfigMap. It always returns nil: a failed
// read becomes an unknown entry, a failed write is logged, and without its
// owner the ConfigMap is never written (uninstall must collect it).
func (d *Detector) Sync(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("detection")
	if d.owner == nil {
		owner, err := OwnerRef(ctx, d.Reader, d.Namespace, d.DeploymentName)
		if err != nil {
			if !d.warnedOwner {
				d.warnedOwner = true
				log.Info("Detection waits for the operator Deployment to own its ConfigMap", "deployment", d.DeploymentName, "reason", reason(err))
			}
			return nil
		}
		d.owner = owner
	}
	res := d.detect(ctx)
	d.last.Store(&res)
	data, err := detect.Encode(res)
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

// OwnerRef is the owner reference of the operator Deployment namespace/name,
// which owns the ConfigMap detection and the linker write: uninstall collects
// it.
func OwnerRef(ctx context.Context, r client.Reader, namespace, name string) (*metav1ac.OwnerReferenceApplyConfiguration, error) {
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, deploy); err != nil {
		return nil, err
	}
	return metav1ac.OwnerReference().WithAPIVersion("apps/v1").WithKind("Deployment").WithName(deploy.Name).WithUID(deploy.UID), nil
}

// detect reads the cluster and runs every rule.
func (d *Detector) detect(ctx context.Context) detect.Result {
	scoped := len(d.Namespaces) > 0
	var watched []string // nil: every namespace is read
	if scoped {
		watched = d.Namespaces
	}
	res := detect.Result{AsOf: d.Now().UTC(), Cluster: d.Cluster, ClusterName: d.ClusterName, Namespaces: d.Namespaces}

	res.Installation = d.installation()

	ws, refs, sampled, wsErr := d.workloads(ctx)
	res.SecretRefs, res.SecretRefsTotal = refs.out, refs.total
	if sampled {
		// A sampled run did not read every pod: at least one ref is unread.
		res.SecretRefsTotal++
	}
	reg := or(wsErr, "registry", func() detect.Entry { return detect.Registries(ws, sampled, !scoped) })
	res.Entries = append(res.Entries, reg)
	if ps := detect.PullSecret(ws, reg, sampled, !scoped); ps.Kind != "" {
		res.Entries = append(res.Entries, ps)
	}

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

	istioServed, istioErr := d.Served(istioGV)
	res.Entries = append(res.Entries, readerOff(detect.IstioEgress(istioServed, classed(istioErr)), d.Readers.ServiceEntries))
	if istioErr == nil && istioServed {
		// No Istio, no egress gateway: nothing is listed and no entry is written.
		gateways, err := d.egressGateways(ctx)
		if gw := detect.EgressGateways(gateways, d.Readers.GatewayLogs, classed(err)); gw.Kind != "" {
			if d.Readers.GatewayLogs == detect.GatewayLogsOn {
				gw = detect.ReaderOff(gw, d.Readers.KernelEvidence, "gateway logs are read only with kernel evidence on",
					"helm upgrade <release> <chart> --reuse-values --set nodeAgent.enabled=true")
			}
			res.Entries = append(res.Entries, gw)
		}
	}
	ciliumServed, ciliumErr := d.Served(ciliumGV)
	res.Entries = append(res.Entries, readerOff(detect.CiliumFQDN(ciliumServed, classed(ciliumErr)), d.Readers.CiliumPolicies))

	dexServed, dexErr := d.Served(dexGV)
	services, err := d.dexServices(ctx)
	res.Entries = append(res.Entries, sampledEntry(detect.SignIn(dexServed, services, classed(errors.Join(dexErr, err)), watched), scoped, scopedReason))

	gwServed, gwErr := d.Served(gatewayGV)
	var routes []detect.Route
	if gwErr == nil && gwServed {
		routes, gwErr = d.routes(ctx)
	}
	ingresses, err := d.ingresses(ctx)
	res.Entries = append(res.Entries, sampledEntry(detect.Exposure(gwServed, classed(errors.Join(gwErr, err)), routes, ingresses, watched), scoped, scopedReason))

	// A Flux with helm-controller and no kustomize-controller still applies
	// what Pacto promotes through a HelmRelease.
	fluxServed, fluxErr := d.Served(fluxGV)
	helmServed, helmErr := d.Served(fluxHelmGV)
	argoServed, argoErr := d.Served(argoGV)
	idp := d.Installation != nil && detect.IDPProfile(d.Installation.Profile)
	if e, ok := detect.Applier(fluxServed || helmServed, argoServed, classed(errors.Join(fluxErr, helmErr, argoErr)), idp); ok {
		res.Entries = append(res.Entries, e)
	}
	res.Entries = append(res.Entries, d.crossplane(ctx))
	d.managed(ctx, &res)
	d.remote(ctx, &res)
	if d.Pieces != nil {
		res.Entries = append(res.Entries, d.Pieces()...)
	}

	svcs, svcErr := d.services(ctx)
	if err := errors.Join(wsErr, svcErr); err != nil {
		res.Entries = append(res.Entries, detect.TracesUnknown("reading the cluster failed: "+reason(err)))
	} else {
		// The Backstage rule reads the same pods and Services: no new list call.
		entries := detect.Traces(ws, svcs, d.Namespace)
		if b := detect.Backstage(ws, svcs); b.Kind != "" {
			entries = append(entries, b)
		}
		// A rule judged on part of the cluster proves no single backend.
		for _, e := range entries {
			if sampled && e.Kind == "backstage" {
				// The several-namespaces guard saw only the sampled pods, so a
				// look-alike in a namespace that sorts first could win: the
				// dashboard reads a detected catalog without asking, so give it none.
				e.Endpoint = ""
			}
			e = sampledEntry(e, sampled, fmt.Sprintf("sampled the first %d running pods", detect.MaxPods))
			res.Entries = append(res.Entries, sampledEntry(e, scoped, scopedReason))
		}
	}
	return res
}

// installation is a copy of the configured installation with the last push
// attempt, nil when none is configured. The operator configures one on every
// install; only its registry fields need --registry.
func (d *Detector) installation() *detect.Installation {
	if d.Installation == nil {
		return nil
	}
	inst := *d.Installation
	if p := d.LastPush(); p != nil {
		last := *p
		inst.LastPush = &last
	}
	return &inst
}

// crossplane reads the Crossplane API major from discovery and its core
// Deployment: CRDs outlive Crossplane, so only a running controller reads as
// Crossplane, and a controller whose APIs are not served yet is a Crossplane
// still starting. Each run asks afresh, so a Crossplane installed or removed
// later shows on the next run.
func (d *Detector) crossplane(ctx context.Context) detect.Entry {
	var c detect.CrossplaneFacts
	for _, v := range []string{"v2", "v1"} {
		served, err := d.Served(crossplaneGroup + v)
		if err != nil {
			c.APIErr = classed(err)
			break
		}
		if served {
			c.API = v
			break
		}
	}
	if c.APIErr == nil {
		c.Version, c.Controller = d.crossplaneController(ctx)
	}
	// Profile hardened refuses the bootstrap the hint proposes.
	idp := d.Installation != nil && slices.Contains(d.Installation.Profile, "idp") && !slices.Contains(d.Installation.Profile, "hardened")
	return detect.Crossplane(c, idp)
}

// crossplaneController is the image tag of the Crossplane core Deployment and
// whether it was found with an available replica. A namespace-list install may not list Deployments
// cluster-wide, and a failed list is unread, never none. The rbac-manager
// carries the core's app.kubernetes.io/name, so it never counts as the core.
func (d *Detector) crossplaneController(ctx context.Context) (version, controller string) {
	if len(d.Namespaces) > 0 {
		return "", detect.CrossplaneControllerUnread
	}
	for _, sel := range crossplaneLabels {
		l := &appsv1.DeploymentList{}
		if err := d.Reader.List(ctx, l, sel); err != nil {
			return "", detect.CrossplaneControllerUnread
		}
		for _, dep := range l.Items {
			if strings.HasSuffix(dep.Labels["app"], "-rbac-manager") {
				continue
			}
			if dep.Status.AvailableReplicas == 0 {
				return imageTag(dep.Spec.Template.Spec.Containers), detect.CrossplaneControllerNotReady
			}
			return imageTag(dep.Spec.Template.Spec.Containers), detect.CrossplaneControllerFound
		}
	}
	return "", detect.CrossplaneControllerNone
}

// imageTag is the tag of the first container's image, empty without one.
func imageTag(cs []corev1.Container) string {
	if len(cs) == 0 {
		return ""
	}
	ref, _, _ := strings.Cut(cs[0].Image, "@")
	if i := strings.LastIndexAny(ref, ":/"); i >= 0 && ref[i] == ':' {
		return ref[i+1:]
	}
	return ""
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
// skipped namespaces, grouped by controller owner, and the Secret names each
// owner's pods reference (names only, at most detect.MaxSecretRefs, with
// their total; one unread on a failed list).
func (d *Detector) workloads(ctx context.Context) ([]detect.Workload, secretRefs, bool, error) {
	byID, order, seen := map[string]*detect.Workload{}, []string{}, 0
	refs := secretRefs{seen: map[fleet.RawSecretRef]bool{}, cluster: d.Cluster, name: d.ClusterName}
	for _, ns := range d.scopes() {
		next := ""
		for {
			l := &corev1.PodList{}
			if err := d.Reader.List(ctx, l, client.InNamespace(ns), client.MatchingFields{"status.phase": string(corev1.PodRunning)},
				client.Limit(pageSize), client.Continue(next)); err != nil {
				// No pod read in full: the refs are unknown, never none.
				return nil, secretRefs{total: 1}, false, err
			}
			for i := range l.Items {
				if seen == detect.MaxPods {
					return collect(byID, order), refs, true, nil
				}
				seen++
				p := &l.Items[i]
				if slices.Contains(d.Skip, p.Namespace) {
					continue
				}
				id := string(p.UID)
				var owner detect.Object
				if o := metav1.GetControllerOf(p); o != nil {
					id, owner = string(o.UID), ownerObject(p, o)
				}
				refs.add(owner, p)
				w, ok := byID[id]
				if !ok {
					w = &detect.Workload{ID: id, Namespace: p.Namespace, Owner: owner}
					byID[id] = w
					order = append(order, id)
				}
				for _, c := range p.Spec.Containers {
					w.Images = append(w.Images, c.Image)
					w.Env = append(w.Env, envFacts(c.Env)...)
				}
				for _, s := range p.Spec.ImagePullSecrets {
					w.PullSecrets = append(w.PullSecrets, s.Name)
				}
			}
			if next = l.Continue; next == "" {
				break
			}
		}
	}
	return collect(byID, order), refs, false, nil
}

// ownerObject is the controller o of p as a detect object: a ReplicaSet
// named <deployment>-<pod-template-hash> reads as that Deployment.
func ownerObject(p *corev1.Pod, o *metav1.OwnerReference) detect.Object {
	if hash := p.Labels[appsv1.DefaultDeploymentUniqueLabelKey]; o.Kind == "ReplicaSet" && hash != "" {
		if deploy, ok := strings.CutSuffix(o.Name, "-"+hash); ok {
			return detect.Object{Kind: "Deployment", Namespace: p.Namespace, Name: deploy}
		}
	}
	return detect.Object{Kind: o.Kind, Namespace: p.Namespace, Name: o.Name}
}

// envFacts are the variables of env that detect.EnvAllowlist lists: the
// literal value of DD_SITE, and for DD_API_KEY only the name of the Secret
// its value comes from. Nothing is read from EnvFrom or any Secret.
func envFacts(env []corev1.EnvVar) []detect.EnvFact {
	var out []detect.EnvFact
	for _, e := range env {
		switch {
		case !detect.EnvAllowlist[e.Name]:
		case e.Name == "DD_SITE":
			out = append(out, detect.EnvFact{Key: e.Name, Value: e.Value})
		case e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil:
			out = append(out, detect.EnvFact{Key: e.Name, SecretName: e.ValueFrom.SecretKeyRef.Name})
		}
	}
	return out
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
// ponytail: routes, Ingresses, Dex Services and Services are one List per scope, unpaged; page them if a cluster outgrows that.
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

// services reads every Service in scope with its type, whether it selects
// pods, its TCP ports and only the labels detect.PrometheusLabels lists.
func (d *Detector) services(ctx context.Context) ([]detect.Service, error) {
	var out []detect.Service
	for _, ns := range d.scopes() {
		l := &corev1.ServiceList{}
		if err := d.Reader.List(ctx, l, client.InNamespace(ns)); err != nil {
			return nil, err
		}
		for _, s := range l.Items {
			svc := detect.Service{Namespace: s.Namespace, Name: s.Name, Type: string(s.Spec.Type), HasSelector: len(s.Spec.Selector) > 0}
			for _, k := range detect.PrometheusLabels {
				if v, ok := s.Labels[k]; ok {
					if svc.Labels == nil {
						svc.Labels = map[string]string{}
					}
					svc.Labels[k] = v
				}
			}
			for _, p := range s.Spec.Ports {
				if p.Protocol == "" || p.Protocol == corev1.ProtocolTCP {
					svc.Ports = append(svc.Ports, detect.ServicePort{Name: p.Name, Port: p.Port})
				}
			}
			out = append(out, svc)
		}
	}
	return out, nil
}

// egressGateways are the Istio egress gateway pods: labelled istio=egressgateway in istio-system and owned
// by a Deployment there, the pods the controller trusts with gateway logs on. A labelled pod anywhere else is
// never listed. One unpaged List: a cluster runs a handful of gateway replicas.
func (d *Detector) egressGateways(ctx context.Context) ([]detect.Object, error) {
	l := &corev1.PodList{}
	if err := d.Reader.List(ctx, l, client.InNamespace(detect.GatewayNamespace), client.MatchingLabels{"istio": "egressgateway"}); err != nil {
		return nil, err
	}
	var out []detect.Object
	for i := range l.Items {
		p := &l.Items[i]
		if o := metav1.GetControllerOf(p); o != nil && ownerObject(p, o).Kind == "Deployment" {
			out = append(out, detect.Object{Kind: "Pod", Namespace: p.Namespace, Name: p.Name})
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
