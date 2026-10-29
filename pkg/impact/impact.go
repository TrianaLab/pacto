// Package impact composes a semantic contract diff with the operational graph to
// answer "what is the real blast radius of this change?". It is framework
// independent: it consumes the pure diff engine ([diff]) and the immutable
// operational-graph read model ([fleet]) and imports no Kubernetes, OCI client,
// dashboard, MCP or HTTP code, so the same analysis backs the CLI, an MCP tool
// and the dashboard.
//
// Impact is deliberately conservative about certainty. A declared dependency and
// compatibility range is contractual evidence; a runtime observation is observed
// evidence; a transitive effect is inferred; missing or stale evidence is
// unknown. It never presents an inferred path as a confirmed runtime impact and
// never recommends actions — it lists review targets. External controllers and
// agents act; external policy and IAM systems authorize.
package impact

import (
	"context"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// SchemaVersion identifies the impact wire model.
const SchemaVersion = "pacto.dev/impact/v1"

// limitationSource marks a limitation the analysis raised itself, as opposed to
// one copied from the snapshot. boundEnvelope keeps these ahead of the cap.
const limitationSource = "impact"

// Confidence grades how strongly the evidence supports an affected-consumer claim.
type Confidence string

const (
	ConfidenceContractual  Confidence = "contractual"  // declared dependency WITH a usable compatibility range
	ConfidenceDeclared     Confidence = "declared"     // declared dependency but no usable compatibility range
	ConfidenceObserved     Confidence = "observed"     // runtime use observed in a window
	ConfidenceCorroborated Confidence = "corroborated" // declared and observed agree
	ConfidenceInferred     Confidence = "inferred"     // transitive effect through another affected service
	ConfidenceUnknown      Confidence = "unknown"      // required evidence is incomplete or stale
)

// Compatibility verdicts for a consumer's declared range against the new version.
const (
	CompatibilityCompatible   = "compatible"
	CompatibilityIncompatible = "incompatible"
	CompatibilityUnknown      = "unknown"
)

// ObservedEdge is an observed caller→provider dependency (e.g. derived from
// OpenTelemetry traces). It is evidence of real traffic, independent of what a
// contract declares.
type ObservedEdge struct {
	Consumer string
	Provider string
}

// Options configures the analysis.
type Options struct {
	// Domain is the logical-service domain of the changed service. It disambiguates
	// same-named services across domains so impact answers are domain-isolated. The
	// empty domain is the default single-domain case.
	Domain string
	// IncludeObserved lets observed relationships raise confidence and surface
	// observed-only (shadow) consumers. When false the analysis is declared-only.
	IncludeObserved bool
	// ObservedEdges are observed caller→provider dependencies. Callers of the
	// changed service corroborate a declared consumer (raising confidence to
	// corroborated) or, when only observed, appear as observed-only consumers.
	ObservedEdges []ObservedEdge
}

// AffectedConsumer is one service affected by the change.
type AffectedConsumer struct {
	Service              string     `json:"service"`
	Domain               string     `json:"domain,omitempty"`
	Depth                int        `json:"depth"`  // 1 = direct, >1 = transitive
	Direct               bool       `json:"direct"` // depth == 1
	Path                 []string   `json:"path"`
	Owner                string     `json:"owner,omitempty"`
	Required             bool       `json:"required"`
	Compatibility        string     `json:"compatibility,omitempty"`
	CompatibilityVerdict string     `json:"compatibilityVerdict"`
	Provenance           string     `json:"provenance"`
	Confidence           Confidence `json:"confidence"`
	Status               string     `json:"status,omitempty"`
	Targets              []string   `json:"targets,omitempty"`
	// Uncontracted marks a runtime caller no contract describes: Service is its
	// raw observed identity, and it has no range a change could be checked against.
	Uncontracted bool `json:"uncontracted,omitempty"`
	// Note qualifies the verdict in plain words. On a BREAKING change a
	// compatible verdict only means the consumer's range accepts the new version,
	// not that the consumer avoids what the change removes.
	Note string `json:"note,omitempty"`
}

// Release gate values (see [Result.Gate]).
const (
	// GateBlocked: the change is BREAKING and reaches a consumer that does not
	// accept the new version and runs on a target the snapshot knows.
	GateBlocked = "blocked"
	// GateUnverified: the change is BREAKING and nothing is shown to block it,
	// but the answer cannot clear it either: the answer is partial, or consumers
	// depend on the service whose safety the evidence cannot show.
	GateUnverified = "unverified"
	// GateClear: nothing in the answer stands in the way of the change.
	GateClear = "clear"
)

// Result is the deterministic impact answer.
type Result struct {
	SchemaVersion              string        `json:"schemaVersion"`
	SnapshotID                 string        `json:"snapshotId"`
	AsOf                       time.Time     `json:"asOf"`
	Service                    string        `json:"service"`
	OldVersion                 string        `json:"oldVersion,omitempty"`
	NewVersion                 string        `json:"newVersion,omitempty"`
	Classification             string        `json:"classification"`
	BreakingChanges            []diff.Change `json:"breakingChanges,omitempty"`
	PotentiallyBreakingChanges []diff.Change `json:"potentiallyBreakingChanges,omitempty"`
	// NonBreakingChanges completes the field-level semantic diff. The three change
	// sets together are exactly diff.Result.Changes, partitioned by classification,
	// so a consumer can present the WHOLE change (an added optional field is the most
	// common real change) without re-running the diff over the same two contracts.
	NonBreakingChanges []diff.Change      `json:"nonBreakingChanges,omitempty"`
	Consumers          []AffectedConsumer `json:"consumers"`
	ActiveTargets      []string           `json:"activeTargets,omitempty"`
	Owners             []string           `json:"owners,omitempty"`
	Completeness       fleet.Completeness `json:"completeness"`
	Limitations        []fleet.Limitation `json:"limitations,omitempty"`
	// LimitationsTruncated reports that the snapshot and analysis produced more
	// limitations than the answer carries.
	LimitationsTruncated bool `json:"limitationsTruncated,omitempty"`
	// NewRevisionAdded reports that the changed service was in none of the fleet
	// sources, so the new revision itself was added to the snapshot to find the
	// services that depend on it. The changed service then has no targets.
	NewRevisionAdded bool `json:"newRevisionAdded,omitempty"`
	// Gate is the release decision `pacto impact` exits on: blocked (exit 1),
	// unverified (exit 2) or clear (exit 0). GateReason says why in plain words
	// and, when it is not clear, what to do next.
	Gate       string `json:"gate" enum:"blocked,unverified,clear"`
	GateReason string `json:"gateReason"`
}

// boundEnvelope applies the same cap fleet answers apply to the completeness
// envelope. Several build paths emit one limitation per record -- per ambiguous
// revision link, per non-canonical compliance value -- so on a fleet where two
// sources merely disagree about a mutable tag the envelope is one entry per
// target, and copying it verbatim makes the envelope most of the answer.
//
// The analysis's OWN limitations are moved to the front first. They are appended
// after the snapshot's, and a fleet large enough to truncate a blast radius is a
// fleet with hundreds of per-record limitations, so the entries that explain why
// THIS answer is partial were exactly the ones the cap discarded.
func (r *Result) boundEnvelope() {
	sort.SliceStable(r.Limitations, func(i, j int) bool {
		return r.Limitations[i].Source == limitationSource && r.Limitations[j].Source != limitationSource
	})
	r.Limitations, r.LimitationsTruncated = fleet.BoundLimitations(r.Limitations)
}

// ReleaseBlocking reports whether this change is the kind `pacto impact` fails
// on: a breaking change that reaches a consumer which is both incompatible and
// actually deployed. A consumer that is incompatible on paper but running
// nowhere the snapshot can see is not a release blocker.
func (r *Result) ReleaseBlocking() bool {
	if r.Classification != "BREAKING" {
		return false
	}
	for _, c := range r.Consumers {
		if c.CompatibilityVerdict == CompatibilityIncompatible && len(c.Targets) > 0 {
			return true
		}
	}
	return false
}

// gate derives the release gate from the finished answer. Only a BREAKING change
// can be held. It is blocked when an incompatible consumer runs somewhere. It
// is clear only when the answer is complete and no service depends on the
// changed one: a partial answer may be missing consumers, and a consumer that
// is incompatible on paper, has no usable range or only matches by range is a
// consumer nobody has shown to be safe.
func (r *Result) gate() (string, string) {
	switch r.Classification {
	case "BREAKING":
	case "POTENTIAL_BREAKING":
		return GateClear, "the change is POTENTIAL_BREAKING: the gate holds only BREAKING changes, so review the potentially breaking changes by hand"
	default:
		return GateClear, "the change is " + r.Classification
	}
	if r.ReleaseBlocking() {
		var active []string
		for _, c := range r.Consumers {
			if c.CompatibilityVerdict == CompatibilityIncompatible && len(c.Targets) > 0 {
				active = append(active, c.Service)
			}
		}
		return GateBlocked, "breaking changes affect active consumers, which run on known targets and do not accept " +
			r.Service + " " + r.NewVersion + ": " + strings.Join(active, ", ")
	}
	if hasCode(r.Limitations, "SERVICE_NOT_IN_FLEET") {
		return GateUnverified, "the change is BREAKING, but " + r.Service + " is not in the fleet snapshot, so its consumers are unknown. " +
			"Add the directory or registry that holds the services depending on it as a fleet source and run the analysis again"
	}
	if r.Completeness != fleet.CompletenessComplete {
		return GateUnverified, "the change is BREAKING and the answer is partial, so consumers Pacto cannot see may break. " +
			"Fix the sources named in limitations and run the analysis again"
	}
	if len(r.Consumers) > 0 {
		n := strconv.Itoa(len(r.Consumers)) + " consumers depend"
		if len(r.Consumers) == 1 {
			n = "1 consumer depends"
		}
		return GateUnverified, "the change is BREAKING and " + n + " on " + r.Service +
			", but none is shown to be both incompatible and running. Review each consumer, and add a source that knows where services run " +
			"(for example --target-state) to tell deployed impact apart"
	}
	return GateClear, "the change is BREAKING, but no service in the complete fleet snapshot depends on " + r.Service
}

func hasCode(ls []fleet.Limitation, code string) bool {
	for _, l := range ls {
		if l.Code == code {
			return true
		}
	}
	return false
}

// openRangeProbe is a version no real range caps below: a range that accepts it
// has no upper bound.
const openRangeProbe = "999999.0.0"

// noteRangeOnlyMatches qualifies every compatible verdict on a BREAKING change.
// The verdict compares the range to the new version and nothing else, so it
// cannot show that the consumer avoids the removed or changed element.
func (r *Result) noteRangeOnlyMatches() {
	if r.Classification != "BREAKING" {
		return
	}
	for i := range r.Consumers {
		c := &r.Consumers[i]
		if c.CompatibilityVerdict != CompatibilityCompatible {
			continue
		}
		c.Note = "compatible only means the range " + c.Compatibility + " accepts " + r.NewVersion +
			"; this change is BREAKING, so check that " + c.Service + " does not use what it removes or changes"
		if fleet.CheckRange(c.Compatibility, openRangeProbe) == fleet.RangeSatisfied {
			c.Note = "the range " + c.Compatibility + " has no upper bound, so " + c.Note
		}
	}
}

// Analyze compares old→new and projects the change onto the operational graph.
// The answer always carries its release gate (see [Result.Gate]).
func Analyze(ctx context.Context, old, new *contract.Contract, oldFS, newFS fs.FS, snap *fleet.FleetSnapshot, opts Options) *Result {
	res := analyze(ctx, old, new, oldFS, newFS, snap, opts)
	res.boundEnvelope()
	res.noteRangeOnlyMatches()
	res.Gate, res.GateReason = res.gate()
	return res
}

func analyze(ctx context.Context, old, new *contract.Contract, oldFS, newFS fs.FS, snap *fleet.FleetSnapshot, opts Options) *Result {
	d := diff.Compare(ctx, old, new, oldFS, newFS)
	svc := new.Service.Name

	res := &Result{
		SchemaVersion:  SchemaVersion,
		SnapshotID:     snap.SnapshotID,
		AsOf:           snap.GeneratedAt,
		Service:        svc,
		OldVersion:     old.Service.Version,
		NewVersion:     new.Service.Version,
		Classification: d.Classification.String(),
		Completeness:   snap.Completeness,
		Limitations:    append([]fleet.Limitation(nil), snap.Limitations...),
		Consumers:      []AffectedConsumer{},
	}
	// Breaking and potentially-breaking changes are kept SEPARATE: a
	// POTENTIAL_BREAKING change is not a confirmed break and must not be presented
	// as one.
	for _, ch := range d.Changes {
		switch ch.Classification {
		case diff.Breaking:
			res.BreakingChanges = append(res.BreakingChanges, ch)
		case diff.PotentialBreaking:
			res.PotentiallyBreakingChanges = append(res.PotentiallyBreakingChanges, ch)
		default:
			res.NonBreakingChanges = append(res.NonBreakingChanges, ch)
		}
	}

	svcKey := fleet.NewServiceKeyDomain(opts.Domain, svc)
	q := fleet.NewQuery(snap)
	graph, err := q.Graph(fleet.GraphQuery{Service: string(svcKey), Direction: fleet.DirectionDependents, Transitive: true})
	if err != nil {
		// The changed service is not in the graph, so NO consumer can be ruled in
		// or out. Reporting "complete" here would let an empty consumer list read
		// as "nothing breaks" when it means "I could not tell" — and warnPartial
		// early-returns on complete, so the limitation below would never reach a
		// human at all.
		res.Completeness = fleet.CompletenessPartial
		res.Limitations = append(res.Limitations, fleet.Limitation{
			Code: "SERVICE_NOT_IN_FLEET", Source: limitationSource,
			Message: "the changed service is not present in the operational graph; consumers cannot be determined",
		})
		return res
	}
	if graph.Truncated {
		// The traversal stopped at the node cap, so the consumer list below is a
		// deterministic PREFIX of the reachable dependents. Leaving completeness
		// alone would let ReleaseBlocking() clear a breaking change on the strength
		// of whichever consumers happened to sort first, and warnPartial
		// early-returns on complete, so the cap would never reach a human.
		res.Completeness = fleet.CompletenessPartial
		res.Limitations = append(res.Limitations, fleet.Limitation{
			Code: "GRAPH_TRUNCATED", Source: limitationSource,
			Message: "the dependents traversal hit the " + strconv.Itoa(fleet.MaxGraphNodes) +
				"-node cap; the consumer list is a prefix of the blast radius, not the whole of it",
		})
	}

	changed := graph.Root
	// Resolve observed (OTel) endpoints to unique domain-qualified keys ONCE;
	// unresolved/ambiguous ones become explicit limitations and never corroborate.
	resolvedObserved, obsLims := resolveObservedEdges(snap, opts.ObservedEdges, opts.IncludeObserved)
	res.Limitations = append(res.Limitations, obsLims...)
	// The snapshot may already carry observed relationships folded in by Build (the
	// real observation pipeline). They are pre-resolved to domain-qualified keys, so
	// they join the ad-hoc --traces edges directly when observed evidence is opted in.
	if opts.IncludeObserved {
		resolvedObserved = append(resolvedObserved, snapshotObservedEdges(snap)...)
	}
	owners := map[string]bool{}
	targets := map[string]bool{}
	for _, tk := range serviceTargets(snap, changed) {
		targets[tk] = true
	}
	for _, cn := range unionConsumers(graph, changed, resolvedObserved) {
		c := consumerImpact(snap, changed, cn, new.Service.Version, resolvedObserved)
		res.Consumers = append(res.Consumers, c)
		if c.Owner != "" {
			owners[c.Owner] = true
		}
		for _, tk := range c.Targets {
			targets[tk] = true
		}
	}
	if opts.IncludeObserved {
		for _, u := range snap.UncontractedCallers {
			if u.ToService == changed {
				res.Consumers = append(res.Consumers, AffectedConsumer{
					Service: u.Caller, Depth: 1, Direct: true, Path: []string{u.Caller, string(changed)},
					CompatibilityVerdict: CompatibilityUnknown, Provenance: fleet.ProvenanceObserved,
					Confidence: ConfidenceObserved, Uncontracted: true,
				})
			}
		}
	}
	res.Owners = sortedKeys(owners)
	res.ActiveTargets = sortedKeys(targets)
	return res
}

// consumerNode identifies one affected consumer and its position in the graph.
type consumerNode struct {
	key   fleet.ServiceKey
	name  string
	depth int
	path  []fleet.ServiceKey
}

// resolvedObservedEdge is an observed caller→provider edge whose endpoints have
// been resolved to UNIQUE domain-qualified fleet ServiceKeys.
type resolvedObservedEdge struct{ consumer, provider fleet.ServiceKey }

// ObservedIdentityUnresolved is the limitation code for an observed endpoint whose
// OTel service name could not be mapped to exactly one fleet service. It aliases
// the fleet code so the pipeline reports one stable code end to end.
const ObservedIdentityUnresolved = fleet.LimitationObservedIdentityUnresolved

// resolveObservedEdges maps raw observed (OTel) endpoint names to UNIQUE
// domain-qualified ServiceKeys via the snapshot. A name matching no service, or
// more than one (across domains), is NOT silently assigned the default domain: the
// edge is dropped from corroboration and a structured limitation is emitted, so
// observed evidence can never corroborate or affect the wrong same-named service in
// another domain. Returns nothing when observed evidence was not opted in.
func resolveObservedEdges(snap *fleet.FleetSnapshot, edges []ObservedEdge, include bool) ([]resolvedObservedEdge, []fleet.Limitation) {
	if !include || len(edges) == 0 {
		return nil, nil
	}
	resolve := snap.ObservedNameResolver()
	var out []resolvedObservedEdge
	var lims []fleet.Limitation
	seen := map[string]bool{}
	addLim := func(name string, res fleet.ObservedResolution) bool {
		reason := ""
		switch res {
		case fleet.ObservedUnknown:
			reason = "unknown"
		case fleet.ObservedAmbiguous:
			reason = "ambiguous across domains"
		default:
			return true
		}
		if !seen[reason+":"+name] {
			seen[reason+":"+name] = true
			lims = append(lims, fleet.Limitation{Code: ObservedIdentityUnresolved, Source: limitationSource,
				Message: "observed service " + name + " could not be mapped to a unique fleet service (" + reason + "); it does not corroborate or affect any domain-qualified service"})
		}
		return false
	}
	for _, e := range edges {
		ck, cRes := resolve(e.Consumer)
		pk, pRes := resolve(e.Provider)
		cOK := addLim(e.Consumer, cRes)
		pOK := addLim(e.Provider, pRes)
		if cOK && pOK {
			out = append(out, resolvedObservedEdge{consumer: ck, provider: pk})
		}
	}
	sort.Slice(lims, func(i, j int) bool { return lims[i].Message < lims[j].Message })
	return out, lims
}

// snapshotObservedEdges extracts the observed dependency relationships Build
// already folded into the snapshot. They are pre-resolved to domain-qualified
// keys, so no re-resolution (and no default-domain risk) applies here.
func snapshotObservedEdges(snap *fleet.FleetSnapshot) []resolvedObservedEdge {
	var out []resolvedObservedEdge
	for _, r := range snap.Relationships {
		if r.Type == fleet.RelationshipDependency && r.Provenance == fleet.ProvenanceObserved && r.Resolved {
			out = append(out, resolvedObservedEdge{consumer: r.FromService, provider: r.ToService})
		}
	}
	return out
}

// unionConsumers merges the declared dependents (from the graph) with observed
// callers of the changed service — using only observed edges whose endpoints
// resolved to unique domain-qualified keys. Observed-only callers are direct
// (depth 1). The result is sorted for deterministic output.
func unionConsumers(graph *fleet.GraphResult, changed fleet.ServiceKey, observed []resolvedObservedEdge) []consumerNode {
	byKey := map[fleet.ServiceKey]consumerNode{}
	for _, node := range graph.Nodes {
		byKey[node.Key] = consumerNode{key: node.Key, name: node.Name, depth: node.Depth, path: node.Path}
	}
	for _, e := range observed {
		if e.provider != changed {
			continue
		}
		if _, ok := byKey[e.consumer]; !ok {
			_, name := fleet.ParseServiceKey(e.consumer)
			byKey[e.consumer] = consumerNode{key: e.consumer, name: name, depth: 1, path: []fleet.ServiceKey{changed, e.consumer}}
		}
	}
	out := make([]consumerNode, 0, len(byKey))
	for _, cn := range byKey {
		out = append(out, cn)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

// consumerImpact builds the affected-consumer record for one consumer.
func consumerImpact(snap *fleet.FleetSnapshot, changed fleet.ServiceKey, node consumerNode, newVersion string, observedEdges []resolvedObservedEdge) AffectedConsumer {
	domain, _ := fleet.ParseServiceKey(node.key)
	c := AffectedConsumer{
		Service: node.name,
		Domain:  domain,
		Depth:   node.depth,
		Direct:  node.depth == 1,
		Path:    pathConsumerFirst(node.path),
	}
	if s := snap.Services[node.key]; s != nil {
		c.Owner = s.Owner.DisplayString()
		c.Status = s.Status
		for _, tk := range s.Targets {
			c.Targets = append(c.Targets, string(tk))
		}
		sort.Strings(c.Targets)
	}

	rel, hasDeclared, observed := edgeEvidence(snap, node.key, changed, newVersion, observedEdges)
	// The observedEdges slice is empty unless the caller opted in and the endpoints
	// resolved to unique domain-qualified keys, so `observed` is already gated.
	observedCounted := observed
	c.Required = rel.Required
	c.Compatibility = rel.Compatibility
	c.Provenance = provenance(hasDeclared, observedCounted)
	c.CompatibilityVerdict = compatibilityVerdict(rel.Compatibility, newVersion, hasDeclared)
	c.Confidence = confidence(node.depth, hasDeclared, rel.Compatibility != "", observedCounted)
	return c
}

// edgeEvidence finds the declared dependency edge from consumer→changed and
// whether an observed edge exists — either recorded in the graph as an observed
// relationship or supplied via telemetry. The observedEdges are pre-resolved to
// unique domain-qualified keys, so comparison is a plain key match (no default-
// domain coercion that could corroborate a same-named service in another domain).
//
// A consumer running more than one revision declares one edge per revision.
// The one that accepts newVersion least speaks for the consumer (incompatible
// over unknown over compatible): one running revision that breaks is enough
// for the change to break the consumer, whatever order the edges come in.
func edgeEvidence(snap *fleet.FleetSnapshot, consumer, changed fleet.ServiceKey, newVersion string, observedEdges []resolvedObservedEdge) (rel fleet.Relationship, declared, observed bool) {
	for i := range snap.Relationships {
		r := snap.Relationships[i]
		if r.Type != fleet.RelationshipDependency || r.FromService != consumer || r.ToService != changed {
			continue
		}
		// Skipped, never counted. Build folds observed relationships into the
		// snapshot unconditionally, so reading corroboration out of them here
		// bypassed the opt-in: a declared consumer came back "declared+observed" and
		// corroborated from a plain declared-only analysis over any snapshot with
		// observation sources. When the caller DOES opt in, snapshotObservedEdges
		// already supplies the same edges through observedEdges, so this is the one
		// place the gate belongs. The continue stays: an observed relationship is
		// not the declared one.
		if r.Provenance == fleet.ProvenanceObserved {
			continue
		}
		if !declared || verdictRank(compatibilityVerdict(r.Compatibility, newVersion, true)) > verdictRank(compatibilityVerdict(rel.Compatibility, newVersion, true)) {
			rel = r
		}
		declared = true
	}
	for _, e := range observedEdges {
		if e.consumer == consumer && e.provider == changed {
			observed = true
		}
	}
	return rel, declared, observed
}

func provenance(declared, observed bool) string {
	switch {
	case declared && observed:
		return "declared+observed"
	case observed:
		return fleet.ProvenanceObserved
	case declared:
		return fleet.ProvenanceDeclared
	default:
		// No declared or counted-observed edge to this consumer: the effect is
		// inferred (transitive, or a direct edge whose evidence was not counted).
		return fleet.ProvenanceInferred
	}
}

// confidence grades a consumer per the documented model. A declared dependency
// counts as contractual ONLY when it carries a usable compatibility range;
// declared-without-range is its own weaker grade.
func confidence(depth int, declared, hasRange, observed bool) Confidence {
	if depth > 1 {
		return ConfidenceInferred
	}
	switch {
	case declared && observed:
		return ConfidenceCorroborated
	case observed:
		return ConfidenceObserved
	case declared && hasRange:
		return ConfidenceContractual
	case declared:
		return ConfidenceDeclared
	default:
		return ConfidenceUnknown
	}
}

// compatibilityVerdict checks the new version against a consumer's declared
// compatibility range. Without a declared range the verdict is unknown.
func compatibilityVerdict(constraint, version string, declared bool) string {
	if !declared {
		return CompatibilityUnknown
	}
	switch fleet.CheckRange(constraint, version) {
	case fleet.RangeSatisfied:
		return CompatibilityCompatible
	case fleet.RangeUnsatisfied:
		return CompatibilityIncompatible
	}
	return CompatibilityUnknown
}

// verdictRank orders compatibility verdicts from the one that accepts most.
func verdictRank(v string) int {
	switch v {
	case CompatibilityIncompatible:
		return 2
	case CompatibilityUnknown:
		return 1
	}
	return 0
}

func serviceTargets(snap *fleet.FleetSnapshot, key fleet.ServiceKey) []string {
	s := snap.Services[key]
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.Targets))
	for _, tk := range s.Targets {
		out = append(out, string(tk))
	}
	return out
}

// pathConsumerFirst renders the path in the documented consumer → intermediate →
// changed-service orientation. The fleet dependents traversal produces it
// changed-service-first, so it is reversed here. Every consumer node carries a
// non-empty path, so no nil guard is needed.
func pathConsumerFirst(ks []fleet.ServiceKey) []string {
	out := make([]string, len(ks))
	for i, k := range ks {
		out[len(ks)-1-i] = string(k)
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
