package validation

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// validateInterfaceLifecycle checks each interfaces[].lifecycle: both dates are
// canonical YYYY-MM-DD, the sunset is not before the deprecation, and a
// successor either names another declared interface or is an absolute URL.
func validateInterfaceLifecycle(c *contract.Contract, result *ValidationResult) {
	names := map[string]bool{}
	for _, iface := range c.Interfaces {
		names[iface.Name] = true
	}
	for i, iface := range c.Interfaces {
		checkLifecycle(result, fmt.Sprintf("interfaces[%d].lifecycle", i), iface.Lifecycle, "INVALID_INTERFACE_LIFECYCLE",
			func(s string) bool { return validSuccessor(s, iface.Name, names) },
			"INTERFACE_SUCCESSOR_UNKNOWN", "successor %q names no other interface of this service; name one, or give an absolute URL such as an oci:// reference")
	}
}

// validateServiceLifecycle checks service.lifecycle (pactoVersion 2.4) like an
// interface lifecycle, except that its successor must be an absolute URL.
func validateServiceLifecycle(c *contract.Contract, result *ValidationResult) {
	checkLifecycle(result, "service.lifecycle", c.Service.Lifecycle, "INVALID_SERVICE_LIFECYCLE",
		func(s string) bool { return validSuccessor(s, "", nil) },
		"SERVICE_SUCCESSOR_INVALID", "successor %q must be an absolute URL such as an oci:// reference to the service that replaces this one")
}

// checkLifecycle checks the lifecycle l at path: both dates are canonical
// YYYY-MM-DD and the sunset is not before the deprecation (invalidCode), and a
// successor passes successorOK (successorCode with successorMsg, a format
// taking the successor). A nil lifecycle passes.
func checkLifecycle(result *ValidationResult, path string, l *contract.InterfaceLifecycle, invalidCode string, successorOK func(string) bool, successorCode, successorMsg string) {
	if l == nil {
		return
	}
	switch {
	case !isCanonicalDate(l.Deprecated):
		result.AddError(path+".deprecated", invalidCode, fmt.Sprintf("deprecated %q must be a valid YYYY-MM-DD date", l.Deprecated))
	case l.Sunset != "" && !isCanonicalDate(l.Sunset):
		result.AddError(path+".sunset", invalidCode, fmt.Sprintf("sunset %q must be a valid YYYY-MM-DD date", l.Sunset))
	case l.Sunset != "" && l.Sunset < l.Deprecated: // canonical dates compare as strings
		result.AddError(path+".sunset", invalidCode, fmt.Sprintf("sunset %s is before deprecated %s", l.Sunset, l.Deprecated))
	}
	if s := l.Successor; s != "" && !successorOK(s) {
		result.AddError(path+".successor", successorCode, fmt.Sprintf(successorMsg, s))
	}
}

// validSuccessor reports whether s is an absolute URL or names a declared
// interface other than self.
func validSuccessor(s, self string, names map[string]bool) bool {
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		return err == nil && u.Host != ""
	}
	return s != self && names[s]
}

// DeprecatedInterfaceWarnings returns one warning for each deprecated interface
// of provider, the contract a consumer's dependency at path resolved to. Each
// names the interface, its sunset and its successor. From the sunset date on the
// code is DEPENDENCY_INTERFACE_SUNSET instead of DEPENDENCY_INTERFACE_DEPRECATED.
//
// Both stay warnings: the consumer's own contract is still well-formed, and a
// verdict that flips with the calendar would make validation depend on when it
// runs. The distinct code is what a pipeline gates on to fail after the sunset.
// A contract does not say which of a provider's interfaces it calls, so every
// deprecated interface is reported.
func DeprecatedInterfaceWarnings(path string, provider *contract.Contract, now time.Time) []contract.ValidationWarning {
	var out []contract.ValidationWarning
	for _, iface := range provider.Interfaces {
		l := iface.Lifecycle
		if l == nil {
			continue
		}
		code, msg := "DEPENDENCY_INTERFACE_DEPRECATED", fmt.Sprintf("%s@%s interface %s is deprecated since %s",
			provider.Service.Name, provider.Service.Version, iface.Name, l.Deprecated)
		switch {
		case l.SunsetReached(now):
			code, msg = "DEPENDENCY_INTERFACE_SUNSET", msg+fmt.Sprintf(" and reached its sunset %s: it may be removed in any release", l.Sunset)
		case l.Sunset != "":
			msg += fmt.Sprintf(", sunset %s", l.Sunset)
		}
		if l.Successor != "" {
			msg += fmt.Sprintf("; successor %s", l.Successor)
		}
		out = append(out, contract.ValidationWarning{Path: path, Code: code, Message: msg})
	}
	return out
}
