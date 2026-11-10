package impact

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// Event kinds, consumer effects and bounds of an event answer.
const (
	EventUnavailable = "unavailable"
	EventRemoved     = "removed"
	EventRegressed   = "regressed"

	EffectBreaks     = "breaks"
	EffectMayDegrade = "may-degrade"
	EffectUnknown    = "unknown"

	MaxConsumerResources  = 20
	MaxRegressionEvidence = 50

	// LimitationDeliveryEvidenceUnavailable: regression evidence could not be read.
	LimitationDeliveryEvidenceUnavailable = "DELIVERY_EVIDENCE_UNAVAILABLE"
	// LimitationDeliveryEvidenceTruncated: more regression evidence was read
	// than the answer carries (MaxRegressionEvidence).
	LimitationDeliveryEvidenceTruncated = "DELIVERY_EVIDENCE_TRUNCATED"
)

// Regression evidence kinds (see [RegressionEvidence.Kind]).
const (
	RegressionFailed   = "failed"
	RegressionCheck    = "check"
	RegressionRollback = "rollback"
)

// EvidenceLifecycle is the evidence kind of a provider's declared lifecycle.
const EvidenceLifecycle = "lifecycle"

// Event is a non-contract what-if: Service (a fleet service key) becomes
// unavailable, is removed, or Revision of it regressed.
type Event struct {
	Kind     string `json:"kind" enum:"unavailable,removed,regressed" doc:"What happens to the service"`
	Service  string `json:"service" doc:"The fleet service key of the service"`
	Revision string `json:"revision,omitempty" doc:"The revision that regressed: a version or an artifact digest"`
}

// RegressionEvidence is one delivery fact about the regressed revision, read
// with the caller's identity.
type RegressionEvidence struct {
	Namespace string `json:"namespace,omitempty"`
	Stage     string `json:"stage,omitempty"`
	Digest    string `json:"digest,omitempty"`
	Kind      string `json:"kind" enum:"failed,check,rollback" doc:"failed: the Stage lists the digest as failed; check: a re-check gate failed it; rollback: a rollback moved the Stage off it"`
	Gate      string `json:"gate,omitempty"`
	Result    string `json:"result,omitempty"`
	Message   string `json:"message,omitempty"`
	At        string `json:"at,omitempty"`
	Operation string `json:"operation,omitempty"`
	// Record is the signed record a rollback line came from; empty when it
	// was read from the Operation's status (shown "from status, not signed").
	Record string `json:"record,omitempty" doc:"The signed record a rollback line came from; empty when it was read from the Operation's status"`
}

// Regression is what the caller could read about a regressed revision.
type Regression struct {
	Evidence []RegressionEvidence
	// Unavailable says why delivery evidence could not be read ("" when it was).
	Unavailable string
}

// effectRank orders effects for a consumer: the worst path speaks.
func effectRank(e string) int {
	switch e {
	case EffectBreaks:
		return 3
	case EffectUnknown:
		return 2
	case EffectMayDegrade:
		return 1
	}
	return 0
}

// pathGrade is the grade of a path made of hop h followed by a path graded g:
// any unknown hop makes it unknown, any optional one may-degrade.
func pathGrade(h, g string) string {
	switch {
	case h == EffectUnknown || g == EffectUnknown:
		return EffectUnknown
	case h == EffectMayDegrade || g == EffectMayDegrade:
		return EffectMayDegrade
	}
	return EffectBreaks
}

// effectPath is a consumer's effect and the path, consumer first, that
// justifies it.
type effectPath struct {
	effect string
	path   []fleet.ServiceKey
}

// eventEffects grades every walked consumer of the graph's root by the worst
// of every declared path to it, never by the first shortest path alone. A hop
// with a declared dependency required on any revision is required; one with
// only optional dependencies is optional; one reached only by a configuration
// reference is unknown. It relaxes in BFS order until nothing changes; a path
// never passes through the consumer it grades.
//
// ponytail: fixpoint over every edge per round, O(rounds * E) with at most
// three grades per node; fine under the graph node cap.
func eventEffects(graph *fleet.GraphResult, snap *fleet.FleetSnapshot) map[fleet.ServiceKey]effectPath {
	order := make([]fleet.ServiceKey, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		order = append(order, n.Key)
	}
	paths := relaxPaths(order, graph.Root, hopGrades(snap, append([]fleet.ServiceKey{graph.Root}, order...)))
	out := map[fleet.ServiceKey]effectPath{}
	for _, n := range order {
		for _, g := range effectGrades {
			if p, ok := paths[n][g]; ok {
				out[n] = effectPath{effect: g, path: p}
				break
			}
		}
	}
	return out
}

