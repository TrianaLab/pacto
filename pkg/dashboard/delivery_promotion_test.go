package dashboard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// fed is a Stage in env whose candidates come from the Stage upNS/upName.
func fed(env, name, upNS, upName string) delivery.StageObject {
	st := stage(env, name, name, delivery.StageStatus{})
	if upName != "" {
		st.Spec.Candidates.UpstreamStage = &delivery.StageRef{Name: upName, Namespace: upNS}
	}
	return st
}

func TestInboxCountsWhatWaitsPerEnvironment(t *testing.T) {
	f := sampleCluster("acme:team-checkout")
	// payments in prod is fed by payments in test; a held change on a Stage
	// the caller cannot read has no environment.
	f.stages[0].Spec.Candidates.UpstreamStage = &delivery.StageRef{Namespace: "test", Name: "payments"}
	f.stages = append(f.stages, stage("test", "payments", "payments", delivery.StageStatus{}))
	f.ops = append(f.ops, operation("hidden", "unseen", "nowhere", held()), operation("prod", "shared", "payments", held("team-checkout", "team-ledger")))
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	_, inbox := call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	got := fmt.Sprint(inbox["byEnvironment"])
	want := "[map[environment:other errored:1 frozen:1 held:0 upstream:[]] map[environment:prod errored:0 frozen:0 held:3 upstream:[test]] map[environment: errored:0 frozen:0 held:1 upstream:[]]]"
	expect(t, got == want, "per environment, in promotion order, the unreadable last:\n%s\nwant\n%s", got, want)

	expect(t, inbox["heldShared"] == float64(1), "one held change names two teams: %v", inbox["heldShared"])

	// environment narrows the lists, never the tallies.
	_, inbox = call(t, http.MethodGet, base+"/api/delivery/inbox?environment=other", nil)
	expect(t, fmt.Sprint(inbox["byEnvironment"]) == want, "the tallies stay whole: %v", inbox["byEnvironment"])
	expect(t, len(inbox["held"].([]any)) == 0 && len(inbox["errored"].([]any)) == 1 && len(inbox["frozen"].([]any)) == 1,
		"only what waits to enter other: %v", inbox)
	expect(t, inbox["heldPage"].(map[string]any)["total"] == float64(0), "the held total follows the filter: %v", inbox["heldPage"])
	// except leaves out the environments a figure draws, so a folded station's
	// counts open exactly what they count; an empty value names the unreadable.
	_, inbox = call(t, http.MethodGet, base+"/api/delivery/inbox?except=other&except=", nil)
	expect(t, fmt.Sprint(inbox["byEnvironment"]) == want, "except keeps the tallies whole: %v", inbox["byEnvironment"])
	expect(t, inbox["heldPage"].(map[string]any)["total"] == float64(3) && len(inbox["errored"].([]any)) == 0 && len(inbox["frozen"].([]any)) == 0,
		"only what waits to enter prod: %v", inbox)
	_, inbox = call(t, http.MethodGet, base+"/api/delivery/inbox?except=prod", nil)
	expect(t, inbox["heldPage"].(map[string]any)["total"] == float64(1), "the held change on an unreadable Stage stays: %v", inbox["heldPage"])
}

