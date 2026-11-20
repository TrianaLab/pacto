package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/spf13/viper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
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
