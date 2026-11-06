package fleet

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
)

// Provisioning record kinds: the consumer entry a report is about.
const (
	provisioningConfiguration = "configuration"
	provisioningDependency    = "dependency"
)

// ProvisioningStatus is the provisioning state of one offering use in one
// environment, joined from provider reports at query time. Entry names the
// consumer's configuration or dependency entry when the use has more than one,
// each with its own status. Phase is the effective phase: Unknown when no
// provider reported, when the newest report is older than the provisioning
// window (Stale), when its outcome is not Observed, when it is about a consumer
// revision other than the current one, or when current reporters (fresh and
// about the current revision) disagree (Conflict lists their phases).
// ReportedPhase keeps what the newest report said. Verified is true only for
// reports accepted through signed envelopes.
type ProvisioningStatus struct {
	Environment   string     `json:"environment,omitempty"`
	Entry         string     `json:"entry,omitempty"`
	Phase         string     `json:"phase" enum:"Requested,Provisioning,Ready,Failed,Deleting,Unknown"`
	ReportedPhase string     `json:"reportedPhase,omitempty"`
	Revision      *EntityRef `json:"revision,omitempty"`
	Provider      string     `json:"provider,omitempty"`
	ExternalRef   string     `json:"externalRef,omitempty"`
	Source        string     `json:"source,omitempty"`
	SourceKind    string     `json:"sourceKind,omitempty"`
	Producer      string     `json:"producer,omitempty"`
	ProducerKeyID string     `json:"producerKeyId,omitempty"`
	Provenance    string     `json:"provenance" enum:"observed"`
	Verified      bool       `json:"verified"`
	AsOf          *time.Time `json:"asOf,omitempty"`
	Stale         bool       `json:"stale,omitempty"`
	Conflict      []string   `json:"conflict,omitempty"`
	Reason        string     `json:"reason,omitempty"`
}

// ingestProvisioning validates one source's provisioning reports into the
// snapshot and returns one counted limitation for those it dropped. The message
// names no service: the snapshot may not hold it, so a restricted view could
// not hide its name.
func ingestProvisioning(snap *FleetSnapshot, src Source, raws []RawProvisioning, now time.Time) []Limitation {
	invalid := 0
	for _, raw := range raws {
		if !validProvisioning(raw, now) {
			invalid++
			continue
		}
		snap.Provisioning = append(snap.Provisioning, ProvisioningRecord{
			ServiceKey: NewServiceKeyDomain(raw.Domain, raw.Service), Service: raw.Service, Domain: raw.Domain,
			Digest: raw.Digest, Kind: raw.Kind, Name: raw.Name, Environment: raw.Environment,
			Provider: raw.Provider, ExternalRef: raw.ExternalRef, Phase: raw.Phase, Outcome: raw.Outcome,
			Producer: raw.Producer, ProducerKeyID: raw.ProducerKeyID, ObservedAt: raw.ObservedAt,
			AcceptedAt: copyTime(raw.AcceptedAt), Verified: raw.Verified, Source: src.ID(), SourceKind: src.Kind(),
		})
	}
	if invalid == 0 {
		return nil
	}
	return []Limitation{{Code: LimitationSourceRecordInvalid, Source: src.ID(),
		Message: fmt.Sprintf("%d provisioning records were invalid and dropped", invalid)}}
}

// validProvisioning applies the ResourceProvisioned rules to a raw record: a
// named consumer revision with a complete digest and entry, a known outcome, the payload (all but the
// environment) only for an Observed outcome, and an observedAt no more than MaxProvisioningClockSkew
// after the time it was accepted (now for a record with no accept time).
func validProvisioning(raw RawProvisioning, now time.Time) bool {
	if raw.Service == "" || !contract.ValidOCIDigest(raw.Digest) || raw.Name == "" || raw.ObservedAt.IsZero() ||
		(raw.Kind != provisioningConfiguration && raw.Kind != provisioningDependency) || !evidence.Outcome(raw.Outcome).Valid() {
		return false
	}
	accepted := now
	if raw.AcceptedAt != nil {
		accepted = *raw.AcceptedAt
	}
	if raw.ObservedAt.After(accepted.Add(MaxProvisioningClockSkew)) {
		return false
	}
	p := evidence.ProvisionedObservation{Provider: raw.Provider, ExternalRef: raw.ExternalRef, Phase: evidence.ProvisionPhase(raw.Phase), Environment: raw.Environment}
	if raw.Outcome != string(evidence.Observed) {
		// No payload, but the environment it is about still names its row.
		return p == evidence.ProvisionedObservation{Environment: raw.Environment} && (raw.Environment == "" || evidence.ValidEnvironment(raw.Environment))
	}
	return evidence.ValidateProvisioned(p) == nil
}