func TestUpstreamOfReadsTheDeclaredSource(t *testing.T) {
	up := stage("test", "pay", "pay", delivery.StageStatus{})
	up.Spec.Environment = "staging"
	f := &fakeDelivery{stages: []delivery.StageObject{up}}
	at := func(c delivery.Candidates, ready ...string) string {
		st := stage("prod", "pay", "pay", delivery.StageStatus{})
		st.Spec.Candidates = c
		for _, r := range ready {
			st.Status.Conditions = []delivery.Condition{{Type: "Ready", Status: "False", Reason: r}}
		}
		return fmt.Sprintf("%+v", *upstreamOf(context.Background(), f, &st, ""))
	}
	cases := map[string]string{
		at(delivery.Candidates{UpstreamStage: &delivery.StageRef{Namespace: "test", Name: "pay"}}):          "{Kind:stage Namespace:test Stage:pay Environment:staging Problem: Stream: Repository: Trust:}",
		at(delivery.Candidates{UpstreamStage: &delivery.StageRef{Name: "gone"}}):                            "{Kind:stage Namespace:prod Stage:gone Environment: Problem:missing Stream: Repository: Trust:}",
		at(delivery.Candidates{UpstreamRecords: &delivery.RemoteStage{Repository: "r/s", Stream: "c/n/s"}}): "{Kind:records Namespace: Stage: Environment: Problem: Stream:c/n/s Repository:r/s Trust:unknown}",
		at(delivery.Candidates{OCIRepositoryRef: &delivery.LocalRef{Name: "pay-candidates"}}):               "{Kind:artifact Namespace: Stage: Environment: Problem: Stream: Repository:pay-candidates Trust:}",
		at(delivery.Candidates{}): "{Kind:none Namespace: Stage: Environment: Problem: Stream: Repository: Trust:}",
		// SPEC 21 r4: a dead entry is never drawn as a live source.
		at(delivery.Candidates{OCIRepositoryRef: &delivery.LocalRef{Name: "gone"}}, "CandidateNotFound"):       "{Kind:artifact Namespace: Stage: Environment: Problem:missing Stream: Repository:gone Trust:}",
		at(delivery.Candidates{OCIRepositoryRef: &delivery.LocalRef{Name: "noverify"}}, "CandidateUnverified"): "{Kind:artifact Namespace: Stage: Environment: Problem:unverified Stream: Repository:noverify Trust:}",
	}
	// Outside the dashboard's namespace the upstream is never read.
	scoped := stage("prod", "pay", "pay", delivery.StageStatus{})
	scoped.Spec.Candidates = delivery.Candidates{UpstreamStage: &delivery.StageRef{Namespace: "test", Name: "pay"}}
	if got := *upstreamOf(context.Background(), f, &scoped, "prod"); got.Problem != "outOfScope" || got.Environment != "" {
		t.Errorf("out of scope: %+v", got)
	}
	scoped.Spec.Candidates.UpstreamStage = &delivery.StageRef{Name: "pay"}
	if got := *upstreamOf(context.Background(), f, &scoped, ""); got.Problem != "self" {
		t.Errorf("self: %+v", got)
	}
	f.stageErr = errors.New("boom")
	cases[at(delivery.Candidates{UpstreamStage: &delivery.StageRef{Namespace: "test", Name: "pay"}})] = "{Kind:stage Namespace:test Stage:pay Environment: Problem:unreadable Stream: Repository: Trust:}"
	for got, want := range cases {
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}
}

