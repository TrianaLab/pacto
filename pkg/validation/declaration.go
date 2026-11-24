package validation

import (
	"cmp"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
)

// declarationStage keeps a declaration's record and pacto.yaml in step: every
// range the signer accepted (or, on a draft, requested) is the declared
// compatibility of a dependency pacto.yaml declares, and every proposed range
// nobody accepted stays compatibility: unknown. A record with no declaration
// is not checked. Every field a record says was carried from a registry
// declaration is declared in pacto.yaml, so a hand-edited bundle cannot
// claim a carry its contract does not hold. A declaration that claims an
// external system sits in a contract whose service.external names it.
func declarationStage(result *ValidationResult, c *contract.Contract, rec *contract.InferredRecord) {
	carriedStage(result, c, rec.Inputs.Declaration)
	d := rec.Declaration
	if d == nil {
		return
	}
	if id, ok := contract.InferredFromExternal(d.InferredFrom); ok && c.Service.External != id {
		result.AddError(contract.InferredRecordFile, string(finding.CodeDeclarationExternalMismatch),
			"the declaration claims external "+id+", but pacto.yaml declares service.external "+cmp.Or(c.Service.External, "none"))
	}
	declared := make(map[string]string, len(c.Dependencies))
	for _, dep := range c.Dependencies {
		declared["dependencies["+dep.Name+"].compatibility"] = dep.Compatibility
	}
	accepted := make(map[string]bool, len(d.Accepted)+len(d.Requested))
	for _, l := range []struct {
		verb string
		list []contract.InferredAcceptance
	}{{"accepts", d.Accepted}, {"requests", d.Requested}} {
		for _, a := range l.list {
			accepted[a.Field] = true
			if got, ok := declared[a.Field]; !ok || got != a.Range {
				result.AddError(contract.InferredRecordFile, string(finding.CodeDeclarationAcceptedMismatch),
					"the declaration "+l.verb+" "+a.Range+" on "+a.Field+", but pacto.yaml declares "+declaredText(got, ok))
			}
		}
	}
	for _, f := range rec.Fields {
		got := declared[f.Field]
		if f.Proposed == "" || got == "" || got == contract.CompatibilityUnknown || accepted[f.Field] {
			continue
		}
		result.AddError(contract.InferredRecordFile, string(finding.CodeDeclarationRangeNotAccepted),
			strings.TrimSuffix(f.Field, ".compatibility")+" declares compatibility "+got+", a proposed range nobody accepted; "+
				"keep compatibility: unknown or accept the proposal "+f.Proposed)
	}
}

// carriedStage adds an error for each key cited carries that pacto.yaml does
// not declare: service.owner needs an owner, dependencies[<name>] the
// dependency and its .compatibility a range other than unknown.
func carriedStage(result *ValidationResult, c *contract.Contract, cited *contract.CitedDeclaration) {
	if cited == nil {
		return
	}
	declared := map[string]bool{"service.owner": !c.Service.Owner.IsEmpty()}
	for _, dep := range c.Dependencies {
		declared["dependencies["+dep.Name+"]"] = true
		declared["dependencies["+dep.Name+"].compatibility"] = dep.Compatibility != "" && dep.Compatibility != contract.CompatibilityUnknown
	}
	for _, field := range strings.Split(cited.Fields, ",") {
		if !declared[field] {
			result.AddError(contract.InferredRecordFile, string(finding.CodeCarriedFieldNotDeclared),
				contract.InferredRecordFile+" says "+field+" was carried from "+cited.Record+", and pacto.yaml does not declare it")
		}
	}
}

// declaredText is how a declaration finding names a declared compatibility.
func declaredText(compatibility string, declared bool) string {
	switch {
	case !declared:
		return "no such dependency"
	case compatibility == "":
		return "no compatibility"
	}
	return "compatibility " + compatibility
}