// effectGrades are the effects from the one that speaks first.
var effectGrades = []string{EffectBreaks, EffectUnknown, EffectMayDegrade}

// hopGrades grades every declared consuming hop between the walked services:
// required (breaks), optional (may-degrade) or configuration only (unknown).
func hopGrades(snap *fleet.FleetSnapshot, walked []fleet.ServiceKey) map[fleet.ServiceKey]map[fleet.ServiceKey]string {
	in := map[fleet.ServiceKey]bool{}
	for _, k := range walked {
		in[k] = true
	}
	hops := map[fleet.ServiceKey]map[fleet.ServiceKey]string{}
	for _, r := range snap.Relationships {
		if !fleet.ConsumesEdge(r) || r.Hidden || !in[r.FromService] || !in[r.ToService] || r.FromService == r.ToService {
			continue
		}
		if hops[r.FromService] == nil {
			hops[r.FromService] = map[fleet.ServiceKey]string{}
		}
		hops[r.FromService][r.ToService] = hopGrade(hops[r.FromService][r.ToService], r)
	}
	return hops
}

// hopGrade folds one relationship into a hop's grade so far.
func hopGrade(g string, r fleet.Relationship) string {
	switch {
	case r.Type == fleet.RelationshipDependency && r.Required:
		return EffectBreaks
	case r.Type == fleet.RelationshipDependency && g != EffectBreaks:
		return EffectMayDegrade
	case g == "":
		return EffectUnknown
	}
	return g
}

// relaxPaths finds, per service in order, the shortest path to root of each
// grade, relaxing until nothing changes.
func relaxPaths(order []fleet.ServiceKey, root fleet.ServiceKey, hops map[fleet.ServiceKey]map[fleet.ServiceKey]string) map[fleet.ServiceKey]map[string][]fleet.ServiceKey {
	paths := map[fleet.ServiceKey]map[string][]fleet.ServiceKey{root: {EffectBreaks: {root}}}
	for changed := true; changed; {
		changed = false
		for _, n := range order {
			tos := make([]fleet.ServiceKey, 0, len(hops[n]))
			for to := range hops[n] {
				tos = append(tos, to)
			}
			slices.Sort(tos)
			for _, to := range tos {
				changed = extendPaths(paths, n, to, hops[n][to]) || changed
			}
		}
	}
	return paths
}

// extendPaths extends every path from to with the hop n->to of grade h,
// keeping the shortest path of each grade for n, and reports a change.
func extendPaths(paths map[fleet.ServiceKey]map[string][]fleet.ServiceKey, n, to fleet.ServiceKey, h string) bool {
	changed := false
	for _, g := range effectGrades {
		p, ok := paths[to][g]
		if !ok || slices.Contains(p, n) {
			continue
		}
		ng := pathGrade(h, g)
		if paths[n] == nil {
			paths[n] = map[string][]fleet.ServiceKey{}
		}
		// The shortest path of a grade justifies it best.
		if old, seen := paths[n][ng]; !seen || len(old) > len(p)+1 {
			paths[n][ng] = append([]fleet.ServiceKey{n}, p...)
			changed = true
		}
	}
	return changed
}

