package impact

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// configRefVerdict judges a consumer that reaches the changed service only
// through configuration references: each referencing revision's inline and
// environment values are checked against the schema the new revision gives the
// configuration they select. The revision that accepts it least speaks for the
// consumer, as with dependency ranges.
func configRefVerdict(snap *fleet.FleetSnapshot, refs []fleet.Relationship, provider *contract.Bundle) (verdict, note string) {
	byRev := map[fleet.RevisionKey][]string{}
	var order []fleet.RevisionKey
	for _, r := range refs {
		if byRev[r.FromRevision] == nil {
			order = append(order, r.FromRevision)
		}
		byRev[r.FromRevision] = append(byRev[r.FromRevision], r.To)
	}
	slices.Sort(order)
	for i, rk := range order {
		v, n := revisionConfigRefVerdict(snap.Revisions[rk], byRev[rk], provider)
		if i == 0 || verdictRank(v) > verdictRank(verdict) {
			verdict, note = v, n
		}
	}
	return verdict, note
}

// revisionConfigRefVerdict checks the values rev gives the named configuration
// references against provider. A selection rev's version makes an error is
// incompatible. It is unknown when the values or the schema cannot be read or
// an older consumer's selection fails, and says why.
func revisionConfigRefVerdict(rev *fleet.ContractRevision, names []string, provider *contract.Bundle) (string, string) {
	who := provider.Contract.Service.Name + "@" + provider.Contract.Service.Version
	if rev == nil || rev.Contract == nil {
		return CompatibilityUnknown, "the consumer's contract cannot be read, so its values for " + who + " cannot be checked"
	}
	fsys := rev.BundleFS()
	if why := unreadableEnvironments(rev.Contract, fsys); why != "" {
		return CompatibilityUnknown, why
	}
	// Only the configurations that name the changed service: values the
	// consumer gives anything else are no business of this change.
	c := *rev.Contract
	c.Configurations = nil
	byRef := providerByRef{}
	for _, cfg := range rev.Contract.Configurations {
		if slices.Contains(names, cfg.Name) {
			c.Configurations = append(c.Configurations, cfg)
			byRef[cfg.Ref] = provider
		}
	}
	refs, res := validation.ResolveConfigRefs(context.Background(), &c, fsys, validation.ConfigRefOptions{Resolver: byRef})
	// A selection the consumer's own version makes an error, such as an
	// ambiguous ref in 2.4, is a confirmed break, not uncertainty.
	if len(res.Errors) > 0 {
		var why []string
		for _, e := range res.Errors {
			why = append(why, e.Message)
		}
		return CompatibilityIncompatible, strings.Join(why, "; ")
	}
	for _, cfg := range c.Configurations {
		if err := refs[cfg.Name].Err; err != nil {
			return CompatibilityUnknown, err.Error() + " in " + who
		}
	}
	var violations []string
	recorded := recordedTierFindings(rev)
	for _, st := range []validation.Stage{
		validation.ConfigRefStage(&c, refs, validation.ValidationResult{}, validation.ConfigRefOptions{}),
		validation.EnvironmentStageWith(&c, fsys, refs),
	} {
		for _, e := range st.Errors {
			// ENVIRONMENT_UNKNOWN_CONFIGURATION is a configuration this check
			// left out; a recorded tier finding is the consumer's own, there
			// before this change.
			if e.Code != "ENVIRONMENT_UNKNOWN_CONFIGURATION" && !recorded[tierKey(e.Path, e.Code, e.Message)] {
				msg, _, _ := strings.Cut(e.Message, ". Next: ")
				violations = append(violations, msg+" against "+who)
			}
		}
	}
	if len(violations) > 0 {
		return CompatibilityIncompatible, strings.Join(violations, "; ")
	}
	return CompatibilityCompatible, "the values for configuration " + strings.Join(names, ", ") + " satisfy the schema of " + who
}

// recordedTierFindings is the set of tierKey of the TierNotTunable findings
// rev's own validation run recorded, against the provider revision the
// snapshot holds.
func recordedTierFindings(rev *fleet.ContractRevision) map[string]bool {
	out := map[string]bool{}
	for _, f := range rev.Validation {
		if k := tierKey(f.ContractPath, string(f.Code), f.Message); k != "" {
			out[k] = true
		}
	}
	return out
}

// tierKey names a TierNotTunable finding by its path and the field it names,
// leaving out the offering version, which differs between revisions; "" for
// any other code.
func tierKey(path, code, msg string) string {
	if code != string(finding.CodeTierNotTunable) {
		return ""
	}
	field, _, _ := strings.Cut(msg, " in offering ")
	return path + "\x00" + field
}

// unreadableEnvironments says which environment file of c cannot be read, or
// "" when every one can: values Pacto cannot read are unknown, never a break.
// An environment with no file has no values to read.
func unreadableEnvironments(c *contract.Contract, fsys fs.FS) string {
	if fsys == nil && slices.ContainsFunc(c.Environments, func(e contract.Environment) bool { return e.File != "" }) {
		return "the consumer's environment files are not in this snapshot, so its values cannot be checked"
	}
	for _, env := range c.Environments {
		if _, _, err := contract.ReadEnvironmentValues(fsys, env); err != nil {
			return "environment " + env.Name + ": cannot read " + env.File + ": " + err.Error()
		}
	}
	return ""
}

// providerByRef resolves exactly the consumer's references to the changed
// service, to its new revision. Anything further, such as a selected
// configuration that is itself a reference, is left unresolved.
type providerByRef map[string]*contract.Bundle

func (m providerByRef) ResolveBundle(_ context.Context, ref string) (*contract.Bundle, error) {
	if b := m[ref]; b != nil {
		return b, nil
	}
	return nil, errors.New("impact checks one revision and does not follow " + ref)
}
