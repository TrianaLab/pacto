package fleet

import (
	"cmp"
	"maps"
	"math"
	"slices"
	"strconv"
	"sync"

	"github.com/trianalab/pacto/v4/pkg/finding"
)

// Bounds of a fleet map answer.
const (
	// MaxMapGroups bounds the groups of a map; past it the smallest fold into
	// one "Other" group.
	MaxMapGroups = 300
	// MaxMapRibbons bounds the ribbons of a map; past it the smallest are
	// dropped.
	MaxMapRibbons = 2000
	// maxFlatTeams is how many owner teams a map shows as top-level groups;
	// past it each domain is a group and its teams are sub-clusters.
	maxFlatTeams = 40
	// maxWorstCategories bounds MapAttention.Worst.
	maxWorstCategories = 3
)

// The limitations a map adds to what the snapshot says.
const (
	// LimitationEnvironmentOrderUnknown says environments are listed by name:
	// the caller read no Stage that orders them.
	LimitationEnvironmentOrderUnknown = "ENVIRONMENT_ORDER_UNKNOWN"
	// LimitationMapGroupsFolded says the smallest groups were folded into one.
	LimitationMapGroupsFolded = "MAP_GROUPS_FOLDED"
	// LimitationMapRibbonsTruncated says the smallest ribbons were dropped.
	LimitationMapRibbonsTruncated = "MAP_RIBBONS_TRUNCATED"
	// LimitationEnvironmentPlacementUnknown says Pacto cannot tell whether
	// some services run in the requested environment.
	LimitationEnvironmentPlacementUnknown = "ENVIRONMENT_PLACEMENT_UNKNOWN"
)

// The groupings of a map.
const (
	MapGroupByDomain      = "domain"
	MapGroupByTeam        = "team"
	MapGroupByEnvironment = "environment"
)

// The kinds of a map group.
const (
	MapKindDomain      = "domain"
	MapKindTeam        = "team"
	MapKindEnvironment = "environment"
	MapKindPreviews    = "previews"
	MapKindExternal    = "external"
	MapKindUnowned     = "unowned"
	MapKindNone        = "none"
	MapKindUnknown     = "unknown"
	MapKindOther       = "other"
)

// The keys of the special groups. None is a slug, so none collides with a
// domain, a canonical owner key or an environment name.
const (
	mapKeyExternal = "(external)"
	mapKeyUnowned  = "(unowned)"
	mapKeyPreviews = "(previews)"
	mapKeyOther    = "(other)"
)

// MapQuery asks for the whole fleet as groups. EnvironmentOrder ranks the
// environments (lowest first) for the environment grouping; without it they
// read by name, and OrderWhy says why when the caller knows.
type MapQuery struct {
	GroupBy          string
	EnvironmentOrder map[string]int
	OrderWhy         string
}

// FleetMap is the whole fleet at the Groups level: every group laid out once,
// the ribbons between them and the fleet's match and completeness. The layout
// depends only on the visible services and the grouping.
type FleetMap struct {
	Meta             ProductMeta        `json:"meta"`
	GroupBy          string             `json:"groupBy" enum:"domain,team,environment"`
	Nested           bool               `json:"nested,omitempty" doc:"Teams are sub-clusters of their domain: there are more than 40"`
	Groups           []MapGroup         `json:"groups"`
	Ribbons          []MapRibbon        `json:"ribbons"`
	RibbonsTruncated bool               `json:"ribbonsTruncated,omitempty" doc:"The smallest ribbons were dropped to keep at most 2000"`
	Match            MatchTally         `json:"match"`
	Knowledge        KnowledgeTally     `json:"knowledge"`
	Totals           MapTotals          `json:"totals"`
	EnvironmentOrder string             `json:"environmentOrder,omitempty" enum:"stages,name" doc:"How the environment columns are ordered: by the Stages the caller may read, or by name"`
	Bounds           MapBounds          `json:"bounds"`
	Limitations      LimitationsPreview `json:"limitations"`
}

// MapGroup is one group of the map: a domain, a team, an environment or a
// special group. X, Y and R place it in world units; Subs and node positions
// are relative to its centre.
type MapGroup struct {
	Key             string         `json:"key"`
	Label           string         `json:"label"`
	Kind            string         `json:"kind" enum:"domain,team,environment,previews,external,unowned,none,unknown,other"`
	Services        int            `json:"services" doc:"Nodes in the group: services, or targets in the environment grouping, inferred ones included"`
	Inferred        int            `json:"inferred" doc:"Of those, the inferred ones"`
	Externals       int            `json:"externals,omitempty" doc:"External systems, in the external group"`
	X               float64        `json:"x"`
	Y               float64        `json:"y"`
	R               float64        `json:"r"`
	Subs            []MapSub       `json:"subs,omitempty"`
	Match           MatchTally     `json:"match"`
	PercentComplete *int           `json:"percentComplete,omitempty" doc:"Mean contract completeness of the group's judged services; absent when none is judged"`
	Missing         []SectionCount `json:"missing,omitempty" doc:"How many of the group's judged services miss each section, in the checklist's order; a section none misses is left out"`
	Attention       MapAttention   `json:"attention"`
	Depth           *int           `json:"depth,omitempty" doc:"The environment's column, lowest first; absent for groups outside the order"`
	Preview         bool           `json:"preview,omitempty"`
}

