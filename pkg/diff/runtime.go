package diff

import (
	"github.com/trianalab/pacto/pkg/contract"
)

// diffRuntime compares runtime semantics: workload, state, persistence,
// lifecycle, and health.
func diffRuntime(old, new *contract.Contract) []Change {
	if old.Runtime == nil && new.Runtime == nil {
		return nil
	}

	var changes []Change
	oldRT := old.Runtime
	newRT := new.Runtime

	if oldRT == nil {
		oldRT = &contract.Runtime{}
	}
	if newRT == nil {
		newRT = &contract.Runtime{}
	}

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
	changes = append(changes, diffHealth(oldRT.Health, newRT.Health)...)

	// Metrics
	changes = append(changes, diffMetrics(oldRT.Metrics, newRT.Metrics)...)

	return changes
}

func diffHealth(old, new *contract.Health) []Change {
	var changes []Change
	oldIface, newIface := "", ""
	oldPath, newPath := "", ""
	var oldDelay, newDelay *int
	if old != nil {
		oldIface, oldPath, oldDelay = old.Interface, old.Path, old.InitialDelaySeconds
	}
	if new != nil {
		newIface, newPath, newDelay = new.Interface, new.Path, new.InitialDelaySeconds
	}
	if oldIface != newIface {
		changes = append(changes, newChange("runtime.health.interface", Modified, oldIface, newIface))
	}
	if oldPath != newPath {
		changes = append(changes, newChange("runtime.health.path", Modified, oldPath, newPath))
	}
	if intPtrChanged(oldDelay, newDelay) {
		changes = append(changes, newChange("runtime.health.initialDelaySeconds", Modified, intPtrVal(oldDelay), intPtrVal(newDelay)))
	}
	return changes
}

func diffMetrics(old, new *contract.Metrics) []Change {
	var changes []Change
	oldIface, newIface := "", ""
	oldPath, newPath := "", ""
	if old != nil {
		oldIface, oldPath = old.Interface, old.Path
	}
	if new != nil {
		newIface, newPath = new.Interface, new.Path
	}
	if oldIface != newIface {
		changes = append(changes, newChange("runtime.metrics.interface", Modified, oldIface, newIface))
	}
	if oldPath != newPath {
		changes = append(changes, newChange("runtime.metrics.path", Modified, oldPath, newPath))
	}
	return changes
}

func diffLifecycle(old, new *contract.Lifecycle) []Change {
	var changes []Change

	if old == nil && new == nil {
		return nil
	}

	// Normalise nil to empty so every field is compared uniformly.
	if old == nil {
		old = &contract.Lifecycle{}
	}
	if new == nil {
		new = &contract.Lifecycle{}
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
	if intPtrChanged(old.GracefulShutdownSeconds, new.GracefulShutdownSeconds) {
		changes = append(changes, newChange("runtime.lifecycle.gracefulShutdownSeconds",
			intPtrChangeType(old.GracefulShutdownSeconds, new.GracefulShutdownSeconds),
			intPtrVal(old.GracefulShutdownSeconds), intPtrVal(new.GracefulShutdownSeconds)))
	}

	return changes
}

func intPtrChanged(a, b *int) bool {
	if a == nil && b == nil {
		return false
	}
	if a == nil || b == nil {
		return true
	}
	return *a != *b
}

func intPtrVal(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// intPtrChangeType returns the change type for an optional integer pointer
// transition. The caller must ensure intPtrChanged(old, new) is true.
func intPtrChangeType(old, new *int) ChangeType {
	if old == nil {
		return Added
	}
	if new == nil {
		return Removed
	}
	return Modified
}
