package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/viper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// TestDeclareRefusedPrintsSentence: an owner refusal prints refused: and the
// approved sentence, and exits as every declare refusal does.
func TestDeclareRefusedPrintsSentence(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	msg := "service.owner is attested as team payments (CODEOWNERS:7 at a1b2c3, signed by github.com/acme/invoices/.github/workflows/ci.yml); " +
		"only payments members or default approvers (platform-admins) may set it"
	refused := &app.DeclareError{Code: delivery.OwnerSignerNotAllowed, Message: msg, CLI: "pacto declare invoices --registry r"}
	fail := func(app.DeclareRequest) (*app.DeclareResult, error) { return &app.DeclareResult{}, refused }
	_, _, err := runRoot(t, declareDeps(t, &got, nil, refused, fail), nil, "declare", "invoices", "--yes")
	if err == nil || err.Error() != "refused: "+msg+"\nrun: pacto declare invoices --registry r" || jsonExitOf(err).Code != 1 {
		t.Fatalf("%v", err)
	}
}

// TestDeclareRefusesLocalOwnerPolicy: pacto declare takes no owner policy
// file; its owner policy comes only from the signed target snapshots.
func TestDeclareRefusesLocalOwnerPolicy(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	if _, _, err := runRoot(t, declareDeps(t, &got, nil, nil, nil), nil, "declare", "invoices", "--owner-policy", "p.json"); err == nil ||
		!strings.Contains(err.Error(), "unknown flag: --owner-policy") {
		t.Fatal(err)
	}
}

