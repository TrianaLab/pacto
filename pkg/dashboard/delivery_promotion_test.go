package dashboard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
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

func TestPromotionOrderFollowsDeclaredUpstreams(t *testing.T) {
	cases := []struct {
		name     string
		stages   []delivery.StageObject
		order    string
		upstream map[string][]string
		cycle    string
	}{
		{
			// Names alone would put prod before staging's "s" and qa first.
			name:     "a chain in no name order",
			stages:   []delivery.StageObject{fed("prod", "a", "staging", "a"), fed("qa", "a", "", ""), fed("staging", "a", "qa", "a")},
			order:    "[qa staging prod]",
			upstream: map[string][]string{"qa": {}, "staging": {"qa"}, "prod": {"qa", "staging"}},
			cycle:    "map[]",
		},
		{
			name: "a fork, and an upstream in the Stage's own namespace",
			stages: []delivery.StageObject{fed("dev", "a", "", ""), fed("prod-us", "a", "dev", "a"), fed("prod-eu", "a", "dev", "a"),
				{Metadata: delivery.ObjectMeta{Namespace: "dev", Name: "b"}, Spec: delivery.StageSpec{Environment: "dev-2",
					Candidates: delivery.Candidates{UpstreamStage: &delivery.StageRef{Name: "a"}}}}},
			order:    "[dev dev-2 prod-eu prod-us]",
			upstream: map[string][]string{"dev": {}, "dev-2": {"dev"}, "prod-eu": {"dev"}, "prod-us": {"dev"}},
			cycle:    "map[]",
		},
		{
			// c is free to go first; the cycle a <-> b is placed by name after
			// it and reported; 0, fed by b, comes after the cycle, not by name.
			name: "a dangling upstream orders nothing and a cycle is reported",
			stages: []delivery.StageObject{fed("b", "x", "a", "x"), fed("a", "x", "b", "x"), fed("c", "x", "nowhere", "x"),
				fed("0", "x", "b", "x")},
			order:    "[c a b 0]",
			upstream: map[string][]string{"0": {"a", "b"}, "a": {"b"}, "b": {"a"}, "c": {}},
			cycle:    "map[a:true b:true]",
		},
	}
	for _, c := range cases {
		order, up, cycle := promotionOrder(c.stages)
		for k, v := range up {
			if len(v) == 0 {
				up[k] = []string{}
			}
		}
		if fmt.Sprint(order) != c.order || !reflect.DeepEqual(up, c.upstream) || fmt.Sprint(cycle) != c.cycle {
			t.Errorf("%s: order %v upstream %v cycle %v, want %s %v %s", c.name, order, up, cycle, c.order, c.upstream, c.cycle)
		}
	}
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
}

func TestUpstreamOfReadsTheDeclaredSource(t *testing.T) {
	up := stage("test", "pay", "pay", delivery.StageStatus{})
	up.Spec.Environment = "staging"
	f := &fakeDelivery{stages: []delivery.StageObject{up}}
	at := func(c delivery.Candidates) string {
		st := stage("prod", "pay", "pay", delivery.StageStatus{})
		st.Spec.Candidates = c
		return fmt.Sprintf("%+v", *upstreamOf(context.Background(), f, &st))
	}
	cases := map[string]string{
		at(delivery.Candidates{UpstreamStage: &delivery.StageRef{Namespace: "test", Name: "pay"}}):          "{Kind:stage Namespace:test Stage:pay Environment:staging Problem: Stream: Repository:}",
		at(delivery.Candidates{UpstreamStage: &delivery.StageRef{Name: "gone"}}):                            "{Kind:stage Namespace:prod Stage:gone Environment: Problem:missing Stream: Repository:}",
		at(delivery.Candidates{UpstreamRecords: &delivery.RemoteStage{Repository: "r/s", Stream: "c/n/s"}}): "{Kind:records Namespace: Stage: Environment: Problem: Stream:c/n/s Repository:r/s}",
		at(delivery.Candidates{OCIRepositoryRef: &delivery.LocalRef{Name: "pay-candidates"}}):               "{Kind:artifact Namespace: Stage: Environment: Problem: Stream: Repository:pay-candidates}",
		at(delivery.Candidates{}): "{Kind:none Namespace: Stage: Environment: Problem: Stream: Repository:}",
	}
	f.stageErr = errors.New("boom")
	cases[at(delivery.Candidates{UpstreamStage: &delivery.StageRef{Namespace: "test", Name: "pay"}})] = "{Kind:stage Namespace:test Stage:pay Environment: Problem:unreadable Stream: Repository:}"
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
			for _, k := range stageStates {
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
	g := promotionGraph(stages, stagesByName(stages), true)
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
	g = promotionGraph([]delivery.StageObject{node("prod", "hidden", "svc")}, nil, false)
	expect(t, g.Nodes[0].Upstream.Problem == "unreadable", "partial: %+v", g.Nodes[0])
	// An upstream of another service names its environment but draws no edge.
	other := fed("test", "other", "", "")
	g = promotionGraph([]delivery.StageObject{node("prod", "test", "other")}, stagesByName([]delivery.StageObject{other}), true)
	expect(t, g.Nodes[0].Upstream.Environment == "test" && len(g.Edges) == 0, "outside the graph: %+v", g)
}
