package fleet

import (
	"slices"
	"strings"

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
// in two kinds and once in WithGaps.
type KnowledgeTally struct {
	Services  int           `json:"services" doc:"Services that are no golden path; the whole the other three partition"`
	Described int           `json:"described" doc:"Services whose contract and OpenAPI interface documents were read and leave nothing out; gRPC and AsyncAPI documents are never read"`
	WithGaps  int           `json:"withGaps" doc:"Services with at least one knowledge gap"`
	Unknown   int           `json:"unknown" doc:"Services whose knowledge Pacto cannot judge: an unreadable contract or OpenAPI document, conflicting summaries or business domains, or placement it cannot see"`
	Gaps      KnowledgeGaps `json:"gaps"`
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
	known = !q.snap.targetsHidden[s.Key] && hasTargetSource(q.snap) && targetPlacementKnown(q.snap)
	return known, known
}

// serviceKnowledge returns the service's gap kinds in the fixed order, or, when
// Pacto cannot judge them, no gaps and the first cause.
func (q *Query) serviceKnowledge(s *ServiceRecord) (gaps []string, cause string) {
	cur := q.currentRevision(s)
	switch {
	case q.ownershipUnknown(s) || cur == nil || cur.Contract == nil:
		return nil, causeContractUnreadable
	case slices.ContainsFunc(cur.Contract.Interfaces, func(i contract.Interface) bool {
		return i.Type == contract.InterfaceTypeOpenAPI && i.Ref != "" && !slices.Contains(cur.SpecsRead, i.Name)
	}):
		return nil, causeInterfaceDocsUnread
	case s.SummaryUnknown:
		return nil, causeSummaryUnknown
	case s.BusinessDomainUnknown:
		return nil, causeDomainUnknown
	}
	runsNowhere, known := q.notDeployed(s)
	if !known {
		return nil, causePlacementUnknown
	}
	has := map[string]bool{
		GapNoSummary:            s.Summary == "",
		GapNoDomain:             s.BusinessDomain == "" && !s.BusinessDomainUnknown,
		GapNoInterfaceDocs:      slices.ContainsFunc(cur.Contract.Interfaces, func(i contract.Interface) bool { return i.Ref == "" }),
		GapNoReadiness:          cur.Readiness == nil,
		GapNotDeployed:          runsNowhere,
		GapUnverifiedCapability: slices.ContainsFunc(cur.Contract.Capabilities, func(c contract.Capability) bool { return c.Type == contract.CapabilityExtension }),
	}
	for _, g := range gapKinds {
		if has[g] {
			gaps = append(gaps, g)
		}
	}
	return gaps, ""
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
			Summary: "Inferred service taken over by a contract", NextAction: editContract(),
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
