package discover

import (
	"regexp"
	"slices"
	"strconv"

	"github.com/trianalab/pacto/v4/pkg/schemax"
)

// Literals maps each folder file to the sensitive keys it sets to a literal
// value for the workload: an inline env value or a key of a folder ConfigMap
// the workload reads. It reads the environment before resolveEnv lets a later
// variable replace an earlier one of the same name, because the earlier
// literal is still in the file the bundle publishes.
func (sc *Scan) Literals() map[string][]string {
	out := map[string][]string{}
	add := func(file, name, value string) {
		if SensitiveLiteral(name, value) && !slices.Contains(out[file], name) {
			out[file] = append(out[file], name)
		}
	}
	for _, v := range sc.Workload.Env {
		cm, found := sc.ConfigMaps[v.Ref]
		switch {
		case v.From == "configmap" && found && v.Key == "":
			for k, val := range cm.data {
				add(cm.file, v.Name+k, val)
			}
		case v.From == "configmap" && found:
			add(cm.file, v.Name, cm.data[v.Key])
		case v.From == "":
			add(v.File, v.Name, v.Value)
		}
	}
	return out
}

// EnvProperty is an environment variable's schema property: its key and
// type, never its value.
func EnvProperty(e EnvVar) map[string]any {
	switch {
	case e.Secret:
		return map[string]any{"type": "string", "writeOnly": true, "description": "Read from the Secret " + e.Ref + ", whose values pacto init does not read."}
	case schemax.SensitiveKey(e.Name) || schemax.SensitiveValue(literal(e.Value)):
		return map[string]any{"type": "string", "writeOnly": true, "description": "Sensitive: pacto init does not copy its value."}
	}
	return map[string]any{"type": TypeOf(e.Value)}
}

// TypeOf is an environment value's JSON Schema type: an integer, a boolean or
// a string.
func TypeOf(v string) string {
	if _, err := strconv.ParseInt(v, 10, 64); err == nil {
		return "integer"
	}
	if v == "true" || v == "false" {
		return "boolean"
	}
	return "string"
}

// varRef is a Kubernetes dependent-variable reference, $(NAME), which the
// kubelet expands from another variable when it reads env[].value.
var varRef = regexp.MustCompile(`\$\([A-Za-z_][A-Za-z0-9_.-]*\)`)

// literal is the part of an env value the manifest itself sets: v less its
// $(NAME) references, which hold no value of their own.
func literal(v string) string {
	return varRef.ReplaceAllString(v, "")
}

// SensitiveLiteral reports whether an env variable named name sets a literal
// value that is sensitive, by name or as a URL with credentials.
func SensitiveLiteral(name, value string) bool {
	v := literal(value)
	return v != "" && (schemax.SensitiveKey(name) || schemax.SensitiveValue(v))
}
