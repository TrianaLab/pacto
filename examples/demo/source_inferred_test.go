package main

import (
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoHasInferredServices: the demo reads three workloads no contract
// describes, and the kernel-style caller legacy-billing stays an uncontracted
// caller of payments-service that links to its inferred entity.
func TestDemoHasInferredServices(t *testing.T) {
	snap := demoFleet(t)
	want := map[string]string{
		"k8s:shop/Deployment/legacy-billing":      "shop",
		"k8s:shop/CronJob/nightly-export":         "shop",
		"k8s:observability/DaemonSet/log-shipper": "observability",
	}
	inferred := 0
	for _, s := range snap.Services {
		if !s.Inferred() {
			continue
		}
		inferred++
		ns, ok := want[s.Name]
		if !ok || s.Workload == nil || s.Workload.Namespace != ns || s.Workload.Image == nil {
			t.Errorf("inferred service %s: %+v", s.Name, s.Workload)
		}
	}
	if inferred != len(want) {
		t.Errorf("inferred services = %d, want %d", inferred, len(want))
	}
	billing := fleet.NewServiceKey("k8s:shop/Deployment/legacy-billing")
	found := false
	for _, c := range snap.UncontractedCallers {
		if c.Caller == "k8s:shop/Deployment/legacy-billing" {
			found = c.ToService == fleet.NewServiceKey("payments-service") && c.Entity == billing
		}
	}
	if !found {
		t.Errorf("legacy-billing is not an uncontracted caller of payments-service with its entity: %+v", snap.UncontractedCallers)
	}
}
