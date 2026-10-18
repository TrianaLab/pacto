/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"net/netip"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v4/pkg/evidence"
)

// httpRouteGVK is read unstructured so the operator carries no Gateway API dependency.
var httpRouteGVK = schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRoute"}

// interfaceExposure maps the Kubernetes resources that route to a Service port (spec section 4) onto the
// platform-neutral evidence.Exposure values: spec.externalIPs, NodePort and LoadBalancer Services, and the
// Ingresses and HTTPRoutes that reference the port. External needs
// positive evidence of an address outside private networks: a public spec.externalIP, a LoadBalancer that
// carries no internal-LB annotation and publishes a public ingress address, or an Ingress that routes to
// the port and publishes one. A NodePort (the node network may be private), an internal or address-less
// LoadBalancer, an address-less Ingress and any HTTPRoute (the gateway's address is not read) leave the
// exposure unknown (empty). Internal needs a ClusterIP Service that nothing routes to, every source having
// answered, so a read error leaves it unknown too.
func (o *Observer) interfaceExposure(ctx context.Context, routes *httpRoutes, svc *corev1.Service, p corev1.ServicePort) evidence.Exposure {
	if slices.ContainsFunc(svc.Spec.ExternalIPs, publicIP) {
		return evidence.ExposureExternal
	}
	viaIngress, publicIngress, ingressErr := o.ingressRoutesTo(ctx, svc, p)
	if publicIngress {
		return evidence.ExposureExternal
	}
	switch svc.Spec.Type {
	case corev1.ServiceTypeLoadBalancer:
		if !internalLoadBalancer(svc.Annotations) && slices.ContainsFunc(svc.Status.LoadBalancer.Ingress, func(i corev1.LoadBalancerIngress) bool {
			return publicAddress(i.IP, i.Hostname)
		}) {
			return evidence.ExposureExternal
		}
		return ""
	case corev1.ServiceTypeNodePort:
		return ""
	}
	viaRoute, routeErr := o.httpRouteRoutesTo(ctx, routes, svc, p)
	if viaIngress || viaRoute || ingressErr != nil || routeErr != nil {
		return ""
	}
	return evidence.ExposureInternal
}

// internalLBAnnotations are the cloud annotations that make a LoadBalancer Service internal, each with the
// value that does; an empty value means any value but "false". The list is the common providers only: an
// internal LoadBalancer of another provider still needs a public ingress address to read external, which
// it does not have.
var internalLBAnnotations = map[string]string{
	"networking.gke.io/load-balancer-type":                               "internal",
	"cloud.google.com/load-balancer-type":                                "internal",
	"service.beta.kubernetes.io/aws-load-balancer-internal":              "", // "true" or a CIDR such as 0.0.0.0/0
	"service.beta.kubernetes.io/aws-load-balancer-scheme":                "internal",
	"service.beta.kubernetes.io/azure-load-balancer-internal":            "true",
	"service.kubernetes.io/ibm-load-balancer-cloud-provider-ip-type":     "private",
	"service.beta.kubernetes.io/oci-load-balancer-internal":              "true",
	"oci-network-load-balancer.oraclecloud.com/internal":                 "true",
	"service.beta.kubernetes.io/openstack-internal-load-balancer":        "true",
	"service.beta.kubernetes.io/alibaba-cloud-loadbalancer-address-type": "intranet",
}

