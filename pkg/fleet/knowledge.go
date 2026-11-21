package fleet

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// categoryKnowledge is the attention category of a gap in what a service's
// contract says, of a service whose contract Pacto cannot read, or of an
// inferred service a contract took over (see attentionInferredReplaced).
const categoryKnowledge = "knowledge"

// attentionInferredReplaced is the attention code of an inferred service a
// declared service replaced: through a Pacto target within
// RecentEvidenceWindow, through a declaration while it holds.
const attentionInferredReplaced = "INFERRED_REPLACED"

// Knowledge gap kinds, the values of [EntityFilter.Gap]. GapUnknown keeps the
// services whose knowledge Pacto cannot judge (see [KnowledgeTally.Unknown]).
const (
	GapNoSummary            = "no-summary"
	GapNoDomain             = "no-domain"
	GapNoInterfaceDocs      = "no-interface-docs"
	GapNoReadiness          = "no-readiness"
	GapNotDeployed          = "not-deployed"
	GapUnverifiedCapability = "unverified-capability"
	GapUnknown              = "unknown"
)

// gapKinds is every gap kind in its fixed display order.
var gapKinds = []string{GapNoSummary, GapNoDomain, GapNoInterfaceDocs, GapNoReadiness, GapNotDeployed, GapUnverifiedCapability}

func validGap(s string) bool { return s == GapUnknown || slices.Contains(gapKinds, s) }

// GapFilterValues is every value [EntityFilter.Gap] accepts, in display order.
func GapFilterValues() []string { return append(slices.Clone(gapKinds), GapUnknown) }

// The causes of an unknown service's knowledge, first match wins.
const (
	causeContractUnreadable  = "CONTRACT_UNREADABLE"
	causeInterfaceDocsUnread = "INTERFACE_DOCS_UNREAD"
	causeSummaryUnknown      = "SUMMARY_UNKNOWN"
	causeDomainUnknown       = "DOMAIN_UNKNOWN"
	causePlacementUnknown    = "PLACEMENT_UNKNOWN"
)

// KnowledgeTally partitions the services that are no golden path by what their
// contracts leave out: Described + WithGaps + Unknown == Services. A service is
// Described only when its current contract and the OpenAPI documents it names
// were read (Pacto reads no gRPC or AsyncAPI document) and nothing is missing;
// Unknown when Pacto cannot tell (an unreadable contract or OpenAPI document,
// conflicting summaries or business domains, or placement it cannot see),
// never folded into Described. Gaps counts services per kind: a service with two gaps counts
// in two kinds and once in WithGaps. ContractCompleteness is the checklist
// figure over the same services; it is set by finish once every service was added.
type KnowledgeTally struct {
	Services             int               `json:"services" doc:"Services that are no golden path; the whole the other three partition"`
	Described            int               `json:"described" doc:"Services whose contract and OpenAPI interface documents were read and leave nothing out; gRPC and AsyncAPI documents are never read"`
	WithGaps             int               `json:"withGaps" doc:"Services with at least one knowledge gap"`
	Unknown              int               `json:"unknown" doc:"Services whose knowledge Pacto cannot judge: an unreadable contract or OpenAPI document, conflicting summaries or business domains, or placement it cannot see"`
	Gaps                 KnowledgeGaps     `json:"gaps"`
	ContractCompleteness FleetCompleteness `json:"contractCompleteness"`

	pctSum  int            // sum of the judged services' percentages
	missing map[string]int // services per section left undeclared (missing or inferred only)
}

// KnowledgeGaps counts services per gap kind; the kinds overlap.
type KnowledgeGaps struct {
	NoSummary            int `json:"noSummary" doc:"Services whose contract declares no summary"`
	NoDomain             int `json:"noDomain" doc:"Services with no business domain"`
	NoInterfaceDocs      int `json:"noInterfaceDocs" doc:"Services whose current revision declares an interface with no document"`
	NoReadiness          int `json:"noReadiness" doc:"Services whose current revision carries no readiness assessment"`
	NotDeployed          int `json:"notDeployed" doc:"Services that run nowhere, counted only when at least one source reports operational targets and every such source is complete"`
	UnverifiedCapability int `json:"unverifiedCapability" doc:"Services whose current revision declares an extension capability no evaluator verifies"`
}

