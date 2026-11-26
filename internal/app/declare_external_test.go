package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// claimReq is a person's claim of stripe for owner; the cluster client only
// reads the installation signer policy.
func (e *declareEnv) claimReq(owner string) DeclareRequest {
	return DeclareRequest{Service: "external:stripe", Owner: owner, Registry: e.host + "/acme", Dynamic: e.dyn,
		Signing: declareSigning{TrustedRoot: e.root, FulcioURL: e.ss.Fulcio, TimestampURL: e.ss.TSA}}
}

// stripeTags are the stripe repository's version and draft tags.
func (e *declareEnv) stripeTags(t *testing.T) []string {
	t.Helper()
	tags, _ := e.reg.Tags(t.Context(), e.host+"/acme/stripe")
	return slices.DeleteFunc(tags, func(tag string) bool {
		return strings.HasPrefix(tag, "pacto-sig-") || strings.HasPrefix(tag, "sha256-")
	})
}

// pulledStripe is the stripe bundle under tag and its record.
func (e *declareEnv) pulledStripe(t *testing.T, tag string) (*contract.Bundle, *contract.InferredRecord) {
	t.Helper()
	b, err := e.reg.Pull(t.Context(), e.host+"/acme/stripe:"+tag)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := contract.ReadInferredRecord(b.FS)
	if err != nil || rec == nil {
		t.Fatalf("%v %v", err, rec)
	}
	return b, rec
}

// claimed is stripe claimed for team-platform by the default approver.
func (e *declareEnv) claimed(t *testing.T) *DeclareResult {
	t.Helper()
	e.login(personToken("admin@acme.com"))
	return e.declare(t, e.claimReq("team-platform"))
}

func TestClaimPlanMinimalFolder(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.claimReq("team-platform")
	req.Dynamic = nil
	p, d := e.plan(t, req)
	if p.Service != "stripe" || p.Version != firstDeclaration || p.BundleRepo != e.host+"/acme/stripe" || p.InferredFrom != "external:stripe" ||
		p.Owner != "team:team-platform" || p.Cluster != "" || p.State != contract.DeclarationDeclared || p.Signer.Source != signerLogin {
		t.Fatalf("%+v", p)
	}
	c := d.bundle.Contract
	if c.Service.External != "stripe" || c.Service.Owner.Team != "team-platform" || len(c.Dependencies) != 0 || d.record.Inputs.Cluster != nil || len(d.record.Fields) != 0 {
		t.Fatalf("%s\n%s", d.raw, d.record.Marshal())
	}
}

// TestClaimReadsNoWorkload: a plan writes nothing, and with a cluster client
// the claim reads the signer policy and no workload.
func TestClaimReadsNoWorkload(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.plan(t, e.claimReq("team-platform"))
	if len(e.stripeTags(t)) != 0 {
		t.Fatal("a plan wrote")
	}
	for _, a := range e.dyn.Actions() {
		if r := a.GetResource().Resource; r != "configmaps" {
			t.Fatalf("read %s", r)
		}
	}
}

func TestClaimPlanName(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.claimReq("team-platform")
	req.Name = "payments-provider"
	p, _ := e.plan(t, req)
	if p.Service != "payments-provider" || p.BundleRepo != e.host+"/acme/payments-provider" || p.InferredFrom != "external:stripe" {
		t.Fatalf("%+v", p)
	}
	req.Name = "Bad/Name"
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); refusal(t, err).Code != codeFieldNotAcceptable || !strings.Contains(err.Error(), "is not a service name") {
		t.Fatal(err)
	}
	// A double dash is a service name the schema accepts.
	req.Name = "my--vendor"
	if p, _ := e.plan(t, req); p.Service != "my--vendor" {
		t.Fatalf("%+v", p)
	}
}

func TestClaimRefusesOtherEgress(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	for _, s := range []string{"external:other-egress", "external:", "external:Stripe"} {
		req := e.claimReq("team-platform")
		req.Service = s
		if _, _, err := e.svc.PlanDeclare(t.Context(), req); refusal(t, err).Code != codeExternalUnknown {
			t.Fatal(s, err)
		}
	}
}

func TestClaimRefusesNoOwner(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	_, _, err := e.svc.PlanDeclare(t.Context(), e.claimReq(""))
	if de := refusal(t, err); de.Code != codeOwnerRequired || de.CLI != "pacto claim stripe --owner <team> --registry "+e.host+"/acme" {
		t.Fatalf("%+v", de)
	}
}

