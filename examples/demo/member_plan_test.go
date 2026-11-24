package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// TestDemoMemberPlanMatchesJoinList: the environments board of payments
// carries the hub's member plans, placed over the join list's approved
// members: us-west alone in test, proposed from what it advertises; both in
// prod, confirmed by the person who signed the requests, eu-prod applied and
// us-west refused at its pacto.published gate. The pending ap-dev is never
// named, and a service no person asked members for still gets its placement.
func TestDemoMemberPlanMatchesJoinList(t *testing.T) {
	_, h := demoServer(t)
	var env struct {
		Members []delivery.MemberPlan `json:"members"`
	}
	if code := call(t, h, "GET", "/api/delivery/environments?service=payments", "", &env); code != 200 {
		t.Fatalf("environments: %d", code)
	}
	if len(env.Members) != 2 || env.Members[0].Environment != "test" || env.Members[1].Environment != "prod" {
		t.Fatalf("member plans %+v, want test then prod", env.Members)
	}
	checkDemoMembersApproved(t, env.Members)
	checkDemoMemberStates(t, env.Members[0], env.Members[1])
	if got := demoMemberPlans("checkout", []string{"test", "prod"}); len(got) != 2 || got[1].Placement.Source != delivery.PlacementUnconfirmed {
		t.Errorf("checkout: %+v", got)
	}
}

// TestDemoMemberRefusalRow: the refused list carries us-west's rejected
// promote as a member row naming the member and the gate's reason.
func TestDemoMemberRefusalRow(t *testing.T) {
	_, h := demoServer(t)
	var out dashboard.PublishRefusals
	if code := call(t, h, "GET", "/api/delivery/refusals", "", &out); code != 200 {
		t.Fatalf("refusals: %d", code)
	}
	i := slices.IndexFunc(out.Refusals, func(r dashboard.PublishRefusal) bool { return r.Check == "member" })
	if i < 0 {
		t.Fatalf("no member row in %+v", out.Refusals)
	}
	r := out.Refusals[i]
	if r.Cluster != "us-west" || r.Service != "payments" || r.Code != "Rejected" || !strings.Contains(r.Reason, "no publisher check accepted") || r.Verified {
		t.Errorf("member row %+v", r)
	}
}

// checkDemoMembersApproved: every member a plan names is an approved member
// of the join list, under its name there.
func checkDemoMembersApproved(t *testing.T, plans []delivery.MemberPlan) {
	t.Helper()
	approved := map[string]string{}
	for _, c := range demoClusterJoins().Clusters {
		if c.State == delivery.JoinApproved {
			approved[c.UID] = c.Name
		}
	}
	for _, p := range plans {
		for _, m := range p.Members {
			if approved[m.UID] != m.Name {
				t.Errorf("%s: member %s/%s is not an approved member of the join list", p.Environment, m.UID, m.Name)
			}
		}
	}
}

// checkDemoMemberStates: test is proposed from what us-west advertises;
// prod is confirmed by alice, eu-prod applied and us-west refused.
func checkDemoMemberStates(t *testing.T, test, prod delivery.MemberPlan) {
	t.Helper()
	if test.Placement.Source != delivery.PlacementUnconfirmed || len(test.Members) != 1 || test.Members[0].State != delivery.MemberNoRequest {
		t.Errorf("test: %+v", test)
	}
	if prod.Placement.Source != delivery.RequestPlacedAdvertised || prod.Placement.ConfirmedBy != "alice@example.com" ||
		!strings.HasPrefix(prod.Next, "pacto delivery propose --service payments --environment prod") || prod.OrderUnknown {
		t.Errorf("prod placement: %+v", prod.Placement)
	}
	states := map[string]delivery.MemberState{}
	for _, m := range prod.Members {
		states[m.Name] = m
	}
	us := states["us-west"]
	if states["eu-prod"].State != delivery.MemberApplied || us.State != delivery.MemberRefused || !strings.Contains(us.Reason, "no publisher check accepted") || us.AsOf == "" {
		t.Errorf("prod members: %+v", prod.Members)
	}
}
