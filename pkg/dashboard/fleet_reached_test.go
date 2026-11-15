package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// reachedFake is the sample cluster whose payments Stage moved to dRun,
// whose contract is the fleet's payments revision, by a signed record.
func reachedFake() *fakeDelivery {
	f := sampleCluster("acme:team-payments")
	f.contract = "oci://r/payments@" + dContract
	f.history = &delivery.History{Stage: delivery.StageNow{Name: "payments"}, Source: "records", Verified: true, Entries: []delivery.HistoryEntry{{Operation: "ship",
		Kind: delivery.RecordOperation, Record: "rec-1", Outcome: delivery.Succeeded, Pointer: &delivery.PointerChange{After: dRun},
		Applied: "2026-11-01T00:00:00Z"}}}
	return f
}

// startReached serves f over a fleet holding the payments revision.
func startReached(t *testing.T, f *fakeDelivery) string {
	t.Helper()
	return startDeliveryWith(t, f, Options{Fleet: revisionFleet(t, contractRev("payments", dContract)),
		Auth: (&authed{state: AuthState{SignedIn: true}}).auth(false, true)}, true, nil)
}

// entityChanges is the payments service's changes as base answers them.
func entityChanges(t *testing.T, base, query string) fleet.ChangeHistoryPreview {
	t.Helper()
	resp, err := http.Get(base + "/api/fleet/entities/service?key=payments" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	var out struct {
		Service struct {
			Changes fleet.ChangeHistoryPreview `json:"changes"`
		} `json:"service"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, err)
	}
	return out.Service.Changes
}

func TestEntityReachedUsesCallerDelivery(t *testing.T) {
	h := entityChanges(t, startReached(t, reachedFake()), "&reached=true")
	expect(t, h.Times == fleet.TimesRead && len(h.Items) > 0, "changes %+v", h)
	want := fleet.ChangeReach{Namespace: "prod", Stage: "payments", At: "2026-11-01T00:00:00Z", Record: "rec-1", Verified: true}
	expect(t, len(h.Items[0].Reached) == 1 && h.Items[0].Reached[0] == want, "reached %+v", h.Items[0].Reached)
}

func TestEntityReachedNotCached(t *testing.T) {
	base := startReached(t, reachedFake())
	entityChanges(t, base, "&reached=true")
	h := entityChanges(t, base, "")
	expect(t, h.Times == fleet.TimesNotRead && h.Items[0].Reached == nil, "a later answer without reached carries none: %+v", h)
}

func TestEntityReachedForbiddenHidden(t *testing.T) {
	f := reachedFake()
	f.forbidAll, f.nsErr = true, Refuse(http.StatusForbidden, "Forbidden", "namespaces is forbidden")
	h := entityChanges(t, startReached(t, f), "&reached=true")
	expect(t, h.Times == fleet.TimesHidden && strings.Contains(h.TimesReason, "forbidden"), "changes %+v", h)
}

// A caller allowed to list Stages in no namespace (cluster-wide refused, every
// namespace refused or none allowed) reads hidden, never "time not recorded".
func TestEntityReachedNoNamespaceAllowedHidden(t *testing.T) {
	for name, set := range map[string]func(*fakeDelivery){
		"none allowed":  func(f *fakeDelivery) { f.stageNS = []string{} },
		"each refused":  func(f *fakeDelivery) { f.stageNS, f.forbid = []string{"prod"}, map[string]bool{"prod": true} },
		"guess refused": func(f *fakeDelivery) { f.stageNS, f.forbid = nil, map[string]bool{"prod": true} },
	} {
		f := reachedFake()
		f.forbidAll = true
		set(f)
		h := entityChanges(t, startReached(t, f), "&reached=true")
		expect(t, h.Times == fleet.TimesHidden && strings.Contains(h.Lines()[0], "time hidden"), "%s: changes %+v lines %q", name, h, h.Lines())
	}
}

func TestReachedHiddenKeepsEventCount(t *testing.T) {
	f := reachedFake()
	base := startReached(t, f)
	plain := entityChanges(t, base, "")
	f.forbidAll, f.nsErr = true, Refuse(http.StatusForbidden, "Forbidden", "namespaces is forbidden")
	hidden := entityChanges(t, base, "&reached=true")
	expect(t, hidden.Times == fleet.TimesHidden && hidden.Total == plain.Total && len(hidden.Items) == len(plain.Items), "hidden %+v plain %+v", hidden, plain)
}

func TestEntityReachedPartialNamespaces(t *testing.T) {
	f := reachedFake()
	f.forbidAll, f.stageNS, f.forbid = true, []string{"prod", "books"}, map[string]bool{"books": true}
	h := entityChanges(t, startReached(t, f), "&reached=true")
	expect(t, h.Times == fleet.TimesPartial && len(h.TimesUnreadable) == 1 && strings.Contains(h.TimesUnreadable[0], "books"), "changes %+v", h)
	expect(t, len(h.Items[0].Reached) == 1, "what was read still counts: %+v", h.Items[0])
	// A Stage whose history cannot be read is named too.
	f = reachedFake()
	f.historyErr = errors.New("records down")
	h = entityChanges(t, startReached(t, f), "&reached=true")
	expect(t, h.Times == fleet.TimesPartial && strings.Contains(h.TimesUnreadable[0], "prod/payments: records down"), "changes %+v", h)
}

func TestReachedPartialNamespacesNotRecordedWording(t *testing.T) {
	f := reachedFake()
	f.forbidAll, f.stageNS, f.forbid = true, []string{"prod", "books"}, map[string]bool{"books": true}
	f.history = &delivery.History{}
	h := entityChanges(t, startReached(t, f), "&reached=true")
	expect(t, strings.Contains(h.Lines()[0], "time not recorded in the Stages read ("), "lines %q", h.Lines())
}

func TestEntityReachedDeliveryForErrorNotRead(t *testing.T) {
	base := startServer(t, NewServer(Options{UI: testUI(), Fleet: revisionFleet(t, contractRev("payments", dContract)),
		DeliveryInstalled: func(context.Context) (bool, error) { return false, errors.New("connection refused") },
		Delivery:          func(*http.Request) (DeliveryAPI, error) { return reachedFake(), nil }}))
	h := entityChanges(t, base, "&reached=true")
	expect(t, h.Times == fleet.TimesNotRead && strings.Contains(h.TimesReason, "connection refused"), "changes %+v", h)
	// Any other list error reads nothing either.
	f := reachedFake()
	f.listErr = errors.New("apiserver down")
	h = entityChanges(t, startReached(t, f), "&reached=true")
	expect(t, h.Times == fleet.TimesNotRead && strings.Contains(h.TimesReason, "apiserver down"), "changes %+v", h)
}

func TestEntityReachedNoDeliveryHost(t *testing.T) {
	base := startServer(t, NewServer(Options{UI: testUI(), Fleet: revisionFleet(t, contractRev("payments", dContract))}))
	h := entityChanges(t, base, "&reached=true")
	expect(t, h.Times == fleet.TimesNotRead && strings.Contains(h.TimesReason, "no cluster"), "changes %+v", h)
}
