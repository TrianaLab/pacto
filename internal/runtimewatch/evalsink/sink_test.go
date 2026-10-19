/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package evalsink

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/probe"
	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

var now = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// procFS is a map-backed /proc for pid selectors.
type procFS struct{ fstest.MapFS }

func (p procFS) Readlink(string) (string, error)         { return "", fs.ErrNotExist }
func (p procFS) Open(name string) (io.ReadCloser, error) { return p.MapFS.Open(name) }

func svcContract() *contract.Contract {
	httpBinding := func(path string) *contract.CapabilityBinding {
		return &contract.CapabilityBinding{Type: contract.CapabilityBindingHTTP, Interface: "api", Path: path}
	}
	return &contract.Contract{
		PactoVersion: "1.1",
		Service:      contract.Service{Name: "svc", Version: "1.0.0"},
		Interfaces:   []contract.Interface{{Name: "api", Type: contract.InterfaceTypeOpenAPI, Ref: "x"}, {Name: "admin", Type: contract.InterfaceTypeOpenAPI, Ref: "x"}},
		Dependencies: []contract.Dependency{{Name: "db", Ref: "oci://r/db", Required: true}, {Name: "cache", Ref: "oci://r/cache"}, {Name: "queue", Ref: "oci://r/q"}},
		Capabilities: []contract.Capability{
			{Type: contract.CapabilityHealth, Binding: httpBinding("/healthz")},
			{Type: contract.CapabilityMetrics, Binding: httpBinding("")},
			{Type: "acme.dev/custom", Ref: "x"},
		},
	}
}

// server answers /healthz with *health and /metrics with Prometheus text, and returns its port.
func server(t *testing.T, health *int) int32 {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "up 1\n")
			return
		}
		w.WriteHeader(*health)
	}))
	t.Cleanup(srv.Close)
	_, p, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(p)
	return int32(port)
}

func unitSnapshot(port int32, listening bool) *runtimefacts.Snapshot {
	w := runtimefacts.Workload{
		Identity:   runtimefacts.Identity{Kind: runtimefacts.KindUnit, ID: "svc.service", Name: "svc.service"},
		BaselineAt: now.Add(-time.Minute),
		Connections: []runtimefacts.Connection{
			{Address: "127.0.0.1", Port: 5432, Successes: 1, LastSuccessAt: now.Add(-time.Second)},
			{Address: "10.0.0.9", Port: 6379, Failures: 1, LastFailureAt: now.Add(-time.Second)},
		},
	}
	if listening {
		w.Listeners = []runtimefacts.Listener{{Port: port, Family: "IPv4", Address: "0.0.0.0"}}
	}
	return &runtimefacts.Snapshot{PublishIntervalSeconds: 30, LastPublished: now,
		Collection: runtimefacts.Collection{State: runtimefacts.StateRunning}, Workloads: []runtimefacts.Workload{w}}
}

func newSink(t *testing.T, targets []Target, emitted *[]Result) *Sink {
	t.Helper()
	return &Sink{
		Sources: []func(context.Context) ([]Target, error){func(context.Context) ([]Target, error) { return targets, nil }},
		Load: func(context.Context, string) (*contract.Bundle, error) {
			return &contract.Bundle{Contract: svcContract()}, nil
		},
		Emit: func(_ context.Context, rs []Result) error {
			*emitted = rs
			return nil
		},
		Prober: probe.New(time.Second),
		Window: time.Minute,
		Now:    func() time.Time { return now },
		Resolve: func(_ context.Context, host string) ([]string, error) {
			if host == "cache.local" {
				return []string{"10.0.0.9"}, nil
			}
			return []string{host}, nil
		},
	}
}

// outcomes maps each observation's subject name to its outcome and payload.
func outcomes(t *testing.T, r Result) map[string]evidence.Observation {
	t.Helper()
	if errs := evidence.ValidateEvidenceSet(r.Evidence); len(errs) > 0 {
		t.Fatalf("invalid evidence set: %v", errs)
	}
	out := map[string]evidence.Observation{}
	for _, o := range r.Evidence.Observations {
		out[o.Subject.Name] = o
	}
	return out
}

