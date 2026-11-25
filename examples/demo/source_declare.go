package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// demoDeclareRegistry is the registry the demo's declarations would be
// published to, as pacto declare --registry names it.
const demoDeclareRegistry = "demo.pacto.local"

// demoNoSignIn is why nobody signs a declaration in the demo.
const demoNoSignIn = "the demo has no sign-in"

// demoProposal is one range the demo's cluster read proposes for an
// inferred service: the provider it was seen calling, the range and the
// version that provider runs.
type demoProposal struct{ dependency, proposed, observed string }

// demoProposals are the ranges the cluster read proposes per inferred
// workload name: legacy-billing was seen calling payments-service, which
// runs 2.1.0 in production.
var demoProposals = map[string][]demoProposal{
	"legacy-billing": {{dependency: "payments-service", proposed: "^2.1.0", observed: "2.1.0"}},
}

// demoDeclarer is the demo's dashboard.Declarer, held in the tab: it plans
// what pacto declare would publish for an inferred service, nobody signs,
// and Save draft adds a draft-<n> to an in-memory list the next plan lists.
// Nothing reaches a registry.
type demoDeclarer struct {
	who    func(*http.Request) string
	mu     sync.Mutex
	drafts map[string][]demoDraft // per service key, oldest first
	claims map[fleet.ExternalKey]demoClaim
	// claimed refolds the fleet with every claimed revision of the tab; nil
	// leaves the fleet as it is.
	claimed func([]fleet.RawRevision) error
}

// demoDraft is one saved draft: its tag, the ranges it requests, whom it
// was written for and, for a created service, the intent it was saved from.
type demoDraft struct {
	tag, digest, onBehalfOf, intent string
	accept                          []string
}

func newDemoDeclarer(who func(*http.Request) string) *demoDeclarer {
	return &demoDeclarer{who: who, drafts: map[string][]demoDraft{}, claims: map[fleet.ExternalKey]demoClaim{}}
}

// CanWrite implements dashboard.Declarer: the demo saves drafts in the tab.
func (*demoDeclarer) CanWrite() bool { return true }

// MayDeclareTemplate implements dashboard.Declarer: every demo visitor may
// accept a golden path the demo catalog holds.
func (*demoDeclarer) MayDeclareTemplate(context.Context) bool { return true }

func demoSigning() dashboard.DeclareSigning {
	return dashboard.DeclareSigning{Mode: "none", Reason: demoNoSignIn}
}

// Plan implements dashboard.Declarer.
func (d *demoDeclarer) Plan(r *http.Request, w dashboard.DeclareWorkload, in dashboard.DeclareInput) (*dashboard.DeclareAnswer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if id, ok := fleet.ParseExternalSubject(w.Key); ok {
		ans, _ := d.planClaim(d.who(r), id, in.ClaimInput, in.IntentDigest)
		return ans, nil
	}
	if w.Offering != "" {
		return planOffering(w, in.Offering), nil
	}
	ans := &dashboard.DeclareAnswer{Signing: demoSigning(), Write: dashboard.DeclareWrite{Available: true}}
	// A draft's requested ranges stay unknown unless the person accepts them.
	accept, from := in.Accept, (*demoDraft)(nil)
	if in.FromDraft != nil {
		if from = d.draft(w.Key, *in.FromDraft); from == nil {
			ans.Refusal = &dashboard.DeclareRefusal{Code: "DraftNotFound", Message: in.FromDraft.Tag + " is not a draft of " + w.Name + " with the digest shown"}
			return ans, nil
		}
	}
	if bad := notProposed(w.Name, accept); bad != "" {
		ans.Refusal = &dashboard.DeclareRefusal{Code: "RangeNotProposed", Message: "the cluster read proposes no range for " + bad, Command: demoCommand(w, nil, "")}
		return ans, nil
	}
	ans.Plan = demoPlan(w, accept, d.who(r), from)
	ans.Drafts, ans.DraftsTotal = d.views(w), len(d.drafts[w.Key])
	ans.Command = demoCommand(w, accept, ans.Plan.IntentDigest)
	if w.Template == nil {
		ans.PRCLI = demoCommand(w, accept, "") + " --pr"
	}
	if from != nil {
		ans.Command = demoCommand(w, nil, "") + " --from-draft " + from.tag
		for _, a := range accept {
			ans.Command += " --accept " + a
		}
		if w.Template == nil {
			ans.PRCLI = ans.Command + " --pr"
		}
	}
	return ans, nil
}

