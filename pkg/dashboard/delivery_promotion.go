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
	return kahn(sortedNames(envs), feeds, cycle), upstream, cycle
}

// kahn orders left (already in name order) so each name comes after every
// name that feeds it. Each round takes the first name whose feeders are all
// placed. When none is free, every one left is on a cycle or fed by one, so
// the first on a cycle goes next: a name fed by a cycle still comes after it.
func kahn(left []string, feeds map[string]map[string]bool, cycle map[string]bool) []string {
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

// Upstream is where a Stage's candidates come from, as its spec declares.
type Upstream struct {
	Kind        string `json:"kind" enum:"stage,records,artifact,none" doc:"stage: another Stage in this cluster; records: a Stage in another cluster, read from its signed records; artifact: new artifacts of an OCIRepository; none: the Stage declares no source, so it never promotes"`
	Namespace   string `json:"namespace,omitempty" doc:"kind stage: the upstream Stage's namespace"`
	Stage       string `json:"stage,omitempty" doc:"kind stage: the upstream Stage's name"`
	Environment string `json:"environment,omitempty" doc:"kind stage: the upstream Stage's environment, when it could be read"`
	Problem     string `json:"problem,omitempty" enum:"missing,unreadable,outOfScope" doc:"kind stage: why the upstream Stage could not be read; outOfScope when it sits outside the namespace the dashboard shows"`
	Stream      string `json:"stream,omitempty" doc:"kind records: the upstream Stage's stream id, <clusterID>/<namespace>/<name>"`
	Repository  string `json:"repository,omitempty" doc:"kind records: the upstream recorder's streams repository; kind artifact: the OCIRepository's name"`
	Trust       string `json:"trust,omitempty" enum:"trusted,untrusted,unreadable,unknown" doc:"kind records: whether the promoter trusts that record stream, from the Stage's Ready condition. trusted: it reads and verifies the records; untrusted: it trusts no key for the records of other clusters, so the Stage never promotes; unreadable: the records cannot be read or verified; unknown: the promoter has not said yet"`
}

// recordsTrust is whether the promoter trusts st's upstream record stream, as
// its Ready condition says: the promoter sets UpstreamUntrusted when it holds
// no upstream key and UpstreamUnreadable when the records fail to verify.
func recordsTrust(st *delivery.StageObject) string {
	i := slices.IndexFunc(st.Status.Conditions, func(c delivery.Condition) bool { return c.Type == "Ready" })
	switch {
	case i < 0:
		return "unknown"
	case st.Status.Conditions[i].Reason == "UpstreamUntrusted":
		return "untrusted"
	case st.Status.Conditions[i].Reason == "UpstreamUnreadable":
		return "unreadable"
	case st.Status.Conditions[i].Status == "True":
		return "trusted"
	}
	// Not Ready for another reason (its applier): the records were not judged.
	return "unknown"
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
		return &Upstream{Kind: "records", Stream: c.UpstreamRecords.Stream, Repository: c.UpstreamRecords.Repository, Trust: recordsTrust(st)}
	case c.OCIRepositoryRef != nil:
		return &Upstream{Kind: "artifact", Repository: c.OCIRepositoryRef.Name}
	}
	return &Upstream{Kind: "none"}
}

// upstreamListed is upstreamOf from the Stages already listed, so a board of
// many rows reads no Stage twice. An upstream Stage not in the list is
// missing only when the listing read its namespace in full. Outside the
// dashboard's namespace it is out of scope; else it is unreadable: it may sit
// in a namespace the caller cannot list.
func upstreamListed(st *delivery.StageObject, byStage map[string]*delivery.StageObject, l Listing) *Upstream {
	c := st.Spec.Candidates
	if c.UpstreamStage == nil {
		return upstreamOf(context.Background(), nil, st)
	}
	u := &Upstream{Kind: "stage", Namespace: cmp.Or(c.UpstreamStage.Namespace, st.Metadata.Namespace), Stage: c.UpstreamStage.Name}
	switch up := byStage[u.Namespace+"/"+u.Stage]; {
	case up != nil:
		u.Environment = up.Spec.Environment
	case l.read(u.Namespace):
		u.Problem = "missing"
	case l.scope != "" && u.Namespace != l.scope:
		u.Problem = "outOfScope"
	default:
		u.Problem = "unreadable"
	}
	return u
}

// Stage states (B2), worst first: a Stage is counted in the first that holds,
// so the counts of an environment sum to its Stages.
var stageStates = []string{"failed", "frozen", "autohold", "waiting", "unverified", "empty", "verified"}

// stageState is the one state st is counted in; held counts the held
// operations by namespace/Stage.
func stageState(st delivery.StageObject, held map[string]int) string {
	ss := st.Status
	switch {
	case slices.Contains(ss.FailedDigests, ss.Pointer):
		return "failed"
	case ss.Frozen:
		return "frozen"
	case ss.AutoHold:
		return "autohold"
	case held[st.Metadata.Namespace+"/"+st.Metadata.Name] > 0:
		return "waiting"
	case ss.Pointer == "":
		return "empty"
	case !slices.Contains(ss.Verified, ss.Pointer):
		return "unverified"
	}
	return "verified"
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
	Cycle       bool     `json:"cycle,omitempty" doc:"The environment is on a cycle of candidates.upstreamStage links, so it is placed by name"`
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
	order, upstream, cycle := promotionOrder(stages)
	out := []EnvironmentCount{}
	for _, env := range order {
		c := counts[env]
		if up := upstream[env]; up != nil {
			c.Upstream = up
		}
		c.Cycle = cycle[env]
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
	Namespace   string   `json:"namespace"`
	Stage       string   `json:"stage"`
	Environment string   `json:"environment"`
	Level       int      `json:"level" doc:"Links from the nearest Stage nothing in this graph feeds: 0 is an entry, 1 is fed by an entry"`
	Upstream    Upstream `json:"upstream" doc:"Where the Stage's candidates come from"`
	Cycle       bool     `json:"cycle,omitempty" doc:"The Stage is on a cycle of upstream links, so the graph gives it no place"`
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
		n := PromotionNode{Namespace: st.Metadata.Namespace, Stage: st.Metadata.Name, Environment: st.Spec.Environment, Upstream: *upstreamListed(st, byStage, l)}
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
		if _, on := closure(k, feeds); on {
			cycle[k] = true
		}
	}
	slices.SortFunc(keys, func(a, b string) int {
		return cmp.Or(fleet.NaturalCompare(nodes[a].Environment, nodes[b].Environment), cmp.Compare(a, b))
	})
	level := map[string]int{}
	for _, k := range kahn(keys, feeds, cycle) {
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
