package fleetsrc

import (
	"cmp"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/otelobserver"
)

// Bounds of the OTLP receiver. ponytail: fixed; a flag when a cluster proves
// one too small.
const (
	receiverPath        = "/v1/traces"
	maxReceiverBody     = 4 << 20 // 4 MiB
	maxReceiverInFlight = 2
	maxReceiverPairs    = 5000
	maxReceiverNames    = 1000
	maxReceiverSenders  = 1000
	receiverRowName     = "OTLP receiver"
)

// Receiver is the dashboard's OTLP/HTTP receiver: POST /v1/traces with an
// OTLP JSON body and nothing else. It maps spans to calls through
// [otelobserver.DependencyEdges], drops the spans and keeps, per caller and
// callee, the call count and when it received them (span times are the
// sender's claim), plus how many distinct senders it heard from: a count,
// never an address. Everything is bounded and pruned after 15 minutes. It
// runs on its own listener, never on the dashboard's.
type Receiver struct {
	host, url, addr string
	now             func() time.Time
	slots           chan struct{}

	mu      sync.Mutex
	pairs   map[[2]string]*pairSeen
	names   map[string]int
	senders map[string]time.Time
	// requests counts JSON requests, protobuf refused protobuf ones, encoded
	// refused ones in an encoding it does not read, dropped calls beyond a cap
	// and unattributed calls with a refused name.
	requests, protobuf, encoded, dropped, unattributed windowCount
	// offBy is the source that won the last time the receiver source read,
	// empty while the receiver is on.
	offBy string
}

// pairSeen is the calls of one caller and callee pair.
type pairSeen struct {
	count       int
	first, last time.Time
}

// windowCount is a count that restarts once its last event is older than the
// window. ponytail: coarse; per-event times when a row needs exact counts.
type windowCount struct {
	n    int
	last time.Time
}

func (c *windowCount) add(n int, at time.Time) { c.n, c.last = c.n+n, at }

func (c *windowCount) prune(at time.Time) {
	if at.Sub(c.last) > traceWindow {
		c.n = 0
	}
}

// NewReceiver is a receiver Collectors reach at advertised, an http://HOST:PORT
// URL: it listens on PORT, on loopback when HOST is a loopback name.
func NewReceiver(advertised string, now func() time.Time) (*Receiver, error) {
	u, err := url.Parse(advertised)
	if err != nil || u.Scheme != "http" || u.Hostname() == "" || u.User != nil || strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("--otlp-receiver %q is not http://HOST:PORT", advertised)
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()
	// Port 0 listens on any free port, which Collectors cannot be told:
	// only a loopback receiver (a test) may take it.
	if err != nil || port == 0 && !loopback {
		return nil, fmt.Errorf("--otlp-receiver %q names no port", advertised)
	}
	addr := ":" + strconv.FormatUint(port, 10)
	if loopback {
		addr = "127.0.0.1" + addr
	}
	return &Receiver{
		host: u.Host, url: "http://" + u.Host, addr: addr, now: now, slots: make(chan struct{}, maxReceiverInFlight),
		pairs: map[[2]string]*pairSeen{}, names: map[string]int{}, senders: map[string]time.Time{},
	}, nil
}

// Addr is the address the receiver listens on.
func (r *Receiver) Addr() string { return r.addr }

// ServeHTTP serves POST /v1/traces only: any other path is 404, any other
// method 405, a body that is not JSON 415, a body over 4 MiB 413 and a third
// request at once 503 with Retry-After. The answer is the OTLP JSON
// ExportTraceServiceResponse; while another source won it rejects every span.
func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch {
	case req.URL.Path != receiverPath:
		http.NotFound(w, req)
		return
	case req.Method != http.MethodPost:
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "only POST", http.StatusMethodNotAllowed)
		return
	}
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	default:
		w.Header().Set("Retry-After", "1")
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	if mt, _, _ := mime.ParseMediaType(req.Header.Get("Content-Type")); mt != "application/json" {
		if strings.Contains(mt, "protobuf") {
			r.count(&r.protobuf)
		}
		http.Error(w, "send OTLP JSON: set encoding: json", http.StatusUnsupportedMediaType)
		return
	}
	data, err := readBody(w, req)
	var tooBig *http.MaxBytesError
	switch {
	case errors.Is(err, errEncoding):
		r.count(&r.encoded)
		http.Error(w, "send OTLP JSON with no compression or gzip", http.StatusUnsupportedMediaType)
		return
	case errors.As(err, &tooBig), errors.Is(err, errTooBig):
		http.Error(w, "body over 4 MiB", http.StatusRequestEntityTooLarge)
		return
	}
	td, perr := otelobserver.ParseTraces(data)
	if err != nil || perr != nil {
		http.Error(w, "not OTLP JSON traces", http.StatusBadRequest)
		return
	}
	resp := map[string]any{}
	if off := r.record(td, req.RemoteAddr); off != "" {
		resp["partialSuccess"] = map[string]any{"rejectedSpans": strconv.Itoa(spanCount(td)), "errorMessage": "pacto reads edges from " + off}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// errEncoding and errTooBig are a body in an encoding the receiver does not
// read and a gzip body over 4 MiB once decompressed.
var (
	errEncoding = errors.New("unsupported Content-Encoding")
	errTooBig   = errors.New("decompressed body over 4 MiB")
)

// readBody is req's body, at most 4 MiB as sent and, gzip-compressed (the
// Collector's otlphttp default), at most 4 MiB decompressed too.
func readBody(w http.ResponseWriter, req *http.Request) ([]byte, error) {
	body := io.Reader(http.MaxBytesReader(w, req.Body, maxReceiverBody))
	switch strings.ToLower(strings.TrimSpace(req.Header.Get("Content-Encoding"))) {
	case "", "identity":
	case "gzip":
		zr, err := gzip.NewReader(body)
		if err != nil {
			return nil, err
		}
		body = io.LimitReader(zr, maxReceiverBody+1)
	default:
		return nil, errEncoding
	}
	data, err := io.ReadAll(body)
	if err == nil && len(data) > maxReceiverBody {
		err = errTooBig
	}
	return data, err
}

// spanCount is how many spans td holds.
func spanCount(td *otelobserver.TracesData) int {
	n := 0
	for _, rs := range td.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			n += len(ss.Spans)
		}
	}
	return n
}

