package detect

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// Route is one HTTPRoute parent: the route's namespace and the Gateway it
// attaches to.
type Route struct{ Namespace, ParentNamespace, ParentName string }

// Ingress is one Ingress: its namespace and class.
type Ingress struct{ Namespace, Class string }

// discoveryFailed is the reason of an entry whose API discovery failed. A
// failure is never read as "not served".
func discoveryFailed(kind string, err error) Entry {
	return UnknownEntry(kind, "API discovery failed: "+err.Error())
}

// Metrics says whether the Prometheus Operator API is served.
func Metrics(served bool, err error) Entry {
	if err != nil {
		return discoveryFailed("metrics", err)
	}
	e := Entry{Kind: "metrics", Value: "none found", Evidence: "monitoring.coreos.com/v1 is not served", Source: "API discovery", Completeness: Complete}
	if served {
		e.Value, e.Evidence = "Prometheus Operator", "serves monitoring.coreos.com/v1; read passively"
	}
	return e
}

// What the detector read of the Crossplane core Deployment.
const (
	CrossplaneControllerFound    = "found"
	CrossplaneControllerNotReady = "not-ready"
	CrossplaneControllerNone     = "none"
	CrossplaneControllerUnread   = "unread"
)

// CrossplaneFacts is what the detector read of Crossplane: API is the
// apiextensions.crossplane.io major discovery serves (v2, v1 or empty),
// APIErr a discovery failure, Version the image tag of the core Deployment
// and Controller one of the CrossplaneController values.
type CrossplaneFacts struct {
	API     string
	APIErr  error
	Version string
	// Controller is CrossplaneControllerFound (a core Deployment with an
	// available replica), CrossplaneControllerNotReady (one with none),
	// CrossplaneControllerNone or CrossplaneControllerUnread.
	Controller string
}

// Crossplane says whether Crossplane runs. CRDs outlive Crossplane, so only
// a served API with a found controller reads complete and running; served
// APIs without a controller, or with one that could not be read, are
// partial. A core Deployment with no available replica (scaled to zero or
// crash-looping) is neither running nor none found, partial. A found
// controller whose APIs discovery does not serve yet is a
// Crossplane still starting, partial. No API served otherwise is
// fleet.CrossplaneNoneFound, complete, with the bootstrap hint under profile
// idp. The Deployment's namespace never enters the entry.
func Crossplane(c CrossplaneFacts, idp bool) Entry {
	const kind = fleet.ProviderCrossplane
	if c.APIErr != nil {
		return discoveryFailed(kind, c.APIErr)
	}
	e := Entry{Kind: kind, Source: "Crossplane Deployment and API discovery", Completeness: Complete}
	served := "serves apiextensions.crossplane.io/" + c.API
	switch {
	case c.Controller == CrossplaneControllerNotReady:
		e.Value, e.Evidence, e.Completeness = "Crossplane not available", "the core Deployment has no available replica", Partial
		e.Reason, e.Scope = "the Crossplane Deployment has no available replica", fleet.ScopeCluster
	case c.API == "" && c.Controller == CrossplaneControllerFound:
		e.Value, e.Evidence, e.Completeness = "Crossplane starting", "runs and does not serve apiextensions.crossplane.io yet", Partial
		e.Reason, e.Scope = "a Crossplane Deployment runs but discovery does not serve its APIs yet", fleet.ScopeCluster
	case c.API == "":
		e.Value, e.Evidence, e.Source = fleet.CrossplaneNoneFound, "apiextensions.crossplane.io is not served", "API discovery"
		if idp {
			e.Hint = "helm upgrade <release> <chart> --reuse-values --set idp.crossplane.install=true"
		}
	case c.Controller == CrossplaneControllerFound:
		e.Value, e.Evidence, e.Scope = "Crossplane "+c.API, "runs and "+served, fleet.ScopeCluster
		if isVersion(c.Version) {
			e.Value = "Crossplane " + c.Version
		}
	case c.Controller == CrossplaneControllerNone:
		e.Value, e.Evidence, e.Completeness = "Crossplane APIs served, controller not found", served, Partial
		e.Reason = "the Crossplane APIs are served but no Crossplane Deployment runs"
	default:
		e.Value, e.Evidence, e.Completeness = "Crossplane "+c.API+" APIs served", served, Partial
		e.Reason = "the Crossplane Deployment could not be read"
	}
	return e
}