// FleetCompleteness is the contract completeness of a set of services: the
// mean percentage over the services Pacto can judge, how many it cannot, how
// many declare every section that applies to them and the sections most often
// left undeclared. Judged + Unknown is every service counted.
type FleetCompleteness struct {
	PercentComplete *int           `json:"percentComplete,omitempty" doc:"Mean contract completeness over the judged services, rounded down; absent when no service is judged"`
	Judged          int            `json:"judged" doc:"Services whose contract completeness Pacto can judge"`
	Unknown         int            `json:"unknown" doc:"Services whose contract Pacto cannot read, so no section can be judged; never counted as complete"`
	FullyDescribed  int            `json:"fullyDescribed" doc:"Judged services whose contract declares every section that applies to them"`
	MostMissing     []SectionCount `json:"mostMissing" doc:"The three sections most often left undeclared (missing or inferred only), most first"`
}

// String prints the percentage instead of its address, so two equal figures
// print alike.
func (c FleetCompleteness) String() string {
	pct := "none"
	if c.PercentComplete != nil {
		pct = strconv.Itoa(*c.PercentComplete)
	}
	return fmt.Sprintf("{PercentComplete:%s Judged:%d Unknown:%d FullyDescribed:%d MostMissing:%v}", pct, c.Judged, c.Unknown, c.FullyDescribed, c.MostMissing)
}

// SectionCount is how many services leave a completeness section undeclared.
type SectionCount struct {
	Section string `json:"section" enum:"interfaces,configuration,dependencies,owner,summary,domain,runtime,readiness"`
	Count   int    `json:"count"`
}

// maxMostMissing bounds FleetCompleteness.MostMissing.
const maxMostMissing = 3

// addService tallies one service that is no golden path into the gap kinds
// and the contract completeness figure.
func (k *KnowledgeTally) addService(q *Query, s *ServiceRecord) {
	k.add(q.serviceKnowledge(s))
	k.addCompleteness(q.serviceCompleteness(s))
}

func (k *KnowledgeTally) add(gaps []string, cause string) {
	k.Services++
	switch {
	case cause != "":
		k.Unknown++
		return
	case len(gaps) == 0:
		k.Described++
		return
	}
	k.WithGaps++
	for _, g := range gaps {
		switch g {
		case GapNoSummary:
			k.Gaps.NoSummary++
		case GapNoDomain:
			k.Gaps.NoDomain++
		case GapNoInterfaceDocs:
			k.Gaps.NoInterfaceDocs++
		case GapNoReadiness:
			k.Gaps.NoReadiness++
		case GapNotDeployed:
			k.Gaps.NotDeployed++
		default:
			k.Gaps.UnverifiedCapability++
		}
	}
}

func (k *KnowledgeTally) addCompleteness(sc *ServiceCompleteness) {
	c := &k.ContractCompleteness
	if sc.Percent == nil {
		c.Unknown++
		return
	}
	c.Judged++
	k.pctSum += *sc.Percent
	c.FullyDescribed += b2i(*sc.Percent == 100)
	for _, sec := range sc.Sections {
		if sec.State == contract.CompletenessMissing || sec.State == contract.CompletenessInferred {
			if k.missing == nil {
				k.missing = map[string]int{}
			}
			k.missing[sec.Section]++
		}
	}
}

// finish sets the figures add cannot keep running: the mean percentage and the
// most missing sections. Every caller calls it after its last add.
func (k *KnowledgeTally) finish() {
	c := &k.ContractCompleteness
	if c.Judged > 0 {
		c.PercentComplete = new(k.pctSum / c.Judged)
	}
	c.MostMissing = []SectionCount{}
	for _, sec := range contract.SectionNames() {
		if n := k.missing[sec]; n > 0 {
			c.MostMissing = append(c.MostMissing, SectionCount{sec, n})
		}
	}
	slices.SortStableFunc(c.MostMissing, func(a, b SectionCount) int { return b.Count - a.Count })
	c.MostMissing = c.MostMissing[:min(len(c.MostMissing), maxMostMissing)]
}