// count adds one to c now.
func (r *Receiver) count(c *windowCount) {
	r.mu.Lock()
	defer r.mu.Unlock()
	at := r.now()
	c.prune(at)
	c.add(1, at)
}

// record keeps the calls of td from the sender at remote (host only, counted)
// and returns the source that won, empty while the receiver is on.
func (r *Receiver) record(td *otelobserver.TracesData, remote string) string {
	edges := otelobserver.DependencyEdges(td)
	r.mu.Lock()
	defer r.mu.Unlock()
	at := r.now()
	r.prune(at)
	r.requests.add(1, at)
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	if _, ok := r.senders[host]; ok || len(r.senders) < maxReceiverSenders {
		r.senders[host] = at
	}
	for _, e := range edges {
		r.keep(e, at)
	}
	return r.offBy
}

// keep adds edge e received at at, unless a name is unattributable or a cap
// is reached; either is counted.
func (r *Receiver) keep(e otelobserver.Edge, at time.Time) {
	if !otelobserver.AttributableName(e.From) || !otelobserver.AttributableName(e.To) {
		r.unattributed.add(e.Count, at)
		return
	}
	k := [2]string{e.From, e.To}
	if p, ok := r.pairs[k]; ok {
		p.count, p.last = p.count+e.Count, at
		return
	}
	newNames := 0
	for _, n := range k {
		if r.names[n] == 0 {
			newNames++
		}
	}
	if len(r.pairs) >= maxReceiverPairs || len(r.names)+newNames > maxReceiverNames {
		r.dropped.add(e.Count, at)
		return
	}
	r.pairs[k] = &pairSeen{count: e.Count, first: at, last: at}
	for _, n := range k {
		r.names[n]++
	}
}

// prune forgets everything older than the window ending at at.
func (r *Receiver) prune(at time.Time) {
	for k, p := range r.pairs {
		if at.Sub(p.last) > traceWindow {
			delete(r.pairs, k)
			for _, n := range k {
				if r.names[n]--; r.names[n] == 0 {
					delete(r.names, n)
				}
			}
		}
	}
	for h, t := range r.senders {
		if at.Sub(t) > traceWindow {
			delete(r.senders, h)
		}
	}
	for _, c := range []*windowCount{&r.requests, &r.protobuf, &r.encoded, &r.dropped, &r.unattributed} {
		c.prune(at)
	}
}

// receiverWindow is what the receiver holds for the last 15 minutes.
type receiverWindow struct {
	edges                                              []fleet.ObservedEdge
	calls, senders                                     int
	requests, protobuf, encoded, dropped, unattributed int
}

// window is the receiver's last 15 minutes, edges sorted.
func (r *Receiver) window() receiverWindow {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune(r.now())
	w := receiverWindow{senders: len(r.senders), requests: r.requests.n, protobuf: r.protobuf.n, encoded: r.encoded.n, dropped: r.dropped.n, unattributed: r.unattributed.n}
	for k, p := range r.pairs {
		w.calls += p.count
		w.edges = append(w.edges, fleet.ObservedEdge{From: k[0], To: k[1], Count: p.count, FirstSeen: p.first, LastSeen: p.last})
	}
	slices.SortFunc(w.edges, func(a, b fleet.ObservedEdge) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To))
	})
	return w
}

// setOff records the source that won (empty: the receiver is on).
func (r *Receiver) setOff(winner string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.offBy = winner
}

// ReceiverSource is the advisory, caution-only [fleet.Source] over the
// calls the dashboard's receiver holds: anything in the cluster can send
// them, so they add callers and undeclared edges and never corroborate a
// declared one. Its row is the detected Collector's when there is one, else
// an "OTLP receiver" row once a request arrived.
type ReceiverSource struct {
	id        string
	res       *detect.Result
	collector *detect.Entry
	live      *LiveEdges
}

