package fleet

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/finding"
)

// Extension check states. An extension capability with no check is
// unverified: no trusted producer has a report on it.
const (
	ExtensionCovered = "covered"
	ExtensionFailing = "failing"
	ExtensionUnknown = "unknown"
)

// LimitationExtensionUnmatched: extension reports name no contract revision in
// the snapshot or an extension capability it declares; they were dropped.
const LimitationExtensionUnmatched = "EXTENSION_UNMATCHED"

// ExtensionCheck is what trusted producers report on one extension capability
// a revision declares: covered (every current report says present), failing
// (a current report says absent; Producers names each such producer) or
// unknown (a current report did not answer, every report is stale, or the
// last report is about another revision). It is observed evidence and never
// positive by silence: an extension no producer reported on has no check.
type ExtensionCheck struct {
	Ref                string          `json:"ref" doc:"The extension capability ref the check is about."`
	State              string          `json:"state" enum:"covered,failing,unknown" doc:"covered: every current report says present. failing: a current report says absent. unknown: the reports are stale, did not answer or are about another revision."`
	Producers          []string        `json:"producers,omitempty" doc:"Every producer that reports the capability absent, sorted; set on a failing check."`
	Producer           string          `json:"producer,omitempty" doc:"The producer whose report the check reads."`
	ProducerKeyID      string          `json:"producerKeyId,omitempty" doc:"The key that producer signed the report with."`
	Source             string          `json:"source,omitempty" doc:"The evidence source the report came from."`
	SourceKind         string          `json:"sourceKind,omitempty" doc:"The kind of that source."`
	Provenance         string          `json:"provenance,omitempty" enum:"observed" doc:"Always observed: a check is read from signed reports."`
	AsOf               *time.Time      `json:"asOf,omitempty" doc:"When the producer observed what the report says."`
	Stale              bool            `json:"stale,omitempty" doc:"The report is older than the freshness window."`
	Revision           *EntityRef      `json:"revision,omitempty" doc:"The other revision the last report is about, when the reader can see it."`
	Reason             string          `json:"reason,omitempty" doc:"Why the check reads failing or unknown."`
	Details            json.RawMessage `json:"details,omitempty" doc:"The producer's own free-form account in its newest current report; Pacto never interprets it."`
	CountsTowardStatus bool            `json:"countsTowardStatus,omitempty" doc:"The revision is one the service status reads: a revision a target runs, else the current one."`
}

// extensionRecord is one verified extension report Build matched to a
// revision. cluster is the id of the cluster whose Evidence Server accepted
// it, so a restricted view drops what its caller may not read.
type extensionRecord struct {
	service                          ServiceKey
	digest, ref                      string
	present                          *bool
	details                          json.RawMessage
	outcome, producer, producerKeyID string
	observedAt                       time.Time
	acceptedAt                       *time.Time
	source, sourceKind, cluster      string
}

func (r *extensionRecord) at() time.Time  { return *cmp.Or(r.acceptedAt, &r.observedAt) }
func (r *extensionRecord) origin() string { return r.source + "\x00" + r.producer }
func (r *extensionRecord) env() string    { return "" }

// ingestExtensions validates one source's extension reports into the
// snapshot and returns one counted limitation, naming no service, for those
// it dropped (an unsigned record among them).
func ingestExtensions(snap *FleetSnapshot, src Source, raws []RawExtension, now time.Time) []Limitation {
	invalid := 0
	for _, raw := range raws {
		if !validExtension(raw, now) {
			invalid++
			continue
		}
		rec := extensionRecord{
			service: NewServiceKeyDomain(raw.Domain, raw.Service), digest: raw.Digest, ref: raw.Ref,
			present: clonePtr(raw.Present), details: slices.Clone(raw.Details), outcome: raw.Outcome,
			producer: raw.Producer, producerKeyID: raw.ProducerKeyID, observedAt: raw.ObservedAt,
			acceptedAt: copyTime(raw.AcceptedAt), source: src.ID(), sourceKind: src.Kind(),
		}
		if raw.Cluster != nil {
			rec.cluster = raw.Cluster.ID
		}
		snap.extensionRecords = append(snap.extensionRecords, rec)
	}
	if invalid == 0 {
		return nil
	}
	return []Limitation{{Code: LimitationSourceRecordInvalid, Source: src.ID(),
		Message: fmt.Sprintf("%d extension reports were invalid or unsigned and dropped", invalid)}}
}

// validExtension applies the extension CapabilityObserved rules to a raw
// record: a signed report about a named revision with a complete digest and a
// namespaced ref, a printable producer, a known outcome, present set exactly
// for an Observed outcome, details only on an Observed report and within the
// evidence bound, and an observedAt no more than MaxProvisioningClockSkew
// after the time it was accepted.
func validExtension(raw RawExtension, now time.Time) bool {
	if !raw.Verified || raw.Service == "" || !contract.ValidOCIDigest(raw.Digest) || !contract.NamespacedRef(raw.Ref) ||
		raw.ObservedAt.IsZero() || !evidence.Outcome(raw.Outcome).Valid() || raw.Producer == "" ||
		strings.ContainsFunc(raw.Producer, func(r rune) bool { return !unicode.IsPrint(r) }) || aheadOfAccept(raw.ObservedAt, raw.AcceptedAt, now) {
		return false
	}
	observed := raw.Outcome == string(evidence.Observed)
	if observed != (raw.Present != nil) {
		return false
	}
	return len(raw.Details) == 0 || (observed && evidence.ValidCapabilityDetails(raw.Details) == nil)
}

