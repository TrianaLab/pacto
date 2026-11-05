package fleetsrc

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func miraNodes(names map[string]string) []miraNode {
	var nodes []miraNode
	for k, n := range names {
		nodes = append(nodes, miraNode{Key: k, Name: n})
	}
	return nodes
}

func TestNormalizeMiraMap(t *testing.T) {
	nodes := miraNodes(map[string]string{
		"1": "payments", "2": "payments", "3": "orders", "4": "unknown", "5": "audit",
		"6": "k8s:secret/Deployment/ledger", "7": "evil\x1b]8;;http://x\x07", "8": strings.Repeat("a", 254), "9": "",
	})
	cases := []struct {
		name         string
		edges        []miraEdge
		want         []fleet.ObservedEdge
		unattributed int
	}{
		{
			name: "instances of one service fold into one edge",
			edges: []miraEdge{
				{From: "3", To: "1", Calls: 3},
				{From: "3", To: "2", Calls: 2},
			},
			want: []fleet.ObservedEdge{{From: "orders", To: "payments", Count: 5}},
		},
		{
			name: "entry and same-name pairs are dropped without counting",
			edges: []miraEdge{
				{From: "entry", To: "3", Calls: 7},
				{From: "1", To: "2", Calls: 1},
			},
		},
		{
			name: "unknown names and missing keys are dropped and counted",
			edges: []miraEdge{
				{From: "4", To: "1", Calls: 1},
				{From: "99", To: "1", Calls: 1},
				{From: "3", To: "4", Calls: 1},
				{From: "3", To: "99", Calls: 1},
				{From: "3", To: "1", Calls: 1},
			},
			want:         []fleet.ObservedEdge{{From: "orders", To: "payments", Count: 1}},
			unattributed: 4,
		},
		{
			name: "names Mira cannot vouch for are dropped and counted",
			edges: []miraEdge{
				{From: "6", To: "1", Calls: 1},
				{From: "7", To: "1", Calls: 1},
				{From: "8", To: "1", Calls: 1},
				{From: "9", To: "1", Calls: 1},
				{From: "3", To: "6", Calls: 1},
			},
			unattributed: 5,
		},
		{
			name: "calls that count nothing are dropped and counted, sums saturate",
			edges: []miraEdge{
				{From: "3", To: "5", Calls: 0},
				{From: "3", To: "5", Calls: -50},
				{From: "3", To: "1", Calls: math.MaxInt - 1},
				{From: "3", To: "2", Calls: math.MaxInt - 1},
			},
			want:         []fleet.ObservedEdge{{From: "orders", To: "payments", Count: math.MaxInt}},
			unattributed: 2,
		},
		{
			name: "output is sorted by caller then callee",
			edges: []miraEdge{
				{From: "3", To: "5", Calls: 1},
				{From: "5", To: "1", Calls: 1},
				{From: "3", To: "1", Calls: 1},
			},
			want: []fleet.ObservedEdge{
				{From: "audit", To: "payments", Count: 1},
				{From: "orders", To: "audit", Count: 1},
				{From: "orders", To: "payments", Count: 1},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m miraMap
			m.Map.Nodes, m.Map.Edges = nodes, tc.edges
			got, unattributed := normalizeMiraMap(m)
			if !reflect.DeepEqual(got, tc.want) || unattributed != tc.unattributed {
				t.Fatalf("got %+v unattributed=%d, want %+v unattributed=%d", got, unattributed, tc.want, tc.unattributed)
			}
		})
	}
}

func TestMiraSourceIDKind(t *testing.T) {
	if s := NewMiraSource("", ""); s.ID() != "mira" || s.Kind() != fleet.SourceKindMira {
		t.Fatalf("defaults: id=%q kind=%q", s.ID(), s.Kind())
	}
	if NewMiraSource("mira-2", "").ID() != "mira-2" {
		t.Fatal("custom id not honored")
	}
}

const miraCleanMap = `{"map":{"nodes":[{"key":"1","name":"payments","spans":4,"errors":0,"avg_nano":"10"},{"key":"2","name":"orders","spans":4}],"edges":[{"from":"2","to":"1","calls":4,"errors":0},{"from":"entry","to":"2","calls":4}],"unresolved":0,"extra":true},"stats":{"blocks_total":1,"blocks_scanned":1,"rows_scanned":8,"rows_matched":8,"elapsed_us":5},"version":"0.9"}`

func TestMiraSourceCollectPostsTheWindow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/map" || r.Header.Get("Content-Type") != "application/json" ||
			string(body) != `{"from":"-15m","to":"now","max_spans":200000}` {
			t.Errorf("request = %s %s %q %s", r.Method, r.URL.Path, r.Header.Get("Content-Type"), body)
		}
		_, _ = w.Write([]byte(miraCleanMap))
	}))
	defer srv.Close()

	col, err := NewMiraSource("mira", srv.URL+"/").Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []fleet.ObservedEdge{{From: "orders", To: "payments", Count: 4}}
	if !reflect.DeepEqual(col.Observed, want) || !col.ObservedUndeclaredOnly || len(col.Limitations) != 0 || col.State != nil {
		t.Fatalf("collection = %+v", col)
	}
}