func TestEvaluate_Compliant(t *testing.T) {
	health := 200
	port := server(t, &health)
	target := Target{Name: "unit/svc.service", Contract: "./svc", Match: Selector{Unit: "svc.service"}, Host: "127.0.0.1",
		Interfaces: map[string]int32{"api": port}, Dependencies: map[string]string{"db": "127.0.0.1:5432", "cache": "cache.local:6379"}}
	var got []Result
	s := newSink(t, []Target{target}, &got)
	s.Protocol = true
	s.Evaluate(context.Background(), unitSnapshot(port, true))
	if len(got) != 1 {
		t.Fatalf("results = %+v", got)
	}
	obs := outcomes(t, got[0])
	api, err := obs["api"].GetInterfaceObservation()
	if err != nil || !api.Present || api.Protocol != evidence.ProtocolHTTP1 || obs["api"].Provenance.Collector != CollectorKernel {
		t.Fatalf("api = %+v, %v, %+v", api, err, obs["api"])
	}
	if obs["admin"].Outcome != evidence.Unsupported || obs["queue"].Outcome != evidence.Unsupported {
		t.Fatalf("unbound: admin %s, queue %s", obs["admin"].Outcome, obs["queue"].Outcome)
	}
	checkCompliantRest(t, obs)
	if got[0].Status != finding.StatusUnknown || got[0].Evidence.Subject.Name != "unit/svc.service" || got[0].Evidence.Source != Source {
		t.Fatalf("result = %+v", got[0])
	}
}

// checkCompliantRest checks the dependencies and capabilities of TestEvaluate_Compliant.
func checkCompliantRest(t *testing.T, obs map[string]evidence.Observation) {
	t.Helper()
	if db, _ := obs["db"].GetDependencyObservation(); !db.Reachable {
		t.Fatalf("db = %+v", obs["db"])
	}
	// The cache only failed: a negative, held back by the window.
	if obs["cache"].Outcome != evidence.Insufficient {
		t.Fatalf("cache = %+v", obs["cache"])
	}
	for _, name := range []string{"health", "metrics"} {
		if c, _ := obs[name].GetCapabilityObservation(); !c.Present || obs[name].Provenance.Collector != Collector {
			t.Fatalf("%s = %+v", name, obs[name])
		}
	}
	if _, ok := obs["acme.dev/custom"]; ok {
		t.Fatal("an extension capability was observed")
	}
}

func TestEvaluate_NegativesAfterTheWindow(t *testing.T) {
	health := 404
	port := server(t, &health)
	target := Target{Name: "unit/svc.service", Contract: "./svc", Match: Selector{Unit: "svc.service"}, Host: "127.0.0.1",
		Interfaces: map[string]int32{"api": port}, Dependencies: map[string]string{"cache": "cache.local:6379"}}
	var got []Result
	s := newSink(t, []Target{target}, &got)
	snap := unitSnapshot(port, false)
	s.Evaluate(context.Background(), snap)
	first := outcomes(t, got[0])
	for _, name := range []string{"api", "cache", "health"} {
		if first[name].Outcome != evidence.Insufficient {
			t.Fatalf("first %s = %+v", name, first[name])
		}
	}
	s.Now = func() time.Time { return now.Add(time.Minute) }
	snap.LastPublished = now.Add(time.Minute)
	snap.Workloads[0].Connections[1].LastFailureAt = now.Add(time.Minute)
	s.Evaluate(context.Background(), snap)
	second := outcomes(t, got[0])
	if p, _ := second["api"].GetInterfaceObservation(); second["api"].Outcome != evidence.Observed || p.Present {
		t.Fatalf("api = %+v", second["api"])
	}
	if d, _ := second["cache"].GetDependencyObservation(); second["cache"].Outcome != evidence.Observed || d.Reachable {
		t.Fatalf("cache = %+v", second["cache"])
	}
	if c, _ := second["health"].GetCapabilityObservation(); second["health"].Outcome != evidence.Observed || c.Present {
		t.Fatalf("health = %+v", second["health"])
	}
	codes := map[finding.Code]string{}
	for _, f := range got[0].Findings {
		if len(f.EvidenceRefs) > 0 {
			codes[f.Code] = f.EvidenceRefs[0].Collector
		}
	}
	if codes[finding.CodeInterfaceAbsent] != CollectorKernel || got[0].Status != finding.StatusNonCompliant {
		t.Fatalf("findings = %+v", got[0].Findings)
	}
	// A positive closes the window: the next negative starts a new one.
	snap.Workloads[0].Listeners = []runtimefacts.Listener{{Port: port, Address: "0.0.0.0"}}
	s.Evaluate(context.Background(), snap)
	if _, open := s.windows["unit/svc.service/interface/api"]; open {
		t.Fatal("the window stayed open after a positive")
	}
}

