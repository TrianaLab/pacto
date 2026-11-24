package mcp

import (
	"cmp"
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/externalid"
	"github.com/trianalab/pacto/v4/pkg/fleet"
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
	// The claim codes: a claim names no owner, its repository holds another
	// external system, it names no external system, or it would publish
	// under pacto-inferred.
	RefusalOwnerRequired           WriteRefusal = "OwnerRequired"
	RefusalExternalMismatch        WriteRefusal = "ExternalMismatch"
	RefusalExternalUnknown         WriteRefusal = "ExternalUnknown"
	RefusalClaimRepositoryInferred WriteRefusal = "ClaimRepositoryInferred"
	// The offering acceptance codes: an open parameter schema, a source
	// that is no contribution or that no producer key verified, a bundle
	// that offers another name, and no version.
	RefusalOfferingSchemaOpen       WriteRefusal = "OfferingSchemaOpen"
	RefusalOfferingSourceInvalid    WriteRefusal = "OfferingSourceInvalid"
	RefusalOfferingSourceUnverified WriteRefusal = "OfferingSourceUnverified"
	RefusalOfferingMismatch         WriteRefusal = "OfferingMismatch"
	RefusalVersionRequired          WriteRefusal = "VersionRequired"
	RefusalOfferingInvalid          WriteRefusal = "OfferingInvalid"
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
	// The template:<namespace>/<name> codes: no such template, no catalog to
	// read it from, a catalog that could not be read, a name no contract
	// carries, a name a service already holds, and (an HTTP server's own) a
	// catalog the installation did not configure.
	RefusalTemplateNotFound           WriteRefusal = "TemplateNotFound"
	RefusalTemplateCatalogUnset       WriteRefusal = "TemplateCatalogUnset"
	RefusalTemplateCatalogUnavailable WriteRefusal = "TemplateCatalogUnavailable"
	RefusalTemplateNameInvalid        WriteRefusal = "TemplateNameInvalid"
	RefusalTemplateNameTaken          WriteRefusal = "TemplateNameTaken"
	RefusalTemplateSourceUnverified   WriteRefusal = "TemplateSourceUnverified"
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
	RefusalOwnerRequired, RefusalExternalMismatch, RefusalExternalUnknown, RefusalClaimRepositoryInferred, RefusalOfferingInvalid,
	RefusalOfferingSchemaOpen, RefusalOfferingSourceInvalid, RefusalOfferingSourceUnverified, RefusalOfferingMismatch, RefusalVersionRequired,
	RefusalTemplateNotFound, RefusalTemplateCatalogUnset, RefusalTemplateCatalogUnavailable, RefusalTemplateNameInvalid, RefusalTemplateNameTaken,
	RefusalTemplateSourceUnverified,
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
	// Owner, Name and Offering are set only on a claim (service
	// external:<identity>): the owning team, the service name the claim
	// publishes under and the platform offering it intends to join. Owner
	// also names the owner of an accepted offering.
	Owner    string `json:"owner"`
	Name     string `json:"name,omitempty"`
	Offering string `json:"offering,omitempty"`
	// Version and From are set only on an offering acceptance (service
	// offering:<name>): the version it publishes and the inferred offering's
	// contribution ref.
	Version string `json:"version,omitempty"`
	From    string `json:"from,omitempty"`
}

// serviceNameRE is serviceNamePattern, the name an offering acceptance
// names.
var serviceNameRE = regexp.MustCompile(serviceNamePattern)

// Claim is the external system a claims and whether a is a claim.
func (a DeclareArgs) Claim() (string, bool) {
	return strings.CutPrefix(a.Service, fleet.ExternalSubjectPrefix)
}

// AcceptedOffering is the offering a accepts and whether a is an
// acceptance (service offering:<name>).
func (a DeclareArgs) AcceptedOffering() (string, bool) {
	return strings.CutPrefix(a.Service, fleet.OfferingSubjectPrefix)
}

// template is whether a accepts a golden path a catalog holds
// (service template:<namespace>/<name>).
func (a DeclareArgs) template() bool {
	return strings.HasPrefix(a.Service, fleet.TemplateSubjectPrefix)
}

// Verb is the command a person runs for a: pacto claim for a claim, else
// pacto declare.
func (a DeclareArgs) Verb() string {
	if _, ok := a.Claim(); ok {
		return "pacto claim"
	}
	return "pacto declare"
}

