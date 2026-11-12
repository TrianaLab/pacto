package fleet

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/validation"
)

// MaxPlacementRevisions caps the revisions listed per environment group.
const MaxPlacementRevisions = 8

// EnvironmentPlacement is one environment of a service: declared by its
// current revision, reported by its targets, or both, with how many of those
// targets run a revision that declares it.
type EnvironmentPlacement struct {
	Environment         string             `json:"environment,omitempty" doc:"The environment name; empty for targets that report no label or whose sources disagree"`
	Targets             int                `json:"targets" doc:"Observed: the targets you can see that report this environment"`
	Declared            int                `json:"declared" doc:"Targets whose exactly linked revision declares this environment"`
	NotDeclared         int                `json:"notDeclared" doc:"Targets whose exactly linked revision does not declare it"`
	Unknown             int                `json:"unknown" doc:"Targets Pacto cannot check: no label, conflicting labels, no linked revision or one linked only by a mutable tag or version (inferred, not proof)"`
	Revisions           []EntityRef        `json:"revisions" doc:"The distinct revisions these targets run, by an exact link only, newest first, at most 8"`
	RevisionsTotal      int                `json:"revisionsTotal" doc:"How many distinct revisions these targets run"`
	RevisionsTruncated  bool               `json:"revisionsTruncated" doc:"revisions holds only the newest 8 of revisionsTotal"`
	Declaration         *EnvironmentResult `json:"declaration,omitempty" doc:"Declared: the current revision's entry for this environment with its values check, lifetime, expiry and owner. Absent when the current revision does not declare it"`
	Owner               EnvironmentOwner   `json:"owner" doc:"Who owns the environment: declared on it, inferred from the service owner, or unknown"`
	Provisioning        PhaseTally         `json:"provisioning" doc:"Observed provisioning statuses of the service's offering uses in this environment, by phase; Unknown until a provider reports"`
	ProvisioningTracked bool               `json:"provisioningTracked" doc:"False when no provider is asked about this environment for the current revision; the tally is then not a zero"`
	ProvisioningReason  string             `json:"provisioningReason,omitempty" doc:"Why provisioning is not tracked here"`
	TargetsComplete     bool               `json:"targetsComplete" doc:"False when the caller may not see every target of the service or a target source is unavailable; targets then counts only the visible ones"`
	TargetsReason       string             `json:"targetsReason,omitempty" doc:"Why the target count may be incomplete; never names a hidden environment"`
}

// EnvironmentOwner is who owns an environment: declared on the environment,
// inferred from the service owner when it declares none, or unknown.
type EnvironmentOwner struct {
	Owner      string     `json:"owner,omitempty"`
	Ref        *EntityRef `json:"ref,omitempty"`
	Provenance string     `json:"provenance" enum:"declared,inferred,unknown"`
	Reason     string     `json:"reason,omitempty"`
}

// PlacementPreview is a service's environments, declared and observed, one
// item per environment, listed alphabetically (natural order) with the
// unlabelled group last. It is never a promotion order.
type PlacementPreview struct {
	Total            int                                `json:"total"`
	Count            int                                `json:"count"`
	Truncated        bool                               `json:"truncated"`
	Items            []EnvironmentPlacement             `json:"items"`
	CurrentRevision  *EntityRef                         `json:"currentRevision,omitempty" doc:"The revision whose declarations these are"`
	Differences      []validation.EnvironmentDifference `json:"differences" doc:"Declared configuration values that differ between the current revision's environments"`
	DifferencesTotal int                                `json:"differencesTotal"`
	DifferencesError string                             `json:"differencesError,omitempty" doc:"Why the differences are unknown"`
}

// Reasons an environment's targets or provisioning are not fully known.
const (
	targetsIncompleteReason = "you may not see every target of this service"
	unlabelledProvisioning  = "targets with no environment label have no provisioning status"
	noEnvironmentsDeclared  = "the revision declares no environments; see the service-wide offering status"
	noProvisioningHere      = "no offering use of the current revision provisions a resource"
)