func TestEvaluate_InconclusiveProbes(t *testing.T) {
	health := 503
	port := server(t, &health)
	var got []Result
	target := Target{Name: "t", Contract: "./svc", Match: Selector{Unit: "svc.service"}, Host: "127.0.0.1", Interfaces: map[string]int32{"api": port},
		Dependencies: map[string]string{"db": "nowhere:1"}}
	s := newSink(t, []Target{target}, &got)
	s.Resolve = func(context.Context, string) ([]string, error) { return nil, errors.New("no such host") }
	s.Evaluate(context.Background(), unitSnapshot(port, true))
	obs := outcomes(t, got[0])
	if obs["health"].Outcome != evidence.Insufficient || obs["db"].Outcome != evidence.Failed {
		t.Fatalf("health %+v, db %+v", obs["health"], obs["db"])
	}

	// Metrics: a closed port is a failed collection, a body that is not Prometheus text inconclusive.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	target.Interfaces["api"] = int32(closed)
	s = newSink(t, []Target{target}, &got)
	s.Resolve = nil // the system resolver: an IP literal resolves to itself
	target.Dependencies["db"] = "127.0.0.1:5432"
	s.Evaluate(context.Background(), unitSnapshot(int32(closed), true))
	if obs := outcomes(t, got[0]); obs["metrics"].Outcome != evidence.Failed || obs["health"].Outcome != evidence.Insufficient {
		t.Fatalf("closed port: %+v", obs)
	}
	text := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") }))
	defer text.Close()
	target.Interfaces["api"] = int32(text.Listener.Addr().(*net.TCPAddr).Port)
	s.Evaluate(context.Background(), unitSnapshot(target.Interfaces["api"], true))
	if obs := outcomes(t, got[0]); obs["metrics"].Outcome != evidence.Insufficient {
		t.Fatalf("not Prometheus: %+v", obs["metrics"])
	}
}

func TestEvaluate_UnprobeableCapabilities(t *testing.T) {
	var got []Result
	for name, tc := range map[string]struct {
		target Target
		cap    contract.Capability
	}{
		"no probe host":     {Target{Interfaces: map[string]int32{"api": 1}}, contract.Capability{Binding: &contract.CapabilityBinding{Type: "http", Interface: "api"}}},
		"interface unbound": {Target{Host: "h"}, contract.Capability{Binding: &contract.CapabilityBinding{Type: "http", Interface: "api"}}},
		"no binding":        {Target{Host: "h"}, contract.Capability{}},
		"not http":          {Target{Host: "h"}, contract.Capability{Binding: &contract.CapabilityBinding{Type: "grpc", Interface: "api"}}},
		"unsafe path":       {Target{Host: "h", Interfaces: map[string]int32{"api": 1}}, contract.Capability{Binding: &contract.CapabilityBinding{Type: "http", Interface: "api", Path: "//evil"}}},
	} {
		s := newSink(t, nil, &got)
		for _, typ := range []string{contract.CapabilityHealth, contract.CapabilityMetrics} {
			tc.cap.Type = typ
			c := &contract.Contract{Service: contract.Service{Name: "s"}, Capabilities: []contract.Capability{tc.cap}}
			tc.target.Match = Selector{Unit: "svc.service"}
			r := s.evaluate(context.Background(), bound{t: tc.target, c: c}, nil, unitSnapshot(1, true), now, map[string]time.Time{})
			if o := r.Evidence.Observations[0]; o.Outcome != evidence.Unsupported {
				t.Errorf("%s %s: %+v", name, typ, o)
			}
		}
	}
}

