package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

const agentUser = "system:serviceaccount:agents:bot"

// asAgent makes every later caller lookup on c answer agentUser.
func asAgent(c *dynamicfake.FakeDynamicClient) {
	c.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"userInfo": map[string]any{
			"username": agentUser}}}}, nil
	})
}

// TestProposeAgentNeedsOnBehalfOf: the client refuses an agent's propose
// that names nobody, itself or a service account before it reaches the
// cluster, with the code a client shows and the flag that fixes it.
func TestProposeAgentNeedsOnBehalfOf(t *testing.T) {
	ctx := context.Background()
	for behalf, code := range map[string]string{"": "NoPrincipal", agentUser: "SelfPrincipal", "system:serviceaccount:a:b": "NotAPerson"} {
		d, c, _ := deliveryFixture(t, agentUser, nil, nil)
		_, plan, err := d.Propose(ctx, delivery.Request{Namespace: "prod", Stage: "payments-prod", To: dlB, OnBehalfOf: behalf})
		var who *PrincipalError
		if !errors.As(err, &who) || who.Code != code || !strings.Contains(err.Error(), "--on-behalf-of") || plan.IntentDigest == "" {
			t.Fatalf("on behalf of %q: %v %+v", behalf, err, who)
		}
		for _, a := range c.Actions() {
			if a.GetVerb() == "create" && a.GetResource().Resource == "operations" {
				t.Fatalf("on behalf of %q: an Operation was created", behalf)
			}
		}
	}
	d, _, _ := deliveryFixture(t, agentUser, nil, nil)
	if _, _, err := d.Propose(ctx, delivery.Request{Namespace: "prod", Stage: "payments-prod", To: dlB, OnBehalfOf: "dana"}); err != nil {
		t.Fatalf("an agent naming a person proposes: %v", err)
	}
}

// TestPlanAgentWithoutPrincipalStillPlans: a plan is a preview and names
// nobody, so it is never refused for that.
func TestPlanAgentWithoutPrincipalStillPlans(t *testing.T) {
	d, _, _ := deliveryFixture(t, agentUser, nil, nil)
	if plan, _, err := d.Plan(context.Background(), delivery.Request{Namespace: "prod", Stage: "payments-prod", To: dlB}); err != nil || plan.IntentDigest == "" {
		t.Fatalf("plan: %+v %v", plan, err)
	}
}

// TestUndoPlanAsAgentNeedsNoPrincipal: an agent reads what undoing a change
// would take without naming anyone.
func TestUndoPlanAsAgentNeedsNoPrincipal(t *testing.T) {
	applied := map[string]any{"phase": "Concluded", "outcome": "Succeeded", "pointer": map[string]any{"before": dlC, "after": dlA}}
	e := newRecordEnv(t, opFixture("ship", shipSpec(dlA), applied))
	e.twoRecords()
	asAgent(e.c)
	if u, err := e.d.Undo(context.Background(), "prod", "ship", ""); err != nil || u.Kind != "rollback" || u.Plan == nil {
		t.Fatalf("undo plan as an agent = %+v %v", u, err)
	}
}

// TestProgressAsAgentNeedsNoPrincipal: progress plans the candidate as the
// caller, and an agent caller names nobody.
func TestProgressAsAgentNeedsNoPrincipal(t *testing.T) {
	d, c := statusFixture(t)
	asAgent(c)
	ri := c.Resource(gvrStages).Namespace("prod")
	u, _ := ri.Get(context.Background(), "payments-prod", metav1.GetOptions{})
	_ = unstructured.SetNestedField(u.Object, dlB, "status", "candidate")
	_ = unstructured.SetNestedSlice(u.Object, []any{map[string]any{"type": "Ready", "status": "True", "reason": "Ready"}}, "status", "conditions")
	if _, err := ri.Update(context.Background(), u, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	s, err := d.Status(context.Background(), delivery.StatusQuery{Service: "payments", Why: true})
	if err != nil || len(s.Stages) == 0 || s.Stages[0].Progress == nil || s.Stages[0].Progress.Plan == nil {
		t.Fatalf("progress as an agent = %+v %v", s, err)
	}
}
