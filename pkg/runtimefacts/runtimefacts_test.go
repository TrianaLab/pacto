package runtimefacts

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/evidence"
)

var now = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return now.Add(-d) }

// snapshot is a fresh, running host with one completely observed workload listening on 8080.
func snapshot() *Snapshot {
	return &Snapshot{
		PublishIntervalSeconds: 30,
		LastPublished:          ago(10 * time.Second),
		Collection:             Collection{State: StateRunning},
		Workloads: []Workload{{
			Identity:   Identity{Kind: KindPod, ID: "w1"},
			BaselineAt: ago(5 * time.Minute),
			Listeners:  []Listener{{Port: 8080, Family: "IPv4", Address: "0.0.0.0"}},
		}},
	}
}

func TestAddress(t *testing.T) {
	if got := NewAddress("::ffff:10.0.0.1", 80); got != (Address{IP: "10.0.0.1", Port: 80}) {
		t.Errorf("IPv4-mapped: got %v", got)
	}
	if got := NewAddress("not-an-ip", 1).String(); got != "not-an-ip:1" {
		t.Errorf("an unparseable address is kept as written: got %s", got)
	}
	if got := NewAddress("fd00:0::1", 80).String(); got != "[fd00::1]:80" {
		t.Errorf("IPv6: got %s", got)
	}
}

func TestVerdictOutcome(t *testing.T) {
	for v, want := range map[Verdict]evidence.Outcome{
		Failed: evidence.Failed, Stale: evidence.Stale, Partial: evidence.Insufficient,
	} {
		if got := v.Outcome(); got != want {
			t.Errorf("%v.Outcome() = %s, want %s", v, got, want)
		}
	}
}

func TestStalenessBound(t *testing.T) {
	s := &Snapshot{}
	if got := s.StalenessBound(0); got != 90*time.Second {
		t.Errorf("default interval: got %s", got)
	}
	s.PublishIntervalSeconds = 10
	if got := s.StalenessBound(0); got != 30*time.Second {
		t.Errorf("own interval: got %s", got)
	}
	if got := s.StalenessBound(time.Minute); got != time.Minute {
		t.Errorf("configured: got %s", got)
	}
}

func TestClassify(t *testing.T) {
	start := ago(10 * time.Minute)
	tests := []struct {
		name      string
		mutate    func(*Snapshot)
		id        string
		staleness time.Duration
		want      Coverage
		facts     bool
	}{
		{"complete", func(*Snapshot) {}, "w1", 0, CoverageComplete, true},
		{"never published", func(s *Snapshot) { s.LastPublished = time.Time{} }, "w1", 0, CoverageStale, false},
		{"beyond the staleness bound", func(s *Snapshot) { s.LastPublished = ago(2 * time.Minute) }, "w1", 0, CoverageStale, false},
		{"80s is within 3 x the 30s default interval", func(s *Snapshot) { s.PublishIntervalSeconds, s.LastPublished = 0, ago(80*time.Second) }, "w1", 0, CoverageComplete, true},
		{"80s is beyond an explicit 1m bound", func(s *Snapshot) { s.LastPublished = ago(80 * time.Second) }, "w1", time.Minute, CoverageStale, false},
		{"collection failed", func(s *Snapshot) { s.Collection.State = StateFailed }, "w1", 0, CoverageFailed, false},
		{"not mapped", func(*Snapshot) {}, "w2", 0, CoveragePartial, false},
		{"shared namespace", func(s *Snapshot) { s.Workloads[0].Shared = true }, "w1", 0, CoveragePartial, false},
		{"degraded", func(s *Snapshot) { s.Collection.State = StateDegraded }, "w1", 0, CoveragePartial, true},
		{"truncated", func(s *Snapshot) { s.Workloads[0].Truncated = true }, "w1", 0, CoveragePartial, true},
		{"no baseline", func(s *Snapshot) { s.Workloads[0].BaselineAt = time.Time{} }, "w1", 0, CoveragePartial, true},
		{"baseline before start", func(s *Snapshot) { s.Workloads[0].BaselineAt = ago(11 * time.Minute) }, "w1", 0, CoveragePartial, true},
		{"drop after baseline", func(s *Snapshot) { s.Collection.LastDropAt = ago(time.Minute) }, "w1", 0, CoveragePartial, true},
		{"drop before baseline", func(s *Snapshot) { s.Collection.LastDropAt = ago(6 * time.Minute) }, "w1", 0, CoverageComplete, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := snapshot()
			tt.mutate(s)
			got := Classify(s, tt.id, start, now, tt.staleness)
			if got.Coverage != tt.want || (got.Facts != nil) != tt.facts || !got.Start.Equal(start) {
				t.Fatalf("got coverage %d facts=%v, want %d facts=%v", got.Coverage, got.Facts != nil, tt.want, tt.facts)
			}
		})
	}
	if got := Classify(nil, "w1", start, now, 0); got.Coverage != CoverageStale {
		t.Fatalf("no snapshot: got %d", got.Coverage)
	}
}