func TestMatch(t *testing.T) {
	cid := "0123456789ab" + "cdef0123456789abcdef0123456789abcdef0123456789abcdef"
	snap := &runtimefacts.Snapshot{Workloads: []runtimefacts.Workload{
		{Identity: runtimefacts.Identity{Kind: runtimefacts.KindUnit, ID: "c1/api.service", Name: "api.service"}},
		{Identity: runtimefacts.Identity{Kind: runtimefacts.KindSession, ID: "pacto-run-1", Name: "pacto-run-1"}},
		{Identity: runtimefacts.Identity{Kind: runtimefacts.KindContainer, ID: cid}},
	}}
	s := &Sink{Proc: procFS{fstest.MapFS{"7/cgroup": {Data: []byte("0::/docker/" + cid + "\n")}}}}
	for sel, want := range map[Selector]string{
		{Unit: "api.service"}:                              "c1/api.service",
		{Unit: "c1/api.service"}:                           "c1/api.service",
		{Session: "pacto-run-1"}:                           "pacto-run-1",
		{Container: cid[:12]}:                              cid,
		{Container: cid[:11]}:                              "",
		{Cgroup: "/system.slice/docker-" + cid + ".scope"}: cid,
		{PID: 7}: cid,
		{PID: 8}: "",
	} {
		var ids []string
		if want := s.selects(sel); want != nil {
			for _, w := range snap.Workloads {
				if want(w.Identity) {
					ids = append(ids, w.ID)
				}
			}
		}
		if got := ""; len(ids) == 1 {
			got = ids[0]
			if got != want {
				t.Errorf("%v: got %q, want %q", sel, got, want)
			}
		} else if want != "" || len(ids) != 0 {
			t.Errorf("%v: got %v, want %q", sel, ids, want)
		}
	}
}

func TestSink_PublishRunAndContracts(t *testing.T) {
	loads := map[string]int{}
	emitted := make(chan []Result, 10)
	s := &Sink{
		Sources: []func(context.Context) ([]Target, error){
			func(context.Context) ([]Target, error) {
				u := Selector{Unit: "u"}
				return []Target{{Name: "a", Contract: "oci://r/a@sha256:x", Match: u}, {Name: "b", Contract: "./b", Match: u},
					{Name: "c", Contract: "./missing", Match: u}}, nil
			},
			func(context.Context) ([]Target, error) { return nil, errors.New("docker down") },
		},
		Load: func(_ context.Context, ref string) (*contract.Bundle, error) {
			loads[ref]++
			if ref == "./missing" {
				return nil, errors.New("no pacto.yaml")
			}
			return &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: ref}}}, nil
		},
		Emit: func(_ context.Context, rs []Result) error {
			emitted <- rs
			return errors.New("emit failed")
		},
		Now: func() time.Time { return now },
	}
	// Two publishes before Run takes one: only the latest is evaluated.
	_ = s.Publish(context.Background(), runtimefacts.Snapshot{NodeName: "old"})
	_ = s.Publish(context.Background(), runtimefacts.Snapshot{NodeName: "new"})
	if snap := <-s.q(); snap.NodeName != "new" {
		t.Fatalf("queued %q", snap.NodeName)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	_ = s.Publish(ctx, runtimefacts.Snapshot{})
	<-emitted
	_ = s.Publish(ctx, runtimefacts.Snapshot{})
	last := <-emitted
	cancel()
	<-done
	// A registry contract is loaded once, a directory on every evaluation.
	if loads["oci://r/a@sha256:x"] != 1 || loads["./b"] != 2 {
		t.Fatalf("loads = %v", loads)
	}
	if len(last) != 3 || last[2].Error == "" || last[1].Status == "" {
		t.Fatalf("results = %+v", last)
	}
}

