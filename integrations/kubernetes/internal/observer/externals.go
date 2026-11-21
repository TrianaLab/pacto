package observer

import (
	"cmp"
	"context"
	"maps"
	"net/url"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	unversioned "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/externalid"
	"github.com/trianalab/pacto/v4/pkg/schemax"
)

// MaxStatusExternals caps status.externals.
const MaxStatusExternals = 50

// ExternalCatalog is the digest status.externalCatalog records: the operator
// classifies with the catalog compiled into it.
// ponytail: no operator flag loads an organization catalog yet; add one with
// 517-S1's loader when an organization needs its own entries in the operator.
func ExternalCatalog() string { return externalcatalog.DefaultDigest() + " (embedded)" }

// serviceEntryList is the Istio ServiceEntry list read as unstructured, so the
// operator needs no Istio types.
var serviceEntryList = schema.GroupVersionKind{Group: "networking.istio.io", Version: "v1", Kind: "ServiceEntryList"}

// ciliumPolicyList and ciliumClusterwidePolicyList are Cilium's network
// policies read as unstructured, so the operator needs no Cilium types.
var (
	ciliumPolicyList            = schema.GroupVersionKind{Group: "cilium.io", Version: "v2", Kind: "CiliumNetworkPolicyList"}
	ciliumClusterwidePolicyList = schema.GroupVersionKind{Group: "cilium.io", Version: "v2", Kind: "CiliumClusterwideNetworkPolicyList"}
)

// externalScan collects the externals one pod spec names.
type externalScan struct {
	o     *Observer
	input CollectInput
	cat   *externalcatalog.Catalog
	owner string
	out   []unversioned.ExternalStatus
	cov   unversioned.CallerCoverage
	cms   map[string]*corev1.ConfigMap
	// labels are the target pod template's labels and its namespace, as a
	// Cilium endpointSelector sees them.
	labels labels.Set
	// hosts are the host matches a value gave, checked against ServiceEntries.
	hosts []externalcatalog.HostPort
}

// observeExternals lists the systems outside the cluster that the target
// workload's pod spec names, with a coverage that says when the list may be
// incomplete. Values come only from plain env values and the ConfigMap keys
// the pod spec references; a Secret contributes its name and key names and is
// never read. Services, EndpointSlices and ServiceEntries are read only in the
// Pacto's own namespace, plus ServiceEntries exported to every namespace.
// Cilium network policies of the Pacto's namespace and cluster-wide ones add
// the toFQDNs rules that select the target's pods.
func (o *Observer) observeExternals(ctx context.Context, input CollectInput) ([]unversioned.ExternalStatus, unversioned.CallerCoverage) {
	spec := o.getPodSpec(ctx, input)
	if spec == nil {
		return nil, unversioned.CallerCoverage{State: unversioned.CallerCoverageUnavailable, Reason: "pod-spec-unavailable"}
	}
	x := &externalScan{
		o: o, input: input, cat: externalcatalog.Default(),
		owner:  strings.ToLower(input.WorkloadKind) + "/" + input.WorkloadName,
		cov:    unversioned.CallerCoverage{State: unversioned.CallerCoverageComplete},
		cms:    map[string]*corev1.ConfigMap{},
		labels: labels.Merge(o.workload(ctx, input).snap.podLabels, labels.Set{ciliumNamespaceLabel: input.Namespace}),
	}
	for _, c := range spec.Containers {
		for _, e := range c.Env {
			x.env(ctx, e)
		}
		for _, f := range c.EnvFrom {
			x.envFrom(ctx, f)
		}
	}
	x.declared(ctx)
	x.serviceEntries(ctx)
	x.ciliumPolicies(ctx)
	return x.result()
}

