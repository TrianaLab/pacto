package validation

import (
	"cmp"
	"fmt"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
)

// byNameForm checks a named dependency with no ref that is not external: from
// pactoVersion 2.5 a dependency by name, which Pacto resolves in the
// consumer's domain and whose version it reads from the fleet, so it gives
// none. Before 2.5 it needs a ref.
func byNameForm(c *contract.Contract, base string, dep contract.Dependency, result *ValidationResult) bool {
	allowed := !contract.Older(cmp.Or(c.PactoVersion, contract.Current()), "2.5")
	switch {
	case allowed && dep.Version == "":
		return true
	case allowed:
		result.AddError(base+".version", "VERSION_WITHOUT_REF", fmt.Sprintf("dependency %q has no ref, so Pacto resolves it by name and reads its version from the fleet: drop version and say which versions work in compatibility", dep.Name))
	case dep.Version == "":
		result.AddError(base+".ref", string(finding.CodeNeedsPacto25), `ref-less dependency by name needs pactoVersion "2.5"; set pactoVersion: "2.5", which keeps every older field`)
	default:
		result.AddError(base+".ref", "DEPENDENCY_REF_REQUIRED", fmt.Sprintf("dependency %q has no ref: set ref to its contract (for a contract not published yet, the repository it will be published at, with the versions you accept in compatibility), or, if this Pacto system does not deliver it, set external: true and the version you run against", dep.Name))
	}
	return false
}
