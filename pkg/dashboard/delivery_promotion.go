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

// InboxEnvironment is what waits to enter one environment, over every page
// of the inbox.
type InboxEnvironment struct {
	Environment string   `json:"environment" doc:"The environment the Stages deliver to; empty for held changes on a Stage the caller cannot read"`
	Held        int      `json:"held" doc:"Held changes on Stages in this environment"`
	Frozen      int      `json:"frozen" doc:"Frozen Stages in this environment"`
	Errored     int      `json:"errored" doc:"Errored changes on Stages in this environment that are still frozen"`
	Upstream    []string `json:"upstream" doc:"Environments whose Stages feed a Stage here, directly or through others, as candidates.upstreamStage declares"`
	Cycle       bool     `json:"cycle,omitempty" doc:"A Stage here is on a cycle of candidates.upstreamStage links, so the Stages give the environment no place in the order: it is placed by name"`
	Unordered   bool     `json:"unordered,omitempty" doc:"Different services order this environment both before and after another, with no Stage cycle: no one order fits them all, so it is placed by name"`
}

// inboxByEnvironment counts the inbox's sections by the environment of the
// Stage each item is on, in promotion order. Only environments where
// something waits are listed; the empty environment (a Stage the caller
// cannot read) comes last.
func inboxByEnvironment(stages []delivery.StageObject, held, errored []InboxItem, frozen []delivery.StageObject) []InboxEnvironment {
	byStage := delivery.StagesByName(stages)
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
	order, upstream, cycle, unordered := delivery.PromotionOrder(stages)
	out := []InboxEnvironment{}
	for _, env := range append(order, "") {
		if c := counts[env]; c != nil {
			if up := upstream[env]; up != nil {
				c.Upstream = up
			}
			c.Cycle, c.Unordered = cycle[env], unordered[env]
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

// outsideEnvironments drops the items waiting to enter one of except; an item
// on a Stage the caller cannot read waits to enter "".
func outsideEnvironments(items []InboxItem, byStage map[string]*delivery.StageObject, except []string) []InboxItem {
	return slices.DeleteFunc(items, func(it InboxItem) bool {
		env := ""
		if st := byStage[it.Namespace+"/"+it.Stage]; st != nil {
			env = st.Spec.Environment
		}
		return slices.Contains(except, env)
	})
}

// upstreamOf reads where st's candidates come from; an upstream Stage is
// read for its environment. One outside scope (the dashboard's namespace,
// empty for every namespace) is never read: it is out of scope, as the board
// and every other read say.
func upstreamOf(ctx context.Context, api DeliveryAPI, st *delivery.StageObject, scope string) *delivery.Upstream {
	c := st.Spec.Candidates
	if c.UpstreamStage == nil {
		return delivery.UpstreamListed(st, nil, nil, "")
	}
	u := &delivery.Upstream{Kind: "stage", Namespace: cmp.Or(c.UpstreamStage.Namespace, st.Metadata.Namespace), Stage: c.UpstreamStage.Name}
	switch {
	case u.Namespace == st.Metadata.Namespace && u.Stage == st.Metadata.Name:
		u.Problem = "self"
		return u
	case scope != "" && u.Namespace != scope:
		u.Problem = "outOfScope"
		return u
	}
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
}

// stageState is the one state st is counted in; held counts the held
// operations by namespace/Stage.
func stageState(st delivery.StageObject, held map[string]int) string {
	return delivery.StageState(st, held[st.Metadata.Namespace+"/"+st.Metadata.Name], false)
}

// EnvironmentCount is how the Stages of one environment stand, over every
// page of the board (B2).
type EnvironmentCount struct {
	Environment string   `json:"environment"`
	Tier        string   `json:"tier,omitempty" enum:"dev,test,prod" doc:"Inferred from the name only (named like prod); never a promotion order"`
	Stages      int      `json:"stages" doc:"Stages in this environment; the state counts sum to it"`
	Failed      int      `json:"failed" doc:"Stages whose running digest failed"`
	Frozen      int      `json:"frozen"`
	AutoHold    int      `json:"autoHold" doc:"Stages on auto-hold, and neither failed nor frozen"`
	Waiting     int      `json:"waiting" doc:"Stages with a change waiting for a person, and in no state above"`
	Unverified  int      `json:"unverified" doc:"Stages whose running digest is not verified here, and in no state above"`
	Empty       int      `json:"empty" doc:"Stages that run nothing yet"`
	Verified    int      `json:"verified" doc:"Stages whose running digest is verified here, and in no state above"`
	Upstream    []string `json:"upstream" doc:"Environments whose Stages feed a Stage here, directly or through others, as candidates.upstreamStage declares"`
	Cycle       bool     `json:"cycle,omitempty" doc:"A Stage here is on a cycle of candidates.upstreamStage links, so the environment is placed by name"`
	Unordered   bool     `json:"unordered,omitempty" doc:"Different services order this environment both before and after another, with no Stage cycle: no one order fits them all, so it is placed by name"`
}

// countByEnvironment counts stages by environment and state, in the
// promotion order the Stages declare.
func countByEnvironment(stages []delivery.StageObject, held map[string]int) []EnvironmentCount {
	counts := map[string]*EnvironmentCount{}
	for _, st := range stages {
		env := st.Spec.Environment
		c := counts[env]
		if c == nil {
			c = &EnvironmentCount{Environment: env, Tier: envTier(env), Upstream: []string{}}
			counts[env] = c
		}
		c.Stages++
		switch stageState(st, held) {
		case "failed":
			c.Failed++
		case "frozen":
			c.Frozen++
		case "autohold":
			c.AutoHold++
		case "waiting":
			c.Waiting++
		case "unverified":
			c.Unverified++
		case "empty":
			c.Empty++
		default:
			c.Verified++
		}
	}
	order, upstream, cycle, unordered := delivery.PromotionOrder(stages)
	out := []EnvironmentCount{}
	for _, env := range order {
		c := counts[env]
		if up := upstream[env]; up != nil {
			c.Upstream = up
		}
		c.Cycle, c.Unordered = cycle[env], unordered[env]
		out = append(out, *c)
	}
	return out
}

// PromotionGraph is one service's own promotion path: its Stages and the
// upstream links between them, as each Stage's spec.candidates declares
// (SPEC 21). Different services can have different chains, forks or skips.
type PromotionGraph struct {
	Nodes []PromotionNode `json:"nodes" doc:"The service's Stages, each after every Stage that feeds it; a cycle is placed by name and marked"`
	Edges []PromotionEdge `json:"edges" doc:"From a Stage to a Stage of the same service it feeds, as candidates.upstreamStage declares"`
	Last  *LastPromotion  `json:"last,omitempty" doc:"The newest promote of these Stages that succeeded, with its recorded times; absent when none did"`
}

// LastPromotion is the newest succeeded promote on a service's Stages.
type LastPromotion struct {
	To       string `json:"to" doc:"The Stage it promoted, namespace/name"`
	From     string `json:"from,omitempty" doc:"The Stage of this graph that offered the version, namespace/name; empty when the promoted Stage is an entry"`
	Digest   string `json:"digest" doc:"The digest it promoted"`
	Started  string `json:"started,omitempty" doc:"When the promote started, RFC 3339"`
	Finished string `json:"finished" doc:"When it concluded, RFC 3339"`
}

// lastPromotion is the newest succeeded promote among ops on g's Stages, by
// its recorded finish time; nil when none succeeded.
func lastPromotion(g *PromotionGraph, ops []delivery.OperationObject) *LastPromotion {
	in := map[string]bool{}
	for _, n := range g.Nodes {
		in[n.Namespace+"/"+n.Stage] = true
	}
	feeder := map[string]string{}
	for _, e := range g.Edges {
		feeder[e.To] = e.From
	}
	var last *LastPromotion
	for _, o := range ops {
		k := o.Metadata.Namespace + "/" + o.Spec.Stage
		t := o.Status.Times
		if !in[k] || o.Spec.Type != delivery.TypePromote || o.Status.Outcome != delivery.Succeeded || t.Finished == "" || (last != nil && t.Finished <= last.Finished) {
			continue
		}
		last = &LastPromotion{To: k, From: feeder[k], Digest: cmp.Or(o.Status.Pointer.After, o.Spec.Params.To), Started: t.Started, Finished: t.Finished}
	}
	return last
}

// PromotionNode is one Stage of a promotion graph.
type PromotionNode struct {
	Namespace   string            `json:"namespace"`
	Stage       string            `json:"stage"`
	Environment string            `json:"environment"`
	Level       int               `json:"level" doc:"Links from the nearest Stage nothing in this graph feeds: 0 is an entry, 1 is fed by an entry"`
	Upstream    delivery.Upstream `json:"upstream" doc:"Where the Stage's candidates come from"`
	Cycle       bool              `json:"cycle,omitempty" doc:"The Stage is on a cycle of upstream links, so the graph gives it no place"`
}

// PromotionEdge links two Stages by namespace/name.
type PromotionEdge struct {
	From string `json:"from" doc:"The upstream Stage, namespace/name"`
	To   string `json:"to" doc:"The Stage it feeds, namespace/name"`
}

// promotionGraph is the graph of one service's stages; byStage holds every
// listed Stage, so an upstream outside the service still says where it is.
func promotionGraph(stages []delivery.StageObject, byStage map[string]*delivery.StageObject, l Listing) *PromotionGraph {
	g := &PromotionGraph{Nodes: []PromotionNode{}, Edges: []PromotionEdge{}}
	key := func(st *delivery.StageObject) string { return st.Metadata.Namespace + "/" + st.Metadata.Name }
	nodes := map[string]PromotionNode{}
	feeds := map[string]map[string]bool{}
	var keys []string
	for i := range stages {
		st := &stages[i]
		k := key(st)
		n := PromotionNode{Namespace: st.Metadata.Namespace, Stage: st.Metadata.Name, Environment: st.Spec.Environment, Upstream: *delivery.UpstreamListed(st, byStage, l.read, l.scope)}
		nodes[k], keys = n, append(keys, k)
	}
	for _, k := range keys {
		if u := nodes[k].Upstream; u.Kind == "stage" && u.Problem == "" {
			if f := u.Namespace + "/" + u.Stage; f != k {
				if _, ok := nodes[f]; ok {
					feeds[k] = map[string]bool{f: true}
					g.Edges = append(g.Edges, PromotionEdge{From: f, To: k})
				}
			}
		}
	}
	cycle := map[string]bool{}
	for _, k := range keys {
		// A Stage that names itself waits on itself: a cycle of one.
		if _, on := delivery.Closure(k, feeds); on || nodes[k].Upstream.Problem == "self" {
			cycle[k] = true
		}
	}
	slices.SortFunc(keys, func(a, b string) int {
		return cmp.Or(fleet.NaturalCompare(nodes[a].Environment, nodes[b].Environment), cmp.Compare(a, b))
	})
	level := map[string]int{}
	for _, k := range delivery.Kahn(keys, feeds, cycle) {
		n := nodes[k]
		for f := range feeds[k] {
			if l, placed := level[f]; placed {
				n.Level = l + 1
			}
		}
		n.Cycle, level[k] = cycle[k], n.Level
		g.Nodes = append(g.Nodes, n)
	}
	slices.SortFunc(g.Edges, func(a, b PromotionEdge) int { return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To)) })
	return g
}
