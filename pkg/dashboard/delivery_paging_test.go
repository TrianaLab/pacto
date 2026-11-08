package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

func TestPageOf(t *testing.T) {
	all := []int{1, 2, 3, 4, 5}
	items, p := pageOf(all, 0, 0, 2)
	expect(t, fmt.Sprint(items) == "[1 2]" && p.Total == 5 && p.Limit == 2 && *p.NextOffset == 2, "the default page: %v %+v", items, p)
	items, p = pageOf(all, 4, 2, 10)
	expect(t, fmt.Sprint(items) == "[5]" && p.Offset == 4 && p.NextOffset == nil, "the last page: %v %+v", items, p)
	items, p = pageOf(all, 99, 2, 10)
	expect(t, fmt.Sprint(items) == "[5]" && p.Offset == 4, "past the end is the last page, never a dead end: %v %+v", items, p)
	items, p = pageOf([]int{}, 7, 0, 10)
	expect(t, len(items) == 0 && items != nil && p.Total == 0 && p.Offset == 0, "nothing to page: %v %+v", items, p)
	items, p = pageOf(all, 0, 500, 10)
	expect(t, len(items) == 5 && p.Limit == maxPage, "a limit is capped: %+v", p)
}

// Names give a tier label only; with no declared upstream they order by name,
// numbers in number order, never prod after test because of the words (SPEC 21).
func TestEnvironmentColumnsNeverOrderByTier(t *testing.T) {
	var stages []delivery.StageObject
	for _, e := range []string{"prod-us", "env-10", "staging", "env-2", "test", "Development", "prod-eu", "preprod", "env-02", "a1b", "a1"} {
		stages = append(stages, stage(e, "x", "x", delivery.StageStatus{}))
	}
	envs, tiers := environmentColumns(stages)
	expect(t, strings.Join(envs, ",") == "Development,a1,a1b,env-02,env-2,env-10,preprod,prod-eu,prod-us,staging,test", "name order: %v", envs)
	expect(t, tiers["preprod"] == "test" && tiers["Development"] == "dev" && tiers["a1"] == "", "inferred tiers: %v", tiers)
	expect(t, envTier("PROD") == "prod" && envTier("books") == "", "tiers by name")
}

func TestMatches(t *testing.T) {
	expect(t, matches("", "x") && matches("  ", "x"), "no filter keeps everything")
	expect(t, matches("PAY", "ledger", "payments"), "a field contains it, ignoring case")
	expect(t, !matches("orders", "ledger", "payments"), "no field contains it")
}

// The change a rollback undid, ported from the dashboard's own rule.
func TestUndoneBy(t *testing.T) {
	d := func(n string) string { return "sha256:" + strings.Repeat(n, 64) }
	line := func(op, typ, from, to, finished string) delivery.HistoryEntry {
		return delivery.HistoryEntry{Operation: op, Type: "delivery.pacto.trianalab.io/" + typ, From: d(from), Finished: finished,
			Pointer: &delivery.PointerChange{Before: d(from), After: d(to)}}
	}
	rb := line("rb", "rollback", "2", "1", "t2")
	promote := line("op1", "promote", "1", "2", "t1")
	got := func(e *delivery.HistoryEntry) string {
		if e == nil {
			return ""
		}
		return e.Operation
	}
	expect(t, got(undoneBy([]delivery.HistoryEntry{promote, rb}, "op1", d("2"), "t1")) == "rb", "the rollback after it")
	noTime := rb
	noTime.Finished = ""
	expect(t, got(undoneBy([]delivery.HistoryEntry{noTime}, "op1", d("2"), "t1")) == "rb", "a line with no time")
	expect(t, got(undoneBy([]delivery.HistoryEntry{rb}, "op1", d("2"), "")) == "rb", "a change with no time")
	earlier := rb
	earlier.Finished = "t0"
	other := rb
	other.From = d("1")
	nameless := rb
	nameless.Operation = ""
	unmoved := rb
	unmoved.Pointer = nil
	for _, e := range []delivery.HistoryEntry{earlier, other, nameless, unmoved} {
		expect(t, undoneBy([]delivery.HistoryEntry{promote, e}, "op1", d("2"), "t1") == nil, "undid nothing: %+v", e)
	}
	// A moves to 4, B moves on to 5, C rolls B back to 4, D rolls C back to
	// 5: D starts from 4 but undid C, and A was never undone.
	abcd := []delivery.HistoryEntry{line("A", "promote", "3", "4", "t1"), line("B", "promote", "4", "5", "t2"),
		line("C", "rollback", "5", "4", "t3"), line("D", "rollback", "4", "5", "t4")}
	expect(t, undoneBy(abcd, "A", d("4"), "t1") == nil, "A moved on, not undone")
	expect(t, got(undoneBy(abcd, "B", d("5"), "t2")) == "C", "C undid B")
	expect(t, got(undoneBy(abcd, "C", d("4"), "t3")) == "D", "D undid C")
}

