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
//   - a dependency when it declares a workload or an interface, with the range of
//     its major version when that range is semver and admits it;
//   - a configuration reference when it declares a required configuration;
//   - a policy reference when it declares a policy and neither applies.
//
// It is nil when newest has no OCI reference or nothing to consume.
func consumeSnippet(offering string, provider *ServiceRecord, newest *ContractRevision) *ConsumeSnippet {
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
	sn.Policy = !sn.Dependency && !sn.Configuration && len(c.Policies) > 0
	var b strings.Builder
	if sn.Dependency {
		sn.Compatibility = majorRange(newest.Version)
		fmt.Fprintf(&b, "dependencies:\n  - name: %s\n    ref: %s\n    required: true\n", provider.Name, sn.Ref)
		if sn.Compatibility != "" {
			fmt.Fprintf(&b, "    compatibility: %q\n", sn.Compatibility)
		}
	}
	if sn.Configuration {
		fmt.Fprintf(&b, "configurations:\n  - name: %s\n    ref: %s\n    required: true\n", offering, sn.Ref)
	}
	if sn.Policy {
		fmt.Fprintf(&b, "policies:\n  - name: %s\n    ref: %s\n", offering, sn.Ref)
	}
	if b.Len() == 0 {
		return nil
	}
	sn.YAML = b.String()
	return sn
}

// majorRange is ^MAJOR.0.0 for version, empty unless that range parses and
// admits version, so a version that is not semver never gets a wrong range.
func majorRange(version string) string {
	major, _, _ := strings.Cut(version, ".")
	r := "^" + major + ".0.0"
	if CheckRange(r, version) != RangeSatisfied {
		return ""
	}
	return r
}