// EvaluationCoverage sums the assertion coverage the sources report over the
// target population. NotReporting counts the targets whose source reported no
// coverage at all, never folded into zero.
type EvaluationCoverage struct {
	Evaluated    int `json:"evaluated" doc:"Assertions evaluated, summed over the targets that report coverage"`
	Required     int `json:"required" doc:"Assertions required, summed over the targets that report coverage"`
	Reporting    int `json:"reporting" doc:"Targets whose source reported coverage"`
	NotReporting int `json:"notReporting" doc:"Targets whose source reported no coverage"`
}

func (c *EvaluationCoverage) add(t *TargetRecord) {
	if t.Coverage == nil {
		c.NotReporting++
		return
	}
	c.Reporting++
	c.Evaluated += t.Coverage.Evaluated
	c.Required += t.Coverage.Required
}

// currentRevision is the service's current revision record: the newest in use,
// else the newest known.
func (q *Query) currentRevision(s *ServiceRecord) *ContractRevision {
	inUse := map[RevisionKey]bool{}
	for _, tk := range s.Targets {
		inUse[q.snap.Targets[tk].ContractRevision] = true
	}
	var active, all []*ContractRevision
	for _, rk := range s.Revisions {
		if r := q.snap.Revisions[rk]; r != nil {
			all = append(all, r)
			if inUse[rk] {
				active = append(active, r)
			}
		}
	}
	return newestInUse(active, all)
}

// notDeployed reports whether the service runs nowhere and whether Pacto can
// know: a service with a visible target runs somewhere, and one with none runs
// nowhere only when the view hid none of its targets, at least one source
// reports targets and every such source is complete. Every not-deployed count calls it.
func (q *Query) notDeployed(s *ServiceRecord) (yes, known bool) {
	if len(s.Targets) > 0 {
		return false, true
	}
	known = q.placementKnown(s)
	return known, known
}

// placementKnown reports whether Pacto can tell where s runs: the view hid
// none of its targets, at least one source reports targets and every such
// source is complete. Only then is a missing target proof that s does not run
// there.
func (q *Query) placementKnown(s *ServiceRecord) bool {
	return !q.snap.targetsHidden[s.Key] && hasTargetSource(q.snap) && targetPlacementKnown(q.snap)
}

// fleetCompleteness is the contract completeness of every visible service
// that is no golden path and no inferred service, the Overview's own figure.
func (q *Query) fleetCompleteness() FleetCompleteness {
	var k KnowledgeTally
	for _, s := range q.snap.Services {
		if !s.Template && !s.Inferred() {
			k.addCompleteness(q.serviceCompleteness(s))
		}
	}
	k.finish()
	return k.ContractCompleteness
}

// knowledgeCauses are the causes of an unknown knowledge state a readable
// current revision can still have, in first-match order.
var knowledgeCauses = []string{causeInterfaceDocsUnread, causeSummaryUnknown, causeDomainUnknown, causePlacementUnknown}

// knowledgeFacts reports each gap kind and unknown cause that holds for a
// service whose current revision cur is readable.
func (q *Query) knowledgeFacts(s *ServiceRecord, cur *ContractRevision) map[string]bool {
	runsNowhere, known := q.notDeployed(s)
	return map[string]bool{
		causeInterfaceDocsUnread: slices.ContainsFunc(cur.Contract.Interfaces, func(i contract.Interface) bool {
			return i.Type == contract.InterfaceTypeOpenAPI && i.Ref != "" && !slices.Contains(cur.SpecsRead, i.Name)
		}),
		causeSummaryUnknown:     s.SummaryUnknown,
		causeDomainUnknown:      s.BusinessDomainUnknown,
		causePlacementUnknown:   !known,
		GapNoSummary:            s.Summary == "",
		GapNoDomain:             s.BusinessDomain == "" && !s.BusinessDomainUnknown,
		GapNoInterfaceDocs:      slices.ContainsFunc(cur.Contract.Interfaces, func(i contract.Interface) bool { return i.Ref == "" }),
		GapNoReadiness:          cur.Readiness == nil,
		GapNotDeployed:          runsNowhere,
		GapUnverifiedCapability: slices.ContainsFunc(cur.Contract.Capabilities, func(c contract.Capability) bool { return c.Type == contract.CapabilityExtension }),
	}
}