// provisioningKey names the consumer entry a report is about.
type provisioningKey struct {
	service      ServiceKey
	digest, kind string
	name         string
}

func provisioningKind(relType string) string {
	if relType == RelationshipConfigRef {
		return provisioningConfiguration
	}
	return provisioningDependency
}

// provisioningUse reports whether rel is a use a provider may report on: a
// declared, resolved dependency or configuration reference of a revision.
func provisioningUse(rel *Relationship) bool {
	return rel.Provenance == ProvenanceDeclared && rel.Resolved && rel.FromRevision != "" &&
		(rel.Type == RelationshipDependency || rel.Type == RelationshipConfigRef)
}

// matchProvisioning drops the reports whose (service, digest) names no
// revision in the snapshot or whose revision resolves no use of that kind and
// name, with one counted limitation per source that names no service, then
// keeps the newest MaxProvisioningRecords.
func matchProvisioning(snap *FleetSnapshot) {
	uses := map[provisioningKey]bool{}
	for i := range snap.Relationships {
		rel := &snap.Relationships[i]
		if r := snap.Revisions[rel.FromRevision]; r != nil && provisioningUse(rel) {
			uses[provisioningKey{rel.FromService, r.Digest, provisioningKind(rel.Type), rel.To}] = true
		}
	}
	unmatched := map[string]int{}
	snap.Provisioning = slices.DeleteFunc(snap.Provisioning, func(rec ProvisioningRecord) bool {
		if uses[provisioningKey{rec.ServiceKey, rec.Digest, rec.Kind, rec.Name}] {
			return false
		}
		unmatched[rec.Source]++
		return true
	})
	for _, src := range slices.Sorted(maps.Keys(unmatched)) {
		snap.Limitations = append(snap.Limitations, Limitation{Code: LimitationProvisioningUnmatched, Source: src,
			Message: fmt.Sprintf("%d provisioning reports name no contract revision in this snapshot or a use it resolves", unmatched[src])})
	}
	if len(snap.Provisioning) > MaxProvisioningRecords {
		slices.SortStableFunc(snap.Provisioning, func(a, b ProvisioningRecord) int { return b.at().Compare(a.at()) })
		snap.Limitations = append(snap.Limitations, Limitation{Code: LimitationProvisioningTruncated,
			Message: fmt.Sprintf("%d provisioning reports exceed the limit of %d; the oldest were dropped", len(snap.Provisioning), MaxProvisioningRecords)})
		snap.provisioningDropped = map[ServiceKey]int{}
		for _, rec := range snap.Provisioning[MaxProvisioningRecords:] {
			snap.provisioningDropped[rec.ServiceKey]++
		}
		snap.Provisioning = snap.Provisioning[:MaxProvisioningRecords]
	}
}

// at orders reports: the server's accept time when there is one, else the
// report's own time.
func (r *ProvisioningRecord) at() time.Time {
	if r.AcceptedAt != nil {
		return *r.AcceptedAt
	}
	return r.ObservedAt
}

func sortProvisioning(recs []ProvisioningRecord) {
	slices.SortFunc(recs, func(a, b ProvisioningRecord) int {
		return cmp.Or(cmp.Compare(a.ServiceKey, b.ServiceKey), cmp.Compare(a.Digest, b.Digest), cmp.Compare(a.Kind, b.Kind),
			cmp.Compare(a.Name, b.Name), cmp.Compare(a.Environment, b.Environment), cmp.Compare(a.Source, b.Source),
			cmp.Compare(a.Producer, b.Producer), a.at().Compare(b.at()), cmp.Compare(a.Phase, b.Phase), cmp.Compare(a.Outcome, b.Outcome),
			cmp.Compare(a.Provider, b.Provider), cmp.Compare(a.ExternalRef, b.ExternalRef))
	})
}

