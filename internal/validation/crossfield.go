package validation

import (
	"fmt"
	"io/fs"

	"github.com/Masterminds/semver/v3"
	"github.com/trianalab/pacto/internal/graph"
	"github.com/trianalab/pacto/pkg/contract"
)

// ValidateCrossField performs Layer 2 validation: cross-field consistency,
// file existence, reference validation, and semantic rules that cannot be
// expressed in JSON Schema alone.
func ValidateCrossField(c *contract.Contract, bundleFS fs.FS) ValidationResult {
	var result ValidationResult

	validateServiceVersion(c, &result)
	validateInterfaceNamesUnique(c, &result)
	validateInterfacePorts(c, &result)
	validateInterfaceContracts(c, &result)
	validateHealthInterface(c, &result)
	validateInterfaceFiles(c, bundleFS, &result)
	validateConfigFiles(c, bundleFS, &result)
	validateDependencyRefs(c, &result)
	validateImageRef(c, &result)
	validateScaling(c, &result)
	validateJobScaling(c, &result)
	validateStatePersistenceInvariants(c, &result)

	return result
}

func validateServiceVersion(c *contract.Contract, result *ValidationResult) {
	if _, err := semver.NewVersion(c.Service.Version); err != nil {
		result.AddError(
			"service.version",
			"INVALID_SEMVER",
			fmt.Sprintf("service version %q is not valid semver: %v", c.Service.Version, err),
		)
	}
}

func validateInterfaceNamesUnique(c *contract.Contract, result *ValidationResult) {
	seen := make(map[string]int)
	for i, iface := range c.Interfaces {
		if prev, exists := seen[iface.Name]; exists {
			result.AddError(
				fmt.Sprintf("interfaces[%d].name", i),
				"DUPLICATE_INTERFACE_NAME",
				fmt.Sprintf("interface name %q is already declared at interfaces[%d]", iface.Name, prev),
			)
		}
		seen[iface.Name] = i
	}
}

func validateInterfacePorts(c *contract.Contract, result *ValidationResult) {
	for i, iface := range c.Interfaces {
		switch iface.Type {
		case contract.InterfaceTypeHTTP, contract.InterfaceTypeGRPC:
			if iface.Port == nil {
				result.AddError(
					fmt.Sprintf("interfaces[%d].port", i),
					"PORT_REQUIRED",
					fmt.Sprintf("port is required for %s interface %q", iface.Type, iface.Name),
				)
			}
		case contract.InterfaceTypeEvent:
			if iface.Port != nil {
				result.AddWarning(
					fmt.Sprintf("interfaces[%d].port", i),
					"PORT_IGNORED",
					fmt.Sprintf("port is not applicable for event interface %q", iface.Name),
				)
			}
		}
	}
}

func validateInterfaceContracts(c *contract.Contract, result *ValidationResult) {
	for i, iface := range c.Interfaces {
		switch iface.Type {
		case contract.InterfaceTypeGRPC, contract.InterfaceTypeEvent:
			if iface.Contract == "" {
				result.AddError(
					fmt.Sprintf("interfaces[%d].contract", i),
					"CONTRACT_REQUIRED",
					fmt.Sprintf("contract is required for %s interface %q", iface.Type, iface.Name),
				)
			}
		}
	}
}

func validateHealthInterface(c *contract.Contract, result *ValidationResult) {
	if c.Runtime == nil || c.Runtime.Health == nil {
		return
	}
	healthIface := c.Runtime.Health.Interface

	var found *contract.Interface
	for i := range c.Interfaces {
		if c.Interfaces[i].Name == healthIface {
			found = &c.Interfaces[i]
			break
		}
	}

	if found == nil {
		result.AddError(
			"runtime.health.interface",
			"HEALTH_INTERFACE_NOT_FOUND",
			fmt.Sprintf("health interface %q does not match any declared interface", healthIface),
		)
		return
	}

	if found.Type == contract.InterfaceTypeEvent {
		result.AddError(
			"runtime.health.interface",
			"HEALTH_INTERFACE_INVALID",
			fmt.Sprintf("health interface %q is an event interface; health checks require http or grpc", healthIface),
		)
		return
	}

	if found.Type == contract.InterfaceTypeHTTP && c.Runtime.Health.Path == "" {
		result.AddError(
			"runtime.health.path",
			"HEALTH_PATH_REQUIRED",
			"health check path is required when the health interface type is http",
		)
	}

	if found.Type == contract.InterfaceTypeGRPC && c.Runtime.Health.Path != "" {
		result.AddWarning(
			"runtime.health.path",
			"HEALTH_PATH_IGNORED",
			"health check path is not used for grpc interfaces; gRPC uses the standard health protocol",
		)
	}
}

