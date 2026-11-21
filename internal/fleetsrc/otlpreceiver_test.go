package fleetsrc

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// spanBody is an OTLP JSON payload in the e2e-otel fixture's shape: per
// caller, one CLIENT span with peer.service per callee, dated 2001.
func spanBody(calls map[string][]string) string {
	var rs []map[string]any
	for caller, callees := range calls {
		var spans []map[string]any
		for _, c := range callees {
			spans = append(spans, map[string]any{"kind": 3, "startTimeUnixNano": "978307200000000000", "endTimeUnixNano": "978307201000000000",
				"attributes": []any{map[string]any{"key": "peer.service", "value": map[string]string{"stringValue": c}}}})
		}
		rs = append(rs, map[string]any{
			"resource":   map[string]any{"attributes": []any{map[string]any{"key": "service.name", "value": map[string]string{"stringValue": caller}}}},
			"scopeSpans": []any{map[string]any{"spans": spans}},
		})
	}
	b, _ := json.Marshal(map[string]any{"resourceSpans": rs})
	return string(b)
}

// clock is a settable clock.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// newTestReceiver is a receiver advertised at the in-cluster dashboard
// Service, on clk, held by a LiveEdges on the same clock.
func newTestReceiver(t *testing.T) (*Receiver, *LiveEdges, *clock) {
	t.Helper()
	clk := &clock{t: jaegerNow}
	r, err := NewReceiver("http://pacto-dashboard.pacto-system:4318", clk.now)
	if err != nil {
		t.Fatal(err)
	}
	live := NewLiveEdges(clk.now)
	live.Receiver = r
	return r, live, clk
}

// send posts body to r from remote with content type ct.
func send(r *Receiver, method, path, ct, body, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", ct)
	req.RemoteAddr = remote
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func post(r *Receiver, body string) *httptest.ResponseRecorder {
	return send(r, http.MethodPost, receiverPath, "application/json", body, "10.0.0.1:5555")
}

// collector is a detected Collector entry, with a Prometheus when prom.
func collector(prom bool) (*detect.Result, *detect.Entry) {
	e := detect.Entry{Kind: "otel-collector", Value: "OTel Collector", Evidence: "running in 1 workload", Completeness: detect.Complete}
	if prom {
		e.Endpoint = "http://prometheus.monitoring.svc:9090"
	}
	res := detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: jaegerNow.Add(-time.Minute), Entries: []detect.Entry{e}}
	return &res, &res.Entries[0]
}

// collectReceiver collects s, checking what every collection says.
func collectReceiver(t *testing.T, s *ReceiverSource) *fleet.Collection {
	t.Helper()
	col, err := s.Collect(context.Background())
	if err != nil || !col.Advisory || !col.ObservedUndeclaredOnly || !col.ObservedCautionOnly || col.State == nil {
		t.Fatalf("Collect = %+v, %v", col, err)
	}
	if s.ID() != "otlp-receiver" || s.Kind() != fleet.SourceKindOTLPReceiver {
		t.Fatalf("id %q kind %q", s.ID(), s.Kind())
	}
	return col
}

// rowText is everything a row says.
func rowText(p fleet.Provider) string {
	return strings.Join(append([]string{p.Evidence, p.Hint, reason(p)}, p.Details...), "\n")
}

func TestNewReceiverAddresses(t *testing.T) {
	for in, want := range map[string]string{
		"http://pacto-dashboard.pacto-system:4318": ":4318",
		"http://127.0.0.1:0":                       "127.0.0.1:0",
		"http://localhost:4318/":                   "127.0.0.1:4318",
	} {
		if r, err := NewReceiver(in, time.Now); err != nil || r.Addr() != want {
			t.Errorf("NewReceiver(%q) = %v, %v; want %s", in, r, err, want)
		}
	}
	for _, bad := range []string{"https://x:4318", "http://x", "http://u:p@x:1", "http://x:1/v1/traces", "http://x:1?a=b", "http://x:1#f", "::", "http://x:99999", "http://pacto-dashboard.pacto-system:0"} {
		if _, err := NewReceiver(bad, time.Now); err == nil {
			t.Errorf("NewReceiver(%q) accepted", bad)
		}
	}
}