func TestMiraSourceCollectPartial(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"truncated", `{"map":{"nodes":[{"key":"1","name":"payments"}],"edges":[]},"stats":{"rows_scanned":200000}}`, "200000"},
		{"unresolved", `{"map":{"nodes":[{"key":"1","name":"payments"}],"edges":[],"unresolved":4},"stats":{}}`, "4 "},
		{"missing key", `{"map":{"nodes":[{"key":"1","name":"payments"}],"edges":[{"from":"9","to":"1","calls":1}]},"stats":{}}`, "1 "},
		{"unknown name", `{"map":{"nodes":[{"key":"1","name":"payments"},{"key":"2","name":"unknown"}],"edges":[{"from":"2","to":"1","calls":1}]},"stats":{}}`, "1 "},
		{"empty", `{"map":{"nodes":[],"edges":[]},"stats":{"rows_scanned":12}}`, "no spans in the last 15 minutes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := serveJSON(t, tc.body)
			defer srv.Close()
			src := NewMiraSource("mira-1", srv.URL)
			col, err := src.Collect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(col.Limitations) != 1 {
				t.Fatalf("limitations = %+v, want exactly one", col.Limitations)
			}
			l := col.Limitations[0]
			if l.Code != fleet.LimitationSourcePartial || l.Source != "mira-1" || !strings.Contains(l.Message, tc.want) {
				t.Fatalf("limitation = %+v, want partial from mira-1 mentioning %q", l, tc.want)
			}
			for _, leak := range []string{"payments", "unknown", srv.URL, "127.0.0.1"} {
				if strings.Contains(l.Message, leak) {
					t.Fatalf("limitation message %q leaks %q", l.Message, leak)
				}
			}
			if st := buildOneSourceState(t, src); st.Status != fleet.SourcePartial {
				t.Fatalf("source state = %q, want partial", st.Status)
			}
		})
	}
}

func TestMiraSourceCollectErrors(t *testing.T) {
	huge := `{"map":{"nodes":[{"key":"1","name":"` + strings.Repeat("a", maxMiraBodyBytes+1) + `"}]}}`
	status := func(code int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(body))
		}
	}
	cases := []struct {
		name    string
		handler http.HandlerFunc
		closed  bool
	}{
		{name: "500 with a secret", handler: status(http.StatusInternalServerError, "secret-token")},
		{name: "501 behind mira proxy", handler: status(http.StatusNotImplemented, "secret-token")},
		{name: "400", handler: status(http.StatusBadRequest, "unknown field secret-token")},
		{name: "invalid JSON", handler: status(http.StatusOK, "{secret-token")},
		{name: "oversize value", handler: status(http.StatusOK, huge)},
		{name: "connection refused", handler: status(http.StatusOK, miraCleanMap), closed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			if tc.closed {
				srv.Close()
			}
			src := NewMiraSource("mira", srv.URL)
			if _, err := src.Collect(context.Background()); err == nil {
				t.Fatal("Collect succeeded, want an error")
			}
			snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, src)
			if err != nil {
				t.Fatal(err)
			}
			if snap.Sources[0].Status != fleet.SourceUnavailable || !hasLimitationCode(snap.Limitations, fleet.LimitationSourceUnavailable) {
				t.Fatalf("sources=%+v limitations=%+v, want unavailable", snap.Sources, snap.Limitations)
			}
			out, _ := json.Marshal(snap)
			for _, leak := range []string{"secret-token", srv.URL} {
				if strings.Contains(string(out), leak) {
					t.Fatalf("snapshot leaks %q", leak)
				}
			}
		})
	}
}

func TestMiraSourceCollectBadURL(t *testing.T) {
	if _, err := NewMiraSource("mira", "http://\x7f").Collect(context.Background()); err == nil {
		t.Fatal("expected a request-construction error")
	}
}

func TestMiraSourceRefusesRedirects(t *testing.T) {
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = w.Write([]byte(miraCleanMap))
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/v1/map", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	if _, err := NewMiraSource("mira", srv.URL).Collect(context.Background()); err == nil || hit {
		t.Fatalf("err=%v hit=%v, want an error and no request to the redirect target", err, hit)
	}
}

func TestMiraSourceTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	src := NewMiraSource("mira", srv.URL)
	src.client.Timeout = 50 * time.Millisecond
	if _, err := src.Collect(context.Background()); err == nil {
		t.Fatal("expected a timeout error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewMiraSource("mira", srv.URL).Collect(ctx); err == nil {
		t.Fatal("expected a cancellation error")
	}
}

func TestCheckMiraURL(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://mira.example", true},
		{"http://127.0.0.1:4318", true},
		{"http://mira:4318", true},
		{"https://user:pw@mira.example", false},
		{"ftp://x", false},
		{"", false},
		{"https://", false},
		{"%zz", false},
		{"http://mira:4318/?token=x", false},
		{"http://mira:4318/#frag", false},
		{"http://mira:4318/#", false},
		{"http://mira:4318/base/", true},
	}
	for _, tc := range cases {
		err := CheckMiraURL(tc.url)
		if (err == nil) != tc.ok {
			t.Errorf("CheckMiraURL(%q) = %v, want ok=%v", tc.url, err, tc.ok)
		}
		if err != nil && (strings.Contains(err.Error(), "pw") || strings.Contains(err.Error(), "user")) {
			t.Errorf("CheckMiraURL(%q) error %q leaks userinfo", tc.url, err)
		}
	}
}
