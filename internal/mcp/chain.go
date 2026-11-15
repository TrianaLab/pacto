package mcp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// EditOptions are what the server that registers pacto_edit adds to an edit.
// It is a Go argument, never part of the tool's input.
type EditOptions struct {
	// Impact analyses the change from the contract in oldDir to the edited
	// bundle staged in newDir over the session's fleet snapshot; nil when the
	// session has no snapshot.
	Impact func(ctx context.Context, oldDir, newDir string) (*impact.Result, error)
	// Served reports whether this session registers a tool; nil serves none.
	Served func(tool string) bool
}

// EditImpact is the part of an impact answer an edit needs before it runs.
type EditImpact struct {
	Classification string `json:"classification"`
	Gate           string `json:"gate"`
	GateReason     string `json:"gateReason"`
	// Affected counts the consumers the answer lists.
	Affected int      `json:"affected"`
	Owners   []string `json:"owners,omitempty"`
	// Approvals is who the promote may ask, inferred from the gate.
	Approvals    string   `json:"approvals"`
	Completeness string   `json:"completeness"`
	AsOf         string   `json:"asOf"`
	SnapshotID   string   `json:"snapshotId"`
	Limitations  []string `json:"limitations,omitempty"`
}

// ChainStep is one step that carries a change to running. Tool is empty when
// this session does not serve it; Command is the CLI form either way.
type ChainStep struct {
	Tool    string `json:"tool,omitempty"`
	Command string `json:"command"`
	Why     string `json:"why"`
}

// maxEditList bounds Owners and Limitations; the last entry counts the rest.
const maxEditList = 20

const (
	noSnapshot     = "this session has no fleet snapshot, so who this edit affects is not known"
	providerRuns   = "the provider: an adapter or GitOps tool acts on the published revision; Pacto provisions and applies nothing"
	gitOpsRuns     = "Flux or Argo applies the version a Stage points at; Pacto moves the pointer"
	promoteDecides = "; the promote's own plan decides"
)

// explain is the impact, chain and executor of an edit. The edited bundle is
// staged beside dir only while the impact provider reads it.
func (o fileOps) explain(ctx context.Context, opts EditOptions, dir string, bundleFS fstest.MapFS, c *contract.Contract, input EditInput) (*EditImpact, []ChainStep, string) {
	why := noSnapshot
	var imp *EditImpact
	if opts.Impact != nil {
		res, err := o.stagedImpact(ctx, opts, dir, bundleFS)
		if err != nil {
			why = fmt.Sprintf("impact could not be computed (%v), so who this edit affects is not known", err)
		} else {
			imp = editImpact(res)
		}
	}
	return imp, editChain(c, dir, imp, why, opts.Served), executor(input)
}

func (o fileOps) stagedImpact(ctx context.Context, opts EditOptions, dir string, bundleFS fstest.MapFS) (*impact.Result, error) {
	stage, err := o.stage(dir, bundleFS)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage) //nolint:errcheck // best-effort cleanup of a temporary directory
	return opts.Impact(ctx, dir, stage)
}

func editImpact(r *impact.Result) *EditImpact {
	limits := make([]string, len(r.Limitations))
	for i, l := range r.Limitations {
		limits[i] = l.Code + ": " + l.Message
	}
	return &EditImpact{
		Classification: r.Classification,
		Gate:           r.Gate,
		GateReason:     r.GateReason,
		Affected:       len(r.Consumers),
		Owners:         bounded(r.Owners),
		Approvals:      approvals(r.Gate, r.Owners),
		Completeness:   string(r.Completeness),
		AsOf:           r.AsOf.UTC().Format(time.RFC3339),
		SnapshotID:     r.SnapshotID,
		Limitations:    bounded(limits),
	}
}

// bounded keeps at most maxEditList entries, the last counting the dropped.
func bounded(list []string) []string {
	if len(list) <= maxEditList {
		return list
	}
	kept := append([]string{}, list[:maxEditList-1]...)
	return append(kept, fmt.Sprintf("and %d more", len(list)-maxEditList+1))
}

// approvals is who the promote may ask, inferred from the impact gate: a
// promote of a version not yet published cannot be planned, so its own plan
// has the last word.
func approvals(gate string, owners []string) string {
	switch gate {
	case impact.GateBlocked:
		who := "the affected consumers' owners"
		if len(owners) > 0 {
			who = strings.Join(bounded(owners), ", ")
		}
		return "a person from " + who + " must approve the promote where an affected consumer runs" + promoteDecides
	case impact.GateClear:
		return "impact asks nobody; the Stage's level and gates still apply" + promoteDecides
	default:
		return "Pacto could not show the change is safe, so the promote may ask a person" + promoteDecides
	}
}

// editChain is the steps that carry an edit to running: impact first when it
// is not known, then publish and promote.
func editChain(c *contract.Contract, dir string, imp *EditImpact, unknownWhy string, served func(string) bool) []ChainStep {
	var steps []ChainStep
	if imp == nil {
		steps = append(steps, step(served, "pacto_impact", "pacto impact <published ref> "+dir, unknownWhy))
	}
	return append(steps, promoteChain(c.Service.Name, c.Service.Version, served)...)
}

// promoteChain is publish then promote, for an edited or a new contract. A
// new contract skips impact: no consumer declares a service that is not
// published yet.
func promoteChain(service, version string, served func(string) bool) []ChainStep {
	return []ChainStep{
		step(served, "pacto_publish_plan", "pacto publish --plan", "check what publishing "+version+" needs and what refuses it"),
		step(served, "pacto_publish", "pacto publish", "publish "+version+" so a Stage can take it"),
		step(served, "pacto_delivery_operation_plan", "pacto delivery plan --type promote --to "+version+" --stage <stage>",
			"on each Stage of "+service+"; the plan names the approval floor"),
	}
}

// step names tool only when this session serves it.
func step(served func(string) bool, tool, command, why string) ChainStep {
	if served == nil || !served(tool) {
		return ChainStep{Command: command, Why: why + "; this session does not serve " + tool}
	}
	return ChainStep{Tool: tool, Command: command, Why: why}
}

// executor is who acts on the published revision: the provider for
// configuration references and environments, else the GitOps tool.
func executor(input EditInput) string {
	if len(input.AddConfigurationRefs)+len(input.RemoveConfigurationRefs)+len(input.AddEnvironments)+
		len(input.RemoveEnvironments)+len(input.SetEnvironmentValues) > 0 {
		return providerRuns
	}
	return gitOpsRuns
}