func TestReceiverMapsSpansToEdges(t *testing.T) {
	r, live, _ := newTestReceiver(t)
	if w := post(r, spanBody(map[string][]string{"web": {"payments"}})); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "{}" {
		t.Fatalf("answer %d %s", w.Code, w.Body)
	}
	col := collectReceiver(t, NewReceiverSource("otlp-receiver", nil, nil, live))
	if len(col.Observed) != 1 || col.Observed[0].From != "web" || col.Observed[0].To != "payments" || col.Observed[0].Count != 1 || col.State.Status != fleet.SourceAvailable {
		t.Fatalf("collection = %+v", col)
	}
}

func TestReceiverKeepsNoSpan(t *testing.T) {
	r, _, _ := newTestReceiver(t)
	body := spanBody(map[string][]string{"web": {"payments"}})
	post(r, body)
	post(r, body)
	if len(r.pairs) != 1 || r.pairs[[2]string{"web", "payments"}].count != 2 || len(r.senders) != 1 || r.requests.n != 2 {
		t.Fatalf("store = %+v senders %v", r.pairs, r.senders)
	}
	// A sender with no port counts by its whole address; past the cap a new
	// sender is not counted.
	send(r, http.MethodPost, receiverPath, "application/json", body, "pipe")
	for i := len(r.senders); i < maxReceiverSenders; i++ {
		r.senders[fmt.Sprint(i)] = jaegerNow
	}
	send(r, http.MethodPost, receiverPath, "application/json", body, "10.9.9.9:1")
	if _, ok := r.senders["pipe"]; !ok || len(r.senders) != maxReceiverSenders {
		t.Fatalf("senders %d", len(r.senders))
	}
}

func TestReceiverOnlyServesPostV1Traces(t *testing.T) {
	r, _, _ := newTestReceiver(t)
	for _, c := range []struct {
		method, path string
		code         int
	}{{http.MethodGet, "/v1/traces", 405}, {http.MethodPost, "/api/fleet", 404}, {http.MethodPost, "/v1/metrics", 404}} {
		if w := send(r, c.method, c.path, "application/json", "{}", "10.0.0.1:1"); w.Code != c.code {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, w.Code, c.code)
		}
	}
	if w := send(r, http.MethodPost, receiverPath, "application/json", "not json", "10.0.0.1:1"); w.Code != 400 {
		t.Errorf("bad body = %d", w.Code)
	}
}

func TestReceiverRefusesProtobufAndCountsIt(t *testing.T) {
	r, live, _ := newTestReceiver(t)
	if w := send(r, http.MethodPost, receiverPath, "application/x-protobuf", "x", "10.0.0.1:1"); w.Code != 415 {
		t.Fatalf("protobuf = %d", w.Code)
	}
	if w := send(r, http.MethodPost, receiverPath, "text/plain", "x", "10.0.0.1:1"); w.Code != 415 {
		t.Fatalf("text = %d", w.Code)
	}
	col := collectReceiver(t, NewReceiverSource("otlp-receiver", nil, nil, live))
	if len(col.Providers) != 1 || col.Providers[0].Name != "OTLP receiver" || !strings.Contains(reason(col.Providers[0]), "1 requests used protobuf; set encoding: json") {
		t.Fatalf("rows = %+v", col.Providers)
	}
}

func TestReceiverBodyCap(t *testing.T) {
	r, _, _ := newTestReceiver(t)
	body := `{"resourceSpans":[]}` + strings.Repeat(" ", maxReceiverBody+1-len(`{"resourceSpans":[]}`))
	if w := post(r, body+" "); w.Code != 413 {
		t.Fatalf("over the cap = %d", w.Code)
	}
	if w := post(r, body[:maxReceiverBody]); w.Code != 200 {
		t.Fatalf("at the cap = %d", w.Code)
	}
}