// placement lists a service's environments: every one the current revision
// cur declares, joined by name with the environment labels of targets. Callers
// pass the restricted view's targets, revision, owner and offering uses, so
// nothing hidden is counted. key is the service's key.
func (q *Query) placement(key ServiceKey, targets []*TargetRecord, cur *ContractRevision, owner *OwnershipInfo, uses []OfferingUse) PlacementPreview {
	groups := map[string]*EnvironmentPlacement{}
	group := func(env string) *EnvironmentPlacement {
		if groups[env] == nil {
			groups[env] = &EnvironmentPlacement{Environment: env}
		}
		return groups[env]
	}
	declared := map[string]EnvironmentResult{}
	if cur != nil {
		for _, e := range MarkExpired(cur.Environments, q.snap.GeneratedAt) {
			declared[e.Name] = e
			group(e.Name)
		}
	}
	revs := q.countTargets(group, targets)
	tally := provisioningByEnvironment(uses)
	complete := !q.snap.targetsHidden[key] && targetPlacementKnown(q.snap)
	items := make([]EnvironmentPlacement, 0, len(groups))
	for env, g := range groups {
		shown, total, truncated := boundSlice(chronologicalRevisionRefs(revs[env]), MaxPlacementRevisions)
		g.Revisions, g.RevisionsTotal, g.RevisionsTruncated = shown, total, truncated
		if d, ok := declared[env]; ok {
			g.Declaration = &d
		}
		g.Owner = environmentOwner(g.Declaration, owner)
		g.Provisioning, g.ProvisioningTracked = tally[env], tally[env] != PhaseTally{}
		if !g.ProvisioningTracked {
			g.ProvisioningReason = provisioningReason(env, g.Declaration != nil, len(declared) > 0)
		}
		if g.TargetsComplete = complete; !complete {
			g.TargetsReason = targetsIncompleteReason
		}
		items = append(items, *g)
	}
	slices.SortFunc(items, func(a, b EnvironmentPlacement) int {
		if a.Environment == "" || b.Environment == "" {
			return strings.Compare(b.Environment, a.Environment) // the unlabelled group sorts last
		}
		return NaturalCompare(a.Environment, b.Environment)
	})
	out, total, truncated := boundSlice(items, MaxDetailPreview)
	p := PlacementPreview{Total: total, Count: len(out), Truncated: truncated, Items: out, Differences: []validation.EnvironmentDifference{}}
	if cur != nil {
		// The SNAPSHOT record: cur is a clone without the build-only fields.
		rec := q.snap.Revisions[cur.Key]
		ref := revisionEntityRef(rec)
		p.CurrentRevision = &ref
		p.Differences = append(p.Differences, rec.differences...)
		p.DifferencesTotal, p.DifferencesError = rec.differencesTotal, rec.differencesErr
	}
	return p
}

// countTargets adds each target to the group of the environment it reports
// and returns, per environment, the revisions those targets run by an exact
// link.
func (q *Query) countTargets(group func(string) *EnvironmentPlacement, targets []*TargetRecord) map[string][]*ContractRevision {
	revs := map[string][]*ContractRevision{}
	for _, t := range targets {
		env, _ := targetEnvironmentName(t)
		g := group(env)
		g.Targets++
		if t.RevisionMatch == revisionMatchInferred {
			// ponytail: an inferred link is a mutable tag, not proof of
			// what runs, so it neither counts as declared nor names a revision.
			g.Unknown++
			continue
		}
		switch q.targetEnvironment(t).Declared {
		case "declared":
			g.Declared++
		case "not-declared":
			g.NotDeclared++
		default:
			g.Unknown++
		}
		if r := q.snap.Revisions[t.ContractRevision]; r != nil && !slices.Contains(revs[env], r) {
			revs[env] = append(revs[env], r)
		}
	}
	return revs
}

// provisioningByEnvironment tallies every provisioning status of uses by the
// environment it is about. The default slot of a revision that declares no
// environments is the empty name, which no declared group carries.
func provisioningByEnvironment(uses []OfferingUse) map[string]PhaseTally {
	out := map[string]PhaseTally{}
	for _, u := range uses {
		for _, st := range u.Provisioning {
			if st.Environment == "" {
				continue
			}
			t := out[st.Environment]
			t.add(st.Phase)
			out[st.Environment] = t
		}
	}
	return out
}

// provisioningReason is why no provider is asked about env.
func provisioningReason(env string, declared, revisionDeclares bool) string {
	switch {
	case env == "":
		return unlabelledProvisioning
	case !revisionDeclares:
		return noEnvironmentsDeclared
	case !declared:
		return "the current revision does not declare " + env + ", so no provider is asked about it there"
	default:
		return noProvisioningHere
	}
}

// environmentOwner is the owner the environment declares, else the service
// owner (inferred), else unknown with the reason.
func environmentOwner(d *EnvironmentResult, svc *OwnershipInfo) EnvironmentOwner {
	switch {
	case d == nil:
		return EnvironmentOwner{Provenance: "unknown", Reason: "not declared by the current revision"}
	case d.Owner != nil && !d.Owner.IsEmpty():
		o := EnvironmentOwner{Owner: d.Owner.DisplayString(), Provenance: "declared"}
		if o.Owner == "" {
			o.Owner = d.Owner.Contacts[0].Value // ponytail: a contacts-only owner is named by its first contact
		}
		if key := d.Owner.KeyString(); key != "" {
			ref := ownerEntityRef(key)
			o.Ref = &ref
		}
		return o
	case svc != nil && svc.Declared:
		return EnvironmentOwner{Owner: svc.Owner, Ref: clonePtr(svc.Ref), Provenance: "inferred", Reason: "the environment declares no owner; the service owner is shown"}
	case svc != nil && svc.Unknown:
		return EnvironmentOwner{Provenance: "unknown", Reason: "the environment declares no owner and the service owner is unknown"}
	default:
		return EnvironmentOwner{Provenance: "unknown", Reason: "neither the environment nor the service declares an owner"}
	}
}

// PlacementHeading is the heading the CLI and TUI print above the environment
// lines: a partial answer counts only the targets Pacto can see.
func PlacementHeading(c Completeness) string {
	if c == CompletenessPartial {
		return "Environments (targets Pacto can see)"
	}
	return "Environments"
}