// A 5xx or no answer neither confirms nor refutes an absence: it leaves the health window as it was, and only
// a positive closes it, as in the operator.
func TestEvaluate_HealthWindowSurvivesInconclusiveAnswers(t *testing.T) {
	health := 404
	port := server(t, &health)
	target := Target{Name: "t", Contract: "./svc", Match: Selector{Unit: "svc.service"}, Host: "127.0.0.1",
		Interfaces: map[string]int32{"api": port}}
	var got []Result
	s := newSink(t, []Target{target}, &got)
	s.Name, s.Kernel = "pacto-run", "pacto-run+ebpf"
	at := func(d time.Duration, code int) evidence.Observation {
		health = code
		s.Now = func() time.Time { return now.Add(d) }
		snap := unitSnapshot(port, true)
		snap.LastPublished = now.Add(d)
		s.Evaluate(context.Background(), snap)
		return outcomes(t, got[0])["health"]
	}
	const key = "t/capability/health"
	if obs := at(0, 404); obs.Outcome != evidence.Insufficient || s.windows[key] != now {
		t.Fatalf("404 opens the window: %+v %v", obs, s.windows)
	}
	if obs := at(30*time.Second, 503); obs.Outcome != evidence.Insufficient || s.windows[key] != now {
		t.Fatalf("503 keeps the window: %+v %v", obs, s.windows)
	}
	if got[0].Evidence.Source != "pacto-run" || outcomes(t, got[0])["api"].Provenance.Collector != "pacto-run+ebpf" {
		t.Fatalf("names: %+v", got[0].Evidence)
	}
	srvPort := target.Interfaces["api"]
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	target.Interfaces["api"] = int32(ln.Addr().(*net.TCPAddr).Port)
	_ = ln.Close()
	if obs := at(40*time.Second, 404); obs.Outcome != evidence.Insufficient || s.windows[key] != now {
		t.Fatalf("no answer keeps the window: %+v %v", obs, s.windows)
	}
	target.Interfaces["api"] = srvPort
	if obs := at(time.Minute, 404); obs.Outcome != evidence.Observed || obs.Provenance.Collector != "pacto-run" {
		t.Fatalf("404 past the window is absent: %+v", obs)
	}
	if obs := at(61*time.Second, 200); obs.Outcome != evidence.Observed {
		t.Fatalf("200 = %+v", obs)
	}
	if _, open := s.windows[key]; open {
		t.Fatal("a positive did not close the window")
	}
}

// fleetSink evaluates targets whose contract reference is the service name it loads.
func fleetSink(t *testing.T, targets []Target, contracts map[string]*contract.Contract, got *[]Result) *Sink {
	t.Helper()
	s := newSink(t, targets, got)
	s.Load = func(_ context.Context, ref string) (*contract.Bundle, error) {
		c, ok := contracts[ref]
		if !ok {
			return nil, errors.New("no contract")
		}
		return &contract.Bundle{Contract: c}, nil
	}
	return s
}

// connectedSnapshot is a fresh snapshot of units whose first one connected successfully to every peer.
func connectedSnapshot(units []string, peers ...runtimefacts.Address) *runtimefacts.Snapshot {
	snap := &runtimefacts.Snapshot{PublishIntervalSeconds: 30, LastPublished: now,
		Collection: runtimefacts.Collection{State: runtimefacts.StateRunning}}
	for i, u := range units {
		w := runtimefacts.Workload{Identity: runtimefacts.Identity{Kind: runtimefacts.KindUnit, ID: u, Name: u}, BaselineAt: now.Add(-time.Minute)}
		for _, p := range peers {
			if i == 0 {
				w.Connections = append(w.Connections, runtimefacts.Connection{Address: p.IP, Port: p.Port, Successes: 1, LastSuccessAt: now.Add(-time.Second)})
			}
		}
		snap.Workloads = append(snap.Workloads, w)
	}
	return snap
}