func TestReceiverConcurrencyCap(t *testing.T) {
	r, _, _ := newTestReceiver(t)
	r.slots <- struct{}{}
	r.slots <- struct{}{}
	w := post(r, "{}")
	if w.Code != 503 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("third request = %d %v", w.Code, w.Header())
	}
}

func TestReceiverPairAndNameCaps(t *testing.T) {
	r, live, _ := newTestReceiver(t)
	calls := map[string][]string{}
	for i := range 100 {
		for j := range 51 {
			calls[fmt.Sprintf("c%d", i)] = append(calls[fmt.Sprintf("c%d", i)], fmt.Sprintf("s%d", j))
		}
	}
	post(r, spanBody(calls))
	if len(r.pairs) != maxReceiverPairs || r.dropped.n != 100 {
		t.Fatalf("pairs %d dropped %d", len(r.pairs), r.dropped.n)
	}
	r2, live2, _ := newTestReceiver(t)
	var many []string
	for j := range 1001 {
		many = append(many, fmt.Sprintf("n%d", j))
	}
	post(r2, spanBody(map[string][]string{"caller": many}))
	if len(r2.names) != maxReceiverNames || r2.dropped.n != 2 {
		t.Fatalf("names %d dropped %d", len(r2.names), r2.dropped.n)
	}
	for _, l := range []*LiveEdges{live, live2} {
		col := collectReceiver(t, NewReceiverSource("otlp-receiver", nil, nil, l))
		if col.State.Status != fleet.SourcePartial || !strings.Contains(reason(col.Providers[0]), "calls dropped beyond 5000 call pairs or 1000 names") {
			t.Fatalf("row = %+v", col.Providers)
		}
	}
}

func TestReceiverStampsReceiveTime(t *testing.T) {
	r, live, _ := newTestReceiver(t)
	post(r, spanBody(map[string][]string{"web": {"payments"}}))
	col := collectReceiver(t, NewReceiverSource("otlp-receiver", nil, nil, live))
	if e := col.Observed[0]; !e.LastSeen.Equal(jaegerNow) || !e.FirstSeen.Equal(jaegerNow) {
		t.Fatalf("edge = %+v", e)
	}
}

func TestReceiverPrunesAfterFifteenMinutes(t *testing.T) {
	r, live, clk := newTestReceiver(t)
	post(r, spanBody(map[string][]string{"web": {"payments"}}))
	send(r, http.MethodPost, receiverPath, "application/x-protobuf", "x", "10.0.0.2:1")
	clk.t = clk.t.Add(16 * time.Minute)
	col := collectReceiver(t, NewReceiverSource("otlp-receiver", nil, nil, live))
	if len(col.Observed) != 0 || len(col.Providers) != 0 || len(r.names) != 0 || len(r.senders) != 0 || r.protobuf.n != 0 {
		t.Fatalf("after the window: %+v, names %v", col, r.names)
	}
}

func TestReceiverDropsK8sShapedNames(t *testing.T) {
	r, live, _ := newTestReceiver(t)
	post(r, spanBody(map[string][]string{"k8s:prod/web": {"payments"}, "web": {"unknown"}}))
	col := collectReceiver(t, NewReceiverSource("otlp-receiver", nil, nil, live))
	if len(col.Observed) != 0 || !strings.Contains(reason(col.Providers[0]), "2 calls are not attributable to a named service") {
		t.Fatalf("collection = %+v", col)
	}
}

// graphWithReceiver is a complete service-graph source over a Prometheus
// answering series, sharing live.
func graphWithReceiver(t *testing.T, live *LiveEdges, series ...string) *ServiceGraphSource {
	t.Helper()
	srv, _ := graphServer(t, 200, vector(series...))
	s, _ := graphSource("tempo", srv.URL)
	s.live = live
	return s
}