// isVersion reports whether tag reads as a version: an optional v, then a
// digit.
func isVersion(tag string) bool {
	tag = strings.TrimPrefix(tag, "v")
	return tag != "" && tag[0] >= '0' && tag[0] <= '9'
}

// IstioEgress says whether the Istio ServiceEntry API is served: the
// operator reads the ServiceEntries that name the systems a workload calls
// outside the cluster. The entry comes from a cluster-wide read.
func IstioEgress(served bool, err error) Entry {
	return egressAPI("istio-egress", "ServiceEntry", "networking.istio.io/v1", served, err)
}

// CiliumFQDN says whether the Cilium network policy API is served: the
// operator reads the toFQDNs egress rules that select a workload's pods as
// the systems it may call outside the cluster. The entry comes from a
// cluster-wide read.
func CiliumFQDN(served bool, err error) Entry {
	return egressAPI("cilium-fqdn", "CiliumNetworkPolicy", "cilium.io/v2", served, err)
}

// egressAPI is the entry of kind for an egress reader that needs the crd of
// group version gv: complete when served, unknown when not or when discovery
// failed.
func egressAPI(kind, crd, gv string, served bool, err error) Entry {
	e := Entry{Kind: kind, Evidence: crd + " CRD present", Source: "API discovery", Completeness: Complete}
	switch {
	case err != nil:
		e = discoveryFailed(kind, err)
	case !served:
		e.Evidence, e.Completeness, e.Reason = "no "+crd+" CRD", Unknown, "the cluster serves no "+gv+" API, so no "+crd+" is read"
	}
	e.Scope = fleet.ScopeCluster
	return e
}

// ReaderOff is e lowered to partial, with why and the hint that fixes it, when
// e reads complete but the reader it describes is not running. A running
// reader, or an entry that is not complete, is e unchanged.
func ReaderOff(e Entry, running bool, why, hint string) Entry {
	if !running && e.Completeness == Complete {
		e.Completeness, e.Reason, e.Hint = Partial, why, hint
	}
	return e
}

// GatewayNamespace is the one namespace whose egress gateways are trusted:
// Istio's install namespace. A tenant can label its own pod anywhere else.
const GatewayNamespace = "istio-system"

// Egress gateway log modes, the values of controller.egressGatewayLogs.
const (
	GatewayLogsAuto = "auto"
	GatewayLogsOn   = "on"
	GatewayLogsOff  = "off"
)

// EgressGateways says how the Istio egress gateways (pods labelled
// istio=egressgateway in istio-system, each in Objects) are read in logs mode:
// complete when on, partial with the helm command that turns it on when auto,
// unknown when off. No gateway gives no entry (an empty Kind); a failed list
// is unknown. The evidence only counts, so it names no namespace, and the
// entry comes from a cluster-wide read.
func EgressGateways(gateways []Object, mode string, err error) Entry {
	const kind = "egress-gateway"
	var e Entry
	switch {
	case err != nil:
		e = UnknownEntry(kind, "listing the egress gateway pods failed: "+err.Error())
	case len(gateways) == 0:
		return Entry{}
	default:
		noun := "gateway"
		if len(gateways) != 1 {
			noun += "s"
		}
		e = Entry{Kind: kind, Evidence: fmt.Sprintf("%d Istio egress %s found; logs: %s", len(gateways), noun, mode),
			Source: "pods labelled istio=egressgateway", Completeness: Complete, Objects: gateways[:min(len(gateways), MaxNamed)]}
		switch mode {
		case GatewayLogsOn:
		case GatewayLogsOff:
			e.Completeness, e.Reason = Unknown, "turned off by controller.egressGatewayLogs"
		default:
			e.Completeness, e.Reason = Partial, "needs pods/log"
			e.Hint = "helm upgrade <release> <chart> --reuse-values --set controller.egressGatewayLogs=on"
		}
	}
	e.Scope = fleet.ScopeCluster
	return e
}

