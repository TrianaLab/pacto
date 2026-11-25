package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/trianalab/pacto/v4/internal/shellquote"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// demoPathConfigurations names the configuration of the demo's golden path
// that consumes each offering; an added offering is configured under its own
// name. ponytail: the one demo path; the real planner reads the path's
// contract (app.PlanDeclare).
var demoPathConfigurations = map[string]string{"postgresql": "database"}

// demoCreator is the demo's dashboard.ServiceCreator, held in the tab: it
// plans creating a service from the demo's golden path into
// demo.pacto.local/<name>, checks each offering value against the
// consumable offering's closed schema, nobody signs and Save draft keeps a
// draft-<n> per service in memory. Nothing reaches a registry.
type demoCreator struct {
	who    func(*http.Request) string
	mu     sync.Mutex
	drafts map[string][]demoDraft // per created service name, oldest first
}

func newDemoCreator(who func(*http.Request) string) *demoCreator {
	return &demoCreator{who: who, drafts: map[string][]demoDraft{}}
}

// CanWrite implements dashboard.ServiceCreator: the demo saves drafts in the tab.
func (*demoCreator) CanWrite() bool { return true }

// Plan implements dashboard.ServiceCreator.
func (c *demoCreator) Plan(r *http.Request, p dashboard.CreateServicePath, in dashboard.CreateServiceInput) (*dashboard.CreateServiceAnswer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.plan(c.who(r), p, in), nil
}

func (c *demoCreator) plan(who string, p dashboard.CreateServicePath, in dashboard.CreateServiceInput) *dashboard.CreateServiceAnswer {
	list, providers := demoConsumable(p.View)
	ans := &dashboard.CreateServiceAnswer{Signing: demoSigning(), Write: dashboard.DeclareWrite{Available: true}, Offerings: demoOfferingViews(providers, p.Requests)}
	initCLI := demoInitCLI(p, in)
	repo := cmp.Or(in.Repository, demoDeclareRegistry+"/"+in.Name)
	if !strings.HasPrefix(repo, demoDeclareRegistry+"/") || repo[strings.LastIndex(repo, "/")+1:] != in.Name {
		ans.Refusal = &dashboard.DeclareRefusal{Code: "RepositoryOutsideRegistry", Command: initCLI,
			Message: repo + " is not a repository of " + in.Name + " under " + demoDeclareRegistry}
		return ans
	}
	var from *demoDraft
	if in.FromDraft != nil {
		if from = c.draft(in.Name, *in.FromDraft); from == nil {
			ans.Refusal = &dashboard.DeclareRefusal{Code: "DraftNotFound", Message: in.FromDraft.Tag + " is not a draft of " + in.Name + " with the digest shown"}
			return ans
		}
	}
	partial := list.Meta.Completeness != fleet.CompletenessComplete
	if ans.Refusals = demoCreateRefusals(p, in, providers, partial, initCLI); len(ans.Refusals) > 0 {
		return ans
	}
	ans.Plan = demoCreatePlan(who, p, in, repo, initCLI, from)
	ans.Plan.ConsumableAsOf, ans.Plan.ConsumableCompleteness = list.Meta.AsOf.UTC().Format(time.RFC3339), string(list.Meta.Completeness)
	ans.Drafts, ans.DraftsTotal = c.views(in.Name), len(c.drafts[in.Name])
	return ans
}

// Accept implements dashboard.ServiceCreator: Save draft writes the next
// draft-<n>, unless the newest draft is this plan again; nobody signs.
func (c *demoCreator) Accept(r *http.Request, p dashboard.CreateServicePath, in dashboard.CreateServiceAcceptInput) (*dashboard.CreateServiceAcceptAnswer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	who := c.who(r)
	if in.Mode != "draft" || in.FromDraft != nil {
		return &dashboard.CreateServiceAcceptAnswer{Refusal: &dashboard.DeclareRefusal{Code: "NotAllowed",
			Message: "you cannot sign here: " + demoNoSignIn, Command: demoInitCLI(p, in.CreateServiceInput)}}, nil
	}
	plan := in.CreateServiceInput
	plan.IntentDigest = ""
	ans := c.plan(who, p, plan)
	if ans.Plan == nil {
		return &dashboard.CreateServiceAcceptAnswer{Refusal: ans.Refusal, Refusals: ans.Refusals}, nil
	}
	if ans.Plan.IntentDigest != in.IntentDigest {
		return &dashboard.CreateServiceAcceptAnswer{Plan: ans, Refusal: &dashboard.DeclareRefusal{Code: "PlanStale",
			Message: "the plan changed since intent " + in.IntentDigest + " was reviewed; it is now " + ans.Plan.IntentDigest + ": review the new plan"}}, nil
	}
	list := c.drafts[in.Name]
	if n := len(list); n > 0 && list[n-1].onBehalfOf == who && list[n-1].intent == in.IntentDigest {
		return createdDraftAnswer(ans.Plan.BundleRepo, list[n-1]), nil
	}
	dr := demoDraft{tag: fmt.Sprintf("draft-%d", len(list)+1), onBehalfOf: who, intent: in.IntentDigest}
	dr.digest = digestOf(dr.tag + "\n" + ans.Plan.Contract.Text)
	c.drafts[in.Name] = append(list, dr)
	return createdDraftAnswer(ans.Plan.BundleRepo, dr), nil
}

