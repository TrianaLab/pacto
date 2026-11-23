package main

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/lock"
)

// demoSignIssuer issues the demo's simulated sign-in, the one a person
// signs a claim with in the tab.
const demoSignIssuer = "https://login.demo.pacto.local"

// demoClaimNoDraft is why the demo saves no claim draft.
const demoClaimNoDraft = "the demo claims an external system only with a person's simulated sign-in"

// demoClaimPolicy is what the demo leaves out of a claim's owner rule.
const demoClaimPolicy = "The demo has no owner policy: on a cluster only a default approver of the installation's owner policy sets an external system's first owner"

// demoClaim is the claim of one external system signed in this tab: its
// owner, service name, version and the revision the fleet reads.
type demoClaim struct {
	owner, name, version string
	n                    int // how many claims of this system the tab signed
	rev                  fleet.RawRevision
}

// notName matches every character a service name may not hold.
var notName = regexp.MustCompile(`[^a-z0-9-]`)

// claimName is the service name a claim of id defaults to, as
// inferredcontract.ServiceName names it.
// ponytail: a copy, since that package links Kubernetes into the wasm build;
// TestDemoClaimNameMatchesInferredContract keeps the two in step.
func claimName(id fleet.ExternalKey) string {
	seg := string(id)[strings.LastIndexByte(string(id), '/')+1:]
	name := strings.Trim(notName.ReplaceAllString(seg, "-"), "-")
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}

// person reports whether who signs as a person: an agent's service account
// writes drafts only.
func person(who string) bool { return who != "" && !strings.HasPrefix(who, "system:serviceaccount:") }

// planClaim is what claiming id for in.Owner would publish, signed by who
// through the simulated fresh login; back from it intent is the plan the
// login was started for. The second result is the claim to record.
func (d *demoDeclarer) planClaim(who string, id fleet.ExternalKey, in dashboard.ClaimInput, intent string) (*dashboard.DeclareAnswer, demoClaim) {
	ans := &dashboard.DeclareAnswer{Write: dashboard.DeclareWrite{Available: true}, NoDraft: demoClaimNoDraft,
		Signing: dashboard.DeclareSigning{Mode: "none", Reason: who + " is an agent: an agent writes drafts and a person signs a claim"}}
	if person(who) {
		ans.Signing = dashboard.DeclareSigning{Mode: "fresh-login", Identity: who}
	}
	if in.Owner == "" {
		ans.Refusal = &dashboard.DeclareRefusal{Code: "OwnerRequired", Message: "name the team that owns " + string(id),
			Command: "pacto claim " + string(id) + " --owner <team> --registry " + demoDeclareRegistry}
		return ans, demoClaim{}
	}
	prev := d.claims[id]
	c := demoClaim{owner: in.Owner, name: cmp.Or(in.Name, claimName(id)), n: prev.n + 1}
	unchanged := prev.n > 0 && prev.owner == c.owner && prev.name == c.name
	if unchanged {
		c.n = prev.n // the same claim again plans the version it already is
	}
	c.version = fmt.Sprintf("0.%d.0", c.n)
	text, rec := claimTexts(id, c, who)
	p := &dashboard.DeclarePlan{Service: c.name, Version: c.version, BundleRepo: demoDeclareRegistry + "/" + c.name, State: contract.DeclarationDeclared,
		InferredFrom: contract.ExternalInferredFrom(string(id)), Deploy: "none", Ranges: []dashboard.DeclareRange{}, Owner: c.owner,
		Signer:     dashboard.DeclareSigner{Subject: who, Issuer: demoSignIssuer, Class: contract.SignerClassHuman, Source: "login"},
		PolicyNote: demoClaimPolicy, Contract: dashboard.NewDeclareText([]byte(text)), Record: dashboard.NewDeclareText(rec.Marshal())}
	p.IntentDigest = digestOf(p.Contract.Text + p.Record.Text)
	if unchanged {
		p.Unchanged = prev.version
	}
	ans.Plan = p
	ans.Command = "pacto claim " + string(id) + " --owner " + c.owner
	if in.Name != "" {
		ans.Command += " --name " + in.Name
	}
	ans.Command += " --registry " + demoDeclareRegistry + " --intent " + p.IntentDigest
	if ans.Signing.Mode == "fresh-login" {
		// ponytail: the tab trusts its own simulated sign-in, so the ticket is the intent.
		ans.SignTicket, ans.SignedIn = p.IntentDigest, intent == p.IntentDigest
	}
	return ans, c
}

