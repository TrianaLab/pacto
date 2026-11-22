package contract

import (
	"regexp"
	"regexp/syntax"
	"strings"
	"testing"
)

// maxPatternInsts bounds how long a package pattern's compiled program may be.
// regexp compiles a program recursing up to once per instruction, and a WebKit
// worker's call stack ran out at about 500 (a {1,253} repeat) during package
// init, which stopped the WASM demo loading. The longest pattern 4.5.0 shipped
// was 270, so 300 keeps that headroom.
const maxPatternInsts = 300

// patternInsts is the length of re's compiled program.
func patternInsts(t *testing.T, re *regexp.Regexp) int {
	t.Helper()
	parsed, err := syntax.Parse(re.String(), syntax.Perl)
	if err != nil {
		t.Fatal(err)
	}
	prog, err := syntax.Compile(parsed.Simplify())
	if err != nil {
		t.Fatal(err)
	}
	return len(prog.Inst)
}

func TestPatternsStayShallow(t *testing.T) {
	res := []*regexp.Regexp{ociTagRe, ociDigestRe, serviceNameRE, nameSeparatorRE, surfacePortRE, surfaceClientRE,
		surfaceSourceRE, inferredHostRE, inferredKeyRE, inferredCatalogRE, identitySegmentRE, clusterObjectRE,
		clusterLabelKeyRE, clusterVersionsRE, coverageReasonRE, inferredDigestRE, depRefKeyRE, depCompatKeyRE,
		breakExpiresKeyRE, breakExpiresDateRE, inferredFromRE, pinnedRefRE, acceptanceFieldRE, draftTagRE,
		unknownFieldRe, pathIndexRe}
	for _, re := range sourceForms {
		res = append(res, re)
	}
	for _, re := range res {
		if n := patternInsts(t, re); n > maxPatternInsts {
			t.Errorf("%s compiles to %d instructions, want at most %d: bound its length in code", re, n, maxPatternInsts)
		}
	}
}

// The length checks that replaced the long repeats accept and reject exactly
// what the repeats did.
func TestLengthBoundsMatchTheRepeats(t *testing.T) {
	label := "a" + strings.Repeat("b", 251) + "c" // 253 bytes
	seg := "a" + strings.Repeat("b", 61) + "c"    // 63 bytes
	for _, c := range []struct {
		name string
		ok   func(string) bool
		in   string
		want bool
	}{
		{"client at 500", validSurfaceClient, strings.Repeat("a", 500) + "/443", true},
		{"client past 500", validSurfaceClient, strings.Repeat("a", 501) + "/443", false},
		{"client with slashes", validSurfaceClient, "ns/deploy/web/8080", true},
		{"client without port", validSurfaceClient, "web/", false},
		{"source at 253 runes", validSurfaceSource, strings.Repeat("é", 253), true},
		{"source past 253 runes", validSurfaceSource, strings.Repeat("é", 254), false},
		{"source with a space", validSurfaceSource, "a b", false},
		{"object name at 253", validClusterObject, "ns/Deployment/" + label, true},
		{"object name past 253", validClusterObject, "ns/Deployment/" + label + "d", false},
		{"object name ends in a dot", validClusterObject, "ns/Deployment/web.", false},
		{"label prefix at 253", validClusterLabelKey, label + "/app", true},
		{"label prefix past 253", validClusterLabelKey, label + "d/app", false},
		{"label without prefix", validClusterLabelKey, "app.kubernetes.io", true},
		{"identity of five segments", validIdentity, "a/b/c/d/" + seg, true},
		{"identity of six segments", validIdentity, "a/b/c/d/e/f", false},
		{"identity segment past 63", validIdentity, seg + "d", false},
		{"identity with an empty segment", validIdentity, "a//b", false},
	} {
		if got := c.ok(c.in); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
