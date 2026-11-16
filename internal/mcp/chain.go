package mcp

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// EditOptions are what the server that registers pacto_edit adds to an edit.
// It is a Go argument, never part of the tool's input.
type EditOptions struct {
	// Impact analyses the change to service from the contract in oldDir to
	// the edited bundle staged in newDir over the session's fleet snapshot;
	// nil when the session has no snapshot.
	Impact func(ctx context.Context, service, oldDir, newDir string) (*impact.Result, error)
	// Served reports whether this session registers a tool; nil serves none.
	Served func(tool string) bool
}

// EditImpact is the part of an impact answer an edit needs before it runs.
type EditImpact struct {
	// OldVersion and NewVersion are the two sides the edit is analysed
	// between: the contract on disk before this edit and the edited one.
	OldVersion     string `json:"oldVersion,omitempty"`
	NewVersion     string `json:"newVersion,omitempty"`
	Classification string `json:"classification"`
	Gate           string `json:"gate"`
	GateReason     string `json:"gateReason"`
	// Affected counts the consumers the answer lists; Hidden counts those
	// this viewer may not see.
	Affected int `json:"affected"`
	Hidden   int `json:"hidden,omitempty"`
	// Owners are the owners of every consumer the answer lists.
	Owners []string `json:"owners,omitempty"`
	// Templates are the golden paths that reference the changed policy:
	// they never hold the gate, but each one is worth a review.
	Templates []string `json:"templates,omitempty"`
	// Approvals is who the promote may ask, inferred from the gate.
	Approvals    string   `json:"approvals"`
	Completeness string   `json:"completeness"`
	AsOf         string   `json:"asOf"`
	SnapshotID   string   `json:"snapshotId"`
	Limitations  []string `json:"limitations,omitempty"`
}

// ChainStep is one step that carries a change to running. Tool is empty when
// this session does not serve it; Command is the CLI form either way. Args
// are the tool's arguments the step already knows, such as the contract's
// path.
type ChainStep struct {
	Tool    string            `json:"tool,omitempty"`
	Args    map[string]string `json:"args,omitempty"`
	Command string            `json:"command"`
	Why     string            `json:"why"`
}

// maxEditList bounds Owners and Limitations; the last entry counts the rest.
const maxEditList = 20

const (
	noSnapshot     = "this session has no fleet snapshot, so who this edit affects is not known"
	providerRuns   = "the provider: an adapter or GitOps tool acts on the published revision; Pacto provisions and applies nothing"
	gitOpsRuns     = "Flux or Argo applies the version a Stage points at; Pacto moves the pointer"
	promoteDecides = "; the promote's own plan decides"
	afterWrite     = "; run it after the edit is written: on a dry run the directory still holds the contract before the edit"
)

// explain is the impact, chain and executor of an edit. The edited bundle is
// staged beside dir only while the impact provider reads it.
func (o fileOps) explain(ctx context.Context, opts EditOptions, dir string, bundleFS fstest.MapFS, c *contract.Contract, input EditInput) (*EditImpact, []ChainStep, string) {
	// Complete a copy, as every local read does, so an inferred name places the
	// service in the fleet and names it in the chain; c stays what is written.
	cc := *c
	c = &cc
	abs, _ := filepath.Abs(dir) //nolint:errcheck // fails only without a working directory; Base(dir) is then the best name
	b := &contract.Bundle{Contract: c, FS: bundleFS}
	b.Complete(filepath.Base(cmp.Or(abs, dir)))
	_, nameInferred := b.NameInferred()
	why := noSnapshot
	var imp *EditImpact
	if opts.Impact != nil {
		res, err := o.stagedImpact(ctx, opts, dir, bundleFS, c)
		if err != nil {
			why = fmt.Sprintf("impact could not be computed (%v), so who this edit affects is not known", err)
		} else {
			imp = editImpact(res)
		}
	}
	if input.DryRun {
		why += afterWrite
	}
	return imp, editChain(c, nameInferred, dir, imp, why, opts.Served), executor(input)
}

func (o fileOps) stagedImpact(ctx context.Context, opts EditOptions, dir string, bundleFS fstest.MapFS, c *contract.Contract) (*impact.Result, error) {
	stage, err := o.stage(dir, bundleFS)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)                           //nolint:errcheck // best-effort cleanup of a temporary directory
	return opts.Impact(ctx, c.Service.Name, dir, stage) // c is completed: an inferred name places the service
}

func editImpact(r *impact.Result) *EditImpact {
	if r.NewRevisionAdded {
		r = unverified(r, "SERVICE_NOT_IN_FLEET", "no fleet source holds "+r.Service+", so the edited revision was added to find its consumers and may miss them")
	}
	limits := make([]string, len(r.Limitations))
	for i, l := range r.Limitations {
		limits[i] = l.Code + ": " + l.Message
	}
	limits = bounded(limits)
	if r.LimitationsTruncated {
		limits = append(limits, "and more limitations the answer does not carry")
	}
	return &EditImpact{
		OldVersion:     r.OldVersion,
		NewVersion:     r.NewVersion,
		Classification: r.Classification,
		Gate:           r.Gate,
		GateReason:     r.GateReason,
		Affected:       len(r.Consumers),
		Hidden:         r.Hidden,
		Owners:         bounded(r.Owners),
		Templates:      bounded(r.Templates),
		Approvals:      approvals(r),
		Completeness:   string(r.Completeness),
		AsOf:           r.AsOf.UTC().Format(time.RFC3339),
		SnapshotID:     r.SnapshotID,
		Limitations:    limits,
	}
}

