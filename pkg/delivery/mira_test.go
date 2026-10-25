package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeMira answers /api/v1/query like Mira: `rows` and a `next` cursor while
// more rows remain. spans says how many rows each query document matches.
type fakeMira struct {
	mu      sync.Mutex
	spans   func(q map[string]any) int
	status  int
	body    string // overrides the answer when set
	queries []map[string]any
}

func (f *fakeMira) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path != "/api/v1/query" {
		http.NotFound(w, r)
		return
	}
	var q map[string]any
	b, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(b, &q)
	f.queries = append(f.queries, q)
	if f.status != 0 {
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, `{"error":"boom"}`)
		return
	}
	if f.body != "" {
		_, _ = io.WriteString(w, f.body)
		return
	}
	total, limit, offset := f.spans(q), int(q["limit"].(float64)), 0
	if a, ok := q["after"].(string); ok {
		_, _ = fmt.Sscanf(a, "c%d", &offset)
	}
	n := min(limit, max(total-offset, 0))
	rows := make([]string, n)
	for i := range rows {
		rows[i] = `{"name":"GET /pay"}`
	}
	next := ""
	if offset+n < total {
		next = fmt.Sprintf(`,"next":"c%d"`, offset+n)
	}
	_, _ = fmt.Fprintf(w, `{"rows":[%s],"stats":{"rows_matched":%d}%s}`, strings.Join(rows, ","), n, next)
}

// hasTerm reports whether q filters on field with op.
func hasTerm(q map[string]any, key, op string) bool {
	for _, t := range q["where"].([]any) {
		m := t.(map[string]any)
		if m["field"] == key && m[op] != nil {
			return true
		}
	}
	return false
}

// shop is a service with total spans, errs failing and slow over the threshold.
func shop(total, errs, slow int) func(map[string]any) int {
	return func(q map[string]any) int {
		switch {
		case hasTerm(q, "status_code", "eq"):
			return errs
		case hasTerm(q, "duration_nano", "gt"):
			return slow
		}
		return total
	}
}

func miraCheck(t *testing.T, f *fakeMira, in GateInput, params map[string]string) Verdict {
	t.Helper()
	srv := httptest.NewServer(f)
	defer srv.Close()
	return Mira{URL: srv.URL + "/", Client: srv.Client()}.Check(context.Background(), in, params)
}

func TestMiraVerdicts(t *testing.T) {
	soaked := gateIn() // ten minutes
	early := gateIn()
	early.Window.To = early.Window.From.Add(20 * time.Second)
	lat := map[string]string{"latencyThreshold": "500ms", "maxLatencyRatio": "0.1"}
	cases := []struct {
		name   string
		spans  func(map[string]any) int
		in     GateInput
		params map[string]string
		want   Result
		msg    string
	}{
		{"healthy", shop(100, 1, 0), soaked, nil, Pass, "1 of 100 spans failed"},
		{"errors", shop(100, 10, 0), soaked, nil, Fail, "10 of 100 spans failed (10.00%), above the 5.00% limit"},
		{"too few spans", shop(19, 0, 0), soaked, nil, Unknown, "only 19 spans"},
		{"custom floor", shop(5, 0, 0), soaked, map[string]string{"minSpans": "5"}, Pass, "0 of 5"},
		{"custom limit", shop(100, 10, 0), soaked, map[string]string{"maxErrorRatio": "0.2"}, Pass, "10 of 100"},
		{"fail before soak", shop(100, 50, 0), early, nil, Fail, "above the 5.00% limit"},
		{"pass waits for soak", shop(100, 0, 0), early, nil, Unknown, "soaking"},
		{"slow", shop(100, 0, 30), soaked, lat, Fail, "30 of 100 spans took longer than 500ms"},
		{"fast enough", shop(100, 0, 5), soaked, lat, Pass, "5 of 100 spans took longer than 500ms"},
		{"many pages", shop(25000, 100, 0), soaked, nil, Pass, "100 of 25000"},
	}
	for _, c := range cases {
		v := miraCheck(t, &fakeMira{spans: c.spans}, c.in, c.params)
		if v.Result != c.want || !strings.Contains(v.Message, c.msg) {
			t.Errorf("%s: got %s %q, want %s containing %q", c.name, v.Result, v.Message, c.want, c.msg)
		}
	}
}

