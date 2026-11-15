package fleet

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
)

// Consumption states.
const (
	consumptionReported    = "reported"
	consumptionUnknown     = "unknown"
	consumptionNotReported = "not-reported"
)

// ingestUsage validates one source's usage reports into the snapshot and
// returns one counted limitation, naming no service, for those it dropped.
func ingestUsage(snap *FleetSnapshot, src Source, raws []RawUsage, now time.Time) []Limitation {
	invalid := 0
	for _, raw := range raws {
		if !validUsage(raw, now) {
			invalid++
			continue
		}
		snap.Usage = append(snap.Usage, UsageRecord{
			ServiceKey: NewServiceKeyDomain(raw.Domain, raw.Service), Service: raw.Service, Domain: raw.Domain,
			Digest: raw.Digest, Kind: raw.Kind, Name: raw.Name, Environment: raw.Environment,
			Provider: raw.Provider, ExternalRef: raw.ExternalRef, Metrics: raw.Metrics, Cost: raw.Cost, Outcome: raw.Outcome,
			Producer: raw.Producer, ProducerKeyID: raw.ProducerKeyID, ObservedAt: raw.ObservedAt,
			AcceptedAt: copyTime(raw.AcceptedAt), Verified: raw.Verified, Source: src.ID(), SourceKind: src.Kind(),
		})
	}
	if invalid == 0 {
		return nil
	}
	return []Limitation{{Code: LimitationSourceRecordInvalid, Source: src.ID(),
		Message: fmt.Sprintf("%d usage records were invalid and dropped", invalid)}}
}

// validUsage applies the ResourceUsage rules to a raw record, as
// validProvisioning does for ResourceProvisioned.
func validUsage(raw RawUsage, now time.Time) bool {
	if raw.Service == "" || !contract.ValidOCIDigest(raw.Digest) || raw.Name == "" || raw.ObservedAt.IsZero() ||
		(raw.Kind != provisioningConfiguration && raw.Kind != provisioningDependency) || !evidence.Outcome(raw.Outcome).Valid() {
		return false
	}
	if aheadOfAccept(raw.ObservedAt, raw.AcceptedAt, now) {
		return false
	}
	if raw.Outcome != string(evidence.Observed) {
		// No payload, but the environment it is about still names its row.
		return raw.Provider == "" && raw.ExternalRef == "" && raw.Metrics == nil && raw.Cost == nil &&
			(raw.Environment == "" || evidence.ValidEnvironment(raw.Environment))
	}
	return evidence.ValidateUsage(evidence.UsageObservation{Provider: raw.Provider, ExternalRef: raw.ExternalRef,
		Environment: raw.Environment, Metrics: raw.Metrics, Cost: raw.Cost}) == nil
}

// matchUsage drops the usage reports that name no use of the snapshot, with
// one counted limitation per source, marks the stale ones and keeps the newest
// MaxUsageRecords.
func matchUsage(snap *FleetSnapshot) {
	uses := useKeys(snap)
	unmatched := map[string]int{}
	snap.Usage = slices.DeleteFunc(snap.Usage, func(rec UsageRecord) bool {
		if uses[provisioningKey{rec.ServiceKey, rec.Digest, rec.Kind, rec.Name}] {
			return false
		}
		unmatched[rec.Source]++
		return true
	})
	for _, src := range slices.Sorted(maps.Keys(unmatched)) {
		snap.Limitations = append(snap.Limitations, Limitation{Code: LimitationUsageUnmatched, Source: src,
			Message: fmt.Sprintf("%d usage reports name no contract revision in this snapshot or a use it resolves", unmatched[src])})
	}
	window := cmp.Or(snap.provisioningWindow, RecentEvidenceWindow)
	for i := range snap.Usage {
		snap.Usage[i].Stale = snap.GeneratedAt.Sub(snap.Usage[i].ObservedAt) > window
	}
	if len(snap.Usage) > MaxUsageRecords {
		slices.SortStableFunc(snap.Usage, func(a, b UsageRecord) int { return b.at().Compare(a.at()) })
		snap.Limitations = append(snap.Limitations, Limitation{Code: LimitationUsageTruncated,
			Message: fmt.Sprintf("%d usage reports exceed the limit of %d; the oldest were dropped", len(snap.Usage), MaxUsageRecords)})
		snap.usageDropped = map[ServiceKey]int{}
		for _, rec := range snap.Usage[MaxUsageRecords:] {
			snap.usageDropped[rec.ServiceKey]++
		}
		snap.Usage = snap.Usage[:MaxUsageRecords]
	}
}

func (r *UsageRecord) at() time.Time {
	if r.AcceptedAt != nil {
		return *r.AcceptedAt
	}
	return r.ObservedAt
}
func (r *UsageRecord) origin() string { return r.Source + "\x00" + r.Producer }
func (r *UsageRecord) env() string    { return r.Environment }

func sortUsage(recs []UsageRecord) {
	slices.SortFunc(recs, func(a, b UsageRecord) int {
		if c := cmp.Or(cmp.Compare(a.ServiceKey, b.ServiceKey), cmp.Compare(a.Digest, b.Digest), cmp.Compare(a.Kind, b.Kind),
			cmp.Compare(a.Name, b.Name), cmp.Compare(a.Environment, b.Environment), cmp.Compare(a.Source, b.Source),
			cmp.Compare(a.Producer, b.Producer), a.at().Compare(b.at()), cmp.Compare(a.Outcome, b.Outcome),
			cmp.Compare(a.Provider, b.Provider), cmp.Compare(a.ExternalRef, b.ExternalRef)); c != 0 {
			return c
		}
		return cmp.Compare(figuresKey(&a), figuresKey(&b))
	})
}