// undeclared is the subjects of the result's undeclared dependency observations.
func undeclared(t *testing.T, r Result) []string {
	t.Helper()
	var out []string
	for _, o := range r.Evidence.Observations {
		if d, err := o.GetDependencyObservation(); err == nil && d.Undeclared {
			if !d.Reachable || o.Provenance.Collector != CollectorKernel {
				t.Fatalf("undeclared = %+v", o)
			}
			out = append(out, o.Subject.Name)
		}
	}
	return out
}

func TestEvaluate_UndeclaredPeers(t *testing.T) {
	svc := func(name string, deps ...contract.Dependency) *contract.Contract {
		return &contract.Contract{PactoVersion: "1.1", Service: contract.Service{Name: name, Version: "1.0.0"}, Dependencies: deps}
	}
	contracts := map[string]*contract.Contract{
		"web":                        svc("web", contract.Dependency{Name: "orders", Ref: "./orders"}, contract.Dependency{Name: "pg", Ref: "./pg"}, contract.Dependency{Name: "ledger", Ref: "oci://r.io/ledger:1"}),
		"orders":                     svc("orders"),
		"store":                      svc("postgres"),
		"oci://r.io/ledger@sha256:0": svc("ledger-svc"),
		"api":                        svc("api"),
		"local":                      svc("local"),
		"self":                       svc("web"),
		"anon":                       svc(""),
		"nohost":                     svc("nohost"),
		"lost":                       svc("lost"),
	}
	target := func(name, host string, port int32) Target {
		return Target{Name: name, Contract: name, Match: Selector{Unit: name}, Host: host, Interfaces: map[string]int32{"i": port}}
	}
	web := target("web", "10.0.0.1", 80)
	web.Dependencies = map[string]string{"pg": "10.0.0.3:5432"}
	ledger := target("ledger", "10.0.0.4", 9000)
	ledger.Contract = "oci://r.io/ledger@sha256:0"
	targets := []Target{web, target("orders", "10.0.0.2", 8080), target("store", "10.0.0.3", 5432), ledger,
		target("api", "10.0.0.5", 8443), target("local", "127.0.0.1", 7000), target("self", "10.0.0.6", 1),
		target("anon", "10.0.0.7", 1), target("nohost", "", 1), target("lost", "lost.invalid", 1), target("broken", "10.0.0.8", 1)}
	units := []string{"web"}
	peers := []runtimefacts.Address{}
	for _, tg := range targets[1:] {
		units = append(units, tg.Name)
		peers = append(peers, runtimefacts.NewAddress(tg.Host, tg.Interfaces["i"]))
	}
	var got []Result
	s := fleetSink(t, targets, contracts, &got)
	s.Resolve = func(_ context.Context, host string) ([]string, error) {
		if host == "lost.invalid" {
			return nil, errors.New("no such host")
		}
		return []string{host}, nil
	}
	s.Evaluate(context.Background(), connectedSnapshot(units, peers...))
	// orders by name, store by bound address and ledger by reference are declared; a bridge workload's
	// connection to 127.0.0.1 is its own loopback, not the host-network one.
	if u := undeclared(t, got[0]); len(u) != 1 || u[0] != "api" {
		t.Fatalf("undeclared = %v", u)
	}
	for _, f := range got[0].Findings {
		if f.Code == finding.CodeDependencyUndeclared && f.Subject.Name != "api" {
			t.Fatalf("finding = %+v", f)
		}
	}
	if got[10].Error == "" {
		t.Fatalf("broken = %+v", got[10])
	}

	// On the host's network the loopback is shared: the host-network peer is undeclared too.
	targets[0].Host = "127.0.0.1"
	s.Evaluate(context.Background(), connectedSnapshot(units, peers...))
	if u := undeclared(t, got[0]); len(u) != 2 || u[0] != "api" || u[1] != "local" {
		t.Fatalf("host network: undeclared = %v", u)
	}

	// A declared dependency no target stands for may be the very peer reached: nothing is reported.
	contracts["web"].Dependencies = append(contracts["web"].Dependencies, contract.Dependency{Name: "billing", Ref: "./billing"})
	s.Evaluate(context.Background(), connectedSnapshot(units, peers...))
	if u := undeclared(t, got[0]); len(u) != 0 {
		t.Fatalf("unresolved dependency: undeclared = %v", u)
	}
}