// analyzeEvent answers what happens to the consumers of opts.Event.Service.
func analyzeEvent(snap *fleet.FleetSnapshot, opts Options) *Result {
	ev := *opts.Event
	subject := fleet.ServiceKey(ev.Service)
	_, name := fleet.ParseServiceKey(subject)
	q := fleet.NewQuery(snap)
	meta := q.ProductMeta()
	res := &Result{
		SchemaVersion: SchemaVersion, SnapshotID: snap.SnapshotID, AsOf: snap.GeneratedAt, Service: name, Event: &ev,
		Completeness: snap.Completeness, Limitations: append([]fleet.Limitation(nil), snap.Limitations...),
		Consumers: []AffectedConsumer{}, Sources: meta.Sources, SourcesTruncated: meta.SourcesTruncated,
	}
	if ev.Kind == EventRegressed {
		res.noteRegression(opts.Regression)
	}
	graph := res.dependents(snap, q, subject, ev.Kind == EventRemoved)
	if graph == nil {
		return res
	}
	// An availability event is a runtime question: observed callers count
	// whether or not observed evidence was asked for.
	observed, obsLims := resolveObservedEdges(snap, opts.ObservedEdges, true)
	res.Limitations = append(res.Limitations, obsLims...)
	if len(obsLims) > 0 {
		res.Completeness = fleet.CompletenessPartial
	}
	observed = append(observed, snapshotObservedEdges(snap)...)
	res.observationSources = observationSources(snap, opts.ObservedEdges != nil)
	effects := eventEffects(graph, snap)
	var uses []fleet.OfferingUse
	if ev.Kind == EventRemoved {
		var lims []fleet.Limitation
		uses, lims = q.UsesOf(subject)
		res.Limitations = append(res.Limitations, lims...)
	}
	for _, cn := range unionConsumers(graph, subject, observed) {
		res.Consumers = append(res.Consumers, eventConsumer(snap, subject, cn, observed, effects, uses))
	}
	if ev.Kind == EventRemoved {
		res.addEventReferrers(snap, subject, observed, uses)
	}
	for _, c := range uncontractedCallers(snap, subject) {
		c.Effect, c.Note = EffectUnknown, "a caller no contract describes; what it needs from "+name+" is unknown"
		res.Consumers = append(res.Consumers, c)
	}
	res.collectOwnersAndTargets(snap, subject)
	res.noteUnlisted(snap, q, subject)
	res.noteStaleConsumers()
	return res
}

// noteRegression copies the delivery evidence the caller read, or says why
// none was read.
func (r *Result) noteRegression(reg *Regression) {
	why := "no delivery evidence was read for this answer"
	if reg != nil {
		r.Regression = slices.Clone(reg.Evidence[:min(len(reg.Evidence), MaxRegressionEvidence)])
		why = reg.Unavailable
		if n := len(reg.Evidence) - MaxRegressionEvidence; n > 0 {
			r.Limitations = append(r.Limitations, fleet.Limitation{Code: LimitationDeliveryEvidenceTruncated, Source: limitationSource,
				Message: strconv.Itoa(n) + " more delivery " + plural(n, "fact", "facts") + " about this revision " + plural(n, "is", "are") +
					" not shown: the answer keeps the first " + strconv.Itoa(MaxRegressionEvidence)})
		}
	}
	if why != "" {
		r.Limitations = append(r.Limitations, fleet.Limitation{Code: LimitationDeliveryEvidenceUnavailable, Source: limitationSource, Message: why})
	}
}

// observationSources names the sources the snapshot's observed relationships
// came from, and the traces file when one was read.
func observationSources(snap *fleet.FleetSnapshot, traces bool) []string {
	seen := map[string]bool{}
	for _, r := range snap.Relationships {
		if r.Provenance != fleet.ProvenanceObserved {
			continue
		}
		if r.Source != "" {
			seen[r.Source] = true
		}
		for _, s := range r.ObservedSources {
			seen[s.Source] = true
		}
	}
	out := sortedKeys(seen)
	if traces {
		out = append(out, tracesFileSource)
	}
	return out
}

// eventConsumer is the record for one consumer of an event's subject.
func eventConsumer(snap *fleet.FleetSnapshot, subject fleet.ServiceKey, node consumerNode, observed []resolvedObservedEdge,
	effects map[fleet.ServiceKey]effectPath, uses []fleet.OfferingUse) AffectedConsumer {
	c, stale := consumerBase(snap, node.key, subject, observed)
	c.Service, c.Depth, c.Direct = node.name, node.depth, node.depth == 1
	rel, hasDeclared, observedFresh, seen, refs := edgeEvidence(snap, node.key, subject, "", observed)
	c.Compatibility, c.CompatibilityVerdict = rel.Compatibility, CompatibilityUnknown
	if hasDeclared {
		c.Via = append(c.Via, fleet.RelationshipDependency)
	}
	if len(refs) > 0 {
		c.Via = append(c.Via, fleet.RelationshipConfigRef)
	}
	if seen {
		c.Via = append(c.Via, fleet.ProvenanceObserved)
	}
	declared := hasDeclared || len(refs) > 0
	c.Provenance = provenance(declared, seen)
	c.Confidence = confidence(node.depth, declared, rel.Compatibility != "", observedFresh)
	if stale {
		c.Confidence = ConfidenceUnknown
	}
	ep, ok := effects[node.key]
	if !ok {
		// An observed-only caller: no declared path says what it needs.
		ep = effectPath{effect: EffectUnknown, path: []fleet.ServiceKey{node.key, subject}}
	}
	c.Effect = ep.effect
	c.Path = make([]string, len(ep.path))
	for i, k := range ep.path {
		c.Path[i] = string(k)
	}
	// The shortest breaks path is the direct hop exactly when it is required.
	c.Required = c.Effect == EffectBreaks && len(ep.path) == 2
	c.Note = effectNote(c.Effect, len(ep.path) == 2)
	c.Resources, c.ResourcesTotal = resourcesOf(uses, string(node.key))
	if ev := lifecycleEvidence(uses, string(node.key)); ev != nil {
		c.Evidence = boundEvidence(append(c.Evidence, *ev))
	}
	return c
}