// matchExtensions drops the records whose (service, digest) names no revision
// of the snapshot or whose revision declares no such extension, with one
// counted limitation per source that names no service.
func matchExtensions(snap *FleetSnapshot) {
	unmatched := map[string]int{}
	snap.extensionRecords = slices.DeleteFunc(snap.extensionRecords, func(rec extensionRecord) bool {
		if r := snap.Revisions[NewRevisionKey(rec.service, rec.digest)]; r != nil && declaresExtension(r, rec.ref) {
			return false
		}
		unmatched[rec.source]++
		return true
	})
	for _, src := range slices.Sorted(maps.Keys(unmatched)) {
		snap.Limitations = append(snap.Limitations, Limitation{Code: LimitationExtensionUnmatched, Source: src,
			Message: fmt.Sprintf("%d extension reports name no contract revision in this snapshot or an extension capability it declares", unmatched[src])})
	}
}

func declaresExtension(r *ContractRevision, ref string) bool {
	return r.Contract != nil && slices.ContainsFunc(r.Contract.Capabilities, func(c contract.Capability) bool {
		return c.Type == contract.CapabilityExtension && c.Ref == ref
	})
}

// checked reports whether r carries a check on the extension ref.
func (r *ContractRevision) checked(ref string) bool {
	return slices.ContainsFunc(r.Extensions, func(c ExtensionCheck) bool { return c.Ref == ref })
}

// deriveExtensionChecks sets each revision's Extensions from the snapshot's
// records. Build passes inPlace; a restricted view shares its revisions with
// the snapshot it narrows, so it writes each changed revision as a copy.
func deriveExtensionChecks(snap *FleetSnapshot, inPlace bool) {
	byService := map[ServiceKey][]*extensionRecord{}
	for i := range snap.extensionRecords {
		rec := &snap.extensionRecords[i]
		byService[rec.service] = append(byService[rec.service], rec)
	}
	for _, s := range snap.Services {
		counts := map[RevisionKey]bool{}
		for _, r := range statusRevisions(snap, s) {
			counts[r.Key] = true
		}
		for _, rk := range s.Revisions {
			r := snap.Revisions[rk]
			if r == nil {
				continue
			}
			checks := revisionExtensionChecks(snap, byService[s.Key], r, counts[rk])
			if len(checks) == 0 && len(r.Extensions) == 0 {
				continue
			}
			if !inPlace {
				c := *r
				r = &c
				snap.Revisions[rk] = r
			}
			r.Extensions = checks
		}
	}
}

// revisionExtensionChecks is the check of each extension r declares that has
// a record to read, in declaration order.
func revisionExtensionChecks(snap *FleetSnapshot, recs []*extensionRecord, r *ContractRevision, counts bool) []ExtensionCheck {
	if len(recs) == 0 || r.Contract == nil {
		return nil
	}
	var out []ExtensionCheck
	for _, c := range r.Contract.Capabilities {
		if c.Type != contract.CapabilityExtension {
			continue
		}
		if chk := extensionCheck(snap, recs, r, c.Ref); chk != nil {
			chk.CountsTowardStatus = counts
			out = append(out, *chk)
		}
	}
	return out
}

// extensionCheck is r's check on ref from its service's records recs, nil
// when no record names ref. R's own records (about its digest) speak first;
// only when it has none does the newest report about another revision make
// the check unknown.
func extensionCheck(snap *FleetSnapshot, recs []*extensionRecord, r *ContractRevision, ref string) *ExtensionCheck {
	var own, other []*extensionRecord
	for _, rec := range recs {
		switch {
		case rec.ref != ref:
		case rec.digest == r.Digest:
			own = append(own, rec)
		default:
			other = append(other, rec)
		}
	}
	if len(own) > 0 {
		latest := latestReports(own)
		if current := slices.DeleteFunc(slices.Clone(latest), func(rec *extensionRecord) bool { return snap.reportStale(rec.observedAt) }); len(current) > 0 {
			return currentExtensionCheck(ref, current)
		}
		chk := checkFrom(ref, ExtensionUnknown, newestReport(latest))
		chk.Stale, chk.Reason = true, "the last report is older than the freshness window"
		return chk
	}
	if len(other) == 0 {
		return nil
	}
	newest := newestReport(other)
	chk := checkFrom(ref, ExtensionUnknown, newest)
	chk.Reason = "the last report is for a revision you cannot see"
	if rev := snap.Revisions[NewRevisionKey(newest.service, newest.digest)]; rev != nil {
		er := revisionEntityRef(rev)
		chk.Revision = &er
		chk.Reason = fmt.Sprintf("last report is for %s; %s has no report yet", cmp.Or(rev.Version, rev.Digest), cmp.Or(r.Version, r.Digest))
	}
	return chk
}

