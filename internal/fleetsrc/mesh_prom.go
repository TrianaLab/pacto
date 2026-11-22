package fleetsrc

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// promSeries is one series of a Prometheus instant vector.
type promSeries struct {
	Metric map[string]string `json:"metric"`
	Value  [2]any            `json:"value"`
}

// value is the series' sample, 0 when it is not a number.
func (p promSeries) value() float64 {
	s, _ := p.Value[1].(string)
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) {
		return 0
	}
	return v
}

var (
	errPromUnreachable = errors.New("prometheus unreachable")
	errPromTooLarge    = errors.New("prometheus answer too large")
	errPromUnreadable  = errors.New("prometheus answer unreadable")
	errPromForbidden   = errors.New("prometheus proxy forbidden")
)

// serviceProxy is a client that reaches a Service through the API server's
// service proxy with the caller's own credentials.
type serviceProxy interface {
	ServiceProxyGet(ctx context.Context, namespace, name string, port int32, path string, query url.Values) (io.ReadCloser, error)
}

// promStatusError is a Prometheus answer with a status other than 200.
type promStatusError struct{ code int }

func (e promStatusError) Error() string { return "prometheus answered " + strconv.Itoa(e.code) }

// promQuery runs query at now against cand. Outside the cluster it goes
// through the API server's service proxy when the client has one: a laptop
// never resolves a .svc name.
func (s *MeshSource) promQuery(ctx context.Context, cand svcInfo, query string) ([]promSeries, error) {
	q := url.Values{"query": {query}, "time": {strconv.FormatInt(s.now().Unix(), 10)}}
	if !s.inCluster {
		return s.proxyQuery(ctx, cand, q)
	}
	u := s.baseURL(cand.Namespace, cand.Name, servicePort(cand)) + "/api/v1/query?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errPromUnreachable
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, promStatusError{resp.StatusCode}
	}
	return readPromBody(resp.Body)
}

// proxyQuery reads the query q from cand through the client's service proxy.
func (s *MeshSource) proxyQuery(ctx context.Context, cand svcInfo, q url.Values) ([]promSeries, error) {
	p, ok := s.client.(serviceProxy)
	if !ok {
		return nil, errPromUnreachable
	}
	body, err := p.ServiceProxyGet(ctx, cand.Namespace, cand.Name, servicePort(cand), "/api/v1/query", q)
	if err != nil {
		return nil, proxyError(err)
	}
	defer func() { _ = body.Close() }()
	return readPromBody(body)
}

// proxyError is err of a proxied read as the direct path would see it: the
// API server's own RBAC denial is errPromForbidden, and any other status,
// Prometheus's own relayed through the proxy, a promStatusError.
func proxyError(err error) error {
	var api apierrors.APIStatus
	if !errors.As(err, &api) {
		return err
	}
	st := api.Status()
	relayed := st.Details != nil && slices.ContainsFunc(st.Details.Causes, func(c metav1.StatusCause) bool {
		return c.Type == metav1.CauseTypeUnexpectedServerResponse
	})
	switch {
	case st.Reason == metav1.StatusReasonForbidden && !relayed:
		return errPromForbidden
	case st.Code != 0:
		return promStatusError{int(st.Code)}
	}
	return err
}