// usageIndex groups the snapshot's usage reports by the consumer entry they
// are about.
func (q *Query) usageIndex() map[provisioningKey][]*UsageRecord {
	idx := map[provisioningKey][]*UsageRecord{}
	for i := range q.snap.Usage {
		rec := &q.snap.Usage[i]
		k := provisioningKey{rec.ServiceKey, rec.Digest, rec.Kind, rec.Name}
		idx[k] = append(idx[k], rec)
	}
	return idx
}

// usageFor joins the usage reports about a use's relationships into one
// consumption per environment and consumer entry, the slots provisioningFor
// uses. A use nothing reported usage for keeps the single not-reported entry.
func (q *Query) usageFor(use OfferingUse, rels []*Relationship, current *ContractRevision, idx map[provisioningKey][]*UsageRecord) ([]ResourceConsumption, []Limitation) {
	slots, revs := useSlots(q.snap, rels, current, idx)
	if !slices.ContainsFunc(slices.Collect(maps.Values(slots)), func(recs []*UsageRecord) bool { return len(recs) > 0 }) {
		return notReportedConsumption(), nil
	}
	entries := map[string]bool{}
	for sl := range slots {
		entries[sl.entry] = true
	}
	keys := slices.SortedFunc(maps.Keys(slots), func(a, b provisioningSlot) int {
		return cmp.Or(cmp.Compare(a.env, b.env), cmp.Compare(a.entry, b.entry))
	})
	var out []ResourceConsumption
	var lims []Limitation
	for _, sl := range keys {
		c := ResourceConsumption{State: consumptionNotReported, Provenance: ProvenanceObserved, Reason: consumptionNotReportedReason}
		if len(slots[sl]) > 0 {
			c = usageConsumption(slots[sl], revs, current)
		}
		c.Environment = sl.env
		where := cmp.Or(sl.env, "default")
		if len(entries) > 1 {
			c.Entry, where = sl.entry, where+" for "+sl.entry
		}
		if c.Conflict != nil {
			lims = append(lims, Limitation{Code: LimitationUsageConflict, Message: fmt.Sprintf(
				"usage reports for %s on %s disagree in environment %s", use.Consumer.Label, use.Provider.Label, where)})
		}
		out = append(out, c)
	}
	return out, lims
}

// usageConsumption is one slot's consumption from its reports: the newest per
// (source, producer); only current ones (fresh, about the current consumer
// revision) can disagree or speak for the slot. Figures carry numbers only for
// a signed, fresh, Observed report about the current revision that no other
// current report contradicts. revs holds every record's digest: useSlots and
// matchUsage drop a record whose digest names no revision before it gets here.
func usageConsumption(recs []*UsageRecord, revs map[string]*ContractRevision, current *ContractRevision) ResourceConsumption {
	c := ResourceConsumption{State: consumptionUnknown, Provenance: ProvenanceObserved}
	latest := latestReports(recs)
	currentReports := slices.DeleteFunc(slices.Clone(latest), func(r *UsageRecord) bool {
		return r.Stale || (current != nil && revs[r.Digest].Key != current.Key)
	})
	if producers := usageConflict(currentReports); producers != nil {
		c.Conflict, c.Reason = producers, "the providers that report usage for this resource disagree"
		return c
	}
	newest := newestReport(latest)
	if len(currentReports) > 0 {
		newest = newestReport(currentReports)
	}
	asOf := newest.ObservedAt
	c.Provider, c.ExternalRef, c.AsOf = newest.Provider, newest.ExternalRef, &asOf
	c.Source, c.Producer, c.ProducerKeyID, c.Verified = newest.Source, newest.Producer, newest.ProducerKeyID, newest.Verified
	figures := &UsageFigures{Metrics: newest.Metrics, Cost: newest.Cost}
	rev := revs[newest.Digest]
	ref := revisionEntityRef(rev)
	c.Revision = &ref
	switch {
	case newest.Outcome != string(evidence.Observed):
		c.Reason = "the provider reported " + strings.ToLower(newest.Outcome)
	case newest.Stale:
		c.Stale, c.Reported, c.Reason = true, figures, "the last usage report is older than the provisioning window"
	case current != nil && rev.Key != current.Key:
		c.Reported, c.Reason = figures, fmt.Sprintf("last usage report is for %s; %s has no report yet", cmp.Or(rev.Version, rev.Digest), cmp.Or(current.Version, current.Digest))
	case !newest.Verified:
		c.Reported, c.Reason = figures, "the usage report is not signed"
	default:
		c.State, c.Figures = consumptionReported, figures
	}
	return c
}

// usageConflict is the sorted producers of the Observed current reports when
// they report different figures, nil when they agree. One producer seen
// through several sources counts once, by its newest report: a later sample
// supersedes an earlier one, it never disagrees with it.
func usageConflict(current []*UsageRecord) []string {
	byProducer := map[string]*UsageRecord{}
	for _, r := range current {
		if r.Outcome != string(evidence.Observed) {
			continue
		}
		p := cmp.Or(r.Producer, r.Source)
		if l, ok := byProducer[p]; !ok || l.at().Before(r.at()) {
			byProducer[p] = r
		}
	}
	figures := map[string]bool{}
	for _, r := range byProducer {
		figures[figuresKey(r)] = true
	}
	if len(figures) < 2 {
		return nil
	}
	return slices.Sorted(maps.Keys(byProducer))
}

// figuresKey compares two reports' figures, whatever order they list the
// metrics in.
func figuresKey(r *UsageRecord) string {
	f := UsageFigures{Metrics: slices.SortedFunc(slices.Values(r.Metrics), func(a, b evidence.UsageMetric) int { return cmp.Compare(a.Name, b.Name) }), Cost: r.Cost}
	b, _ := json.Marshal(f) // plain strings and validated finite numbers: never fails
	return string(b)
}
