package dashboard

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// atHistory is a complete, verified records history that moved its Stage to
// dRun on the second of November.
func atHistory() *delivery.History {
	return &delivery.History{Source: delivery.SourceRecords, Verified: true, Completeness: "complete", Entries: []delivery.HistoryEntry{
		{Seq: 1, Operation: "ship", Record: "sha256:r1", Kind: delivery.RecordOperation, Outcome: delivery.Succeeded,
			Applied: "2026-11-02T00:00:00Z", Pointer: &delivery.PointerChange{After: dRun}},
		{Seq: 2, Operation: "later", Record: "sha256:r2", Kind: delivery.RecordOperation, Outcome: delivery.Succeeded,
			Applied: "2026-11-09T00:00:00Z", Pointer: &delivery.PointerChange{After: dNext}}}}
}

func atStages(body map[string]any) []map[string]any {
	var out []map[string]any
	for _, s := range page(body["at"])["stages"].([]any) {
		out = append(out, page(s))
	}
	return out
}

func TestHistoryAt(t *testing.T) {
	f := sampleCluster("acme:team-payments")
	f.history, f.owner, f.businessDomain = atHistory(), "team-payments", "money"
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	_, h := call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments&at=2026-11-05T00:00:00Z", nil)
	ss := atStages(h)
	expect(t, len(ss) == 1 && ss[0]["state"] == "known" && ss[0]["digest"] == dRun && ss[0]["version"] == "1.0.0" &&
		ss[0]["owner"] == "team-payments" && ss[0]["businessDomain"] == "money" && ss[0]["record"] == "sha256:r1", "at %v", ss)
	expect(t, page(h["at"])["completeness"] == "complete" && page(h["at"])["at"] == "2026-11-05T00:00:00Z", "one Stage, known: %v", h["at"])
	one := page(h["histories"].([]any)[0])
	expect(t, page(one["entriesPage"])["total"] == float64(1), "the history keeps only changes at or before at: %v", one["entriesPage"])
	_, h = call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments", nil)
	expect(t, h["at"] == nil, "no at, no answer: %v", h["at"])
	// Every Stage the listing reads, before any record: unknown.
	_, h = call(t, http.MethodGet, base+"/api/delivery/history?at=2026-10-01T00:00:00Z", nil)
	ss = atStages(h)
	expect(t, len(ss) == 3 && ss[0]["state"] == "unknown" && ss[0]["reason"] == "no record before this time" &&
		page(h["at"])["completeness"] == "partial", "unknown before any record: %v", h["at"])
	for _, q := range []string{"at=soon", "at=1h&since=2026-11-01T00:00:00Z", "at=1h&until=2026-11-01T00:00:00Z", "at=1h&timed=true",
		"at=1h&cause=x", "at=1h&correlationID=INC-1"} {
		code, body := call(t, http.MethodGet, base+"/api/delivery/history?"+q, nil)
		expect(t, code == http.StatusBadRequest && strings.Contains(fmt.Sprint(body), "at "), "%s: %d %v", q, code, body)
	}
}

func TestHistoryAtForbiddenNamespace(t *testing.T) {
	f := sampleCluster("acme:team-payments")
	f.history, f.forbidAll, f.forbid, f.stageNS = atHistory(), true, map[string]bool{"books": true}, []string{"prod", "books"}
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	_, h := call(t, http.MethodGet, base+"/api/delivery/history?at=2026-11-05T00:00:00Z", nil)
	ss, at := atStages(h), page(h["at"])
	expect(t, len(ss) == 1 && ss[0]["state"] == "known", "the readable Stage is known: %v", ss)
	unread, _ := at["unreadable"].([]any)
	expect(t, at["completeness"] == "partial" && len(unread) == 1 && strings.Contains(fmt.Sprint(unread[0]), "books") &&
		strings.Contains(at["note"].(string), "others were not read"), "a refused namespace makes it partial: %v", at)
	// The Stage list itself refused: the whole answer is refused.
	f.stageNS, f.forbid = nil, nil
	f.nsErr = Refuse(http.StatusForbidden, "Forbidden", "namespaces is forbidden")
	code, _ := call(t, http.MethodGet, base+"/api/delivery/history?at=2026-11-05T00:00:00Z", nil)
	expect(t, code == http.StatusForbidden, "a refused listing is a refusal, never an empty answer: %d", code)
}

func TestHistoryAtPagedPartial(t *testing.T) {
	f := &fakeDelivery{caller: person(), history: atHistory()}
	for n := range historyStagesPage + 1 {
		f.stages = append(f.stages, stage("prod", fmt.Sprintf("s-%02d", n), "payments", delivery.StageStatus{}))
	}
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	_, h := call(t, http.MethodGet, base+"/api/delivery/history?at=2026-11-05T00:00:00Z", nil)
	at := page(h["at"])
	expect(t, len(atStages(h)) == historyStagesPage && at["completeness"] == "partial" && strings.Contains(at["note"].(string), "others were not read"),
		"the first of two pages is partial: %v", at)
	_, h = call(t, http.MethodGet, base+"/api/delivery/history?at=2026-11-05T00:00:00Z&offset=25", nil)
	expect(t, page(h["at"])["completeness"] == "partial", "the last page is partial too: %v", h["at"])
}

func TestHistoryAtRevisionFromFleet(t *testing.T) {
	f := sampleCluster("acme:team-payments")
	f.history, f.contract = atHistory(), "oci://r/payments@"+dContract
	auth := (&authed{state: AuthState{SignedIn: true}}).auth(false, true)
	fl := revisionFleet(t, contractRev("payments", dContract))
	base := startDeliveryWith(t, f, Options{Fleet: fl, Auth: auth}, true, nil)
	_, h := call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments&at=2026-11-05T00:00:00Z", nil)
	expect(t, atStages(h)[0]["revision"] == revisionKeyOf(t, fl, "payments"), "the fleet's revision: %v", atStages(h))
	// Another service's revision is never linked, and the derived key is never sent.
	base = startDeliveryWith(t, f, Options{Fleet: revisionFleet(t, contractRev("ledger", dContract)), Auth: auth}, true, nil)
	_, h = call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments&at=2026-11-05T00:00:00Z", nil)
	expect(t, atStages(h)[0]["revision"] == nil, "no fleet revision: %v", atStages(h))
	base = startDeliveryWith(t, f, Options{Auth: auth}, true, nil)
	_, h = call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments&at=2026-11-05T00:00:00Z", nil)
	expect(t, atStages(h)[0]["revision"] == nil, "no fleet: %v", atStages(h))
}
