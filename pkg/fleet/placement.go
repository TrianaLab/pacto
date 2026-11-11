package fleet

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// MaxPlacementRevisions caps the revisions listed per environment group.
const MaxPlacementRevisions = 8

// EnvironmentPlacement is one environment a service runs in, as its targets
// report it, with how many of those targets run a revision that declares it.
type EnvironmentPlacement struct {
	Environment        string      `json:"environment,omitempty" doc:"The environment label; empty for targets that report none or whose sources disagree"`
	Targets            int         `json:"targets"`
	Declared           int         `json:"declared" doc:"Targets whose linked revision declares this environment"`
	NotDeclared        int         `json:"notDeclared" doc:"Targets whose linked revision does not declare it"`
	Unknown            int         `json:"unknown" doc:"Targets Pacto cannot check: no label, conflicting labels or no linked revision"`
	Revisions          []EntityRef `json:"revisions" doc:"The distinct revisions these targets run, newest first, at most 8"`
	RevisionsTotal     int         `json:"revisionsTotal" doc:"How many distinct revisions these targets run"`
	RevisionsTruncated bool        `json:"revisionsTruncated" doc:"revisions holds only the newest 8 of revisionsTotal"`
}

// PlacementPreview is where a service runs, one item per environment label,
// listed alphabetically (natural order) with the unlabelled group last. It is
// observed from targets and is never a promotion order.
type PlacementPreview struct {
	Total     int                    `json:"total"`
	Count     int                    `json:"count"`
	Truncated bool                   `json:"truncated"`
	Items     []EnvironmentPlacement `json:"items"`
}

// placement groups targets by the environment they report. Callers pass the
// restricted view's targets, so a hidden target is never counted.
func (q *Query) placement(targets []*TargetRecord) PlacementPreview {
	groups := map[string]*EnvironmentPlacement{}
	revs := map[string][]*ContractRevision{}
	for _, t := range targets {
		env, _ := targetEnvironmentName(t)
		g := groups[env]
		if g == nil {
			g = &EnvironmentPlacement{Environment: env}
			groups[env] = g
		}
		g.Targets++
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
	items := make([]EnvironmentPlacement, 0, len(groups))
	for env, g := range groups {
		shown, total, truncated := boundSlice(chronologicalRevisionRefs(revs[env]), MaxPlacementRevisions)
		g.Revisions, g.RevisionsTotal, g.RevisionsTruncated = shown, total, truncated
		items = append(items, *g)
	}
	slices.SortFunc(items, func(a, b EnvironmentPlacement) int {
		if a.Environment == "" || b.Environment == "" {
			return strings.Compare(b.Environment, a.Environment) // the unlabelled group sorts last
		}
		return NaturalCompare(a.Environment, b.Environment)
	})
	out, total, truncated := boundSlice(items, MaxDetailPreview)
	return PlacementPreview{Total: total, Count: len(out), Truncated: truncated, Items: out}
}

// PlacementHeading is the heading the CLI and TUI print above the placement
// lines: a partial answer lists only the targets Pacto can see.
func PlacementHeading(c Completeness) string {
	if c == CompletenessPartial {
		return "Runs in (observed, targets Pacto can see)"
	}
	return "Runs in (observed)"
}

// Line is the one-line text the CLI and TUI print for an environment group:
// `<env>: <n> target(s), <versions>[ and N more]; <d> declared, <n> not
// declared, <u> unknown`.
func (e EnvironmentPlacement) Line() string {
	env := e.Environment
	if env == "" {
		env = "(no environment label)"
	}
	targets := "targets"
	if e.Targets == 1 {
		targets = "target"
	}
	versions := make([]string, len(e.Revisions))
	for i, r := range e.Revisions {
		versions[i] = cmp.Or(r.Version, r.Label)
	}
	vs := cmp.Or(strings.Join(versions, ", "), "no known revision")
	if e.RevisionsTruncated {
		vs += fmt.Sprintf(" and %d more", e.RevisionsTotal-len(e.Revisions))
	}
	return fmt.Sprintf("%s: %d %s, %s; %d declared, %d not declared, %d unknown", env, e.Targets, targets, vs, e.Declared, e.NotDeclared, e.Unknown)
}
