package diff

import (
	"github.com/trianalab/pacto/pkg/contract"
)

// diffRuntime compares runtime semantics: workload, state, persistence,
// lifecycle, and health.
func diffRuntime(old, new *contract.Contract) []Change {
	var changes []Change

	oldRT := old.Runtime
	newRT := new.Runtime

	// Workload
	if oldRT.Workload != newRT.Workload {
		changes = append(changes, newChange("runtime.workload", Modified, oldRT.Workload, newRT.Workload))
	}

	// State
	if oldRT.State.Type != newRT.State.Type {
		changes = append(changes, newChange("runtime.state.type", Modified, oldRT.State.Type, newRT.State.Type))
	}
	if oldRT.State.Persistence.Scope != newRT.State.Persistence.Scope {
		changes = append(changes, newChange("runtime.state.persistence.scope", Modified, oldRT.State.Persistence.Scope, newRT.State.Persistence.Scope))
	}
	if oldRT.State.Persistence.Durability != newRT.State.Persistence.Durability {
		changes = append(changes, newChange("runtime.state.persistence.durability", Modified, oldRT.State.Persistence.Durability, newRT.State.Persistence.Durability))
	}
	if oldRT.State.DataCriticality != newRT.State.DataCriticality {
		changes = append(changes, newChange("runtime.state.dataCriticality", Modified, oldRT.State.DataCriticality, newRT.State.DataCriticality))
	}

	// Lifecycle
	changes = append(changes, diffLifecycle(oldRT.Lifecycle, newRT.Lifecycle)...)

	// Health
	if oldRT.Health.Interface != newRT.Health.Interface {
		changes = append(changes, newChange("runtime.health.interface", Modified, oldRT.Health.Interface, newRT.Health.Interface))
	}
	if oldRT.Health.Path != newRT.Health.Path {
		changes = append(changes, newChange("runtime.health.path", Modified, oldRT.Health.Path, newRT.Health.Path))
	}
	if intPtrVal(oldRT.Health.InitialDelaySeconds) != intPtrVal(newRT.Health.InitialDelaySeconds) {
		changes = append(changes, newChange("runtime.health.initialDelaySeconds", Modified,
			intPtrVal(oldRT.Health.InitialDelaySeconds), intPtrVal(newRT.Health.InitialDelaySeconds)))
	}

	return changes
}

func diffLifecycle(old, new *contract.Lifecycle) []Change {
	var changes []Change

	if old == nil && new == nil {
		return nil
	}
	if old == nil {
		if new.UpgradeStrategy != "" {
			changes = append(changes, newChange("runtime.lifecycle.upgradeStrategy", Added, nil, new.UpgradeStrategy))
		}
		return changes
	}
	if new == nil {
		if old.UpgradeStrategy != "" {
			changes = append(changes, newChange("runtime.lifecycle.upgradeStrategy", Removed, old.UpgradeStrategy, nil))
		}
		return changes
	}

	if old.UpgradeStrategy != new.UpgradeStrategy {
		ct := Modified
		if old.UpgradeStrategy == "" {
			ct = Added
		} else if new.UpgradeStrategy == "" {
			ct = Removed
		}
		changes = append(changes, newChange("runtime.lifecycle.upgradeStrategy", ct, old.UpgradeStrategy, new.UpgradeStrategy))
	}
	if intPtrVal(old.GracefulShutdownSeconds) != intPtrVal(new.GracefulShutdownSeconds) {
		changes = append(changes, newChange("runtime.lifecycle.gracefulShutdownSeconds", Modified,
			intPtrVal(old.GracefulShutdownSeconds), intPtrVal(new.GracefulShutdownSeconds)))
	}

	return changes
}

func intPtrVal(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
