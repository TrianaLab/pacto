package contract

import (
	"slices"
	"testing"
)

func sectionState(t *testing.T, cc ContractCompleteness, section string) CompletenessSection {
	t.Helper()
	for _, s := range cc.Sections {
		if s.Section == section {
			return s
		}
	}
	t.Fatalf("no section %q in %+v", section, cc.Sections)
	return CompletenessSection{}
}

func fullContract() *Contract {
	return &Contract{
		Service:        Service{Name: "a", Owner: Owner{Team: "t"}, Summary: "s", Domain: "d"},
		Interfaces:     []Interface{{Name: "api", Type: InterfaceTypeOpenAPI, Ref: "openapi.yaml"}},
		Configurations: []Configuration{{Name: "default", Schema: "config.schema.json"}},
		Dependencies:   []Dependency{{Name: "b", Compatibility: "^1.0.0"}},
		Workload:       "service",
		Capabilities:   []Capability{{Type: CapabilityHealth}},
		Readiness:      &Readiness{Expires: "2030-01-01", Claims: []ReadinessClaim{{ID: "r", Status: StatusDone}}},
	}
}

func TestCompletenessAllDeclared(t *testing.T) {
	cc := Completeness(fullContract(), nil)
	if cc.Percent == nil || *cc.Percent != 100 || cc.Declared != 8 || cc.Applicable != 8 {
		t.Fatalf("got %+v", cc)
	}
	want := []string{SectionInterfaces, SectionConfiguration, SectionDependencies, SectionOwner, SectionSummary, SectionDomain, SectionRuntime, SectionReadiness}
	if !slices.Equal(SectionNames(), want) {
		t.Errorf("SectionNames() = %v", SectionNames())
	}
	for i, s := range cc.Sections {
		if s.Section != want[i] || s.State != CompletenessDeclared {
			t.Errorf("section %d = %+v, want %s declared", i, s, want[i])
		}
	}
}

func TestCompletenessWorkerNoInterfacesNotAsked(t *testing.T) {
	c := fullContract()
	c.Interfaces = nil
	cc := Completeness(c, &InferredRecord{Fields: []Inferred{{Field: "service.name", Value: "a"}}})
	if s := sectionState(t, cc, SectionInterfaces); s.State != CompletenessNotApplicable {
		t.Fatalf("interfaces = %+v", s)
	}
	if cc.Applicable != 7 || *cc.Percent != 100 {
		t.Fatalf("got %+v", cc)
	}
}

func TestCompletenessInterfaceWithoutRefMissing(t *testing.T) {
	c := fullContract()
	c.Interfaces = append(c.Interfaces, Interface{Name: "events", Type: "asyncapi"})
	s := sectionState(t, Completeness(c, nil), SectionInterfaces)
	if s.State != CompletenessMissing || s.Reason == "" || s.Declare != "" {
		t.Fatalf("interfaces = %+v", s)
	}
}

func TestCompletenessDependencyUnknownRangeMissing(t *testing.T) {
	c := fullContract()
	c.Dependencies[0].Compatibility = CompatibilityUnknown
	if s := sectionState(t, Completeness(c, nil), SectionDependencies); s.State != CompletenessMissing {
		t.Fatalf("dependencies = %+v", s)
	}
}

func TestCompletenessConfigurationWithoutSchemaMissing(t *testing.T) {
	c := fullContract()
	c.Configurations = []Configuration{{Name: "default", Values: map[string]any{"A": 1}}}
	if s := sectionState(t, Completeness(c, nil), SectionConfiguration); s.State != CompletenessMissing {
		t.Fatalf("configuration = %+v", s)
	}
}

func TestCompletenessRecordOnlyIsInferred(t *testing.T) {
	c := fullContract()
	c.Service.Owner = Owner{}
	rec := &InferredRecord{Fields: []Inferred{
		{Field: "service.owner", Value: "team-a", Source: "CODEOWNERS:1", Declare: "service:\n  owner:\n    team: team-a\n"},
		{Field: "service.owner", Value: "team-a", Source: "catalog-info.yaml:3", Declare: "service:\n  owner:\n    team: team-a\n"},
	}}
	cc := Completeness(c, rec)
	s := sectionState(t, cc, SectionOwner)
	if s.State != CompletenessInferred || s.Declare != "service:\n  owner:\n    team: team-a\n" || s.Reason != "Pacto read it from CODEOWNERS:1; pacto.yaml does not declare it" {
		t.Fatalf("owner = %+v", s)
	}
	if cc.Declared != 7 || cc.Applicable != 8 || *cc.Percent != 87 {
		t.Fatalf("inferred must count in the denominator only: %+v", cc)
	}
}