// env classifies one env var: its name, and its value when it is a plain
// value or a referenced ConfigMap key. A secretKeyRef gives the Secret's name
// and key, never the value.
func (x *externalScan) env(ctx context.Context, e corev1.EnvVar) {
	where := x.owner + " env " + e.Name
	switch {
	case e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil:
		ref := e.ValueFrom.SecretKeyRef
		where += " from secret/" + ref.Name + " key " + ref.Key
		x.add(unversioned.ExternalKindSecretKeyRef, where, x.cat.Classify("", e.Name))
		x.add(unversioned.ExternalKindSecretKeyRef, where, x.cat.Classify("", ref.Key))
	case e.ValueFrom != nil && e.ValueFrom.ConfigMapKeyRef != nil:
		ref := e.ValueFrom.ConfigMapKeyRef
		if cm := x.configMap(ctx, ref.Name); cm != nil {
			x.value(ctx, unversioned.ExternalKindConfigMap, "configmap/"+ref.Name+" key "+ref.Key, cm.Data[ref.Key], e.Name)
		}
	default:
		x.value(ctx, unversioned.ExternalKindEnv, where, e.Value, e.Name)
	}
}

// envFrom classifies a whole ConfigMap's keys and values, or a Secret's name.
func (x *externalScan) envFrom(ctx context.Context, f corev1.EnvFromSource) {
	if f.SecretRef != nil {
		where := x.owner + " envFrom secret/" + f.SecretRef.Name
		x.add(unversioned.ExternalKindSecretRef, where, x.cat.Classify("", f.SecretRef.Name))
		x.add(unversioned.ExternalKindSecretRef, where, x.cat.Classify("", f.Prefix))
		return
	}
	if f.ConfigMapRef == nil {
		return
	}
	cm := x.configMap(ctx, f.ConfigMapRef.Name)
	if cm == nil {
		return
	}
	for _, k := range slices.Sorted(maps.Keys(cm.Data)) {
		x.value(ctx, unversioned.ExternalKindConfigMap, "configmap/"+cm.Name+" key "+k, cm.Data[k], f.Prefix+k)
	}
}

// value classifies a value and its key, and follows a host that names a
// Service of the Pacto's own namespace.
func (x *externalScan) value(ctx context.Context, kind, where, value, key string) {
	// <pod>.<service>.<namespace> of the Pacto's own namespace is in-cluster.
	ms := slices.DeleteFunc(x.cat.Classify(value, key), func(m externalcatalog.Match) bool {
		return externalcatalog.NamespaceShortForm(m, func(ns string) bool { return ns == x.input.Namespace })
	})
	x.add(kind, where, ms)
	for _, m := range ms {
		if m.Kind == externalcatalog.KindHost {
			x.hosts = append(x.hosts, externalcatalog.NormalizeHost(m.Host)...)
		}
	}
	if name, ok := localService(value, key, x.input.Namespace); ok {
		x.service(ctx, name, where)
	}
}

// localService is the Service a value names in namespace: a bare name, or
// name.<namespace> with an optional .svc suffix, written with a scheme or a
// port. A name in another namespace is never looked up.
func localService(value, key, namespace string) (string, bool) {
	if schemax.SensitiveKey(key) {
		return "", false
	}
	raw := strings.TrimPrefix(strings.TrimSpace(value), "jdbc:")
	scheme := strings.Contains(raw, "://")
	if !scheme {
		raw = "tcp://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (!scheme && u.Port() == "") {
		return "", false
	}
	parts := strings.Split(strings.ToLower(u.Hostname()), ".")
	if len(parts) > 1 && (parts[1] != namespace || (len(parts) > 2 && parts[2] != "svc")) {
		return "", false
	}
	return parts[0], len(validation.IsDNS1035Label(parts[0])) == 0
}

// declared follows the declared dependencies that resolve to a Service of the
// Pacto's own namespace.
func (x *externalScan) declared(ctx context.Context) {
	if x.input.Contract == nil {
		return
	}
	for _, dep := range x.input.Contract.Dependencies {
		t, _, err := x.o.resolveDependencyToSibling(ctx, x.input.Namespace, dep)
		if err != nil {
			partialCoverage(&x.cov, "service-unreadable")
			continue
		}
		if t != nil && t.namespace == x.input.Namespace && t.serviceName != "" {
			x.service(ctx, t.serviceName, "dependency "+dep.Name)
		}
	}
}

