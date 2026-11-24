package contract

import (
	"fmt"
	"regexp"
	"strings"
)

// ExternalDependencyName is the dependency name an external system with
// identity id is declared under: the identity when it is a valid dependency
// name, else its last component made one.
func ExternalDependencyName(id string) string {
	if externalDependencyNameRE.MatchString(id) {
		return id
	}
	return SanitizeName(id[strings.LastIndex(id, "/")+1:])
}

var externalDependencyNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ExternalDependencyYAML writes the dependencies block of an external system
// in one of the two shapes validation accepts: with no ref it names the
// external, its identity (when set), required and the version it runs against
// (never a range, which needs a ref); with a ref it names the ref, required
// and the compatibility range (when set), never external, identity or version.
func ExternalDependencyYAML(d Dependency) string {
	var b strings.Builder
	fmt.Fprintf(&b, "dependencies:\n  - name: %s\n", d.Name)
	if d.Ref != "" {
		fmt.Fprintf(&b, "    ref: %s\n    required: %t\n", d.Ref, d.Required)
		if d.Compatibility != "" {
			fmt.Fprintf(&b, "    compatibility: %q\n", d.Compatibility)
		}
		return b.String()
	}
	b.WriteString("    external: true\n")
	if d.Identity != "" {
		fmt.Fprintf(&b, "    identity: %s\n", d.Identity)
	}
	fmt.Fprintf(&b, "    required: %t\n    version: %s\n", d.Required, d.Version)
	return b.String()
}
