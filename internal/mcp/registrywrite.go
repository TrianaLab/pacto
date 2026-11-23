package mcp

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// The agent write contract: every MCP tool that writes to a registry on an
// agent's behalf follows it. A call plans by default (dry_run is true) and
// returns the plan with its intent digest; it writes only with dry_run false
// and that digest, and an agent's write is a draft a person accepts. Every
// refusal carries a stable code from WriteRefusal and the command a person
// runs about it. pacto_declare is its first tool and pacto_accept its second; pacto_publish_plan and
// pacto_publish stay outside it.

// WriteRefusal is the stable code of a refused registry write. The enum is
// published in each write tool's output schema.
type WriteRefusal string

// The write refusal codes. internal/app's declare refusals use the same
// strings, the owner rule's come from pkg/delivery; IntentRequired,
// ExecuteNeedsStdio, NotAllowed and ReviewFailed are this contract's own.
const (
	RefusalPlanStale              WriteRefusal = "PlanStale"
	RefusalIntentRequired         WriteRefusal = "IntentRequired"
	RefusalWorkloadNotFound       WriteRefusal = "WorkloadNotFound"
	RefusalWorkloadAmbiguous      WriteRefusal = "WorkloadAmbiguous"
	RefusalAlreadyDeclared        WriteRefusal = "AlreadyDeclared"
	RefusalRangeNotProposed       WriteRefusal = "RangeNotProposed"
	RefusalNoRegistry             WriteRefusal = "NoRegistry"
	RefusalRegistryUnreadable     WriteRefusal = "RegistryUnreadable"
	RefusalVersionConflict        WriteRefusal = "VersionConflict"
	RefusalSignerNotAllowed       WriteRefusal = "SignerNotAllowed"
	RefusalSignerUnverified       WriteRefusal = "SignerUnverified"
	RefusalDraftNotFound          WriteRefusal = "DraftNotFound"
	RefusalExecuteNeedsStdio      WriteRefusal = "ExecuteNeedsStdio"
	RefusalBaselineNeedsMigration WriteRefusal = "BaselineNeedsMigration"
	RefusalBundleRepoMismatch     WriteRefusal = "BundleRepoMismatch"
	RefusalRepositoryUnknown      WriteRefusal = "RepositoryUnknown"
	RefusalRepositoryUnconfirmed  WriteRefusal = "RepositoryUnconfirmed"
	// The pull request codes: internal/gitexport's, and
	// PullRequestNeedsLocalToken, an HTTP server's refusal of pr.
	RefusalGitHostUnknown             WriteRefusal = "GitHostUnknown"
	RefusalGitTokenMissing            WriteRefusal = "GitTokenMissing"
	RefusalPullRequestFailed          WriteRefusal = "PullRequestFailed"
	RefusalPullRequestNeedsLocalToken WriteRefusal = "PullRequestNeedsLocalToken"
	// An HTTP server's draft write: the caller may not update the workload,
	// writes on behalf of someone else or into another workload's
	// repository (NotAllowed), or the review of the caller failed.
	RefusalNotAllowed   WriteRefusal = "NotAllowed"
	RefusalReviewFailed WriteRefusal = "ReviewFailed"
	// The pacto_accept codes.
	RefusalAcceptNothingProposed    WriteRefusal = "AcceptNothingProposed"
	RefusalFieldNotAcceptable       WriteRefusal = "FieldNotAcceptable"
	RefusalBreakNotAnnounced        WriteRefusal = "BreakNotAnnounced"
	RefusalNotPublished             WriteRefusal = "NotPublished"
	RefusalPersonSigningUnavailable WriteRefusal = "PersonSigningUnavailable"
	// The owner rule's codes, defined once in pkg/delivery.
	RefusalOwnerSignerNotPerson  WriteRefusal = delivery.OwnerSignerNotPerson
	RefusalOwnerSignerNotAllowed WriteRefusal = delivery.OwnerSignerNotAllowed
	RefusalOwnerPolicyUnverified WriteRefusal = delivery.OwnerPolicyUnverified
	RefusalOwnerSignerUnverified WriteRefusal = delivery.OwnerSignerUnverified
)

