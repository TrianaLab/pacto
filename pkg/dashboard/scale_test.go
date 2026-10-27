package dashboard

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// A large fleet, generated here rather than committed: 600 services, about
// 860 Stages over 12 environments, 1,500 held changes and 40 frozen Stages.
// It is the size of the stress build of the browser demo, so a handler that
// starts doing per-Stage work it should do per page shows up in the
// benchmarks and the budget test below.

// countingDelivery counts the per-item reads a handler makes.
type countingDelivery struct {
	*fakeDelivery
	describes, histories atomic.Int64
	// rereads counts the histories asked for without the Stage the handler
	// had just read, so the host reads it again.
	rereads atomic.Int64
}

func (c *countingDelivery) Describe(ctx context.Context, st *delivery.StageObject, d string) delivery.Revision {
	c.describes.Add(1)
	return c.fakeDelivery.Describe(ctx, st, d)
}

func (c *countingDelivery) History(ctx context.Context, ns, stage string, listed *delivery.StageObject) (*delivery.History, error) {
	c.histories.Add(1)
	if listed == nil {
		c.rereads.Add(1)
	}
	return c.fakeDelivery.History(ctx, ns, stage, listed)
}

var scaleEnvs = [...]string{"test", "dev", "qa", "staging", "perf", "preprod", "sandbox", "prod-eu-west-1", "prod-us-east-1",
	"prod-ap-southeast-2", "dr-eu-central-1", "canary-global-edge"}

func scaleDigest(svc string, v int) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(fmt.Sprint(svc, "@", v))))
}

// largeCluster is the generated fleet: the delivery world and its snapshot.
func largeCluster(tb testing.TB) (*countingDelivery, func(context.Context) (*fleet.Query, error)) {
	tb.Helper()
	r := rand.New(rand.NewPCG(415, 1))
	team := func(i int) string { return fmt.Sprintf("team-%d", i%20) }
	f := &fakeDelivery{caller: person("acme:team-0", "acme:team-1"), known: map[string]string{}}
	var revs []fleet.RawRevision
	var targets []fleet.RawTarget
	for i := range 600 {
		svc := fmt.Sprintf("service-%03d", i)
		revs = append(revs, fleet.RawRevision{Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0",
			Service: contract.Service{Name: svc, Version: "1.0.0", Owner: contract.Owner{Team: team(i)}}}}})
		targets = append(targets, fleet.RawTarget{Scope: scaleEnvs[i%len(scaleEnvs)], Kind: "kubernetes-workload", Name: svc, Service: svc})
		if i >= 200 {
			continue // 200 services are delivered
		}
		var published []string
		for v := range 40 {
			d := scaleDigest(svc, v)
			published = append(published, d)
			f.known[d] = fmt.Sprintf("1.%d.0", v)
		}
		envs := append([]string{"test"}, scaleEnvs[1:1+r.IntN(5)]...)
		if i%20 == 0 {
			envs = scaleEnvs[:]
		}
		for _, env := range envs {
			ss := delivery.StageStatus{Pointer: published[30], Verified: published[25:35], Offered: published[20:30],
				Candidate: published[39], Level: delivery.Level([]string{"observe", "advise", "auto-nonbreaking", "autonomous"}[r.IntN(4)]),
				Conditions: []delivery.Condition{{Type: "Ready", Status: "True"}}}
			if env == "test" {
				ss.Published = published
			}
			p := &delivery.Proposal{}
			for range r.IntN(5) {
				p.Blockers = append(p.Blockers, delivery.Blocker{Kind: "undeclared-range", Subject: fmt.Sprintf("service-%03d", r.IntN(600)), Message: "no range"})
			}
			ss.Proposal = p
			for k := range 20 {
				ss.Stream.Concluded = append(ss.Stream.Concluded, delivery.Concluded{UID: fmt.Sprint(k), Type: delivery.TypePromote, Outcome: delivery.Succeeded})
			}
			for range 5 {
				ss.Shadow = append(ss.Shadow, delivery.Shadow{Candidate: published[39], Predicted: delivery.PredictProceeds})
			}
			if len(f.stages) < 40 {
				ss.Frozen, ss.FrozenReason = true, "Errored"
			}
			f.stages = append(f.stages, stage(env, svc, svc, ss))
		}
	}
	for k := range 1500 {
		st := f.stages[int(r.Float64()*r.Float64()*float64(len(f.stages)))]
		op := operation(st.Metadata.Namespace, fmt.Sprintf("held-%d", k), st.Metadata.Name, held("team-0"))
		op.Spec.Params.From, op.Spec.Params.To = st.Status.Pointer, st.Status.Candidate
		f.ops = append(f.ops, op)
	}
	for _, st := range f.stages[:40] {
		f.ops = append(f.ops, operation(st.Metadata.Namespace, "broke-"+st.Metadata.Name, st.Metadata.Name,
			delivery.OperationStatus{Phase: delivery.PhaseConcluded, Outcome: delivery.Errored}))
	}
	h := &delivery.History{Stage: delivery.StageNow{Pointer: f.stages[0].Status.Pointer}, Completeness: "complete"}
	for k := range 40 {
		h.Entries = append(h.Entries, delivery.HistoryEntry{Operation: fmt.Sprint(k), Type: delivery.TypePromote, Actor: "alice",
			From: f.stages[0].Status.Published[k%39], To: f.stages[0].Status.Published[k%39+1]})
	}
	f.history = h
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("local", "local",
		&fleet.Collection{Revisions: revs, Targets: targets}))
	if err != nil {
		tb.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	return &countingDelivery{fakeDelivery: f}, func(context.Context) (*fleet.Query, error) { return q, nil }
}

