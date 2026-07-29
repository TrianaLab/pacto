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
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/diff"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// SchemaVersion identifies the impact wire model.
const SchemaVersion = "pacto.dev/impact/v1"

// Confidence grades how strongly the evidence supports an affected-consumer claim.
type Confidence string

const (
	ConfidenceContractual  Confidence = "contractual"  // explicit declared dependency + compatibility
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
}

// Result is the deterministic impact answer.
type Result struct {
	SchemaVersion   string             `json:"schemaVersion"`
	SnapshotID      string             `json:"snapshotId"`
	AsOf            time.Time          `json:"asOf"`
	Service         string             `json:"service"`
	OldVersion      string             `json:"oldVersion,omitempty"`
	NewVersion      string             `json:"newVersion,omitempty"`
	Classification  string             `json:"classification"`
	BreakingChanges []diff.Change      `json:"breakingChanges,omitempty"`
	Consumers       []AffectedConsumer `json:"consumers"`
	ActiveTargets   []string           `json:"activeTargets,omitempty"`
	Owners          []string           `json:"owners,omitempty"`
	Completeness    fleet.Completeness `json:"completeness"`
	Limitations     []fleet.Limitation `json:"limitations,omitempty"`
}

// Analyze compares old→new and projects the change onto the operational graph.
func Analyze(ctx context.Context, old, new *contract.Contract, oldFS, newFS fs.FS, snap *fleet.FleetSnapshot, opts Options) *Result {
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
	for _, ch := range d.Changes {
		if ch.Classification != diff.NonBreaking {
			res.BreakingChanges = append(res.BreakingChanges, ch)
		}
	}

	q := fleet.NewQuery(snap)
	graph, err := q.Graph(fleet.GraphQuery{Service: svc, Direction: fleet.DirectionDependents, Transitive: true})
	if err != nil {
		res.Limitations = append(res.Limitations, fleet.Limitation{
			Code: "SERVICE_NOT_IN_FLEET", Source: "impact",
			Message: "the changed service is not present in the operational graph; consumers cannot be determined",
		})
		return res
	}

	changed := graph.Root
	owners := map[string]bool{}
	targets := map[string]bool{}
	for _, tk := range serviceTargets(snap, changed) {
		targets[tk] = true
	}
	for _, cn := range unionConsumers(graph, changed, opts) {
		c := consumerImpact(snap, changed, cn, new.Service.Version, opts)
		res.Consumers = append(res.Consumers, c)
		if c.Owner != "" {
			owners[c.Owner] = true
		}
		for _, tk := range c.Targets {
			targets[tk] = true
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

// unionConsumers merges the declared dependents (from the graph) with observed
// callers of the changed service. Observed-only callers are included as direct
// (depth 1) consumers only when IncludeObserved is set. The result is sorted by
// name for deterministic output.
func unionConsumers(graph *fleet.GraphResult, changed fleet.ServiceKey, opts Options) []consumerNode {
	byKey := map[fleet.ServiceKey]consumerNode{}
	for _, node := range graph.Nodes {
		byKey[node.Key] = consumerNode{key: node.Key, name: node.Name, depth: node.Depth, path: node.Path}
	}
	if opts.IncludeObserved {
		for _, e := range opts.ObservedEdges {
			if fleet.NewServiceKey(e.Provider) != changed {
				continue
			}
			ck := fleet.NewServiceKey(e.Consumer)
			if _, ok := byKey[ck]; !ok {
				byKey[ck] = consumerNode{key: ck, name: e.Consumer, depth: 1, path: []fleet.ServiceKey{changed, ck}}
			}
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
func consumerImpact(snap *fleet.FleetSnapshot, changed fleet.ServiceKey, node consumerNode, newVersion string, opts Options) AffectedConsumer {
	domain, _ := fleet.ParseServiceKey(node.key)
	c := AffectedConsumer{
		Service: node.name,
		Domain:  domain,
		Depth:   node.depth,
		Direct:  node.depth == 1,
		Path:    keysToStrings(node.path),
	}
	if s := snap.Services[node.key]; s != nil {
		c.Owner = s.Owner.DisplayString()
		c.Status = s.Status
		for _, tk := range s.Targets {
			c.Targets = append(c.Targets, string(tk))
		}
		sort.Strings(c.Targets)
	}

	rel, hasDeclared, observed := edgeEvidence(snap, node.key, changed, opts.ObservedEdges)
	// Observed evidence is only counted when the caller opted in; provenance and
	// confidence must agree on what was counted.
	observedCounted := observed && opts.IncludeObserved
	c.Required = rel.Required
	c.Compatibility = rel.Compatibility
	c.Provenance = provenance(hasDeclared, observedCounted)
	c.CompatibilityVerdict = compatibilityVerdict(rel.Compatibility, newVersion, hasDeclared)
	c.Confidence = confidence(node.depth, hasDeclared, observedCounted)
	return c
}

// edgeEvidence finds the declared dependency edge from consumer→changed and
// whether an observed edge exists — either recorded in the graph as an observed
// relationship or supplied via telemetry (observedEdges).
func edgeEvidence(snap *fleet.FleetSnapshot, consumer, changed fleet.ServiceKey, observedEdges []ObservedEdge) (rel fleet.Relationship, declared, observed bool) {
	for i := range snap.Relationships {
		r := snap.Relationships[i]
		if r.Type != fleet.RelationshipDependency || r.FromService != consumer || r.ToService != changed {
			continue
		}
		if r.Provenance == fleet.ProvenanceObserved {
			observed = true
			continue
		}
		rel = r
		declared = true
	}
	for _, e := range observedEdges {
		if fleet.NewServiceKey(e.Consumer) == consumer && fleet.NewServiceKey(e.Provider) == changed {
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

// confidence grades a consumer per the documented model.
func confidence(depth int, declared, observed bool) Confidence {
	if depth > 1 {
		return ConfidenceInferred
	}
	switch {
	case declared && observed:
		return ConfidenceCorroborated
	case observed:
		return ConfidenceObserved
	case declared:
		return ConfidenceContractual
	default:
		return ConfidenceUnknown
	}
}

// compatibilityVerdict checks the new version against a consumer's declared
// compatibility range. Without a declared range the verdict is unknown.
func compatibilityVerdict(constraint, version string, declared bool) string {
	if !declared || constraint == "" || version == "" {
		return CompatibilityUnknown
	}
	cs, err := semver.NewConstraint(constraint)
	if err != nil {
		return CompatibilityUnknown
	}
	v, err := semver.NewVersion(version)
	if err != nil {
		return CompatibilityUnknown
	}
	if cs.Check(v) {
		return CompatibilityCompatible
	}
	return CompatibilityIncompatible
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

// keysToStrings renders a path of service keys as strings for the wire model.
// Every consumer node carries a non-empty path (root→consumer), so no nil guard
// is needed.
func keysToStrings(ks []fleet.ServiceKey) []string {
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = string(k)
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