// writeRefusals is the enum, in the order the output schema lists it.
var writeRefusals = []WriteRefusal{
	RefusalPlanStale, RefusalIntentRequired, RefusalWorkloadNotFound, RefusalWorkloadAmbiguous, RefusalAlreadyDeclared,
	RefusalRangeNotProposed, RefusalNoRegistry, RefusalRegistryUnreadable, RefusalVersionConflict,
	RefusalSignerNotAllowed, RefusalSignerUnverified, RefusalDraftNotFound, RefusalExecuteNeedsStdio,
	RefusalBaselineNeedsMigration, RefusalBundleRepoMismatch, RefusalRepositoryUnknown, RefusalRepositoryUnconfirmed,
	RefusalGitHostUnknown, RefusalGitTokenMissing, RefusalPullRequestFailed, RefusalPullRequestNeedsLocalToken, RefusalNotAllowed, RefusalReviewFailed, RefusalAcceptNothingProposed, RefusalFieldNotAcceptable,
	RefusalBreakNotAnnounced, RefusalNotPublished, RefusalPersonSigningUnavailable,
	RefusalOwnerSignerNotPerson, RefusalOwnerSignerNotAllowed, RefusalOwnerPolicyUnverified, RefusalOwnerSignerUnverified,
}

// Refused is a refused write: its code, what happened and the command a
// person runs about it.
type Refused struct {
	Code    WriteRefusal `json:"code"`
	Message string       `json:"message"`
	CLI     string       `json:"cli"`
}

// DraftRef is the draft a write left: its tag, the bundle it pushed
// (repo@digest) and whether it is signed.
type DraftRef struct {
	Tag       string `json:"tag"`
	Published string `json:"published"`
	Signed    bool   `json:"signed"`
}

// PullRequest is the pull request a stdio pacto_declare with pr opened:
// its URL, the login of the token that opened it, its branch and where the
// token came from.
type PullRequest struct {
	URL         string `json:"url"`
	Login       string `json:"login"`
	Branch      string `json:"branch"`
	TokenSource string `json:"tokenSource"`
}

// WriteResult is what a write tool answers: the plan and its intent digest,
// whether anything was written, the draft it left, the command a person
// accepts that draft with, or the refusal; with pr, the pull request it
// opened after the write or why that failed (the write stays).
type WriteResult struct {
	Plan             any          `json:"plan,omitempty"`
	IntentDigest     string       `json:"intentDigest,omitempty"`
	Executed         bool         `json:"executed"`
	Draft            *DraftRef    `json:"draft,omitempty"`
	AcceptCLI        string       `json:"acceptCLI,omitempty"`
	Refusal          *Refused     `json:"refusal,omitempty"`
	PullRequest      *PullRequest `json:"pullRequest,omitempty"`
	PullRequestError *Refused     `json:"pullRequestError,omitempty"`
}

// dryRun is whether a call only plans: an unset dry_run does.
func dryRun(arg *bool) bool { return arg == nil || *arg }

// requireIntent refuses a write whose caller passed no intent digest, or
// one that is not the fresh plan's.
func requireIntent(planned, passed string) *Refused {
	switch {
	case passed == "":
		return &Refused{Code: RefusalIntentRequired, Message: "writing needs the intent_digest of the plan you reviewed: call again with dry_run true, " +
			"review the plan, then pass its intent_digest with dry_run false"}
	case passed != planned:
		return &Refused{Code: RefusalPlanStale, Message: "the plan changed since intent " + passed + " was reviewed; it is now " + planned +
			": review the new plan and call again with its intent_digest"}
	}
	return nil
}

// writeResultSchema is a write tool's output schema, with the refusal enum.
func writeResultSchema() map[string]any {
	codes := make([]string, len(writeRefusals))
	for i, c := range writeRefusals {
		codes[i] = string(c)
	}
	str := map[string]any{"type": "string"}
	refusal := map[string]any{"type": "object", "required": []string{"code", "message", "cli"}, "properties": map[string]any{
		"code": map[string]any{"type": "string", "enum": codes}, "message": str, "cli": str}}
	return map[string]any{"type": "object", "required": []string{"executed"}, "properties": map[string]any{
		"plan":         map[string]any{},
		"intentDigest": str,
		"executed":     map[string]any{"type": "boolean"},
		"acceptCLI":    str,
		"draft": map[string]any{"type": "object", "properties": map[string]any{
			"tag": str, "published": str, "signed": map[string]any{"type": "boolean"}}},
		"refusal": refusal,
		"pullRequest": map[string]any{"type": "object", "properties": map[string]any{
			"url": str, "login": str, "branch": str, "tokenSource": str}},
		"pullRequestError": refusal,
	}}
}