func createdDraftAnswer(repo string, dr demoDraft) *dashboard.CreateServiceAcceptAnswer {
	return &dashboard.CreateServiceAcceptAnswer{Published: repo + "@" + dr.digest, Tag: dr.tag, Draft: true}
}

// draft is the draft of name ref names, nil when its tag or digest moved.
func (c *demoCreator) draft(name string, ref dashboard.DraftRef) *demoDraft {
	for i, dr := range c.drafts[name] {
		if dr.tag == ref.Tag && dr.digest == ref.Digest {
			return &c.drafts[name][i]
		}
	}
	return nil
}

// views are name's drafts as the panel lists them, newest first, at most 20.
func (c *demoCreator) views(name string) []dashboard.DeclareDraft {
	var out []dashboard.DeclareDraft
	list := c.drafts[name]
	for i := len(list) - 1; i >= 0 && len(out) < 20; i-- {
		out = append(out, dashboard.DeclareDraft{Tag: list[i].tag, Digest: list[i].digest, State: contract.DeclarationDraft,
			Asker: unsignedAsker, OnBehalfOf: list[i].onBehalfOf})
	}
	return out
}

// demoConsumable is the consumable offerings list of q and the provider a
// create consumes per offering, the first by service key, as the dashboard's
// creator reads it.
func demoConsumable(q *fleet.Query) (*fleet.EntityList, map[string]fleet.ConsumableProvider) {
	list, _ := q.Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindOffering}, Consumable: true, Limit: fleet.MaxEntityLimit}) // ponytail: a fixed valid filter
	out := map[string]fleet.ConsumableProvider{}
	for _, e := range list.Entities {
		out[e.Key] = e.Consumable.Providers[0]
	}
	return list, out
}

// demoOfferingViews are the consumable offerings, the requested ones first.
func demoOfferingViews(providers map[string]fleet.ConsumableProvider, requested []string) []dashboard.CreateServiceOfferingView {
	var first, rest []dashboard.CreateServiceOfferingView
	for _, name := range slices.Sorted(maps.Keys(providers)) {
		p := providers[name]
		v := dashboard.CreateServiceOfferingView{Offering: name, Version: p.Version, Requested: slices.Contains(requested, name), Classes: p.Classes, Parameters: p.Parameters}
		if v.Requested {
			first = append(first, v)
		} else {
			rest = append(rest, v)
		}
	}
	return append(first, rest...)
}

// demoCreateRefusals are every refused input of in: an offering nobody may
// consume here (unknown on a partial read), an environment the path does not
// declare (the demo path declares none) and each value the offering's closed
// schema refuses, at its stable field with both fixes.
func demoCreateRefusals(p dashboard.CreateServicePath, in dashboard.CreateServiceInput, providers map[string]fleet.ConsumableProvider, partial bool, initCLI string) []dashboard.CreateServiceRefusal {
	var out []dashboard.CreateServiceRefusal
	for _, pick := range in.Offerings {
		prov, ok := providers[pick.Offering]
		switch {
		case !ok && partial:
			out = append(out, dashboard.CreateServiceRefusal{Code: "OfferingUnknown", Field: "offerings[" + pick.Offering + "]",
				Message: "Pacto could not confirm " + pick.Offering + " is consumable: the offerings list is partial"})
			continue
		case !ok:
			out = append(out, dashboard.CreateServiceRefusal{Code: "OfferingNotConsumable", Field: "offerings[" + pick.Offering + "]",
				Message: pick.Offering + " is not an offering you may consume here: pacto fleet offerings --consumable lists those you may"})
			continue
		}
		for _, env := range slices.Sorted(maps.Keys(pick.Environments)) {
			out = append(out, dashboard.CreateServiceRefusal{Code: "EnvironmentNotDeclared", Field: "environments[" + env + "]",
				Message: "the golden path " + p.Name + " declares no environment " + env})
		}
		cfg := cmp.Or(demoPathConfigurations[pick.Offering], pick.Offering)
		schema, _ := p.View.OfferingSchema(prov.Revision.Key, demoProviderConfiguration(prov))
		for _, r := range validation.CheckOfferingValues(schema, pick.Values, nil) {
			key := strings.TrimPrefix(r.Field, "values.")
			ref := dashboard.CreateServiceRefusal{Code: string(r.Code), Message: r.Message, Field: "configurations[" + cfg + "].values." + key,
				Closest: r.Closest, Allowed: r.Allowed, CLI: initCLI}
			if r.Closest != nil {
				ref.CLI += " --set " + shellquote.Quote(fmt.Sprintf("configurations[%s].values.%s=%v", cfg, key, r.Closest))
				data, _ := json.Marshal(map[string]any{"offerings": []dashboard.CreateServiceOffering{{Offering: pick.Offering, Values: map[string]any{key: r.Closest}}}}) // ponytail: decoded values always marshal
				ref.MCP = string(data)
			}
			out = append(out, ref)
		}
	}
	return out
}