// effectNote says why a consumer has its effect.
func effectNote(effect string, direct bool) string {
	switch {
	case effect == EffectBreaks && direct:
		return "it declares the dependency as required"
	case effect == EffectBreaks:
		return "every hop on its path is a required dependency"
	case effect == EffectMayDegrade:
		return "every path to it holds an optional dependency, so it may degrade"
	}
	return "it reaches it only through a configuration reference or an observed call, so the effect is unknown"
}

// resourcesOf is every distinct provisioning status of the uses by consumer
// key, bounded to MaxConsumerResources, and how many there are. A dependency
// and a configuration reference that ask for the same entry share a status.
func resourcesOf(uses []fleet.OfferingUse, key string) ([]fleet.ProvisioningStatus, int) {
	var out []fleet.ProvisioningStatus
	seen := map[[4]string]bool{}
	for _, u := range uses {
		if u.Consumer.Key != key {
			continue
		}
		for _, p := range u.Provisioning {
			id := [4]string{p.Environment, p.Entry, p.Phase, p.Source}
			if !seen[id] {
				seen[id] = true
				out = append(out, p)
			}
		}
	}
	return out[:min(len(out), MaxConsumerResources)], len(out)
}

// lifecycleEvidence is the provider's declared lifecycle as a use by
// consumer key reaches it, nil when it declares none.
func lifecycleEvidence(uses []fleet.OfferingUse, key string) *Evidence {
	for _, u := range uses {
		if u.Consumer.Key == key && u.ProviderLifecycle != nil {
			return &Evidence{Kind: EvidenceLifecycle, Source: u.Provider.Key, Provenance: fleet.ProvenanceDeclared,
				Message: u.Provider.Label + " declares its lifecycle: " + u.ProviderLifecycle.String()}
		}
	}
	return nil
}

// addEventReferrers adds the services that reference a removed subject
// through a policy reference: their effect is unknown.
func (r *Result) addEventReferrers(snap *fleet.FleetSnapshot, subject fleet.ServiceKey, observed []resolvedObservedEdge, uses []fleet.OfferingUse) {
	refs := policyReferrers(snap, subject)
	keys := make([]fleet.ServiceKey, 0, len(refs))
	for k := range refs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		c, stale := consumerBase(snap, k, subject, observed)
		c.Depth, c.Direct, c.Provenance, c.Confidence = 1, true, fleet.ProvenanceDeclared, ConfidenceDeclared
		if stale {
			c.Confidence = ConfidenceUnknown
		}
		c.Via, c.CompatibilityVerdict, c.Effect = []string{fleet.RelationshipPolicyRef}, CompatibilityUnknown, EffectUnknown
		c.Note = "its policy reference will no longer resolve, so its next revision fails validation"
		if ev := lifecycleEvidence(uses, string(k)); ev != nil {
			c.Evidence = boundEvidence(append(c.Evidence, *ev))
		}
		r.mergeConsumer(c)
	}
}

// eventPhrase says what happens to the subject.
func (r *Result) eventPhrase() string {
	switch r.Event.Kind {
	case EventUnavailable:
		return "becomes unavailable"
	case EventRemoved:
		return "is removed"
	}
	return strings.TrimSuffix("regressed at "+r.Event.Revision, " at ")
}

