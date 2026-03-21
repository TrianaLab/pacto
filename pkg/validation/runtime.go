package validation

import (
	"fmt"

	"github.com/trianalab/pacto/pkg/contract"
)

// RuntimeContext represents observed runtime state collected from the
// actual environment (e.g. a Kubernetes cluster, local dev, CI).
// It is intentionally generic — no platform-specific types allowed.
type RuntimeContext struct {
	// HTTPPaths lists the HTTP paths actually served by the running service.
	HTTPPaths []string

	// EnvVars holds configuration environment variables present at runtime.
	EnvVars map[string]string

	// Ports lists the ports actually exposed by the running service.
	Ports []int
}

// RuntimeValidationResult holds the outcome of comparing a contract
// against observed runtime state.
type RuntimeValidationResult struct {
	Errors   []contract.ValidationError
	Warnings []contract.ValidationWarning
}

// IsValid returns true if there are no errors.
func (r *RuntimeValidationResult) IsValid() bool {
	return len(r.Errors) == 0
}

// addError appends a validation error.
func (r *RuntimeValidationResult) addError(path, code, message string) {
	r.Errors = append(r.Errors, contract.ValidationError{
		Path:    path,
		Code:    code,
		Message: message,
	})
}

// addWarning appends a validation warning.
func (r *RuntimeValidationResult) addWarning(path, code, message string) {
	r.Warnings = append(r.Warnings, contract.ValidationWarning{
		Path:    path,
		Code:    code,
		Message: message,
	})
}

// ValidateRuntime compares a contract's declared state against the
// observed runtime context. It checks that declared interfaces and
// configuration are present at runtime.
func ValidateRuntime(c *contract.Contract, ctx RuntimeContext) RuntimeValidationResult {
	var result RuntimeValidationResult

	validateRuntimePorts(c, ctx, &result)
	validateRuntimeConfig(c, ctx, &result)

	return result
}

// validateRuntimePorts checks that every declared HTTP/gRPC interface port
// is present in the observed runtime ports.
func validateRuntimePorts(c *contract.Contract, ctx RuntimeContext, result *RuntimeValidationResult) {
	if len(ctx.Ports) == 0 {
		return
	}

	observed := make(map[int]bool, len(ctx.Ports))
	for _, p := range ctx.Ports {
		observed[p] = true
	}

	for i, iface := range c.Interfaces {
		if iface.Port == nil {
			continue
		}
		if !observed[*iface.Port] {
			result.addError(
				fmt.Sprintf("interfaces[%d].port", i),
				"PORT_NOT_OBSERVED",
				fmt.Sprintf("interface %q declares port %d but it was not observed at runtime", iface.Name, *iface.Port),
			)
		}
	}
}

// validateRuntimeConfig checks that configuration values declared in
// the contract are present as environment variables at runtime.
func validateRuntimeConfig(c *contract.Contract, ctx RuntimeContext, result *RuntimeValidationResult) {
	if c.Configuration == nil || len(c.Configuration.Values) == 0 {
		return
	}
	if len(ctx.EnvVars) == 0 {
		return
	}

	for key := range c.Configuration.Values {
		if _, ok := ctx.EnvVars[key]; !ok {
			result.addWarning(
				fmt.Sprintf("configuration.values.%s", key),
				"CONFIG_NOT_OBSERVED",
				fmt.Sprintf("configuration key %q is declared but not found in runtime environment", key),
			)
		}
	}
}
