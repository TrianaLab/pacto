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
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/validation"
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
	// LastSeen is the latest time the call was seen; nil when the source has
	// no observation window.
	LastSeen *time.Time
	// Count is how many calls were seen; 0 when the source does not count.
	Count int
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
	// A traces file that was read but saw no call is an empty, non-nil slice,
	// so an event answer still names it among the sources it consulted.
	ObservedEdges []ObservedEdge
	// Event switches the analysis to a non-contract what-if about one fleet
	// service. Analyze then ignores its contract and FS arguments, which may
	// be nil.
	Event *Event
	// Regression is the delivery evidence the caller read for a regressed
	// event, with its own identity; nil when none was read.
	Regression *Regression
}

// AffectedConsumer is one service affected by the change.
type AffectedConsumer struct {
	Service              string     `json:"service"`
	Domain               string     `json:"domain,omitempty"`
	Depth                int        `json:"depth"`  // 1 = direct, >1 = transitive
	Direct               bool       `json:"direct"` // depth == 1
	Path                 []string   `json:"path"`
	Owner                string     `json:"owner,omitempty"`
	OwnerKey             string     `json:"ownerKey,omitempty" doc:"The canonical owner key (for example team:shop). Group or link by it; owner is the display label"`
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
	// Via lists the edge types that make this service a consumer.
	Via []string `json:"via,omitempty" doc:"The edge types that make this service a consumer: dependency, configuration_reference, policy_reference or observed"`
	// Unmet lists the guarantees this consumer declares it requires from the
	// changed service that the new revision does not meet, over every revision
	// of the consumer the snapshot holds. A consumer with an unmet guarantee
	// breaks whatever its range accepts.
	Unmet []contract.Mismatch `json:"unmet,omitempty"`
	// Note qualifies the verdict in plain words. On a BREAKING change a
	// compatible verdict only means the consumer's range accepts the new version,
	// not that the consumer avoids what the change removes.
	Note string `json:"note,omitempty"`
	// Evidence lists the facts behind this consumer and their age, newest
	// first, bounded to MaxConsumerEvidence.
	Evidence []Evidence `json:"evidence,omitempty" doc:"The facts behind this consumer and their age: its targets, observed calls and unreachable findings, newest first"`
	// Effect is what an event does to this consumer; empty on a contract change.
	Effect string `json:"effect,omitempty" enum:"breaks,may-degrade,unknown" doc:"What the event does to this consumer: breaks (every hop of a path is a required dependency), may-degrade (every path holds an optional dependency) or unknown"`
	// Resources are the provisioning statuses of what this consumer asked a
	// removed service for, bounded to MaxConsumerResources.
	Resources      []fleet.ProvisioningStatus `json:"resources,omitempty" doc:"What this consumer asked the removed service for, one provisioning status per environment, at most 20"`
	ResourcesTotal int                        `json:"resourcesTotal,omitempty" doc:"How many provisioning statuses there are, including those beyond the bound"`
	// policyVerdict is the verdict of checking this consumer's contract
	// against the changed service's new policy, empty when it references no
	// policy of it. A failed check holds the change whatever its classification.
	policyVerdict string
	// rangeVerdict is the verdict of the consumer's range or configuration
	// reference before a policy verdict was folded in; empty on a row that
	// only references the policy.
	rangeVerdict string
}