func validateInterfaceFiles(c *contract.Contract, bundleFS fs.FS, result *ValidationResult) {
	if bundleFS == nil {
		return
	}
	for i, iface := range c.Interfaces {
		if iface.Contract == "" {
			continue
		}
		if _, err := fs.Stat(bundleFS, iface.Contract); err != nil {
			result.AddError(
				fmt.Sprintf("interfaces[%d].contract", i),
				"FILE_NOT_FOUND",
				fmt.Sprintf("interface contract file %q not found in bundle", iface.Contract),
			)
		}
	}
}

func validateConfigFiles(c *contract.Contract, bundleFS fs.FS, result *ValidationResult) {
	if c.Configuration == nil || c.Configuration.Schema == "" {
		return
	}
	if bundleFS == nil {
		return
	}
	if _, err := fs.Stat(bundleFS, c.Configuration.Schema); err != nil {
		result.AddError(
			"configuration.schema",
			"FILE_NOT_FOUND",
			fmt.Sprintf("configuration schema file %q not found in bundle", c.Configuration.Schema),
		)
	}
}

func validateDependencyRefs(c *contract.Contract, result *ValidationResult) {
	for i, dep := range c.Dependencies {
		parsed := graph.ParseDependencyRef(dep.Ref)

		if parsed.IsOCI() {
			ref, err := contract.ParseOCIReference(parsed.Location)
			if err != nil {
				result.AddError(
					fmt.Sprintf("dependencies[%d].ref", i),
					"INVALID_OCI_REF",
					fmt.Sprintf("invalid OCI reference %q: %v", dep.Ref, err),
				)
				continue
			}

			if ref.Digest == "" && ref.Tag != "" {
				result.AddWarning(
					fmt.Sprintf("dependencies[%d].ref", i),
					"TAG_NOT_DIGEST",
					fmt.Sprintf("dependency %q uses a tag instead of a digest; digest pinning is recommended", dep.Ref),
				)
			}
		}

		if dep.Compatibility == "" {
			result.AddError(
				fmt.Sprintf("dependencies[%d].compatibility", i),
				"EMPTY_COMPATIBILITY",
				"compatibility range must not be empty",
			)
		} else if _, err := contract.ParseRange(dep.Compatibility); err != nil {
			result.AddError(
				fmt.Sprintf("dependencies[%d].compatibility", i),
				"INVALID_COMPATIBILITY",
				fmt.Sprintf("invalid compatibility range %q: %v", dep.Compatibility, err),
			)
		}
	}
}

func validateImageRef(c *contract.Contract, result *ValidationResult) {
	if c.Service.Image == nil {
		return
	}
	if _, err := contract.ParseOCIReference(c.Service.Image.Ref); err != nil {
		result.AddError(
			"service.image.ref",
			"INVALID_IMAGE_REF",
			fmt.Sprintf("invalid image reference %q: %v", c.Service.Image.Ref, err),
		)
	}
}

func validateScaling(c *contract.Contract, result *ValidationResult) {
	if c.Scaling == nil {
		return
	}
	if c.Scaling.Min > c.Scaling.Max {
		result.AddError(
			"scaling",
			"SCALING_MIN_EXCEEDS_MAX",
			fmt.Sprintf("scaling min (%d) must not exceed max (%d)", c.Scaling.Min, c.Scaling.Max),
		)
	}
}

func validateJobScaling(c *contract.Contract, result *ValidationResult) {
	if c.Runtime != nil && c.Runtime.Workload == contract.WorkloadTypeJob && c.Scaling != nil {
		result.AddError(
			"scaling",
			"JOB_SCALING_NOT_ALLOWED",
			"scaling must not be applied to job workloads",
		)
	}
}

func validateStatePersistenceInvariants(c *contract.Contract, result *ValidationResult) {
	if c.Runtime == nil {
		return
	}
	// Invariant: stateless services must use ephemeral durability.
	if c.Runtime.State.Type == contract.StateStateless &&
		c.Runtime.State.Persistence.Durability == contract.DurabilityPersistent {
		result.AddError(
			"runtime.state.persistence.durability",
			"STATELESS_PERSISTENT_CONFLICT",
			"stateless services must use ephemeral durability; persistent durability requires stateful or hybrid",
		)
	}
}