// B2: every Stage is counted in exactly one state, worst first, per
// environment in the declared order; environment, tier and state narrow the
// rows, never the tally.
func TestEnvironmentsCountByEnvironment(t *testing.T) {
	f := sampleCluster("acme:team-checkout")
	f.stages[0].Spec.Candidates.UpstreamStage = &delivery.StageRef{Namespace: "test", Name: "payments"}
	f.stages = append(f.stages,
		stage("test", "payments", "payments", delivery.StageStatus{Pointer: dRun, Verified: []string{dRun}}),
		stage("test", "ledger", "ledger", delivery.StageStatus{Pointer: dRun}),
		stage("test", "orders", "orders", delivery.StageStatus{}),
		stage("test", "paused", "paused", delivery.StageStatus{Pointer: dRun, AutoHold: true}),
		stage("books", "failing", "failing", delivery.StageStatus{Pointer: dNext, FailedDigests: []string{dNext}}),
		stage("books", "held", "held", delivery.StageStatus{Pointer: dRun}))
	f.ops = append(f.ops, operation("books", "waits", "held", held()))
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	want := "books:3 failed:1 frozen:1 waiting:1 other:1 frozen:1 test:4 autohold:1 unverified:1 empty:1 verified:1 prod:1 frozen:1"
	tally := func(envs map[string]any) string {
		var out []string
		for _, e := range page(envs["counts"])["byEnvironment"].([]any) {
			c := page(e)
			out = append(out, fmt.Sprintf("%v:%v", c["environment"], c["stages"]))
			// Stage states (B2), worst first: a Stage is counted in the first that
			// holds, so the counts of an environment sum to its Stages.
			for _, k := range []string{"failed", "frozen", "autohold", "waiting", "unverified", "empty", "verified"} {
				key := map[string]string{"autohold": "autoHold"}[k]
				if key == "" {
					key = k
				}
				if n := c[key].(float64); n > 0 {
					out = append(out, fmt.Sprintf("%s:%v", k, n))
				}
			}
		}
		return strings.Join(out, " ")
	}
	_, envs := call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	expect(t, tally(envs) == want, "per environment, test before prod as declared:\n%s\nwant\n%s", tally(envs), want)
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments?state=failed&environment=books", nil)
	rows := envs["environments"].([]any)
	expect(t, len(rows) == 1 && page(rows[0])["stage"] == "failing" && tally(envs) == want, "state narrows the rows, not the tally: %v", rows)
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments?state=unverified", nil)
	rows = envs["environments"].([]any)
	expect(t, len(rows) == 1 && page(rows[0])["stage"] == "ledger", "a Stage counts in one state only: %v", rows)
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments?except=test&except=books", nil)
	rows = envs["environments"].([]any)
	expect(t, len(rows) == 2 && tally(envs) == want, "except narrows the rows to the other environments, not the tally: %v", rows)
	for _, r := range rows {
		expect(t, page(r)["environment"] != "test" && page(r)["environment"] != "books", "no Stage of a left-out environment: %v", r)
	}
	code, _ := call(t, http.MethodGet, base+"/api/delivery/environments?state=nope", nil)
	expect(t, code == http.StatusUnprocessableEntity, "an unknown state is refused: %d", code)
	// Rows say where their candidates come from.
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments?service=payments", nil)
	for _, r := range envs["environments"].([]any) {
		u := page(page(r)["upstream"])
		if page(r)["environment"] == "prod" {
			expect(t, u["kind"] == "stage" && u["environment"] == "test", "prod is fed by test: %v", u)
		} else {
			expect(t, u["kind"] == "none", "test declares nothing: %v", u)
		}
	}
	g := page(envs["promotion"])
	expect(t, fmt.Sprint(g["edges"]) == "[map[from:test/payments to:prod/payments]]", "the service's own edge: %v", g["edges"])
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	expect(t, envs["promotion"] == nil, "no graph without a service: %v", envs["promotion"])
}