func TestObservable(t *testing.T) {
	for proto, want := range map[string]bool{"": true, "TCP": true, "UDP": false, "SCTP": false} {
		if Observable(proto) != want {
			t.Errorf("%q: want %v", proto, want)
		}
	}
}

func TestInterfaceVerdict(t *testing.T) {
	facts := func(addr string) *Workload {
		return &Workload{Listeners: []Listener{{Port: 9090}, {Port: 8080, Address: addr}}}
	}
	ready := func(c Coverage, w *Workload) Backing {
		return Backing{Observed: Observed{Coverage: c, Facts: w}, Ready: true, Port: 8080, Resolved: true}
	}
	unresolved := ready(CoverageComplete, facts("0.0.0.0"))
	unresolved.Resolved = false
	notReady := ready(CoverageComplete, facts("0.0.0.0"))
	notReady.Ready = false
	tests := []struct {
		name    string
		backing []Backing
		floor   Coverage
		want    Verdict
	}{
		{"no ready backing workload", []Backing{notReady}, CoverageComplete, Silent},
		{"listener on all addresses", []Backing{ready(CoverageComplete, facts("0.0.0.0"))}, CoverageComplete, Positive},
		{"listener with no address", []Backing{ready(CoveragePartial, facts(""))}, CoverageComplete, Positive},
		{"loopback listener", []Backing{ready(CoverageComplete, facts("127.0.0.1"))}, CoverageComplete, Negative},
		{"IPv4-mapped loopback listener", []Backing{ready(CoverageComplete, facts("::ffff:127.0.0.1"))}, CoverageComplete, Negative},
		{"one listener among several is enough", []Backing{ready(CoverageStale, nil), ready(CoverageComplete, facts("::"))}, CoverageComplete, Positive},
		{"unresolved port", []Backing{unresolved}, CoverageComplete, Partial},
		{"partial floor", []Backing{ready(CoverageComplete, facts("127.0.0.1"))}, CoveragePartial, Partial},
		{"failed", []Backing{ready(CoverageFailed, nil)}, CoverageComplete, Failed},
		{"stale beats failed", []Backing{ready(CoverageFailed, nil), ready(CoverageStale, nil)}, CoverageComplete, Stale},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InterfaceVerdict(tt.backing, tt.floor); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDependencyVerdict pins the per-peer timestamp rule: lifetime counters never decide, only the latest
// success and failure within the freshness bound, and never a timestamp from before the workload started.
func TestDependencyVerdict(t *testing.T) {
	peer := []Address{NewAddress("10.96.0.10", 80)}
	other := []Address{NewAddress("10.96.0.10", 80), NewAddress("10.0.1.7", 8080)}
	conn := func(success, failure time.Time) Connection {
		return Connection{Address: "10.96.0.10", Port: 80, Successes: 9, Failures: 9, LastSuccessAt: success, LastFailureAt: failure}
	}
	var never time.Time
	tests := []struct {
		name  string
		conns []Connection
		peers []Address
		want  Verdict
	}{
		{"counters without timestamps are silent", []Connection{conn(never, never)}, peer, Silent},
		{"fresh success", []Connection{conn(ago(time.Second), never)}, peer, Positive},
		{"IPv4-mapped peer address matches", []Connection{{Address: "::ffff:10.96.0.10", Port: 80, LastSuccessAt: ago(time.Second)}}, peer, Positive},
		{"success beyond the freshness bound is history", []Connection{conn(ago(2*time.Minute), never)}, peer, Silent},
		{"success at the same instant as the failure is positive", []Connection{conn(ago(time.Second), ago(time.Second))}, peer, Positive},
		{"failure newer than the success is negative", []Connection{conn(ago(time.Minute), ago(time.Second))}, peer, Negative},
		{"fresh failure after a stale success is negative", []Connection{conn(ago(time.Hour), ago(time.Second))}, peer, Negative},
		{"stale failure is silent", []Connection{conn(never, ago(2*time.Minute))}, peer, Silent},
		{"timestamps before the workload started are another's", []Connection{conn(ago(11*time.Minute), ago(12*time.Minute))}, peer, Silent},
		{"other peers do not count", []Connection{{Address: "10.0.1.8", Port: 80, LastSuccessAt: ago(time.Second)}}, peer, Silent},
		{"any positive peer wins over a negative one", []Connection{
			{Address: "10.0.1.7", Port: 8080, Failures: 1, LastFailureAt: ago(5 * time.Second)},
			{Address: "10.96.0.10", Port: 80, Successes: 1, LastSuccessAt: ago(5 * time.Second)},
		}, other, Positive},
		{"entries for one peer fold before judging", []Connection{
			conn(ago(30*time.Second), never), conn(never, ago(time.Second)),
		}, peer, Negative},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := View{Now: now, Freshness: 90 * time.Second, Workloads: []Observed{
				{Coverage: CoverageComplete, Facts: &Workload{Connections: tt.conns}, Start: ago(10 * time.Minute)},
			}}
			if got := v.DependencyVerdict(tt.peers); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
	for c, want := range map[Coverage]Verdict{CoverageStale: Stale, CoverageFailed: Failed, CoveragePartial: Silent} {
		v := View{Now: now, Freshness: time.Minute, Workloads: []Observed{{Coverage: CoverageComplete}, {Coverage: c}}}
		if got := v.DependencyVerdict(peer); got != want {
			t.Errorf("silence under coverage %d: got %v, want %v", c, got, want)
		}
	}
}

func TestUndeclared(t *testing.T) {
	v := View{Now: now, Freshness: time.Minute, Workloads: []Observed{
		{Facts: &Workload{Connections: []Connection{
			{Address: "10.96.0.20", Port: 5432, LastSuccessAt: ago(time.Second)},
			{Address: "10.96.0.30", Port: 6379, LastSuccessAt: ago(time.Second)},
			{Address: "10.96.0.40", Port: 80, LastSuccessAt: ago(time.Hour)},
		}}, Start: ago(time.Hour + time.Minute)},
		{Coverage: CoverageStale},
	}}
	candidates := Binding{Dependencies: map[string][]Address{
		"ledger": {NewAddress("10.0.0.1", 5432), NewAddress("10.96.0.20", 5432)},
		"cache":  {NewAddress("10.96.0.30", 6379)},
		"old":    {NewAddress("10.96.0.40", 80)},
		"idle":   {NewAddress("10.96.0.50", 80)},
	}}
	prov := evidence.Provenance{Collector: "ebpf", DetectedAt: now}
	got := v.Undeclared(candidates, prov)
	if len(got) != 2 || got[0].Subject.Name != "cache" || got[1].Subject.Name != "ledger" {
		t.Fatalf("got %+v, want cache and ledger", got)
	}
	p, err := got[0].GetDependencyObservation()
	if err != nil || !p.Reachable || !p.Undeclared || got[0].Subject.Kind != "dependency" || got[0].Provenance != prov {
		t.Fatalf("got %+v %+v (%v)", got[0], p, err)
	}
}

// TestSnapshotWire pins the split wire form: pods under "pods" keyed by uid with no kind, every other kind
// under "workloads", and a round trip that restores the pod kind.
func TestSnapshotWire(t *testing.T) {
	s := Snapshot{NodeName: "n", Workloads: []Workload{
		{Identity: Identity{Kind: KindPod, ID: "u1", Name: "dropped"}, Shared: true},
		{Identity: Identity{Kind: KindUnit, ID: "web.service", Name: "web.service"}, Truncated: true},
	}}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"nodeName":"n","pods":[{"uid":"u1","netnsShared":true}],` +
		`"workloads":[{"kind":"unit","id":"web.service","name":"web.service","truncated":true}]}`
	if string(b) != want {
		t.Fatalf("wire:\n got  %s\n want %s", b, want)
	}
	var back Snapshot
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	s.Workloads[0].Name = ""
	if !reflect.DeepEqual(back, s) {
		t.Fatalf("round trip:\n got  %+v\n want %+v", back, s)
	}
	if err := json.Unmarshal([]byte(`{"nodeName":"n","extra":1}`), &back); err == nil {
		t.Fatal("an unknown field must be rejected")
	}
}

func TestObservationBuilders(t *testing.T) {
	prov := evidence.Provenance{Collector: "c"}
	pos := InterfaceObservation("api", evidence.InterfaceObservation{Type: "http", Protocol: evidence.ProtocolHTTP1}, Positive, prov)
	if p, err := pos.GetInterfaceObservation(); err != nil || !p.Present || p.Protocol != evidence.ProtocolHTTP1 || p.Type != "http" {
		t.Fatalf("positive = %+v, %v", p, err)
	}
	neg := InterfaceObservation("api", evidence.InterfaceObservation{Type: "http", Protocol: evidence.ProtocolHTTP1}, Negative, prov)
	if p, err := neg.GetInterfaceObservation(); err != nil || p.Present || p.Protocol != "" || p.Type != "http" {
		t.Fatalf("negative = %+v, %v", p, err)
	}
}

func TestObservationBuilders_Inconclusive(t *testing.T) {
	prov := evidence.Provenance{Collector: "c"}
	for v, want := range map[Verdict]evidence.Outcome{Silent: evidence.Insufficient, Partial: evidence.Insufficient,
		Failed: evidence.Failed, Stale: evidence.Stale} {
		if o := InterfaceObservation("api", evidence.InterfaceObservation{}, v, prov); o.Outcome != want || o.Subject.Name != "api" {
			t.Errorf("interface %v = %+v", v, o)
		}
		if o := DependencyObservation("db", v, prov); o.Outcome != want || o.Subject.Kind != "dependency" {
			t.Errorf("dependency %v = %+v", v, o)
		}
	}
}

func TestDependencyObservationBuilder(t *testing.T) {
	prov := evidence.Provenance{Collector: "c"}
	for v, want := range map[Verdict]bool{Positive: true, Negative: false} {
		d, err := DependencyObservation("db", v, prov).GetDependencyObservation()
		if err != nil || d.Reachable != want {
			t.Errorf("dependency %v = %+v, %v", v, d, err)
		}
	}
}

func TestCoverageString(t *testing.T) {
	for c, want := range map[Coverage]string{CoverageComplete: "Complete", CoveragePartial: "Partial", CoverageFailed: "Failed", CoverageStale: "Stale"} {
		if c.String() != want {
			t.Errorf("%d: %q", c, c.String())
		}
	}
}