func page(v any) map[string]any { return v.(map[string]any) }

// needs is a board answer's failed, frozen and waiting counts.
func needs(envs map[string]any) string {
	c := page(envs["counts"])
	return fmt.Sprint(c["failed"], c["frozen"], c["waiting"])
}

// The environments board pages services on the server, keeps every column on
// every page and filters with q, environment and tier.
func TestDeliveryEnvironmentsPage(t *testing.T) {
	f := sampleCluster("acme:team-checkout")
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)

	_, envs := call(t, http.MethodGet, base+"/api/delivery/environments?limit=1&offset=1", nil)
	p := page(envs["page"])
	expect(t, p["total"] == float64(3) && p["offset"] == float64(1) && p["nextOffset"] == float64(2), "services are paged: %v", p)
	rows := envs["environments"].([]any)
	expect(t, len(rows) == 1 && page(rows[0])["service"] == "orphan", "only the page's Stages: %v", rows)
	expect(t, strings.Join(toStrings(envs["environmentNames"]), ",") == "books,other,prod", "every column on every page, by name when no Stage declares an upstream: %v", envs["environmentNames"])
	expect(t, needs(envs) == "0 3 1", "the counts are over every page: %v", envs["counts"])
	f.stages[1].Status.Pointer = dNext // ledger runs the digest that failed there
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments?limit=1", nil)
	expect(t, needs(envs) == "1 3 1", "a Stage running a failed digest counts as failed: %v", envs["counts"])
	f.stages[1].Status.Pointer = ""
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments?q=LED", nil)
	expect(t, len(envs["environments"].([]any)) == 1 && strings.Join(toStrings(envs["environmentNames"]), ",") == "books", "q filters services: %v", envs)

	expect(t, fmt.Sprint(envs["environmentTiers"]) == "map[]", "q narrowed to books, which has no tier: %v", envs["environmentTiers"])
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments?environment=other", nil)
	rows = envs["environments"].([]any)
	expect(t, len(rows) == 1 && page(rows[0])["service"] == "orphan" && page(envs["page"])["total"] == float64(1), "only services with a Stage there: %v", envs)
	expect(t, strings.Join(toStrings(envs["environmentNames"]), ",") == "books,other,prod", "the columns to pick from stay: %v", envs["environmentNames"])
	expect(t, fmt.Sprint(envs["environmentTiers"]) == "map[prod:prod]", "the tiers names say: %v", envs["environmentTiers"])
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments?tier=prod", nil)
	rows = envs["environments"].([]any)
	expect(t, len(rows) == 1 && page(rows[0])["service"] == "payments", "only services with a prod Stage: %v", rows)
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments?tier=dev", nil)
	expect(t, len(envs["environments"].([]any)) == 0 && page(envs["page"])["total"] == float64(0), "no dev Stage: %v", envs)
	code, _ := call(t, http.MethodGet, base+"/api/delivery/environments?tier=nope", nil)
	expect(t, code == http.StatusUnprocessableEntity, "an unknown tier is refused: %d", code)

	f.stages[1].Status.Conditions = []delivery.Condition{{Type: "Ready", Status: "False", Reason: "NoCandidateSource", Message: "the Stage names no candidates"}}
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments?q=ledger", nil)
	led := page(envs["environments"].([]any)[0])
	expect(t, led["ready"] == "False: NoCandidateSource" && led["readyMessage"] == "the Stage names no candidates", "why it cannot promote, in words: %v", led)
}

