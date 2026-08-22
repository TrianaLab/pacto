package dashboard

import (
	"testing"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

func sectionState(d *ServiceDetails, id string) SectionInfo { return d.SectionMeta[id] }

func TestComputeSectionMeta_BundleWithRuntime(t *testing.T) {
	d := &ServiceDetails{
		Interfaces:      []InterfaceInfo{{Name: "http"}},
		Configurations:  []ConfigurationInfo{{Name: "app"}},
		Workload:        "service",
		State:           &StateInfo{Type: "stateless"},
		Validation:      &ValidationInfo{Errors: []ValidationIssue{{Code: "E1"}}},
		ObservedRuntime: &ObservedRuntime{},
		Conditions:      []Condition{{Type: "Ready"}},
		Docs:            []DocInfo{{Path: "docs/x.md"}},
		Capabilities:    []CapabilityInfo{{Type: "health"}},
		Tools:           []CapabilityTool{{Name: "getX", Method: "GET", Path: "/x"}},
	}
	d.ContractStatus = StatusCompliant
	computeSectionMeta(d, "oci", true)

	if s := sectionState(d, SectionCapabilities); s.State != SectionPresent || s.Source != "oci" {
		t.Errorf("capabilities present from bundle: %+v", s)
	}

	if s := sectionState(d, SectionInterfaces); s.State != SectionPresent || s.Source != "oci" {
		t.Errorf("interfaces: %+v", s)
	}
	if s := sectionState(d, SectionPolicies); s.State != SectionEmpty || s.Reason == "" {
		t.Errorf("policies should be empty with reason: %+v", s)
	}
	if s := sectionState(d, SectionValidation); s.State != SectionPresent {
		t.Errorf("validation present (has errors): %+v", s)
	}
	if s := sectionState(d, SectionDocs); s.State != SectionPresent || s.Source != "oci" {
		t.Errorf("docs present from bundle: %+v", s)
	}
	if s := sectionState(d, SectionObservedRuntime); s.State != SectionPresent || s.Source != "k8s" {
		t.Errorf("observedRuntime present from k8s: %+v", s)
	}
	if s := sectionState(d, SectionResources); s.State != SectionEmpty || s.Source != "k8s" {
		t.Errorf("resources empty (evaluated, no data): %+v", s)
	}
}

func TestComputeSectionMeta_Reference(t *testing.T) {
	d := &ServiceDetails{Configurations: []ConfigurationInfo{{Name: "provisioning"}}}
	d.ContractStatus = StatusReference
	computeSectionMeta(d, "", true) // k8s overlay present (reference comes from k8s)

	if s := sectionState(d, SectionConfigurations); s.State != SectionPresent || s.Source != "k8s" {
		t.Errorf("config present from k8s for reference: %+v", s)
	}
	for _, id := range []string{SectionResources, SectionPorts, SectionEndpoints, SectionObservedRuntime, SectionRuntimeDiff, SectionConditions} {
		if s := sectionState(d, id); s.State != SectionNotApplicable || s.Reason == "" {
			t.Errorf("%s should be not_applicable for reference: %+v", id, s)
		}
	}
	// Docs unavailable: reference came via k8s (no bundle).
	if s := sectionState(d, SectionDocs); s.State != SectionUnavailable {
		t.Errorf("docs unavailable without bundle: %+v", s)
	}
	// Capabilities likewise unavailable without a bundle.
	if s := sectionState(d, SectionCapabilities); s.State != SectionUnavailable || s.Reason == "" {
		t.Errorf("capabilities unavailable without bundle: %+v", s)
	}
}

func TestComputeSectionMeta_BundleOnly_NoRuntime(t *testing.T) {
	d := &ServiceDetails{
		Interfaces: []InterfaceInfo{{Name: "http"}},
		Validation: &ValidationInfo{}, // no issues
	}
	d.ContractStatus = StatusCompliant
	computeSectionMeta(d, "local", false) // bundle only, no k8s

	if s := sectionState(d, SectionValidation); s.State != SectionEmpty || s.Reason != "no validation issues" {
		t.Errorf("validation empty/valid: %+v", s)
	}
	if s := sectionState(d, SectionDocs); s.State != SectionEmpty || s.Source != "local" {
		t.Errorf("docs empty from bundle (no docs packed): %+v", s)
	}
	if s := sectionState(d, SectionCapabilities); s.State != SectionEmpty || s.Source != "local" {
		t.Errorf("capabilities empty from bundle (none declared): %+v", s)
	}
	// Runtime sections: not evaluated, non-reference -> unavailable.
	for _, id := range []string{SectionResources, SectionObservedRuntime, SectionConditions} {
		if s := sectionState(d, id); s.State != SectionUnavailable || s.Reason == "" {
			t.Errorf("%s should be unavailable (no cluster): %+v", id, s)
		}
	}
}

func TestMarkRuntimeOverrides(t *testing.T) {
	base := &ServiceDetails{Service: Service{Version: "1.0.0", Owner: contract.Owner{Team: "team-a"}}}
	rt := &ServiceDetails{Service: Service{Version: "2.0.0", Owner: contract.Owner{Team: "team-b"}}}
	res := &ServiceDetails{SectionMeta: map[string]SectionInfo{}}
	markRuntimeOverrides(res, base, rt)
	if res.SectionMeta["version"].OverriddenBy != "k8s" {
		t.Errorf("expected version overridden by k8s, got %+v", res.SectionMeta["version"])
	}
	if res.SectionMeta["owner"].OverriddenBy != "k8s" {
		t.Errorf("expected owner overridden by k8s, got %+v", res.SectionMeta["owner"])
	}

	// No override when values match.
	same := &ServiceDetails{SectionMeta: map[string]SectionInfo{}}
	markRuntimeOverrides(same, base, &ServiceDetails{Service: Service{Version: "1.0.0", Owner: contract.Owner{Team: "team-a"}}})
	if _, ok := same.SectionMeta["version"]; ok {
		t.Error("did not expect version override when versions match")
	}
	if _, ok := same.SectionMeta["owner"]; ok {
		t.Error("did not expect owner override when owners match")
	}

	// An owner is a structure, not the string it renders as. A team and a person
	// who share a label are two owners, so the cluster naming one where the bundle
	// names the other IS an override — and the same owner with its contacts listed
	// in the other order is NOT.
	email, chat := contract.OwnerContact{Type: "email", Value: "a@acme.com"}, contract.OwnerContact{Type: "chat", Value: "#b"}
	for _, tc := range []struct {
		name     string
		from, rt contract.Owner
		want     bool
	}{{
		name: "a team replaced by a person of the same name",
		from: contract.Owner{Team: "team-a"},
		rt:   contract.Owner{DRI: "team-a"},
		want: true,
	}, {
		name: "the same owner with its contacts reordered",
		from: contract.Owner{Team: "team-a", Contacts: []contract.OwnerContact{email, chat}},
		rt:   contract.Owner{Team: "team-a", Contacts: []contract.OwnerContact{chat, email}},
		want: false,
	}, {
		name: "the same team, escalating to somebody else",
		from: contract.Owner{Team: "team-a", DRI: "alice"},
		rt:   contract.Owner{Team: "team-a", DRI: "bob"},
		want: true,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got := &ServiceDetails{SectionMeta: map[string]SectionInfo{}}
			markRuntimeOverrides(got,
				&ServiceDetails{Service: Service{Version: "1.0.0", Owner: tc.from}},
				&ServiceDetails{Service: Service{Version: "1.0.0", Owner: tc.rt}})
			if _, ok := got.SectionMeta["owner"]; ok != tc.want {
				t.Errorf("owner override = %v, want %v", ok, tc.want)
			}
		})
	}

	// No-op guards (nil base/runtime/sectionMeta).
	markRuntimeOverrides(&ServiceDetails{}, nil, rt)
	markRuntimeOverrides(&ServiceDetails{}, base, nil)
	markRuntimeOverrides(&ServiceDetails{SectionMeta: nil}, base, rt)
}

func TestComputeSectionMeta_K8sOnlyDefSource(t *testing.T) {
	d := &ServiceDetails{Interfaces: []InterfaceInfo{{Name: "grpc"}}}
	d.ContractStatus = StatusCompliant
	computeSectionMeta(d, "", true)
	if s := sectionState(d, SectionInterfaces); s.Source != "k8s" {
		t.Errorf("def source should be k8s when no bundle: %+v", s)
	}
}