// contractUnread reports whether Pacto cannot read the service's current
// contract (or its ownership), so nothing about the contract can be judged.
func (q *Query) contractUnread(s *ServiceRecord, cur *ContractRevision) bool {
	return q.ownershipUnknown(s) || cur == nil || cur.Contract == nil
}

// serviceKnowledge returns the service's gap kinds in the fixed order, or, when
// Pacto cannot judge them, no gaps and the first cause.
func (q *Query) serviceKnowledge(s *ServiceRecord) (gaps []string, cause string) {
	cur := q.currentRevision(s)
	if q.contractUnread(s, cur) {
		return nil, causeContractUnreadable
	}
	has := q.knowledgeFacts(s, cur)
	for _, c := range knowledgeCauses {
		if has[c] {
			return nil, c
		}
	}
	for _, g := range gapKinds {
		if has[g] {
			gaps = append(gaps, g)
		}
	}
	return gaps, ""
}

// ServiceCompleteness is the contract completeness of one service as the fleet
// sees it: the contract checklist over its current revision and inferred
// record, with each section Pacto could not read marked unknown and the gap
// kinds that are no section of their own added as notes. Percent is absent
// when no section can be judged.
type ServiceCompleteness struct {
	Percent      *int                  `json:"percentComplete,omitempty" doc:"Declared sections over applicable sections, rounded down; absent when no section can be judged"`
	Sections     []CompletenessSection `json:"sections" doc:"Every section in its fixed order"`
	Completeness Completeness          `json:"completeness" doc:"Whether every source the answer reads was complete"`
	AsOf         time.Time             `json:"asOf"`
}

// CompletenessSection is one checklist row with what the fleet adds to it.
type CompletenessSection struct {
	contract.CompletenessSection
	Note string `json:"note,omitempty" doc:"What else the fleet knows about this section, such as a service that runs nowhere"`
}

// gapSection is where each gap kind and unknown cause lands in the checklist.
// A row with a note adds that note to the section; a cause with none makes the
// section unknown; a gap kind with none is the section's own missing state. An
// unreadable contract makes every section unknown.
var gapSection = map[string]struct{ section, note string }{
	GapNoSummary:             {contract.SectionSummary, ""},
	GapNoDomain:              {contract.SectionDomain, ""},
	GapNoInterfaceDocs:       {contract.SectionInterfaces, ""},
	GapNoReadiness:           {contract.SectionReadiness, ""},
	GapNotDeployed:           {contract.SectionRuntime, "runs nowhere: no source reports an operational target"},
	GapUnverifiedCapability:  {contract.SectionRuntime, "declares an extension capability no evaluator verifies"},
	causeInterfaceDocsUnread: {contract.SectionInterfaces, ""},
	causeSummaryUnknown:      {contract.SectionSummary, ""},
	causeDomainUnknown:       {contract.SectionDomain, ""},
	causePlacementUnknown:    {contract.SectionRuntime, "placement unknown: Pacto cannot see every place this service runs"},
}