// Every other long delivery list pages on the server, keeps the newest change
// first and filters with q.
func TestDeliveryListsPage(t *testing.T) {
	f := sampleCluster("acme:team-checkout")
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	_, pub := call(t, http.MethodGet, base+"/api/delivery/published?limit=2", nil)
	expect(t, len(pub["services"].([]any)) == 2 && page(pub["page"])["total"] == float64(3), "published services are paged: %v", pub["page"])
	_, pub = call(t, http.MethodGet, base+"/api/delivery/published?q=pay", nil)
	expect(t, len(pub["services"].([]any)) == 1 && page(pub["services"].([]any)[0])["service"] == "payments", "q filters services: %v", pub)

	_, inbox := call(t, http.MethodGet, base+"/api/delivery/inbox?limit=1&held=1", nil)
	expect(t, names(inbox["held"]) == "anyone:accept-unknown" && page(inbox["heldPage"])["total"] == float64(2), "held is paged: %v", inbox)
	expect(t, page(inbox["frozenPage"])["total"] == float64(1) && page(inbox["erroredPage"])["total"] == float64(1), "each section has its page: %v", inbox)
	expect(t, inbox["heldShared"] == float64(0), "no held change names more than one team: %v", inbox["heldShared"])
	_, inbox = call(t, http.MethodGet, base+"/api/delivery/inbox?q=orphan", nil)
	expect(t, len(inbox["held"].([]any)) == 0 && len(inbox["frozen"].([]any)) == 1, "q filters every section: %v", inbox)

	_, au := call(t, http.MethodGet, base+"/api/delivery/autonomy?limit=1&q=pay", nil)
	expect(t, len(au["stages"].([]any)) == 1 && page(au["page"])["total"] == float64(1), "Stages are paged and filtered: %v", au)
	expect(t, page(au["blockersPage"])["total"] == float64(0), "blockers are filtered too: %v", au["blockersPage"])
	_, au = call(t, http.MethodGet, base+"/api/delivery/autonomy?limit=1", nil)
	expect(t, au["canMoveUp"] == float64(0), "counted over every page: %v", au["canMoveUp"])
}

// History across Stages pages the Stages and shows the newest changes of
// each; one Stage pages its changes, newest first.
func TestDeliveryHistoryPages(t *testing.T) {
	f := sampleCluster("acme:team-checkout")
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	var entries []delivery.HistoryEntry
	for i := 1; i <= 12; i++ {
		entries = append(entries, delivery.HistoryEntry{Seq: int64(i), Operation: fmt.Sprintf("op-%d", i), Actor: map[bool]string{true: "carol", false: "bot"}[i%2 == 0]})
	}
	f.history = &delivery.History{Stage: delivery.StageNow{Name: "payments"}, Completeness: "complete", Entries: entries,
		Problems: []string{"seq 3 is missing", "seq 5 is missing"}, Unreadable: []string{"sha256:x: not signed"}}
	_, h := call(t, http.MethodGet, base+"/api/delivery/history?limit=2", nil)
	hs := h["histories"].([]any)
	// The summary carries how many problems a Stage has, never the lists themselves.
	expect(t, page(hs[0])["problems"] == nil && page(hs[0])["unreadable"] == nil &&
		page(hs[0])["problemCount"] == float64(2) && page(hs[0])["unreadableCount"] == float64(1), "summary counts only: %v", hs[0])
	expect(t, len(hs) == 2 && page(h["page"])["total"] == float64(3) && page(hs[0])["namespace"] == "books", "Stages in order, paged: %v", h["page"])
	expect(t, fmt.Sprint(h["counts"]) == "map[frozen:3 waiting:1]", "the counts are over every page: %v", h["counts"])
	_, h = call(t, http.MethodGet, base+"/api/delivery/history?q=PAY", nil)
	expect(t, page(h["page"])["total"] == float64(1), "q filters Stages: %v", h["page"])
	first := page(hs[1])
	got := first["entries"].([]any)
	expect(t, len(got) == historyRecent && page(got[0])["operation"] == "op-12" && page(first["entriesPage"])["total"] == float64(12),
		"the newest changes of each Stage: %v", first["entriesPage"])
	_, h = call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments&limit=4&offset=4&q=carol", nil)
	one := page(h["histories"].([]any)[0])
	got = one["entries"].([]any)
	expect(t, h["counts"] == nil, "one Stage has no counts: %v", h["counts"])
	expect(t, len(one["problems"].([]any)) == 2 && len(one["unreadable"].([]any)) == 1 && one["problemCount"] == float64(2),
		"one Stage keeps its lists: %v", one)
	expect(t, len(got) == 2 && page(got[0])["operation"] == "op-4" && page(one["entriesPage"])["total"] == float64(6),
		"one Stage pages its changes, filtered, newest first: %v", one["entriesPage"])
}