// MapSub is one sub-cluster of a group: a team in its domain, a preview or a
// domain in its environment. X and Y are relative to the group's centre.
// Label is left out when it is the key.
type MapSub struct {
	Key      string  `json:"key"`
	Label    string  `json:"label,omitempty" doc:"Absent when it reads like the key"`
	Services int     `json:"services"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	R        float64 `json:"r"`
}

// MapRibbon is the dependencies from one group to another by match state.
// Matched, ExpectedOnly, ObservedOnly and Insufficient sum to Total; Problems
// and CrossEnvironment count edges of that total. A dependency with no match
// state (a configuration reference) draws no ribbon. A count of zero is left
// out of the answer.
type MapRibbon struct {
	From             string `json:"from"`
	To               string `json:"to"`
	Total            int    `json:"total"`
	Matched          int    `json:"matched,omitempty"`
	ExpectedOnly     int    `json:"expectedOnly,omitempty"`
	ObservedOnly     int    `json:"observedOnly,omitempty"`
	Insufficient     int    `json:"insufficient,omitempty"`
	Problems         int    `json:"problems,omitempty"`
	CrossEnvironment int    `json:"crossEnvironment,omitempty"`
}

// MapAttention is the attention items of a group by severity and its worst
// categories, worst first.
type MapAttention struct {
	Errors   int                      `json:"errors"`
	Warnings int                      `json:"warnings"`
	Info     int                      `json:"info"`
	Worst    []AttentionCategoryCount `json:"worst"`
}

// MapTotals counts what the map draws before any bound.
type MapTotals struct {
	Services  int `json:"services" doc:"Visible services that are no golden path, inferred ones included"`
	Inferred  int `json:"inferred"`
	Externals int `json:"externals"`
	Targets   int `json:"targets"`
	Groups    int `json:"groups" doc:"Groups before folding"`
	Folded    int `json:"folded,omitempty" doc:"Groups folded into Other"`
	Ribbons   int `json:"ribbons" doc:"Ribbons before the bound"`
}

// MapBounds is the box every group fits in, in world units.
type MapBounds struct {
	MinX float64 `json:"minX"`
	MinY float64 `json:"minY"`
	MaxX float64 `json:"maxX"`
	MaxY float64 `json:"maxY"`
}

// MapProblem is the worst problem of a dependency edge, read from facts the
// snapshot holds.
type MapProblem struct {
	Code     string `json:"code"`
	Severity string `json:"severity" enum:"error,warning"`
	Reason   string `json:"reason,omitempty"`
}

// mapGroupAcc is one group while the map is built.
type mapGroupAcc struct {
	g        MapGroup
	special  bool
	nodes    []layoutNode
	services map[ServiceKey]bool
	subLabel map[string]string
	match    MatchTally
	know     KnowledgeTally
	sev      map[string]string
	cats     map[string]int
}

// mapBuild holds what every grouping reads: the visible services, their
// edges and degrees, and each service's completeness, computed once.
type mapBuild struct {
	q        *Query
	mq       MapQuery
	services []*ServiceRecord
	edges    []*mapEdge
	degree   map[ServiceKey]int
	// complete memoizes completenessOf; pageMembers shares one build across
	// concurrent requests, so completeMu guards it.
	completeMu sync.Mutex
	complete   map[ServiceKey]*ServiceCompleteness
	groups     map[string]*mapGroupAcc
	order      []string
	rename     map[string]string
	// svcGroup is the group of each service (domain and team groupings);
	// targetGroup of each target (environment grouping).
	svcGroup    map[ServiceKey]string
	targetGroup map[TargetKey]string
	// specialOf is the unknown or none group a service sits in as a
	// service, and previewSubs the previews drawn (environment grouping).
	specialOf   map[ServiceKey]string
	previewSubs map[string]bool
	limits      []Limitation
	// nested, envOrder and preFold are what grouping said: teams nest in
	// domains, how environments are ordered, the totals before folding.
	nested   bool
	envOrder string
	preFold  MapTotals
}

// Map returns the whole fleet as groups laid out once, by domain (the
// default), owner team or environment. Every count reads the view: a hidden
// service is in no group, count, ribbon or tally.
func (q *Query) Map(mq MapQuery) (*FleetMap, error) {
	b, err := q.mapMembers(mq)
	if err != nil {
		return nil, err
	}
	fm := &FleetMap{GroupBy: b.mq.GroupBy, Nested: b.nested, EnvironmentOrder: b.envOrder, Totals: b.preFold}
	b.tally()
	b.attention()
	fm.Ribbons, fm.Totals.Ribbons, fm.RibbonsTruncated = b.ribbons()
	fm.Groups, fm.Bounds = b.layout()
	fm.Match, fm.Knowledge = b.fleetTallies()
	fm.Limitations = limitationsPreview(b.limits)
	fm.Meta = q.productMetaWith(b.limits)
	return fm, nil
}

// pageMembers is mapMembers for groupBy alone, built once per query: a page
// of nodes and a focus only read it, and the map asks one page per group in
// view, so rebuilding the whole fleet's groups for each page made every page
// O(fleet). Map itself tallies into its build, so it never shares one.
func (q *Query) pageMembers(groupBy string) (*mapBuild, error) {
	mq := MapQuery{GroupBy: cmp.Or(groupBy, MapGroupByDomain)}
	// Only the three groupings are kept: groupBy comes from the request.
	if q.members == nil || mq.GroupBy != MapGroupByDomain && mq.GroupBy != MapGroupByTeam && mq.GroupBy != MapGroupByEnvironment {
		return q.mapMembers(mq)
	}
	v, _ := q.members.LoadOrStore(mq.GroupBy, sync.OnceValues(func() (*mapBuild, error) { return q.mapMembers(mq) }))
	return v.(func() (*mapBuild, error))()
}

// mapMembers puts every visible service, target and external system in its
// group, folded past MaxMapGroups: what both the map and a page of nodes
// read. It packs no group.
func (q *Query) mapMembers(mq MapQuery) (*mapBuild, error) {
	mq.GroupBy = cmp.Or(mq.GroupBy, MapGroupByDomain)
	if mq.GroupBy != MapGroupByDomain && mq.GroupBy != MapGroupByTeam && mq.GroupBy != MapGroupByEnvironment {
		return nil, &InvalidQueryError{Field: "groupBy", Value: mq.GroupBy, Reason: "must be domain, team or environment"}
	}
	b := q.newMapBuild(mq)
	switch mq.GroupBy {
	case MapGroupByEnvironment:
		b.envOrder = b.environmentGroups()
	case MapGroupByTeam:
		b.nested = b.teamGroups()
	default:
		b.domainGroups()
	}
	b.externalGroup()
	b.preFold = b.totals()
	b.fold()
	return b, nil
}

// newMapBuild reads the visible services that are no golden path, their
// dependency edges and degrees.
func (q *Query) newMapBuild(mq MapQuery) *mapBuild {
	b := &mapBuild{q: q, mq: mq, degree: map[ServiceKey]int{}, complete: map[ServiceKey]*ServiceCompleteness{},
		groups: map[string]*mapGroupAcc{}, rename: map[string]string{}, svcGroup: map[ServiceKey]string{}, targetGroup: map[TargetKey]string{},
		specialOf: map[ServiceKey]string{}, previewSubs: map[string]bool{}}
	for _, s := range q.snap.Services {
		if !s.Template {
			b.services = append(b.services, s)
		}
	}
	slices.SortFunc(b.services, func(a, c *ServiceRecord) int { return cmp.Compare(a.Key, c.Key) })
	b.edges = q.dependencyEdges(func(k ServiceKey) bool { s := q.snap.Services[k]; return s != nil && !s.Template })
	for _, me := range b.edges {
		b.degree[me.from]++
		b.degree[me.to]++
	}
	return b
}

// group returns the group under key, adding it with label and kind.
func (b *mapBuild) group(key, label, kind string) *mapGroupAcc {
	g := b.groups[key]
	if g == nil {
		g = &mapGroupAcc{g: MapGroup{Key: key, Label: label, Kind: kind}, services: map[ServiceKey]bool{}, subLabel: map[string]string{},
			sev: map[string]string{}, cats: map[string]int{}}
		g.special = kind != MapKindDomain && kind != MapKindTeam && kind != MapKindEnvironment
		b.groups[key] = g
		b.order = append(b.order, key)
	}
	return g
}

// addService adds s to g as a node keyed key in sub-cluster sub.
func (b *mapBuild) addService(g *mapGroupAcc, s *ServiceRecord, key, sub string) {
	g.services[s.Key] = true
	g.nodes = append(g.nodes, layoutNode{key: key, degree: b.degree[s.Key], inferred: s.Inferred(), sub: sub})
	g.g.Inferred += b2i(s.Inferred())
}

// domainGroup is the group of s by business domain.
func (b *mapBuild) domainGroup(s *ServiceRecord) *mapGroupAcc {
	switch {
	case s.BusinessDomainUnknown:
		return b.group(UnknownBusinessDomain, "Domain unknown", MapKindUnknown)
	case s.BusinessDomain == "":
		return b.group(NoBusinessDomain, "No domain", MapKindNone)
	}
	return b.group(s.BusinessDomain, s.BusinessDomain, MapKindDomain)
}

func (b *mapBuild) domainGroups() {
	for _, s := range b.services {
		g := b.domainGroup(s)
		b.svcGroup[s.Key] = g.g.Key
		b.addService(g, s, string(s.Key), "")
	}
}

// teamOf is the canonical owner key s is consistently owned by and its
// label; unknown is set when Pacto cannot read its ownership.
func (b *mapBuild) teamOf(s *ServiceRecord) (key, label string, unknown bool) {
	bucket, key := b.q.ownershipBucket(s)
	if key != "" {
		return key, ownerLabelOf(key), false
	}
	return "", "", bucket == OwnershipUnknown
}

// teamGroups groups by owner team; past maxFlatTeams teams each domain is a
// group and its teams are sub-clusters. It reports whether it nested.
func (b *mapBuild) teamGroups() bool {
	teams := map[string]bool{}
	for _, s := range b.services {
		if k, _, _ := b.teamOf(s); k != "" {
			teams[k] = true
		}
	}
	nested := len(teams) > maxFlatTeams
	for _, s := range b.services {
		key, label, unknown := b.teamOf(s)
		var g *mapGroupAcc
		sub := ""
		switch {
		case nested:
			g, sub = b.domainGroup(s), cmp.Or(key, mapKeyUnowned)
			g.subLabel[sub] = cmp.Or(label, "No identified owner")
		case key != "":
			g = b.group(key, label, MapKindTeam)
		case unknown:
			g = b.group(UnknownBusinessDomain, "Owner unknown", MapKindUnknown)
		default:
			g = b.group(mapKeyUnowned, "No identified owner", MapKindUnowned)
		}
		b.svcGroup[s.Key] = g.g.Key
		b.addService(g, s, string(s.Key), sub)
	}
	return nested
}

// externalGroup adds the external systems as one group at the rim.
func (b *mapBuild) externalGroup() {
	if len(b.q.snap.Externals) == 0 {
		return
	}
	g := b.group(mapKeyExternal, "External systems", MapKindExternal)
	for k, e := range b.q.snap.Externals {
		g.nodes = append(g.nodes, layoutNode{key: string(k), degree: len(e.Consumers)})
	}
	g.g.Externals = len(g.nodes)
}

// totals counts what the map draws before folding.
func (b *mapBuild) totals() MapTotals {
	t := MapTotals{Services: len(b.services), Externals: len(b.q.snap.Externals), Targets: len(b.q.snap.Targets), Groups: len(b.groups)}
	for _, s := range b.services {
		t.Inferred += b2i(s.Inferred())
	}
	return t
}

// fold folds the smallest ordinary groups into one Other group when there
// are more than MaxMapGroups. Special groups are never folded.
func (b *mapBuild) fold() {
	if len(b.groups) <= MaxMapGroups {
		return
	}
	var ordinary []*mapGroupAcc
	specials := 0
	for _, k := range b.order {
		if g := b.groups[k]; g.special {
			specials++
		} else {
			ordinary = append(ordinary, g)
		}
	}
	slices.SortFunc(ordinary, func(a, c *mapGroupAcc) int {
		return cmp.Or(cmp.Compare(len(c.nodes), len(a.nodes)), cmp.Compare(a.g.Key, c.g.Key))
	})
	folded := ordinary[MaxMapGroups-specials-1:]
	other := &mapGroupAcc{g: MapGroup{Key: mapKeyOther, Label: "Other (" + strconv.Itoa(len(folded)) + ")", Kind: MapKindOther},
		special: true, services: map[ServiceKey]bool{}, subLabel: map[string]string{}, sev: map[string]string{}, cats: map[string]int{}}
	for _, g := range folded {
		b.rename[g.g.Key] = mapKeyOther
		delete(b.groups, g.g.Key)
		for k := range g.services {
			other.services[k] = true
		}
		for _, n := range g.nodes {
			n.sub = ""
			other.nodes = append(other.nodes, n)
		}
		other.g.Inferred += g.g.Inferred
	}
	b.groups[mapKeyOther] = other
	b.order = slices.DeleteFunc(append(b.order, mapKeyOther), func(k string) bool { return b.rename[k] != "" })
	b.limits = append(b.limits, Limitation{Code: LimitationMapGroupsFolded,
		Message: strconv.Itoa(len(folded)) + " of the smallest groups are folded into Other: a map shows at most " + strconv.Itoa(MaxMapGroups) + " groups"})
}

// resolve is the group a key names after folding.
func (b *mapBuild) resolve(key string) string { return cmp.Or(b.rename[key], key) }

// completenessOf is s's completeness, computed once.
func (b *mapBuild) completenessOf(s *ServiceRecord) *ServiceCompleteness {
	b.completeMu.Lock()
	defer b.completeMu.Unlock()
	sc := b.complete[s.Key]
	if sc == nil {
		sc = b.q.serviceCompleteness(s)
		b.complete[s.Key] = sc
	}
	return sc
}

// tally sets each group's match and completeness. The environment grouping
// judges each target against its revision's environments; the others judge
// services, and a group counts the dependencies its services make.
func (b *mapBuild) tally() {
	if b.mq.GroupBy == MapGroupByEnvironment {
		b.environmentTally()
	} else {
		byKey := map[ServiceKey]*ServiceRecord{}
		for _, s := range b.services {
			byKey[s.Key] = s
			b.groups[b.resolve(b.svcGroup[s.Key])].match.Services.add(b.q.serviceMatch(s))
		}
		for _, me := range b.edges {
			b.groups[b.resolve(b.svcGroup[me.from])].match.Dependencies.add(me.state)
		}
	}
	for _, g := range b.groups {
		for k := range g.services {
			if s := b.q.snap.Services[k]; !s.Inferred() {
				g.know.addCompleteness(b.completenessOf(s))
			}
		}
		g.know.finish()
		g.g.PercentComplete = g.know.ContractCompleteness.PercentComplete
		g.g.Missing = g.know.missingSections()
		if g.g.Kind != MapKindExternal {
			g.match.finish(b.q.snap.Completeness)
		}
		g.g.Match = g.match
	}
}

// fleetTallies are the match and knowledge of every visible service, the
// Overview's own figures.
func (b *mapBuild) fleetTallies() (MatchTally, KnowledgeTally) {
	var m MatchTally
	var k KnowledgeTally
	for _, s := range b.services {
		m.Services.add(b.q.serviceMatch(s))
		if !s.Inferred() {
			k.add(b.q.serviceKnowledge(s))
			k.addCompleteness(b.completenessOf(s))
		}
	}
	for _, me := range b.edges {
		m.Dependencies.add(me.state)
	}
	m.finish(b.q.snap.Completeness)
	k.finish()
	return m, k
}

// itemGroups are the groups an attention item counts in: its target's
// environment in the environment grouping, else its service's group.
func (b *mapBuild) itemGroups(it AttentionItem) []string {
	if b.mq.GroupBy != MapGroupByEnvironment {
		if g, ok := b.svcGroup[ServiceKey(it.Service)]; ok {
			return []string{b.resolve(g)}
		}
		return nil
	}
	if it.Entity.Kind == KindTarget {
		if g, ok := b.targetGroup[TargetKey(it.Entity.Key)]; ok {
			return []string{b.resolve(g)}
		}
	}
	var out []string
	for _, k := range b.order {
		if b.groups[k].services[ServiceKey(it.Service)] {
			out = append(out, k)
		}
	}
	return out
}

// attentionSeverityRank orders attention severities worst first.
var attentionSeverityRank = map[string]int{severityError: 0, severityWarning: 1, severityInfo: 2}

// attention counts the attention items of each group in one pass over
// collectAttention, and keeps each group's worst categories.
func (b *mapBuild) attention() {
	for _, it := range b.q.collectAttention() {
		for _, k := range b.itemGroups(it) {
			g := b.groups[k]
			switch it.Severity {
			case severityError:
				g.g.Attention.Errors++
			case severityWarning:
				g.g.Attention.Warnings++
			default:
				g.g.Attention.Info++
			}
			g.cats[it.Category]++
			if w, ok := g.sev[it.Category]; !ok || attentionSeverityRank[it.Severity] < attentionSeverityRank[w] {
				g.sev[it.Category] = it.Severity
			}
		}
	}
	for _, g := range b.groups {
		g.g.Attention.Worst = worstCategories(g.cats, g.sev)
	}
}

// worstCategories is the categories with an item, by their worst severity
// then the canonical order, at most maxWorstCategories.
func worstCategories(cats map[string]int, sev map[string]string) []AttentionCategoryCount {
	out := []AttentionCategoryCount{}
	for _, c := range attentionCategoryOrder {
		if n := cats[c]; n > 0 {
			out = append(out, AttentionCategoryCount{Category: c, Count: n})
		}
	}
	slices.SortStableFunc(out, func(a, c AttentionCategoryCount) int {
		return cmp.Compare(attentionSeverityRank[sev[a.Category]], attentionSeverityRank[sev[c.Category]])
	})
	return out[:min(len(out), maxWorstCategories)]
}

// problemFacts are the facts an edge problem reads, gathered once per map.
type problemFacts struct {
	failing  map[ServiceKey]bool
	breaking map[[2]ServiceKey]string
	previews map[string]bool
}

// problemFactsOf gathers the services with a failing health check, the
// consumer-provider pairs an unexpired break names and the previews.
func (q *Query) problemFactsOf() problemFacts {
	pf := problemFacts{failing: map[ServiceKey]bool{}, breaking: map[[2]ServiceKey]string{}, previews: q.previewEnvironments()}
	for _, t := range q.snap.Targets {
		if f, _ := healthFindings(t); f != nil {
			pf.failing[t.ServiceKey] = true
		}
	}
	today := q.snap.GeneratedAt.UTC().Format("2006-01-02")
	for _, s := range q.snap.Services {
		rev := q.currentRevision(s)
		if rev == nil || rev.Contract == nil {
			continue
		}
		for _, br := range rev.Contract.Breaks {
			if br.Expires != "" && br.Expires < today {
				continue
			}
			for _, name := range br.Consumers {
				if k := lookupServiceInDomain(q.snap, rev.Domain, name); k != "" {
					pf.breaking[[2]ServiceKey{k, s.Key}] = br.Reason
				}
			}
		}
	}
	return pf
}

// crossEnvironment is the worst cross-environment problem of me's visible
// target pairs: an error, a warning into a preview or when stale.
func (q *Query) crossEnvironment(me *mapEdge, previews map[string]bool) *MapProblem {
	var worst *MapProblem
	for _, p := range me.pairs {
		from, to := q.snap.Targets[p.from], q.snap.Targets[p.to]
		if from == nil || to == nil || from.Environment == to.Environment {
			continue
		}
		it := crossEnvironmentItem(from, to, previews[to.Environment], p.lastSeen, q.observedStale(p.lastSeen))
		if worst == nil || it.Severity == severityError {
			worst = &MapProblem{Code: it.Code, Severity: it.Severity, Reason: it.Reason}
		}
	}
	return worst
}

// edgeProblem is the worst problem of a dependency edge, nil for none:
// an announced break, a call across environments, a failing provider health
// check, a range the provider does not satisfy, an unmet guarantee, a
// deprecated provider, then a call no contract declares.
func (q *Query) edgeProblem(me *mapEdge, pf problemFacts) *MapProblem {
	e := me.e
	if r, ok := pf.breaking[[2]ServiceKey{me.from, me.to}]; ok {
		return &MapProblem{Code: "BREAKING", Severity: severityError, Reason: r}
	}
	if p := q.crossEnvironment(me, pf.previews); p != nil {
		return p
	}
	if pf.failing[me.to] {
		return &MapProblem{Code: string(finding.CodeHealthFailing), Severity: severityWarning, Reason: "the provider's health check is failing"}
	}
	for _, c := range e.DeclaredClaims.Items {
		if c.RangeStatus == RangeUnsatisfied {
			return &MapProblem{Code: LimitationRangeUnsatisfied, Severity: severityWarning, Reason: q.rangeReason(c.Compatibility, me.to)}
		}
	}
	switch {
	case e.GuaranteeStatus == GuaranteeUnmet:
		return &MapProblem{Code: "GUARANTEE_UNMET", Severity: severityWarning, Reason: "the provider does not meet a guarantee this dependency requires"}
	case e.ProviderLifecycle != nil || len(e.DeprecatedInterfaces) > 0:
		return &MapProblem{Code: "DEPENDENCY_SERVICE_DEPRECATED", Severity: severityWarning, Reason: "the provider or an interface it offers is deprecated"}
	case me.state == MatchObservedOnly && !e.Inferred:
		return &MapProblem{Code: string(finding.CodeDependencyUndeclared), Severity: severityWarning, Reason: "observed calls and no contract declares them"}
	}
	return nil
}

// rangeReason words a range provider does not satisfy and, when Pacto sees
// it running, the version it runs: "accepts ^2.3 · runs 3.0.1".
func (q *Query) rangeReason(compatibility string, provider ServiceKey) string {
	reason := "accepts " + compatibility
	if s := q.snap.Services[provider]; s != nil {
		if v, ok := q.runningVersion(s, ""); ok && v != "" {
			reason += " · runs " + v
		}
	}
	return reason
}

// ribbonKey is the ordered pair of groups a ribbon joins.
type ribbonKey struct{ from, to string }

// addRibbon counts one edge of match state state into the ribbon from -> to.
func addRibbon(rs map[ribbonKey]*MapRibbon, from, to, state string, problem, cross bool) {
	k := ribbonKey{from, to}
	r := rs[k]
	if r == nil {
		r = &MapRibbon{From: from, To: to}
		rs[k] = r
	}
	r.Total++
	switch state {
	case MatchMatched:
		r.Matched++
	case MatchExpectedOnly:
		r.ExpectedOnly++
	case MatchObservedOnly:
		r.ObservedOnly++
	default:
		r.Insufficient++
	}
	r.Problems += b2i(problem)
	r.CrossEnvironment += b2i(cross)
}

// ribbons is the dependencies between groups, largest first, at most
// MaxMapRibbons, with how many there were and whether any was dropped. In
// the environment grouping a ribbon is the calls from one environment into
// another.
func (b *mapBuild) ribbons() ([]MapRibbon, int, bool) {
	pf := b.q.problemFactsOf()
	rs := map[ribbonKey]*MapRibbon{}
	for _, me := range b.edges {
		if me.state == "" {
			continue
		}
		problem := b.q.edgeProblem(me, pf) != nil
		if b.mq.GroupBy == MapGroupByEnvironment {
			b.environmentRibbons(rs, me, problem)
			continue
		}
		if from, to := b.resolve(b.svcGroup[me.from]), b.resolve(b.svcGroup[me.to]); from != to {
			addRibbon(rs, from, to, me.state, problem, b.q.crossEnvironment(me, pf.previews) != nil)
		}
	}
	out := make([]MapRibbon, 0, len(rs))
	for _, r := range rs {
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, c MapRibbon) int {
		return cmp.Or(cmp.Compare(c.Total, a.Total), cmp.Compare(a.From, c.From), cmp.Compare(a.To, c.To))
	})
	total := len(out)
	if total <= MaxMapRibbons {
		return out, total, false
	}
	b.limits = append(b.limits, Limitation{Code: LimitationMapRibbonsTruncated,
		Message: strconv.Itoa(total-MaxMapRibbons) + " of the smallest ribbons are left out: a map shows at most " + strconv.Itoa(MaxMapRibbons)})
	return out[:MaxMapRibbons], total, true
}

// layout places every group once: environment columns in order, else the
// tangent packing, largest first and specials at the rim.
func (b *mapBuild) layout() ([]MapGroup, MapBounds) {
	accs := make([]*mapGroupAcc, 0, len(b.groups))
	for _, k := range b.order {
		accs = append(accs, b.groups[k])
	}
	slices.SortFunc(accs, func(a, c *mapGroupAcc) int {
		return cmp.Or(cmp.Compare(b2i(a.special), b2i(c.special)), cmp.Compare(len(c.nodes), len(a.nodes)), cmp.Compare(a.g.Key, c.g.Key))
	})
	cs := make([]*circle, len(accs))
	index := map[string]int{}
	for i, g := range accs {
		gl := layoutGroup(g.nodes)
		g.g.Services, g.g.R = len(g.nodes)-g.g.Externals, round1(gl.r)
		g.g.Subs = subsOf(g, gl)
		cs[i] = &circle{key: g.g.Key, r: gl.r, special: g.special}
		index[g.g.Key] = i
	}
	packMapGroups(b, accs, cs, index)
	out := make([]MapGroup, len(accs))
	bounds := MapBounds{MinX: math.Inf(1), MinY: math.Inf(1), MaxX: math.Inf(-1), MaxY: math.Inf(-1)}
	for i, g := range accs {
		g.g.X, g.g.Y = round1(cs[i].x), round1(cs[i].y)
		out[i] = g.g
		bounds.MinX, bounds.MinY = min(bounds.MinX, g.g.X-g.g.R), min(bounds.MinY, g.g.Y-g.g.R)
		bounds.MaxX, bounds.MaxY = max(bounds.MaxX, g.g.X+g.g.R), max(bounds.MaxY, g.g.Y+g.g.R)
	}
	if len(out) == 0 {
		bounds = MapBounds{}
	}
	return out, bounds
}

// packMapGroups places the groups of a map: environment columns, else the
// tangent packing. A variable, so a test proves a page of nodes never calls
// it.
var packMapGroups = func(b *mapBuild, accs []*mapGroupAcc, cs []*circle, index map[string]int) {
	if b.mq.GroupBy == MapGroupByEnvironment {
		depth := make([]*int, len(accs))
		for i, g := range accs {
			depth[i] = g.g.Depth
		}
		packColumns(cs, depth)
		return
	}
	packCircles(cs, mapGroupGap, b.groupWeights(index))
}

// subsOf is the sub-clusters of g as the layout placed them.
func subsOf(g *mapGroupAcc, gl groupLayout) []MapSub {
	var out []MapSub
	for i, c := range gl.subs {
		n := 0
		for _, p := range gl.nodes {
			n += b2i(p.sub == i)
		}
		label := g.subLabel[c.key]
		if label == c.key {
			label = ""
		}
		out = append(out, MapSub{Key: c.key, Label: label, Services: n, X: round1(c.x), Y: round1(c.y), R: round1(c.r)})
	}
	return out
}

// groupWeights is how many edges join each pair of groups, read by the
// packer to place linked groups close; index maps a group key to its
// placement position.
func (b *mapBuild) groupWeights(index map[string]int) func(i, j int) int {
	w := map[[2]int]int{}
	for _, me := range b.edges {
		i, j := index[b.resolve(b.svcGroup[me.from])], index[b.resolve(b.svcGroup[me.to])]
		if i != j {
			w[[2]int{min(i, j), max(i, j)}]++
		}
	}
	return func(i, j int) int { return w[[2]int{min(i, j), max(i, j)}] }
}

// environmentGroups groups targets by environment, in the caller's order or
// by name, with the previews collapsed into one group. A target that names
// no environment and an inferred service sit in the unknown group; a
// declared service with no visible target in the none group. It reports how
// the environments are ordered.
func (b *mapBuild) environmentGroups() string {
	q := b.q
	previews := q.previewEnvironments()
	envs := map[string]bool{}
	for _, t := range q.snap.Targets {
		if t.Environment != "" && !previews[t.Environment] {
			envs[t.Environment] = true
		}
	}
	for _, s := range b.services {
		for _, e := range b.declaredEnvironments(s) {
			envs[e] = envs[e] || !previews[e]
		}
	}
	how := b.orderEnvironments(envs)
	for _, k := range slices.Sorted(maps.Keys(q.snap.Targets)) {
		b.addTarget(q.snap.Targets[k], previews)
	}
	if g := b.groups[mapKeyPreviews]; g != nil {
		for _, n := range g.nodes {
			b.previewSubs[n.sub] = true
		}
		g.g.Label = "Previews (" + strconv.Itoa(len(b.previewSubs)) + ")"
	}
	for _, s := range b.services {
		var g *mapGroupAcc
		switch {
		case s.Inferred():
			g = b.group(UnknownBusinessDomain, "Environment unknown", MapKindUnknown)
		case len(s.Targets) == 0:
			g = b.group(NoBusinessDomain, "No visible target", MapKindNone)
		default:
			continue
		}
		b.specialOf[s.Key] = g.g.Key
		b.addService(g, s, string(s.Key), "")
	}
	return how
}

// addTarget adds target t as a node of its environment group, clustered by
// its service's domain; a golden path's target draws none.
func (b *mapBuild) addTarget(t *TargetRecord, previews map[string]bool) {
	s := b.q.snap.Services[t.ServiceKey]
	if s == nil || s.Template {
		return
	}
	g, sub := b.environmentGroup(t, previews)
	if sub == "" && g.g.Kind == MapKindEnvironment {
		sub = cmp.Or(s.BusinessDomain, NoBusinessDomain)
		g.subLabel[sub] = cmp.Or(s.BusinessDomain, "No domain")
	}
	b.targetGroup[t.Key] = g.g.Key
	b.addService(g, s, string(t.Key), sub)
}

// environmentGroup is the group of target t and its sub-cluster in it.
func (b *mapBuild) environmentGroup(t *TargetRecord, previews map[string]bool) (*mapGroupAcc, string) {
	switch {
	case t.Environment == "":
		return b.group(UnknownBusinessDomain, "Environment unknown", MapKindUnknown), ""
	case previews[t.Environment]:
		return b.previewsGroup(), t.Environment
	}
	return b.groups[t.Environment], ""
}

// previewsGroup is the one group every preview collapses into.
func (b *mapBuild) previewsGroup() *mapGroupAcc {
	g := b.group(mapKeyPreviews, "Previews", MapKindPreviews)
	if g.g.Depth == nil {
		g.special, g.g.Preview, g.g.Depth = false, true, new(-1)
	}
	return g
}

// declaredEnvironments is the environments s's current revision declares.
func (b *mapBuild) declaredEnvironments(s *ServiceRecord) []string {
	rev := b.q.currentRevision(s)
	if s.Inferred() || rev == nil || rev.Contract == nil {
		return nil
	}
	var out []string
	for _, e := range rev.Contract.Environments {
		out = append(out, e.Name)
	}
	return out
}

// orderEnvironments adds one group per persistent environment (envs keeps
// false for a preview), in the caller's order, then by name; without an
// order every environment reads by name and the map says so.
func (b *mapBuild) orderEnvironments(envs map[string]bool) string {
	rank, how := b.mq.EnvironmentOrder, "stages"
	if len(rank) == 0 {
		how = "name"
		b.limits = append(b.limits, Limitation{Code: LimitationEnvironmentOrderUnknown,
			Message: cmp.Or(b.mq.OrderWhy, "No Stage orders the environments, so they read by name")})
	}
	var names []string
	for e, persistent := range envs {
		if persistent {
			names = append(names, e)
		}
	}
	slices.SortFunc(names, func(a, c string) int {
		ra, oka := rank[a]
		rc, okc := rank[c]
		return cmp.Or(cmp.Compare(b2i(!oka), b2i(!okc)), cmp.Compare(ra, rc), cmp.Compare(a, c))
	})
	for i, e := range names {
		b.group(e, e, MapKindEnvironment).g.Depth = new(i)
	}
	return how
}

// targetMatch is the match state of target t in its environment: matched
// when its revision declares the environment or declares none, observed-only
// when it declares others. Whatever Pacto cannot tell is insufficient, never
// a confident state: a stale or quarantined target, a revision linked only by
// a mutable tag, an unknown or unreadable revision and an unknown or
// conflicting environment label.
func (q *Query) targetMatch(t *TargetRecord) string {
	if t.Stale || t.Quarantined || t.RevisionMatch == revisionMatchInferred {
		return MatchInsufficient
	}
	switch q.targetEnvironment(t).Declared {
	case "declared":
		return MatchMatched
	case "not-declared":
		rev := q.snap.Revisions[t.ContractRevision]
		switch {
		case rev.Contract == nil:
			return MatchInsufficient
		case len(rev.Contract.Environments) == 0:
			return MatchMatched
		}
		return MatchObservedOnly
	}
	return MatchInsufficient
}

// environmentTally judges every target in its environment, every declared
// environment a service has no visible target in (expected-only only when
// Pacto can tell where the service runs), the services of the special
// groups, and each dependency in every environment both ends run in.
func (b *mapBuild) environmentTally() {
	q := b.q
	in := map[ServiceKey]map[string]bool{}
	for k, gk := range b.targetGroup {
		t := q.snap.Targets[k]
		b.groups[b.resolve(gk)].match.Services.add(q.targetMatch(t))
		if in[t.ServiceKey] == nil {
			in[t.ServiceKey] = map[string]bool{}
		}
		in[t.ServiceKey][b.resolve(gk)] = true
	}
	for _, s := range b.services {
		for _, e := range b.declaredEnvironments(s) {
			g := b.environmentGroupOf(e)
			if g == nil || in[s.Key][g.g.Key] {
				continue
			}
			state := MatchInsufficient
			if q.placementKnown(s) {
				state = MatchExpectedOnly
			}
			g.match.Services.add(state)
		}
		if g := b.specialGroupOf(s); g != nil {
			g.match.Services.add(q.serviceMatch(s))
		}
	}
	for _, me := range b.edges {
		for gk := range in[me.from] {
			if in[me.to][gk] {
				b.groups[gk].match.Dependencies.add(me.state)
			}
		}
	}
}

// environmentGroupOf is the group environment e is drawn in, nil for none.
func (b *mapBuild) environmentGroupOf(e string) *mapGroupAcc {
	if g := b.groups[b.resolve(e)]; g != nil && !g.special {
		return g
	}
	if b.previewSubs[e] {
		return b.groups[mapKeyPreviews]
	}
	return nil
}

// specialGroupOf is the unknown or none group s sits in as a service, nil
// when it sits in environments.
func (b *mapBuild) specialGroupOf(s *ServiceRecord) *mapGroupAcc {
	if k, ok := b.specialOf[s.Key]; ok {
		return b.groups[k]
	}
	return nil
}

// environmentRibbons counts the calls of me from one environment group into
// another, one per visible target pair.
func (b *mapBuild) environmentRibbons(rs map[ribbonKey]*MapRibbon, me *mapEdge, problem bool) {
	for _, p := range me.pairs {
		fk, okf := b.targetGroup[p.from]
		tk, okt := b.targetGroup[p.to]
		if !okf || !okt {
			continue
		}
		if from, to := b.resolve(fk), b.resolve(tk); from != to {
			addRibbon(rs, from, to, me.state, problem, true)
		}
	}
}