// SPEC 21: a service's own graph from its Stages' upstreams: a fork, an
// upstream in another cluster, an artifact entry, a Stage with no source, a
// dangling upstream and a cycle, each said, never ordered by name.
func TestPromotionGraph(t *testing.T) {
	node := func(env, upNS, upName string) delivery.StageObject { return fed(env, "svc", upNS, upName) }
	dev := node("dev", "", "")
	dev.Spec.Candidates.OCIRepositoryRef = &delivery.LocalRef{Name: "svc-candidates"}
	remote := node("dr", "", "")
	remote.Spec.Candidates.UpstreamRecords = &delivery.RemoteStage{Repository: "r", Stream: "c/n/s"}
	stages := []delivery.StageObject{node("prod-us", "staging", "svc"), node("staging", "dev", "svc"), node("prod-eu", "staging", "svc"), dev,
		remote, node("lonely", "", ""), node("orphan", "nowhere", "svc"), node("a", "b", "svc"), node("b", "a", "svc")}
	g := promotionGraph(stages, delivery.StagesByName(stages), Listing{everywhere: true})
	var got []string
	for _, n := range g.Nodes {
		s := fmt.Sprintf("%s@%d:%s", n.Environment, n.Level, n.Upstream.Kind)
		if n.Upstream.Problem != "" {
			s += "/" + n.Upstream.Problem
		}
		if n.Cycle {
			s += "/cycle"
		}
		got = append(got, s)
	}
	want := "dev@0:artifact dr@0:records lonely@0:none orphan@0:stage/missing staging@1:stage prod-eu@2:stage prod-us@2:stage a@0:stage/cycle b@1:stage/cycle"
	expect(t, strings.Join(got, " ") == want, "nodes:\n%s\nwant\n%s", strings.Join(got, " "), want)
	expect(t, fmt.Sprint(g.Edges) == "[{a/svc b/svc} {b/svc a/svc} {dev/svc staging/svc} {staging/svc prod-eu/svc} {staging/svc prod-us/svc}]", "edges: %v", g.Edges)
	// An upstream the caller may not list is unreadable, not missing.
	g = promotionGraph([]delivery.StageObject{node("prod", "hidden", "svc")}, nil, Listing{listed: []string{"prod"}})
	expect(t, g.Nodes[0].Upstream.Problem == "unreadable", "partial: %+v", g.Nodes[0])
	// One outside the dashboard's namespace is out of scope, never missing.
	g = promotionGraph([]delivery.StageObject{node("prod", "test", "svc")}, nil, Listing{listed: []string{"prod"}, scope: "prod"})
	expect(t, g.Nodes[0].Upstream.Problem == "outOfScope", "scoped: %+v", g.Nodes[0])
	// An upstream of another service names its environment but draws no edge.
	other := fed("test", "other", "", "")
	g = promotionGraph([]delivery.StageObject{node("prod", "test", "other")}, delivery.StagesByName([]delivery.StageObject{other}), Listing{everywhere: true})
	expect(t, g.Nodes[0].Upstream.Environment == "test" && len(g.Edges) == 0, "outside the graph: %+v", g)
	// A Stage that names itself waits on itself: a cycle, never a feed from
	// another service.
	self := []delivery.StageObject{node("prod", "prod", "svc")}
	g = promotionGraph(self, delivery.StagesByName(self), Listing{everywhere: true})
	expect(t, g.Nodes[0].Upstream.Problem == "self" && g.Nodes[0].Cycle && len(g.Edges) == 0, "self: %+v", g)
}

// The newest succeeded promote on a service's Stages, with the Stage that
// offered it, for the figure's arc and its real times.
func TestLastPromotionIsTheNewestSucceeded(t *testing.T) {
	g := &PromotionGraph{Nodes: []PromotionNode{{Namespace: "test", Stage: "pay"}, {Namespace: "prod", Stage: "pay"}},
		Edges: []PromotionEdge{{From: "test/pay", To: "prod/pay"}}}
	done := func(ns, name, finished string, out delivery.Outcome) delivery.OperationObject {
		o := operation(ns, name, "pay", delivery.OperationStatus{Outcome: out, Times: delivery.Times{Started: "2026-11-08T10:00:00Z", Finished: finished}})
		return o
	}
	expect(t, lastPromotion(g, nil) == nil, "none succeeded")
	other := done("prod", "u", "2026-11-08T13:00:00Z", delivery.Succeeded)
	other.Spec.Type = "delivery.pacto.trianalab.io/unfreeze"
	stray := done("books", "s", "2026-11-08T14:00:00Z", delivery.Succeeded)
	ops := []delivery.OperationObject{
		done("test", "a", "2026-11-08T11:00:00Z", delivery.Succeeded),
		done("prod", "b", "2026-11-08T12:00:00Z", delivery.Succeeded),
		done("prod", "c", "2026-11-08T12:30:00Z", delivery.Failed),
		done("prod", "d", "", delivery.Succeeded),
		done("test", "e", "2026-11-08T11:30:00Z", delivery.Succeeded),
		other, stray,
	}
	got := lastPromotion(g, ops)
	want := LastPromotion{To: "prod/pay", From: "test/pay", Digest: dNext, Started: "2026-11-08T10:00:00Z", Finished: "2026-11-08T12:00:00Z"}
	expect(t, got != nil && *got == want, "%+v", got)
	ops[1].Status.Pointer.After = dRun
	expect(t, lastPromotion(g, ops).Digest == dRun, "the pointer it moved wins over the requested digest")
	expect(t, lastPromotion(g, ops[:1]).From == "", "an entry has no feeder")
}
