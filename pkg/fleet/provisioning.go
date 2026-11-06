package fleet

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/evidence"
)

// Provisioning record kinds: the consumer entry a report is about.
const (
	provisioningConfiguration = "configuration"
	provisioningDependency    = "dependency"
)

// ProvisioningStatus is the provisioning state of one offering use in one
// environment, joined from provider reports at query time. Phase is the
// effective phase: Unknown when no provider reported, when the newest report is
// older than the provisioning window (Stale), when its outcome is not Observed,
// when it is about a consumer revision other than the current one, or when
// reporters disagree (Conflict lists their phases). ReportedPhase keeps what
// the newest report said. Verified is true only for reports accepted through
// signed envelopes.
type ProvisioningStatus struct {
	Environment   string     `json:"environment,omitempty"`
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
// named consumer revision and entry, a known outcome, the payload only for an
// Observed outcome, and an observedAt no more than MaxProvisioningClockSkew
// after the time it was accepted (now for a record with no accept time).
func validProvisioning(raw RawProvisioning, now time.Time) bool {
	if raw.Service == "" || raw.Digest == "" || raw.Name == "" || raw.ObservedAt.IsZero() ||
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
		return p == evidence.ProvisionedObservation{}
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

// provisioningFor joins the reports about a use's relationships (each matched
// by the declaring revision's digest, so a report attaches to the provider that
// revision asked for) into one status per environment. current is the consumer
// revision the use speaks for. No report is one Unknown status.
func (q *Query) provisioningFor(use OfferingUse, rels []*Relationship, current *ContractRevision) ([]ProvisioningStatus, []Limitation) {
	want := map[provisioningKey]bool{}
	revs := map[string]*ContractRevision{}
	for _, rel := range rels {
		if r := q.snap.Revisions[rel.FromRevision]; r != nil {
			want[provisioningKey{rel.FromService, r.Digest, provisioningKind(rel.Type), rel.To}] = true
			revs[r.Digest] = r
		}
	}
	byEnv := map[string][]*ProvisioningRecord{}
	for i := range q.snap.Provisioning {
		rec := &q.snap.Provisioning[i]
		if want[provisioningKey{rec.ServiceKey, rec.Digest, rec.Kind, rec.Name}] {
			byEnv[rec.Environment] = append(byEnv[rec.Environment], rec)
		}
	}
	if len(byEnv) == 0 {
		return []ProvisioningStatus{{Phase: StatusUnknown, Provenance: ProvenanceObserved, Reason: "no provider has reported provisioning for this use"}}, nil
	}
	var out []ProvisioningStatus
	var lims []Limitation
	for _, env := range slices.Sorted(maps.Keys(byEnv)) {
		st := q.provisioningStatus(env, byEnv[env], revs, current)
		if st.Conflict != nil {
			lims = append(lims, Limitation{Code: LimitationProvisioningConflict, Message: fmt.Sprintf(
				"provisioning reports for %s on %s disagree in environment %s", use.Consumer.Label, use.Provider.Label, cmp.Or(env, "default"))})
		}
		out = append(out, st)
	}
	return out, lims
}

// latestReports is the newest report per (source, producer), in a stable
// order, and the distinct phases the Observed ones report.
func latestReports(recs []*ProvisioningRecord) ([]*ProvisioningRecord, []string) {
	latest := map[string]*ProvisioningRecord{}
	for _, r := range recs {
		k := r.Source + "\x00" + r.Producer
		if l := latest[k]; l == nil || l.at().Before(r.at()) {
			latest[k] = r
		}
	}
	var out []*ProvisioningRecord
	phases := map[string]bool{}
	for _, k := range slices.Sorted(maps.Keys(latest)) {
		out = append(out, latest[k])
		if latest[k].Outcome == string(evidence.Observed) {
			phases[latest[k].Phase] = true
		}
	}
	return out, slices.Sorted(maps.Keys(phases))
}

// provisioningStatus is one environment's status from its reports: the newest
// report per (source, producer), then the Unknown rules of ProvisioningStatus.
func (q *Query) provisioningStatus(env string, recs []*ProvisioningRecord, revs map[string]*ContractRevision, current *ContractRevision) ProvisioningStatus {
	st := ProvisioningStatus{Environment: env, Phase: StatusUnknown, Provenance: ProvenanceObserved}
	latest, phases := latestReports(recs)
	if len(phases) > 1 {
		st.Conflict, st.Reason = phases, "the providers that report on this use disagree"
		return st
	}
	newest := latest[0]
	for _, r := range latest[1:] {
		if newest.at().Before(r.at()) {
			newest = r
		}
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
	case q.snap.GeneratedAt.Sub(newest.ObservedAt) > cmp.Or(q.snap.provisioningWindow, RecentEvidenceWindow):
		st.Stale, st.Reason = true, "the last report is older than the provisioning window"
	case current != nil && rev.Key != current.Key:
		st.Reason = fmt.Sprintf("last report is for %s; %s has no report yet", cmp.Or(rev.Version, rev.Digest), cmp.Or(current.Version, current.Digest))
	default:
		st.Phase = newest.Phase
	}
	return st
}