// ArgsRefused refuses, before any read, arguments a's subject does not
// take: a claim takes owner, name, registry, draft and intent and names a
// valid external identity; an offering acceptance takes owner, version,
// from, registry, draft and intent; a workload takes no owner, name,
// offering, version or from. Its command carries only arguments the subject
// takes.
func (a DeclareArgs) ArgsRefused() *Refused {
	if name, ok := a.AcceptedOffering(); ok {
		return a.offeringRefused(name)
	}
	id, claim := a.Claim()
	switch {
	case a.Version != "" || a.From != "":
		return &Refused{Code: RefusalFieldNotAcceptable, Message: "version and from are an offering acceptance's (service offering:<name>): " +
			"call again without them", CLI: a.Command("")}
	case claim && a.workloadArgs():
		return &Refused{Code: RefusalFieldNotAcceptable, Message: "pacto claim takes owner, name, offering, registry, draft and intent: " +
			"call again without namespace, dir, accept, max_items, call_window, pr, repository, folder and repo_map", CLI: a.Command("")}
	case claim:
		if _, err := externalid.Parse(id); err != nil {
			return &Refused{Code: RefusalExternalUnknown, Message: "service external:<identity> names no external identity: " +
				"find its identity with pacto fleet search", CLI: "pacto claim <identity> --owner <team> --registry <registry>"}
		}
	case a.template() && (a.workloadArgs() || a.Owner != "" || a.Name != "" || a.Offering != ""):
		return &Refused{Code: RefusalFieldNotAcceptable, Message: "a golden path a catalog holds takes registry, draft and intent: " +
			"call again without namespace, dir, accept, max_items, call_window, pr, repository, folder, repo_map, owner, name and offering", CLI: a.Command("")}
	case a.Owner != "" || a.Name != "" || a.Offering != "":
		return &Refused{Code: RefusalFieldNotAcceptable, Message: "owner, name and offering are a claim's (service external:<identity>): " +
			"a workload's owner, name and offering come from its contract", CLI: a.Command("")}
	}
	return nil
}

// workloadArgs is whether a sets an argument only a workload takes.
func (a DeclareArgs) workloadArgs() bool {
	return a.Namespace != "" || a.Dir != "" || len(a.Accept) > 0 || a.MaxItems != 0 || a.CallWindow != "" ||
		a.PR || a.Repository != "" || a.Folder != "" || a.RepoMap != ""
}

// offeringRefused refuses an acceptance of offering name that names no
// offering, or sets an argument it takes not.
func (a DeclareArgs) offeringRefused(name string) *Refused {
	switch {
	case !serviceNameRE.MatchString(name):
		return &Refused{Code: RefusalFieldNotAcceptable, Message: "service offering:<name> names a lower-case offering of at most 63 characters: " +
			"find its name with pacto_fleet_entities, kind offering and declaration inferred", CLI: "pacto fleet offerings --inferred"}
	case a.workloadArgs() || a.Name != "" || a.Offering != "":
		return &Refused{Code: RefusalFieldNotAcceptable, Message: "accepting an offering takes owner, version, from, registry, draft and intent: " +
			"call again without namespace, dir, accept, max_items, call_window, pr, repository, folder, repo_map, name and offering", CLI: a.Command("")}
	}
	return nil
}

// Command is the pacto declare (pacto claim for a claim) a person runs for
// a: it replays every argument the intent digest binds, and the digest when
// intent is set. Without --yes it prints the plan and writes nothing. A
// claim's command carries only the flags pacto claim defines.
func (a DeclareArgs) Command(intent string) string {
	reg := cmp.Or(a.Registry, "<registry>")
	if _, ok := a.AcceptedOffering(); ok {
		return a.offeringCommand(reg, intent)
	}
	if id, ok := a.Claim(); ok {
		parts := []string{"pacto claim", id, "--owner", cmp.Or(a.Owner, "<team>")}
		if a.Name != "" {
			parts = append(parts, "--name", a.Name)
		}
		if a.Offering != "" {
			parts = append(parts, "--offering", a.Offering)
		}
		parts = append(parts, "--registry", reg)
		if intent != "" {
			parts = append(parts, "--intent", intent)
		}
		return strings.Join(parts, " ")
	}
	parts := []string{"pacto declare", a.Service}
	if a.template() {
		parts = append(parts, "--backstage-url", "<backstage-url>")
	}
	if a.Namespace != "" {
		parts = append(parts, "--namespace", a.Namespace)
	}
	if a.Dir != "" {
		parts = append(parts, "--dir", a.Dir)
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
	Service      string          `json:"service"`
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

// offeringCommand is the pacto declare offering:<name> a person runs for a,
// with the flags that command defines. The --detected whose installation
// key verifies from is the person's own: a stdio server's is its own
// --detected, which a person's machine may not read.
func (a DeclareArgs) offeringCommand(reg, intent string) string {
	parts := []string{"pacto declare", a.Service, "--from", cmp.Or(a.From, "<contribution>"), "--detected", "k8s:<namespace>/<configmap>",
		"--registry", reg, "--version", cmp.Or(a.Version, "<version>")}
	if a.Owner != "" {
		parts = append(parts, "--owner", a.Owner)
	}
	if intent != "" {
		parts = append(parts, "--intent", intent)
	}
	return strings.Join(parts, " ")
}