func TestClaimRefusesInferredRepository(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.claimReq("team-platform")
	req.Registry = e.host + "/acme/" + contract.InferredRepository
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); refusal(t, err).Code != codeClaimRepositoryInferred {
		t.Fatal(err)
	}
}

func TestClaimBaselineOtherExternalRefused(t *testing.T) {
	t.Parallel()
	for external, want := range map[string]string{"  external: twilio\n": "which describes twilio", "": "which describes no external"} {
		e := newDeclareEnv(t)
		person := e.signer(t, personToken("ana@acme.com"))
		e.publishBundle(t, e.host+"/acme/stripe", "1.0.0", map[string]string{
			"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: stripe\n  version: 1.0.0\n" + external}, &person)
		_, _, err := e.svc.PlanDeclare(t.Context(), e.claimReq("team-platform"))
		if de := refusal(t, err); de.Code != codeExternalMismatch || !strings.Contains(de.Message, e.host+"/acme/stripe already holds stripe 1.0.0, "+want) ||
			!strings.Contains(de.Message, "claim with --name") {
			t.Fatalf("%+v", de)
		}
	}
}

func TestClaimBaselineSameExternalOwnerChangeChecked(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.withOwnerPolicy(t)
	e.claimed(t)
	p, _ := e.plan(t, e.claimReq("billing"))
	if p.Version != "0.1.1" || p.Owner != "team:billing" || !slices.Contains(p.MayAccept, "platform-admins") || p.Baseline == nil {
		t.Fatalf("%+v", p)
	}
	e.login(personToken("ana@acme.com")) // a billing member, not of team-platform
	if _, err := e.svc.Declare(t.Context(), e.claimReq("billing")); refusal(t, err).Code != delivery.OwnerSignerNotAllowed {
		t.Fatal(err)
	}
	if tags := e.stripeTags(t); !slices.Equal(tags, []string{firstDeclaration}) {
		t.Fatalf("%v", tags)
	}
}