// service reports the external an ExternalName or selectorless Service of the
// Pacto's own namespace points at. A Service with a selector is in-cluster.
func (x *externalScan) service(ctx context.Context, name, from string) {
	svc := &corev1.Service{}
	if err := x.o.client.Get(ctx, types.NamespacedName{Namespace: x.input.Namespace, Name: name}, svc); err != nil {
		if client.IgnoreNotFound(err) != nil {
			partialCoverage(&x.cov, "service-unreadable")
		}
		return
	}
	where := "service/" + name + " from " + from
	if svc.Spec.Type == corev1.ServiceTypeExternalName {
		x.objectHost(unversioned.ExternalKindExternalNameService, where, svc.Spec.ExternalName)
		return
	}
	if len(svc.Spec.Selector) > 0 {
		return
	}
	eps := &discoveryv1.EndpointSliceList{}
	if err := x.o.client.List(ctx, eps, client.InNamespace(x.input.Namespace), client.MatchingLabels{discoveryv1.LabelServiceName: name}); err != nil {
		partialCoverage(&x.cov, "service-unreadable")
		return
	}
	for _, s := range eps.Items {
		if s.AddressType != discoveryv1.AddressTypeFQDN {
			continue
		}
		for _, ep := range s.Endpoints {
			for _, a := range ep.Addresses {
				x.objectHost(unversioned.ExternalKindSelectorlessService, where, a)
			}
		}
	}
}

// objectHost reports a host a namespace object names. An object says the host
// is outside the cluster, so a host no catalog entry classifies is legacy. A
// host that gives no identity (a private suffix such as .corp.internal, or a
// two-label name no rule names) makes the list partial, never silently short.
func (x *externalScan) objectHost(kind, where, host string) {
	for _, h := range externalcatalog.NormalizeHost(host) {
		m, ok := x.cat.MatchHost(h)
		if !ok {
			partialCoverage(&x.cov, "object-host-unclassified")
			continue
		}
		if m.Class == externalcatalog.ClassUnclassified {
			m.Class = externalcatalog.ClassLegacy
		}
		x.add(kind, where, []externalcatalog.Match{m})
	}
}

// serviceEntries reports the hosts the pod spec names that an Istio
// ServiceEntry of the Pacto's namespace, or one exported to every namespace,
// lists. Without the ServiceEntry API the reader is off and the list partial,
// unless the pod spec names no host a ServiceEntry could list.
func (x *externalScan) serviceEntries(ctx context.Context) {
	if len(x.hosts) == 0 {
		return
	}
	if !x.input.ServiceEntries {
		partialCoverage(&x.cov, "serviceentry-reader-off")
		return
	}
	local, err := x.listServiceEntries(ctx, client.InNamespace(x.input.Namespace))
	if err != nil {
		partialCoverage(&x.cov, "serviceentry-unreadable")
		return
	}
	// The cluster-wide list is read only for entries exported to every namespace.
	all, err := x.listServiceEntries(ctx)
	if err != nil {
		partialCoverage(&x.cov, "serviceentry-unreadable")
	}
	exported := slices.DeleteFunc(all, func(se unstructured.Unstructured) bool {
		to, _, _ := unstructured.NestedStringSlice(se.Object, "spec", "exportTo")
		return se.GetNamespace() == x.input.Namespace || !slices.Contains(to, "*")
	})
	for _, se := range append(local, exported...) {
		x.serviceEntry(se)
	}
}

func (x *externalScan) listServiceEntries(ctx context.Context, opts ...client.ListOption) ([]unstructured.Unstructured, error) {
	l := &unstructured.UnstructuredList{}
	l.SetGroupVersionKind(serviceEntryList)
	err := x.o.client.List(ctx, l, opts...)
	return l.Items, err
}

