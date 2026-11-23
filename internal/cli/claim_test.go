package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// claimPlanFixture is a first claim of stripe for team-platform, planned for
// a person's login.
func claimPlanFixture() *app.DeclarePlan {
	return &app.DeclarePlan{Service: "stripe", Version: "0.1.0", BundleRepo: "ghcr.io/acme/stripe", State: contract.DeclarationDeclared,
		InferredFrom: "external:stripe", Deploy: "none", Owner: "team:team-platform",
		Signer:       app.SignerView{Issuer: "https://oauth2.sigstore.dev/auth", Class: "human", Source: "login"},
		PolicyNote:   "no installation signer policy was read (no Kubernetes client was configured); readers judge this signature with their own",
		Notes:        []string{"service.owner changes from none to team:team-platform: only a person among the default approvers may set it"},
		IntentDigest: declareIntent}
}

const claimPlanText = `plan: oci://ghcr.io/acme/stripe:0.1.0 (first declaration, contract only, deploy: none)
state: declared, signed by you, after a Sigstore login at https://oauth2.sigstore.dev/auth
inferredFrom external:stripe
note: service.owner changes from none to team:team-platform: only a person among the default approvers may set it
no installation signer policy was read (no Kubernetes client was configured); readers judge this signature with their own
intent: ` + declareIntent + "\n"