// readPromBody decodes a Prometheus instant-vector answer of at most
// maxPromBody bytes.
func readPromBody(body io.Reader) ([]promSeries, error) {
	b, err := io.ReadAll(io.LimitReader(body, maxPromBody+1))
	switch {
	case err != nil:
		return nil, err
	case len(b) > maxPromBody:
		return nil, errPromTooLarge
	}
	var answer struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string       `json:"resultType"`
			Result     []promSeries `json:"result"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &answer) != nil || answer.Status != "success" || answer.Data.ResultType != "vector" {
		return nil, errPromUnreadable
	}
	return answer.Data.Result, nil
}

// firstAnswer asks cands in order and keeps the first answer holding at
// least one series. A timeout stops the loop. Every failure is a fixed text
// that names no Service. Only when every candidate asked answered empty does
// the text say the mesh's calls are not recorded; else it is the first
// failure. cand is the Service read, else the first that failed, else the
// first asked.
func (s *MeshSource) firstAnswer(ctx context.Context, cands []svcInfo, query, metric string) (cand svcInfo, series []promSeries, lims []fleet.Limitation) {
	if len(cands) == 0 {
		return svcInfo{}, nil, []fleet.Limitation{s.unread("no Prometheus found in the namespaces this installation reads")}
	}
	var first error
	failed := cands[0]
	for _, c := range cands {
		got, err := s.promQuery(ctx, c, query)
		switch {
		case err == nil && len(got) > 0:
			return c, got, nil
		case err != nil && first == nil:
			first, failed = err, c
		}
		if isTimeout(err) {
			break
		}
	}
	if first == nil {
		return cands[0], nil, []fleet.Limitation{s.unread("the Prometheus Pacto found holds no " + metric + " series for the last hour, so it does not record this mesh's calls")}
	}
	return failed, nil, []fleet.Limitation{s.unread(promErrorText(first))}
}

// unread is a MESH_TELEMETRY_UNREAD limitation of this source.
func (s *MeshSource) unread(message string) fleet.Limitation {
	return fleet.Limitation{Code: fleet.LimitationMeshTelemetryUnread, Source: s.id, Message: message}
}

// isTimeout reports whether err is a deadline or a transport timeout.
func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

// promErrorText is the fixed text of a failed Prometheus read, never its
// raw error.
func promErrorText(err error) string {
	var status promStatusError
	switch {
	case errors.As(err, &status):
		return fmt.Sprintf("the Prometheus Pacto found refused the query (HTTP %d)", status.code)
	case isTimeout(err):
		return "the Prometheus Pacto found timed out"
	case errors.Is(err, errPromTooLarge):
		return "the Prometheus Pacto found answered more than 16 MiB"
	case errors.Is(err, errPromForbidden):
		return "the Prometheus Pacto found is not reachable from here and you may not proxy to it"
	case errors.Is(err, errPromUnreadable):
		return "the Prometheus Pacto found answered with an unreadable body"
	default:
		return "the Prometheus Pacto found is not reachable from here"
	}
}

// istioBy are the labels the Istio query keeps. cluster is the external
// label a Prometheus receiving other clusters' series conventionally adds.
// destination_service_name names the host of a call leaving the cluster.
const istioBy = "reporter, cluster, source_cluster, source_workload, source_workload_namespace, destination_cluster, destination_workload, destination_workload_namespace, destination_service_name"

// istioQuery is the calls and TCP connections of the last hour between
// workloads, both ends narrowed to namespaces when scoped. A scoped
// destination also keeps calls leaving the cluster: an unknown destination
// and the trusted egress gateways.
func istioQuery(namespaces []string) string {
	sel := ""
	if m := scopeMatcher("source_workload_namespace", namespaces); m != "" {
		sel = "{" + m + "," + scopeMatcher("destination_workload_namespace", widened(namespaces, "unknown", detect.GatewayNamespace)) + "}"
	}
	return fmt.Sprintf("sum by (%s) (increase(istio_requests_total%s[1h])) or sum by (%s) (increase(istio_tcp_connections_opened_total%s[1h]))",
		istioBy, sel, istioBy, sel)
}

// widened is namespaces with extra, sorted, each once.
func widened(namespaces []string, extra ...string) []string {
	out := append(slices.Clone(namespaces), extra...)
	slices.Sort(out)
	return slices.Compact(out)
}

// meshTally counts what a reader left out of the edges.
type meshTally struct {
	unattributed, outOfScope, crossCluster, passthrough int
	multiCluster, truncated, egressTruncated            bool
}

// limitations words t, counts only.
func (t meshTally) limitations(id string) []fleet.Limitation {
	var out []fleet.Limitation
	add := func(code string, n int, message string) {
		if n > 0 {
			out = append(out, fleet.Limitation{Code: code, Source: id, Message: message})
		}
	}
	add(fleet.LimitationMeshTelemetryUnread, b2i(t.multiCluster), "this Prometheus holds calls of several clusters; Pacto cannot tell which is this one")
	add(fleet.LimitationMeshTelemetryUnread, b2i(t.truncated), "only the "+strconv.Itoa(maxMeshSeries)+" busiest call pairs were read")
	add(fleet.LimitationMeshCallsUnattributed, t.unattributed, countOf(t.unattributed, "call series", "call series")+" had an end Pacto could not name")
	add(fleet.LimitationMeshCallsUnattributed, t.outOfScope, countOf(t.outOfScope, "call series", "call series")+" reach namespaces this installation does not read")
	add(fleet.LimitationMeshCallsUnattributed, t.crossCluster, countOf(t.crossCluster, "cross-cluster call was", "cross-cluster calls were")+" not attributed")
	add(fleet.LimitationMeshCallsUnattributed, t.passthrough, countOf(t.passthrough, "call series", "call series")+" went through the passthrough cluster, which names no host; add a ServiceEntry")
	add(fleet.LimitationEgressSeriesCap, b2i(t.egressTruncated), egressCapText)
	return out
}

// b2i is 1 for true.
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// meshCall is one call series between two named workloads.
type meshCall struct {
	from, to string
	count    float64
	reporter string
}

// egressCapText words a cap on the calls to external hosts kept.
var egressCapText = "only the " + strconv.Itoa(maxEgressSeries) + " busiest calls to external hosts were read"

// callEnds is what one series says about a call: its two ends as K8sCaller
// names ("" when the mesh could not name one), their namespaces, the cluster
// it was seen in and whether it crossed to another cluster. host is set
// when the call left the cluster for a named host; passthrough when it left
// with no host named.
type callEnds struct {
	from, to, fromNS, toNS string
	cluster, reporter      string
	host                   string
	cross, passthrough     bool
}

// egressHost is name as a host outside the cluster: lowercase, port,
// trailing dot and a leading "*." (a wildcard ServiceEntry) stripped. It is
// "" when the fleet could not fold it into an External: no dotted host, an
// in-cluster Service name, an address or a name the catalog cannot classify
// (one under a private suffix). The caller counts such a call as one it
// could not name.
func egressHost(name string) string {
	h := strings.ToLower(name)
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.TrimPrefix(strings.TrimSuffix(h, "."), "*.")
	if strings.Contains(h, ".svc.") || strings.HasSuffix(h, ".svc") {
		return ""
	}
	hs := externalcatalog.NormalizeHost(h)
	if len(hs) == 0 {
		return ""
	}
	if _, ok := externalcatalog.Default().MatchHost(hs[0]); !ok {
		return ""
	}
	return h
}

// istioEgress sets e.host or e.passthrough when the series m is a call that
// left the cluster: its destination is unknown or a trusted egress gateway
// (in the gateway namespace; a tenant can name its own workload so), and
// its service name is a host, or Istio's passthrough cluster.
func istioEgress(e *callEnds, m map[string]string) {
	dst := m["destination_workload"]
	gateway := m["destination_workload_namespace"] == detect.GatewayNamespace && strings.Contains(dst, "egressgateway")
	if named(dst) != "" && !gateway {
		return
	}
	if svc := m["destination_service_name"]; svc == "PassthroughCluster" {
		e.passthrough = true
	} else {
		e.host = egressHost(svc)
	}
}

// istioEnds reads an Istio series: kindless names, cross-cluster when its
// two cluster labels differ, seen in the cluster its external cluster label
// names, else its own cluster labels (Istio names every cluster Kubernetes
// by default).
func istioEnds(m map[string]string) callEnds {
	srcNS, dstNS := named(m["source_workload_namespace"]), named(m["destination_workload_namespace"])
	src, dst := named(m["source_workload"]), named(m["destination_workload"])
	srcCluster, dstCluster := named(m["source_cluster"]), named(m["destination_cluster"])
	e := callEnds{fromNS: srcNS, toNS: dstNS, reporter: m["reporter"], cluster: cmp.Or(m["cluster"], srcCluster, dstCluster),
		cross: srcCluster != "" && dstCluster != "" && srcCluster != dstCluster}
	if srcNS != "" && src != "" {
		e.from = fleet.K8sCaller("", srcNS, "", src)
	}
	if dstNS != "" && dst != "" {
		e.to = fleet.K8sCaller("", dstNS, "", dst)
	}
	istioEgress(&e, m)
	return e
}

// istioEdges folds Istio series into kindless edges: the larger of the two
// reporters per pair, ends Pacto cannot name, ends outside namespaces and
// cross-cluster series counted and left out, nothing when the calls kept
// belong to several clusters.
func istioEdges(series []promSeries, asOf time.Time, namespaces []string) ([]fleet.ObservedEdge, meshTally) {
	return seriesEdges(series, asOf, namespaces, istioEnds)
}

// linkerdBy are the labels the Linkerd query keeps.
// authority names the host of a call leaving the cluster.
const linkerdBy = "namespace, deployment, statefulset, daemonset, dst_namespace, dst_deployment, dst_statefulset, dst_daemonset, target_cluster_name, cluster, authority"

// linkerdQuery is the outbound responses and TCP connections of the last
// hour between workloads, both ends narrowed to namespaces when scoped. A
// scoped destination also keeps calls with no destination namespace, those
// leaving the cluster.
func linkerdQuery(namespaces []string) string {
	sel := `direction="outbound"`
	if m := scopeMatcher("namespace", namespaces); m != "" {
		sel += "," + m + "," + scopeMatcher("dst_namespace", widened(namespaces, ""))
	}
	return fmt.Sprintf("sum by (%s) (increase(response_total{%s}[1h])) or sum by (%s) (increase(tcp_open_total{%s}[1h]))",
		linkerdBy, sel, linkerdBy, sel)
}

// linkerdWorkloads are the workload labels a Linkerd proxy sets, and their
// kinds.
var linkerdWorkloads = [][2]string{{"deployment", "Deployment"}, {"statefulset", "StatefulSet"}, {"daemonset", "DaemonSet"}}

// linkerdEnd is the K8sCaller name of the end whose labels start with
// prefix, "" when no workload label is set (a Job pod, an unmeshed peer).
func linkerdEnd(m map[string]string, prefix string) string {
	ns := m[prefix+"namespace"]
	for _, w := range linkerdWorkloads {
		if name := m[prefix+w[0]]; name != "" && ns != "" {
			return fleet.K8sCaller("", ns, w[1], name)
		}
	}
	return ""
}

// linkerdEnds reads a Linkerd outbound series: named with their kind, a call
// to a mirrored service of another cluster (target_cluster_name) crossing,
// seen in the cluster its external cluster label names.
// A series with no destination workload whose authority is a host left the
// cluster.
func linkerdEnds(m map[string]string) callEnds {
	e := callEnds{from: linkerdEnd(m, ""), to: linkerdEnd(m, "dst_"), fromNS: m["namespace"], toNS: m["dst_namespace"],
		cluster: m["cluster"], cross: m["target_cluster_name"] != ""}
	if e.to == "" {
		e.host = egressHost(m["authority"])
	}
	return e
}

// linkerdEdges folds Linkerd series into edges named with their kind; the
// same rules as istioEdges.
func linkerdEdges(series []promSeries, asOf time.Time, namespaces []string) ([]fleet.ObservedEdge, meshTally) {
	return seriesEdges(series, asOf, namespaces, linkerdEnds)
}

// seriesEdges folds series read by ends into edges, counting and leaving
// out ends Pacto cannot name, ends outside namespaces and cross-cluster
// calls, and giving nothing when the calls kept belong to several clusters.
// A call from a caller in namespaces that left the cluster is an edge to
// its host, kept apart under its own cap.
func seriesEdges(series []promSeries, asOf time.Time, namespaces []string, ends func(map[string]string) callEnds) ([]fleet.ObservedEdge, meshTally) {
	var t meshTally
	var calls, egress []meshCall
	clusters := map[string]bool{}
	for _, p := range series {
		v := p.value()
		if v <= 0 {
			continue
		}
		e := ends(p.Metric)
		leaves := e.from != "" && (e.host != "" || e.passthrough) && inScope(namespaces, e.fromNS)
		switch {
		case leaves && e.passthrough:
			t.passthrough++
		case leaves:
			clusters[e.cluster] = true
			egress = append(egress, meshCall{from: e.from, to: e.host, count: v, reporter: e.reporter})
		case e.from == "" || e.to == "":
			t.unattributed++
		case !inScope(namespaces, e.fromNS) || !inScope(namespaces, e.toNS):
			t.outOfScope++
		case e.cross:
			t.crossCluster++
		default:
			clusters[e.cluster] = true
			calls = append(calls, meshCall{from: e.from, to: e.to, count: v, reporter: e.reporter})
		}
	}
	delete(clusters, "")
	if len(clusters) > 1 {
		return nil, meshTally{multiCluster: true}
	}
	edges, truncated := foldCalls(calls, asOf, maxMeshSeries)
	out, egressTruncated := foldCalls(egress, asOf, maxEgressSeries)
	t.truncated, t.egressTruncated = truncated, egressTruncated
	return append(edges, out...), t
}

// named is v, or "" when the mesh could not name it.
func named(v string) string {
	if v == "unknown" {
		return ""
	}
	return v
}

// inScope reports whether ns is read: every namespace when unscoped.
func inScope(namespaces []string, ns string) bool {
	return len(namespaces) == 0 || slices.Contains(namespaces, ns)
}

// foldCalls is one edge per pair: the largest count of any reporter, at
// least 1, seen in the window ending at asOf; the busiest limit kept. A
// call to itself is no edge.
func foldCalls(calls []meshCall, asOf time.Time, limit int) (edges []fleet.ObservedEdge, truncated bool) {
	type pair struct{ from, to string }
	per := map[pair]map[string]float64{}
	for _, c := range calls {
		if c.from == c.to {
			continue
		}
		k := pair{c.from, c.to}
		if per[k] == nil {
			per[k] = map[string]float64{}
		}
		per[k][c.reporter] += c.count
	}
	for k, by := range per {
		top := slices.Max(slices.Collect(maps.Values(by)))
		edges = append(edges, fleet.ObservedEdge{From: k.from, To: k.to, Count: max(1, int(math.Round(min(top, 1e15)))),
			FirstSeen: asOf.Add(-meshWindow), LastSeen: asOf})
	}
	slices.SortFunc(edges, func(a, b fleet.ObservedEdge) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To))
	})
	if len(edges) > limit {
		return edges[:limit], true
	}
	return edges, false
}
