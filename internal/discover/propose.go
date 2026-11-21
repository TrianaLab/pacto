package discover

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// breakExpiryDays is how long an undated break applies after its commit.
const breakExpiryDays = 30

// Sourced is one proposed value and where it came from.
type Sourced struct{ Value, Source string }

// ProposeInput is what pacto ci read for one service's proposals: by
// dependency name, the ref its organization index entry names (index:
// source) and the versions of the provider running beside this service as
// "2.3.1, 2.4.0" (snapshot: source); and HEAD's commit and commit time
// (Unix seconds), from which an undated break expires.
type ProposeInput struct {
	Refs, Running map[string]Sourced
	Commit, Epoch string
}

// BreakExpiry is the default expiry of an undated break committed at epoch
// (Unix seconds) by commit: 30 days later as YYYY-MM-DD, and the source that
// says so. Both are "" when either input is unknown.
func BreakExpiry(epoch int64, commit string) (date, source string) {
	if epoch <= 0 || len(commit) < 12 {
		return "", ""
	}
	return time.Unix(epoch, 0).UTC().AddDate(0, 0, breakExpiryDays).Format(time.DateOnly),
		contract.SourceCommit + commit[:12] + " +" + strconv.Itoa(breakExpiryDays) + "d"
}

// Propose is a copy of rec with what pacto ci proposes for c from in: the
// ref of each dependency by name, a ^MAJOR.MINOR.0 range for each dependency
// whose range is unknown, and the expiry of each undated break. A proposal
// is never declared: it reads inferred until a person accepts it. rec is
// never changed; a nil rec with nothing to propose stays nil.
func Propose(rec *contract.InferredRecord, c *contract.Contract, in ProposeInput) *contract.InferredRecord {
	var add []contract.Inferred
	for _, d := range c.Dependencies {
		if d.External {
			continue
		}
		if f, ok := proposeRef(d, in); ok {
			add = append(add, f)
		}
		if f, ok := proposeRange(d, in); ok {
			add = append(add, f)
		}
	}
	add = append(add, proposeExpiries(c, in)...)
	if len(add) == 0 {
		return rec
	}
	out := &contract.InferredRecord{Format: contract.InferredRecordFormat}
	if rec != nil {
		cp := *rec
		cp.Fields = slices.Clone(rec.Fields)
		out = &cp
	}
	for _, f := range add {
		addField(out, f)
	}
	return out
}

// proposeRef is the ref proposal of d, a dependency by name.
func proposeRef(d contract.Dependency, in ProposeInput) (contract.Inferred, bool) {
	r, ok := in.Refs[d.Name]
	if d.Ref != "" || !ok {
		return contract.Inferred{}, false
	}
	return contract.Inferred{Field: "dependencies[" + d.Name + "].ref", Value: r.Value, Source: r.Source,
		Confidence: contract.ConfidenceLikely, Declare: "ref: " + r.Value}, true
}

// proposeRange is the range proposal of d when its range is unknown and the
// provider runs beside this service.
func proposeRange(d contract.Dependency, in ProposeInput) (contract.Inferred, bool) {
	run, ok := in.Running[d.Name]
	if !ok || d.Compatibility != "" && d.Compatibility != contract.CompatibilityUnknown {
		return contract.Inferred{}, false
	}
	proposed := contract.ProposedRange(strings.Split(run.Value, ", "))
	if proposed == "" {
		return contract.Inferred{}, false
	}
	return contract.Inferred{Field: "dependencies[" + d.Name + "].compatibility", Value: "compatibility " + contract.CompatibilityUnknown,
		Proposed: proposed, Source: run.Source, Confidence: contract.ConfidenceLikely, Declare: "compatibility: " + strconv.Quote(proposed)}, true
}

// proposeExpiries are the default expiries of c's undated breaks.
func proposeExpiries(c *contract.Contract, in ProposeInput) []contract.Inferred {
	epoch, _ := strconv.ParseInt(in.Epoch, 10, 64) // an unreadable epoch is unknown: no expiry
	date, source := BreakExpiry(epoch, in.Commit)
	if date == "" {
		return nil
	}
	var out []contract.Inferred
	for i, b := range c.Breaks {
		if b.Expires == "" {
			out = append(out, contract.Inferred{Field: "breaks[" + strconv.Itoa(i) + "].expires", Value: date, Source: source,
				Confidence: contract.ConfidenceLikely, Declare: "expires: " + strconv.Quote(date)})
		}
	}
	return out
}
