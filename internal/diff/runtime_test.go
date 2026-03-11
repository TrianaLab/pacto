package diff

import (
	"testing"

	"github.com/trianalab/pacto/pkg/contract"
)

func TestDiffLifecycle_BothNil(t *testing.T) {
	changes := diffLifecycle(nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffLifecycle_OldNil(t *testing.T) {
	newLC := &contract.Lifecycle{UpgradeStrategy: "rolling"}
	changes := diffLifecycle(nil, newLC)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != Added {
		t.Errorf("expected Added, got %s", changes[0].Type)
	}
}

func TestDiffLifecycle_OldNil_EmptyStrategy(t *testing.T) {
	newLC := &contract.Lifecycle{}
	changes := diffLifecycle(nil, newLC)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes when new strategy is empty, got %d", len(changes))
	}
}

func TestDiffLifecycle_OldNil_GracefulShutdownAdded(t *testing.T) {
	v := 30
	newLC := &contract.Lifecycle{GracefulShutdownSeconds: &v}
	changes := diffLifecycle(nil, newLC)
	found := false
	for _, c := range changes {
		if c.Path == "runtime.lifecycle.gracefulShutdownSeconds" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected gracefulShutdownSeconds Added change when lifecycle is added")
	}
}

func TestDiffLifecycle_NewNil_GracefulShutdownRemoved(t *testing.T) {
	v := 30
	oldLC := &contract.Lifecycle{GracefulShutdownSeconds: &v}
	changes := diffLifecycle(oldLC, nil)
	found := false
	for _, c := range changes {
		if c.Path == "runtime.lifecycle.gracefulShutdownSeconds" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected gracefulShutdownSeconds Removed change when lifecycle is removed")
	}
}

func TestDiffLifecycle_NewNil(t *testing.T) {
	oldLC := &contract.Lifecycle{UpgradeStrategy: "rolling"}
	changes := diffLifecycle(oldLC, nil)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != Removed {
		t.Errorf("expected Removed, got %s", changes[0].Type)
	}
}

func TestDiffLifecycle_NewNil_EmptyStrategy(t *testing.T) {
	oldLC := &contract.Lifecycle{}
	changes := diffLifecycle(oldLC, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes when old strategy is empty, got %d", len(changes))
	}
}

func TestDiffLifecycle_UpgradeStrategyChanged(t *testing.T) {
	oldLC := &contract.Lifecycle{UpgradeStrategy: "rolling"}
	newLC := &contract.Lifecycle{UpgradeStrategy: "recreate"}
	changes := diffLifecycle(oldLC, newLC)
	found := false
	for _, c := range changes {
		if c.Path == "runtime.lifecycle.upgradeStrategy" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected upgradeStrategy Modified change")
	}
}

func TestDiffLifecycle_StrategyAddedFromEmpty(t *testing.T) {
	oldLC := &contract.Lifecycle{UpgradeStrategy: ""}
	newLC := &contract.Lifecycle{UpgradeStrategy: "rolling"}
	changes := diffLifecycle(oldLC, newLC)
	found := false
	for _, c := range changes {
		if c.Path == "runtime.lifecycle.upgradeStrategy" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected upgradeStrategy Added change")
	}
}

func TestDiffLifecycle_StrategyRemovedToEmpty(t *testing.T) {
	oldLC := &contract.Lifecycle{UpgradeStrategy: "rolling"}
	newLC := &contract.Lifecycle{UpgradeStrategy: ""}
	changes := diffLifecycle(oldLC, newLC)
	found := false
	for _, c := range changes {
		if c.Path == "runtime.lifecycle.upgradeStrategy" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected upgradeStrategy Removed change")
	}
}

func TestDiffLifecycle_GracefulShutdownChanged(t *testing.T) {
	old30 := 30
	new60 := 60
	oldLC := &contract.Lifecycle{UpgradeStrategy: "rolling", GracefulShutdownSeconds: &old30}
	newLC := &contract.Lifecycle{UpgradeStrategy: "rolling", GracefulShutdownSeconds: &new60}
	changes := diffLifecycle(oldLC, newLC)
	found := false
	for _, c := range changes {
		if c.Path == "runtime.lifecycle.gracefulShutdownSeconds" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected gracefulShutdownSeconds Modified change")
	}
}

func TestIntPtrVal_Nil(t *testing.T) {
	if got := intPtrVal(nil); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestIntPtrVal_NonNil(t *testing.T) {
	v := 42
	if got := intPtrVal(&v); got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
}

func TestDiffRuntime_WorkloadChanged(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Runtime.Workload = "job"
	changes := diffRuntime(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "runtime.workload" {
			found = true
		}
	}
	if !found {
		t.Error("expected runtime.workload change")
	}
}

func TestDiffRuntime_StateTypeChanged(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Runtime.State.Type = "stateful"
	changes := diffRuntime(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "runtime.state.type" {
			found = true
		}
	}
	if !found {
		t.Error("expected runtime.state.type change")
	}
}

func TestDiffRuntime_PersistenceScopeChanged(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Runtime.State.Persistence.Scope = "shared"
	changes := diffRuntime(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "runtime.state.persistence.scope" {
			found = true
		}
	}
	if !found {
		t.Error("expected runtime.state.persistence.scope change")
	}
}

func TestDiffRuntime_PersistenceDurabilityChanged(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Runtime.State.Persistence.Durability = "persistent"
	changes := diffRuntime(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "runtime.state.persistence.durability" {
			found = true
		}
	}
	if !found {
		t.Error("expected runtime.state.persistence.durability change")
	}
}

func TestDiffRuntime_DataCriticalityChanged(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Runtime.State.DataCriticality = "high"
	changes := diffRuntime(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "runtime.state.dataCriticality" {
			found = true
		}
	}
	if !found {
		t.Error("expected runtime.state.dataCriticality change")
	}
}

func TestDiffRuntime_HealthInterfaceChanged(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Runtime.Health.Interface = "grpc"
	changes := diffRuntime(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "runtime.health.interface" {
			found = true
		}
	}
	if !found {
		t.Error("expected runtime.health.interface change")
	}
}

func TestDiffRuntime_HealthPathChanged(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Runtime.Health.Path = "/healthz"
	changes := diffRuntime(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "runtime.health.path" {
			found = true
		}
	}
	if !found {
		t.Error("expected runtime.health.path change")
	}
}

func TestDiffRuntime_HealthInitialDelayChanged(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	v := 10
	new.Runtime.Health.InitialDelaySeconds = &v
	changes := diffRuntime(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "runtime.health.initialDelaySeconds" {
			found = true
		}
	}
	if !found {
		t.Error("expected runtime.health.initialDelaySeconds change")
	}
}

func TestDiffRuntime_BothNilRuntime(t *testing.T) {
	old := &contract.Contract{Service: contract.ServiceIdentity{Name: "a", Version: "1.0.0"}}
	new := &contract.Contract{Service: contract.ServiceIdentity{Name: "a", Version: "1.0.0"}}
	changes := diffRuntime(old, new)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for nil runtimes, got %d", len(changes))
	}
}

func TestDiffRuntime_OldNilRuntime(t *testing.T) {
	old := &contract.Contract{Service: contract.ServiceIdentity{Name: "a", Version: "1.0.0"}}
	new := minimalContract()
	changes := diffRuntime(old, new)
	if len(changes) == 0 {
		t.Error("expected changes when runtime added")
	}
}

func TestDiffRuntime_NewNilRuntime(t *testing.T) {
	old := minimalContract()
	new := &contract.Contract{Service: contract.ServiceIdentity{Name: "a", Version: "1.0.0"}}
	changes := diffRuntime(old, new)
	if len(changes) == 0 {
		t.Error("expected changes when runtime removed")
	}
}