// serviceEntry reports each host the pod spec names that one ServiceEntry
// lists, exactly or under a "*." wildcard. An entry exported from another
// namespace is not named: where names only objects of the Pacto's namespace.
func (x *externalScan) serviceEntry(se unstructured.Unstructured) {
	hosts, _, _ := unstructured.NestedStringSlice(se.Object, "spec", "hosts")
	obj := "serviceentry (exported)"
	if se.GetNamespace() == x.input.Namespace {
		obj = "serviceentry/" + se.GetNamespace() + "/" + se.GetName()
	}
	for _, rule := range hosts {
		rule = strings.ToLower(rule)
		for _, h := range x.hosts {
			suffix, wild := strings.CutPrefix(rule, "*")
			if h.Host == rule || (wild && strings.HasSuffix(h.Host, suffix)) {
				x.objectHost(unversioned.ExternalKindServiceEntry, obj+" host "+rule, h.String())
			}
		}
	}
}

// ciliumNamespaceLabel is the label Cilium gives every endpoint for its pod's
// namespace; a cluster-wide policy selects a namespace through it.
const ciliumNamespaceLabel = "io.kubernetes.pod.namespace"

// ciliumPolicies reports the hosts the Cilium toFQDNs egress rules that select
// the target's pods allow, from the policies of the Pacto's namespace and the
// cluster-wide ones. A rule allows a call and never witnesses one, so each use
// is inferred. Without the Cilium API no such rule can exist: nothing is read
// and coverage is not lowered.
func (x *externalScan) ciliumPolicies(ctx context.Context) {
	if !x.input.CiliumPolicies {
		return
	}
	for _, r := range []struct {
		gvk  schema.GroupVersionKind
		obj  string
		opts []client.ListOption
	}{
		{ciliumPolicyList, "ciliumnetworkpolicy/", []client.ListOption{client.InNamespace(x.input.Namespace)}},
		{ciliumClusterwidePolicyList, "ciliumclusterwidenetworkpolicy/", nil},
	} {
		l := &unstructured.UnstructuredList{}
		l.SetGroupVersionKind(r.gvk)
		if err := x.o.client.List(ctx, l, r.opts...); err != nil {
			partialCoverage(&x.cov, "cilium-unreadable")
			continue
		}
		for _, p := range l.Items {
			x.ciliumPolicy(r.obj+strings.TrimPrefix(p.GetNamespace()+"/"+p.GetName(), "/"), p)
		}
	}
}

// ciliumPolicy reports the toFQDNs rules of one policy's spec and specs that
// select the target's pods.
func (x *externalScan) ciliumPolicy(obj string, p unstructured.Unstructured) {
	var specs []any
	if s, ok := p.Object["spec"]; ok {
		specs = append(specs, s)
	}
	more, _, _ := unstructured.NestedSlice(p.Object, "specs")
	for _, s := range append(specs, more...) {
		spec, _ := s.(map[string]any)
		if !x.selects(spec) {
			continue
		}
		egress, _, _ := unstructured.NestedSlice(spec, "egress")
		for _, e := range egress {
			rule, _ := e.(map[string]any)
			fqdns, _, _ := unstructured.NestedSlice(rule, "toFQDNs")
			for _, f := range fqdns {
				fqdn, _ := f.(map[string]any)
				if name, _, _ := unstructured.NestedString(fqdn, "matchName"); name != "" {
					x.objectHost(unversioned.ExternalKindCiliumFQDN, obj+" toFQDNs "+name, name)
				}
				if pattern, _, _ := unstructured.NestedString(fqdn, "matchPattern"); pattern != "" {
					x.fqdnPattern(obj+" toFQDNs "+pattern, pattern)
				}
			}
		}
	}
}

// selects reports whether a policy spec's endpointSelector selects the
// target's pods. Cilium prefixes label keys with their source (k8s:, any:);
// the prefix is dropped. A spec without one (a host policy) or with a selector
// that does not parse selects nothing.
func (x *externalScan) selects(spec map[string]any) bool {
	raw, ok := spec["endpointSelector"].(map[string]any)
	if !ok {
		return false
	}
	var ls metav1.LabelSelector
	if runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &ls) != nil {
		return false
	}
	matchLabels := map[string]string{}
	for k, v := range ls.MatchLabels {
		matchLabels[ciliumLabelKey(k)] = v
	}
	ls.MatchLabels = matchLabels
	for i := range ls.MatchExpressions {
		ls.MatchExpressions[i].Key = ciliumLabelKey(ls.MatchExpressions[i].Key)
	}
	sel, err := metav1.LabelSelectorAsSelector(&ls)
	return err == nil && sel.Matches(x.labels)
}