// internalLoadBalancer reports whether the annotations ask the cloud for an internal load balancer.
func internalLoadBalancer(annotations map[string]string) bool {
	for key, want := range internalLBAnnotations {
		v := annotations[key]
		if want == "" {
			if v != "" && !strings.EqualFold(v, "false") {
				return true
			}
		} else if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

// publicAddress reports whether a load balancer ingress address is outside private networks: a public IP,
// or a hostname that is not an AWS internal ELB name (internal-...).
func publicAddress(ip, hostname string) bool {
	if ip != "" {
		return publicIP(ip)
	}
	return hostname != "" && !strings.HasPrefix(hostname, "internal-")
}

// publicIP reports whether the address is a unicast IP outside the private (RFC 1918, ULA), loopback,
// link-local and carrier-grade NAT (100.64.0.0/10) ranges.
func publicIP(s string) bool {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	return addr.IsGlobalUnicast() && !addr.IsPrivate() && !cgnat.Contains(addr)
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// ingressRoutesTo reports whether an Ingress in the Service's namespace (Ingress backends cannot cross
// namespaces) routes to the port, and whether one that does publishes a public address.
func (o *Observer) ingressRoutesTo(ctx context.Context, svc *corev1.Service, p corev1.ServicePort) (routes, public bool, err error) {
	var list networkingv1.IngressList
	if err := o.client.List(ctx, &list, client.InNamespace(svc.Namespace)); err != nil {
		return false, false, err
	}
	for _, ing := range list.Items {
		if !ingressRoutesToPort(&ing, svc.Name, p) {
			continue
		}
		routes = true
		if slices.ContainsFunc(ing.Status.LoadBalancer.Ingress, func(i networkingv1.IngressLoadBalancerIngress) bool {
			return publicAddress(i.IP, i.Hostname)
		}) {
			return true, true, nil
		}
	}
	return routes, false, nil
}

// ingressRoutesToPort reports whether the Ingress has a default backend or a rule path to the port.
func ingressRoutesToPort(ing *networkingv1.Ingress, svcName string, p corev1.ServicePort) bool {
	if b := ing.Spec.DefaultBackend; b != nil && ingressBackendMatches(b.Service, svcName, p) {
		return true
	}
	for _, rule := range ing.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			if ingressBackendMatches(path.Backend.Service, svcName, p) {
				return true
			}
		}
	}
	return false
}

// ingressBackendMatches reports whether an Ingress service backend names the Service port, by number or by
// name.
func ingressBackendMatches(b *networkingv1.IngressServiceBackend, svcName string, p corev1.ServicePort) bool {
	if b == nil || b.Name != svcName {
		return false
	}
	return b.Port.Number == p.Port || (b.Port.Name != "" && b.Port.Name == p.Name)
}

// httpRouteRoutesTo reports whether a Gateway API HTTPRoute in any watched namespace has a backendRef to the
// Service port. An API server without the Gateway API (no kind match) has no routes; any other error, a
// Forbidden included, is returned so the exposure stays unknown.
func (o *Observer) httpRouteRoutesTo(ctx context.Context, routes *httpRoutes, svc *corev1.Service, p corev1.ServicePort) (bool, error) {
	items, err := routes.list(ctx, o.client)
	if err != nil {
		return false, err
	}
	for _, route := range items {
		rules, _, _ := unstructured.NestedSlice(route.Object, "spec", "rules")
		for _, rule := range rules {
			ruleMap, _ := rule.(map[string]any)
			refs, _, _ := unstructured.NestedSlice(ruleMap, "backendRefs")
			for _, ref := range refs {
				refMap, _ := ref.(map[string]any)
				if backendRefMatches(refMap, route.GetNamespace(), svc, p) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// httpRoutes memoises the HTTPRoute list for one interfaces pass: an unstructured read bypasses the
// manager's cache, so without it every interface would repeat the cluster-wide LIST.
type httpRoutes struct {
	done  bool
	items []unstructured.Unstructured
	err   error
}

// list reads every HTTPRoute once. An API server without the Gateway API (no kind match) has none.
func (r *httpRoutes) list(ctx context.Context, c client.Client) ([]unstructured.Unstructured, error) {
	if !r.done {
		r.done = true
		l := &unstructured.UnstructuredList{}
		l.SetGroupVersionKind(httpRouteGVK)
		if err := c.List(ctx, l); err != nil && !meta.IsNoMatchError(err) {
			r.err = err
		} else {
			r.items = l.Items
		}
	}
	return r.items, r.err
}

// backendRefMatches reports whether an HTTPRoute backendRef names the Service port. Group and kind default to
// the core Service, the namespace to the route's own.
func backendRefMatches(ref map[string]any, routeNamespace string, svc *corev1.Service, p corev1.ServicePort) bool {
	group, _, _ := unstructured.NestedString(ref, "group")
	kind, _, _ := unstructured.NestedString(ref, "kind")
	name, _, _ := unstructured.NestedString(ref, "name")
	namespace, _, _ := unstructured.NestedString(ref, "namespace")
	port, hasPort, _ := unstructured.NestedFieldNoCopy(ref, "port")
	if namespace == "" {
		namespace = routeNamespace
	}
	if group != "" || (kind != "" && kind != "Service") || name != svc.Name || namespace != svc.Namespace {
		return false
	}
	return !hasPort || toInt64(port) == int64(p.Port)
}

// toInt64 reads a JSON number that decoding may have produced as int64 or float64.
func toInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		return -1
	}
}