// A concluded change says which rollback undid it, read from its Stage's
// history; a history that cannot be read leaves it out.
func TestOperationUndoneBy(t *testing.T) {
	done := delivery.OperationStatus{Phase: delivery.PhaseConcluded, Outcome: delivery.Failed, Applied: true,
		Pointer: delivery.PointerChange{Before: dRun, After: dNext}}
	f := &fakeDelivery{caller: person(), stages: []delivery.StageObject{stage("prod", "payments", "payments", delivery.StageStatus{})},
		ops: []delivery.OperationObject{operation("prod", "done", "payments", done)},
		history: &delivery.History{Entries: []delivery.HistoryEntry{{Operation: "done", From: dRun, Pointer: &delivery.PointerChange{After: dNext}},
			{Operation: "rb", Type: delivery.TypeRollback, From: dNext, Pointer: &delivery.PointerChange{After: dRun}}}}}
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	_, out := call(t, http.MethodGet, base+"/api/delivery/operations/prod/done", nil)
	expect(t, out["undoneBy"] != nil && page(out["undoneBy"])["operation"] == "rb", "undone by: %v", out["undoneBy"])
	f.historyErr = fmt.Errorf("gone")
	_, out = call(t, http.MethodGet, base+"/api/delivery/operations/prod/done", nil)
	_, has := out["undoneBy"]
	expect(t, !has, "an unreadable history says nothing: %v", out)
}

// A row that offers nothing asks no access review, so a reader's board costs
// none; a namespace RBAC refuses loses its actions.
func TestMayRaiseAsksOnlyForAnAction(t *testing.T) {
	f := &fakeDelivery{deny: map[string]bool{"create operations team-b": true}}
	rows := []EnvironmentRow{{Namespace: "team-a", Actions: []string{"promote"}}, {Namespace: "team-b", Actions: []string{"unfreeze"}},
		{Namespace: "team-c", Actions: []string{}}}
	mayRaise(context.Background(), f, rows)
	if len(rows[0].Actions) != 1 || len(rows[1].Actions) != 0 || f.cans.Load() != 2 {
		t.Errorf("rows %v, %d reviews", rows, f.cans.Load())
	}
}

// service is an exact match: one call returns exactly that service's Stages,
// however many services contain the name, and intersects with q.
func TestEnvironmentsServiceFilter(t *testing.T) {
	f := &fakeDelivery{caller: person(), stages: []delivery.StageObject{
		stage("prod", "orders", "orders", delivery.StageStatus{}),
		stage("dev", "orders", "orders", delivery.StageStatus{}),
		stage("prod", "orders-api", "orders-api", delivery.StageStatus{}),
	}}
	for n := range 250 {
		f.stages = append(f.stages, stage("prod", fmt.Sprintf("a-orders-%d", n), fmt.Sprintf("a-orders-%d", n), delivery.StageStatus{}))
	}
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)

	_, envs := call(t, http.MethodGet, base+"/api/delivery/environments?service=orders&limit=1", nil)
	rows := envs["environments"].([]any)
	expect(t, len(rows) == 2 && page(envs["page"])["total"] == float64(1), "only the orders Stages: %v", envs)
	for _, r := range rows {
		expect(t, page(r)["service"] == "orders", "never another service: %v", r)
	}
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments?service=orders&q=ORD", nil)
	expect(t, len(envs["environments"].([]any)) == 2, "service and a matching q intersect: %v", envs)
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments?service=orders&q=api", nil)
	expect(t, len(envs["environments"].([]any)) == 0 && page(envs["page"])["total"] == float64(0), "service and q intersect: %v", envs)
}

