package detect

import (
	"cmp"
	"slices"
)

// Service is the part of a Service a Prometheus or trace-backend read uses:
// its labels (the operator keeps only the keys [PrometheusLabels] lists), its
// type, whether it selects pods and its TCP ports.
type Service struct {
	Namespace, Name string
	Labels          map[string]string
	Type            string
	HasSelector     bool
	Ports           []ServicePort
}

// ServicePort is one port of a Service.
type ServicePort struct {
	Name string `json:"name"`
	Port int32  `json:"port"`
}

// promClass is one label class of Prometheus Services, in the order asked.
type promClass struct {
	labels  map[string]string
	vizOnly bool
}

// promClasses are the Prometheus label classes in the order they are asked.
var promClasses = []promClass{
	{labels: map[string]string{"linkerd.io/extension": "viz", "component": "prometheus"}, vizOnly: true},
	{labels: map[string]string{"operated-prometheus": "true"}},
	{labels: map[string]string{"app.kubernetes.io/name": "prometheus"}},
	{labels: map[string]string{"app": "prometheus"}},
}

// PrometheusLabels are the Service label keys [PrometheusCandidates] reads;
// a reader keeps no other label.
var PrometheusLabels = []string{"linkerd.io/extension", "component", "operated-prometheus", "app.kubernetes.io/name", "app"}

// PrometheusCandidates are the Prometheus Services to ask, in order. An
// ExternalName or selectorless Service is never one. A candidate is trusted
// in a namespace of trusted (a control plane) or when every Service of its
// class sits in one other namespace; a class with Services in several other
// namespaces gives none of them, and several says so. When any
// trusted-namespace candidate exists only those are returned: a tenant's lone
// Service never outranks or backs up the control plane's Prometheus.
func PrometheusCandidates(svcs []Service, trusted []string, preferViz bool) (cands []Service, several bool) {
	refused := map[[2]string]bool{}
	var in, out []Service
	for _, class := range promClasses {
		if class.vizOnly && !preferViz {
			continue
		}
		classIn, classOut, others := classServices(svcs, class, trusted)
		if others > 1 {
			// A refused Service stays refused under a later class.
			for _, svc := range classOut {
				refused[[2]string{svc.Namespace, svc.Name}] = true
			}
			classOut, several = nil, true
		}
		in = append(in, sortedServices(classIn)...)
		for _, svc := range sortedServices(classOut) {
			if !refused[[2]string{svc.Namespace, svc.Name}] {
				out = append(out, svc)
			}
		}
	}
	pick := in
	if len(pick) == 0 {
		pick = out
	}
	seen := map[[2]string]bool{}
	for _, svc := range pick {
		if k := [2]string{svc.Namespace, svc.Name}; !seen[k] {
			seen[k] = true
			cands = append(cands, svc)
		}
	}
	return cands, several
}

// classServices are the Services of class in a trusted namespace and the
// others, and how many other namespaces hold one. Every Service of the class
// counts, one an earlier class took included, so a copy of a later label
// never looks alone.
func classServices(svcs []Service, class promClass, trusted []string) (in, out []Service, others int) {
	namespaces := map[string]bool{}
	for _, svc := range svcs {
		switch {
		case !hasLabels(svc, class.labels) || svc.Type == "ExternalName" || !svc.HasSelector:
		case slices.Contains(trusted, svc.Namespace):
			in = append(in, svc)
		default:
			out = append(out, svc)
			namespaces[svc.Namespace] = true
		}
	}
	return in, out, len(namespaces)
}

// hasLabels reports whether svc carries every label of want.
func hasLabels(svc Service, want map[string]string) bool {
	for k, v := range want {
		if svc.Labels[k] != v {
			return false
		}
	}
	return true
}

// sortedServices is svcs by namespace, then name.
func sortedServices(svcs []Service) []Service {
	slices.SortFunc(svcs, func(a, b Service) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	return svcs
}

// PrometheusPort is the Prometheus port of svc: the one named web, http-web
// or http, else 9090 when listed, else the first, else 9090.
func PrometheusPort(svc Service) int32 {
	for _, name := range []string{"web", "http-web", "http"} {
		if i := slices.IndexFunc(svc.Ports, func(p ServicePort) bool { return p.Name == name }); i >= 0 {
			return svc.Ports[i].Port
		}
	}
	if len(svc.Ports) == 0 || slices.ContainsFunc(svc.Ports, func(p ServicePort) bool { return p.Port == 9090 }) {
		return 9090
	}
	return svc.Ports[0].Port
}