// provisioningIndex groups the snapshot's reports by the consumer entry they
// are about, so each use looks up its own instead of scanning every report.
func (q *Query) provisioningIndex() map[provisioningKey][]*ProvisioningRecord {
	idx := map[provisioningKey][]*ProvisioningRecord{}
	for i := range q.snap.Provisioning {
		rec := &q.snap.Provisioning[i]
		k := provisioningKey{rec.ServiceKey, rec.Digest, rec.Kind, rec.Name}
		idx[k] = append(idx[k], rec)
	}
	return idx
}

// provisioningSlot is one status of a use: an environment and the consumer
// entry (a configuration or dependency name) the reports are about.
type provisioningSlot struct{ env, entry string }

// provisioningFor joins the reports about a use's relationships (each matched
// by the declaring revision's digest, so a report attaches to the provider that
// revision asked for) into one status per environment and consumer entry.
// current is the consumer revision the use speaks for: each of its entries gets
// a status in every environment it declares (the default one when it declares
// none), Unknown when nothing reported there. Entry is set only when the use
// has more than one entry, so one entry's Ready never speaks for another.
func (q *Query) provisioningFor(use OfferingUse, rels []*Relationship, current *ContractRevision, idx map[provisioningKey][]*ProvisioningRecord) ([]ProvisioningStatus, []Limitation) {
	revs := map[string]*ContractRevision{}
	slots := map[provisioningSlot][]*ProvisioningRecord{}
	seen := map[provisioningKey]bool{}
	var currentEntries []string
	for _, rel := range rels {
		r := q.snap.Revisions[rel.FromRevision]
		if r == nil {
			continue
		}
		revs[r.Digest] = r
		k := provisioningKey{rel.FromService, r.Digest, provisioningKind(rel.Type), rel.To}
		if seen[k] {
			continue
		}
		seen[k] = true
		for _, rec := range idx[k] {
			sl := provisioningSlot{rec.Environment, rel.To}
			slots[sl] = append(slots[sl], rec)
		}
		if current != nil && r.Key == current.Key {
			currentEntries = append(currentEntries, rel.To)
		}
	}
	seedProvisioningSlots(slots, currentEntries, current)
	if len(slots) == 0 {
		return []ProvisioningStatus{{Phase: StatusUnknown, Provenance: ProvenanceObserved, Reason: noProvisioningReport}}, nil
	}
	return q.provisioningStatuses(use, slots, revs, current)
}

// noProvisioningReport is the reason of a status nothing reported.
const noProvisioningReport = "no provider has reported provisioning for this use"

// seedProvisioningSlots gives each entry of the current revision a slot in
// every environment it declares, so an environment nothing reported on reads
// Unknown. A revision that declares none gets the default environment, unless
// a report already speaks for the entry.
func seedProvisioningSlots(slots map[provisioningSlot][]*ProvisioningRecord, entries []string, current *ContractRevision) {
	var envs []string
	if current != nil && current.Contract != nil {
		for _, e := range current.Contract.Environments {
			envs = append(envs, e.Name)
		}
	}
	for _, entry := range entries {
		seed := envs
		if len(seed) == 0 {
			if slices.ContainsFunc(slices.Collect(maps.Keys(slots)), func(sl provisioningSlot) bool { return sl.entry == entry }) {
				continue
			}
			seed = []string{""}
		}
		for _, env := range seed {
			if _, ok := slots[provisioningSlot{env, entry}]; !ok {
				slots[provisioningSlot{env, entry}] = nil
			}
		}
	}
}

// provisioningStatuses is one status per slot, sorted by environment and
// entry, and the conflicts they raise.
func (q *Query) provisioningStatuses(use OfferingUse, slots map[provisioningSlot][]*ProvisioningRecord, revs map[string]*ContractRevision, current *ContractRevision) ([]ProvisioningStatus, []Limitation) {
	entries := map[string]bool{}
	for sl := range slots {
		entries[sl.entry] = true
	}
	keys := slices.SortedFunc(maps.Keys(slots), func(a, b provisioningSlot) int {
		return cmp.Or(cmp.Compare(a.env, b.env), cmp.Compare(a.entry, b.entry))
	})
	var out []ProvisioningStatus
	var lims []Limitation
	for _, sl := range keys {
		st := ProvisioningStatus{Environment: sl.env, Phase: StatusUnknown, Provenance: ProvenanceObserved, Reason: noProvisioningReport}
		if len(slots[sl]) > 0 {
			st = q.provisioningStatus(sl.env, slots[sl], revs, current)
		}
		where := cmp.Or(sl.env, "default")
		if len(entries) > 1 {
			st.Entry, where = sl.entry, where+" for "+sl.entry
		}
		if st.Conflict != nil {
			lims = append(lims, Limitation{Code: LimitationProvisioningConflict, Message: fmt.Sprintf(
				"provisioning reports for %s on %s disagree in environment %s", use.Consumer.Label, use.Provider.Label, where)})
		}
		out = append(out, st)
	}
	return out, lims
}