// currentExtensionCheck reads the fresh reports about the revision itself: a
// report of absence fails it, reports that all say present cover it, and any
// other answer leaves it unknown.
func currentExtensionCheck(ref string, current []*extensionRecord) *ExtensionCheck {
	var absent, unanswered []*extensionRecord
	for _, rec := range current {
		switch {
		case rec.outcome != string(evidence.Observed):
			unanswered = append(unanswered, rec)
		case !*rec.present:
			absent = append(absent, rec)
		}
	}
	if len(absent) > 0 {
		slices.SortFunc(absent, func(a, b *extensionRecord) int { return cmp.Compare(a.producer, b.producer) })
		chk := checkFrom(ref, ExtensionFailing, absent[0])
		for _, rec := range absent {
			chk.Producers = appendUnique(chk.Producers, rec.producer)
		}
		chk.Reason = "reported absent by " + strings.Join(chk.Producers, ", ")
		return chk
	}
	if len(unanswered) > 0 {
		rec := newestReport(unanswered)
		chk := checkFrom(ref, ExtensionUnknown, rec)
		chk.Reason = rec.producer + " reported " + strings.ToLower(rec.outcome)
		return chk
	}
	rec := newestReport(current)
	chk := checkFrom(ref, ExtensionCovered, rec)
	chk.Details = slices.Clone(rec.details)
	return chk
}

func checkFrom(ref, state string, rec *extensionRecord) *ExtensionCheck {
	asOf := rec.observedAt
	return &ExtensionCheck{Ref: ref, State: state, Producer: rec.producer, ProducerKeyID: rec.producerKeyID,
		Source: rec.source, SourceKind: rec.sourceKind, Provenance: ProvenanceObserved, AsOf: &asOf}
}

// extensionStatus is the status the counting checks of s add to its
// aggregate: NonCompliant when one fails, Unknown when one is unknown, else "".
func extensionStatus(snap *FleetSnapshot, s *ServiceRecord) string {
	st := ""
	for _, r := range statusRevisions(snap, s) {
		for _, c := range r.Extensions {
			switch c.State {
			case ExtensionFailing:
				return StatusNonCompliant
			case ExtensionUnknown:
				st = StatusUnknown
			}
		}
	}
	return st
}

// worstStatus is the more severe of two statuses, "" meaning none.
func worstStatus(a, b string) string {
	if a == "" || (b != "" && severityRank[b] < severityRank[a]) {
		return b
	}
	return a
}

// extensionReasons explains each failing or unknown check of r that counts
// toward its service's status, naming the producer.
func extensionReasons(r *ContractRevision) []Reason {
	var out []Reason
	for _, c := range r.Extensions {
		var code string
		switch {
		case !c.CountsTowardStatus || c.State == ExtensionCovered:
			continue
		case c.State == ExtensionFailing:
			code = string(finding.CodeCapabilityAbsent)
		case c.Stale:
			code = LimitationSourceStale
		default:
			code = LimitationEvidenceMissing
		}
		out = append(out, Reason{Code: code, Message: c.Reason, Source: c.Source, ObservedAt: copyTime(c.AsOf),
			Assertion: &AssertionRef{Kind: "capability", Name: c.Ref}})
	}
	return out
}

// extensionAttention raises one item per failing or unknown check of r that
// counts toward its service's status. Neither code edits the contract, so
// neither item carries a NextAction.
func extensionAttention(r *ContractRevision, ref EntityRef) []AttentionItem {
	var out []AttentionItem
	version := cmp.Or(r.Version, r.Digest)
	for _, c := range r.Extensions {
		item := AttentionItem{Entity: ref, Service: string(r.ServiceKey), Label: ref.Label + ": " + c.Ref, Source: c.Source}
		switch {
		case !c.CountsTowardStatus || c.State == ExtensionCovered:
			continue
		case c.State == ExtensionFailing:
			item.Severity, item.Code, item.Category = severityError, string(finding.CodeCapabilityAbsent), categoryNonCompliant
			item.Summary = "Extension capability reported absent"
			item.Reason = fmt.Sprintf("%s reports %s absent on %s", strings.Join(c.Producers, ", "), c.Ref, version)
			item.NextStep = fmt.Sprintf("Fix %s or ask %s why it reports it absent", c.Ref, c.Producer)
		default:
			item.Severity, item.Code, item.Category = severityWarning, codeExtensionUnreported, categoryUnknown
			if c.Stale {
				item.Category = categoryStale
			}
			item.Summary, item.Reason = "Extension capability not reported", c.Reason
			item.NextStep = fmt.Sprintf("Have %s report on %s", c.Producer, version)
		}
		out = append(out, item)
	}
	return out
}

// codeExtensionUnreported is the attention code of an extension check that
// reads unknown.
const codeExtensionUnreported = "EXTENSION_UNREPORTED"
