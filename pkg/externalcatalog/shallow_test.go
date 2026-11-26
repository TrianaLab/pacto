package externalcatalog

import (
	"regexp"
	"regexp/syntax"
	"testing"
)

// The package compiles into the WASM demo, where a WebKit worker's stack ran
// out on a pattern of about 500 instructions during package init; the
// contract package's TestPatternsStayShallow sets the same 300 bound.
func TestPatternsStayShallow(t *testing.T) {
	for _, re := range []*regexp.Regexp{hostPatternRE, keyPatternRE, sdkModuleRE, goMajorRE, terraformRE, crossplaneRE, inventoryRE, openAPIRE,
		labelRE, pypiRunRE, hostKeyRE} {
		parsed, err := syntax.Parse(re.String(), syntax.Perl)
		if err != nil {
			t.Fatal(err)
		}
		prog, err := syntax.Compile(parsed.Simplify())
		if err != nil {
			t.Fatal(err)
		}
		if n := len(prog.Inst); n > 300 {
			t.Errorf("%s compiles to %d instructions, want at most 300: bound its length in code", re, n)
		}
	}
}