func TestClaimSameOwnerUnchanged(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.withOwnerPolicy(t)
	e.claimed(t)
	res, err := e.svc.Declare(t.Context(), e.claimReq("team-platform"))
	if err != nil || res.Executed || res.Plan.Unchanged != firstDeclaration {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestClaimPublishesThroughDeclare(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.withOwnerPolicy(t)
	res := e.claimed(t)
	if !res.Executed || res.Draft || !res.Signed || res.Tag != firstDeclaration {
		t.Fatalf("%+v", res)
	}
	if tags := e.stripeTags(t); !slices.Equal(tags, []string{firstDeclaration}) {
		t.Fatalf("%v", tags)
	}
	b, rec := e.pulledStripe(t, firstDeclaration)
	want := contract.InferredSigner{Subject: "admin@acme.com", Issuer: personIssuer, Class: contract.SignerClassHuman}
	if d := rec.Declaration; d.State != contract.DeclarationDeclared || d.Asker != want || d.InferredFrom != "external:stripe" || d.Cluster != "" {
		t.Fatalf("%s", rec.Marshal())
	}
	if c := b.Contract.Service; c.External != "stripe" || c.Owner.Team != "team-platform" || c.Version != firstDeclaration {
		t.Fatalf("%+v", c)
	}
	if got := e.signers(t, res.Published); len(got) != 1 || got[0].Subject != want.Subject {
		t.Fatalf("%+v", got)
	}
}

func TestClaimDefaultApproverAllowed(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.withOwnerPolicy(t)
	p, _ := e.plan(t, e.claimReq("team-platform"))
	if !slices.Equal(p.MayAccept, []string{"platform-admins"}) ||
		!slices.Contains(p.Notes, "service.owner changes from none to team:team-platform: only a person among platform-admins may set it") {
		t.Fatalf("%+v", p)
	}
	if res := e.claimed(t); !res.Executed {
		t.Fatalf("%+v", res)
	}
}

// TestClaimRoutingGrantsNoOwnerRight: an unclaimed external is routed to the
// installation's default approvers, and that routing grants nobody else the
// first owner: a person who is no default approver is refused before
// anything is pushed.
func TestClaimRoutingGrantsNoOwnerRight(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.withOwnerPolicy(t)
	e.login(personToken("eve@acme.com"))
	_, err := e.svc.Declare(t.Context(), e.claimReq("team-platform"))
	if de := refusal(t, err); de.Code != delivery.OwnerSignerNotAllowed || !strings.HasPrefix(de.CLI, "pacto claim stripe --owner team-platform") {
		t.Fatalf("%+v", de)
	}
	if tags := e.stripeTags(t); len(tags) != 0 {
		t.Fatalf("pushed %v", tags)
	}
}

func TestClaimNoPolicyUnverified(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.login(personToken("admin@acme.com"))
	p, _ := e.plan(t, e.claimReq("team-platform"))
	if !slices.ContainsFunc(p.Notes, func(n string) bool { return strings.Contains(n, "unknown until a verified owner policy is read") }) {
		t.Fatalf("%+v", p.Notes)
	}
	if _, err := e.svc.Declare(t.Context(), e.claimReq("team-platform")); refusal(t, err).Code != delivery.OwnerPolicyUnverified {
		t.Fatal(err)
	}
}

func TestClaimKeyWritesDraft(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	dir := t.TempDir()
	keyFiles(t, dir)
	req := e.claimReq("team-platform")
	req.Signing.Key, req.OnBehalfOf = filepath.Join(dir, "cosign.key"), "ana"
	res := e.declare(t, req)
	if !res.Draft || !res.Signed || res.Tag != "draft-1" || res.Plan.AcceptCLI != "pacto claim stripe --registry "+e.host+"/acme --from-draft draft-1" {
		t.Fatalf("%+v", res)
	}
	_, rec := e.pulledStripe(t, "draft-1")
	if d := rec.Declaration; d.Asker.Issuer != signerKey || d.Asker.Class != contract.SignerClassAgent || d.OnBehalfOf != "ana" || d.InferredFrom != "external:stripe" {
		t.Fatalf("%s", rec.Marshal())
	}
}

// agentClaim writes an unsigned agent draft claiming stripe for
// team-platform on ana's behalf.
func (e *declareEnv) agentClaim(t *testing.T) {
	t.Helper()
	req := e.claimReq("team-platform")
	req.Agent, req.OnBehalfOf = true, "ana"
	if res := e.declare(t, req); res.Tag != "draft-1" {
		t.Fatalf("%+v", res)
	}
}

// claimDraft is a person's request accepting the claim draft tag.
func (e *declareEnv) claimDraft(tag string) DeclareRequest {
	req := e.claimReq("")
	req.FromDraft = tag
	return req
}

func TestClaimFromDraft(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.withOwnerPolicy(t)
	e.agentClaim(t)
	e.login(personToken("admin@acme.com"))
	res := e.declare(t, e.claimDraft("draft-1"))
	if res.Draft || res.Tag != firstDeclaration || res.Plan.UnbackedUnchecked != "" || len(res.Plan.Unbacked) != 0 {
		t.Fatalf("%+v", res.Plan)
	}
	_, rec := e.pulledStripe(t, firstDeclaration)
	if d := rec.Declaration; d.State != contract.DeclarationDeclared || d.Draft == nil || d.Draft.Tag != "draft-1" || d.Draft.OnBehalfOf != "ana" ||
		d.InferredFrom != "external:stripe" {
		t.Fatalf("%s", rec.Marshal())
	}
}

func TestClaimFromDraftOtherSystemRefused(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.agentClaim(t)
	e.login(personToken("admin@acme.com"))
	req := e.claimDraft("draft-1")
	req.Service, req.Name = "external:twilio", "stripe"
	if _, err := e.svc.Declare(t.Context(), req); refusal(t, err).Code != codeDraftNotFound || !strings.Contains(err.Error(), "claims another system, not twilio") {
		t.Fatal(err)
	}
	// A claim draft is no workload draft.
	claim := e.claimReq("team-platform")
	claim.Agent, claim.Name = true, "invoices"
	e.declare(t, claim)
	if _, err := e.svc.Declare(t.Context(), e.fromDraft("draft-1")); refusal(t, err).Code != codeDraftNotFound {
		t.Fatal(err)
	}
}

func TestClaimFromDraftNoClusterInput(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.withOwnerPolicy(t)
	e.agentClaim(t)
	req := e.claimDraft("draft-1")
	req.Dynamic = nil
	p, d := e.plan(t, req)
	if p.Owner != "team:team-platform" || p.Service != "stripe" || d.record.Inputs.Cluster != nil || p.InferredFrom != "external:stripe" {
		t.Fatalf("%+v", p)
	}
}

func TestClaimFromDraftTakesDraftOwner(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.withOwnerPolicy(t)
	e.agentClaim(t)
	req := e.claimDraft("draft-1")
	req.Owner = "team-platform" // the draft's own owner
	if p, _ := e.plan(t, req); p.Owner != "team:team-platform" {
		t.Fatalf("%+v", p)
	}
	req.Owner = "billing"
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); refusal(t, err).Code != codeOwnerRequired || !strings.Contains(err.Error(), "the draft names owner team-platform") {
		t.Fatal(err)
	}
}

// TestClaimFromDraftWithoutOwnerRefused: a draft whose pacto.yaml names no
// team leaves the claim with no owner.
func TestClaimFromDraftWithoutOwnerRefused(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "dev"}, Fields: []contract.Inferred{},
		Declaration: &contract.InferredDeclaration{State: contract.DeclarationDraft, Asker: contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent},
			InferredFrom: "external:stripe"}}
	e.publishBundle(t, e.host+"/acme/stripe", "draft-1", map[string]string{
		"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: stripe\n  version: 0.1.0\n  external: stripe\n", contract.InferredRecordFile: string(rec.Marshal())}, nil)
	if _, _, err := e.svc.PlanDeclare(t.Context(), e.claimDraft("draft-1")); refusal(t, err).Code != codeOwnerRequired {
		t.Fatal(err)
	}
}