// DeclareArgs are pacto_declare's arguments.
type DeclareArgs struct {
	Service      string   `json:"service"`
	Namespace    string   `json:"namespace"`
	Dir          string   `json:"dir"`
	Registry     string   `json:"registry"`
	Accept       []string `json:"accept"`
	OnBehalfOf   string   `json:"on_behalf_of"`
	DryRun       *bool    `json:"dry_run"`
	IntentDigest string   `json:"intent_digest"`
	MaxItems     int      `json:"max_items"`
	CallWindow   string   `json:"call_window"`
	Draft        bool     `json:"draft"`
	// PR, Repository, Folder and RepoMap are pacto declare's --pr,
	// --repository, --folder and --repo-map (RepoMap stdio only).
	PR         bool   `json:"pr"`
	Repository string `json:"repository"`
	Folder     string `json:"folder"`
	RepoMap    string `json:"repo_map"`
}

// Command is the pacto declare a person runs for a: it replays every
// argument the intent digest binds, and the digest when intent is set.
// Without --yes it prints the plan and writes nothing.
func (a DeclareArgs) Command(intent string) string {
	parts := []string{"pacto declare", a.Service}
	if a.Namespace != "" {
		parts = append(parts, "--namespace", a.Namespace)
	}
	if a.Dir != "" {
		parts = append(parts, "--dir", a.Dir)
	}
	reg := a.Registry
	if reg == "" {
		reg = "<registry>"
	}
	parts = append(parts, "--registry", reg)
	for _, n := range a.Accept {
		parts = append(parts, "--accept", n)
	}
	if a.MaxItems != 0 {
		parts = append(parts, "--max-items", strconv.Itoa(a.MaxItems))
	}
	if a.CallWindow != "" {
		parts = append(parts, "--call-window", a.CallWindow)
	}
	if a.PR {
		parts = append(parts, "--pr")
	}
	for _, f := range [][2]string{{"--repository", a.Repository}, {"--folder", a.Folder}, {"--repo-map", a.RepoMap}} {
		if f[1] != "" {
			parts = append(parts, f[0], f[1])
		}
	}
	if intent != "" {
		parts = append(parts, "--intent", intent)
	}
	return strings.Join(parts, " ")
}

// RefusalCLI is the command a person runs about a refusal of code that
// names none: the pull request codes add what the person passes, every
// other code replays the plan.
func (a DeclareArgs) RefusalCLI(code WriteRefusal) string {
	pr := a
	pr.PR = true
	switch code {
	case RefusalGitHostUnknown:
		return pr.Command("") + " --git-host github|gitlab|bitbucket|gitea"
	case RefusalPullRequestFailed:
		return pr.Command("") + " --yes"
	case RefusalGitTokenMissing, RefusalPullRequestNeedsLocalToken:
		return pr.Command("")
	}
	return a.Command("")
}

// Declarer plans and writes declarations for pacto_declare. The command
// layer supplies it, so this package never depends on the app service: Plan
// returns the plan as JSON (its intentDigest member is the plan's digest),
// Execute the run's result as JSON (plan, executed, tag, published, draft,
// signed). A refusal is the *Refused, never an error.
type Declarer interface {
	Plan(ctx context.Context, a DeclareArgs) (json.RawMessage, *Refused, error)
	Execute(ctx context.Context, a DeclareArgs) (json.RawMessage, *Refused, error)
}

// declaredRun is the part of a Declarer's JSON pacto_declare reads.
type declaredRun struct {
	IntentDigest string          `json:"intentDigest"`
	AcceptCLI    string          `json:"acceptCLI"`
	Unchanged    string          `json:"unchanged"`
	Plan         json.RawMessage `json:"plan"`
	Executed     bool            `json:"executed"`
	Tag          string          `json:"tag"`
	Published    string          `json:"published"`
	Draft        bool            `json:"draft"`
	Signed       bool            `json:"signed"`
	// PullRequest and PullRequestError are a stdio run's pull request.
	PullRequest      *PullRequest `json:"pullRequest"`
	PullRequestError *Refused     `json:"pullRequestError"`
	// Repository is the plan's repository, which a failed pull request's
	// command names.
	Repository *struct {
		URL string `json:"url"`
	} `json:"repository"`
}
