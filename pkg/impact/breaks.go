package impact

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/semver"
)

// The states of an announced break for one consumer.
const (
	// BreakAnnounced: the provider announces the break for the consumer and
	// nothing accepts it yet, so the consumer keeps its verdict.
	BreakAnnounced = "announced"
	// BreakAccepted: the consumer's declared owner accepted the break with a
	// counted record, or its own ranges admit the new version: it never blocks.
	BreakAccepted = "accepted"
	// BreakExpired: the announcement expired, or records no expiry: unknown
	// never accepts, so the consumer keeps its verdict.
	BreakExpired = "expired"
)

// LimitationAcceptancesTruncated says an acceptance listing could not be
// read to the end without a counted record: the consumer reads unverified.
const LimitationAcceptancesTruncated = "ACCEPTANCES_TRUNCATED"

// NoteAcceptancesNotRead starts the note of an answer whose caller read no
// signed acceptances: nothing reads accepted by a signature, which is
// unknown, never a checked negative.
const NoteAcceptancesNotRead = "ACCEPTANCES_NOT_READ"

// reasonNotRead is why an announced break reads no acceptance when none
// was read.
const reasonNotRead = "acceptances not read: the CLI, a stdio MCP server or a signed-in dashboard viewer reads them"

// BreakAnnouncement is a provider's breaks[] entry for one consumer: why it
// breaks, the digest of the entry, its expiry (YYYY-MM-DD, "" when none is
// recorded) and where that expiry comes from.
type BreakAnnouncement struct {
	Reason, Announcement, Expires, ExpiresSource string
}

// BreakInput is what the caller read about the breaks the new revision
// announces: by consumer name, and the acceptances attached to a consumer's
// revisions with whether that listing is complete.
type BreakInput struct {
	Announced   map[string]BreakAnnouncement
	Acceptances func(consumer fleet.ServiceKey) ([]Acceptance, bool)
}

// BreakState is where an announced break stands for one consumer.
type BreakState struct {
	State   string `json:"state" enum:"announced,accepted,expired" doc:"announced: the provider announces the break and nothing accepts it; accepted: the consumer's declared owner signed a counted acceptance, or its ranges admit the new version; expired: the announcement expired or records no expiry"`
	By      string `json:"by,omitempty" doc:"Who signed the counted acceptance"`
	Record  string `json:"record,omitempty" doc:"Where the counted acceptance is: <repository>@sha256:<hex>"`
	Expires string `json:"expires,omitempty" doc:"When the announcement expires (YYYY-MM-DD); empty when none is recorded"`
	Route   string `json:"route,omitempty" doc:"Whom an acceptance by somebody other than the declared owner waits for, such as 'waiting for the owner of reports: ana@acme.com', or how to get an owner"`
	Reason  string `json:"reason,omitempty" doc:"Why the break is not accepted, in plain words; it says when acceptances were not read"`
}

// Text says where the break stands for consumer.
func (b *BreakState) Text(consumer string) string {
	var text string
	switch {
	case b.State == BreakExpired:
		text = "expired " + cmp.Or(b.Expires, "-")
	case b.State == BreakAccepted && b.By != "":
		text = "accepted for " + consumer + " (" + b.By + ", " + b.Record + ")"
	case b.State == BreakAccepted:
		text = "accepted for " + consumer
	case b.Reason == reasonNotRead:
		text = "announced for " + consumer
	default:
		text = "announced for " + consumer + ", not accepted"
	}
	for _, more := range []string{b.Reason, b.Route} {
		if more != "" {
			text += "; " + more
		}
	}
	return text
}

// applyBreaks gives each announced, direct, declared consumer of a BREAKING
// change its break state. Only a counted acceptance of exactly this break
// (provider, version and announcement digest) or the consumer's own ranges
// accept it; an expired or undated announcement never does.
func (r *Result) applyBreaks(snap *fleet.FleetSnapshot, in *BreakInput, changed fleet.ServiceKey) {
	if r.Classification != "BREAKING" {
		return
	}
	for i := range r.Consumers {
		c := &r.Consumers[i]
		ann, ok := in.Announced[c.Service]
		if !ok || !c.Direct || c.Provenance == fleet.ProvenanceInferred || c.Uncontracted {
			continue
		}
		c.Break = r.breakState(snap, in, changed, *c, ann)
	}
}

// breakState is the state of ann for consumer c.
func (r *Result) breakState(snap *fleet.FleetSnapshot, in *BreakInput, changed fleet.ServiceKey, c AffectedConsumer, ann BreakAnnouncement) *BreakState {
	st := &BreakState{State: BreakExpired, Expires: ann.Expires}
	if st.Reason = ExpiredBreak(ann.Expires, r.AsOf); st.Reason != "" {
		return st
	}
	key := fleet.NewServiceKeyDomain(c.Domain, c.Service)
	want := BreakRef{Provider: string(changed), Version: r.NewVersion, Announcement: ann.Announcement}
	st.State = BreakAnnounced
	var accs []Acceptance
	complete, unread := true, ""
	if in.Acceptances != nil {
		accs, complete = in.Acceptances(key)
	} else {
		unread = reasonNotRead
		r.noteAcceptancesNotRead()
	}
	var proposal string
	for _, a := range accs {
		if a.Break == nil || *a.Break != want {
			continue
		}
		switch a.State {
		case AcceptCounted:
			st.State, st.By, st.Record, st.Reason = BreakAccepted, a.By, a.Record, ""
			return st
		case AcceptRouted:
			st.Route = a.Reason
		case AcceptProposal:
			proposal = a.Reason
		}
	}
	if !complete {
		r.markAcceptancesTruncated(c.Service)
	}
	admits, why := rangeAdmits(snap, key, changed, r.NewVersion)
	if admits && len(c.Unmet) == 0 {
		st.State, why = BreakAccepted, c.Service+"'s ranges admit "+r.NewVersion
	}
	st.Reason = cmp.Or(why, proposal, unread)
	if st.State == BreakAccepted {
		st.Reason = why
	}
	return st
}

