package validation

import (
	"fmt"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/graph"
)

// validateTemplate checks service.template (pactoVersion 2.4): each plugin a
// golden path names is listed once.
func validateTemplate(c *contract.Contract, result *ValidationResult) {
	if c.Service.Template == nil {
		return
	}
	seen := make(map[string]int)
	for i, p := range c.Service.Template.Plugins {
		if prev, exists := seen[p.Name]; exists {
			result.AddError(
				fmt.Sprintf("service.template.plugins[%d].name", i),
				string(finding.CodeDuplicateTemplatePlugin),
				fmt.Sprintf("plugin %q is already listed at service.template.plugins[%d]", p.Name, prev),
			)
			continue
		}
		seen[p.Name] = i
	}
}

// validateOrigin checks service.origin.ref (pactoVersion 2.4): an origin is an
// oci:// reference or a directory path, so another scheme or an oci://
// reference that does not parse is refused.
func validateOrigin(c *contract.Contract, result *ValidationResult) {
	if c.Service.Origin == nil {
		return
	}
	ref := c.Service.Origin.Ref
	parsed := graph.ParseDependencyRef(ref)
	if parsed.IsOCI() {
		if _, err := contract.ParseOCIReference(parsed.Location); err != nil {
			result.AddError("service.origin.ref", string(finding.CodeInvalidOriginRef), fmt.Sprintf("invalid OCI reference %q: %v", ref, err))
		}
		return
	}
	if strings.Contains(ref, "://") {
		result.AddError("service.origin.ref", string(finding.CodeInvalidOriginRef), fmt.Sprintf("origin %q must be an oci:// reference or a directory path", ref))
	}
}
