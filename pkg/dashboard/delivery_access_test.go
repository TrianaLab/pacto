package dashboard

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// accessCluster holds one change for team-checkout on prod/payments.
func accessCluster() *fakeDelivery {
	return &fakeDelivery{caller: person("acme:team-checkout"), ops: []delivery.OperationObject{operation("prod", "held", "payments", held("team-checkout"))},
		stages: []delivery.StageObject{stage("prod", "payments", "payments", delivery.StageStatus{})}}
}

func TestDashboardDecideRefusesWithoutUpdateRights(t *testing.T) {
	f := accessCluster()
	f.access = map[string]bool{"prod/payments": false}
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	code, out := call(t, http.MethodPost, base+"/api/delivery/operations/prod/held/decision", map[string]string{"decision": "approve"})
	expect(t, code == http.StatusForbidden && out["code"] == "CannotUpdateWorkload" && len(f.approved) == 0 &&
		strings.HasPrefix(out["detail"].(string), "https://idp#carol cannot update deployments/payments in prod"), "denied: %d %v", code, out)
	f.access, f.accessErr = nil, "timeout"
	code, out = call(t, http.MethodPost, base+"/api/delivery/operations/prod/held/decision", map[string]string{"decision": "approve"})
	expect(t, code == http.StatusForbidden && out["code"] == "ApproverUnverified" && len(f.approved) == 0, "unverified: %d %v", code, out)
}

func TestOfferHiddenWithoutUpdateRights(t *testing.T) {
	f := accessCluster()
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	_, out := call(t, http.MethodGet, base+"/api/delivery/operations/prod/held", nil)
	expect(t, slices.Contains(toStrings(out["actions"]), "approve"), "allowed: %v", out["actions"])
	f.access = map[string]bool{"prod/payments": false}
	_, out = call(t, http.MethodGet, base+"/api/delivery/operations/prod/held", nil)
	expect(t, !slices.Contains(toStrings(out["actions"]), "approve") && !slices.Contains(toStrings(out["actions"]), "reject"), "denied: %v", out["actions"])
}

// TestOfferAccessMemoised: the change view asks one review for a held change
// and none for one that waits on nobody.
func TestOfferAccessMemoised(t *testing.T) {
	f := accessCluster()
	f.ops = append(f.ops, operation("prod", "done", "payments", delivery.OperationStatus{Phase: delivery.PhaseConcluded, Outcome: delivery.Succeeded}))
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	call(t, http.MethodGet, base+"/api/delivery/operations/prod/held", nil)
	expect(t, f.accesses.Load() == 1, "one review for the held change: %d", f.accesses.Load())
	call(t, http.MethodGet, base+"/api/delivery/operations/prod/done", nil)
	expect(t, f.accesses.Load() == 1, "none for a concluded change: %d", f.accesses.Load())
}

func TestInboxHidesHeldWithoutUpdateRights(t *testing.T) {
	f := accessCluster()
	f.ops = append(f.ops, operation("prod", "held-too", "payments", held("team-checkout")), operation("prod", "elsewhere", "gone", held("team-checkout")))
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	_, inbox := call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	expect(t, names(inbox["held"]) == "held:approve,held-too:approve", "a Stage missing from the listing is not held on the caller: %v", inbox["held"])
	expect(t, f.accesses.Load() == 1, "two held changes on one Stage ask one review: %d", f.accesses.Load())
	f.access = map[string]bool{"prod/payments": false}
	_, inbox = call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	expect(t, len(inbox["held"].([]any)) == 0, "the caller's team, but not their workload: %v", inbox["held"])
}

func TestUnfreezeOfferHiddenWithoutUpdateRights(t *testing.T) {
	f := sampleCluster("acme:team-payments")
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	unfreeze := func() map[string]any {
		t.Helper()
		_, envs := call(t, http.MethodGet, base+"/api/delivery/environments?service=payments", nil)
		for _, o := range rowOf(t, envs, "payments")["offers"].([]any) {
			if o.(map[string]any)["action"] == "unfreeze" {
				return o.(map[string]any)
			}
		}
		t.Fatalf("no unfreeze offer: %v", envs)
		return nil
	}
	o := unfreeze()
	expect(t, o["available"] == true, "allowed: %v", o)
	f.access = map[string]bool{"prod/payments": false}
	o = unfreeze()
	expect(t, o["available"] == false && strings.HasPrefix(o["reason"].(string), "you cannot update deployments/payments in prod"), "denied: %v", o)
	f.access, f.accessErr = nil, "timeout"
	o = unfreeze()
	expect(t, o["available"] == false && strings.HasPrefix(o["reason"].(string), "Pacto could not check whether you may update deployments/payments in prod"), "unverified: %v", o)
}
