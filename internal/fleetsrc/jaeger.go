package fleetsrc

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/otelobserver"
)

// Bounds of a Jaeger read. ponytail: fixed like Mira's; a flag when a
// cluster proves one too small.
const (
	traceWindow        = 15 * time.Minute
	maxJaegerBodyBytes = 16 << 20 // 16 MiB; a body cut at the limit fails to decode
	maxJaegerLinks     = 5000
)

// jaegerName names Jaeger as a source that can win.
const jaegerName = "Jaeger"

// jaegerNoLinks is why a Jaeger that answered with no dependency is partial:
// some storage backends compute dependencies only in a batch job.
const jaegerNoLinks = "Jaeger returned no dependencies for the last 15 minutes; " +
	"Cassandra and Elasticsearch storage need the spark-dependencies job"

// jaegerLink is one dependency of Jaeger's GET /api/dependencies answer
// (Jaeger v1 query and v2 both serve it).
type jaegerLink struct {
	Parent    string `json:"parent"`
	Child     string `json:"child"`
	CallCount int    `json:"callCount"`
}

// JaegerSource is an advisory [fleet.Source] over the dependencies a
// detected Jaeger query API computes for the last 15 minutes. It puts raw
// service names into the collection and lets [fleet.Build] resolve them;
// Jaeger samples, so its silence never counts against a declared call
// (ObservedUndeclaredOnly). Collect never fails: a Jaeger that does not
// answer is a partial source with an unavailable row that says so, so the
// row stays in Detected providers.
type JaegerSource struct {
	id     string
	res    detect.Result
	entry  detect.Entry
	live   *LiveEdges
	now    func() time.Time
	client *http.Client
}

// NewJaegerSource reads the Jaeger of entry e of res, at e's in-cluster
// Endpoint, and records a read with complete coverage in live. The client
// refuses redirects, ignores proxies and times out after 10s.
func NewJaegerSource(id string, res detect.Result, e detect.Entry, live *LiveEdges, now func() time.Time) *JaegerSource {
	return &JaegerSource{id: id, res: res, entry: e, live: live, now: now, client: &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		// ponytail: in-cluster .svc names never go through an egress proxy.
		Transport: &http.Transport{Proxy: nil},
	}}
}

// ID implements [fleet.Source].
func (s *JaegerSource) ID() string { return s.id }

// Kind is [fleet.SourceKindTraces].
func (s *JaegerSource) Kind() string { return fleet.SourceKindTraces }

// Collect reads the dependencies and reports them with the source's one row.
func (s *JaegerSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	at := s.now()
	live := detect.LiveState{ReadAt: at}
	var edges []fleet.ObservedEdge
	links, err := s.read(ctx, at)
	if err != nil {
		live.Status, live.Reason = fleet.SourceUnavailable, err.Error()
	} else {
		var reasons []string
		edges, reasons = jaegerEdges(links, at)
		if n := s.candidates(); n > 1 {
			reasons = append(reasons, fmt.Sprintf("%d Jaeger query Services found; Pacto read none of them as complete", n))
		}
		live.Status, live.Edges, live.Reason = fleet.SourceAvailable, len(edges), strings.Join(reasons, "; ")
		if len(reasons) > 0 {
			live.Status = fleet.SourcePartial
		}
	}
	return edgeCollection(s.res, s.entry, s.id, live, s.live, jaegerName, edges), nil
}