func TestCompletenessRecordMakesSectionApply(t *testing.T) {
	c := fullContract()
	c.Configurations, c.Workload, c.Capabilities = nil, "", nil
	rec := &InferredRecord{Fields: []Inferred{
		{Field: "configuration.A", Declare: `"A": {"type":"string"}`},
		{Field: "configuration.B", Declare: `"B": {"type":"string"}`},
		{Field: "workload"},
	}}
	cc := Completeness(c, rec)
	if s := sectionState(t, cc, SectionConfiguration); s.State != CompletenessInferred || s.Declare != "\"A\": {\"type\":\"string\"}\n\"B\": {\"type\":\"string\"}" {
		t.Fatalf("configuration = %+v", s)
	}
	if s := sectionState(t, cc, SectionRuntime); s.State != CompletenessInferred || s.Reason != "Pacto inferred it; pacto.yaml does not declare it" || s.Declare != "" {
		t.Fatalf("runtime = %+v", s)
	}
}

func TestCompletenessPercentRounding(t *testing.T) {
	c := fullContract()
	c.Service.Summary, c.Readiness, c.Capabilities = "", nil, nil
	cc := Completeness(c, nil)
	if cc.Declared != 5 || cc.Applicable != 8 || *cc.Percent != 62 {
		t.Fatalf("got %+v", cc)
	}
}

func TestCompletenessNilRecord(t *testing.T) {
	c := &Contract{Service: Service{Name: "a", Template: &Template{}}}
	cc := Completeness(c, nil)
	if cc.Applicable != 3 || cc.Declared != 0 || *cc.Percent != 0 {
		t.Fatalf("got %+v", cc)
	}
	if s := sectionState(t, cc, SectionReadiness); s.State != CompletenessNotApplicable {
		t.Fatalf("a golden path carries no readiness: %+v", s)
	}
	for _, name := range []string{SectionOwner, SectionSummary, SectionDomain} {
		if s := sectionState(t, cc, name); s.State != CompletenessMissing {
			t.Errorf("%s = %+v", name, s)
		}
	}
}

func TestCompletenessNoSectionAppliesHasNoPercent(t *testing.T) {
	saved := sectionRules
	t.Cleanup(func() { sectionRules = saved })
	sectionRules = sectionRules[:1]
	if cc := Completeness(&Contract{}, nil); cc.Percent != nil || cc.Applicable != 0 {
		t.Fatalf("got %+v", cc)
	}
}

// TestSectionOfFieldMapsEveryInferredField pins which inferred record field
// each section reads.
func TestSectionOfFieldMapsEveryInferredField(t *testing.T) {
	for field, want := range map[string]string{
		"interfaces[api]":                    SectionInterfaces,
		"ports[8080]":                        SectionInterfaces,
		"configuration.DB_PASSWORD":          SectionConfiguration,
		"dependencies[ledger]":               SectionDependencies,
		"dependencies[ledger].compatibility": SectionDependencies,
		"dependencies[ledger].ref":           SectionDependencies,
		"service.owner":                      SectionOwner,
		"service.summary":                    SectionSummary,
		"service.domain":                     SectionDomain,
		"workload":                           SectionRuntime,
		"capabilities[health]":               SectionRuntime,
		"service.name":                       "",
		"service.version":                    "",
		"pactoVersion":                       "",
		"image":                              "",
		"image.digest[app]":                  "",
		"environments[staging].file":         "",
		"breaks[0].expires":                  "",
		"publish.build":                      "",
	} {
		if got := SectionOfField(field); got != want {
			t.Errorf("SectionOfField(%q) = %q, want %q", field, got, want)
		}
	}
}
