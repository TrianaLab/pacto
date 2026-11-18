package discover

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// The YAML snippets pacto init writes into a new pacto.yaml, shared with the
// declare text of an inferred record so the two never disagree.

// plainRE starts with a letter, a dot or a slash, or is x.y.z, so YAML never
// reads it as a number.
var plainRE = regexp.MustCompile(`^([A-Za-z./][A-Za-z0-9._/-]*|[0-9]+\.[0-9]+\.[0-9]+[A-Za-z0-9._+-]*)$`)

// yamlWords read as booleans or null when plain.
var yamlWords = map[string]bool{"true": true, "false": true, "yes": true, "no": true, "on": true, "off": true, "null": true, "y": true, "n": true}

// Quote writes s as a YAML string: plain when YAML reads it back as that same
// string, else single-quoted.
func Quote(s string) string {
	if plainRE.MatchString(s) && !yamlWords[strings.ToLower(s)] {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// InterfaceYAML declares the interface name of type typ the spec at ref
// describes.
func InterfaceYAML(name, typ, ref string) string {
	return "interfaces:\n  - name: " + name + "\n    type: " + typ + "\n    ref: " + Quote(ref) + "\n"
}

// HealthYAML declares a health capability, bound to the api interface at
// path when path is set and api is true.
func HealthYAML(path string, api bool) string {
	out := "capabilities:\n  - type: health\n"
	if path != "" && api {
		out += "    binding:\n      type: http\n      interface: api\n      path: " + Quote(path) + "\n"
	}
	return out
}

// DependencyYAML is one dependencies entry with compatibility unknown and
// comment beside it.
func DependencyYAML(name, ref, comment string) string {
	return fmt.Sprintf("  - name: %s\n    ref: %s\n    required: true\n    compatibility: %s   # %s\n", name, ref, contract.CompatibilityUnknown, comment)
}
