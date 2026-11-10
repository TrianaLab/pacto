package impact

import (
	"sort"
	"strconv"
	"time"

	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// MaxConsumerEvidence bounds [AffectedConsumer.Evidence].
const MaxConsumerEvidence = 20

// Evidence kinds (see [Evidence.Kind]).
const (
	EvidenceObservedCall = "observed-call"
	EvidenceTarget       = "target"
	EvidenceUnreachable  = "unreachable"
)

// tracesFileSource names the source of an observed call read from a traces
// file, which carries no source id of its own.
const tracesFileSource = "traces file"

// Evidence is one fact behind an affected consumer, with its age.
type Evidence struct {
	Kind       string     `json:"kind" enum:"observed-call,target,unreachable,lifecycle" doc:"What the fact is: an observed call to the changed service, a target the consumer runs on, a finding that the consumer cannot reach the changed service or the provider's declared lifecycle. Provisioning reports are in resources"`
	Source     string     `json:"source,omitempty" doc:"Where the fact comes from: the observation source id, the traces file or the target key"`
	At         *time.Time `json:"at,omitempty" doc:"When the fact was last seen. Empty when the source records no time"`
	Stale      bool       `json:"stale,omitempty" doc:"The fact is older than 24h before the snapshot's asOf, or has no time, so it does not count as current"`
	Provenance string     `json:"provenance" enum:"declared,observed" doc:"Whether a contract declares the fact or a source observed it"`
	Message    string     `json:"message" doc:"The fact in plain words"`
}

// staleAt reports whether evidence seen at at is older than the recent window
// before the snapshot's own time. Evidence with no time is stale.
func staleAt(snap *fleet.FleetSnapshot, at *time.Time) bool {
	return at == nil || at.Before(snap.GeneratedAt.Add(-fleet.RecentEvidenceWindow))
}

// consumerEvidence lists the facts behind the consumer key of changed: the
// targets it runs on, the observed calls to changed and the findings on its
// targets that it cannot reach changed. stale reports that any of them is
// older than the window, which makes the consumer's confidence unknown.
func consumerEvidence(snap *fleet.FleetSnapshot, key, changed fleet.ServiceKey, observed []resolvedObservedEdge) (out []Evidence, stale bool) {
	if s := snap.Services[key]; s != nil {
		deps := declaredDependencyNames(snap, key, changed)
		for _, tk := range s.Targets {
			out = append(out, targetEvidence(snap, snap.Targets[tk], tk, deps)...)
		}
	}
	for _, e := range observed {
		if e.consumer == key && e.provider == changed {
			out = append(out, observedEvidence(e))
		}
	}
	for _, e := range out {
		stale = stale || e.Stale
	}
	return boundEvidence(out), stale
}

// declaredDependencyNames are the names under which the consumer's declared
// dependencies reach changed, as an unreachable finding names them.
func declaredDependencyNames(snap *fleet.FleetSnapshot, consumer, changed fleet.ServiceKey) map[string]bool {
	out := map[string]bool{}
	for _, r := range snap.Relationships {
		if r.Type == fleet.RelationshipDependency && r.Provenance == fleet.ProvenanceDeclared && r.FromService == consumer && r.ToService == changed {
			out[r.To] = true
		}
	}
	return out
}

// targetEvidence is the target entry for one target of a consumer, followed by
// one entry per finding there that the consumer cannot reach a dependency
// named in deps.
func targetEvidence(snap *fleet.FleetSnapshot, t *fleet.TargetRecord, key fleet.TargetKey, deps map[string]bool) []Evidence {
	if t == nil {
		return []Evidence{{Kind: EvidenceTarget, Source: string(key), Stale: true, Provenance: fleet.ProvenanceObserved,
			Message: "the target is not in the snapshot"}}
	}
	stale := t.Stale || staleAt(snap, t.EvidenceAt)
	msg := "no evidence time"
	if t.EvidenceAt != nil {
		msg = "evidence " + snap.GeneratedAt.Sub(*t.EvidenceAt).Round(time.Minute).String() + " old, window 24h"
	}
	if t.Stale {
		msg += "; the source marks it stale"
	}
	out := []Evidence{{Kind: EvidenceTarget, Source: string(key), At: copyTime(t.EvidenceAt), Stale: stale, Provenance: fleet.ProvenanceObserved, Message: msg}}
	for _, f := range t.Findings {
		if f.Code == finding.CodeDependencyUnreachable && f.Subject.Kind == "dependency" && deps[f.Subject.Name] {
			out = append(out, Evidence{Kind: EvidenceUnreachable, Source: string(key), At: copyTime(t.EvidenceAt), Stale: stale,
				Provenance: fleet.ProvenanceObserved, Message: f.Message})
		}
	}
	return out
}

// observedEvidence is the entry for one observed call to the changed service.
func observedEvidence(e resolvedObservedEdge) Evidence {
	msg := "calls seen"
	if e.count > 0 {
		msg = strconv.Itoa(e.count) + " " + plural(e.count, "call", "calls") + " seen"
	}
	switch {
	case e.lastSeen == nil:
		msg += ", no observation window"
	case e.stale:
		msg += ", last seen more than 24h before asOf"
	}
	return Evidence{Kind: EvidenceObservedCall, Source: e.source, At: copyTime(e.lastSeen), Stale: e.stale, Provenance: fleet.ProvenanceObserved, Message: msg}
}

// boundEvidence keeps the newest MaxConsumerEvidence entries (entries with no
// time last) and says in the last kept entry how many it dropped.
func boundEvidence(ev []Evidence) []Evidence {
	sort.SliceStable(ev, func(i, j int) bool {
		a, b := ev[i].At, ev[j].At
		if (a == nil) != (b == nil) {
			return a != nil
		}
		return a != nil && a.After(*b)
	})
	if len(ev) <= MaxConsumerEvidence {
		return ev
	}
	dropped := len(ev) - MaxConsumerEvidence
	ev = ev[:MaxConsumerEvidence]
	ev[MaxConsumerEvidence-1].Message += "; " + strconv.Itoa(dropped) + " older entries not shown"
	return ev
}

func copyTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}
