package delivery

import (
	"cmp"
	"slices"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// ── promotion order: from the Stages' declared upstreams, never from names ──

// PromotionOrder orders the environments of stages the way their Stages
// declare changes travel: an environment comes after every environment one
// of its Stages names as candidates.upstreamStage. Environments the links do
// not order keep name order (env-2 before env-10). upstream maps each
// environment to every environment that feeds it, directly or through
// others. cycle holds the environments of Stages on a cycle of Stage links
// (a Stage that names itself included), which the Stages give no place.
// unordered holds the environments the links of different services order
// both ways (one service's eu feeds its us, another's us feeds its eu): no
// Stage cycle exists, but no one order fits every service. Both are placed by
// name, and the caller says why.
func PromotionOrder(stages []StageObject) (order []string, upstream map[string][]string, cycle, unordered map[string]bool) {
	byStage := map[string]*StageObject{}
	for i := range stages {
		byStage[stages[i].Metadata.Namespace+"/"+stages[i].Metadata.Name] = &stages[i]
	}
	feeds := map[string]map[string]bool{}      // environment -> environments it is fed by directly
	stageFeeds := map[string]map[string]bool{} // Stage -> the Stage it is fed by
	envs := map[string]bool{}
	for _, st := range stages {
		env := st.Spec.Environment
		envs[env] = true
		u := st.Spec.Candidates.UpstreamStage
		if u == nil {
			continue
		}
		ns := cmp.Or(u.Namespace, st.Metadata.Namespace)
		up := byStage[ns+"/"+u.Name]
		if up == nil {
			continue
		}
		stageFeeds[st.Metadata.Namespace+"/"+st.Metadata.Name] = map[string]bool{ns + "/" + u.Name: true}
		if up.Spec.Environment != env {
			if feeds[env] == nil {
				feeds[env] = map[string]bool{}
			}
			feeds[env][up.Spec.Environment] = true
		}
	}
	upstream, cycle, unordered = map[string][]string{}, map[string]bool{}, map[string]bool{}
	for k := range stageFeeds {
		if _, on := Closure(k, stageFeeds); on {
			cycle[byStage[k].Spec.Environment] = true
		}
	}
	envCycle := map[string]bool{}
	for env := range envs {
		up, onCycle := Closure(env, feeds)
		upstream[env] = up
		if onCycle {
			envCycle[env] = true
		}
		if onCycle && !cycle[env] {
			unordered[env] = true
		}
	}
	return Kahn(sortedNames(envs), feeds, envCycle), upstream, cycle, unordered
}

// Kahn orders left (already in name order) so each name comes after every
// name that feeds it. Each round takes the first name whose feeders are all
// placed. When none is free, every one left is on a cycle or fed by one, so
// the first on a cycle goes next: a name fed by a cycle still comes after it.
func Kahn(left []string, feeds map[string]map[string]bool, cycle map[string]bool) []string {
	order := []string{}
	placed := map[string]bool{}
	for len(left) > 0 {
		i := slices.IndexFunc(left, func(e string) bool {
			for f := range feeds[e] {
				if !placed[f] {
					return false
				}
			}
			return true
		})
		if i < 0 {
			i = slices.IndexFunc(left, func(e string) bool { return cycle[e] })
		}
		placed[left[i]] = true
		order = append(order, left[i])
		left = slices.Delete(left, i, i+1)
	}
	return order
}

// Closure is every environment that feeds env, directly or through others,
// in name order, and whether env feeds itself (it is on a cycle).
func Closure(env string, feeds map[string]map[string]bool) ([]string, bool) {
	seen := map[string]bool{}
	next := []string{env}
	for len(next) > 0 {
		e := next[0]
		next = next[1:]
		for f := range feeds[e] {
			if !seen[f] {
				seen[f] = true
				next = append(next, f)
			}
		}
	}
	onCycle := seen[env]
	delete(seen, env)
	return sortedNames(seen), onCycle
}

func sortedNames(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.SortFunc(out, func(a, b string) int { return cmp.Or(fleet.NaturalCompare(a, b), cmp.Compare(a, b)) })
	return out
}