// edgeCollection is the collection of a live edge source over entry e of
// res: its one row, the edges it read, advisory and undeclared-only, and a
// read with complete coverage (an available row) recorded in le under name.
// The winner's row says whether the receiver is off or what it misses.
// The source state is never unavailable: that would drop the row, which says
// why instead.
func edgeCollection(res detect.Result, e detect.Entry, id string, live detect.LiveState, le *LiveEdges, name string, edges []fleet.ObservedEdge) *fleet.Collection {
	row := detect.EdgeSourceRow(res, e, id, live)
	if row.Status != fleet.SourceAvailable {
		// Only the latest read counts: a backend that failed never wins.
		le.markIncomplete(name)
	} else {
		le.markComplete(name, edges)
		// The winner's row says what the receiver does about it.
		if winner, missing := le.verdict(); winner == name && le.Receiver != nil {
			if missing == 0 {
				live.Won = "the OTLP receiver is off"
			} else {
				live.Details = append(live.Details, missesText(name, missing))
			}
			row = detect.EdgeSourceRow(res, e, id, live)
		}
	}
	state := row.Status
	if state == fleet.SourceUnavailable {
		state = fleet.SourcePartial
	}
	at := live.ReadAt
	return &fleet.Collection{
		Advisory: true, Observed: edges, ObservedUndeclaredOnly: true, Providers: []fleet.Provider{row},
		State: &fleet.SourceState{Status: state, ObservedAt: &at},
	}
}

// candidates is how many Jaeger query Services the entry names.
func (s *JaegerSource) candidates() int {
	n := 0
	for _, o := range s.entry.Objects {
		if o.Kind == "Service" {
			n++
		}
	}
	return n
}

// jaegerError is why a read failed, never with the URL or a raw body.
type jaegerError string

func (e jaegerError) Error() string { return string(e) }

// read asks the dependencies of the window ending at at.
func (s *JaegerSource) read(ctx context.Context, at time.Time) ([]jaegerLink, error) {
	if s.entry.Endpoint == "" {
		return nil, jaegerError("the detection names no in-cluster Jaeger query URL")
	}
	u := s.entry.Endpoint + "/api/dependencies?endTs=" + strconv.FormatInt(at.UnixMilli(), 10) +
		"&lookback=" + strconv.FormatInt(traceWindow.Milliseconds(), 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, jaegerError("the detection names an unusable Jaeger query URL")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, jaegerError("the Jaeger query API did not answer")
	}
	defer func() { _ = resp.Body.Close() }()
	// A refused redirect surfaces here as its 3xx status.
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, jaegerError(fmt.Sprintf("the Jaeger query API answered %d", resp.StatusCode))
	}
	var body struct {
		Data []jaegerLink `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJaegerBodyBytes)).Decode(&body); err != nil {
		return nil, jaegerError("the Jaeger query API answered something Pacto cannot read")
	}
	return body.Data, nil
}

// jaegerEdges folds at most maxJaegerLinks links into one edge per pair of
// names, sorted, each seen in the window ending at at, with the reasons the
// read is incomplete (counts only, never names). A link with a name
// [otelobserver.AttributableName] refuses or no calls is dropped and counted;
// a call within one service is dropped. Sums saturate.
func jaegerEdges(links []jaegerLink, at time.Time) ([]fleet.ObservedEdge, []string) {
	var reasons []string
	if len(links) == 0 {
		reasons = append(reasons, jaegerNoLinks)
	}
	if len(links) > maxJaegerLinks {
		links = links[:maxJaegerLinks]
		reasons = append(reasons, fmt.Sprintf("the read stopped at %d dependencies", maxJaegerLinks))
	}
	var edges []fleet.ObservedEdge
	index, unattributed := map[[2]string]int{}, 0
	for _, l := range links {
		switch {
		case !otelobserver.AttributableName(l.Parent) || !otelobserver.AttributableName(l.Child) || l.CallCount <= 0:
			unattributed++
		case l.Parent == l.Child:
		default:
			k := [2]string{l.Parent, l.Child}
			if i, ok := index[k]; ok {
				edges[i].Count = min(edges[i].Count, math.MaxInt-l.CallCount) + l.CallCount
				continue
			}
			index[k] = len(edges)
			edges = append(edges, fleet.ObservedEdge{From: l.Parent, To: l.Child, Count: l.CallCount, FirstSeen: at.Add(-traceWindow), LastSeen: at})
		}
	}
	if unattributed > 0 {
		reasons = append(reasons, fmt.Sprintf("%d calls are not attributable to a named service", unattributed))
	}
	slices.SortFunc(edges, func(a, b fleet.ObservedEdge) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To))
	})
	return edges, reasons
}