// TestEnvironmentsServiceDomainFilter: same-named services in two registry
// domains never share Stages; a Stage that names no domain matches either.
func TestEnvironmentsServiceDomainFilter(t *testing.T) {
	inDomain := func(env, domain string) delivery.StageObject {
		st := stage(env, "payments-"+env, "payments", delivery.StageStatus{})
		st.Spec.Service.Domain = domain
		return st
	}
	f := &fakeDelivery{caller: person(), stages: []delivery.StageObject{
		inDomain("a", "ghcr.io/a"), inDomain("b", "ghcr.io/b"), inDomain("any", ""),
	}}
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	for domain, want := range map[string][]string{"ghcr.io/a": {"a", "any"}, "ghcr.io/b": {"any", "b"}, "": {"any"}} {
		_, envs := call(t, http.MethodGet, base+"/api/delivery/environments?service=payments&domain="+url.QueryEscape(domain), nil)
		var got []string
		for _, r := range envs["environments"].([]any) {
			row := page(r)
			expect(t, row["domain"] == nil || row["domain"] == domain, "%q: row of another domain: %v", domain, row)
			got = append(got, row["environment"].(string))
		}
		slices.Sort(got)
		expect(t, slices.Equal(got, want), "%q: environments %v, want %v", domain, got, want)
	}
}

// History takes the same exact service and domain filter as the board: a
// longer name containing the service and another domain's Stage are left out,
// and a Stage that names no domain stays.
func TestDeliveryHistoryServiceFilter(t *testing.T) {
	inDomain := func(env, svc, domain string) delivery.StageObject {
		st := stage(env, svc+"-"+env, svc, delivery.StageStatus{})
		st.Spec.Service.Domain = domain
		return st
	}
	f := &fakeDelivery{caller: person(), stages: []delivery.StageObject{
		inDomain("a", "payments", "ghcr.io/a"), inDomain("b", "payments", "ghcr.io/b"), inDomain("any", "payments", ""),
		inDomain("a", "payments-api", "ghcr.io/a"),
	}}
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	for domain, want := range map[string][]string{"ghcr.io/a": {"a/payments-a", "any/payments-any"}, "": {"any/payments-any"}} {
		_, h := call(t, http.MethodGet, base+"/api/delivery/history?service=payments&domain="+url.QueryEscape(domain), nil)
		var got []string
		for _, x := range h["histories"].([]any) {
			got = append(got, page(page(x)["stage"])["name"].(string))
		}
		expect(t, slices.Equal(got, want) && page(h["page"])["total"] == float64(len(want)), "%q: Stages %v, want %v", domain, got, want)
	}
}