// Accept implements dashboard.Declarer: Save draft writes the next
// draft-<n>, unless the newest draft is this one again; nobody signs.
func (d *demoDeclarer) Accept(r *http.Request, w dashboard.DeclareWorkload, in dashboard.DeclareAcceptInput) (*dashboard.DeclareAcceptAnswer, error) {
	if id, ok := fleet.ParseExternalSubject(w.Key); ok {
		return d.acceptClaim(d.who(r), id, in)
	}
	if w.Offering != "" {
		// Nobody signs in the demo and it saves no offering draft.
		a := planOffering(w, in.Offering)
		if a.Refusal != nil {
			return &dashboard.DeclareAcceptAnswer{Refusal: a.Refusal}, nil
		}
		return &dashboard.DeclareAcceptAnswer{Refusal: &dashboard.DeclareRefusal{Code: "NotAllowed", Message: demoOfferingNoDraft, Command: a.Command}}, nil
	}
	if in.Mode != "draft" || in.FromDraft != nil {
		return &dashboard.DeclareAcceptAnswer{Refusal: &dashboard.DeclareRefusal{Code: "NotAllowed",
			Message: "you cannot sign here: " + demoNoSignIn, Command: demoCommand(w, in.Accept, in.IntentDigest)}}, nil
	}
	who := d.who(r)
	p, err := d.Plan(r, w, dashboard.DeclareInput{ServiceKey: in.ServiceKey, Accept: in.Accept})
	if err != nil || p.Refusal != nil {
		return &dashboard.DeclareAcceptAnswer{Refusal: p.Refusal}, err
	}
	if p.Plan.IntentDigest != in.IntentDigest {
		return &dashboard.DeclareAcceptAnswer{Plan: p, Refusal: &dashboard.DeclareRefusal{Code: "PlanStale",
			Message: "the plan changed since intent " + in.IntentDigest + " was reviewed; it is now " + p.Plan.IntentDigest + ": review the new plan"}}, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	list := d.drafts[w.Key]
	accept := slices.Sorted(slices.Values(in.Accept))
	if n := len(list); n > 0 && list[n-1].onBehalfOf == who && slices.Equal(list[n-1].accept, accept) {
		return draftAnswer(w, list[n-1]), nil
	}
	dr := demoDraft{tag: fmt.Sprintf("draft-%d", len(list)+1), onBehalfOf: who, accept: accept}
	dr.digest = digestOf(dr.tag + "\n" + p.Plan.Contract.Text + p.Plan.Record.Text)
	d.drafts[w.Key] = append(list, dr)
	return draftAnswer(w, dr), nil
}

func draftAnswer(w dashboard.DeclareWorkload, dr demoDraft) *dashboard.DeclareAcceptAnswer {
	repo := demoDeclareRegistry + "/" + w.Name
	subject := w.Name
	if w.Template != nil {
		subject = w.Key + " --backstage-url " + demoBackstageBase
	}
	return &dashboard.DeclareAcceptAnswer{Published: repo + "@" + dr.digest, Tag: dr.tag, Draft: true,
		Command: "pacto declare " + subject + " --registry " + demoDeclareRegistry + " --from-draft " + dr.tag}
}

// AcceptOffer implements dashboard.Declarer: the demo plans no
// acceptances, and nobody signs.
func (*demoDeclarer) AcceptOffer(*http.Request, string) dashboard.AcceptExecuteOffer {
	return dashboard.AcceptExecuteOffer{Signing: demoSigning(), Write: dashboard.DeclareWrite{Available: true}}
}

// AcceptRanges implements dashboard.Declarer: the demo plans no
// acceptances, so there is nothing to carry out.
func (*demoDeclarer) AcceptRanges(*http.Request, dashboard.AcceptTarget, dashboard.AcceptExecuteInput) (*dashboard.AcceptExecuteAnswer, error) {
	return &dashboard.AcceptExecuteAnswer{Refusal: &dashboard.DeclareRefusal{Code: "NotAllowed", Message: "you cannot sign here: " + demoNoSignIn}}, nil
}

// draft is the draft of key named by ref, nil when its tag or digest moved.
func (d *demoDeclarer) draft(key string, ref dashboard.DraftRef) *demoDraft {
	for i, dr := range d.drafts[key] {
		if dr.tag == ref.Tag && dr.digest == ref.Digest {
			return &d.drafts[key][i]
		}
	}
	return nil
}

// views are w's drafts as the panel lists them, newest first, at most 20.
func (d *demoDeclarer) views(w dashboard.DeclareWorkload) []dashboard.DeclareDraft {
	var out []dashboard.DeclareDraft
	list := d.drafts[w.Key]
	for i := len(list) - 1; i >= 0 && len(out) < 20; i-- {
		dr := list[i]
		out = append(out, dashboard.DeclareDraft{Tag: dr.tag, Digest: dr.digest, State: contract.DeclarationDraft,
			Asker: unsignedAsker, OnBehalfOf: dr.onBehalfOf, Requested: requested(dr.accept, demoProposals[w.Name])})
	}
	return out
}

// unsignedAsker is the asker of an unsigned draft: nobody, as an agent.
var unsignedAsker = contract.InferredSigner{Issuer: "none", Class: contract.SignerClassAgent}

// notProposed is the first of accept the cluster read proposes no range
// for, "" when it proposes one for each.
func notProposed(name string, accept []string) string {
	for _, a := range accept {
		if !slices.ContainsFunc(demoProposals[name], func(p demoProposal) bool { return p.dependency == a }) {
			return a
		}
	}
	return ""
}

// requested are the accepted ranges as "<dependency> <range>".
func requested(accept []string, props []demoProposal) []string {
	var out []string
	for _, p := range props {
		if slices.Contains(accept, p.dependency) {
			out = append(out, p.dependency+" "+p.proposed)
		}
	}
	return out
}

// demoCommand is the pacto declare a person runs for w with accept and
// intent: a golden path by its template:<namespace>/<name> subject.
func demoCommand(w dashboard.DeclareWorkload, accept []string, intent string) string {
	cmd := "pacto declare " + w.Name + " --namespace " + w.Namespace + " --registry " + demoDeclareRegistry
	if w.Template != nil {
		// A template is read from the catalog: the real CLI needs its URL.
		cmd = "pacto declare " + w.Key + " --backstage-url " + demoBackstageBase + " --registry " + demoDeclareRegistry
	}
	for _, a := range accept {
		cmd += " --accept " + a
	}
	if intent != "" {
		cmd += " --intent " + intent
	}
	return cmd
}

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// demoPlan is what declaring w would publish with accept, as a draft for
// who: version 0.1.0 with no deploy artifact, one range per proposal, and
// from's digest folded into the intent when a draft is being accepted.
func demoPlan(w dashboard.DeclareWorkload, accept []string, who string, from *demoDraft) *dashboard.DeclarePlan {
	if w.Template != nil {
		return demoTemplatePlan(w, who)
	}
	props := demoProposals[w.Name]
	var b strings.Builder
	fmt.Fprintf(&b, "pactoVersion: \"2.5\"\nservice:\n  name: %s\n  version: 0.1.0\n", w.Name)
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1",
			Cluster: &contract.InferredClusterInput{AsOf: demoNow().Format(time.RFC3339), Object: w.Namespace + "/" + w.Kind + "/" + w.Name, CallWindow: "24h"}},
		Declaration: &contract.InferredDeclaration{State: contract.DeclarationDraft, Asker: unsignedAsker, OnBehalfOf: who,
			InferredFrom: fleet.K8sCaller("", w.Namespace, w.Kind, w.Name), Cluster: w.Cluster},
	}
	// ponytail: the demo's one GitOps repository answers; the real ladder is app.PlanDeclare's.
	if len(w.Repositories) == 1 {
		rec.Declaration.Repository = &contract.InferredSourceRepository{URL: w.Repositories[0].URL, Rung: contract.RepositoryRungGitOps,
			Folder: path.Clean(w.Repositories[0].Path), FolderRung: contract.RepositoryRungGitOps}
	}
	plan := &dashboard.DeclarePlan{Service: w.Name, Version: "0.1.0", BundleRepo: demoDeclareRegistry + "/" + w.Name, State: contract.DeclarationDraft,
		InferredFrom: rec.Declaration.InferredFrom, Cluster: w.Cluster, Deploy: "none", Repository: rec.Declaration.Repository,
		Signer: dashboard.DeclareSigner{Class: contract.SignerClassAgent, Source: "none", Why: demoNoSignIn}}
	if len(props) > 0 {
		b.WriteString("dependencies:\n")
	}
	for _, p := range props {
		field := "dependencies[" + p.dependency + "].compatibility"
		rng, ok := "unknown", slices.Contains(accept, p.dependency)
		if ok {
			rng = p.proposed
			rec.Declaration.Requested = append(rec.Declaration.Requested, contract.InferredAcceptance{Field: field, Range: p.proposed})
		}
		fmt.Fprintf(&b, "- name: %s\n  ref: %s%s\n  required: true\n  compatibility: %s\n", p.dependency, demoRegistry, p.dependency, rng)
		rec.Fields = append(rec.Fields, contract.Inferred{Field: field, Value: "compatibility unknown", Proposed: p.proposed,
			Source: "cluster:fleet reported versions " + p.observed, Confidence: contract.ConfidenceLikely,
			AsOf: rec.Inputs.Cluster.AsOf, Completeness: contract.CoveragePartial})
		plan.Ranges = append(plan.Ranges, dashboard.DeclareRange{Dependency: p.dependency, Compatibility: rng, Proposed: p.proposed, Observed: p.observed, Accepted: ok})
	}
	plan.Contract, plan.Record = dashboard.NewDeclareText([]byte(b.String())), dashboard.NewDeclareText(rec.Marshal())
	intent := plan.Contract.Text + plan.Record.Text
	if from != nil {
		intent += from.digest
		plan.Notes = []string{"A draft is accepted only with a person's signature: run the command in a terminal where you sign in"}
		for _, r := range requested(from.accept, props) {
			plan.Notes = append(plan.Notes, "requested by the draft: "+r+"; it stays unknown unless you accept it")
		}
	}
	plan.ClusterAsOf = rec.Inputs.Cluster.AsOf
	plan.IntentDigest = digestOf(intent)
	return plan
}