// acceptClaim publishes the claim plan who reviewed, signed by who: the
// claimed revision joins the tab's fleet and nothing reaches a registry.
func (d *demoDeclarer) acceptClaim(who string, id fleet.ExternalKey, in dashboard.DeclareAcceptInput) (*dashboard.DeclareAcceptAnswer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	ans, c := d.planClaim(who, id, in.ClaimInput, in.IntentDigest)
	switch {
	case in.Mode != "sign" || in.FromDraft != nil:
		return &dashboard.DeclareAcceptAnswer{Refusal: &dashboard.DeclareRefusal{Code: "DraftNotOffered", Message: demoClaimNoDraft, Command: ans.Command}}, nil
	case !person(who):
		return &dashboard.DeclareAcceptAnswer{Refusal: &dashboard.DeclareRefusal{Code: "NotAllowed", Message: ans.Signing.Reason, Command: ans.Command}}, nil
	case ans.Refusal != nil:
		return &dashboard.DeclareAcceptAnswer{Refusal: ans.Refusal}, nil
	case ans.Plan.IntentDigest != in.IntentDigest:
		return &dashboard.DeclareAcceptAnswer{Plan: ans, Refusal: &dashboard.DeclareRefusal{Code: "PlanStale",
			Message: "the plan changed since intent " + in.IntentDigest + " was reviewed; it is now " + ans.Plan.IntentDigest + ": review the new plan"}}, nil
	case ans.Plan.Unchanged != "":
		return &dashboard.DeclareAcceptAnswer{Refusal: &dashboard.DeclareRefusal{Code: "AlreadyDeclared",
			Message: string(id) + " is already claimed for " + c.owner + " at " + ans.Plan.Unchanged + ": nothing changed"}}, nil
	}
	rev := claimRevision(id, c, who)
	c.rev = rev
	d.claims[id] = c
	if d.claimed != nil {
		revs := []fleet.RawRevision{}
		for _, k := range slices.Sorted(maps.Keys(d.claims)) {
			revs = append(revs, d.claims[k].rev)
		}
		if err := d.claimed(revs); err != nil {
			return nil, err
		}
	}
	return &dashboard.DeclareAcceptAnswer{Published: ans.Plan.BundleRepo + "@" + rev.Digest, Tag: c.version, Signed: true}, nil
}

// claimTexts are the pacto.yaml and pacto.inferred.yaml claim c of id
// publishes, declared by who: the service, its version, the external it
// describes and its owner, and a record naming external:<id>.
func claimTexts(id fleet.ExternalKey, c demoClaim, who string) (string, *contract.InferredRecord) {
	text := fmt.Sprintf("pactoVersion: \"2.5\"\nservice:\n  name: %q\n  version: %q\n  external: %q\n  owner:\n    team: %q\n", c.name, c.version, string(id), c.owner)
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "4.6.0"},
		Declaration: &contract.InferredDeclaration{State: contract.DeclarationDeclared,
			Asker:        contract.InferredSigner{Subject: who, Issuer: demoSignIssuer, Class: contract.SignerClassHuman},
			InferredFrom: contract.ExternalInferredFrom(string(id))}}
	return text, rec
}

// claimRevision is claim c of id as the fleet reads it from the default
// registry, its signature checked as who's.
func claimRevision(id fleet.ExternalKey, c demoClaim, who string) fleet.RawRevision {
	text, rec := claimTexts(id, c, who)
	fsys := fstest.MapFS{"pacto.yaml": {Data: []byte(text)}, contract.InferredRecordFile: {Data: rec.Marshal()}}
	ct, doc, err := contract.ParseDocument([]byte(text))
	if err != nil {
		panic(err) // quoted values the route's patterns checked
	}
	h, err := lock.HashFS(fsys)
	if err != nil {
		panic(err) // an in-memory FS
	}
	return fleet.RawRevision{
		Bundle: &contract.Bundle{Contract: ct, RawYAML: []byte(text), FS: fsys}, Document: doc,
		RequestedRef: requestedRef(demoRegistry, c.name, c.version), ResolvedRef: demoRegistry + c.name + "@" + h, Digest: h,
		Provenance: &fleet.BundleProvenance{Signature: fleet.BundleSignature{Status: fleet.SignatureUnverified},
			Signers: []fleet.VerifiedSigner{{Subject: who, Issuer: demoSignIssuer, Class: contract.SignerClassHuman}},
			Source:  "local", CheckedAt: demoNow()},
	}
}
