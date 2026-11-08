package dashboard

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// ── promotion order: from the Stages' declared upstreams, never from names ──

// promotionOrder orders the environments of stages the way their Stages
// declare changes travel: an environment comes after every environment one
// of its Stages names as candidates.upstreamStage. Environments the links do
// not order keep name order (env-2 before env-10). upstream maps each
// environment to every environment that feeds it, directly or through
// others; cycle holds the environments on a cycle of links, which the Stages
// give no place: they are placed by name, and the caller says so.
func promotionOrder(stages []delivery.StageObject) (order []string, upstream map[string][]string, cycle map[string]bool) {
	byStage := stagesByName(stages)
	feeds := map[string]map[string]bool{} // environment -> environments it is fed by directly
	envs := map[string]bool{}
	for _, st := range stages {
		env := st.Spec.Environment
		envs[env] = true
		u := st.Spec.Candidates.UpstreamStage
		if u == nil {
			continue
		}
		ns := cmp.Or(u.Namespace, st.Metadata.Namespace)
		if up := byStage[ns+"/"+u.Name]; up != nil && up.Spec.Environment != env {
			if feeds[env] == nil {
				feeds[env] = map[string]bool{}
			}
			feeds[env][up.Spec.Environment] = true
		}
	}
	upstream, cycle = map[string][]string{}, map[string]bool{}
	for env := range envs {
		up, onCycle := closure(env, feeds)
		upstream[env] = up
		if onCycle {
			cycle[env] = true
		}
	}
	// Kahn's order: each round takes the first environment by name whose
	// feeders are all placed. When none is free, every one left is on a
	// cycle or fed by one, so the first by name on a cycle goes next: an
	// environment fed by a cycle still comes after it.
	left := sortedNames(envs)
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
	return order, upstream, cycle
}

// closure is every environment that feeds env, directly or through others,
// in name order, and whether env feeds itself (it is on a cycle).
func closure(env string, feeds map[string]map[string]bool) ([]string, bool) {
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

// InboxEnvironment is what waits to enter one environment, over every page
// of the inbox.
type InboxEnvironment struct {
	Environment string   `json:"environment" doc:"The environment the Stages deliver to; empty for held changes on a Stage the caller cannot read"`
	Held        int      `json:"held" doc:"Held changes on Stages in this environment"`
	Frozen      int      `json:"frozen" doc:"Frozen Stages in this environment"`
	Errored     int      `json:"errored" doc:"Errored changes on Stages in this environment that are still frozen"`
	Upstream    []string `json:"upstream" doc:"Environments whose Stages feed a Stage here, directly or through others, as candidates.upstreamStage declares"`
	Cycle       bool     `json:"cycle,omitempty" doc:"The environment is on a cycle of candidates.upstreamStage links, so the Stages give it no place in the order: it is placed by name"`
}

// inboxByEnvironment counts the inbox's sections by the environment of the
// Stage each item is on, in promotion order. Only environments where
// something waits are listed; the empty environment (a Stage the caller
// cannot read) comes last.
func inboxByEnvironment(stages []delivery.StageObject, held, errored []InboxItem, frozen []delivery.StageObject) []InboxEnvironment {
	byStage := stagesByName(stages)
	envOf := func(it InboxItem) string {
		if st := byStage[it.Namespace+"/"+it.Stage]; st != nil {
			return st.Spec.Environment
		}
		return ""
	}
	counts := map[string]*InboxEnvironment{}
	at := func(env string) *InboxEnvironment {
		if counts[env] == nil {
			counts[env] = &InboxEnvironment{Environment: env, Upstream: []string{}}
		}
		return counts[env]
	}
	for _, it := range held {
		at(envOf(it)).Held++
	}
	for _, it := range errored {
		at(envOf(it)).Errored++
	}
	for _, st := range frozen {
		at(st.Spec.Environment).Frozen++
	}
	order, upstream, cycle := promotionOrder(stages)
	out := []InboxEnvironment{}
	for _, env := range append(order, "") {
		if c := counts[env]; c != nil {
			if up := upstream[env]; up != nil {
				c.Upstream = up
			}
			c.Cycle = cycle[env]
			out = append(out, *c)
			delete(counts, env)
		}
	}
	return out
}

// inEnvironment keeps the items on Stages in env.
func inEnvironment(items []InboxItem, byStage map[string]*delivery.StageObject, env string) []InboxItem {
	return slices.DeleteFunc(items, func(it InboxItem) bool {
		st := byStage[it.Namespace+"/"+it.Stage]
		return st == nil || st.Spec.Environment != env
	})
}

// Upstream is where a Stage's candidates come from, as its spec declares.
type Upstream struct {
	Kind        string `json:"kind" enum:"stage,records,artifact,none" doc:"stage: another Stage in this cluster; records: a Stage in another cluster, read from its signed records; artifact: new artifacts of an OCIRepository; none: the Stage declares no source, so it never promotes"`
	Namespace   string `json:"namespace,omitempty" doc:"kind stage: the upstream Stage's namespace"`
	Stage       string `json:"stage,omitempty" doc:"kind stage: the upstream Stage's name"`
	Environment string `json:"environment,omitempty" doc:"kind stage: the upstream Stage's environment, when it could be read"`
	Problem     string `json:"problem,omitempty" enum:"missing,unreadable" doc:"kind stage: why the upstream Stage could not be read"`
	Stream      string `json:"stream,omitempty" doc:"kind records: the upstream Stage's stream id, <clusterID>/<namespace>/<name>"`
	Repository  string `json:"repository,omitempty" doc:"kind records: the upstream recorder's streams repository; kind artifact: the OCIRepository's name"`
}

// upstreamOf reads where st's candidates come from; an upstream Stage is
// read for its environment.
func upstreamOf(ctx context.Context, api DeliveryAPI, st *delivery.StageObject) *Upstream {
	c := st.Spec.Candidates
	switch {
	case c.UpstreamStage != nil:
		u := &Upstream{Kind: "stage", Namespace: cmp.Or(c.UpstreamStage.Namespace, st.Metadata.Namespace), Stage: c.UpstreamStage.Name}
		up, err := api.Stage(ctx, u.Namespace, u.Stage)
		var de *DeliveryError
		switch {
		case err == nil:
			u.Environment = up.Spec.Environment
		case errors.As(err, &de) && de.Status == http.StatusNotFound:
			u.Problem = "missing"
		default:
			u.Problem = "unreadable"
		}
		return u
	case c.UpstreamRecords != nil:
		return &Upstream{Kind: "records", Stream: c.UpstreamRecords.Stream, Repository: c.UpstreamRecords.Repository}
	case c.OCIRepositoryRef != nil:
		return &Upstream{Kind: "artifact", Repository: c.OCIRepositoryRef.Name}
	}
	return &Upstream{Kind: "none"}
}
