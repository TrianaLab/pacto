package ci

import (
	"fmt"
	"time"

	"github.com/trianalab/pacto/v4/pkg/impact"
)

// Plan is one pacto ci run: every detected value with its source, one row
// per service folder and the gate the rows settle to.
type Plan struct {
	Platform string   `json:"platform"`
	Registry Value    `json:"registry"`
	Base     Value    `json:"base"`
	OIDC     bool     `json:"oidc"`
	Notes    []string `json:"notes,omitempty"`
	// Unreadable counts the folders the walk could not read: each may hide
	// a service, so the plan cannot read clear.
	Unreadable      int          `json:"unreadable,omitempty"`
	ConsumerSources []string     `json:"consumerSources,omitempty"`
	Snapshot        SnapshotInfo `json:"snapshot"`
	// Index says where the least complete impact answer read consumers from
	// in the organization index (IndexText, see NoteIndex), "" when no answer
	// read one.
	Index string `json:"index,omitempty"`
	// indexComplete is whether Index says complete.
	indexComplete bool
	Skipped       []Skip `json:"skipped,omitempty"`
	Rows          []Row  `json:"rows"`
	Gate          string `json:"gate"`
	Exit          int    `json:"exit"`
	// Reason is the line a non-zero exit writes to stderr, "" on exit 0.
	Reason string `json:"reason"`
	// Refused is why a publish run as a whole was refused, "" when it ran.
	Refused string `json:"refused,omitempty"`
}

// SnapshotInfo is the fleet snapshot impact ran over: when, how complete
// and what each consumer source answered. A plan where no row reached impact
// read no snapshot, so it carries no asOf.
type SnapshotInfo struct {
	AsOf         time.Time    `json:"asOf,omitzero"`
	Completeness string       `json:"completeness,omitempty"`
	Sources      []SourceLine `json:"sources,omitempty"`
}

// SourceLine is one consumer source and its status.
type SourceLine struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Skip is a folder the root .pactoignore hid, with the deciding rule.
type Skip struct {
	Rel  string `json:"rel"`
	Rule string `json:"rule"`
}

// Row is one service folder's plan.
type Row struct {
	Service        string   `json:"service"`
	Dir            string   `json:"dir"`
	Repository     string   `json:"repository,omitempty"`
	Changed        string   `json:"changed,omitempty"` // "yes", "no" or "" (not decided)
	Baseline       string   `json:"baseline,omitempty"`
	BaselineDigest string   `json:"baselineDigest,omitempty"`
	Next           string   `json:"next,omitempty"`
	Derived        bool     `json:"derived,omitempty"`
	Classification string   `json:"classification,omitempty"`
	Impact         Impact   `json:"impact"`
	Inferred       int      `json:"inferred"`
	Reason         string   `json:"reason,omitempty"`
	Notes          []string `json:"notes,omitempty"`
	Errors         []string `json:"errors,omitempty"`
	// Publish mode only (pacto ci --publish), empty in plan mode: the version
	// published, the tool that built the image, who signed, the bundle as
	// <ref>@<digest>, the outcome (a State constant) and the attempts made.
	To        string `json:"to,omitempty"`
	Tool      string `json:"tool,omitempty"`
	Identity  string `json:"identity,omitempty"`
	Published string `json:"published,omitempty"`
	State     string `json:"state,omitempty"`
	Attempts  int    `json:"attempts,omitempty"`
	// Images are the images the published bundle deploys, as
	// <repository>@<digest>, recorded with it.
	Images []string `json:"images,omitempty"`
}

// The outcomes of a row in publish mode.
const (
	StatePublished = "published"
	StateUnchanged = "unchanged"
	StateFailed    = "failed"
	StateBlocked   = "blocked"
	StateRefused   = "refused"
)

