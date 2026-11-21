package fleetsrc

import (
	"context"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// Bounds of a mesh read. ponytail: each is fixed; a value only when a
// cluster proves it too small.
const (
	meshWindow        = time.Hour        // the calls of the last hour
	meshTimeout       = 10 * time.Second // one Prometheus read
	meshBudget        = 15 * time.Second // the whole Collect
	meshReadInterval  = 5 * time.Minute  // one real read per process per interval
	maxPromBody       = 16 << 20         // 16 MiB of one Prometheus answer
	maxMeshSeries     = 5000             // the busiest call pairs kept
	maxPromCandidates = 3                // Prometheus Services asked at most
)

// API groups whose discovery says a mesh runs.
const (
	istioGroup         = "networking.istio.io"
	linkerdGroup       = "linkerd.io"
	linkerdPolicyGroup = "policy.linkerd.io"
	ciliumGroup        = "cilium.io"
)

// meshKind is what tells one mesh apart: how it is found, which of its
// Services are its control plane, which namespaces it covers and how its
// Prometheus series fold into edges.
type meshKind struct {
	id, name, metric string
	// controlPlane marks the Services listed as the mesh's own objects;
	// trusts adds namespaces a Prometheus may be trusted in (nil: none).
	controlPlane, trusts func(svcInfo) bool
	covers               func(nsInfo) bool
	preferViz            bool
	query                func(namespaces []string) string
	edges                func(series []promSeries, asOf time.Time, namespaces []string) ([]fleet.ObservedEdge, meshTally)
}

var istioKind = meshKind{
	id: fleet.ProviderIstio, name: "Istio", metric: "istio_requests_total",
	controlPlane: func(svc svcInfo) bool { return svc.Labels["app"] == "istiod" },
	covers:       istioCovers, query: istioQuery, edges: istioEdges,
}

var linkerdKind = meshKind{
	id: fleet.ProviderLinkerd, name: "Linkerd", metric: "response_total",
	controlPlane: func(svc svcInfo) bool { _, ok := svc.Labels["linkerd.io/control-plane-component"]; return ok },
	trusts:       func(svc svcInfo) bool { return svc.Labels["linkerd.io/extension"] == "viz" },
	covers:       linkerdCovers, preferViz: true, query: linkerdQuery, edges: linkerdEdges,
}

// meshesServed is each mesh served with the group its evidence names.
func meshesServed(served map[string]string) (kinds []meshKind, groups []string) {
	if _, ok := served[istioGroup]; ok {
		kinds, groups = append(kinds, istioKind), append(groups, istioGroup)
	}
	for _, g := range []string{linkerdPolicyGroup, linkerdGroup} {
		if _, ok := served[g]; ok {
			kinds, groups = append(kinds, linkerdKind), append(groups, g)
			break
		}
	}
	return kinds, groups
}

var (
	servicesGVR   = schema.GroupVersionResource{Version: "v1", Resource: "services"}
	namespacesGVR = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
)

// systemNamespaces never count toward mesh coverage.
var systemNamespaces = []string{"kube-system", "kube-public", "kube-node-lease"}

// MeshSource is an advisory [fleet.Source] over the calls a service mesh
// already records: it finds the mesh through API discovery, finds the
// Prometheus that records it among the cluster's Services and reads one
// bounded query; for Cilium it reads Hubble Relay's status and a bounded
// window of its flows. It only lists Services and Namespaces and never
// writes. Its
// silence never counts against a declared call (ObservedUndeclaredOnly), and
// every gap makes its row partial, never empty-complete.
type MeshSource struct {
	id         string
	cluster    *fleet.Cluster
	client     k8sclient.K8sClient
	namespaces []string
	inCluster  bool
	now        func() time.Time
	http       *http.Client
	baseURL    func(namespace, name string, port int32) string
	// dial opens the connection to Hubble Relay at addr.
	dial func(ctx context.Context, addr string) (net.Conn, error)
	memo *MeshMemo
}

// MeshMemo keeps a mesh source's last read, so sources rebuilt for each
// fleet build still read the mesh at most once per meshReadInterval.
type MeshMemo struct {
	mu       sync.Mutex
	last     *fleet.Collection
	lastRead time.Time
}

// WithMemo makes s keep its last read in m, shared with every source given
// m, and returns s.
func (s *MeshSource) WithMemo(m *MeshMemo) *MeshSource {
	s.memo = m
	return s
}

// NewMeshSource returns a mesh source over namespaces (empty for all). id is
// its provenance id; cluster is the cluster it reads, nil when unknown.
// Prometheus is read directly only when client runs inside the cluster.
func NewMeshSource(id string, cluster *fleet.Cluster, client k8sclient.K8sClient, namespaces []string) *MeshSource {
	in, ok := client.(interface{ InCluster() bool })
	return &MeshSource{
		id: id, cluster: cluster, client: client, namespaces: normalizeNamespaces(namespaces),
		inCluster: ok && in.InCluster(), now: time.Now, memo: &MeshMemo{},
		// ponytail: in-cluster .svc names never go through an egress proxy.
		http: &http.Client{Timeout: meshTimeout, Transport: &http.Transport{
			Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		}},
		baseURL: func(namespace, name string, port int32) string {
			return "http://" + name + "." + namespace + ".svc:" + strconv.Itoa(int(port))
		},
		dial: func(ctx context.Context, addr string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", addr)
		},
	}
}

// ID implements [fleet.Source].
func (s *MeshSource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *MeshSource) Kind() string { return fleet.SourceKindMesh }

// Cluster implements [fleet.ClusterSource].
func (s *MeshSource) Cluster() *fleet.Cluster { return s.cluster }

// Collect never fails: it serves the last read while it is younger than
// meshReadInterval, else reads again under one meshBudget deadline. A read
// its caller cut short is served once and never kept.
func (s *MeshSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	m := s.memo
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.last == nil || s.now().Sub(m.lastRead) >= meshReadInterval {
		readCtx, cancel := context.WithTimeout(ctx, meshBudget)
		defer cancel()
		at, col := s.now(), s.read(readCtx)
		if ctx.Err() != nil {
			return col, nil
		}
		m.last, m.lastRead = col, at
	}
	return cloneCollection(m.last), nil
}

// cloneCollection copies what Build may sort or append to, so two builds
// never share a kept read.
func cloneCollection(c *fleet.Collection) *fleet.Collection {
	out := *c
	out.Observed = slices.Clone(c.Observed)
	out.Providers = slices.Clone(c.Providers)
	for i := range out.Providers {
		out.Providers[i].Objects = slices.Clone(out.Providers[i].Objects)
		out.Providers[i].Limitations = slices.Clone(out.Providers[i].Limitations)
	}
	if c.State != nil {
		st := *c.State
		out.State = &st
	}
	return &out
}

// read is one real read: discovery, then the Services and Namespaces in
// scope, then each mesh found and Hubble.
func (s *MeshSource) read(ctx context.Context) *fleet.Collection {
	now := s.now()
	col := &fleet.Collection{Advisory: true, Cluster: s.cluster}
	served, err := s.client.PreferredVersions(ctx, istioGroup, linkerdGroup, linkerdPolicyGroup, ciliumGroup)
	if err != nil {
		col.State = &fleet.SourceState{Status: fleet.SourceUnavailable, Error: &fleet.SourceError{
			Code: "DISCOVERY_FAILED", Message: "API discovery failed, so Pacto cannot tell whether a service mesh runs"}}
		return col
	}
	status := fleet.SourceAvailable
	kinds, groups := meshesServed(served)
	_, hubble := served[ciliumGroup]
	if len(kinds) > 0 || hubble {
		cl := s.listCluster(ctx)
		if !cl.servicesErr {
			cl.controlPlanes = controlPlaneNamespaces(cl.services, kinds)
		}
		add := func(row fleet.Provider, edges []fleet.ObservedEdge) {
			col.Providers = append(col.Providers, row)
			col.Observed = append(col.Observed, edges...)
		}
		for i, k := range kinds {
			add(s.meshRead(ctx, cl, now, k, groups[i]))
		}
		if hubble {
			add(s.hubbleRow(ctx, cl, now))
		}
		col.ObservedUndeclaredOnly = true
		if slices.ContainsFunc(col.Providers, func(p fleet.Provider) bool { return p.Status != fleet.SourceAvailable }) {
			status = fleet.SourcePartial
		}
	}
	col.State = &fleet.SourceState{Status: status, ObservedAt: &now, LastSuccessfulSync: &now}
	return col
}

// svcInfo is the part of a Service a mesh read uses.
type svcInfo = detect.Service

// svcPort is one port of a Service.
type svcPort = detect.ServicePort

// nsInfo is the part of a Namespace a coverage check uses.
type nsInfo struct {
	Name        string
	Labels      map[string]string
	Annotations map[string]string
}

// meshCluster is what a mesh read lists: the Services and Namespaces in
// scope, and whether either list failed. When servicesErr is set, services
// may miss any namespace, so no Service is trusted for being the only one.
type meshCluster struct {
	services     []svcInfo
	namespaces   []nsInfo
	servicesErr  bool
	namespaceErr bool
	// controlPlanes are the namespaces of every mesh found's control plane;
	// no mesh counts them toward its coverage.
	controlPlanes []string
}

// k8sObject is the list item shape of a Service or a Namespace.
type k8sObject struct {
	Metadata struct {
		Name        string            `json:"name"`
		Namespace   string            `json:"namespace"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Type     string            `json:"type"`
		Selector map[string]string `json:"selector"`
		Ports    []svcPort         `json:"ports"`
	} `json:"spec"`
}

// listCluster lists the Services of every namespace in scope and the
// Namespaces in scope. A failed Services list keeps what was read and says
// so; a failed Namespaces list makes coverage unknown.
func (s *MeshSource) listCluster(ctx context.Context) meshCluster {
	var cl meshCluster
	for _, ns := range readNamespaces(s.namespaces) {
		items, err := listAll(ctx, s.client, servicesGVR, ns)
		cl.servicesErr = cl.servicesErr || err != nil
		for _, it := range items {
			cl.services = append(cl.services, svcInfo{Namespace: it.Metadata.Namespace, Name: it.Metadata.Name, Labels: it.Metadata.Labels,
				Type: it.Spec.Type, HasSelector: len(it.Spec.Selector) > 0, Ports: it.Spec.Ports})
		}
	}
	items, err := listAll(ctx, s.client, namespacesGVR, "")
	cl.namespaceErr = err != nil
	for _, it := range items {
		if len(s.namespaces) == 0 || slices.Contains(s.namespaces, it.Metadata.Name) {
			cl.namespaces = append(cl.namespaces, nsInfo{Name: it.Metadata.Name, Labels: it.Metadata.Labels, Annotations: it.Metadata.Annotations})
		}
	}
	return cl
}

// listAll pages through gvr in ns.
func listAll(ctx context.Context, c k8sclient.K8sClient, gvr schema.GroupVersionResource, ns string) ([]k8sObject, error) {
	var out []k8sObject
	for token := ""; ; {
		items, next, err := listItems[k8sObject](ctx, c, gvr, ns, token)
		if err != nil {
			return out, err
		}
		out = append(out, items...)
		if next == "" {
			return out, nil
		}
		token = next
	}
}

// controlPlaneNamespaces are the control-plane namespaces of every mesh of
// kinds, each found by soleNamespace.
func controlPlaneNamespaces(services []svcInfo, kinds []meshKind) []string {
	var out []string
	for _, k := range kinds {
		if ns, ok := soleNamespace(services, k.controlPlane); ok && !slices.Contains(out, ns) {
			out = append(out, ns)
		}
	}
	return out
}

// soleNamespace is the namespace of the Services match selects when they all
// sit in one namespace. Anyone who can create a Service can copy a label, so
// a label found in two or more namespaces marks none of them: a tenant's copy
// only takes trust away from the real control plane, never gives it.
// ponytail: an installation whose scope hides the real control plane still
// trusts a lone copy; a signal tenants cannot write (namespace labels) closes
// that when a cluster needs it.
func soleNamespace(services []svcInfo, match func(svcInfo) bool) (string, bool) {
	ns := ""
	for _, svc := range services {
		switch {
		case !match(svc):
		case ns == "":
			ns = svc.Namespace
		case ns != svc.Namespace:
			return "", false
		}
	}
	return ns, ns != ""
}

// meshRead is the row of mesh k, whose evidence names group, and the calls
// its Prometheus recorded.
func (s *MeshSource) meshRead(ctx context.Context, cl meshCluster, now time.Time, k meshKind, group string) (fleet.Provider, []fleet.ObservedEdge) {
	if cl.servicesErr {
		// A Service that looks like the only one of its label may only be the
		// only one read: nothing is trusted, so no Prometheus is asked.
		return meshRow(k.id, k.name, "serves "+group+"; Services not read", now, nil, []fleet.Limitation{
			s.unread("Services could not be read in every namespace, so Pacto did not look for the Prometheus that records this mesh")}), nil
	}
	var objects []fleet.ProviderObject
	var lims []fleet.Limitation
	var trusted []string
	if ns, ok := soleNamespace(cl.services, k.controlPlane); ok {
		trusted = append(trusted, ns)
		for _, svc := range cl.services {
			if k.controlPlane(svc) {
				objects = append(objects, serviceObject(svc))
			}
		}
	} else if slices.ContainsFunc(cl.services, k.controlPlane) {
		lims = append(lims, s.unread("the mesh's control plane could not be told apart from a copy in another namespace"))
	}
	if k.trusts != nil {
		if ns, ok := soleNamespace(cl.services, k.trusts); ok && !slices.Contains(trusted, ns) {
			trusted = append(trusted, ns)
		}
	}
	cov, covLims := s.coverage(cl, append(slices.Clone(cl.controlPlanes), trusted...), k.covers)
	objects, lims = append(objects, cov...), append(lims, covLims...)
	cands, candLims := prometheusCandidates(cl.services, trusted, k.preferViz)
	for i := range candLims {
		candLims[i].Source = s.id
	}
	lims = append(lims, candLims...)
	evidence := "serves " + group + "; no Prometheus found"
	var edges []fleet.ObservedEdge
	// Several untrusted servers already said why none is asked.
	if len(cands) > 0 || len(candLims) == 0 {
		cand, series, qLims := s.firstAnswer(ctx, cands, k.query(s.namespaces), k.metric)
		lims = append(lims, qLims...)
		if len(cands) > 0 {
			objects, evidence = append(objects, serviceObject(cand)), "serves "+group+"; Prometheus found, calls not read"
		}
		if len(qLims) == 0 {
			evidence = "serves " + group + "; calls read from Prometheus"
			var tally meshTally
			edges, tally = k.edges(series, now, s.namespaces)
			lims = append(lims, tally.limitations(s.id)...)
		}
	}
	return meshRow(k.id, k.name, evidence, now, objects, lims), edges
}

// coverage is the uncovered namespaces as objects and their count-only
// limitation, or the limitation that coverage is unknown.
func (s *MeshSource) coverage(cl meshCluster, controlPlane []string, covered func(nsInfo) bool) ([]fleet.ProviderObject, []fleet.Limitation) {
	if cl.namespaceErr {
		return nil, []fleet.Limitation{{Code: fleet.LimitationMeshCoveragePartial, Source: s.id,
			Message: "sidecar coverage is unknown because namespaces could not be read"}}
	}
	uncovered := meshCoverage(cl.namespaces, cl.services, controlPlane, covered)
	if len(uncovered) == 0 {
		return nil, nil
	}
	objects := make([]fleet.ProviderObject, 0, len(uncovered))
	for _, ns := range uncovered {
		objects = append(objects, fleet.ProviderObject{Kind: "Namespace", Namespace: ns, Name: ns})
	}
	return objects, []fleet.Limitation{{Code: fleet.LimitationMeshCoveragePartial, Source: s.id,
		Message: countOf(len(uncovered), "namespace", "namespaces") + " without sidecars"}}
}

// meshCoverage is the namespaces expected to be covered that covered says
// are not: those holding at least one Service, minus the system namespaces
// and excluded (the mesh's control-plane namespaces).
func meshCoverage(namespaces []nsInfo, services []svcInfo, excluded []string, covered func(nsInfo) bool) (uncovered []string) {
	hasService := map[string]bool{}
	for _, svc := range services {
		hasService[svc.Namespace] = true
	}
	for _, ns := range namespaces {
		if hasService[ns.Name] && !slices.Contains(systemNamespaces, ns.Name) && !slices.Contains(excluded, ns.Name) && !covered(ns) {
			uncovered = append(uncovered, ns.Name)
		}
	}
	slices.Sort(uncovered)
	return uncovered
}

// istioCovers reports whether Istio injects or captures the namespace's
// pods: injection enabled, a revision label with no istio-injection label
// (which wins over it, as in Istio's webhook) or ambient mode.
func istioCovers(ns nsInfo) bool {
	_, rev := ns.Labels["istio.io/rev"]
	injection, labelled := ns.Labels["istio-injection"]
	return injection == "enabled" || (rev && !labelled) || ns.Labels["istio.io/dataplane-mode"] == "ambient"
}

// linkerdCovers reports whether Linkerd injects the namespace's pods.
func linkerdCovers(ns nsInfo) bool {
	return ns.Annotations["linkerd.io/inject"] == "enabled"
}

// prometheusCandidates are the Prometheus Services to ask, in order, at most
// maxPromCandidates, by [detect.PrometheusCandidates]; when a class with
// Services in several namespaces left no candidate, lims says why.
func prometheusCandidates(services []svcInfo, trusted []string, preferViz bool) (cands []svcInfo, lims []fleet.Limitation) {
	cands, several := detect.PrometheusCandidates(services, trusted, preferViz)
	if several && len(cands) == 0 {
		lims = []fleet.Limitation{{Code: fleet.LimitationMeshTelemetryUnread,
			Message: "several Prometheus servers could record this mesh; Pacto cannot tell which one does"}}
	}
	return cands[:min(len(cands), maxPromCandidates)], lims
}

// servicePort is the Prometheus port of svc ([detect.PrometheusPort]).
func servicePort(svc svcInfo) int32 { return detect.PrometheusPort(svc) }

// serviceObject is svc as a row object.
func serviceObject(svc svcInfo) fleet.ProviderObject {
	return fleet.ProviderObject{Kind: "Service", Namespace: svc.Namespace, Name: svc.Name}
}

// meshRow is one mesh row read at asOf, partial when lims is not empty.
func meshRow(id, name, evidence string, asOf time.Time, objects []fleet.ProviderObject, lims []fleet.Limitation) fleet.Provider {
	status := fleet.SourceAvailable
	if len(lims) > 0 {
		status = fleet.SourcePartial
	}
	return fleet.Provider{ID: id, Name: name, Evidence: evidence, UsedFor: []string{fleet.UseObservedEdges},
		Status: status, AsOf: &asOf, Objects: objects, Limitations: lims}
}

// scopeMatcher is the PromQL matcher keeping label to namespaces, empty when
// unscoped.
func scopeMatcher(label string, namespaces []string) string {
	if len(namespaces) == 0 {
		return ""
	}
	quoted := make([]string, len(namespaces))
	for i, ns := range namespaces {
		quoted[i] = regexp.QuoteMeta(ns)
	}
	return label + "=~" + strconv.Quote(strings.Join(quoted, "|"))
}

// countOf is n and the noun in its number: "1 namespace", "3 namespaces".
func countOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