// ciliumLabelKey is a Cilium selector key without its source prefix.
func ciliumLabelKey(k string) string {
	for _, p := range []string{"k8s:", "any:"} {
		k = strings.TrimPrefix(k, p)
	}
	return k
}

// fqdnPattern reports the system a toFQDNs matchPattern allows. A leading
// "*." or "**." is dropped; what remains names a host when it has a
// registrable domain, by a catalog rule first. A bare "*", an inner wildcard
// or a remainder without a registrable domain allows hosts nothing bounds, so
// the list is partial.
func (x *externalScan) fqdnPattern(where, pattern string) {
	rest := strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(pattern), "**."), "*.")
	host := externalcatalog.HostPort{Host: strings.TrimSuffix(rest, ".")}
	if strings.Contains(rest, "*") {
		partialCoverage(&x.cov, "fqdn-pattern-unbounded")
		return
	}
	m, ok := x.cat.MatchHost(host)
	if !ok {
		id, bounded := externalid.Host(host.Host)
		if !bounded {
			partialCoverage(&x.cov, "fqdn-pattern-unbounded")
			return
		}
		m = externalcatalog.Match{Identity: id, Confidence: externalcatalog.ConfidenceLikely, Kind: externalcatalog.KindHost, Host: host.String()}
	}
	if m.Class == externalcatalog.ClassUnclassified || m.Class == "" {
		m.Class = externalcatalog.ClassLegacy
	}
	x.add(unversioned.ExternalKindCiliumFQDN, where, []externalcatalog.Match{m})
}

// configMap reads a ConfigMap the pod spec references, once. A missing one
// names nothing; one that cannot be read makes the list partial.
func (x *externalScan) configMap(ctx context.Context, name string) *corev1.ConfigMap {
	if cm, ok := x.cms[name]; ok {
		return cm
	}
	cm := &corev1.ConfigMap{}
	if err := x.o.client.Get(ctx, types.NamespacedName{Namespace: x.input.Namespace, Name: name}, cm); err != nil {
		if client.IgnoreNotFound(err) != nil {
			partialCoverage(&x.cov, "configmap-unreadable")
		}
		cm = nil
	}
	x.cms[name] = cm
	return cm
}

func (x *externalScan) add(kind, where string, ms []externalcatalog.Match) {
	for _, m := range ms {
		x.out = append(x.out, unversioned.ExternalStatus{
			Identity: string(m.Identity), Class: m.Class, Confidence: m.Confidence,
			Kind: kind, Where: where, Host: m.Host, Entry: m.Entry,
		})
	}
}

// result sorts and deduplicates the entries and cuts them at MaxStatusExternals.
func (x *externalScan) result() ([]unversioned.ExternalStatus, unversioned.CallerCoverage) {
	slices.SortFunc(x.out, func(a, b unversioned.ExternalStatus) int {
		return cmp.Or(cmp.Compare(a.Identity, b.Identity), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Where, b.Where), cmp.Compare(a.Host, b.Host))
	})
	out := slices.Compact(x.out)
	if len(out) > MaxStatusExternals {
		out = out[:MaxStatusExternals]
		// Truncation outranks an earlier partial reason: it is the one a reader can act on least.
		x.cov.State, x.cov.Reason = unversioned.CallerCoveragePartial, "truncated"
	}
	return out, x.cov
}

// Externals is what the last Collect found in the target workload's pod spec (observeExternals) and its
// coverage. Both are nil before a Collect.
func (o *Observer) Externals() ([]unversioned.ExternalStatus, *unversioned.CallerCoverage) {
	if o.run == nil {
		return nil, nil
	}
	cov := o.run.externalsCoverage
	return o.run.externals, &cov
}