// serviceCompleteness is the service's contract completeness, nil for a golden
// path or an inferred service, which have no contract of their own to judge.
func (q *Query) serviceCompleteness(s *ServiceRecord) *ServiceCompleteness {
	if s.Template || s.Inferred() {
		return nil
	}
	out := &ServiceCompleteness{Completeness: q.snap.Completeness, AsOf: q.snap.GeneratedAt}
	cur := q.currentRevision(s)
	if q.contractUnread(s, cur) {
		for _, name := range contract.SectionNames() {
			out.Sections = append(out.Sections, CompletenessSection{CompletenessSection: contract.CompletenessSection{
				Section: name, State: contract.CompletenessUnknown, Reason: "Pacto could not read the contract of this service's current revision",
			}})
		}
		out.Percent = sectionPercent(out.Sections)
		return out
	}
	byName := map[string]*CompletenessSection{}
	for _, sec := range contract.Completeness(cur.Contract, cur.Record).Sections {
		out.Sections = append(out.Sections, CompletenessSection{CompletenessSection: sec})
	}
	for i := range out.Sections {
		byName[out.Sections[i].Section] = &out.Sections[i]
	}
	if o := byName[contract.SectionOwner]; o.State == contract.CompletenessMissing &&
		(s.OwnerProvenance == OwnerProvenanceAttested || s.OwnerProvenance == OwnerProvenanceInferred) {
		o.State, o.Reason = contract.CompletenessInferred, "the owner is "+s.OwnerProvenance+"; pacto.yaml does not declare it"
	}
	has := q.knowledgeFacts(s, cur)
	for _, code := range append(slices.Clone(knowledgeCauses), gapKinds...) {
		at, ok := gapSection[code]
		if !has[code] || !ok {
			continue
		}
		sec := byName[at.section]
		switch {
		case at.note != "":
			sec.Note = strings.TrimPrefix(sec.Note+"; "+at.note, "; ")
		case slices.Contains(knowledgeCauses, code):
			sec.State, sec.Reason = contract.CompletenessUnknown, knowledgeItems[code].reason
		}
	}
	out.Percent = sectionPercent(out.Sections)
	return out
}

// sectionPercent is declared over applicable, rounded down, or nil when every
// applicable section is unknown.
func sectionPercent(sections []CompletenessSection) *int {
	declared, applicable, judged := 0, 0, false
	for _, s := range sections {
		if s.State == contract.CompletenessNotApplicable {
			continue
		}
		applicable++
		declared += b2i(s.State == contract.CompletenessDeclared)
		judged = judged || s.State != contract.CompletenessUnknown
	}
	if !judged {
		return nil
	}
	return new(declared * 100 / applicable)
}

// withGaps reports whether Pacto can judge the service and finds a gap.
func (q *Query) withGaps(s *ServiceRecord) bool {
	gaps, cause := q.serviceKnowledge(s)
	return cause == "" && len(gaps) > 0
}

// matchGap reports whether a SERVICE reference that is no golden path has the
// gap kind, or for GapUnknown an unknown knowledge state.
func (q *Query) matchGap(r EntityRef, want string) bool {
	if r.Kind != KindService || r.Template || r.Declaration == DeclarationInferred {
		return false
	}
	gaps, cause := q.serviceKnowledge(q.snap.Services[ServiceKey(r.Key)])
	if want == GapUnknown {
		return cause != ""
	}
	return slices.Contains(gaps, want)
}

// knowledgeItem is the attention wording of one gap kind or unknown cause.
type knowledgeItem struct{ summary, reason, next string }

// knowledgeItems words each gap that raises an item. No readiness raises none:
// the readiness category already lists each revision without an assessment.
var knowledgeItems = map[string]knowledgeItem{
	GapNoSummary: {"Contract declares no summary", "no contract revision of this service declares service.summary and no source reports one",
		"Add a one-line service.summary to the contract"},
	GapNoDomain: {"No business domain", "no contract revision of this service declares service.domain and no source reports one",
		"Declare service.domain in the contract"},
	GapNoInterfaceDocs: {"Interface declares no document", "the current contract revision declares an interface with no ref",
		"Point each interface's ref at its document in the bundle"},
	GapNotDeployed: {"Runs nowhere", "no source reports an operational target of this service, and every source that reports targets is complete",
		"Deploy the service, or retire its contract if it is gone"},
	GapUnverifiedCapability: {"Extension capability Pacto cannot verify", "the current contract revision declares an extension capability that no evaluator checks, so it stays declared only",
		"Check the capability by other means; Pacto reports it unverified"},
	causeContractUnreadable: {"Owner, domain and summary unknown: no contract of this service could be read",
		"Pacto could not read any contract revision of this service, so it cannot tell whether an owner is declared",
		"Make a contract revision of this service readable"},
	causeInterfaceDocsUnread: {"Interface document unread", "the contract declares interface documents Pacto could not read",
		"Make the interface documents the contract names readable in its bundle"},
	causeDomainUnknown: {"Business domain unknown", "its sources report different business domains or none could be read",
		"Declare service.domain in the contract"},
	causeSummaryUnknown: {"Summary unknown", "its sources report different summaries or none could be read",
		"Declare service.summary in the contract"},
	causePlacementUnknown: {"Placement unknown", "Pacto cannot see every place this service runs",
		"Restore or add the sources that report targets (Kubernetes, an Evidence Server or a target state), or ask for a view that covers them"},
}