// scaleHandler serves the generated fleet in memory, as the browser demo does.
func scaleHandler(tb testing.TB) (http.Handler, *countingDelivery) {
	tb.Helper()
	c, q := largeCluster(tb)
	srv := NewServer(Options{UI: testUI(), Fleet: q, Auth: (&authed{state: AuthState{SignedIn: true}}).auth(false, true),
		Delivery: func(*http.Request) (DeliveryAPI, error) { return c, nil }})
	mux := http.NewServeMux()
	srv.registerAPI(mux)
	return withRequest(mux), c
}

// The answers each view reads first, with the per-item reads each may make:
// a page's worth, never one per Stage.
var scaleViews = []struct {
	path                 string
	describes, histories int64
}{
	{"/api/delivery/inbox", 2*25 + 2*25, 0},
	{"/api/delivery/environments", 2 * 25 * int64(len(scaleEnvs)), 0},
	{"/api/delivery/published", 10 * 40, 0},
	{"/api/delivery/autonomy", 0, 0},
	{"/api/delivery/history", 25 * 3, 25},
	{"/api/delivery/history?namespace=test&stage=service-000", 1 + 2*40, 1},
	{"/api/fleet/overview", 0, 0},
	{"/api/fleet/entities?kinds=service&limit=25", 0, 0},
	{"/api/fleet/attention", 0, 0},
}

func serveScale(tb testing.TB, h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		tb.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
	}
	return rec
}

// TestLargeFleetBudget keeps each view's first answer cheap on a large fleet:
// a page of artifact and history reads, and well under the wall-clock budget.
// The budget is loose (CI runs this with -race); it catches a handler that
// turns per page into per Stage, not a few milliseconds.
func TestLargeFleetBudget(t *testing.T) {
	h, c := scaleHandler(t)
	const budget = time.Second
	for _, v := range scaleViews {
		c.describes.Store(0)
		c.histories.Store(0)
		c.rereads.Store(0)
		start := time.Now()
		serveScale(t, h, v.path)
		took := time.Since(start)
		t.Logf("%-60s %v describes=%d histories=%d", v.path, took.Round(time.Millisecond), c.describes.Load(), c.histories.Load())
		expect(t, took < budget, "%s took %v, over the %v budget", v.path, took, budget)
		expect(t, c.describes.Load() <= v.describes, "%s described %d digests, more than a page's %d", v.path, c.describes.Load(), v.describes)
		expect(t, c.histories.Load() <= v.histories, "%s read %d histories, more than a page's %d", v.path, c.histories.Load(), v.histories)
		expect(t, c.rereads.Load() == 0, "%s asked for %d histories without the Stage it had read", v.path, c.rereads.Load())
	}
}

// slowHistory answers a history after a cluster's round trips.
type slowHistory struct{ *countingDelivery }

func (s slowHistory) History(ctx context.Context, ns, stage string, listed *delivery.StageObject) (*delivery.History, error) {
	time.Sleep(20 * time.Millisecond)
	return s.countingDelivery.History(ctx, ns, stage, listed)
}

// A page of every Stage's history reads the histories side by side: 25 slow
// reads one after another would take half a second.
func TestHistoryPageReadsInParallel(t *testing.T) {
	c, q := largeCluster(t)
	srv := NewServer(Options{UI: testUI(), Fleet: q, Delivery: func(*http.Request) (DeliveryAPI, error) { return slowHistory{c}, nil }})
	mux := http.NewServeMux()
	srv.registerAPI(mux)
	start := time.Now()
	serveScale(t, withRequest(mux), "/api/delivery/history")
	took := time.Since(start)
	expect(t, c.histories.Load() == 25 && took < 250*time.Millisecond, "25 histories took %v (%d read)", took, c.histories.Load())
}

// slowDescribe reads an artifact the way a registry does, after a round trip.
type slowDescribe struct{ *countingDelivery }

func (s slowDescribe) Describe(ctx context.Context, st *delivery.StageObject, d string) delivery.Revision {
	time.Sleep(5 * time.Millisecond)
	return s.countingDelivery.Describe(ctx, st, d)
}

// A board page reads all of its artifacts at once, each digest once: about
// 200 cold reads two at a time, Stage by Stage, took half a second.
func TestEnvironmentsPageReadsArtifactsTogether(t *testing.T) {
	c, q := largeCluster(t)
	srv := NewServer(Options{UI: testUI(), Fleet: q, Delivery: func(*http.Request) (DeliveryAPI, error) { return slowDescribe{c}, nil }})
	mux := http.NewServeMux()
	srv.registerAPI(mux)
	start := time.Now()
	serveScale(t, withRequest(mux), "/api/delivery/environments")
	took := time.Since(start)
	expect(t, c.describes.Load() >= 40 && took < 200*time.Millisecond, "%d artifact reads took %v", c.describes.Load(), took)
}

// go test ./pkg/dashboard -run '^$' -bench LargeFleet
func BenchmarkLargeFleet(b *testing.B) {
	h, _ := scaleHandler(b)
	for _, v := range scaleViews {
		b.Run(v.path, func(b *testing.B) {
			for b.Loop() {
				serveScale(b, h, v.path)
			}
		})
	}
}