func TestReceiverOffWhileServiceGraphComplete(t *testing.T) {
	r, live, _ := newTestReceiver(t)
	_, graphRow := collectGraph(t, graphWithReceiver(t, live, edge("web", "payments", "3")))
	if !strings.Contains(graphRow.Evidence, "won: the OTLP receiver is off") {
		t.Fatalf("winner row = %+v", graphRow)
	}
	res, c := collector(false)
	s := NewReceiverSource("otlp-receiver", res, c, live)
	collectReceiver(t, s)
	w := post(r, spanBody(map[string][]string{"web": {"payments"}}))
	var answer struct {
		PartialSuccess struct{ RejectedSpans, ErrorMessage string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil || answer.PartialSuccess.RejectedSpans != "1" ||
		answer.PartialSuccess.ErrorMessage != "pacto reads edges from Service graph in Prometheus" {
		t.Fatalf("answer %s %v", w.Body, err)
	}
	col := collectReceiver(t, s)
	if len(col.Observed) != 0 || !strings.Contains(reason(col.Providers[0]), "off: Service graph in Prometheus reports complete coverage") || col.Providers[0].ID != "otel-collector" {
		t.Fatalf("collection = %+v", col)
	}
}

func TestReceiverStaysOnWhenWinnerHasSeveralCandidates(t *testing.T) {
	r, live, _ := newTestReceiver(t)
	srv, _ := jaegerServer(t, []any{link("web", "payments", 3)})
	js, _ := jaegerSource(srv.URL, 2)
	js.live = live
	_, row := collectJaeger(t, js)
	if row.Status != fleet.SourcePartial || strings.Contains(row.Evidence, "won:") {
		t.Fatalf("jaeger row = %+v", row)
	}
	post(r, spanBody(map[string][]string{"web": {"payments"}}))
	col := collectReceiver(t, NewReceiverSource("otlp-receiver", nil, nil, live))
	if len(col.Observed) != 1 {
		t.Fatalf("receiver = %+v", col)
	}
}

func TestReceiverBackOnWhenWinnerMissesPairs(t *testing.T) {
	r, live, _ := newTestReceiver(t)
	post(r, spanBody(map[string][]string{"web": {"payments", "audit"}}))
	_, graphRow := collectGraph(t, graphWithReceiver(t, live, edge("web", "payments", "3")))
	col := collectReceiver(t, NewReceiverSource("otlp-receiver", nil, nil, live))
	want := "Service graph in Prometheus misses 1 calls the receiver saw"
	if len(col.Observed) != 2 || !strings.Contains(rowText(col.Providers[0]), want) || !strings.Contains(rowText(graphRow), want) || strings.Contains(graphRow.Evidence, "won:") {
		t.Fatalf("receiver %+v, winner %+v", col, graphRow)
	}
	if w := post(r, "{}"); strings.TrimSpace(w.Body.String()) != "{}" {
		t.Fatalf("back on answer %s", w.Body)
	}
}

func TestReceiverBackOnAfterWinnerWindow(t *testing.T) {
	r, live, clk := newTestReceiver(t)
	live.markComplete(jaegerName, nil)
	s := NewReceiverSource("otlp-receiver", nil, nil, live)
	collectReceiver(t, s)
	if r.offBy != jaegerName {
		t.Fatalf("off by %q", r.offBy)
	}
	clk.t = clk.t.Add(61 * time.Second)
	post(r, spanBody(map[string][]string{"web": {"payments"}}))
	if col := collectReceiver(t, s); len(col.Observed) != 1 || r.offBy != "" {
		t.Fatalf("after the window %+v off %q", col, r.offBy)
	}
}

func TestCollectorRowNotExportingHasSnippet(t *testing.T) {
	_, live, _ := newTestReceiver(t)
	res, c := collector(false)
	col := collectReceiver(t, NewReceiverSource("otlp-receiver", res, c, live))
	row := col.Providers[0]
	if row.Status != fleet.SourcePartial || !strings.Contains(reason(row), "not exporting to pacto-dashboard.pacto-system:4318") ||
		strings.Contains(row.Hint, "\n") || !strings.Contains(row.Hint, "otlphttp/pacto") || !strings.Contains(row.Hint, "encoding: json") ||
		!strings.Contains(rowText(row), "add otlphttp/pacto to service.pipelines.traces.exporters") {
		t.Fatalf("row = %+v", row)
	}
}

func TestCollectorRowOffersServiceGraphFirst(t *testing.T) {
	_, live, _ := newTestReceiver(t)
	res, c := collector(true)
	row := collectReceiver(t, NewReceiverSource("otlp-receiver", res, c, live)).Providers[0]
	// Additive: no list a merge would put in place of the Collector's own
	// traces exporters.
	if !strings.HasPrefix(row.Hint, "connectors: {servicegraph: {}}") || strings.Contains(row.Hint, "\n") || strings.Contains(row.Hint, "traces:") ||
		!strings.Contains(rowText(row), "add servicegraph to service.pipelines.traces.exporters") || !strings.Contains(rowText(row), "otlphttp/pacto") {
		t.Fatalf("row = %+v", row)
	}
}

func TestCollectorRowExporting(t *testing.T) {
	r, live, _ := newTestReceiver(t)
	post(r, spanBody(map[string][]string{"web": {"payments"}}))
	send(r, http.MethodPost, receiverPath, "application/json", spanBody(map[string][]string{"web": {"payments"}}), "10.0.0.9:7")
	res, c := collector(false)
	row := collectReceiver(t, NewReceiverSource("otlp-receiver", res, c, live)).Providers[0]
	text := rowText(row)
	if row.Status != fleet.SourceAvailable || !strings.Contains(text, "received 2 calls from 2 in-cluster senders in the last 15 minutes") || strings.Contains(text, "10.0.0") {
		t.Fatalf("row = %+v", row)
	}
}

func TestReceiverRowAbsentUntilARequest(t *testing.T) {
	r, live, _ := newTestReceiver(t)
	s := NewReceiverSource("otlp-receiver", nil, nil, live)
	if col := collectReceiver(t, s); len(col.Providers) != 0 || col.State.Status != fleet.SourcePartial {
		t.Fatalf("before a request: %+v", col)
	}
	// A request with no call (a probe, an empty export) makes the row
	// appear partial, with the hint; a call makes it available.
	for _, body := range []string{`{"resourceSpans":[]}`, `{}`, `null`} {
		post(r, body)
		col := collectReceiver(t, s)
		if len(col.Providers) != 1 || col.Providers[0].ID != "otlp-receiver" || col.Providers[0].Name != "OTLP receiver" || col.Providers[0].Status != fleet.SourcePartial ||
			col.State.Status != fleet.SourcePartial || col.Providers[0].Hint == "" || !strings.Contains(rowText(col.Providers[0]), "held no call between named services") ||
			strings.Contains(rowText(col.Providers[0]), "detected ") {
			t.Fatalf("after %s: %+v", body, col.Providers)
		}
	}
	post(r, spanBody(map[string][]string{"web": {"payments"}}))
	if col := collectReceiver(t, s); col.Providers[0].Status != fleet.SourceAvailable || col.Providers[0].Hint != "" {
		t.Fatalf("after a call: %+v", col.Providers)
	}
}

// gzip is body compressed with gzip.
func gzipped(t *testing.T, body string) string {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	if _, err := zw.Write([]byte(body)); err != nil || zw.Close() != nil {
		t.Fatal(err)
	}
	return b.String()
}

// sendEncoded posts body as JSON with Content-Encoding enc.
func sendEncoded(r *Receiver, enc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, receiverPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", enc)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// The Collector's otlphttp exporter compresses with gzip by default: the
// receiver reads it, bounded once decompressed too, and refuses and counts
// any other encoding.
func TestReceiverReadsGzip(t *testing.T) {
	r, live, _ := newTestReceiver(t)
	if w := sendEncoded(r, "gzip", gzipped(t, spanBody(map[string][]string{"web": {"payments"}}))); w.Code != 200 {
		t.Fatalf("gzip = %d %s", w.Code, w.Body)
	}
	if w := sendEncoded(r, "gzip", "not gzip"); w.Code != 400 {
		t.Fatalf("broken gzip = %d", w.Code)
	}
	bomb := gzipped(t, `{"resourceSpans":[`+strings.Repeat(" ", maxReceiverBody)+`]}`)
	if w := sendEncoded(r, "gzip", bomb); w.Code != 413 {
		t.Fatalf("gzip over 4 MiB decompressed = %d", w.Code)
	}
	if w := sendEncoded(r, "zstd", "x"); w.Code != 415 {
		t.Fatalf("zstd = %d", w.Code)
	}
	col := collectReceiver(t, NewReceiverSource("otlp-receiver", nil, nil, live))
	if len(col.Observed) != 1 || !strings.Contains(reason(col.Providers[0]), "1 requests used a compression other than gzip") {
		t.Fatalf("collection = %+v", col)
	}
}

// A refused request counts in its own window: the count restarts once the
// last one is older than 15 minutes, even with nothing read in between.
func TestReceiverRefusedCountRestarts(t *testing.T) {
	r, _, clk := newTestReceiver(t)
	for range 3 {
		send(r, http.MethodPost, receiverPath, "application/x-protobuf", "x", "10.0.0.1:1")
	}
	clk.t = clk.t.Add(time.Hour)
	send(r, http.MethodPost, receiverPath, "application/x-protobuf", "x", "10.0.0.1:1")
	if r.protobuf.n != 1 {
		t.Fatalf("protobuf = %d, want 1", r.protobuf.n)
	}
}

// The receiver names a callee by rpc.service or host where the winner names
// the server's service.name: those are the winner's calls, not misses. A
// callee the winner never saw under a plain name, or a caller it never saw,
// still is.
func TestReceiverCalleeAliasIsNoMiss(t *testing.T) {
	r, live, _ := newTestReceiver(t)
	post(r, spanBody(map[string][]string{"web": {"payments.v1.PaymentService", "payments.prod.svc.cluster.local:8080"}}))
	_, graphRow := collectGraph(t, graphWithReceiver(t, live, edge("web", "payments", "3")))
	col := collectReceiver(t, NewReceiverSource("otlp-receiver", nil, nil, live))
	if len(col.Observed) != 0 || !strings.Contains(graphRow.Evidence, "won: the OTLP receiver is off") || strings.Contains(rowText(graphRow), "misses") {
		t.Fatalf("receiver %+v, winner %+v", col, graphRow)
	}
	post(r, spanBody(map[string][]string{"cart": {"payments.v1.PaymentService"}, "web": {"payments.v2.Refunds", "ledger"}}))
	if _, missing := live.verdict(); missing != 2 {
		t.Fatalf("missing = %d, want the unknown caller's call and the plain callee", missing)
	}
}

// Only the latest read counts: a source that read complete and then failed
// no longer wins, and the receiver turns back on.
func TestFailedReadNoLongerWins(t *testing.T) {
	r, live, _ := newTestReceiver(t)
	post(r, spanBody(map[string][]string{"web": {"payments"}}))
	collectGraph(t, graphWithReceiver(t, live, edge("web", "payments", "3")))
	if name, _ := live.verdict(); name != serviceGraphName {
		t.Fatalf("winner %q", name)
	}
	srv, _ := graphServer(t, 500, "")
	failed, _ := graphSource("tempo", srv.URL)
	failed.live = live
	collectGraph(t, failed)
	if name, _ := live.verdict(); name != "" {
		t.Fatalf("a failed source still wins: %q", name)
	}
	if col := collectReceiver(t, NewReceiverSource("otlp-receiver", nil, nil, live)); len(col.Observed) != 1 || r.offBy != "" {
		t.Fatalf("receiver %+v off %q", col, r.offBy)
	}
}
