package dashboard

import (
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

func progressCodes(b map[string]any) []string {
	var out []string
	for _, r := range b["reasons"].([]any) {
		out = append(out, r.(map[string]any)["code"].(string))
	}
	return out
}

func TestDeliveryProgress(t *testing.T) {
	f := sampleCluster("acme:team-payments")
	f.owner, f.known = "team-payments", map[string]string{dNext: "2.0.0"}
	f.stages[0].Status.Frozen = false
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	const url = "/api/delivery/progress?namespace=prod&stage=payments"
	code, b := call(t, http.MethodGet, base+url, nil)
	expect(t, code == http.StatusOK && b["state"] == "needs-person" && b["candidate"] == dNext && b["asOf"] != "" &&
		b["namespace"] == "prod" && b["stage"] == "payments", "progress %d %v", code, b)
	expect(t, slices.Equal(progressCodes(b), []string{"Held", "Held", "Held", "Level", "PlanHeld"}), "reasons %v", progressCodes(b))
	expect(t, len(f.planned) == 1 && f.planned[0].To == dNext && f.planned[0].Type == delivery.TypePromote, "planned %v", f.planned)
	expect(t, b["plan"].(map[string]any)["to"] == dNext, "plan %v", b["plan"])
	vs := b["versions"].(map[string]any)
	expect(t, vs[dRun] == "1.0.0" && vs[dNext] == "2.0.0", "versions %v", vs)
	expect(t, f.cans.Load() == 1, "one access review: %d", f.cans.Load())

	f.actErr = errors.New("registry down")
	_, b = call(t, http.MethodGet, base+url, nil)
	expect(t, b["state"] == "unknown" && slices.Contains(progressCodes(b), "PlanUnknown") && b["plan"] == nil, "plan error %v", b)
	f.actErr = nil

	f.opsErr = errors.New("operations forbidden")
	code, b = call(t, http.MethodGet, base+url, nil)
	expect(t, code == http.StatusOK && b["state"] == "unknown" && slices.Contains(progressCodes(b), "OpsUnknown"), "ops error %d %v", code, b)
	f.opsErr = nil

	f.deny = map[string]bool{"create operations prod": true}
	_, b = call(t, http.MethodGet, base+url, nil)
	expect(t, b["state"] == "blocked" && slices.Contains(progressCodes(b), "NotPermitted"), "review denied %v", b)
	f.deny, f.canErr = nil, errors.New("timeout")
	_, b = call(t, http.MethodGet, base+url, nil)
	expect(t, b["state"] == "unknown" && slices.Contains(progressCodes(b), "RBACUnknown"), "review error %v", b)
	f.canErr = nil

	code, _ = call(t, http.MethodGet, base+"/api/delivery/progress?namespace=prod&stage=gone", nil)
	expect(t, code == http.StatusNotFound, "no Stage: %d", code)
	f.callerErr = Refuse(http.StatusUnauthorized, "Unauthorized", "who?")
	code, _ = call(t, http.MethodGet, base+url, nil)
	expect(t, code == http.StatusUnauthorized, "no caller: %d", code)
	code, _ = call(t, http.MethodGet, base+"/api/delivery/progress?namespace=prod", nil)
	expect(t, code == http.StatusUnprocessableEntity, "progress needs a Stage: %d", code)
}

func TestDeliveryProgressOutOfScope(t *testing.T) {
	f := sampleCluster()
	base := startServer(t, NewServer(Options{UI: testUI(), Fleet: ownedFleet(t), Auth: (&authed{state: AuthState{SignedIn: true}}).auth(false, true),
		DeliveryNamespace: "prod", Delivery: func(*http.Request) (DeliveryAPI, error) { return f, nil }}))
	code, b := call(t, http.MethodGet, base+"/api/delivery/progress?namespace=books&stage=ledger", nil)
	expect(t, code == http.StatusNotFound && b["code"] == "OutOfScope" && len(f.planned) == 0, "out of scope: %d %v", code, b)
	// The dashboard cannot reach delivery at all.
	base = startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), false, nil)
	code, _ = call(t, http.MethodGet, base+"/api/delivery/progress?namespace=prod&stage=payments", nil)
	expect(t, code == http.StatusNotFound, "not installed: %d", code)
}