func TestSameContract(t *testing.T) {
	for _, tc := range []struct {
		dep, target string
		want        bool
	}{
		{"oci://r:5000/a/svc:1.0", "oci://r:5000/a/svc@sha256:0", true},
		{"oci://r:5000/a/svc", "oci://r:5000/a/svc:2", true},
		{"oci://r/a/svc", "oci://r/a/other", false},
		{"./svc", "./svc", false},
		{"oci://r/a/svc", "r/a/svc", false},
	} {
		if got := sameContract(tc.dep, tc.target); got != tc.want {
			t.Errorf("sameContract(%q, %q) = %v", tc.dep, tc.target, got)
		}
	}
}

func TestEvaluate_ForeignNetns(t *testing.T) {
	health := 200
	port := server(t, &health)
	var got []Result
	for _, host := range []string{"127.0.0.1", "localhost"} {
		target := Target{Name: "unit/svc.service", Contract: "./svc", Match: Selector{Unit: "svc.service"}, Host: host,
			Interfaces: map[string]int32{"api": port}}
		s := newSink(t, []Target{target}, &got)
		s.ForeignNetns, s.Protocol = true, true
		s.Evaluate(context.Background(), unitSnapshot(port, true))
		obs := outcomes(t, got[0])
		api, _ := obs["api"].GetInterfaceObservation()
		if !api.Present || api.Protocol != "" || obs["health"].Outcome != evidence.Unsupported || obs["metrics"].Outcome != evidence.Unsupported {
			t.Fatalf("%s: %+v", host, obs)
		}
		if len(got[0].Notes) != 1 || !strings.Contains(got[0].Notes[0], "--network=host") {
			t.Fatalf("%s: notes = %v", host, got[0].Notes)
		}
	}
}

func TestEvaluate_BindingStart(t *testing.T) {
	// The unit's only process started 10s before now: a success 20s before is a previous occupant's.
	boot := now.Add(-time.Hour)
	proc := procFS{fstest.MapFS{
		"stat":     {Data: []byte("btime " + strconv.FormatInt(boot.Unix(), 10) + "\n")},
		"5/cgroup": {Data: []byte("0::/system.slice/svc.service\n")},
		"5/stat":   {Data: []byte("5 (svc) S 1 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 " + strconv.Itoa(int((time.Hour-10*time.Second)/(10*time.Millisecond))) + " 0")},
	}}
	target := Target{Name: "unit/svc.service", Contract: "./svc", Match: Selector{Unit: "svc.service"},
		Dependencies: map[string]string{"db": "127.0.0.1:5432"}}
	var got []Result
	s := newSink(t, []Target{target}, &got)
	s.Proc = proc
	snap := unitSnapshot(1, false)
	snap.Workloads[0].Connections[0].LastSuccessAt = now.Add(-20 * time.Second)
	s.Evaluate(context.Background(), snap)
	if db := outcomes(t, got[0])["db"]; db.Outcome != evidence.Insufficient {
		t.Fatalf("db before start = %+v", db)
	}
	snap.Workloads[0].Connections[0].LastSuccessAt = now.Add(-5 * time.Second)
	s.Evaluate(context.Background(), snap)
	if d, _ := outcomes(t, got[0])["db"].GetDependencyObservation(); !d.Reachable {
		t.Fatalf("db after start = %+v", outcomes(t, got[0])["db"])
	}
}
