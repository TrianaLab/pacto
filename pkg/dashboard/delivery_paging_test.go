package dashboard

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

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

func TestSortEnvironments(t *testing.T) {
	envs := []string{"prod-us", "env-10", "staging", "env-2", "test", "Development", "prod-eu", "preprod", "env-02", "a1b", "a1"}
	sortEnvironments(envs)
	expect(t, strings.Join(envs, ",") == "Development,test,staging,preprod,prod-eu,prod-us,a1,a1b,env-02,env-2,env-10", "the way changes travel: %v", envs)
	_, tier := envRank("PROD")
	_, none := envRank("books")
	expect(t, tier == "prod" && none == "", "tiers: %q %q", tier, none)
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
	expect(t, strings.Join(toStrings(envs["environmentNames"]), ",") == "prod,books,other", "every column on every page, prod before names it cannot place: %v", envs["environmentNames"])
	expect(t, fmt.Sprint(envs["counts"]) == "map[failed:0 frozen:3 waiting:1]", "the counts are over every page: %v", envs["counts"])
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments?q=LED", nil)
	expect(t, len(envs["environments"].([]any)) == 1 && strings.Join(toStrings(envs["environmentNames"]), ",") == "books", "q filters services: %v", envs)

	expect(t, fmt.Sprint(envs["environmentTiers"]) == "map[]", "q narrowed to books, which has no tier: %v", envs["environmentTiers"])
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments?environment=other", nil)
	rows = envs["environments"].([]any)
	expect(t, len(rows) == 1 && page(rows[0])["service"] == "orphan" && page(envs["page"])["total"] == float64(1), "only services with a Stage there: %v", envs)
	expect(t, strings.Join(toStrings(envs["environmentNames"]), ",") == "prod,books,other", "the columns to pick from stay: %v", envs["environmentNames"])
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