func TestMiraCitationReruns(t *testing.T) {
	f := &fakeMira{spans: shop(100, 10, 0)}
	in := gateIn()
	v := miraCheck(t, f, in, map[string]string{"where": `[{"attr":"k8s.namespace.name","eq":"prod"}]`})
	var q map[string]any
	if err := json.Unmarshal([]byte(v.Citation["query"]), &q); err != nil {
		t.Fatal(err)
	}
	// Absolute bounds, Mira's traces signal and the service filter: re-running
	// this document reads exactly the same spans.
	if q["signal"] != "traces" || int64(q["from"].(float64)) != in.Window.From.UnixNano() || int64(q["to"].(float64)) != in.Window.To.UnixNano() {
		t.Errorf("query bounds: %s", v.Citation["query"])
	}
	where := v.Citation["query"]
	if !strings.Contains(where, `{"attr":"service.name","eq":"payments"}`) || !strings.Contains(where, `{"attr":"k8s.namespace.name","eq":"prod"}`) {
		t.Errorf("query filter: %s", where)
	}
	if !strings.Contains(v.Citation["errorQuery"], `{"eq":2,"field":"status_code"}`) || v.Citation["from"] != "2026-10-20T10:00:00Z" ||
		v.Citation["spans"] != "100" || v.Citation["errors"] != "10" || v.Citation["maxErrorRatio"] != "0.05" || v.Citation["minSpans"] != "20" {
		t.Errorf("citation: %v", v.Citation)
	}
	// Every request pages with the largest limit Mira serves: count-only
	// (limit 0) is refused by Mira's query API.
	for _, q := range f.queries {
		if q["limit"].(float64) != miraPageSize {
			t.Errorf("limit %v", q["limit"])
		}
	}
}

// failAfter serves ok answers of 30 spans, then fails every request with status.
func failAfter(ok, status int) *httptest.Server {
	calls := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls++; calls > ok {
			w.WriteHeader(status)
			return
		}
		_, _ = io.WriteString(w, `{"rows":[`+strings.TrimSuffix(strings.Repeat(`{},`, 30), ",")+`]}`)
	}))
}

func TestMiraFailedReadsAreUnknown(t *testing.T) {
	cases := map[string]*fakeMira{
		"server error": {status: 500},
		"not json":     {body: "<html>"},
		"no rows key":  {body: `{"error":"x"}`},
		"too many":     {spans: shop(miraPageSize*miraMaxPages+1, 0, 0)},
	}
	for name, f := range cases {
		if v := miraCheck(t, f, gateIn(), nil); v.Result != Unknown {
			t.Errorf("%s: %+v", name, v)
		}
	}
	if v := (Mira{URL: "http://127.0.0.1:1", Client: http.DefaultClient}).Check(context.Background(), gateIn(), nil); v.Result != Unknown {
		t.Errorf("unreachable: %+v", v)
	}
	// A failed error count or latency count is Unknown too, never Pass.
	for name, c := range map[string]struct {
		ok     int
		params map[string]string
	}{
		"errors":  {1, nil},
		"latency": {2, map[string]string{"latencyThreshold": "1s", "maxErrorRatio": "1"}},
	} {
		srv := failAfter(c.ok, 503)
		v := Mira{URL: srv.URL, Client: srv.Client()}.Check(context.Background(), gateIn(), c.params)
		srv.Close()
		if v.Result != Unknown || !strings.Contains(v.Message, "503") {
			t.Errorf("%s read: %+v", name, v)
		}
	}
}

// TestMiraCountsOneSpanKind: the gate counts server spans by default, a
// request each, so internal and client spans do not fill the paging ceiling.
func TestMiraCountsOneSpanKind(t *testing.T) {
	for kind, want := range map[string]string{"": `{"eq":2,"field":"kind"}`, "consumer": `{"eq":5,"field":"kind"}`, "any": ""} {
		p := map[string]string{}
		if kind != "" {
			p["spanKind"] = kind
		}
		v := miraCheck(t, &fakeMira{spans: shop(100, 0, 0)}, gateIn(), p)
		if got := v.Citation["query"]; v.Result != Pass || want != "" && !strings.Contains(got, want) || want == "" && strings.Contains(got, `"kind"`) {
			t.Errorf("spanKind %q: %s %s", kind, v.Result, got)
		}
	}
}