// StagesHint is the line the CLI and TUI print after the environment lines:
// the fleet holds no Stages, so it names the command that lists them.
func StagesHint(service string) string {
	return "Stages: pacto delivery status --service " + service
}

// Lines is the text the CLI and TUI print under [PlacementHeading]: one line
// per environment, the environments left out and the differences between the
// declared environments.
func (p PlacementPreview) Lines() []string {
	var out []string
	for _, e := range p.Items {
		out = append(out, "  "+e.Line())
	}
	if p.Truncated {
		out = append(out, fmt.Sprintf("  %d more environments", p.Total-p.Count))
	}
	return append(out, p.differenceLines()...)
}

// differenceLines is one block per configuration path the declared
// environments disagree on, one environment per line, or why they are unknown.
func (p PlacementPreview) differenceLines() []string {
	if p.DifferencesError != "" {
		return []string{"Differences unknown: " + p.DifferencesError}
	}
	if p.DifferencesTotal == 0 {
		return nil
	}
	var envs []string
	for _, e := range p.Items {
		if e.Declaration != nil {
			envs = append(envs, e.Environment)
		}
	}
	out := []string{fmt.Sprintf("Differences (%d of %d):", len(p.Differences), p.DifferencesTotal)}
	for _, d := range p.Differences {
		out = append(out, "  "+strings.TrimSuffix(d.Configuration+"."+d.Path, ".")+":")
		names := slices.Clone(envs)
		for env := range d.Values {
			if !slices.Contains(names, env) {
				names = append(names, env)
			}
		}
		slices.SortFunc(names, NaturalCompare)
		width := 0
		for _, env := range names {
			width = max(width, len(env))
		}
		for _, env := range names {
			v := "not set"
			if val, ok := d.Values[env]; ok {
				b, _ := json.Marshal(val) // decoded from YAML or JSON, so it encodes
				v = string(b)
			}
			out = append(out, fmt.Sprintf("    %-*s  %s", width, env, v))
		}
	}
	return out
}

// Line is the one-line text the CLI and TUI print for an environment: what
// the current revision declares, the owner, the targets you can see and the
// provisioning of the service's offering uses there.
func (e EnvironmentPlacement) Line() string {
	env := cmp.Or(e.Environment, "(no environment label, unknown)")
	return env + ": " + e.declarationText() + "; " + e.ownerText() + "; " + e.targetsText() + "; " + e.provisioningText()
}

func (e EnvironmentPlacement) declarationText() string {
	d := e.Declaration
	if d == nil {
		return "not declared"
	}
	s := "declared: values " + d.Status + ", "
	if d.Lifetime == LifetimeNotDeclared {
		s += "lifetime not declared"
	} else {
		s += d.Lifetime
	}
	if d.Expires != "" {
		s += ", expires " + d.Expires
	}
	if d.Expired {
		s += ", expired (inferred)"
	}
	return s
}

func (e EnvironmentPlacement) ownerText() string {
	if e.Owner.Owner == "" {
		return "owner unknown: " + e.Owner.Reason
	}
	return "owner " + e.Owner.Owner + " (" + e.Owner.Provenance + ")"
}

func (e EnvironmentPlacement) targetsText() string {
	s := fmt.Sprintf("targets you can see: %d", e.Targets)
	if !e.TargetsComplete {
		s += " (incomplete)"
	}
	if e.Targets == 0 {
		return s
	}
	var checks []string
	for _, c := range []struct {
		n    int
		name string
	}{{e.Declared, "declared"}, {e.NotDeclared, "not declared"}, {e.Unknown, "unknown"}} {
		if c.n > 0 {
			checks = append(checks, fmt.Sprintf("%d %s", c.n, c.name))
		}
	}
	if checks != nil {
		s += " (" + strings.Join(checks, ", ") + ")"
	}
	versions := make([]string, len(e.Revisions))
	for i, r := range e.Revisions {
		versions[i] = cmp.Or(r.Version, r.Label)
	}
	vs := cmp.Or(strings.Join(versions, ", "), "no known revision")
	if e.RevisionsTruncated {
		vs += fmt.Sprintf(" and %d more", e.RevisionsTotal-len(e.Revisions))
	}
	return s + ", " + vs
}

func (e EnvironmentPlacement) provisioningText() string {
	if !e.ProvisioningTracked {
		return "provisioning not tracked: " + e.ProvisioningReason
	}
	t := e.Provisioning
	var parts []string
	total := 0
	for _, b := range []struct {
		n    int
		name string
	}{{t.Requested, PhaseBucketRequested}, {t.Provisioning, PhaseBucketProvisioning}, {t.Ready, PhaseBucketReady}, {t.Failed, PhaseBucketFailed}, {t.Deleting, PhaseBucketDeleting}, {t.Unknown, PhaseBucketUnknown}} {
		if b.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", b.n, b.name))
			total += b.n
		}
	}
	return fmt.Sprintf("provisioning %s of %d", strings.Join(parts, ", "), total)
}
