package dashboard

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// withLadder makes ladderOf answer from ladders (by Stage name) and
// uncontractedOf with callers, for one test.
func withLadder(t *testing.T, ladders map[string]stageLadder, callers []uncontractedCaller) {
	t.Helper()
	l, u := ladderOf, uncontractedOf
	t.Cleanup(func() { ladderOf, uncontractedOf = l, u })
	ladderOf = func(st delivery.StageObject) stageLadder { return ladders[st.Metadata.Name] }
	uncontractedOf = func(*fleet.FleetSnapshot) []uncontractedCaller { return callers }
}

// ladder reads one from its wire form, the way the cluster writes it.
func ladder(t *testing.T, wire string) stageLadder {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(wire), &m); err != nil {
		t.Fatal(err)
	}
	return rewire[stageLadder](m)
}

func TestLadderReadsTheWireNames(t *testing.T) {
	lad := ladder(t, `{"rung":"advise","proposal":{"rung":"auto-nonbreaking","evidence":["sha256:a"]},
		"shadow":[{"candidate":"sha256:c","predicted":"Held","owners":["team-checkout"],"observedAt":"2026-10-25T10:00:00Z"}],
		"stream":{"concluded":[{"uid":"u","type":"delivery.pacto.trianalab.io/promote","person":true,"outcome":"Succeeded"}]}}`)
	expect(t, lad.Rung == "advise" && lad.Proposal.Rung == "auto-nonbreaking" && lad.Shadow[0].Owners[0] == "team-checkout" &&
		lad.Stream.Concluded[0].Person && lad.Stream.Concluded[0].Outcome == "Succeeded", "ladder %+v", lad)
	// Today's typed status carries none of it, and a snapshot no caller.
	expect(t, ladderOf(stage("prod", "p", "p", delivery.StageStatus{Pointer: dRun})).Rung == "", "no rung from today's status")
	expect(t, uncontractedOf(&fleet.FleetSnapshot{}) == nil, "no uncontracted caller from today's snapshot")
	got := uncontractedOf(&fleet.FleetSnapshot{UncontractedCallers: []fleet.UncontractedCaller{{Caller: "k8s:prod/Deployment/orders", ToService: "payments"}}})
	expect(t, len(got) == 1 && got[0] == uncontractedCaller{Caller: "k8s:prod/Deployment/orders", ToService: "payments"}, "the snapshot's uncontracted caller")
}

func TestAutonomy(t *testing.T) {
	promote := func(person bool, outcome string) string {
		p := "false"
		if person {
			p = "true"
		}
		return `{"type":"delivery.pacto.trianalab.io/promote","person":` + p + `,"outcome":"` + outcome + `"}`
	}
	withLadder(t, map[string]stageLadder{
		"payments": ladder(t, `{"rung":"observe","proposal":{"blockers":[
			{"kind":"unlabelled-apply-ref","subject":"payments","message":"label the applyRef"},
			{"kind":"undeclared-range","subject":"checkout","message":"checkout declares no range"}]},
			"shadow":[{"candidate":"sha256:new","predicted":"Held"},{"candidate":"sha256:old","predicted":"Proceeds"}]}`),
		"ledger": ladder(t, `{"rung":"advise","proposal":{"rung":"auto-nonbreaking","evidence":["u1"],"blockers":[
			{"kind":"undeclared-range","subject":"checkout","message":"checkout declares no range"},
			{"kind":"missing-contract","subject":"k8s:pay/Deployment/legacy"}]},
			"stream":{"concluded":[`+promote(false, "Succeeded")+`,`+promote(true, "Succeeded")+`,`+promote(false, "Failed")+
			`,{"type":"delivery.pacto.trianalab.io/freeze","outcome":"Succeeded"}]}}`),
	}, []uncontractedCaller{{Caller: "k8s:pay/Deployment/legacy", ToService: "ledger"}, {Caller: "k8s:pay/Deployment/legacy", ToService: "payments"},
		{Caller: "k8s:web/CronJob/report", ToService: "payments"}})
	f := sampleCluster("acme:team-payments")
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	_, out := call(t, http.MethodGet, base+"/api/delivery/autonomy", nil)
	c := out["contracted"].(map[string]any)
	expect(t, c["services"] == float64(2) && c["uncontractedCallers"] == float64(2), "of the services seen: %v", c)
	p := out["promotionsWithoutPerson"].(map[string]any)
	expect(t, p["count"] == float64(1) && p["of"] == float64(2), "only succeeded promotes, no person: %v", p)
	stages := out["stages"].([]any)
	led, pay := stages[0].(map[string]any), stages[2].(map[string]any)
	expect(t, len(stages) == 3 && led["rung"] == "advise" && led["proposal"].(map[string]any)["rung"] == "auto-nonbreaking", "ledger %v", led)
	expect(t, pay["rung"] == "observe" && pay["lastShadow"].(map[string]any)["candidate"] == "sha256:new", "payments %v", pay)
	_, hasRung := stages[1].(map[string]any)["rung"]
	expect(t, !hasRung, "a Stage that reports no rung says none: %v", stages[1])
	var got []string
	for _, b := range out["blockers"].([]any) {
		m := b.(map[string]any)
		got = append(got, m["kind"].(string)+" "+m["subject"].(string))
	}
	expect(t, strings.Join(got, ",") == "missing-contract k8s:pay/Deployment/legacy,missing-contract k8s:web/CronJob/report,"+
		"undeclared-range checkout,unlabelled-apply-ref payments", "one blocker per kind and subject, sorted: %v", got)
	kinds, _ := json.Marshal(out["blockerKinds"])
	expect(t, string(kinds) == `[{"count":2,"kind":"missing-contract"},{"count":1,"kind":"undeclared-range"},{"count":1,"kind":"unlabelled-apply-ref"}]`,
		"how many of each kind, in page order: %s", kinds)
	bl := out["blockers"].([]any)
	legacy, rng := bl[0].(map[string]any), bl[2].(map[string]any)
	expect(t, legacy["fix"].(map[string]any)["command"] == "pacto init --cluster --namespace pay --out ./pacto-drafts" &&
		strings.Join(toStrings(legacy["stages"]), ",") == "books/ledger", "a missing contract copies pacto init: %v", legacy)
	snip := rng["fix"].(map[string]any)["snippet"].(string)
	expect(t, strings.Count(snip, "- name: ledger\n") == 1 && strings.Count(snip, "- name: payments\n") == 1 &&
		strings.Contains(snip, `compatibility: "<the payments versions checkout accepts>"`) &&
		strings.Join(toStrings(rng["stages"]), ",") == "books/ledger,prod/payments", "a range copies one entry per provider: %v", rng)
	_, hasFix := bl[3].(map[string]any)["fix"]
	expect(t, !hasFix, "a blocker with no fix to copy carries none: %v", bl[3])

	// Environments show the rung, and the would-have table on observe only.
	_, envs := call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	rows := envs["environments"].([]any)
	expect(t, rows[2].(map[string]any)["rung"] == "observe" && len(rows[2].(map[string]any)["shadow"].([]any)) == 2, "observe row %v", rows[2])
	_, ledShadow := rows[0].(map[string]any)["shadow"]
	expect(t, rows[0].(map[string]any)["rung"] == "advise" && !ledShadow, "advise row %v", rows[0])
	// The change view names the Stage's rung, and nothing when the Stage cannot be read.
	_, op := call(t, http.MethodGet, base+"/api/delivery/operations/prod/for-checkout", nil)
	expect(t, op["rung"] == "observe", "change rung %v", op["rung"])
	f.stageErr = errors.New("forbidden")
	_, op = call(t, http.MethodGet, base+"/api/delivery/operations/prod/for-checkout", nil)
	_, has := op["rung"]
	expect(t, !has, "no rung from an unreadable Stage: %v", op)
}

