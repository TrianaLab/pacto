package managedres

import (
	"cmp"
	"regexp"
	"strings"
)

// The origins of an [Engine] value: observed is what the cloud reports in
// status, desired only what spec asks for.
const (
	EngineObserved = "observed"
	EngineDesired  = "desired"
)

// maxEngine bounds an engine value; a longer one is dropped.
const maxEngine = 64

// engineRE is the shape of a kept engine value.
var engineRE = regexp.MustCompile(`^[a-z0-9._ -]+$`)

// Engine is the engine obj runs, "<engine> <version>" lower-cased, and its
// origin: observed from status.atProvider or status, else desired from
// spec.forProvider or spec. The keys read are engine with engineVersion or
// version, then databaseVersion. A value longer than 64 bytes or outside
// [a-z0-9._ -] is dropped; with none, both are empty.
func Engine(obj map[string]any) (value, origin string) {
	for _, place := range []struct {
		origin string
		path   []string
	}{
		{EngineObserved, []string{"status", "atProvider"}}, {EngineObserved, []string{"status"}},
		{EngineDesired, []string{"spec", "forProvider"}}, {EngineDesired, []string{"spec"}},
	} {
		if v := engineAt(dig(obj, place.path...)); v != "" {
			return v, place.origin
		}
	}
	return "", ""
}

// engineAt is the kept engine value of the fields m holds, empty when none.
func engineAt(m any) string {
	v := strings.TrimSpace(str(m, "engine") + " " + cmp.Or(str(m, "engineVersion"), str(m, "version")))
	v = strings.ToLower(cmp.Or(v, str(m, "databaseVersion")))
	if len(v) > maxEngine || !engineRE.MatchString(v) {
		return ""
	}
	return v
}
