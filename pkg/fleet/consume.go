package fleet

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// consumeSnippet renders how to consume provider from newest, its newest
// readable revision that is not invalid:
//   - a dependency when it declares a workload or an interface, with the range
//     majorRange gives for its version, or a line asking for one when none does;
//   - a configuration reference when it declares a required configuration;
//   - a policy reference when it declares a policy, or ships the fixed-path
//     policy/schema.json, and neither applies.
//
// It is nil when newest has no OCI reference or nothing to consume.
func consumeSnippet(provider *ServiceRecord, newest *ContractRevision) *ConsumeSnippet {
	if newest == nil {
		return nil
	}
	ref := cmp.Or(newest.ResolvedRef, newest.RequestedRef)
	repo, _, _ := contract.SplitOCIRef(contract.TrimOCIScheme(ref))
	if repo == "" || strings.Contains(repo, "://") {
		return nil
	}
	c := newest.Contract
	sn := &ConsumeSnippet{
		Ref:           contract.OCIScheme + repo,
		Dependency:    c.Workload != "" || len(c.Interfaces) > 0,
		Configuration: slices.ContainsFunc(c.Configurations, func(cfg contract.Configuration) bool { return cfg.Required }),
	}
	sn.Policy = !sn.Dependency && !sn.Configuration && (len(c.Policies) > 0 || newest.fixedPolicy)
	var b strings.Builder
	if sn.Dependency {
		sn.Compatibility = majorRange(newest.Version)
		fmt.Fprintf(&b, "dependencies:\n  - name: %s\n    ref: %s\n    required: true\n", provider.Name, sn.Ref)
		if sn.Compatibility != "" {
			fmt.Fprintf(&b, "    compatibility: %q\n", sn.Compatibility)
		} else {
			// A dependency with a ref needs a range; say so rather than omit it.
			b.WriteString("    compatibility: \"\" # set a semver range: the newest version is not semver\n")
		}
	}
	if sn.Configuration {
		fmt.Fprintf(&b, "configurations:\n  - name: %s\n    ref: %s\n    required: true\n", provider.Name, sn.Ref)
	}
	if sn.Policy {
		fmt.Fprintf(&b, "policies:\n  - name: %s\n    ref: %s\n", provider.Name, sn.Ref)
	}
	if b.Len() == 0 {
		return nil
	}
	sn.YAML = b.String()
	return sn
}

// majorRange is the widest caret range that admits version: ^MAJOR.0.0, or
// ^0.MINOR.0 for a 0.x version, or ^version for a prerelease, which a range
// admits only when it names it. It is empty when none does, so a version that is
// not semver never gets a wrong range.
func majorRange(version string) string {
	major, rest, _ := strings.Cut(version, ".")
	minor, _, _ := strings.Cut(rest, ".")
	candidates := []string{"^" + major + ".0.0"}
	if major == "0" {
		candidates = append(candidates, "^0."+minor+".0")
	}
	for _, r := range append(candidates, "^"+version) {
		if CheckRange(r, version) == RangeSatisfied {
			return r
		}
	}
	return ""
}
