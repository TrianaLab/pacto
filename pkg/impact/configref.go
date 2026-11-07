package impact

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
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
	for _, st := range []validation.Stage{
		validation.ConfigRefStage(&c, refs, validation.ValidationResult{}, validation.ConfigRefOptions{}),
		validation.EnvironmentStageWith(&c, fsys, refs),
	} {
		for _, e := range st.Errors {
			if e.Code != "ENVIRONMENT_UNKNOWN_CONFIGURATION" { // a configuration this check left out
				violations = append(violations, e.Message+" against "+who)
			}
		}
	}
	if len(violations) > 0 {
		return CompatibilityIncompatible, strings.Join(violations, "; ")
	}
	return CompatibilityCompatible, "the values for configuration " + strings.Join(names, ", ") + " satisfy the schema of " + who
}

// unreadableEnvironments says which environment file of c cannot be read, or
// "" when every one can: values Pacto cannot read are unknown, never a break.
func unreadableEnvironments(c *contract.Contract, fsys fs.FS) string {
	if len(c.Environments) > 0 && fsys == nil {
		return "the consumer's environment files are not in this snapshot, so its values cannot be checked"
	}
	for _, env := range c.Environments {
		raw, err := fs.ReadFile(fsys, env.File)
		if err == nil {
			_, err = contract.ParseEnvironmentValues(raw)
		}
		if err != nil {
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

// unresolvedConfigRefConsumers counts the declared configuration references the
// snapshot could not resolve that may name changed: by the repository of one of
// its revisions, or, as dependencies resolve, by a last path segment that is its
// name (with or without -pacto), which also covers a fleet read from
// directories and a relative ref. They may consume it, at a revision Pacto
// cannot tell.
func unresolvedConfigRefConsumers(snap *fleet.FleetSnapshot, changed fleet.ServiceKey) int {
	_, name := fleet.ParseServiceKey(changed)
	repos := map[string]bool{}
	for _, rev := range snap.Revisions {
		if rev.ServiceKey != changed {
			continue
		}
		for _, ref := range []string{rev.RequestedRef, rev.ResolvedRef} {
			if r := repository(ref); r != "" {
				repos[r] = true
			}
		}
	}
	n := 0
	for _, r := range snap.Relationships {
		if r.Type != fleet.RelationshipConfigRef || r.Provenance != fleet.ProvenanceDeclared || r.Resolved {
			continue
		}
		repo := repository(r.RequestedRef)
		last := strings.TrimSuffix(path.Base(strings.TrimRight(repo, "/")), "-pacto")
		if repos[repo] || last == name {
			n++
		}
	}
	return n
}

// repository is the repository a ref names, without scheme, tag or digest.
func repository(ref string) string {
	repo, _, _ := contract.SplitOCIRef(contract.TrimOCIScheme(ref))
	return repo
}