// demoProviderConfiguration is the provider's configuration the parameters
// come from: the first one they name.
func demoProviderConfiguration(p fleet.ConsumableProvider) string {
	for _, par := range p.Parameters {
		if par.Configuration != "" {
			return par.Configuration
		}
	}
	return ""
}

// demoInitCLI is the pacto init that recreates the created folder, every
// token quoted.
func demoInitCLI(p dashboard.CreateServicePath, in dashboard.CreateServiceInput) string {
	cmd := []string{"pacto", "init", in.Name, "--from", p.Ref}
	if in.Owner != "" {
		cmd = append(cmd, "--owner", in.Owner)
	}
	cmd = append(cmd, "--registry", demoDeclareRegistry)
	for i, tok := range cmd {
		cmd[i] = shellquote.Quote(tok)
	}
	return strings.Join(cmd, " ")
}

// demoCreatePlan is what creating in's service from p would publish, as a
// draft for who: version 0.1.0 with no deploy artifact, the path's origin and
// the values set on each offering, and from's digest folded into the intent
// when a draft is being accepted.
func demoCreatePlan(who string, p dashboard.CreateServicePath, in dashboard.CreateServiceInput, repo, initCLI string, from *demoDraft) *dashboard.CreateServicePlan {
	revision := cmp.Or(in.Revision, "pacto-create:"+who+":"+demoNow().UTC().Format(time.RFC3339))
	var b strings.Builder
	fmt.Fprintf(&b, "pactoVersion: \"2.5\"\nservice:\n  name: %s\n  version: 0.1.0\n", in.Name)
	if in.Owner != "" {
		fmt.Fprintf(&b, "  owner:\n    team: %s\n", in.Owner)
	}
	fmt.Fprintf(&b, "  origin:\n    ref: %s\n", p.Ref)
	if len(in.Offerings) > 0 {
		b.WriteString("configurations:\n")
	}
	for _, pick := range in.Offerings {
		values, _ := json.Marshal(pick.Values) // ponytail: decoded values always marshal
		fmt.Fprintf(&b, "- name: %s\n  values: %s\n", cmp.Or(demoPathConfigurations[pick.Offering], pick.Offering), values)
	}
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "4.6.0"}, Fields: []contract.Inferred{},
		Declaration: &contract.InferredDeclaration{State: contract.DeclarationDraft, Asker: unsignedAsker, OnBehalfOf: who}}
	plan := &dashboard.CreateServicePlan{Service: in.Name, Version: "0.1.0", BundleRepo: repo, State: contract.DeclarationDraft, Owner: in.Owner,
		Signer: dashboard.DeclareSigner{Class: contract.SignerClassAgent, Source: "none", Why: demoNoSignIn}, Path: p.Ref, PathName: p.Name,
		PathVersion: p.Version, Namespace: in.Namespace, Revision: revision, InitCLI: initCLI, Contract: dashboard.NewDeclareText([]byte(b.String())),
		Signature: fleet.CreationStep{Step: fleet.StepSignature, Status: fleet.StepStatusUnknown, Detail: "signature not checked: the demo names no template key"},
		Record:    dashboard.NewDeclareText(rec.Marshal()),
		Notes:     []string{"a created service: every value comes from the golden path " + p.Name + " and the values you set; review pacto.yaml"}}
	intent := plan.Contract.Text + in.Namespace + "\n" + revision
	if from != nil {
		intent += from.digest
		plan.Notes = append(plan.Notes, "A draft is accepted only with a person's signature: run the command in a terminal where you sign in")
	}
	plan.IntentDigest = digestOf(intent)
	return plan
}
