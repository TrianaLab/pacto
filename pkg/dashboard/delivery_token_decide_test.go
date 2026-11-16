package dashboard

import (
	"net/http"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// tokenDecide serves f to a token session (token sign-in only, no identity
// provider) and posts an approval of prod/held.
func tokenDecide(t *testing.T, f *fakeDelivery) (int, map[string]any) {
	t.Helper()
	au := (&authed{state: AuthState{SignedIn: true, Method: "token", User: f.caller.User}}).auth(false, false)
	au.TokenLogin = true
	base := startDelivery(t, f, au, true, nil)
	return call(t, http.MethodPost, base+"/api/delivery/operations/prod/held/decision", map[string]string{"decision": "approve"})
}

func TestDecideTokenSessionPersonApproves(t *testing.T) {
	f := accessCluster()
	code, out := tokenDecide(t, f)
	expect(t, code == http.StatusCreated && len(f.approved) == 1 && f.approved[0] == "prod/held=approve",
		"a person's token session decides under the approver rule: %d %v", code, out)
}

func TestDecideTokenSessionAgentRefused(t *testing.T) {
	f := accessCluster()
	f.caller.Identity = delivery.Identity{User: "system:serviceaccount:shop:viewer", Groups: []string{"system:serviceaccounts", "acme:team-checkout"}}
	code, out := tokenDecide(t, f)
	expect(t, code == http.StatusForbidden && out["code"] == "AgentsMayNotApprove" && len(f.approved) == 0,
		"a ServiceAccount token never decides: %d %v", code, out)
}

func TestDecideTokenSessionWithoutRightsRefused(t *testing.T) {
	f := accessCluster()
	f.access = map[string]bool{"prod/payments": false}
	code, out := tokenDecide(t, f)
	expect(t, code == http.StatusForbidden && out["code"] == "CannotUpdateWorkload" && len(f.approved) == 0,
		"no rights on the workload: %d %v", code, out)
}

func TestDecideTokenSessionUnknownAuthTimeOnFreshStage(t *testing.T) {
	f := accessCluster()
	f.caller.AuthTime = ""
	f.stages[0].Status.AuthFreshness = "15m0s"
	code, out := tokenDecide(t, f)
	expect(t, code == http.StatusForbidden && out["code"] == "AuthTimeUnavailable" && len(f.approved) == 0,
		"a token without a login time on a freshness Stage: %d %v", code, out)
}

// A session that may not decide is offered no decision, and the change view
// says why: a local dashboard never decides.
func TestOfferNamesASessionThatMayNotDecide(t *testing.T) {
	f := accessCluster()
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(true, false), true, nil)
	_, out := call(t, http.MethodGet, base+"/api/delivery/operations/prod/held", nil)
	expect(t, out["decisionRefusal"] == "DecisionsNotServed", "local: %v", out)
}
