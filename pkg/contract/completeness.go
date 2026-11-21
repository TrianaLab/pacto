package contract

import (
	"slices"
	"strings"
)

// Contract completeness sections, in their fixed order.
const (
	SectionInterfaces    = "interfaces"
	SectionConfiguration = "configuration"
	SectionDependencies  = "dependencies"
	SectionOwner         = "owner"
	SectionSummary       = "summary"
	SectionDomain        = "domain"
	SectionRuntime       = "runtime"
	SectionReadiness     = "readiness"
)

// Contract completeness section states. Only declared counts as complete:
// inferred and unknown count in the denominator only.
const (
	CompletenessDeclared      = "declared"
	CompletenessInferred      = "inferred"
	CompletenessMissing       = "missing"
	CompletenessUnknown       = "unknown"
	CompletenessNotApplicable = "not-applicable"
)

// ContractCompleteness is how completely a contract describes its service,
// section by section. Percent is Declared over Applicable, rounded down; nil
// when no section applies.
type ContractCompleteness struct {
	Percent    *int                  `json:"percentComplete,omitempty" doc:"Declared sections over applicable sections, rounded down; absent when no section applies"`
	Declared   int                   `json:"declared" doc:"Sections pacto.yaml declares completely"`
	Applicable int                   `json:"applicable" doc:"Sections that apply to this service"`
	Sections   []CompletenessSection `json:"sections" doc:"Every section in its fixed order"`
}

// CompletenessSection is one section's state, why, and the YAML that would
// declare it when the inferred record proposes one.
type CompletenessSection struct {
	Section string `json:"section" enum:"interfaces,configuration,dependencies,owner,summary,domain,runtime,readiness"`
	State   string `json:"state" enum:"declared,inferred,missing,unknown,not-applicable"`
	Reason  string `json:"reason"`
	Declare string `json:"declare,omitempty" doc:"The YAML the inferred record proposes to declare this section"`
}

// sectionRule is how one section reads a contract: whether it applies
// without any inferred field, whether pacto.yaml declares it completely, and
// what is missing when it does not.
type sectionRule struct {
	name             string
	applies, declare func(*Contract) bool
	missing          string
}

func hasHealth(c *Contract) bool {
	return slices.ContainsFunc(c.Capabilities, func(k Capability) bool { return k.Type == CapabilityHealth })
}

func always(*Contract) bool { return true }

var sectionRules = []sectionRule{
	{SectionInterfaces, func(c *Contract) bool { return len(c.Interfaces) > 0 },
		func(c *Contract) bool {
			return len(c.Interfaces) > 0 && !slices.ContainsFunc(c.Interfaces, func(i Interface) bool { return i.Ref == "" })
		}, "an interface names no document (ref)"},
	{SectionConfiguration, func(c *Contract) bool { return len(c.Configurations) > 0 },
		func(c *Contract) bool {
			return len(c.Configurations) > 0 && !slices.ContainsFunc(c.Configurations, func(g Configuration) bool { return g.Schema == "" && g.Ref == "" })
		}, "a configuration has no schema"},
	{SectionDependencies, func(c *Contract) bool { return len(c.Dependencies) > 0 },
		func(c *Contract) bool {
			return len(c.Dependencies) > 0 && !slices.ContainsFunc(c.Dependencies, func(d Dependency) bool { return !IsRange(d.Compatibility) })
		}, "a dependency accepts no version range"},
	{SectionOwner, always, func(c *Contract) bool { return !c.Service.Owner.IsEmpty() }, "no owner"},
	{SectionSummary, always, func(c *Contract) bool { return c.Service.Summary != "" }, "no summary"},
	{SectionDomain, always, func(c *Contract) bool { return c.Service.Domain != "" }, "no business domain"},
	{SectionRuntime, func(c *Contract) bool { return c.Workload != "" },
		func(c *Contract) bool { return c.Workload != "" && hasHealth(c) }, "no workload or no health capability"},
	{SectionReadiness, func(c *Contract) bool { return c.Service.Template == nil },
		func(c *Contract) bool { return c.Readiness != nil && len(c.Readiness.Claims) > 0 }, "no readiness assessment"},
}

// SectionNames is every completeness section in its fixed order.
func SectionNames() []string {
	out := make([]string, 0, len(sectionRules))
	for _, r := range sectionRules {
		out = append(out, r.name)
	}
	return out
}

// SectionOfField is the completeness section an inferred record's field
// belongs to, or "" for a field no section reads (service.name, image).
func SectionOfField(field string) string {
	for prefix, section := range map[string]string{
		"interfaces[": SectionInterfaces, "ports[": SectionInterfaces,
		"configuration.": SectionConfiguration, "dependencies[": SectionDependencies,
		"capabilities[health]": SectionRuntime, "workload": SectionRuntime,
	} {
		if strings.HasPrefix(field, prefix) {
			return section
		}
	}
	switch field {
	case "service.owner":
		return SectionOwner
	case "service.summary":
		return SectionSummary
	case "service.domain":
		return SectionDomain
	}
	return ""
}

// Completeness checks the contract section by section. rec, the inferred
// record, is optional: a section pacto.yaml leaves out that the record knows
// reads inferred, with the YAML the record proposes, and a section the
// contract does not use applies when the record infers it.
func Completeness(c *Contract, rec *InferredRecord) ContractCompleteness {
	inferred := map[string][]Inferred{}
	if rec != nil {
		for _, f := range rec.Fields {
			if s := SectionOfField(f.Field); s != "" {
				inferred[s] = append(inferred[s], f)
			}
		}
	}
	out := ContractCompleteness{Sections: make([]CompletenessSection, 0, len(sectionRules))}
	for _, r := range sectionRules {
		s := r.check(c, inferred[r.name])
		if s.State != CompletenessNotApplicable {
			out.Applicable++
		}
		if s.State == CompletenessDeclared {
			out.Declared++
		}
		out.Sections = append(out.Sections, s)
	}
	if out.Applicable > 0 {
		out.Percent = new(out.Declared * 100 / out.Applicable)
	}
	return out
}

// check is the rule's section for c, given the record's fields for it.
func (r sectionRule) check(c *Contract, fields []Inferred) CompletenessSection {
	s := CompletenessSection{Section: r.name}
	switch {
	case !r.applies(c) && len(fields) == 0:
		s.State, s.Reason = CompletenessNotApplicable, "pacto.yaml declares none and Pacto inferred none"
	case r.declare(c):
		s.State, s.Reason = CompletenessDeclared, "pacto.yaml declares it"
	case len(fields) > 0:
		s.State, s.Reason, s.Declare = CompletenessInferred, inferredReason(fields[0].Source), declareOf(fields)
	default:
		s.State, s.Reason = CompletenessMissing, r.missing
	}
	return s
}

// inferredReason says where the record read a section pacto.yaml leaves out.
func inferredReason(source string) string {
	if source == "" {
		return "Pacto inferred it; pacto.yaml does not declare it"
	}
	return "Pacto read it from " + source + "; pacto.yaml does not declare it"
}

// declareOf joins the distinct YAML the fields propose, in record order.
func declareOf(fields []Inferred) string {
	var parts []string
	for _, f := range fields {
		if f.Declare != "" && !slices.Contains(parts, f.Declare) {
			parts = append(parts, f.Declare)
		}
	}
	return strings.Join(parts, "\n")
}