// NewReceiverSource reads live.Receiver. res and collector are the detection
// and its otel-collector entry, both nil when none was read.
func NewReceiverSource(id string, res *detect.Result, collector *detect.Entry, live *LiveEdges) *ReceiverSource {
	return &ReceiverSource{id: id, res: res, collector: collector, live: live}
}

// ID implements [fleet.Source].
func (s *ReceiverSource) ID() string { return s.id }

// Kind is [fleet.SourceKindOTLPReceiver].
func (s *ReceiverSource) Kind() string { return fleet.SourceKindOTLPReceiver }

// Collect reports the window's calls, or none while another source won.
func (s *ReceiverSource) Collect(context.Context) (*fleet.Collection, error) {
	r := s.live.Receiver
	w := r.window()
	name, missing := s.live.verdict()
	winner := name
	off := winner != "" && missing == 0
	if !off {
		winner = ""
	}
	r.setOff(winner)
	at := s.live.now()
	live := detect.LiveState{ReadAt: at, Status: fleet.SourceAvailable}
	var edges []fleet.ObservedEdge
	switch {
	case off:
		live.Status, live.Reason = fleet.SourcePartial, "off: "+winner+" reports complete coverage"
	case w.requests == 0:
		live.Status, live.Reason = fleet.SourcePartial, "not exporting to "+r.host
		live.Hint, live.Details = s.hint()
	case w.calls == 0:
		// A probe, an empty export or server spans only: nothing to report.
		live.Status, live.Reason = fleet.SourcePartial, "the requests it received held no call between named services"
		live.Hint, live.Details = s.hint()
	default:
		edges = w.edges
		live.Details = []string{fmt.Sprintf("received %d calls from %d in-cluster senders in the last 15 minutes", w.calls, w.senders)}
	}
	live.Edges = len(edges)
	reasons := receiverReasons(w)
	if missing > 0 {
		live.Details = append(live.Details, missesText(name, missing))
	}
	if len(reasons) > 0 {
		live.Status, live.Reason = fleet.SourcePartial, strings.Join(slices.DeleteFunc(append([]string{live.Reason}, reasons...), func(s string) bool { return s == "" }), "; ")
	}
	col := &fleet.Collection{Advisory: true, Observed: edges, ObservedUndeclaredOnly: true, ObservedCautionOnly: true,
		State: &fleet.SourceState{Status: fleet.SourcePartial, ObservedAt: &at}}
	if live.Status == fleet.SourceAvailable {
		col.State.Status = fleet.SourceAvailable
	}
	res, e := detect.Result{}, detect.Entry{Kind: s.id, Completeness: detect.Complete, Evidence: "OTLP/HTTP JSON at " + r.host}
	switch {
	case s.collector != nil:
		res, e = *s.res, *s.collector
	case w.requests == 0 && w.protobuf == 0 && w.encoded == 0:
		// With no Collector detected the row appears once something was sent.
		return col, nil
	default:
		live.Name = receiverRowName
	}
	col.Providers = []fleet.Provider{detect.EdgeSourceRow(res, e, s.id, live)}
	return col, nil
}

// receiverReasons are why the window is incomplete, counts only.
func receiverReasons(w receiverWindow) []string {
	var out []string
	if w.protobuf > 0 {
		out = append(out, fmt.Sprintf("%d requests used protobuf; set encoding: json", w.protobuf))
	}
	if w.encoded > 0 {
		out = append(out, fmt.Sprintf("%d requests used a compression other than gzip; set compression: gzip", w.encoded))
	}
	if w.dropped > 0 {
		out = append(out, fmt.Sprintf("%d calls dropped beyond %d call pairs or %d names", w.dropped, maxReceiverPairs, maxReceiverNames))
	}
	if w.unattributed > 0 {
		out = append(out, fmt.Sprintf("%d calls are not attributable to a named service", w.unattributed))
	}
	return out
}

// hint is the one-line Collector configuration that makes calls reach Pacto
// and what to do with it: the service graph first when the Collector's entry
// names a Prometheus, else the receiver exporter.
func (s *ReceiverSource) hint() (string, []string) {
	exporter := `exporters: {otlphttp/pacto: {endpoint: "` + s.live.Receiver.url + `", encoding: json}}`
	if s.collector != nil && s.collector.Endpoint != "" {
		// Additive only: a list here would replace the Collector's own
		// traces exporters when merged.
		return `connectors: {servicegraph: {}}, exporters: {prometheus: {endpoint: "0.0.0.0:8889"}}, ` +
				`service: {pipelines: {metrics/servicegraph: {receivers: [servicegraph], exporters: [prometheus]}}}`,
			[]string{"add servicegraph to service.pipelines.traces.exporters and let Prometheus scrape the prometheus exporter; or send traces to Pacto: " + exporter}
	}
	return exporter, []string{"add otlphttp/pacto to service.pipelines.traces.exporters"}
}
