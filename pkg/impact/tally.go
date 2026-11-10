package impact

import (
	"sort"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
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
	// OutcomeBreaks and OutcomeMayDegrade partition an event answer, with
	// OutcomeUnknown, by effect.
	OutcomeBreaks     = EffectBreaks
	OutcomeMayDegrade = EffectMayDegrade
)

// outcomeOrder is the canonical display order: err, unknown, ok, then the shortfall.
var outcomeOrder = []string{OutcomeIncompatible, OutcomeUnknown, OutcomeCompatible, OutcomeUnclassified}

// eventOrder is the display order of an event answer's outcomes: its lanes.
var eventOrder = []string{OutcomeBreaks, OutcomeMayDegrade, OutcomeUnknown}

// orderFor is the outcome order of an answer, event or contract.
func orderFor(event bool) []string {
	if event {
		return eventOrder
	}
	return outcomeOrder
}

// MaxOwnerRanking bounds [ConsumerTallies.ByOwner].
const MaxOwnerRanking = 5

// OwnerTally is one owner's share of the affected consumers.
type OwnerTally struct {
	// Owner is the owner's display label. OwnerKey is its canonical key, empty
	// when the consumers name an owner with no canonical identity.
	Owner     string `json:"owner"`
	OwnerKey  string `json:"ownerKey,omitempty" doc:"The owner's canonical key (for example team:shop): the ranking groups by it"`
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

// outcomeOf is a consumer's outcome: its effect in an event answer, else
// what its verdict and refused guarantees say.
func outcomeOf(verdict string, refused bool, effect string) string {
	switch {
	case effect != "":
		return effect
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
	verdict, owner, ownerKey, effect string
	refused                          bool
}

func tallyFacts(facts []consumerFact, event bool) ConsumerTallies {
	byOutcome := map[string]int{}
	byOwner := map[string]*OwnerTally{}
	var t ConsumerTallies
	for _, f := range facts {
		o := outcomeOf(f.verdict, f.refused, f.effect)
		byOutcome[o]++
		if f.owner == "" {
			t.NoOwner++
			continue
		}
		// Group by canonical key when there is one: two owners can share a label.
		group := "label:" + f.owner
		if f.ownerKey != "" {
			group = "key:" + f.ownerKey
		}
		ot := byOwner[group]
		if ot == nil {
			ot = &OwnerTally{Owner: f.owner, OwnerKey: f.ownerKey}
			byOwner[group] = ot
		}
		ot.Consumers++
		// In an event answer the consumers that break rank an owner.
		if o == OutcomeIncompatible || o == OutcomeBreaks {
			ot.Incompatible++
		}
	}
	for _, k := range orderFor(event) {
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
		if a.Owner != b.Owner {
			return a.Owner < b.Owner
		}
		return a.OwnerKey < b.OwnerKey
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
		facts = append(facts, consumerFact{verdict: c.CompatibilityVerdict, owner: c.Owner, ownerKey: c.OwnerKey, effect: c.Effect, refused: len(c.Unmet) > 0})
	}
	r.ConsumerTallies = tallyFacts(facts, r.Event != nil)
}

func tallyProductConsumers(all []ProductConsumer, event bool) ConsumerTallies {
	facts := make([]consumerFact, 0, len(all))
	for _, c := range all {
		facts = append(facts, consumerFact{verdict: c.CompatibilityVerdict, owner: c.Owner, ownerKey: c.OwnerKey, effect: c.Effect, refused: len(c.Unmet) > 0})
	}
	return tallyFacts(facts, event)
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
// Rows that share a label name their namespace ("shop (Team)", "shop (DRI)").
func (t ConsumerTallies) OwnerSummary() string {
	seen := map[string]int{}
	for _, o := range t.ByOwner {
		seen[o.Owner]++
	}
	parts := make([]string, 0, len(t.ByOwner))
	for _, o := range t.ByOwner {
		label := o.Owner
		if k, err := contract.ParseOwnerKey(o.OwnerKey); err == nil && seen[o.Owner] > 1 {
			label = k.Label()
		}
		s := label + " " + strconv.Itoa(o.Consumers)
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
