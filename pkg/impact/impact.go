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

// Options configures the analysis.
type Options struct {
	// IncludeObserved lets observed relationships raise confidence when the graph
	// carries them (declared-only when false).
	IncludeObserved bool
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

	owners := map[string]bool{}
	targets := map[string]bool{}
	for _, tk := range serviceTargets(snap, svc) {
		targets[tk] = true
	}
	for _, node := range graph.Nodes {
		c := consumerImpact(snap, svc, node, new.Service.Version, opts)
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

// consumerImpact builds the affected-consumer record for one dependent node.
func consumerImpact(snap *fleet.FleetSnapshot, changed string, node fleet.GraphNode, newVersion string, opts Options) AffectedConsumer {
	c := AffectedConsumer{
		Service: node.Name,
		Depth:   node.Depth,
		Direct:  node.Depth == 1,
		Path:    node.Path,
	}
	if s := snap.Services[fleet.NewServiceKey(node.Name)]; s != nil {
		c.Owner = s.Owner.DisplayString()
		c.Status = s.Status
		for _, tk := range s.Targets {
			c.Targets = append(c.Targets, string(tk))
		}
		sort.Strings(c.Targets)
	}

	rel, hasDeclared, observed := edgeEvidence(snap, node.Name, changed)
	// Observed evidence is only counted when the caller opted in; provenance and
	// confidence must agree on what was counted.
	observedCounted := observed && opts.IncludeObserved
	c.Required = rel.Required
	c.Compatibility = rel.Compatibility
	c.Provenance = provenance(hasDeclared, observedCounted)
	c.CompatibilityVerdict = compatibilityVerdict(rel.Compatibility, newVersion, hasDeclared)
	c.Confidence = confidence(node.Depth, hasDeclared, observedCounted)
	return c
}

// edgeEvidence finds the declared dependency edge from consumer→changed and
// whether an observed edge exists.
func edgeEvidence(snap *fleet.FleetSnapshot, consumer, changed string) (rel fleet.Relationship, declared, observed bool) {
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

func serviceTargets(snap *fleet.FleetSnapshot, name string) []string {
	s := snap.Services[fleet.NewServiceKey(name)]
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.Targets))
	for _, tk := range s.Targets {
		out = append(out, string(tk))
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