func TestClaimCommandPlanText(t *testing.T) {
	calls := noKube(t)
	var got app.DeclareRequest
	out, _, err := runRoot(t, declareDeps(t, &got, claimPlanFixture(), nil, nil), nil, "claim", "stripe", "--owner", "team-platform",
		"--name", "payments-stripe", "--registry", "ghcr.io/acme")
	want := claimPlanText + "nothing was written: run again with --yes --intent " + declareIntent + " to carry out this plan\n"
	if err != nil || out != want {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Service != "external:stripe" || got.Owner != "team-platform" || got.Name != "payments-stripe" || got.Registry != "ghcr.io/acme" ||
		got.Dynamic != nil || *calls != 1 {
		t.Fatalf("%+v (%d clients)", got, *calls)
	}
}

func TestClaimCommandPublishedText(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	run := func(req app.DeclareRequest) (*app.DeclareResult, error) {
		p := *claimPlanFixture()
		req.OnPlan(&p)
		p.Signer.Subject, p.Signer.Issuer = "alice@acme.com", "https://accounts.google.com"
		return &app.DeclareResult{Plan: p, Executed: true, Signed: true, Tag: "0.1.0", Published: "ghcr.io/acme/stripe@sha256:7c1d"}, nil
	}
	out, _, err := runRoot(t, declareDeps(t, &got, nil, nil, run), nil, "claim", "stripe", "--owner", "team-platform", "--registry", "ghcr.io/acme", "--yes")
	want := claimPlanText + "claimed external:stripe -> oci://ghcr.io/acme/stripe:0.1.0\n" +
		"  signed by alice@acme.com (person), inferredFrom: stripe, no Git write\n"
	if err != nil || out != want {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestClaimCommandDraftText(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	run := func(req app.DeclareRequest) (*app.DeclareResult, error) {
		p := *claimPlanFixture()
		p.State, p.AcceptCLI = contract.DeclarationDraft, "pacto claim stripe --registry ghcr.io/acme --from-draft draft-1"
		return &app.DeclareResult{Plan: p, Executed: true, Draft: true, Tag: "draft-1", Published: "ghcr.io/acme/stripe@sha256:dd"}, nil
	}
	out, _, err := runRoot(t, declareDeps(t, &got, nil, nil, run), nil, "claim", "stripe", "--owner", "team-platform", "--draft", "--yes")
	want := "wrote draft-1 of external:stripe (ghcr.io/acme/stripe@sha256:dd): a person accepts it with " +
		"pacto claim stripe --registry ghcr.io/acme --from-draft draft-1 --yes\n"
	if !strings.HasSuffix(out, want) || err != nil || !got.Draft {
		t.Fatalf("%v\n%s", err, out)
	}
	// An unchanged claim prints its plan and nothing it wrote.
	unchanged := func(req app.DeclareRequest) (*app.DeclareResult, error) {
		p := *claimPlanFixture()
		p.Unchanged = "0.1.0"
		return &app.DeclareResult{Plan: p}, nil
	}
	out, _, err = runRoot(t, declareDeps(t, &got, nil, nil, unchanged), nil, "claim", "stripe", "--owner", "team-platform", "--yes")
	if err != nil || out != "plan: stripe unchanged since 0.1.0: nothing to publish\n" {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestClaimCommandJSON(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	out, _, err := runRoot(t, declareDeps(t, &got, claimPlanFixture(), nil, nil), nil, "claim", "stripe", "--owner", "team-platform", "--output-format", "json")
	var plan app.DeclarePlan
	if err != nil || json.Unmarshal([]byte(out), &plan) != nil || plan.InferredFrom != "external:stripe" || plan.Owner != "team:team-platform" {
		t.Fatalf("%v\n%s", err, out)
	}
	run := func(req app.DeclareRequest) (*app.DeclareResult, error) {
		if req.OnPlan != nil {
			t.Error("JSON prints the plan before the login")
		}
		return &app.DeclareResult{Plan: *claimPlanFixture(), Executed: true, Tag: "0.1.0"}, nil
	}
	out, _, err = runRoot(t, declareDeps(t, &got, nil, nil, run), nil, "claim", "stripe", "--owner", "team-platform", "--yes", "--output-format", "json")
	var res app.DeclareResult
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Tag != "0.1.0" || res.Plan.Service != "stripe" {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestClaimCommandRequiresOwner(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	d := declareDeps(t, &got, claimPlanFixture(), nil, nil)
	if _, _, err := runRoot(t, d, nil, "claim", "stripe"); err == nil || err.Error() != "claiming stripe names its owner: pass --owner <team>" {
		t.Fatal(err)
	}
	if _, _, err := runRoot(t, d, nil, "claim", "stripe", "--owner", "x", "--draft", "--from-draft", "draft-1"); err == nil || err != errDraftAndFromDraft {
		t.Fatal(err)
	}
}

// The acceptance command a claim draft prints runs as printed: the draft
// names the owner.
func TestClaimCommandFromDraftNeedsNoOwner(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	_, _, err := runRoot(t, declareDeps(t, &got, claimPlanFixture(), nil, nil), nil, "claim", "stripe", "--registry", "ghcr.io/acme", "--from-draft", "draft-1")
	if err != nil || got.FromDraft != "draft-1" || got.Owner != "" || got.Service != "external:stripe" {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestClaimCommandFlags(t *testing.T) {
	root := newRootCommand(app.NewService(nil, nil), VersionInfo{Version: "dev"}, defaultDeps())
	cmd, _, err := root.Find([]string{"claim"})
	if err != nil || cmd.Name() != "claim" {
		t.Fatal(err)
	}
	for _, workloadOnly := range []string{"namespace", "dir", "accept", "max-items", "call-window"} {
		if cmd.Flags().Lookup(workloadOnly) != nil {
			t.Errorf("claim registers --%s", workloadOnly)
		}
	}
	for _, shared := range []string{"owner", "name", "registry", "yes", "intent", "draft", "from-draft", "sign-key", "delivery-trust", "trusted-root"} {
		if cmd.Flags().Lookup(shared) == nil {
			t.Errorf("claim lacks --%s", shared)
		}
	}
	if cmd.Flags().Lookup("owner").Annotations != nil {
		t.Error("--owner is no required flag: an accepted draft names the owner")
	}
}

func TestDeclareRefusesExternalSubject(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	_, _, err := runRoot(t, declareDeps(t, &got, declarePlanFixture(), nil, nil), nil, "declare", "external:stripe")
	if err == nil || err.Error() != "claim an external system with pacto claim stripe --owner <team>" {
		t.Fatal(err)
	}
}

// A claim reads no workload: with no kubeconfig it still plans, and a client
// that is there reads the installation signer policy.
func TestClaimCommandNoKubeconfigStillPlans(t *testing.T) {
	calls := noKube(t)
	var got app.DeclareRequest
	d := declareDeps(t, &got, claimPlanFixture(), nil, nil)
	if _, _, err := runRoot(t, d, nil, "claim", "stripe", "--owner", "team-platform"); err != nil || *calls != 0 {
		t.Fatalf("a run without a registry built %d clients", *calls)
	}
	if _, _, err := runRoot(t, d, nil, "claim", "stripe", "--owner", "team-platform", "--registry", "ghcr.io/acme"); err != nil || got.Dynamic != nil {
		t.Fatalf("%v %+v", err, got)
	}
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	initDynamic = func() (dynamic.Interface, error) { return dyn, nil }
	if _, _, err := runRoot(t, d, nil, "claim", "stripe", "--owner", "team-platform", "--registry", "ghcr.io/acme"); err != nil || got.Dynamic != dyn {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestFleetGetExternalOwnerLine(t *testing.T) {
	for _, tc := range []struct {
		claim *fleet.ExternalClaim
		want  string
	}{
		{nil, "  owner    unclaimed, routed to the default approvers (routing only)\n"},
		{&fleet.ExternalClaim{Hidden: true}, "  owner    claimed by a service you cannot see\n"},
		{&fleet.ExternalClaim{Service: fleet.NewServiceKey("stripe"), Version: "0.1.0", Owner: "team-platform"}, "  owner    team-platform (claimed by stripe 0.1.0)\n"},
		{&fleet.ExternalClaim{Service: fleet.NewServiceKey("stripe"), Version: "0.1.0"}, "  owner    no verified owner (claimed by stripe 0.1.0)\n"},
	} {
		var b bytes.Buffer
		printExternal(&b, fleet.ExternalRecord{Key: "stripe", Class: "saas", ConsumersTotal: 1, Claim: tc.claim}, "", fleet.ExternalEvidence{})
		if lines := strings.SplitAfter(b.String(), "\n"); len(lines) < 2 || lines[1] != tc.want {
			t.Errorf("claim %+v:\n%s", tc.claim, b.String())
		}
	}
}

// Other egress folds many hosts: it is no system to claim, so it has no
// owner line.
func TestFleetGetOtherEgressNoOwnerLine(t *testing.T) {
	var b bytes.Buffer
	printExternal(&b, fleet.ExternalRecord{Key: fleet.OtherEgress, Class: fleet.ClassOtherEgress}, "", fleet.ExternalEvidence{})
	if strings.Contains(b.String(), "owner") {
		t.Fatalf("%s", b.String())
	}
}

func TestFleetAttentionUnownedExternalRow(t *testing.T) {
	cmd, buf := testCmd()
	res := &fleet.AttentionList{Count: 1, Total: 1, Items: []fleet.AttentionItem{{
		Entity: fleet.EntityRef{Kind: fleet.KindExternal, Key: "twilio"}, Severity: "info", Code: "UNOWNED_EXTERNAL", Category: "unowned",
		Label: "twilio: 4 services use it", Summary: "Twilio has no owner", NextStep: "Claim twilio for a team",
		NextAction: &fleet.NextAction{Verb: "claim", Tool: "pacto_declare", CLI: "pacto claim twilio --owner <team>"},
	}}}
	if err := printFleetAttention(cmd, res, "text"); err != nil {
		t.Fatal(err)
	}
	want := "  [info] unowned twilio: 4 services use it: Twilio has no owner (next: Claim twilio for a team) (action: claim: pacto claim twilio --owner <team>)\n"
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("%s", buf.String())
	}
}

func TestExplainDeclarationExternalFrom(t *testing.T) {
	for _, tc := range []struct{ name, state, want string }{
		{"stripe", contract.DeclarationDeclared, " from external stripe; signer"},
		{"stripe", contract.DeclarationDraft, " from external stripe, awaiting a person: accept it with pacto claim stripe --from-draft <tag>;"},
		{"payments-stripe", contract.DeclarationDraft, "accept it with pacto claim stripe --name payments-stripe --from-draft <tag>;"},
	} {
		cmd, buf := testCmd()
		result := &app.ExplainResult{Name: tc.name, Declaration: &contract.InferredDeclaration{State: tc.state, InferredFrom: "external:stripe",
			Asker: contract.InferredSigner{Subject: "alice@acme.com", Issuer: "https://accounts.google.com", Class: contract.SignerClassHuman}}}
		if err := printExplainResult(cmd, result, "text"); err != nil || !strings.Contains(buf.String(), tc.want) {
			t.Errorf("%s %s: %v\n%s", tc.name, tc.state, err, buf.String())
		}
	}
}