// latestReports is the newest report per (source, producer), in a stable order.
func latestReports(recs []*ProvisioningRecord) []*ProvisioningRecord {
	latest := map[string]*ProvisioningRecord{}
	for _, r := range recs {
		k := r.Source + "\x00" + r.Producer
		if l := latest[k]; l == nil || l.at().Before(r.at()) {
			latest[k] = r
		}
	}
	out := make([]*ProvisioningRecord, 0, len(latest))
	for _, k := range slices.Sorted(maps.Keys(latest)) {
		out = append(out, latest[k])
	}
	return out
}

// newestReport is the most recently accepted of recs, which is not empty.
func newestReport(recs []*ProvisioningRecord) *ProvisioningRecord {
	newest := recs[0]
	for _, r := range recs[1:] {
		if newest.at().Before(r.at()) {
			newest = r
		}
	}
	return newest
}

// staleReport reports whether r is older than the provisioning window.
func (q *Query) staleReport(r *ProvisioningRecord) bool {
	return q.snap.GeneratedAt.Sub(r.ObservedAt) > cmp.Or(q.snap.provisioningWindow, RecentEvidenceWindow)
}

// provisioningStatus is one slot's status from its reports: the newest report
// per (source, producer), then the Unknown rules of ProvisioningStatus. Only
// current reports (fresh, about the current consumer revision) can disagree or
// speak for the slot; with none, the newest report explains the Unknown.
func (q *Query) provisioningStatus(env string, recs []*ProvisioningRecord, revs map[string]*ContractRevision, current *ContractRevision) ProvisioningStatus {
	st := ProvisioningStatus{Environment: env, Phase: StatusUnknown, Provenance: ProvenanceObserved}
	latest := latestReports(recs)
	currentReports := slices.DeleteFunc(slices.Clone(latest), func(r *ProvisioningRecord) bool {
		return q.staleReport(r) || (current != nil && revs[r.Digest].Key != current.Key)
	})
	phases := map[string]bool{}
	for _, r := range currentReports {
		if r.Outcome == string(evidence.Observed) {
			phases[r.Phase] = true
		}
	}
	if len(phases) > 1 {
		st.Conflict, st.Reason = slices.Sorted(maps.Keys(phases)), "the providers that report on this use disagree"
		return st
	}
	newest := newestReport(latest)
	if len(currentReports) > 0 {
		newest = newestReport(currentReports)
	}
	asOf := newest.ObservedAt
	st.ReportedPhase, st.Provider, st.ExternalRef, st.AsOf = newest.Phase, newest.Provider, newest.ExternalRef, &asOf
	st.Source, st.SourceKind, st.Producer, st.ProducerKeyID, st.Verified = newest.Source, newest.SourceKind, newest.Producer, newest.ProducerKeyID, newest.Verified
	if !newest.Verified {
		st.SourceKind += " (unsigned)"
	}
	rev := revs[newest.Digest]
	ref := revisionEntityRef(rev)
	st.Revision = &ref
	switch {
	case newest.Outcome != string(evidence.Observed):
		st.Reason = "the provider reported " + strings.ToLower(newest.Outcome)
	case q.staleReport(newest):
		st.Stale, st.Reason = true, "the last report is older than the provisioning window"
	case current != nil && rev.Key != current.Key:
		st.Reason = fmt.Sprintf("last report is for %s; %s has no report yet", cmp.Or(rev.Version, rev.Digest), cmp.Or(current.Version, current.Digest))
	default:
		st.Phase = newest.Phase
	}
	return st
}
