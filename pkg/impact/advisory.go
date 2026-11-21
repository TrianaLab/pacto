package impact

import (
	"strings"

	"github.com/trianalab/pacto/v4/pkg/diff"
)

// InferredAdvisory is a possible break on an inferred interface with the
// direct consumers it would hit. It never enters Consumers, the tallies or a
// verdict.
type InferredAdvisory struct {
	diff.Advisory
	Consumers []AdvisoryConsumer `json:"consumers" doc:"The direct consumers of the changed service the possible break would hit, each inferred with compatibility unknown, bounded to 500"`
}

// AdvisoryConsumer is a consumer an advisory names: always inferred, with
// compatibility unknown, because no declared interface says what it calls.
type AdvisoryConsumer struct {
	Service       string     `json:"service"`
	Confidence    Confidence `json:"confidence" enum:"inferred"`
	Compatibility string     `json:"compatibility" enum:"unknown"`
}

// Line is the advisory in one line: "possible break (inferred) <service>
// <summary>; consumers (inferred, unknown): <names>".
func (a InferredAdvisory) Line(service string) string {
	names := make([]string, 0, len(a.Consumers))
	for _, c := range a.Consumers {
		names = append(names, c.Service)
	}
	who := "none"
	if len(names) > 0 {
		who = strings.Join(names, ", ")
	}
	return "possible break (inferred) " + service + " " + a.Summary() + "; consumers (inferred, unknown): " + who
}

// inferredAdvisories wraps the diff's advisories, without consumers yet.
func inferredAdvisories(ds []diff.Advisory) []InferredAdvisory {
	var out []InferredAdvisory
	for _, d := range ds {
		out = append(out, InferredAdvisory{Advisory: d, Consumers: []AdvisoryConsumer{}})
	}
	return out
}

// adviseConsumers names on every advisory the direct consumers of the
// answer, in answer order, bounded to MaxProductConsumers.
func (r *Result) adviseConsumers() {
	for i := range r.Advisories {
		for _, c := range r.Consumers {
			if c.Direct && len(r.Advisories[i].Consumers) < MaxProductConsumers {
				r.Advisories[i].Consumers = append(r.Advisories[i].Consumers,
					AdvisoryConsumer{Service: c.Service, Confidence: ConfidenceInferred, Compatibility: CompatibilityUnknown})
			}
		}
	}
}

// advisoryGate is the reason a non-BREAKING answer with an advisory stays
// unverified, "" otherwise. A BREAKING answer keeps its own gate.
func (r *Result) advisoryGate() string {
	if len(r.Advisories) == 0 || r.Classification == "BREAKING" {
		return ""
	}
	a := r.Advisories[0]
	return "a possible break on inferred interface " + a.Interface + " (" + a.Source + "): declare the interface to classify the change"
}