func TestClaimRefusalCLIIsClaim(t *testing.T) {
	e := newDeclareEnv(t)
	reg := e.host + "/acme"
	req := e.claimReq("team-platform")
	req.Name, req.Namespace, req.Dir = "payments-provider", "ignored", "/ignored"
	req.Accept, req.MaxItems, req.CallWindow = []string{"x"}, 500, time.Hour
	if got := req.command("sha256:x"); got != "pacto claim stripe --owner team-platform --name payments-provider --registry "+reg+" --intent sha256:x" {
		t.Fatal(got)
	}
	if got := req.AcceptCLI("payments-provider", "draft-2"); got != "pacto claim stripe --name payments-provider --registry "+reg+" --from-draft draft-2" {
		t.Fatal(got)
	}
	if got := e.req().AcceptCLI("invoices", "draft-2"); got != AcceptDraftCLI("invoices", e.req().Registry, "draft-2") {
		t.Fatal(got)
	}
	noReg := e.claimReq("")
	noReg.Registry = ""
	if _, _, err := e.svc.PlanDeclare(t.Context(), noReg); refusal(t, err).CLI != "pacto claim stripe --owner <team> --registry <registry>" {
		t.Fatal(err)
	}
	_, err := e.svc.Declare(t.Context(), e.claimDraft("draft-9"))
	if de := refusal(t, err); de.Code != codeDraftNotFound || de.CLI != "pacto claim stripe --owner <team> --registry "+reg {
		t.Fatalf("%+v", de)
	}
	t.Setenv("SIGSTORE_ID_TOKEN", ciWorkflowToken())
	p, _ := e.plan(t, e.claimReq("team-platform"))
	if !slices.Contains(p.Notes, "an ambient OIDC token is set: this run writes a draft; a person accepts it with pacto claim stripe --registry "+reg+" --from-draft <tag>") {
		t.Fatalf("%v", p.Notes)
	}
}

func TestClaimFolderWriteFails(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.withOwnerPolicy(t)
	e.claimed(t)
	writes := 0
	e.svc.sys.writeFile = func(name string, data []byte, perm os.FileMode) error {
		if filepath.Base(name) == DefaultContractPath {
			if writes++; writes == 2 { // the baseline's copy
				return errors.New("disk full")
			}
		}
		return os.WriteFile(name, data, perm)
	}
	if _, _, err := e.svc.PlanDeclare(t.Context(), e.claimReq("billing")); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatal(err)
	}
}

func TestClaimDraftFolderUnreadable(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.claimReq("")
	req.Dir = filepath.Join(t.TempDir(), "missing")
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); err == nil {
		t.Fatal("planned from a missing draft folder")
	}
}
