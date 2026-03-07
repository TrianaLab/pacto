package validation

import (
	"github.com/trianalab/pacto/pkg/contract"
)

// ValidateSemantic performs Layer 3 validation: semantic consistency checks
// based on cross-concern rules that span multiple sections of the contract.
func ValidateSemantic(c *contract.Contract) ValidationResult {
	var result ValidationResult

	validateUpgradeStrategyConsistency(c, &result)

	return result
}

func validateUpgradeStrategyConsistency(c *contract.Contract, result *ValidationResult) {
	if c.Runtime == nil || c.Runtime.Lifecycle == nil {
		return
	}
	if c.Runtime.Lifecycle.UpgradeStrategy == contract.UpgradeStrategyOrdered &&
		c.Runtime.State.Type == contract.StateStateless {
		result.AddWarning(
			"runtime.lifecycle.upgradeStrategy",
			"UPGRADE_STRATEGY_STATE_MISMATCH",
			"ordered upgrade strategy is typically used with stateful services, but state is stateless",
		)
	}
}