// Release gate values (see [Result.Gate]).
const (
	// GateBlocked: a consumer that runs on a target the snapshot knows is shown
	// to break: the change is BREAKING and it does not accept the new version
	// or requires a guarantee it does not meet, or its contract fails the
	// changed service's new policy, whatever the classification.
	GateBlocked = "blocked"
	// GateUnverified: nothing is shown to block the change, but the answer
	// cannot clear it either: a BREAKING change whose answer is partial or whose
	// consumers' safety the evidence cannot show, a service that references the
	// changed policy and fails it, could not be checked or could not be
	// resolved, or an event that reaches any consumer.
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
	Owners             []string           `json:"owners,omitempty" doc:"The canonical owner keys of the affected consumers"`
	ChangedOwnerKey    string             `json:"changedOwnerKey,omitempty" doc:"The canonical owner key of the changed service"`
	Completeness       fleet.Completeness `json:"completeness"`
	Limitations        []fleet.Limitation `json:"limitations,omitempty"`
	// LimitationsTruncated reports that the snapshot and analysis produced more
	// limitations than the answer carries.
	LimitationsTruncated bool `json:"limitationsTruncated,omitempty"`
	// Sources is the health of the sources the snapshot was built from, bounded
	// like every fleet answer's meta; SourcesTruncated reports that more exist.
	Sources          []fleet.SourceState `json:"sources,omitempty"`
	SourcesTruncated bool                `json:"sourcesTruncated,omitempty"`
	// NewRevisionAdded reports that the changed service was in none of the fleet
	// sources, so the new revision itself was added to the snapshot to find the
	// services that depend on it. The changed service then has no targets.
	NewRevisionAdded bool `json:"newRevisionAdded,omitempty"`
	// Gate is the release decision `pacto impact` exits on: blocked (exit 1),
	// unverified (exit 2) or clear (exit 0). GateReason says why in plain words
	// and, when it is not clear, what to do next.
	Gate       string `json:"gate" enum:"blocked,unverified,clear"`
	GateReason string `json:"gateReason"`
	// ConsumerTallies partition Consumers by outcome and rank their owners, so a
	// reader states the blast radius from counts rather than by counting the list.
	ConsumerTallies ConsumerTallies `json:"consumerTallies"`
	// Event is the what-if this answer is about; nil for a contract change.
	Event *Event `json:"event,omitempty"`
	// Regression is the delivery evidence about a regressed revision,
	// bounded to MaxRegressionEvidence.
	Regression []RegressionEvidence `json:"regression,omitempty"`
	// Hidden counts the consumers this viewer may not see. They are never named.
	Hidden int `json:"hidden,omitempty" doc:"How many consumers this viewer may not see; they are never named"`
	// Templates are the golden paths that reference the changed service's
	// policy. A golden path runs nowhere, so it never holds the gate.
	Templates []string `json:"templates,omitempty" doc:"The service keys of the golden paths that reference the changed service's policy: they run nowhere, so they never hold the gate, but a service created from one checks against the new policy. Review each"`
	// observationSources are the observation sources an event answer read.
	observationSources []string
	// hiddenReferrers counts the services this viewer may not see that may
	// reference the changed service's policy: none of them was checked.
	hiddenReferrers int
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
// on: it reaches a consumer that is actually deployed and either broken by a
// breaking change (incompatible, or with an unmet guarantee) or failing the
// new policy (see [Result.blocks]). A consumer that is broken on paper but
// running nowhere the snapshot can see is not a release blocker.
func (r *Result) ReleaseBlocking() bool {
	return slices.ContainsFunc(r.Consumers, r.blocks)
}

// blockingConsumers names the consumers that make a BREAKING change release
// blocking through a range or a guarantee, and whether any of them is blocked
// by an unmet guarantee. Policy referrers are named by policyFailures.
func (r *Result) blockingConsumers() (active []string, unmet bool) {
	for _, c := range r.Consumers {
		if r.breaks(c) {
			active = append(active, c.Service)
			unmet = unmet || len(c.Unmet) > 0
		}
	}
	return active, unmet
}

// policyFailures names the consumers whose contract fails the changed
// service's new policy, and those of them that run on a known target.
func (r *Result) policyFailures() (failing, running []string) {
	for _, c := range r.Consumers {
		if c.policyVerdict == CompatibilityIncompatible {
			failing = append(failing, c.Service)
			if len(c.Targets) > 0 {
				running = append(running, c.Service)
			}
		}
	}
	return failing, running
}

// blocks reports whether one consumer makes the change release blocking: it
// runs on a known target and either the change is BREAKING and the consumer
// is broken (incompatible, or with an unmet guarantee), or its contract fails
// the new policy, whatever the classification. The gate and the product
// answer's per-consumer Blocking flag both read it, so they never disagree.
func (r *Result) blocks(c AffectedConsumer) bool {
	return r.breaks(c) || c.policyVerdict == CompatibilityIncompatible && len(c.Targets) > 0
}

// breaks reports whether a BREAKING change breaks a consumer that runs on a
// known target through its range or a guarantee it requires.
func (r *Result) breaks(c AffectedConsumer) bool {
	verdict := c.CompatibilityVerdict
	if c.policyVerdict != "" {
		// A policy verdict is named by policyFailures: a referrer has no range.
		verdict = c.rangeVerdict
	}
	return r.Classification == "BREAKING" &&
		(verdict == CompatibilityIncompatible || len(c.Unmet) > 0) && len(c.Targets) > 0
}

// gate derives the release gate from the finished answer. Only a BREAKING change
// can be held. It is blocked when an incompatible consumer runs somewhere. It
// is clear only when the answer is complete and no service depends on the
// changed one: a partial answer may be missing consumers, and a consumer that
// is incompatible on paper, has no usable range or only matches by range is a
// consumer nobody has shown to be safe.
//
// A consumer whose contract fails the new policy holds any change, whatever
// its classification: the diff cannot see a policy the provider tightened by
// adding one of its own, but the check against the referrer's document can.
func (r *Result) gate() (string, string) {
	if r.Event != nil {
		return r.eventGate()
	}
	if gate, why := r.blockingGate(); gate != "" {
		return gate, why
	}
	if why := r.policyUnchecked(); why != "" && r.Classification != "BREAKING" {
		return GateUnverified, why
	}
	switch r.Classification {
	case "BREAKING":
	case "POTENTIAL_BREAKING":
		return GateClear, "the change is POTENTIAL_BREAKING: the gate holds only BREAKING changes, so review the potentially breaking changes by hand"
	default:
		return GateClear, "the change is " + r.Classification
	}
	if hasCode(r.Limitations, "SERVICE_NOT_IN_FLEET") {
		return GateUnverified, "the change is BREAKING, but " + r.Service + " is not in the fleet snapshot, so its consumers are unknown. " +
			"Add the directory or registry that holds the services depending on it as a fleet source and run the analysis again"
	}
	if r.Completeness != fleet.CompletenessComplete {
		return GateUnverified, "the change is BREAKING and the answer is partial, so consumers Pacto cannot see may break" + r.unmetNote() + ". " +
			"Fix the sources named in limitations and run the analysis again"
	}
	if len(r.Consumers) > 0 {
		n := strconv.Itoa(len(r.Consumers)) + " consumers depend"
		if len(r.Consumers) == 1 {
			n = "1 consumer depends"
		}
		return GateUnverified, "the change is BREAKING and " + n + " on " + r.Service +
			", but none is shown to be both incompatible and running" + r.unmetNote() + ". Review each consumer, and add a source that knows where services run " +
			"(for example --target-state) to tell deployed impact apart"
	}
	return GateClear, "the change is BREAKING, but no service in the complete fleet snapshot depends on " + r.Service
}

// blockingGate is the gate when a consumer is shown to break: blocked when one
// runs on a known target, unverified when only policy referrers that run
// nowhere known fail the new policy, and "" otherwise.
func (r *Result) blockingGate() (string, string) {
	active, unmet := r.blockingConsumers()
	failing, running := r.policyFailures()
	var why []string
	if len(active) > 0 {
		accept := "do not accept " + r.Service + " " + r.NewVersion
		if unmet {
			accept += " or require a guarantee it does not meet"
		}
		if r.anyStale(active) {
			why = append(why, "breaking changes affect consumers that "+accept+": "+r.whereRun(active))
		} else {
			why = append(why, "breaking changes affect active consumers, which run on known targets and "+accept+": "+strings.Join(active, ", "))
		}
	}
	if len(running) > 0 {
		if r.anyStale(running) {
			why = append(why, r.referrersFail(running)+": "+r.whereRun(running))
		} else {
			why = append(why, r.referrersFail(running)+" and "+plural(len(running), "runs", "run")+" on known targets: "+strings.Join(running, ", "))
		}
	}
	switch {
	case r.ReleaseBlocking():
		return GateBlocked, strings.Join(why, "; ")
	case len(failing) > 0:
		return GateUnverified, r.referrersFail(failing) + ": " + strings.Join(failing, ", ") + ". Review each"
	}
	return "", ""
}

// policyUnchecked says why a change the diff does not call BREAKING still
// cannot clear: services reference the changed one's policy and Pacto could
// not check them, or cannot tell which revision they check against. The diff
// can miss a policy the provider tightened, so an unchecked referrer is never
// taken as safe. It is "" when every referrer was checked.
func (r *Result) policyUnchecked() string {
	var why []string
	n := 0
	for _, c := range r.Consumers {
		if c.policyVerdict == CompatibilityUnknown {
			n++
		}
	}
	if n > 0 {
		why = append(why, strconv.Itoa(n)+" "+plural(n, "service that references", "services that reference")+" "+r.Service+
			" could not be checked against its new policy")
	}
	if h := r.hiddenReferrers; h > 0 {
		why = append(why, strconv.Itoa(h)+" "+plural(h, "service you may not see", "services you may not see")+" may reference "+r.Service+
			", so "+plural(h, "it", "they")+" could not be checked against its new policy")
	}
	if hasCode(r.Limitations, "POLICY_REF_CONSUMERS_UNRESOLVED") {
		why = append(why, "policy references that name "+r.Service+" are not digest-pinned or locked, so Pacto cannot tell which revision they check against")
	}
	if len(why) == 0 {
		return ""
	}
	return "the change is " + r.Classification + ", but " + strings.Join(why, " and ") +
		". The diff can miss a policy the provider tightened, so review each service that references it"
}

// anyStale reports whether any named consumer has target evidence older than
// the recent window (Decision 4).
func (r *Result) anyStale(names []string) bool {
	fresh, _ := r.splitStale(names)
	return len(fresh) < len(names)
}

// splitStale splits named consumers into those whose every target is fresh and
// those with a stale target.
func (r *Result) splitStale(names []string) (fresh, stale []string) {
	staleSet := map[string]bool{}
	for _, c := range r.Consumers {
		if slices.ContainsFunc(c.Evidence, func(e Evidence) bool { return e.Kind == EvidenceTarget && e.Stale }) {
			staleSet[c.Service] = true
		}
	}
	for _, n := range names {
		if staleSet[n] {
			stale = append(stale, n)
		} else {
			fresh = append(fresh, n)
		}
	}
	return fresh, stale
}

// whereRun says where named consumers run, saying only of fresh ones that they
// run there: "a runs on a known target; b was last seen on a target more than
// 24h before asOf", and of one with no such time that it has no current
// target evidence.
func (r *Result) whereRun(names []string) string {
	fresh, stale := r.splitStale(names)
	var parts []string
	if len(fresh) > 0 {
		parts = append(parts, strings.Join(fresh, ", ")+" "+plural(len(fresh), "runs on a known target", "run on known targets"))
	}
	old, untimed := r.splitStaleCause(stale)
	if len(old) > 0 {
		parts = append(parts, strings.Join(old, ", ")+" "+plural(len(old), "was", "were")+" last seen on a target more than 24h before asOf")
	}
	if len(untimed) > 0 {
		parts = append(parts, strings.Join(untimed, ", ")+" "+plural(len(untimed), "has", "have")+" "+noCurrentTarget)
	}
	return strings.Join(parts, "; ")
}

// noCurrentTarget says why a consumer's target evidence is stale when no
// time older than the window shows it.
const noCurrentTarget = "no current target evidence: a target with no evidence time, missing from the snapshot or marked stale by its source"

// splitStaleCause splits named consumers with stale target evidence into
// those whose every stale target carries a time older than the window, which
// may be said to be last seen then, and the rest, of which no time is known.
func (r *Result) splitStaleCause(names []string) (old, untimed []string) {
	cutoff := r.AsOf.Add(-fleet.RecentEvidenceWindow)
	byName := map[string]bool{}
	for _, c := range r.Consumers {
		byName[c.Service] = !slices.ContainsFunc(c.Evidence, func(e Evidence) bool {
			return e.Kind == EvidenceTarget && e.Stale && (e.At == nil || !e.At.Before(cutoff))
		})
	}
	for _, n := range names {
		if byName[n] {
			old = append(old, n)
		} else {
			untimed = append(untimed, n)
		}
	}
	return old, untimed
}

// referrersFail says that n services that reference the changed one fail its
// new policy.
func (r *Result) referrersFail(names []string) string {
	n := len(names)
	return strconv.Itoa(n) + " " + plural(n, "service that references", "services that reference") + " " + r.Service + " " +
		plural(n, "fails", "fail") + " its new policy"
}

// plural picks one or many by n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// unmetNote names the consumers the new revision refuses a guarantee they
// require, as a sentence to add to a gate reason; it is empty when none does.
func (r *Result) unmetNote() string {
	var names []string
	for _, c := range r.Consumers {
		if len(c.Unmet) > 0 {
			names = append(names, c.Service)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return ". " + r.Service + " " + r.NewVersion + " does not meet a guarantee required by " + strings.Join(names, ", ")
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
		// With no range the verdict came from checking configuration values,
		// and configRefVerdict already wrote its note.
		if c.CompatibilityVerdict != CompatibilityCompatible || c.Compatibility == "" {
			continue
		}
		note := "compatible only means the range " + c.Compatibility + " accepts " + r.NewVersion +
			"; this change is BREAKING, so check that " + c.Service + " does not use what it removes or changes"
		if fleet.CheckRange(c.Compatibility, openRangeProbe) == fleet.RangeSatisfied {
			note = "the range " + c.Compatibility + " has no upper bound, so " + note
		}
		// A configuration-reference check that passed keeps its note.
		if c.Note != "" {
			note = c.Note + "; " + note
		}
		c.Note = note
	}
}

// Analyze compares old→new and projects the change onto the operational graph.
// The answer always carries its release gate (see [Result.Gate]).
func Analyze(ctx context.Context, old, new *contract.Contract, oldFS, newFS fs.FS, snap *fleet.FleetSnapshot, opts Options) *Result {
	var res *Result
	if opts.Event != nil {
		res = analyzeEvent(snap, opts)
	} else {
		res = analyze(ctx, old, new, oldFS, newFS, snap, opts)
		res.noteRangeOnlyMatches()
	}
	res.boundEnvelope()
	res.Gate, res.GateReason = res.gate()
	res.tally()
	return res
}

func analyze(ctx context.Context, old, new *contract.Contract, oldFS, newFS fs.FS, snap *fleet.FleetSnapshot, opts Options) *Result {
	d := diff.Compare(ctx, old, new, oldFS, newFS)
	svc := new.Service.Name
	q := fleet.NewQuery(snap)
	meta := q.ProductMeta()

	res := &Result{
		SchemaVersion:    SchemaVersion,
		SnapshotID:       snap.SnapshotID,
		AsOf:             snap.GeneratedAt,
		Service:          svc,
		OldVersion:       old.Service.Version,
		NewVersion:       new.Service.Version,
		Classification:   d.Classification.String(),
		Completeness:     snap.Completeness,
		Limitations:      append([]fleet.Limitation(nil), snap.Limitations...),
		Consumers:        []AffectedConsumer{},
		Sources:          meta.Sources,
		SourcesTruncated: meta.SourcesTruncated,
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

	graph := res.dependents(snap, q, fleet.NewServiceKeyDomain(opts.Domain, svc), true)
	if graph == nil {
		return res
	}
	changed := graph.Root
	// Resolve observed (OTel) endpoints to unique domain-qualified keys ONCE;
	// unresolved/ambiguous ones become explicit limitations and never corroborate.
	resolvedObserved, obsLims := resolveObservedEdges(snap, opts.ObservedEdges, opts.IncludeObserved)
	res.Limitations = append(res.Limitations, obsLims...)
	if len(obsLims) > 0 {
		// An observed caller Pacto could not name may be a consumer it cannot list.
		res.Completeness = fleet.CompletenessPartial
	}
	// The snapshot may already carry observed relationships folded in by Build (the
	// real observation pipeline). They are pre-resolved to domain-qualified keys, so
	// they join the ad-hoc --traces edges directly when observed evidence is opted in.
	if opts.IncludeObserved {
		resolvedObserved = append(resolvedObserved, snapshotObservedEdges(snap)...)
		// Only when no edge was supplied at all: unresolved trace edges already
		// carry their own limitation, and traces did see calls.
		if len(opts.ObservedEdges) == 0 && len(resolvedObserved) == 0 {
			res.Limitations = append(res.Limitations, fleet.Limitation{Code: "OBSERVED_EVIDENCE_ABSENT", Source: limitationSource,
				Message: "observed evidence was asked for, but no observation source and no traces file saw any call, so consumers that call " +
					string(changed) + " without declaring it cannot be found"})
		}
	}
	newBundle := &contract.Bundle{Contract: new, FS: newFS}
	for _, cn := range unionConsumers(graph, changed, resolvedObserved) {
		c := consumerImpact(snap, changed, cn, newBundle, resolvedObserved)
		c.Unmet = unmetGuarantees(snap, cn.key, changed, new)
		res.Consumers = append(res.Consumers, c)
	}
	res.addPolicyReferrers(snap, changed, newBundle, resolvedObserved)
	if opts.IncludeObserved {
		res.Consumers = append(res.Consumers, uncontractedCallers(snap, changed)...)
	}
	res.collectOwnersAndTargets(snap, changed)
	res.noteUnlisted(snap, q, changed)
	res.hiddenReferrers = hiddenReferrers(snap.Services[changed], newBundle)
	res.noteStaleConsumers()
	return res
}

// hiddenReferrers counts the hidden services that may reference s's policy:
// its hidden policy referrers and, when the new revision publishes a policy,
// its hidden dependents too, since one of them may also reference it (the
// restricted snapshot counts such a service once, as a dependent).
func hiddenReferrers(s *fleet.ServiceRecord, provider *contract.Bundle) int {
	if s == nil {
		return 0
	}
	n := s.HiddenPolicyReferrers
	if publishesPolicy(provider) {
		n += s.HiddenDependents
	}
	return n
}

// publishesPolicy reports whether the bundle declares a policy with its own
// schema (a ref only references another's) or ships the policy schema file.
func publishesPolicy(b *contract.Bundle) bool {
	if b.Contract != nil && slices.ContainsFunc(b.Contract.Policies, func(p contract.Policy) bool { return p.Schema != "" }) {
		return true
	}
	if b.FS == nil {
		return false
	}
	_, err := fs.Stat(b.FS, validation.PolicySchemaPath)
	return err == nil
}

// uncontractedCallers are the runtime callers of changed no contract
// describes, each citing the sources that saw it call.
func uncontractedCallers(snap *fleet.FleetSnapshot, changed fleet.ServiceKey) []AffectedConsumer {
	var out []AffectedConsumer
	for _, u := range snap.UncontractedCallers {
		if u.ToService != changed {
			continue
		}
		c := AffectedConsumer{
			Service: u.Caller, Depth: 1, Direct: true, Path: []string{u.Caller, string(changed)},
			CompatibilityVerdict: CompatibilityUnknown, Provenance: fleet.ProvenanceObserved,
			Confidence: ConfidenceObserved, Uncontracted: true, Via: []string{fleet.ProvenanceObserved},
		}
		if u.Ambiguous {
			c.Note = "its name matches several services in different domains, so Pacto cannot tell which one calls " + string(changed)
		}
		for _, src := range u.Sources {
			c.Evidence = append(c.Evidence, Evidence{Kind: EvidenceObservedCall, Source: src, Provenance: fleet.ProvenanceObserved,
				Message: "calls seen, no observation window"})
		}
		c.Evidence = boundEvidence(c.Evidence)
		out = append(out, c)
	}
	return out
}

// noteUnlisted sets what the answer counts or lists outside Consumers: the
// consumers this viewer may not see and, where policy referrers count (a
// contract change or a removal), the golden paths that reference changed.
func (r *Result) noteUnlisted(snap *fleet.FleetSnapshot, q *fleet.Query, changed fleet.ServiceKey) {
	policy := r.Event == nil || r.Event.Kind == EventRemoved
	if s := snap.Services[changed]; s != nil {
		r.Hidden = s.HiddenDependents
		if policy {
			r.Hidden += s.HiddenPolicyReferrers
		}
	}
	if policy {
		for _, k := range q.TemplatesReferencing(changed) {
			r.Templates = append(r.Templates, string(k))
		}
	}
}

// dependents walks every service that depends on key, transitively, and
// notes the unresolved references that may name it (policy ones withPolicy).
// It is nil, with the answer partial, when key is not in the fleet. Contract
// and event answers both walk through it, so they list the same consumers.
func (r *Result) dependents(snap *fleet.FleetSnapshot, q *fleet.Query, key fleet.ServiceKey, withPolicy bool) *fleet.GraphResult {
	graph, err := q.Graph(fleet.GraphQuery{Service: string(key), Direction: fleet.DirectionDependents, Transitive: true})
	if err != nil {
		// The service is not in the graph, so NO consumer can be ruled in or
		// out. Reporting "complete" here would let an empty consumer list read
		// as "nothing breaks" when it means "I could not tell".
		r.Completeness = fleet.CompletenessPartial
		r.Limitations = append(r.Limitations, fleet.Limitation{
			Code: "SERVICE_NOT_IN_FLEET", Source: limitationSource,
			Message: "the service is not present in the operational graph; consumers cannot be determined",
		})
		return nil
	}
	if graph.Truncated {
		// The traversal stopped at the node cap, so the consumer list is a
		// deterministic PREFIX of the reachable dependents. Leaving
		// completeness alone would let the gate clear on the strength of
		// whichever consumers happened to sort first.
		r.Completeness = fleet.CompletenessPartial
		r.Limitations = append(r.Limitations, fleet.Limitation{
			Code: "GRAPH_TRUNCATED", Source: limitationSource,
			Message: "the dependents traversal hit the " + strconv.Itoa(fleet.MaxGraphNodes) +
				"-node cap; the consumer list is a prefix of the blast radius, not the whole of it",
		})
	}
	r.noteUnresolvedRefs(snap, graph.Root, withPolicy)
	return graph
}

// noteStaleConsumers raises one counted limitation when the evidence of any
// consumer is older than the recent window. Completeness is unchanged: the
// consumer set is complete, where those consumers run is what is uncertain.
func (r *Result) noteStaleConsumers() {
	n := 0
	for _, c := range r.Consumers {
		if slices.ContainsFunc(c.Evidence, func(e Evidence) bool { return e.Stale }) {
			n++
		}
	}
	if n == 0 {
		return
	}
	r.Limitations = append(r.Limitations, fleet.Limitation{Code: "CONSUMER_EVIDENCE_STALE", Source: limitationSource,
		Message: strconv.Itoa(n) + " " + plural(n, "consumer has", "consumers have") +
			" target or traffic evidence older than 24h before asOf, so " + plural(n, "its", "their") + " confidence is unknown"})
}

// collectOwnersAndTargets sets Owners to the consumers' owners and
// ActiveTargets to the targets of the changed service and of its consumers.
// Owners are canonical keys: display labels merge distinct owners.
func (r *Result) collectOwnersAndTargets(snap *fleet.FleetSnapshot, changed fleet.ServiceKey) {
	owners := map[string]bool{}
	targets := map[string]bool{}
	for _, tk := range serviceTargets(snap, changed) {
		targets[tk] = true
	}
	if s := snap.Services[changed]; s != nil {
		r.ChangedOwnerKey = s.Owner.KeyString()
	}
	for _, c := range r.Consumers {
		if c.OwnerKey != "" {
			owners[c.OwnerKey] = true
		}
		for _, tk := range c.Targets {
			targets[tk] = true
		}
	}
	r.Owners = sortedKeys(owners)
	r.ActiveTargets = sortedKeys(targets)
}

// noteUnresolvedRefs makes the answer partial when configuration references,
// and with withPolicy policy references, may name changed but did not resolve:
// they may consume or check against it, at a revision Pacto cannot tell.
func (r *Result) noteUnresolvedRefs(snap *fleet.FleetSnapshot, changed fleet.ServiceKey, withPolicy bool) {
	note := func(code, relType, what, verb string) {
		n := unresolvedRefConsumers(snap, changed, relType)
		if n == 0 {
			return
		}
		r.Completeness = fleet.CompletenessPartial
		r.Limitations = append(r.Limitations, fleet.Limitation{Code: code, Source: limitationSource,
			Message: strconv.Itoa(n) + " " + what + " name " + string(changed) +
				" but are not digest-pinned or locked, so Pacto cannot tell which revision they " + verb})
	}
	note("CONFIG_REF_CONSUMERS_UNRESOLVED", fleet.RelationshipConfigRef, "configuration references", "consume")
	if withPolicy {
		note("POLICY_REF_CONSUMERS_UNRESOLVED", fleet.RelationshipPolicyRef, "policy references", "check against")
	}
}

// consumerNode identifies one affected consumer and its position in the graph.
type consumerNode struct {
	key   fleet.ServiceKey
	name  string
	depth int
	path  []fleet.ServiceKey
}

// resolvedObservedEdge is an observed caller→provider edge whose endpoints have
// been resolved to UNIQUE domain-qualified fleet ServiceKeys. A stale edge was
// last seen more than the recent window before the snapshot's time: it still
// names its caller as a consumer, but it no longer corroborates.
type resolvedObservedEdge struct {
	consumer, provider fleet.ServiceKey
	source             string
	count              int
	lastSeen           *time.Time
	stale              bool
}

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
			out = append(out, resolvedObservedEdge{consumer: ck, provider: pk, source: tracesFileSource, count: e.Count,
				lastSeen: copyTime(e.LastSeen), stale: e.LastSeen != nil && staleAt(snap, e.LastSeen)})
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
		// Observed runtime calls are dependency edges; a reference never makes one.
		if r.Type == fleet.RelationshipDependency && r.Provenance == fleet.ProvenanceObserved && r.Resolved {
			out = append(out, resolvedObservedEdge{consumer: r.FromService, provider: r.ToService, source: observedSource(r),
				count: r.ObservedCount, lastSeen: copyTime(r.LastSeen), stale: r.LastSeen != nil && staleAt(snap, r.LastSeen)})
		}
	}
	return out
}

// observedSource names the sources that saw an observed relationship: its
// sole source, else every source of its per-source breakdown.
func observedSource(r fleet.Relationship) string {
	if r.Source != "" {
		return r.Source
	}
	ids := make([]string, 0, len(r.ObservedSources))
	for _, s := range r.ObservedSources {
		ids = append(ids, s.Source)
	}
	return strings.Join(ids, ", ")
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

// consumerImpact builds the affected-consumer record for one consumer of the
// changed service's new revision.
func consumerImpact(snap *fleet.FleetSnapshot, changed fleet.ServiceKey, node consumerNode, newBundle *contract.Bundle, observedEdges []resolvedObservedEdge) AffectedConsumer {
	newVersion := newBundle.Contract.Service.Version
	c, stale := consumerBase(snap, node.key, changed, observedEdges)
	c.Service, c.Depth, c.Direct, c.Path = node.name, node.depth, node.depth == 1, pathConsumerFirst(node.path)

	rel, hasDeclared, observed, seen, refs := edgeEvidence(snap, node.key, changed, newVersion, observedEdges)
	// The observedEdges slice is empty unless the caller opted in and the endpoints
	// resolved to unique domain-qualified keys, so `observed` is already gated.
	// Only a fresh observed call corroborates; a stale one still makes the
	// caller a consumer (seen) and lowers its confidence below.
	observedCounted := observed
	c.Required = rel.Required
	c.Compatibility = rel.Compatibility
	c.CompatibilityVerdict = compatibilityVerdict(rel.Compatibility, newVersion, hasDeclared)
	if hasDeclared {
		c.Via = append(c.Via, fleet.RelationshipDependency)
	}
	if len(refs) > 0 {
		c.Via = append(c.Via, fleet.RelationshipConfigRef)
		// Pacto judges the consumer's configuration-reference values against
		// the new revision's schema itself; with a dependency too, the check
		// that accepts the change least speaks, so a range match never masks
		// values the new schema rejects.
		v, note := configRefVerdict(snap, refs, newBundle)
		if !hasDeclared || verdictRank(v) > verdictRank(c.CompatibilityVerdict) {
			c.CompatibilityVerdict = v
		}
		c.Note, hasDeclared = note, true
	}
	if seen {
		c.Via = append(c.Via, fleet.ProvenanceObserved)
	}
	c.Provenance = provenance(hasDeclared, seen)
	c.Confidence = confidence(node.depth, hasDeclared, rel.Compatibility != "", observedCounted)
	if stale {
		c.Confidence = ConfidenceUnknown
	}
	return c
}

// consumerBase is what every kind of consumer of changed says about the
// service key: its name, domain, owner, status, targets and the evidence
// behind it. Each consumer builder starts from it, so they never disagree
// about who a consumer is or where it runs. stale reports evidence older than
// the recent window: the builder sets the confidence to unknown after its own
// grading, never the reverse.
func consumerBase(snap *fleet.FleetSnapshot, key, changed fleet.ServiceKey, observed []resolvedObservedEdge) (AffectedConsumer, bool) {
	domain, name := fleet.ParseServiceKey(key)
	c := AffectedConsumer{Service: name, Domain: domain, Path: []string{string(key), string(changed)}}
	var stale bool
	c.Evidence, stale = consumerEvidence(snap, key, changed, observed)
	if s := snap.Services[key]; s != nil {
		c.Owner = s.Owner.DisplayString()
		c.OwnerKey = s.Owner.KeyString()
		c.Status = s.Status
		for _, tk := range s.Targets {
			c.Targets = append(c.Targets, string(tk))
		}
		sort.Strings(c.Targets)
	}
	return c, stale
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
//
// refs are the consumer's configuration-reference edges to changed, whose
// values the caller checks beside the dependency edge.
//
// Observed relationships are never counted here: [fleet.ConsumesEdge] takes
// declared edges only. Build folds observed relationships into the snapshot
// unconditionally, so reading corroboration out of them here would bypass the
// opt-in; when the caller DOES opt in, snapshotObservedEdges supplies the same
// edges through observedEdges.
func edgeEvidence(snap *fleet.FleetSnapshot, consumer, changed fleet.ServiceKey, newVersion string, observedEdges []resolvedObservedEdge) (rel fleet.Relationship, declared, observed, seen bool, refs []fleet.Relationship) {
	for i := range snap.Relationships {
		r := snap.Relationships[i]
		if !fleet.ConsumesEdge(r) || r.FromService != consumer || r.ToService != changed {
			continue
		}
		if r.Type == fleet.RelationshipConfigRef {
			refs = append(refs, r)
			continue
		}
		if !declared || verdictRank(compatibilityVerdict(r.Compatibility, newVersion, true)) > verdictRank(compatibilityVerdict(rel.Compatibility, newVersion, true)) {
			rel = r
		}
		declared = true
	}
	for _, e := range observedEdges {
		if e.consumer == consumer && e.provider == changed {
			seen = true
			observed = observed || !e.stale
		}
	}
	return rel, declared, observed, seen, refs
}

// unmetGuarantees lists what the consumer's declared dependencies on the
// changed service require that the new revision does not meet, once each,
// over every revision of the consumer the snapshot holds.
func unmetGuarantees(snap *fleet.FleetSnapshot, consumer, changed fleet.ServiceKey, newC *contract.Contract) []contract.Mismatch {
	var out []contract.Mismatch
	seen := map[contract.Mismatch]bool{}
	for _, r := range snap.Relationships {
		rev := snap.Revisions[r.FromRevision]
		// Dependencies only: guarantee requirements live on dependencies.
		if r.Type != fleet.RelationshipDependency || r.Provenance != fleet.ProvenanceDeclared || r.FromService != consumer || r.ToService != changed || rev == nil || rev.Contract == nil {
			continue
		}
		for _, dep := range rev.Contract.Dependencies {
			if dep.Name != r.To {
				continue
			}
			for _, m := range contract.Unmet(rev.Service, dep, newC) {
				if !seen[m] {
					seen[m] = true
					out = append(out, m)
				}
			}
		}
	}
	return out
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
