package detect

import (
	"fmt"
	"maps"
	"slices"
	"strings"
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

// SignIn is Dex when its API is served or a Service labelled for it exists
// (dexServices as namespace/name). None found is partial: an identity
// provider outside the cluster is not visible from here.
func SignIn(dexServed bool, dexServices []string, err error) Entry {
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
		return Entry{Kind: "sign-in", Value: "none found", Evidence: "no Dex API or Service", Source: "API discovery and Services",
			Completeness: Partial, Reason: "an identity provider outside the cluster is not visible"}
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
func Exposure(gatewayServed bool, gatewayErr error, routes []Route, ingresses []Ingress) Entry {
	if gatewayErr != nil {
		return discoveryFailed("exposure", gatewayErr)
	}
	if gatewayServed && len(routes) > 0 {
		return gatewayEntry(routes)
	}
	t, namespaces := tallies{}, map[string]bool{}
	for _, i := range ingresses {
		if i.Class != "" {
			t.add(i.Class, i.Namespace)
			namespaces[i.Namespace] = true
		}
	}
	if len(t) == 0 {
		return Entry{Kind: "exposure", Value: "none found", Source: "HTTPRoutes and Ingresses", Completeness: Partial,
			Reason: "no HTTPRoute on a served Gateway API and no Ingress with a class was found"}
	}
	top := t.ranked()[0]
	e := Entry{Kind: "exposure", Value: "Ingress class " + top.name, Evidence: fmt.Sprintf("in %d of %d namespaces", len(top.ns), len(namespaces)),
		Source: "Ingresses", Completeness: Complete}
	if !majority(top, len(namespaces)) {
		return partial(e, noMajority)
	}
	e.Hint = fitting("--set dashboard.ingress.enabled=true --set dashboard.ingress.className=" + top.name +
		" --set dashboard.ingress.hosts[0].host=<your-host> --set dashboard.ingress.hosts[0].paths[0].path=/" +
		" --set dashboard.ingress.hosts[0].paths[0].pathType=Prefix")
	return e
}

// gatewayEntry is the Gateway most route namespaces attach to. Its namespace
// is named only in the hint, the namespaces and the objects, never in the
// evidence, so a restricted view can redact it.
func gatewayEntry(routes []Route) Entry {
	t, namespaces := tallies{}, map[string]bool{}
	for _, r := range routes {
		t.add(r.ParentNamespace+"/"+r.ParentName, r.Namespace)
		namespaces[r.Namespace] = true
	}
	top := t.ranked()[0]
	ns, name, _ := strings.Cut(top.name, "/")
	e := Entry{Kind: "exposure", Value: "Gateway API", Source: "HTTPRoutes", Completeness: Complete,
		Evidence:   fmt.Sprintf("HTTPRoutes in %d of %d namespaces attach to the same Gateway", len(top.ns), len(namespaces)),
		Namespaces: []string{ns}, Objects: []Object{{Kind: "Gateway", Namespace: ns, Name: name}}}
	if !majority(top, len(namespaces)) {
		return partial(e, noMajority)
	}
	e.Hint = fitting("--set dashboard.httpRoute.enabled=true --set dashboard.httpRoute.parentRefs[0].name=" + name +
		" --set dashboard.httpRoute.parentRefs[0].namespace=" + ns)
	return e
}
