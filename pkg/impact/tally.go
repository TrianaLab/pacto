package impact

import (
	"sort"
	"strconv"
	"strings"
)

// Consumer outcomes: a DISJOINT partition of the consumers by what the change does
// to each one. byVerdict judges the declared range alone, and Refused overlaps it;
// an outcome folds the two so the buckets sum to the consumer total and can be drawn
// as lanes. A consumer that refuses a required guarantee is incompatible whatever
// its range accepts, so Refused is always a subset of the incompatible outcome.
// Unclassified holds a verdict this engine does not recognize, so a value a newer
// engine emits is counted rather than dropped.
const (
	OutcomeIncompatible = "incompatible"
	OutcomeUnknown      = "unknown"
	OutcomeCompatible   = "compatible"
	OutcomeUnclassified = "unclassified"
)

// outcomeOrder is the canonical display order: err, unknown, ok, then the shortfall.
var outcomeOrder = []string{OutcomeIncompatible, OutcomeUnknown, OutcomeCompatible, OutcomeUnclassified}

// MaxOwnerRanking bounds [ConsumerTallies.ByOwner].
const MaxOwnerRanking = 5

// OwnerTally is one owner's share of the affected consumers.
type OwnerTally struct {
	Owner     string `json:"owner"`
	Consumers int    `json:"consumers"`
	// Incompatible counts this owner's consumers whose outcome is incompatible.
	Incompatible int `json:"incompatible"`
}

// ConsumerTallies are computed over EVERY consumer of the answer (the caller's view
// of the snapshot when it is restricted), never over a page. Consumers hidden from
// the caller are in no bucket and are never named. They hold:
//
//	Sum(ByOutcome.Count) == consumers
//	Sum(ByOwner.Consumers) + BeyondRanking + NoOwner == consumers
type ConsumerTallies struct {
	ByOutcome []TallyBucket `json:"byOutcome"`
	// ByOwner ranks owners by consumers that break, then by consumers, then by name,
	// bounded to MaxOwnerRanking.
	ByOwner []OwnerTally `json:"byOwner"`
	// BeyondRanking counts the consumers of owners ranked past MaxOwnerRanking.
	BeyondRanking int `json:"beyondRanking"`
	// NoOwner counts the consumers with no identified owner.
	NoOwner int `json:"noOwner"`
	// Owners counts the distinct identified owners of the consumers.
	Owners int `json:"owners"`
}

func outcomeOf(verdict string, refused bool) string {
	switch {
	case refused || verdict == CompatibilityIncompatible:
		return OutcomeIncompatible
	case verdict == CompatibilityUnknown:
		return OutcomeUnknown
	case verdict == CompatibilityCompatible:
		return OutcomeCompatible
	}
	return OutcomeUnclassified
}

type consumerFact struct {
	verdict, owner string
	refused        bool
}

func tallyFacts(facts []consumerFact) ConsumerTallies {
	byOutcome := map[string]int{}
	byOwner := map[string]*OwnerTally{}
	var t ConsumerTallies
	for _, f := range facts {
		o := outcomeOf(f.verdict, f.refused)
		byOutcome[o]++
		if f.owner == "" {
			t.NoOwner++
			continue
		}
		ot := byOwner[f.owner]
		if ot == nil {
			ot = &OwnerTally{Owner: f.owner}
			byOwner[f.owner] = ot
		}
		ot.Consumers++
		if o == OutcomeIncompatible {
			ot.Incompatible++
		}
	}
	for _, k := range outcomeOrder {
		t.ByOutcome = append(t.ByOutcome, TallyBucket{Key: k, Count: byOutcome[k]})
	}
	ranked := make([]OwnerTally, 0, len(byOwner))
	for _, o := range byOwner {
		ranked = append(ranked, *o)
	}
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.Incompatible != b.Incompatible {
			return a.Incompatible > b.Incompatible
		}
		if a.Consumers != b.Consumers {
			return a.Consumers > b.Consumers
		}
		return a.Owner < b.Owner
	})
	t.Owners = len(ranked)
	for _, o := range ranked[min(len(ranked), MaxOwnerRanking):] {
		t.BeyondRanking += o.Consumers
	}
	t.ByOwner = ranked[:min(len(ranked), MaxOwnerRanking)]
	return t
}

// tally fills the consumer tallies from the finished consumer list.
func (r *Result) tally() {
	facts := make([]consumerFact, 0, len(r.Consumers))
	for _, c := range r.Consumers {
		facts = append(facts, consumerFact{verdict: c.CompatibilityVerdict, owner: c.Owner, refused: len(c.Unmet) > 0})
	}
	r.ConsumerTallies = tallyFacts(facts)
}

func tallyProductConsumers(all []ProductConsumer) ConsumerTallies {
	facts := make([]consumerFact, 0, len(all))
	for _, c := range all {
		facts = append(facts, consumerFact{verdict: c.CompatibilityVerdict, owner: c.Owner, refused: len(c.Unmet) > 0})
	}
	return tallyFacts(facts)
}

// OutcomeSummary is the outcome tally in words: "2 incompatible, 1 unknown, 9
// compatible", with unclassified only when some consumer is.
func (t ConsumerTallies) OutcomeSummary() string {
	parts := make([]string, 0, len(t.ByOutcome))
	for _, o := range t.ByOutcome {
		if o.Count > 0 || o.Key != OutcomeUnclassified {
			parts = append(parts, strconv.Itoa(o.Count)+" "+o.Key)
		}
	}
	return strings.Join(parts, ", ")
}

// OwnerSummary is the owner ranking in words, with what it leaves out: "team-a 4 (4
// incompatible), team-b 9; 2 more of other owners; 4 with no identified owner".
func (t ConsumerTallies) OwnerSummary() string {
	parts := make([]string, 0, len(t.ByOwner))
	for _, o := range t.ByOwner {
		s := o.Owner + " " + strconv.Itoa(o.Consumers)
		if o.Incompatible > 0 {
			s += " (" + strconv.Itoa(o.Incompatible) + " incompatible)"
		}
		parts = append(parts, s)
	}
	out := strings.Join(parts, ", ")
	if t.BeyondRanking > 0 {
		out += "; " + strconv.Itoa(t.BeyondRanking) + " more of other owners"
	}
	if t.NoOwner > 0 {
		out += "; " + strconv.Itoa(t.NoOwner) + " with no identified owner"
	}
	return strings.TrimPrefix(out, "; ")
}