// demoTemplatePlan is what accepting the golden path w names would publish,
// as a draft for who: a contract-only 2.5 golden path at version 0.1.0 that
// infers nothing and reads no cluster.
func demoTemplatePlan(w dashboard.DeclareWorkload, who string) *dashboard.DeclarePlan {
	t := w.Template
	text := fmt.Sprintf("pactoVersion: \"2.5\"\nservice:\n  name: %s\n  version: 0.1.0\n", t.Name)
	if t.Summary != "" {
		text += fmt.Sprintf("  summary: %q\n", t.Summary)
	}
	text += "  template: {}\n"
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "4.6.0"}, Fields: []contract.Inferred{},
		Declaration: &contract.InferredDeclaration{State: contract.DeclarationDraft, Asker: unsignedAsker, OnBehalfOf: who}}
	plan := &dashboard.DeclarePlan{Service: t.Name, Version: "0.1.0", BundleRepo: demoDeclareRegistry + "/" + t.Name, State: contract.DeclarationDraft,
		Deploy: "none", Ranges: []dashboard.DeclareRange{}, Signer: dashboard.DeclareSigner{Class: contract.SignerClassAgent, Source: "none", Why: demoNoSignIn},
		Notes: []string{"drafted from " + t.Entity + " in " + t.From,
			"the golden path holds the contract only: Backstage keeps the template's steps and skeleton; open it there to scaffold code"}}
	if t.OwnerKey != "" {
		plan.Notes = append(plan.Notes, "Backstage names "+t.OwnerKey+" as owner: declare service.owner in a later version")
	}
	plan.Contract, plan.Record = dashboard.NewDeclareText([]byte(text)), dashboard.NewDeclareText(rec.Marshal())
	plan.IntentDigest = digestOf(plan.Contract.Text + plan.Record.Text)
	return plan
}