// eventGate is the gate of an event answer. It never blocks: only a contract
// change has a version a consumer can refuse.
func (r *Result) eventGate() (string, string) {
	gate, why := r.eventGateCore()
	if r.Event.Kind == EventRegressed {
		why += r.regressionSummary()
	}
	return gate, why
}

func (r *Result) eventGateCore() (string, string) {
	if r.Completeness != fleet.CompletenessComplete {
		return GateUnverified, "the answer is partial, so consumers Pacto cannot see may be affected. Fix the sources named in limitations and run the analysis again"
	}
	if len(r.Consumers) == 0 {
		why := "no contract in the complete snapshot declares a dependency on " + r.Service +
			" and no observation source saw a caller; a service with no contract can still call it. "
		if len(r.observationSources) == 0 {
			return GateClear, why + "No observation source was consulted"
		}
		return GateClear, why + "Observation sources consulted: " + strings.Join(r.observationSources, ", ")
	}
	var breaks, running, lastSeen []string
	counts := map[string]int{}
	for _, c := range r.Consumers {
		counts[c.Effect]++
		if c.Effect != EffectBreaks {
			continue
		}
		breaks = append(breaks, c.Service)
		switch {
		case len(freshTargets(c)) > 0:
			running = append(running, c.Service)
		case len(c.Targets) > 0:
			lastSeen = append(lastSeen, c.Service)
		}
	}
	var parts []string
	if n := len(breaks); n > 0 {
		s := strconv.Itoa(n) + " " + plural(n, "consumer breaks", "consumers break")
		var where []string
		if len(running) > 0 {
			where = append(where, strconv.Itoa(len(running))+" "+plural(len(running), "runs", "run")+" on known targets: "+strings.Join(running, ", "))
		}
		if len(lastSeen) > 0 {
			where = append(where, strconv.Itoa(len(lastSeen))+" "+plural(len(lastSeen), "was", "were")+
				" last seen on a target more than 24h before asOf: "+strings.Join(lastSeen, ", "))
		}
		if len(where) > 0 {
			s += " (" + strings.Join(where, "; ") + ")"
		}
		parts = append(parts, s)
	}
	if n := counts[EffectMayDegrade]; n > 0 {
		parts = append(parts, strconv.Itoa(n)+" may degrade")
	}
	if n := counts[EffectUnknown]; n > 0 {
		parts = append(parts, strconv.Itoa(n)+" "+plural(n, "is", "are")+" unknown")
	}
	return GateUnverified, "if " + r.Service + " " + r.eventPhrase() + ", " + joinAnd(parts) + ". Review each"
}

// joinAnd joins parts as "a, b and c".
func joinAnd(parts []string) string {
	if len(parts) < 2 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// freshTargets are the targets of c whose evidence is within the recent
// window (Decision 4): only of these is it said that c runs there.
func freshTargets(c AffectedConsumer) []string {
	var out []string
	for _, e := range c.Evidence {
		if e.Kind == EvidenceTarget && !e.Stale {
			out = append(out, e.Source)
		}
	}
	sort.Strings(out)
	return out
}

// maxRegressionSummary bounds the delivery facts a gate reason cites.
const maxRegressionSummary = 3

// regressionSummary cites the delivery evidence, or says that delivery
// recorded no failure only when the evidence was read in full.
func (r *Result) regressionSummary() string {
	if len(r.Regression) == 0 {
		if hasCode(r.Limitations, LimitationDeliveryEvidenceUnavailable) {
			return ". Delivery evidence was not read in full, see limitations"
		}
		return ". Delivery recorded no failure of " + r.Event.Revision + " in the Stages you may read; the regression is your hypothesis"
	}
	var cites []string
	for _, e := range r.Regression[:min(len(r.Regression), maxRegressionSummary)] {
		where := e.Namespace + "/" + e.Stage
		switch e.Kind {
		case RegressionCheck:
			cites = append(cites, where+" gate "+e.Gate+" failed ("+e.Message+")")
		case RegressionRollback:
			cites = append(cites, where+" rolled it back ("+e.Message+")")
		default:
			cites = append(cites, where+" lists it as failed")
		}
	}
	s := ". Delivery recorded: " + strings.Join(cites, "; ")
	if more := len(r.Regression) - maxRegressionSummary; more > 0 {
		s += " and " + strconv.Itoa(more) + " more"
	}
	return s
}