// contractUnreadAlone words an unread current revision of a service whose
// owner is still claimed, so the copy never claims the owner is unknown (the
// UI label "Owner unknown" is reserved for OwnershipUnknown).
var contractUnreadAlone = knowledgeItem{"Current contract unread", "Pacto could not read the contract of this service's current revision, so it cannot tell what it leaves out",
	"Make the current contract revision of this service readable"}

// knowledgeAttention reports one info item per (service, gap kind) and one per
// service whose knowledge is unknown, naming its first cause.
func (q *Query) knowledgeAttention() []AttentionItem {
	var out []AttentionItem
	for _, s := range q.snap.Services {
		if s.Template || s.Inferred() {
			continue
		}
		gaps, cause := q.serviceKnowledge(s)
		codes := gaps
		if cause != "" {
			codes = []string{cause}
		}
		for _, c := range codes {
			w, ok := knowledgeItems[c]
			if !ok {
				continue
			}
			if b, _ := q.ownershipBucket(s); c == causeContractUnreadable && b != OwnershipUnknown {
				w = contractUnreadAlone
			}
			out = append(out, AttentionItem{
				Entity: serviceEntityRef(s), Service: string(s.Key), Label: s.Name,
				Severity: severityInfo, Code: gapCode(c), Category: categoryKnowledge,
				Summary: w.summary, Reason: w.reason, NextStep: w.next,
			})
		}
	}
	return append(out, q.replacedAttention()...)
}

// replacedAttention reports each inferred service a declared service replaced
// through a Pacto target within RecentEvidenceWindow (an older one shows on
// the service page only, so a settled fact never stays an item), and every
// replacement through another link, which carries no time.
//
// ponytail: an inferred-from item is untimed, so it stays while the
// declaration is the newest; a signature integrated time on BundleSignature
// would let it age out like a Pacto target's.
func (q *Query) replacedAttention() []AttentionItem {
	var out []AttentionItem
	for _, r := range q.snap.Replacements {
		s := q.snap.Services[r.To] // Restrict keeps only replacements of visible services
		_, from := ParseServiceKey(r.From)
		it := AttentionItem{
			Entity: serviceEntityRef(s), Service: string(s.Key), Label: from + " was replaced by " + s.Name,
			Severity: severityInfo, Code: attentionInferredReplaced, Category: categoryKnowledge,
			Summary: "Inferred service taken over by a contract",
		}
		if r.Link != linkPactoTarget {
			it.Reason = r.Line(q.snap.GeneratedAt)
			it.NextStep = "Confirm that " + from + " is " + s.Name + ": its contract now speaks for that workload; if it is not, publish a new declaration of " + s.Name + " from its own workload"
			out = append(out, it)
			continue
		}
		if r.At == nil {
			continue
		}
		age := q.snap.GeneratedAt.Sub(*r.At)
		if age > RecentEvidenceWindow {
			continue
		}
		it.Reason = "Pacto target " + string(r.Target) + " runs as " + from + ", which ran with no contract before the Pacto was created " + Age(age) + " ago"
		it.NextStep = "Check that " + s.Name + "'s contract describes what " + from + " was seen doing"
		out = append(out, it)
	}
	return out
}

// gapCode is the attention code of a gap kind (no-summary is NO_SUMMARY) or an
// unknown cause, which already is one.
func gapCode(c string) string { return strings.ToUpper(strings.ReplaceAll(c, "-", "_")) }