// unverified is a copy of r that says why it cannot be trusted: a clear gate
// becomes unverified, since unknown is never positive.
func unverified(r *impact.Result, code, msg string) *impact.Result {
	cp := *r
	cp.Limitations = append(slices.Clone(r.Limitations), fleet.Limitation{Code: code, Message: msg})
	if cp.Gate == impact.GateClear {
		cp.Gate, cp.GateReason = impact.GateUnverified, msg
	}
	return &cp
}

// bounded keeps at most maxEditList entries, the last counting the dropped.
func bounded(list []string) []string {
	if len(list) <= maxEditList {
		return list
	}
	kept := append([]string{}, list[:maxEditList-1]...)
	return append(kept, fmt.Sprintf("and %d more", len(list)-maxEditList+1))
}

// breakOwners are the owners of the consumers the change breaks (incompatible
// or with an unmet guarantee), the set a promote's approval floor is drawn
// from, and how many of those consumers name no owner.
func breakOwners(r *impact.Result) (owners []string, noOwner int) {
	for _, c := range r.Consumers {
		if c.CompatibilityVerdict != impact.CompatibilityIncompatible && len(c.Unmet) == 0 {
			continue
		}
		if c.OwnerKey == "" {
			noOwner++
		} else if !slices.Contains(owners, c.OwnerKey) {
			owners = append(owners, c.OwnerKey)
		}
	}
	slices.Sort(owners)
	return owners, noOwner
}

// approvals is who the promote may ask, inferred from the impact gate: a
// promote of a version not yet published cannot be planned, so its own plan
// has the last word.
func approvals(r *impact.Result) string {
	switch r.Gate {
	case impact.GateBlocked:
		owners, noOwner := breakOwners(r)
		who := "the owners of the consumers it breaks"
		if len(owners) > 0 {
			who = "the owners of the consumers it breaks (" + strings.Join(bounded(owners), ", ") + ")"
		}
		if noOwner > 0 {
			who += fmt.Sprintf(", and %d broken consumers name no owner", noOwner)
		}
		return "a person from " + who + " must approve the promote where a broken consumer runs" + promoteDecides
	case impact.GateClear:
		return "impact asks nobody; the Stage's level and gates still apply" + promoteDecides
	default:
		return "Pacto could not show the change is safe, so the promote may ask a person" + promoteDecides
	}
}

// editChain is the steps that carry an edit to running: impact first when it
// is not known, then declaring the identity publish refuses without, then
// publish and promote.
func editChain(c *contract.Contract, nameInferred bool, dir string, imp *EditImpact, unknownWhy string, served func(string) bool) []ChainStep {
	var steps []ChainStep
	if imp == nil {
		steps = append(steps, step(served, "pacto_impact", "pacto impact <published ref> "+dir, unknownWhy))
	}
	if s, ok := declareIdentity(c, nameInferred, dir); ok {
		steps = append(steps, s)
	}
	return append(steps, promoteChain(c.Service.Name, c.Service.Version, dir, served)...)
}

// declareIdentity is the pacto_edit step that declares service.name and
// service.version when the contract leaves either out: publish refuses it
// (PUBLISH_NEEDS_SERVICE_NAME, PUBLISH_NEEDS_SERVICE_VERSION) until it does.
func declareIdentity(c *contract.Contract, nameInferred bool, dir string) (ChainStep, bool) {
	var missing []string
	args := map[string]string{"path": dir}
	if nameInferred || c.Service.Name == "" {
		missing = append(missing, "service.name")
		if c.Service.Name != "" {
			args["name"] = c.Service.Name
		}
	}
	if c.Service.Version == "" {
		missing = append(missing, "service.version")
	}
	if len(missing) == 0 {
		return ChainStep{}, false
	}
	what := strings.Join(missing, " and ")
	return ChainStep{Tool: "pacto_edit", Args: args, Command: "declare " + what + " in " + dir + "/pacto.yaml",
		Why: "pacto publish refuses a contract that does not declare " + what}, true
}

// promoteChain is publish then promote of the contract in dir, for an edited
// or a new contract. A new contract has no published revision to compare
// with, so its chain has no impact step; a consumer that already declares it
// shows that dependency as unresolved until it is published.
func promoteChain(service, version, dir string, served func(string) bool) []ChainStep {
	version = cmp.Or(version, "<version>") // an undeclared service.version names none yet
	publishPlan := step(served, "pacto_publish_plan", "pacto publish --plan "+dir, "check what publishing "+version+" from "+dir+" needs and what refuses it")
	publish := step(served, "pacto_publish", "pacto publish "+dir, "publish "+version+" from "+dir+" so a Stage can take it")
	publishPlan.Args, publish.Args = map[string]string{"path": dir}, map[string]string{"path": dir}
	return []ChainStep{
		publishPlan,
		publish,
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
