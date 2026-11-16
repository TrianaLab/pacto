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

// mayUpdate answers the caller's update reviews with ok, or fails them with
// err, and records what each asked.
func mayUpdate(c *dynamicfake.FakeDynamicClient, ok bool, err error, asked *[]map[string]any) {
	c.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		attrs, _, _ := unstructured.NestedMap(a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).Object, "spec", "resourceAttributes")
		if attrs["verb"] != "update" {
			return false, nil, nil
		}
		if asked != nil {
			*asked = append(*asked, attrs)
		}
		if err != nil {
			return true, nil, err
		}
		return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"allowed": ok}}}, nil
	})
}

func TestWorkloadAccessUsesSelfReview(t *testing.T) {
	d, c, _ := deliveryFixture(t, "dana", nil, nil)
	var asked []map[string]any
	mayUpdate(c, true, nil, &asked)
	a := d.WorkloadAccess(context.Background(), "payments", delivery.WorkloadRef{Kind: "Deployment", Name: "checkout"})
	if !a.Allowed || a.Source != delivery.AccessBySelfReview || a.At == "" || a.Phrase() != "update deployments/checkout in payments" {
		t.Fatalf("access: %+v", a)
	}
	if len(asked) != 1 || asked[0]["group"] != "apps" || asked[0]["resource"] != "deployments" || asked[0]["name"] != "checkout" || asked[0]["namespace"] != "payments" {
		t.Fatalf("review: %+v", asked)
	}
	if a := d.WorkloadAccess(context.Background(), "payments", delivery.WorkloadRef{Kind: "Rollout", Name: "x"}); a.Unverified == "" || len(asked) != 1 {
		t.Fatalf("an unknown kind is never asked: %+v", a)
	}
	mayUpdate(c, true, errors.New("down"), nil)
	if a := d.WorkloadAccess(context.Background(), "payments", delivery.WorkloadRef{Kind: "Deployment", Name: "checkout"}); a.Allowed || !strings.Contains(a.Unverified, "down") {
		t.Fatalf("a failed review is unverified: %+v", a)
	}
}

// TestWorkloadAccessEvaluationErrorUnverified: a refusal the authorizers
// could not evaluate is unknown, never a denial; an allow despite one stands.
func TestWorkloadAccessEvaluationErrorUnverified(t *testing.T) {
	d, c, _ := deliveryFixture(t, "dana", nil, nil)
	status := map[string]any{"allowed": false, "evaluationError": "webhook authorizer timed out"}
	c.PrependReactor("create", "selfsubjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.Unstructured{Object: map[string]any{"status": status}}, nil
	})
	w := delivery.WorkloadRef{Kind: "Deployment", Name: "checkout"}
	if a := d.WorkloadAccess(context.Background(), "payments", w); a.Allowed || a.Unverified != "the SelfSubjectAccessReview could not be evaluated: webhook authorizer timed out" {
		t.Fatalf("refused with an evaluation error: %+v", a)
	}
	status["allowed"] = true
	if a := d.WorkloadAccess(context.Background(), "payments", w); !a.Allowed || a.Unverified != "" {
		t.Fatalf("allowed with an evaluation error: %+v", a)
	}
}

func TestApproveRefusalCopy(t *testing.T) {
	op := obj("delivery.pacto.trianalab.io/v1alpha1", "Operation", "payments", "p1", map[string]any{
		"spec":   map[string]any{"intentDigest": "sha256:i", "stage": "payments", "actor": map[string]any{"user": "bot"}},
		"status": map[string]any{"phase": "Held", "times": map[string]any{"held": "2027-01-15T07:00:00Z"}},
	})
	st := statusStage("payments", "payments", "payments", "prod", "", "", nil)
	ctx := context.Background()
	d, _, _ := deliveryFixture(t, "system:serviceaccount:ci:ci-bot", nil, nil, op, st)
	if _, err := d.Approve(ctx, "payments", "p1", delivery.Approve, ""); err == nil || !strings.HasPrefix(err.Error(), "agents cannot approve") {
		t.Fatalf("agent: %v", err)
	}
	d, c, _ := deliveryFixture(t, "dana", nil, nil, op, st)
	created := 0
	c.PrependReactor("create", "approvals", func(a k8stesting.Action) (bool, runtime.Object, error) {
		created++
		u := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
		u.SetName("p1-x")
		return true, u, nil
	})
	mayUpdate(c, false, nil, nil)
	if _, err := d.Approve(ctx, "payments", "p1", delivery.Approve, ""); err == nil || !strings.HasPrefix(err.Error(), "you cannot update deployments/payments in payments") || created != 0 {
		t.Fatalf("denied: %v, %d created", err, created)
	}
	mayUpdate(c, true, nil, nil)
	if name, err := d.Approve(ctx, "payments", "p1", delivery.Approve, ""); err != nil || name != "p1-x" || created != 1 {
		t.Fatalf("allowed: %q %v", name, err)
	}
}

// TestStatusUnfreezeOfferNeedsAccess: a person's unfreeze offer and hint need
// update rights on the workload.
func TestStatusUnfreezeOfferNeedsAccess(t *testing.T) {
	d, c := statusFixture(t)
	ri := c.Resource(gvrStages).Namespace("prod")
	u, _ := ri.Get(context.Background(), "payments-prod", metav1.GetOptions{})
	_ = unstructured.SetNestedField(u.Object, true, "status", "frozen")
	if _, err := ri.Update(context.Background(), u, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	mayUpdate(c, false, nil, nil)
	s, err := d.Status(context.Background(), delivery.StatusQuery{Service: "payments", Why: true})
	if err != nil {
		t.Fatal(err)
	}
	if o := offer(s.Stages[0], "unfreeze"); o.Available || !strings.HasPrefix(o.Reason, "you cannot update deployments/payments in prod") {
		t.Fatalf("unfreeze offer: %+v", s.Stages[0].Offers)
	}
	for _, r := range s.Stages[0].Progress.Reasons {
		if r.Code == "Frozen" && !strings.HasPrefix(r.Fix, "you cannot update deployments/payments in prod") {
			t.Fatalf("frozen fix: %+v", r)
		}
	}
	mayUpdate(c, true, nil, nil)
	if s, _ = d.Status(context.Background(), delivery.StatusQuery{Service: "payments"}); !offer(s.Stages[0], "unfreeze").Available {
		t.Fatalf("allowed: %+v", s.Stages[0].Offers)
	}
}