// TestDeclareDeliveryTrustReachesThePolicy: --delivery-trust on pacto
// declare and on a stdio pacto mcp is the trust the owner policy's target
// snapshots are verified with, as on every other command.
func TestDeclareDeliveryTrustReachesThePolicy(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	if _, _, err := runRoot(t, declareDeps(t, &got, &app.DeclarePlan{}, nil, nil), nil, "declare", "invoices", "--delivery-trust", "trust.yaml"); err != nil ||
		got.DeliveryTrust != "trust.yaml" {
		t.Fatalf("%v %q", err, got.DeliveryTrust)
	}
	cmd := newMCPCommand(app.NewService(nil, nil), "v", defaultDeps())
	for k, v := range map[string]string{"transport": "stdio", "delivery-trust": "trust.yaml"} {
		if err := cmd.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	decl, ok := mcpAuthoring(cmd, app.NewService(nil, nil)).Declare.(mcpDeclarer)
	if req, err := decl.request(pactomcp.DeclareArgs{Service: "invoices"}); !ok || err != nil || req.DeliveryTrust != "trust.yaml" {
		t.Fatalf("%v %v %q", ok, err, req.DeliveryTrust)
	}
}

// TestDashboardOwnerPolicyEnv reads PACTO_DASHBOARD_OWNER_POLICY, the mount
// the operator configures.
func TestDashboardOwnerPolicyEnv(t *testing.T) {
	t.Setenv("PACTO_DASHBOARD_OWNER_POLICY", "/etc/pacto/owner-policy/policy.json")
	v := viper.New()
	v.SetEnvPrefix("PACTO")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()
	newDashboardCommand(app.NewService(nil, nil), v, "test", defaultDeps())
	if got := v.GetString("dashboard.owner-policy"); got != "/etc/pacto/owner-policy/policy.json" {
		t.Errorf("dashboard.owner-policy = %q", got)
	}
}

// TestMCPOwnerPolicyFlagRefusedOnStdio: a stdio server never reads an owner
// policy file; an HTTP server takes its mount.
func TestMCPOwnerPolicyFlagRefusedOnStdio(t *testing.T) {
	svc := app.NewService(nil, nil)
	for _, c := range []struct {
		args []string
		want error
	}{
		{[]string{"--owner-policy", "p.json"}, errOwnerPolicyOverStdio},
		{[]string{"--owner-policy", "p.json", "-t", "http", "--host", "0.0.0.0"}, nil},
	} {
		cmd := newMCPCommand(svc, "v", defaultDeps())
		cmd.SetArgs(c.args)
		cmd.SetOut(&strings.Builder{})
		cmd.SetErr(&strings.Builder{})
		err := cmd.Execute()
		if c.want != nil && !errors.Is(err, c.want) || c.want == nil && (err == nil || errors.Is(err, errOwnerPolicyOverStdio)) {
			t.Fatalf("%v: %v", c.args, err)
		}
	}
	if svc.OwnerPolicyPath != "p.json" {
		t.Fatalf("OwnerPolicyPath = %q", svc.OwnerPolicyPath)
	}
}

// TestDeclareRefusalHidesRestrictedServiceOwner: over HTTP, a caller the
// fleet does not show the service to learns nothing about its owner: no
// one who may set it, no owner note and a generic refusal.
func TestDeclareRefusalHidesRestrictedServiceOwner(t *testing.T) {
	w := newDeclareWorld(t)
	caller := declareCluster()
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	dep, err := caller.Resource(gvr).Namespace("billing").Get(context.Background(), "invoices", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dep.SetLabels(map[string]string{"team": "payments"})
	if _, err := caller.Resource(gvr).Namespace("billing").Update(context.Background(), dep, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(t.TempDir(), "policy.json")
	_ = os.WriteFile(policy, []byte(`{"defaultApprovers":[{"name":"platform-admins","issuer":"https://accounts.google.com","subject":"^admin@acme\\.com$"}]}`), 0o600)
	svc := app.NewService(nil, nil)
	svc.OwnerPolicyPath = policy
	a := pactomcp.DeclareArgs{Service: "invoices", Namespace: "billing", Registry: w.host + "/acme"}
	plan := func(mode *pactomcp.FleetMode) (app.DeclarePlan, string) {
		raw, refused, err := newRemoteDeclarer(svc, []string{"oci://" + w.host + "/acme"}, mode).Plan(remoteCtx(caller), a)
		if err != nil || refused != nil {
			t.Fatalf("%s %+v %v", raw, refused, err)
		}
		var p app.DeclarePlan
		_ = json.Unmarshal(raw, &p)
		return p, string(raw)
	}
	if p, _ := plan(staticFleetMode(t, nil, nil)); len(p.MayAccept) != 1 {
		t.Fatalf("a caller who sees invoices: %+v", p)
	}
	hide := func(context.Context, *auth.TokenInfo, *fleet.Query) (*fleet.Query, error) {
		return fleet.NewQuery(&fleet.FleetSnapshot{}), nil
	}
	if p, raw := plan(staticFleetMode(t, hide, nil)); len(p.MayAccept) != 0 || strings.Contains(raw, "platform-admins") || strings.Contains(raw, "service.owner") {
		t.Fatalf("%s", raw)
	}
	// An owner refusal names nothing about the hidden owner.
	attested := &app.DeclareError{Code: delivery.OwnerSignerNotPerson, Message: "service.owner is attested as team payments (CODEOWNERS:7 at a1b2c3)", CLI: "pacto declare invoices"}
	var de *app.DeclareError
	if err := hiddenOwnerRefusal(attested); !errors.As(err, &de) || de.Code != delivery.OwnerSignerNotAllowed ||
		de.Message != "you may not set the owner of this service" || de.CLI != "pacto declare invoices" {
		t.Fatalf("%+v", err)
	}
	if other := errors.New("x"); hiddenOwnerRefusal(other) != other {
		t.Fatal("a refusal outside the owner rule changed")
	}
}

// TestDeclareToolRunsAsAgentAndPerson: an owner change declared through
// pacto_declare writes a draft that names who may accept it, and the same
// change through pacto declare under a person's login with no right to set
// the owner is refused with an owner code before anything is signed.
func TestDeclareToolRunsAsAgentAndPerson(t *testing.T) {
	e := newDeclareE2E(t)
	dir, reg := e.ownerChange(t)
	decl := newMCPDeclarer(app.NewService(nil, nil), "", "")
	decl.base.Signing.FulcioURL, decl.base.Signing.TimestampURL, decl.base.Signing.RekorURL, decl.base.Signing.TrustedRoot = e.ss.Fulcio, e.ss.TSA, "", e.root
	server := func() *mcpsdk.Server {
		s, _ := pactomcp.New("v", pactomcp.Authoring{Declare: decl}, pactomcp.Modes{})
		return s
	}
	agent := declareTwice(t, server, e.args(map[string]any{"dir": dir}))
	var plan app.DeclarePlan
	b, _ := json.Marshal(agent.Plan)
	_ = json.Unmarshal(b, &plan)
	if agent.Draft == nil || plan.Owner != "team:payments" || !slices.Equal(plan.MayAccept, []string{"billing members"}) {
		t.Fatalf("%+v\n%s", agent, b)
	}
	o, err := e.run(t, "declare", "invoices", "--namespace", "billing", "--dir", dir, "--registry", e.host+"/acme", "--accept", "ledger",
		"--fulcio-url", e.ss.Fulcio, "--timestamp-url", e.ss.TSA, "--rekor-url", "", "--trusted-root", e.root, "--yes", "--output-format", "json")
	var person struct {
		Refusal app.DeclareError `json:"refusal"`
	}
	if err == nil || json.Unmarshal([]byte(o), &person) != nil || !strings.HasPrefix(person.Refusal.Code, "OWNER_") || e.logins != 1 {
		t.Fatalf("%v\n%s", err, o)
	}
	if _, err := reg.Pull(t.Context(), e.host+"/acme/invoices:"+plan.Version); err == nil {
		t.Fatalf("%s was published", plan.Version)
	}
}

// ownerChange publishes invoices 1.4.0 declaring team billing, signed by
// acme's release workflow (the publisher the installation trusts), and
// returns a folder pacto init --cluster wrote that changes the owner to
// team payments, with a client of the registry.
func (e *declareE2E) ownerChange(t *testing.T) (string, *publish.Client) {
	t.Helper()
	out := t.TempDir()
	if o, err := e.run(t, "init", "--cluster", "--namespace", "billing", "--out", out); err != nil {
		t.Fatalf("%v\n%s", err, o)
	}
	dir := filepath.Join(out, "billing", "invoices")
	raw, err := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	owned := func(team string) []byte {
		return []byte(strings.Replace(string(raw), "  # owner:\n  #   team: <the team that owns invoices>\n", "  version: 1.4.0\n  owner:\n    team: "+team+"\n", 1))
	}
	cm := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": delivery.PolicyConfigMap, "namespace": delivery.PolicyNamespace},
		"data": map[string]any{delivery.PolicyKey: `{"signers":[{"issuer":"` + e2eIssuer + `","subjects":".*@acme\\.com"}]}`,
			delivery.PublishersKey: `{"registry":"ghcr.io/acme"}`}}}
	if _, err := e.dyn.Resource(schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}).Namespace(delivery.PolicyNamespace).
		Create(t.Context(), cm, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(t.TempDir(), "invoices")
	if err := os.MkdirAll(base, 0o755); err != nil || os.WriteFile(filepath.Join(base, "pacto.yaml"), owned("billing"), 0o600) != nil {
		t.Fatal(err)
	}
	if o, err := e.run(t, "push", "oci://"+e.host+"/acme/invoices:1.4.0", "--path", base, "--index", "none"); err != nil {
		t.Fatalf("%v\n%s", err, o)
	}
	reg := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), e.host)
	ci := func(context.Context) (string, error) {
		return testutil.CIToken("https://github.com/acme/invoices/.github/workflows/release.yml@refs/heads/main",
			"https://token.actions.githubusercontent.com", "https://github.com/acme/invoices", "refs/heads/main"), nil
	}
	h, err := reg.Head(t.Context(), e.host+"/acme/invoices:1.4.0")
	s, serr := signing.Keyless(e.ss.Fulcio, e.ss.TSA, "", ci, e.ss.Root)
	if err != nil || serr != nil || publish.SignDigest(t.Context(), reg, s, e.host+"/acme/invoices", h.Digest) != nil ||
		os.WriteFile(filepath.Join(dir, "pacto.yaml"), owned("payments"), 0o600) != nil {
		t.Fatal(err, serr)
	}
	return dir, reg
}