// SignIn is Dex when its API is served or a Service labelled for it exists
// (dexServices as namespace/name). None found is partial: an identity
// provider outside the cluster is not visible from here. watched names the
// namespaces read; nil means every namespace was.
func SignIn(dexServed bool, dexServices []string, err error, watched []string) Entry {
	if err != nil {
		return discoveryFailed("sign-in", err)
	}
	var objects []Object
	namespaces := map[string]bool{}
	for _, s := range dexServices {
		if ns, n, ok := strings.Cut(s, "/"); ok {
			objects = append(objects, Object{Kind: "Service", Namespace: ns, Name: n})
			namespaces[ns] = true
		}
	}
	if !dexServed && len(objects) == 0 {
		e := Entry{Kind: "sign-in", Value: "none found", Evidence: "no Dex API or Service", Source: "API discovery and Services",
			Completeness: Partial, Reason: "an identity provider outside the cluster is not visible"}
		within(&e, watched, nil, nil)
		return e
	}
	var evidence []string
	if dexServed {
		evidence = append(evidence, "serves dex.coreos.com/v1")
	}
	if len(objects) > 0 {
		evidence = append(evidence, fmt.Sprintf("%d Dex Services found", len(objects)))
	}
	e := Entry{Kind: "sign-in", Value: "Dex", Evidence: strings.Join(evidence, "; "), Source: "API discovery and Services", Completeness: Complete}
	place(&e, slices.Sorted(maps.Keys(namespaces)), objects)
	return e
}

// Exposure is the Gateway that HTTPRoutes in the most distinct namespaces
// attach to when the Gateway API is served, else the Ingress class used in
// the most distinct namespaces (Ingresses without a class are not counted).
// The hint is the chart's dashboard route keys, only on a clear majority.
// watched names the namespaces read; nil means every namespace was, and the
// entry is then cluster-scoped: its counts cover namespaces a reader may not
// read.
func Exposure(gatewayServed bool, gatewayErr error, routes []Route, ingresses []Ingress, watched []string) Entry {
	if gatewayErr != nil {
		return discoveryFailed("exposure", gatewayErr)
	}
	if gatewayServed && len(routes) > 0 {
		return gatewayEntry(routes, watched)
	}
	t, namespaces := tallies{}, map[string]bool{}
	for _, i := range ingresses {
		if i.Class != "" {
			t.add(i.Class, i.Namespace)
			namespaces[i.Namespace] = true
		}
	}
	if len(t) == 0 {
		e := Entry{Kind: "exposure", Value: "none found", Source: "HTTPRoutes and Ingresses", Completeness: Partial,
			Reason: "no HTTPRoute on a served Gateway API and no Ingress with a class was found"}
		within(&e, watched, nil, nil)
		return e
	}
	ranked := t.ranked()
	top := ranked[0]
	e := Entry{Kind: "exposure", Value: "Ingress class " + top.name, Evidence: fmt.Sprintf("in %d of %d namespaces", len(top.ns), len(namespaces)),
		Source: "Ingresses", Completeness: Complete}
	within(&e, watched, slices.Sorted(maps.Keys(namespaces)), nil)
	if !majority(ranked, len(namespaces)) {
		return partial(e, noMajority)
	}
	e.Hint = fitting("--set dashboard.ingress.enabled=true --set dashboard.ingress.className=" + top.name +
		" --set dashboard.ingress.hosts[0].host=<your-host> --set dashboard.ingress.hosts[0].paths[0].path=/" +
		" --set dashboard.ingress.hosts[0].paths[0].pathType=Prefix")
	return e
}

// gatewayEntry is the Gateway most route namespaces attach to. Its namespace
// is named only in the hint, the namespaces and the objects, never in the
// evidence, so a restricted view can redact it. The route namespaces its
// counts cover are named too.
func gatewayEntry(routes []Route, watched []string) Entry {
	t, namespaces := tallies{}, map[string]bool{}
	for _, r := range routes {
		t.add(r.ParentNamespace+"/"+r.ParentName, r.Namespace)
		namespaces[r.Namespace] = true
	}
	ranked := t.ranked()
	top := ranked[0]
	ns, name, _ := strings.Cut(top.name, "/")
	e := Entry{Kind: "exposure", Value: "Gateway API", Source: "HTTPRoutes", Completeness: Complete,
		Evidence: fmt.Sprintf("HTTPRoutes in %d of %d namespaces attach to the same Gateway", len(top.ns), len(namespaces))}
	total := len(namespaces)
	namespaces[ns] = true
	within(&e, watched, slices.Sorted(maps.Keys(namespaces)), []Object{{Kind: "Gateway", Namespace: ns, Name: name}})
	if !majority(ranked, total) {
		return partial(e, noMajority)
	}
	e.Hint = fitting("--set dashboard.httpRoute.enabled=true --set dashboard.httpRoute.parentRefs[0].name=" + name +
		" --set dashboard.httpRoute.parentRefs[0].namespace=" + ns)
	return e
}
