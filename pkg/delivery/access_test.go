package delivery

import (
	"context"
	"testing"
)

func TestWorkloadResource(t *testing.T) {
	for kind, want := range map[string][2]string{
		"Deployment": {"apps", "deployments"}, "StatefulSet": {"apps", "statefulsets"}, "DaemonSet": {"apps", "daemonsets"},
		"ReplicaSet": {"apps", "replicasets"}, "Job": {"batch", "jobs"}, "CronJob": {"batch", "cronjobs"},
	} {
		if g, r, ok := WorkloadResource(kind); !ok || g != want[0] || r != want[1] {
			t.Errorf("%s: %s %s %v", kind, g, r, ok)
		}
	}
	if _, _, ok := WorkloadResource("Rollout"); ok {
		t.Error("Rollout has no resource Pacto asks about")
	}
}

func TestAccessForDefaultsToDeployment(t *testing.T) {
	a, group := AccessFor(StageSpec{Service: ServiceRef{Name: "checkout"}}, "payments")
	if group != "apps" || a.Resource != "deployments" || a.Name != "checkout" || a.Namespace != "payments" || a.Unverified != "" || a.Allowed {
		t.Fatalf("%q %+v", group, a)
	}
}

func TestPhrase(t *testing.T) {
	if got := (WorkloadAccess{Resource: "deployments", Name: "checkout", Namespace: "payments"}).Phrase(); got != "update deployments/checkout in payments" {
		t.Fatal(got)
	}
}

// TestAccessMemo: one review per workload; a missing Stage or an unknown
// kind is unverified and never asked.
func TestAccessMemo(t *testing.T) {
	stage := func(name string, w *WorkloadRef) StageObject {
		var st StageObject
		st.Metadata.Namespace, st.Metadata.Name = "prod", name
		st.Spec.Service.Name, st.Spec.Workload = "payments", w
		return st
	}
	asked := 0
	access := AccessMemo([]StageObject{stage("a", nil), stage("b", nil), stage("c", &WorkloadRef{Kind: "Rollout", Name: "x"})},
		func(_ context.Context, ns string, w WorkloadRef) WorkloadAccess {
			asked++
			return WorkloadAccess{Resource: "deployments", Name: w.Name, Namespace: ns, Allowed: true}
		})
	ctx := context.Background()
	if !access(ctx, "prod", "a").Allowed || !access(ctx, "prod", "b").Allowed || asked != 1 {
		t.Fatalf("one review for one workload: %d", asked)
	}
	if a := access(ctx, "prod", "c"); a.Unverified == "" || asked != 1 {
		t.Fatalf("unknown kind: %+v", a)
	}
	if a := access(ctx, "prod", "gone"); a.Unverified == "" || a.Allowed || asked != 1 {
		t.Fatalf("missing Stage: %+v", a)
	}
}
