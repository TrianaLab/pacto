package ci

import (
	"time"

	"github.com/trianalab/pacto/v4/pkg/impact"
)

// Plan is one pacto ci run: every detected value with its source, one row
// per service folder and the gate the rows settle to.
type Plan struct {
	Platform        string       `json:"platform"`
	Registry        Value        `json:"registry"`
	Base            Value        `json:"base"`
	OIDC            bool         `json:"oidc"`
	Notes           []string     `json:"notes,omitempty"`
	ConsumerSources []string     `json:"consumerSources,omitempty"`
	Snapshot        SnapshotInfo `json:"snapshot"`
	Skipped         []Skip       `json:"skipped,omitempty"`
	Rows            []Row        `json:"rows"`
	Gate            string       `json:"gate"`
	Exit            int          `json:"exit"`
}

// SnapshotInfo is the fleet snapshot impact ran over: when, how complete
// and what each consumer source answered.
type SnapshotInfo struct {
	AsOf         time.Time    `json:"asOf"`
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
}

// Impact is a row's gate and the text the IMPACT column prints.
type Impact struct {
	Gate     string   `json:"gate"` // impact.GateClear, GateBlocked or GateUnverified
	Text     string   `json:"text"`
	Blocking []string `json:"blocking,omitempty"`
}

// Settle sets the plan's gate and exit code from its rows: any blocked row
// exits 1, else any unverified row 2, else 0. No rows is unverified: nothing
// verified is not clear.
func (p *Plan) Settle() {
	p.Gate, p.Exit = impact.GateClear, 0
	if len(p.Rows) == 0 {
		p.Gate, p.Exit = impact.GateUnverified, 2
	}
	for _, r := range p.Rows {
		switch {
		case r.Impact.Gate == impact.GateBlocked:
			p.Gate, p.Exit = impact.GateBlocked, 1
		case r.Impact.Gate == impact.GateUnverified && p.Exit == 0:
			p.Gate, p.Exit = impact.GateUnverified, 2
		}
	}
}