func TestAutonomyPartialAndRefused(t *testing.T) {
	withLadder(t, nil, nil)
	a := &authed{state: AuthState{SignedIn: true}}
	f := &fakeDelivery{caller: person(), forbidAll: true, forbid: map[string]bool{"books": true},
		stages: []delivery.StageObject{stage("prod", "payments", "payments", delivery.StageStatus{}), stage("books", "ledger", "ledger", delivery.StageStatus{})}}
	base := startDelivery(t, f, a.auth(false, true), true, nil)
	_, out := call(t, http.MethodGet, base+"/api/delivery/autonomy", nil)
	expect(t, out["completeness"] == "partial" && len(out["stages"].([]any)) == 1 && len(out["blockers"].([]any)) == 0, "partial %v", out)
	// No fleet snapshot: no share to show, never a zero.
	noFleet := startServer(t, NewServer(Options{UI: testUI(), Auth: a.auth(false, true),
		Delivery: func(*http.Request) (DeliveryAPI, error) { return f, nil }}))
	_, out = call(t, http.MethodGet, noFleet+"/api/delivery/autonomy", nil)
	_, has := out["contracted"]
	expect(t, !has, "no snapshot, no share: %v", out)
	f.forbidAll, f.listErr = false, errors.New("boom")
	if code, _ := call(t, http.MethodGet, base+"/api/delivery/autonomy", nil); code != http.StatusInternalServerError {
		t.Fatalf("list error: %d", code)
	}
	base = startDelivery(t, f, a.auth(false, true), true, Refuse(http.StatusUnauthorized, "LoginRequired", "sign in"))
	if code, _ := call(t, http.MethodGet, base+"/api/delivery/autonomy", nil); code != http.StatusUnauthorized {
		t.Fatalf("refused: %d", code)
	}
}

// On today's typed status a concluded operation carries no type, so Pacto
// cannot tell a promotion from a freeze: it counts those apart, never as
// "no promotion succeeded".
func TestAutonomyCountsUntypedOperationsApart(t *testing.T) {
	f := &fakeDelivery{caller: person(), stages: []delivery.StageObject{stage("prod", "payments", "payments",
		delivery.StageStatus{Stream: delivery.Stream{Concluded: []delivery.Concluded{
			{UID: "a", Seq: 1, Outcome: delivery.Succeeded}, {UID: "b", Seq: 2, Outcome: delivery.Errored}}}})}}
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	_, out := call(t, http.MethodGet, base+"/api/delivery/autonomy", nil)
	p := out["promotionsWithoutPerson"].(map[string]any)
	expect(t, p["untyped"] == float64(2) && p["of"] == float64(0) && p["count"] == float64(0), "untyped counted apart: %v", p)
}

func TestFixFor(t *testing.T) {
	expect(t, fixFor("missing-contract", "payments", "", nil).Command == "pacto init --cluster --out ./pacto-drafts", "a service name")
	expect(t, fixFor("undeclared-range", "checkout", "", nil) == nil, "no provider, no snippet")
	expect(t, fixFor("incident", "x", "payments", nil) == nil, "nothing to copy for an incident")
}