// TestMiraJudgesTheNewestSoakPastTheCap: a window with more spans than the
// gate pages through is judged on its newest soak, so a busy service still
// gets a verdict, and the citation names the window it judged.
func TestMiraJudgesTheNewestSoakPastTheCap(t *testing.T) {
	in := gateIn() // ten minutes
	busy := func(errs int) func(map[string]any) int {
		return func(q map[string]any) int {
			if int64(q["from"].(float64)) == in.Window.From.UnixNano() {
				return miraPageSize*miraMaxPages + 1
			}
			return shop(1000, errs, 0)(q)
		}
	}
	for errs, want := range map[int]Result{10: Pass, 500: Fail} {
		v := miraCheck(t, &fakeMira{spans: busy(errs)}, in, map[string]string{"soak": "2m"})
		if v.Result != want || !strings.Contains(v.Message, "judged on the newest 2m0s") || v.Citation["from"] != ts3339(in.Window.To.Add(-2*time.Minute)) ||
			!strings.Contains(v.Citation["query"], fmt.Sprint(in.Window.To.Add(-2*time.Minute).UnixNano())) {
			t.Errorf("%d errors: %s %q %v", errs, v.Result, v.Message, v.Citation)
		}
	}
	// A window no longer than the soak has nothing newer to judge.
	short := gateIn()
	short.Window.To = short.Window.From.Add(time.Minute)
	if v := miraCheck(t, &fakeMira{spans: busy(0)}, short, nil); v.Result != Unknown || !strings.Contains(v.Message, "more than 200000 spans") {
		t.Errorf("short window: %+v", v)
	}
}

// TestMiraMatchesTheVersion: with matchVersion the gate counts only the
// spans of the version under check, so old pods still serving neither hide a
// breach nor fail a clean version; without a contract version it is Unknown.
func TestMiraMatchesTheVersion(t *testing.T) {
	in := gateIn()
	in.Version = "1.4.0"
	v := miraCheck(t, &fakeMira{spans: shop(100, 0, 0)}, in, map[string]string{"matchVersion": "true"})
	if v.Result != Pass || !strings.Contains(v.Citation["query"], `{"attr":"service.version","eq":"1.4.0"}`) {
		t.Fatalf("matched: %s %s", v.Result, v.Citation["query"])
	}
	if v := miraCheck(t, &fakeMira{spans: shop(100, 0, 0)}, in, map[string]string{"matchVersion": "false"}); strings.Contains(v.Citation["query"], "service.version") {
		t.Fatalf("not asked to match: %s", v.Citation["query"])
	}
	in.Version = ""
	if v := miraCheck(t, &fakeMira{spans: shop(100, 0, 0)}, in, map[string]string{"matchVersion": "true"}); v.Result != Unknown || !strings.Contains(v.Message, "no readable contract version") {
		t.Fatalf("no version: %+v", v)
	}
}

func TestMiraParams(t *testing.T) {
	for name, p := range map[string]map[string]string{
		"minSpans":            {"minSpans": "-1"},
		"maxErrorRatio":       {"maxErrorRatio": "2"},
		"maxErrorRatio NaN":   {"maxErrorRatio": "NaN"},
		"maxLatencyRatio NaN": {"latencyThreshold": "1s", "maxLatencyRatio": "nan"},
		"soak":                {"soak": "soon"},
		"latencyThreshold":    {"latencyThreshold": "fast"},
		"maxLatencyRatio":     {"latencyThreshold": "1s", "maxLatencyRatio": "x"},
		"where":               {"where": "{"},
		"spanKind":            {"spanKind": "servers"},
		"matchVersion":        {"matchVersion": "yes please"},
		"unknown":             {"minspans": "3"},
	} {
		v := miraCheck(t, &fakeMira{spans: shop(100, 0, 0)}, gateIn(), p)
		if v.Result != Unknown || !strings.Contains(v.Message, strings.Fields(name)[0]) {
			t.Errorf("%s: %+v", name, v)
		}
	}
}