// since and until keep the changes that ran in the window, plus those with no
// time and those still in flight, before paging; a bad time is refused.
func TestDeliveryHistoryWindow(t *testing.T) {
	f := sampleCluster("acme:team-checkout")
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	f.history = &delivery.History{Stage: delivery.StageNow{Name: "payments"}, Completeness: "complete", Entries: []delivery.HistoryEntry{
		{Seq: 1, Operation: "before", Applied: "2026-11-01T08:00:00Z", Finished: "2026-11-01T09:00:00Z"},
		{Seq: 2, Operation: "timeless"},
		{Seq: 3, Operation: "inside", Applied: "2026-11-02T10:00:00Z", Finished: "2026-11-02T11:00:00Z"},
		{Seq: 4, Operation: "flight", Applied: "2026-11-01T08:00:00Z"},
		{Seq: 5, Operation: "after", Applied: "2026-11-04T10:00:00Z", Finished: "2026-11-04T11:00:00Z"},
	}}
	window := "&since=2026-11-02T00:00:00Z&until=2026-11-03T00:00:00Z"
	_, h := call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments"+window, nil)
	one := page(h["histories"].([]any)[0])
	var ops []string
	for _, e := range one["entries"].([]any) {
		ops = append(ops, page(e)["operation"].(string))
	}
	expect(t, strings.Join(ops, ",") == "flight,inside,timeless" && page(one["entriesPage"])["total"] == float64(3),
		"in-window, in-flight and timeless changes, newest first: %v %v", ops, one["entriesPage"])
	expect(t, strings.Contains(one["note"].(string), "only changes between"), "the note says the list is windowed: %v", one["note"])
	_, h = call(t, http.MethodGet, base+"/api/delivery/history?until=2026-11-03T00:00:00Z", nil)
	first := page(h["histories"].([]any)[0])
	expect(t, page(first["entries"].([]any)[0])["operation"] == "flight" && page(first["entriesPage"])["total"] == float64(4),
		"each Stage's newest change is the newest in the window: %v", first)
	code, body := call(t, http.MethodGet, base+"/api/delivery/history?since=yesterday", nil)
	expect(t, code == http.StatusBadRequest && strings.Contains(fmt.Sprint(body), "since"), "a bad since is refused: %d %v", code, body)
	code, body = call(t, http.MethodGet, base+"/api/delivery/history?until=soon", nil)
	expect(t, code == http.StatusBadRequest && strings.Contains(fmt.Sprint(body), "until"), "a bad until is refused: %d %v", code, body)
	code, body = call(t, http.MethodGet, base+"/api/delivery/history?since=2026-11-03T00:00:00Z&until=2026-11-02T00:00:00Z", nil)
	expect(t, code == http.StatusBadRequest && strings.Contains(fmt.Sprint(body), "since is after until"), "an inverted window is refused: %d %v", code, body)
}

// B7: without a stage, the timeline counts the Operations still in the
// cluster on the Stages that match, never one the caller cannot see; one
// Stage's timeline counts its whole history before q and paging.
func TestDeliveryHistoryTimeline(t *testing.T) {
	f := sampleCluster("acme:team-checkout")
	ago := func(h int) string { return time.Now().Add(-time.Duration(h) * time.Hour).UTC().Format(time.RFC3339) }
	for i := range f.ops {
		f.ops[i].Status.Times.Finished = ago(1)
	}
	f.ops = append(f.ops, operation("hidden", "unseen", "nowhere", delivery.OperationStatus{Phase: delivery.PhaseConcluded, Outcome: delivery.Failed,
		Times: delivery.Times{Finished: ago(1)}}))
	f.history = &delivery.History{Stage: delivery.StageNow{Name: "payments"}, Completeness: "complete", Entries: []delivery.HistoryEntry{
		{Operation: "a", Kind: delivery.RecordGap, Finished: ago(2)}, {Operation: "b", Outcome: delivery.Succeeded, Pointer: &delivery.PointerChange{After: dRun}, Finished: ago(3)}}}
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	_, h := call(t, http.MethodGet, base+"/api/delivery/history", nil)
	tl := page(h["timeline"])
	expect(t, len(tl["buckets"].([]any)) == delivery.TimelineBuckets, "twelve slices: %v", tl)
	tot := page(tl["totals"])
	// 3 held on payments, 2 errored on frozen Stages; the Operations on gone or
	// unseen Stages are not counted.
	expect(t, tot["held"] == float64(3) && tot["failed"] == float64(2) && tot["other"] == float64(0), "the cluster's changes: %v", tot)
	_, h = call(t, http.MethodGet, base+"/api/delivery/history?q=ledger", nil)
	expect(t, page(page(h["timeline"])["totals"])["failed"] == float64(1), "q narrows the timeline: %v", h["timeline"])
	_, h = call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments&q=nothing-matches", nil)
	one := page(h["histories"].([]any)[0])
	tot = page(page(one["timeline"])["totals"])
	expect(t, h["timeline"] == nil && tot["missing"] == float64(1) && tot["released"] == float64(1), "one Stage's timeline is its own, before q: %v", one["timeline"])
}