// Impact is a row's gate and the text the IMPACT column prints.
type Impact struct {
	Gate     string   `json:"gate"` // impact.GateClear, GateBlocked or GateUnverified
	Text     string   `json:"text"`
	Blocking []string `json:"blocking,omitempty"`
	// Sources are the consumer sources that answered for the row, empty
	// when none did or impact never ran.
	Sources []string `json:"sources"`
	// Digest is the content digest of a content-addressed consumer source,
	// empty for a live or file source.
	Digest string `json:"digest"`
}

// Settle redacts every note, reason and error the plan carries, then sets
// its gate, exit code and reason from its rows: any blocked row exits 1,
// else any unverified row or unreadable folder 2, else 0. No rows under root
// is unverified: nothing verified is not clear.
func (p *Plan) Settle(root string) {
	p.redact()
	p.Gate, p.Exit, p.Reason = impact.GateClear, 0, ""
	if len(p.Rows) == 0 {
		p.Gate, p.Exit, p.Reason = impact.GateUnverified, 2, "unverified: no pacto.yaml under "+root
		return
	}
	count := map[string]int{}
	for i, r := range p.Rows {
		count[r.Impact.Gate]++
		if r.Impact.Sources == nil {
			p.Rows[i].Impact.Sources = []string{}
		}
	}
	switch {
	case count[impact.GateBlocked] > 0:
		p.Gate, p.Exit = impact.GateBlocked, 1
	case count[impact.GateUnverified] > 0:
		p.Gate, p.Exit = impact.GateUnverified, 2
	case p.Unreadable > 0:
		p.Gate, p.Exit = impact.GateUnverified, 2
		p.Reason = fmt.Sprintf("unverified: %d folders could not be read", p.Unreadable)
		return
	default:
		return
	}
	p.Reason = fmt.Sprintf("%s: %d of %d services", p.Gate, count[p.Gate], len(p.Rows))
}

// SettlePublish redacts the plan, then sets a publish run's gate, exit code
// and reason from its rows: 1 when the run was refused, found no pacto.yaml
// under root, skipped a folder it could not read (a service there may have
// changed) or any row failed, was blocked or refused, else 0. Publish mode
// never exits 2: an unverified impact never stops a publish, the version
// rule and later gates protect consumers.
func (p *Plan) SettlePublish(root string) {
	p.redact()
	bad := 0
	for _, r := range p.Rows {
		if r.State == StateFailed || r.State == StateBlocked || r.State == StateRefused {
			bad++
		}
	}
	p.Gate, p.Exit, p.Reason = StatePublished, 0, ""
	switch {
	case p.Refused != "":
		p.Gate, p.Exit, p.Reason = StateRefused, 1, "refused: "+p.Refused
	case len(p.Rows) == 0:
		p.Gate, p.Exit, p.Reason = StateFailed, 1, "not published: no pacto.yaml under "+root
	case bad > 0:
		p.Gate, p.Exit, p.Reason = StateFailed, 1, fmt.Sprintf("not published: %d of %d services", bad, len(p.Rows))
	case p.Unreadable > 0:
		p.Gate, p.Exit, p.Reason = StateFailed, 1, fmt.Sprintf("not published: %d folders could not be read", p.Unreadable)
	}
}

// redact passes every text a pull request or a source can shape through
// Redact, before any renderer, the JSON or a record file reads it: a step
// summary can be public.
func (p *Plan) redact() {
	redactAll(p.Notes)
	p.Registry.Value = Redact(p.Registry.Value)
	for i := range p.Rows {
		r := &p.Rows[i]
		r.Impact.Text, r.Reason, r.Repository = Redact(r.Impact.Text), Redact(r.Reason), Redact(r.Repository)
		r.Published, r.Identity = Redact(r.Published), Redact(r.Identity)
		redactAll(r.Notes)
		redactAll(r.Errors)
	}
}

// redactAll redacts each string of ss in place.
func redactAll(ss []string) {
	for i, s := range ss {
		ss[i] = Redact(s)
	}
}
