package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// ownerCommit is the commit acme's CI read the owner file at.
const ownerCommit = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"

// withOwnerPolicy mounts the owner policy: admin@acme.com is the
// platform-admins default approver and ana@acme.com is a billing member.
func (e *declareEnv) withOwnerPolicy(t *testing.T) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "policy.json")
	body := `{"defaultApprovers":[{"name":"platform-admins","issuer":"` + personIssuer + `","subject":"^admin@acme\\.com$"}],` +
		`"teams":{"billing":[{"name":"ana","issuer":"` + personIssuer + `","subject":"^ana@acme\\.com$"}]}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	e.svc.OwnerPolicyPath = p
}

// ownedDir is a folder declaring invoices 1.4.0 owned by team.
func (e *declareEnv) ownedDir(t *testing.T, team string) string {
	t.Helper()
	raw := strings.Replace(e.invoicesBaseline("1.4.0", ledgerDependency)["pacto.yaml"], "team: billing", "team: "+team, 1)
	return e.dirFolder(t, raw, nil)
}

// attestedBaseline publishes invoices 1.4.0 with no declared owner, its
// record reading team payments from CODEOWNERS at ownerCommit, signed by
// acme's release workflow for that commit.
func (e *declareEnv) attestedBaseline(t *testing.T) {
	t.Helper()
	files := e.invoicesBaseline("1.4.0", "")
	files["pacto.yaml"] = strings.Replace(files["pacto.yaml"], "  owner:\n    team: billing\n", "", 1)
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "4", Commit: ownerCommit,
			Sources: []contract.InferredSourceFile{{Path: ".github/CODEOWNERS", Digest: "sha256:" + strings.Repeat("0", 64)}}},
		Fields: []contract.Inferred{{Field: "service.owner", Value: "payments", Source: "repository:.github/CODEOWNERS:7", Confidence: contract.ConfidenceLikely,
			Class: contract.InferredClassOwnerFile, Declare: "service:\n  owner:\n    team: payments\n"}}}
	files[contract.InferredRecordFile] = string(rec.Marshal())
	ci := e.signer(t, testutil.CITokenAt("https://github.com/acme/invoices/.github/workflows/release.yml@refs/heads/main",
		"https://token.actions.githubusercontent.com", "https://github.com/acme/invoices", "refs/heads/main", ownerCommit))
	e.publishBundle(t, e.host+"/acme/invoices", "1.4.0", files, &ci)
	e.pushImage(t, e.host+"/acme/deploy/invoices", "1.4.0")
}

// dirReq is a person's request declaring dir.
func (e *declareEnv) dirReq(dir string) DeclareRequest {
	req := e.req()
	req.Dir = dir
	return req
}

func TestDeclareRefusesBeforePush(t *testing.T) {
	e := newDeclareEnv(t)
	e.withOwnerPolicy(t)
	e.login(personToken("eve@acme.com"))
	_, err := e.svc.Declare(t.Context(), e.dirReq(e.ownedDir(t, "billing")))
	if de := refusal(t, err); de.Code != delivery.OwnerSignerNotAllowed || !strings.Contains(de.CLI, "pacto declare invoices") {
		t.Fatalf("%+v", de)
	}
	if tags := e.tags(t); len(tags) != 0 {
		t.Fatalf("pushed %v", tags)
	}
}

func TestDeclareFirstOwnerNeedsDefaultApprover(t *testing.T) {
	e := newDeclareEnv(t)
	e.withOwnerPolicy(t)
	p, _ := e.plan(t, e.dirReq(e.ownedDir(t, "billing")))
	if !slices.Equal(p.MayAccept, []string{"platform-admins"}) || p.Owner != "team:billing" {
		t.Fatalf("%+v", p)
	}
	e.login(personToken("admin@acme.com"))
	res := e.declare(t, e.dirReq(e.ownedDir(t, "billing")))
	if b, _ := e.pulled(t, res.Tag); b.Contract.Service.Owner.Team != "billing" {
		t.Fatalf("%+v", b.Contract.Service.Owner)
	}
}

func TestDeclareAttestedOwnerRefusalMatchesUX(t *testing.T) {
	e := newDeclareEnv(t)
	e.attestedBaseline(t)
	e.withOwnerPolicy(t)
	e.login(personToken("eve@acme.com"))
	_, err := e.svc.Declare(t.Context(), e.dirReq(e.ownedDir(t, "billing")))
	want := "service.owner is attested as team payments (CODEOWNERS:7 at a1b2c3d, signed by github.com/acme/invoices/.github/workflows/release.yml@refs/heads/main); " +
		"only payments members or default approvers (platform-admins) may set it"
	if de := refusal(t, err); de.Code != delivery.OwnerSignerNotAllowed || de.Message != want {
		t.Fatalf("got  %q\nwant %q", de.Message, want)
	}
}

func TestDeclareDeclaredOwnerMemberAllowed(t *testing.T) {
	e := newDeclareEnv(t)
	e.withBaseline(t) // declared team billing
	e.withOwnerPolicy(t)
	e.login(personToken("ana@acme.com"))
	res := e.declare(t, e.dirReq(e.ownedDir(t, "payments")))
	if b, _ := e.pulled(t, res.Tag); b.Contract.Service.Owner.Team != "payments" {
		t.Fatalf("%+v", b.Contract.Service.Owner)
	}
}

func TestDeclareDefaultApproverAllowed(t *testing.T) {
	e := newDeclareEnv(t)
	e.withBaseline(t)
	e.withOwnerPolicy(t)
	e.login(personToken("admin@acme.com"))
	if res := e.declare(t, e.dirReq(e.ownedDir(t, "payments"))); !res.Executed || res.Draft {
		t.Fatalf("%+v", res)
	}
}

func TestDeclareNoPolicyIsUnverified(t *testing.T) {
	e := newDeclareEnv(t)
	e.withBaseline(t)
	e.login(personToken("admin@acme.com"))
	_, err := e.svc.Declare(t.Context(), e.dirReq(e.ownedDir(t, "payments")))
	if de := refusal(t, err); de.Code != delivery.OwnerPolicyUnverified || !strings.Contains(de.Message, "trust missing") {
		t.Fatalf("%+v", de)
	}
}

// TestDeclareReadsPolicyWithItsTrust: the request's trust config verifies
// the owner policy's snapshots, not only $PACTO_DELIVERY_TRUST.
func TestDeclareReadsPolicyWithItsTrust(t *testing.T) {
	e := newDeclareEnv(t)
	e.withBaseline(t)
	e.login(personToken("admin@acme.com"))
	req := e.dirReq(e.ownedDir(t, "payments"))
	req.DeliveryTrust = filepath.Join(t.TempDir(), "missing.yaml")
	_, err := e.svc.Declare(t.Context(), req)
	if de := refusal(t, err); de.Code != delivery.OwnerPolicyUnverified || !strings.Contains(de.Message, "trust unreadable") {
		t.Fatalf("%+v", de)
	}
}

func TestDeclareOwnerUnchangedNeedsNoRight(t *testing.T) {
	e := newDeclareEnv(t)
	e.withBaseline(t)
	e.login(personToken("eve@acme.com")) // no policy, no membership: the owner does not change
	if res := e.declare(t, e.dirReq(e.ownedDir(t, "billing"))); !res.Executed || res.Plan.Owner != "team:billing" || len(res.Plan.MayAccept) != 0 {
		t.Fatalf("%+v", res)
	}
}

func TestDeclareClusterLabelStaysInferred(t *testing.T) {
	e := newDeclareEnv(t)
	e.withOwnerPolicy(t)
	p, d := e.plan(t, e.req())
	if p.Owner != "" || strings.Contains(string(d.raw), "owner") || d.owner != nil || !slices.Equal(p.MayAccept, []string{"platform-admins"}) {
		t.Fatalf("%+v\n%s", p, d.raw)
	}
}

func TestDeclareOwnerRemovalIsSetting(t *testing.T) {
	e := newDeclareEnv(t)
	e.withBaseline(t)
	e.withOwnerPolicy(t)
	e.login(personToken("eve@acme.com"))
	raw := strings.Replace(e.invoicesBaseline("1.4.0", ledgerDependency)["pacto.yaml"], "  owner:\n    team: billing\n", "", 1)
	_, err := e.svc.Declare(t.Context(), e.dirReq(e.dirFolder(t, raw, nil)))
	if de := refusal(t, err); de.Code != delivery.OwnerSignerNotAllowed {
		t.Fatalf("%+v", de)
	}
}

func TestOnBehalfOfNeverGrantsOwner(t *testing.T) {
	e := newDeclareEnv(t)
	e.withOwnerPolicy(t)
	req := e.dirReq(e.ownedDir(t, "billing"))
	req.Agent, req.OnBehalfOf = true, "admin@acme.com"
	res := e.declare(t, req)
	if !res.Draft || !slices.Equal(res.Plan.MayAccept, []string{"platform-admins"}) {
		t.Fatalf("%+v", res)
	}
	e.login(personToken("eve@acme.com"))
	if _, err := e.svc.Declare(t.Context(), e.fromDraft(res.Tag)); refusal(t, err).Code != delivery.OwnerSignerNotAllowed {
		t.Fatal(err)
	}
	if tags := e.tags(t); !slices.Equal(tags, []string{"draft-1"}) {
		t.Fatalf("%v", tags)
	}
}

func TestDeclareCISignerCannotSetOwner(t *testing.T) {
	e := newDeclareEnv(t)
	e.withOwnerPolicy(t)
	e.login(ciWorkflowToken())
	if res := e.declare(t, e.dirReq(e.ownedDir(t, "billing"))); !res.Draft || slices.Contains(e.tags(t), firstDeclaration) {
		t.Fatalf("%+v", res)
	}
	d := &declaration{owner: &ownerCheck{proposed: delivery.OwnerClaim{Owner: contract.Owner{Team: "billing"}, Provenance: "declared"}}}
	ci := signerChoice{Identity: delivery.PublisherIdentity{Subject: "admin@acme.com", Issuer: personIssuer, Class: delivery.ClassCI}}
	if err := e.svc.ownerRight(e.req(), &DeclarePlan{Service: "invoices"}, d, ci); refusal(t, err).Code != delivery.OwnerSignerNotPerson {
		t.Fatal(err)
	}
}

func TestDeclareOwnerHistoryUnreadable(t *testing.T) {
	e := newDeclareEnv(t)
	e.withBaseline(t)
	for i := range maxOwnerHistory {
		e.pushImage(t, e.host+"/acme/invoices", fmt.Sprintf("0.0.%d", i))
	}
	if _, _, err := e.svc.PlanDeclare(t.Context(), e.req()); refusal(t, err).Code != codeRegistryUnreadable {
		t.Fatal(err)
	}
}

func TestDeclareOwnerHistoryPullFails(t *testing.T) {
	e := newDeclareEnv(t)
	e.withBaseline(t)
	e.faulty(&faultyRegistry{failPull: 2}) // the baseline reads once, then its owner history
	if _, _, err := e.svc.PlanDeclare(t.Context(), e.req()); refusal(t, err).Code != codeRegistryUnreadable || !strings.Contains(err.Error(), "for its owner") {
		t.Fatal(err)
	}
}
