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
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

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
)

// promStatusError is a Prometheus answer with a status other than 200.
type promStatusError struct{ code int }

func (e promStatusError) Error() string { return "prometheus answered " + strconv.Itoa(e.code) }

// promQuery runs query at now against cand. Outside the cluster Prometheus
// is not reachable: a laptop never resolves a .svc name.
func (s *MeshSource) promQuery(ctx context.Context, cand svcInfo, query string) ([]promSeries, error) {
	if !s.inCluster {
		return nil, errPromUnreachable
	}
	u := s.baseURL(cand.Namespace, cand.Name, servicePort(cand)) + "/api/v1/query?" + url.Values{
		"query": {query}, "time": {strconv.FormatInt(s.now().Unix(), 10)},
	}.Encode()
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
// that names no Service; cand is the Service read, else the first asked.
func (s *MeshSource) firstAnswer(ctx context.Context, cands []svcInfo, query, metric string) (cand svcInfo, series []promSeries, lims []fleet.Limitation) {
	if len(cands) == 0 {
		return svcInfo{}, nil, []fleet.Limitation{s.unread("no Prometheus found in the namespaces this installation reads")}
	}
	var first error
	answered := false
	for _, c := range cands {
		got, err := s.promQuery(ctx, c, query)
		if err == nil && len(got) > 0 {
			return c, got, nil
		}
		answered = answered || err == nil
		first = cmp.Or(first, err)
		if isTimeout(err) {
			break
		}
	}
	if answered {
		return cands[0], nil, []fleet.Limitation{s.unread("the Prometheus Pacto found holds no " + metric + " series for the last hour, so it does not record this mesh's calls")}
	}
	return cands[0], nil, []fleet.Limitation{s.unread(promErrorText(first))}
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
	case errors.Is(err, errPromUnreadable):
		return "the Prometheus Pacto found answered with an unreadable body"
	default:
		return "the Prometheus Pacto found is not reachable from here"
	}
}

// istioBy are the labels the Istio query keeps.
const istioBy = "reporter, source_cluster, source_workload, source_workload_namespace, destination_cluster, destination_workload, destination_workload_namespace"

// istioQuery is the calls and TCP connections of the last hour between
// workloads, both ends narrowed to namespaces when scoped.
func istioQuery(namespaces []string) string {
	sel := ""
	if m := scopeMatcher("source_workload_namespace", namespaces); m != "" {
		sel = "{" + m + "," + scopeMatcher("destination_workload_namespace", namespaces) + "}"
	}
	return fmt.Sprintf("sum by (%s) (increase(istio_requests_total%s[1h])) or sum by (%s) (increase(istio_tcp_connections_opened_total%s[1h]))",
		istioBy, sel, istioBy, sel)
}

// meshTally counts what a reader left out of the edges.
type meshTally struct {
	unattributed, outOfScope, crossCluster int
	multiCluster, truncated                bool
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

// istioEdges folds Istio series into kindless edges: the larger of the two
// reporters per pair, ends Pacto cannot name, ends outside namespaces and
// cross-cluster series counted and left out, nothing when the calls kept
// belong to several clusters.
func istioEdges(series []promSeries, asOf time.Time, namespaces []string) ([]fleet.ObservedEdge, meshTally) {
	var t meshTally
	var calls []meshCall
	clusters := map[string]bool{}
	for _, p := range series {
		m, v := p.Metric, p.value()
		if v <= 0 {
			continue
		}
		srcCluster, dstCluster := named(m["source_cluster"]), named(m["destination_cluster"])
		switch src, dst := m["source_workload"], m["destination_workload"]; {
		case named(src) == "" || named(dst) == "" || named(m["source_workload_namespace"]) == "" || named(m["destination_workload_namespace"]) == "":
			t.unattributed++
		case !inScope(namespaces, m["source_workload_namespace"]) || !inScope(namespaces, m["destination_workload_namespace"]):
			t.outOfScope++
		case srcCluster != "" && dstCluster != "" && srcCluster != dstCluster:
			t.crossCluster++
		default:
			clusters[cmp.Or(srcCluster, dstCluster)] = true
			calls = append(calls, meshCall{
				from:  fleet.K8sCaller("", m["source_workload_namespace"], "", src),
				to:    fleet.K8sCaller("", m["destination_workload_namespace"], "", dst),
				count: v, reporter: m["reporter"],
			})
		}
	}
	delete(clusters, "")
	if len(clusters) > 1 {
		return nil, meshTally{multiCluster: true}
	}
	edges, truncated := foldCalls(calls, asOf)
	t.truncated = truncated
	return edges, t
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
// least 1, seen in the window ending at asOf; the busiest maxMeshSeries kept.
// A call to itself is no edge.
func foldCalls(calls []meshCall, asOf time.Time) (edges []fleet.ObservedEdge, truncated bool) {
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
	if len(edges) > maxMeshSeries {
		return edges[:maxMeshSeries], true
	}
	return edges, false
}