// noteAcceptancesNotRead says, once, that the answer read no signed
// acceptances.
func (r *Result) noteAcceptancesNotRead() {
	note := NoteAcceptancesNotRead + ": signed acceptances were not read for this answer, so no announced break or range reads accepted by one; " +
		"the CLI, a stdio MCP server or a signed-in dashboard viewer reads them"
	if !slices.Contains(r.Notes, note) {
		r.Notes = append(r.Notes, note)
	}
}

// markAcceptancesTruncated makes the answer partial: an acceptance of
// consumer may lie past what was read.
func (r *Result) markAcceptancesTruncated(consumer string) {
	r.Completeness = fleet.CompletenessPartial
	r.Limitations = append(r.Limitations, fleet.Limitation{Code: LimitationAcceptancesTruncated, Source: limitationSource,
		Message: "the acceptances attached to " + consumer + " could not all be read, so it reads unverified"})
}

// ExpiredBreak says why an announcement expiring on expires (YYYY-MM-DD)
// no longer applies at asOf, "" while it does: the expiry applies through
// its whole day, and no expiry, or one that does not read, is unknown,
// which never accepts. The gate and the signer's judge share it.
func ExpiredBreak(expires string, asOf time.Time) string {
	if expires == "" {
		return "expiry unknown: run pacto ci or declare expires"
	}
	at, err := time.Parse(time.DateOnly, expires)
	switch {
	case err != nil:
		return "expiry " + expires + " is not a YYYY-MM-DD date"
	case !asOf.Before(at.AddDate(0, 0, 1)):
		return "the break expired on " + expires
	}
	return ""
}

// rangeAdmits reports whether consumer's ranges admit version of changed:
// its newest revision declares a range that admits it and every revision
// running on a target admits it too. A consumer no target holds may run
// where no source looks, so where it runs is unknown and never admits. When
// only the newest does, why names the running revision that does not.
func rangeAdmits(snap *fleet.FleetSnapshot, consumer, changed fleet.ServiceKey, version string) (bool, string) {
	newest := newestRevision(snap, consumer)
	_, name := fleet.ParseServiceKey(consumer)
	if newest == nil || !revisionAdmits(snap, newest.Key, changed, version) {
		return false, ""
	}
	found := false
	for _, k := range slices.Sorted(maps.Keys(snap.Targets)) {
		t := snap.Targets[k]
		if t.ServiceKey != consumer {
			continue
		}
		found = true
		rev := snap.Revisions[t.ContractRevision]
		if rev == nil || !revisionAdmits(snap, rev.Key, changed, version) {
			running := "an unknown revision"
			if rev != nil {
				running = rev.Version
			}
			cluster, scope, _, _, _ := fleet.ParseTargetKey(k)
			return false, name + " " + newest.Version + " admits " + version + "; running " + running + " (" + strings.Trim(cluster+"/"+scope, "/") + ") does not"
		}
	}
	if !found {
		return false, name + " " + newest.Version + " admits " + version + "; where it runs is unknown: add a source that knows where services run (for example --target-state)"
	}
	return true, ""
}

// newestRevision is service's highest-versioned revision, nil when the
// snapshot holds none.
func newestRevision(snap *fleet.FleetSnapshot, service fleet.ServiceKey) *fleet.ContractRevision {
	var newest *fleet.ContractRevision
	for _, rev := range snap.Revisions {
		if rev.ServiceKey == service && (newest == nil || newerRevision(rev, newest)) {
			newest = rev
		}
	}
	return newest
}

// newerRevision reports whether a's version is higher than b's.
func newerRevision(a, b *fleet.ContractRevision) bool {
	if c := semver.Compare(a.Version, b.Version); c != 0 {
		return c > 0
	}
	return a.Key > b.Key
}

// revisionAdmits reports whether revision rev declares a dependency on
// changed whose range admits version.
func revisionAdmits(snap *fleet.FleetSnapshot, rev fleet.RevisionKey, changed fleet.ServiceKey, version string) bool {
	found := false
	for _, rel := range snap.Relationships {
		if rel.FromRevision != rev || rel.ToService != changed || rel.Type != fleet.RelationshipDependency || rel.Provenance != fleet.ProvenanceDeclared {
			continue
		}
		if compatibilityVerdict(rel.Compatibility, version, true) != CompatibilityCompatible {
			return false
		}
		found = true
	}
	return found
}

// acceptedConsumers names the consumers that accepted the break, each with
// who signed or why it is accepted.
func (r *Result) acceptedConsumers() []string {
	var out []string
	for _, c := range r.Consumers {
		if c.Break == nil || c.Break.State != BreakAccepted {
			continue
		}
		if c.Break.By != "" {
			out = append(out, c.Service+" (signed by "+c.Break.By+")")
		} else {
			out = append(out, c.Service+" ("+c.Break.Reason+")")
		}
	}
	return out
}
