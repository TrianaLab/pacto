package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// createDemo is the demo's dashboard with its in-memory creator, signed in
// as alice.
func createDemo(t *testing.T) http.Handler {
	t.Helper()
	q := fleet.NewQuery(demoFleet(t))
	state := func(*http.Request) dashboard.AuthState {
		return dashboard.AuthState{SignedIn: true, CSRF: "browser-demo", Method: "oidc", User: "alice"}
	}
	return demoHandler(dashboard.Options{
		Fleet:         func(context.Context) (*fleet.Query, error) { return q, nil },
		Auth:          &dashboard.Auth{Login: true, State: state},
		CreateService: newDemoCreator(func(r *http.Request) string { return state(r).User }),
	})
}

var httpServicePath = string(fleet.NewServiceKey("http-service"))

func createInput(values map[string]any) dashboard.CreateServiceInput {
	in := dashboard.CreateServiceInput{Path: httpServicePath, Name: "invoices", Owner: "billing", Namespace: "team-billing"}
	if values != nil {
		in.Offerings = []dashboard.CreateServiceOffering{{Offering: "postgresql", Values: values}}
	}
	return in
}

// TestDemoCreatorPlansHTTPService: the demo plans invoices from http-service
// 1.0.0 into demo.pacto.local/invoices, nobody signs and the requested
// postgresql 17.0.0 leads the offerings.
func TestDemoCreatorPlansHTTPService(t *testing.T) {
	var ans dashboard.CreateServiceAnswer
	post(t, createDemo(t), "/api/fleet/services", createInput(map[string]any{"storageGi": 50}), &ans)
	checkDemoCreatePlan(t, &ans)
}

// checkDemoCreatePlan: ans is the demo's plan of invoices with storageGi 50.
func checkDemoCreatePlan(t *testing.T, ans *dashboard.CreateServiceAnswer) {
	t.Helper()
	p := ans.Plan
	if p == nil || ans.Refusal != nil || len(ans.Refusals) > 0 || len(ans.Offerings) == 0 {
		t.Fatalf("answer = %+v", ans)
	}
	o := ans.Offerings[0]
	for what, ok := range map[string]bool{
		"path":         p.PathName == "http-service" && p.PathVersion == "1.0.0" && strings.Contains(p.Path, "@sha256:"),
		"repository":   p.BundleRepo == "demo.pacto.local/invoices" && p.Version == "0.1.0",
		"signature":    p.Signature.Status == fleet.StepStatusUnknown,
		"signer":       p.Signer.Why == demoNoSignIn && ans.Signing.Mode == "none",
		"revision":     strings.HasPrefix(p.Revision, "pacto-create:alice:"),
		"init":         strings.Contains(p.InitCLI, "pacto init invoices --from "),
		"contract":     strings.Contains(p.Contract.Text, `"storageGi":50`),
		"offering":     o.Offering == "postgresql" && o.Version == "17.0.0" && o.Requested,
		"completeness": p.ConsumableCompleteness == string(fleet.CompletenessPartial),
	} {
		if !ok {
			t.Errorf("%s: plan %+v offerings %+v", what, p, ans.Offerings)
		}
	}
}

// TestDemoCreatorSavesDraft: Save draft keeps draft-1, a second save answers
// it again, the next plan lists it, and reviewing it plans it with its digest
// while nobody signs it here.
func TestDemoCreatorSavesDraft(t *testing.T) {
	h := createDemo(t)
	var ans dashboard.CreateServiceAnswer
	post(t, h, "/api/fleet/services", createInput(map[string]any{"storageGi": 50}), &ans)
	intent := ans.Plan.IntentDigest
	accept := dashboard.CreateServiceAcceptInput{CreateServiceInput: createInput(map[string]any{"storageGi": 50}), Mode: "draft"}
	accept.IntentDigest = intent
	for range 2 {
		var saved dashboard.CreateServiceAcceptAnswer
		post(t, h, "/api/fleet/services/accept", accept, &saved)
		if saved.Tag != "draft-1" || !saved.Draft || !strings.HasPrefix(saved.Published, "demo.pacto.local/invoices@sha256:") {
			t.Fatalf("saved = %+v", saved)
		}
	}
	post(t, h, "/api/fleet/services", createInput(map[string]any{"storageGi": 50}), &ans)
	if len(ans.Drafts) != 1 || ans.Drafts[0].Tag != "draft-1" || ans.DraftsTotal != 1 {
		t.Fatalf("drafts = %+v", ans.Drafts)
	}
	from := createInput(map[string]any{"storageGi": 50})
	from.FromDraft = &dashboard.DraftRef{Tag: "draft-1", Digest: ans.Drafts[0].Digest}
	post(t, h, "/api/fleet/services", from, &ans)
	if ans.Plan == nil || ans.Plan.IntentDigest == intent {
		t.Fatalf("draft plan = %+v", ans.Plan)
	}
	var signed dashboard.CreateServiceAcceptAnswer
	post(t, h, "/api/fleet/services/accept", dashboard.CreateServiceAcceptInput{CreateServiceInput: from, Mode: "sign"}, &signed)
	if code(signed.Refusal) != "NotAllowed" || !strings.Contains(signed.Refusal.Command, "pacto init invoices") {
		t.Fatalf("sign = %+v", signed.Refusal)
	}
}

// TestDemoCreatorRefusesOutOfRange: storageGi 2000 is outside postgresql
// 17.0.0's closed schema, refused at its field with the closest value 1000
// and both fixes, and nothing can be saved from it.
func TestDemoCreatorRefusesOutOfRange(t *testing.T) {
	h := createDemo(t)
	var ans dashboard.CreateServiceAnswer
	post(t, h, "/api/fleet/services", createInput(map[string]any{"storageGi": 2000}), &ans)
	if ans.Plan != nil || len(ans.Refusals) != 1 {
		t.Fatalf("answer = %+v", ans)
	}
	r := ans.Refusals[0]
	if r.Code != "ParameterOutOfRange" || r.Field != "configurations[database].values.storageGi" || r.Closest != float64(1000) {
		t.Fatalf("refusal = %+v", r)
	}
	if !strings.Contains(r.CLI, "--set 'configurations[database].values.storageGi=1000'") || r.MCP != `{"offerings":[{"offering":"postgresql","values":{"storageGi":1000}}]}` {
		t.Fatalf("fixes = %q %q", r.CLI, r.MCP)
	}
	var out dashboard.CreateServiceAcceptAnswer
	post(t, h, "/api/fleet/services/accept", dashboard.CreateServiceAcceptInput{CreateServiceInput: createInput(map[string]any{"storageGi": 2000}), Mode: "draft"}, &out)
	if out.Tag != "" || len(out.Refusals) != 1 {
		t.Fatalf("accept = %+v", out)
	}
}

// TestDemoCreatorRefusals: every other refusal the demo gives, and a plan
// that moved since it was reviewed.
func TestDemoCreatorRefusals(t *testing.T) {
	h := createDemo(t)
	var ans dashboard.CreateServiceAnswer
	in := createInput(nil)
	in.Repository = "ghcr.io/acme/invoices"
	post(t, h, "/api/fleet/services", in, &ans)
	if code(ans.Refusal) != "RepositoryOutsideRegistry" {
		t.Fatalf("repository refusal = %+v", ans.Refusal)
	}
	in = createInput(nil)
	in.FromDraft = &dashboard.DraftRef{Tag: "draft-9", Digest: "sha256:" + strings.Repeat("0", 64)}
	post(t, h, "/api/fleet/services", in, &ans)
	if code(ans.Refusal) != "DraftNotFound" {
		t.Fatalf("draft refusal = %+v", ans.Refusal)
	}
	in = createInput(nil)
	in.Offerings = []dashboard.CreateServiceOffering{{Offering: "redis", Values: map[string]any{"a": 1}},
		{Offering: "postgresql", Environments: map[string]map[string]any{"prod": {"storageGi": 20}}}}
	post(t, h, "/api/fleet/services", in, &ans)
	if len(ans.Refusals) != 2 || ans.Refusals[0].Code != "OfferingUnknown" || ans.Refusals[1].Code != "EnvironmentNotDeclared" {
		t.Fatalf("refusals = %+v", ans.Refusals)
	}
	var out dashboard.CreateServiceAcceptAnswer
	stale := dashboard.CreateServiceAcceptInput{CreateServiceInput: createInput(nil), Mode: "draft"}
	stale.IntentDigest = "sha256:old"
	post(t, h, "/api/fleet/services/accept", stale, &out)
	if code(out.Refusal) != "PlanStale" || out.Plan == nil || out.Plan.Plan == nil {
		t.Fatalf("stale = %+v", out)
	}
}

// TestDemoCreatorOfferingNotConsumable: on a complete consumable read an
// unlisted offering is a definite refusal.
func TestDemoCreatorOfferingNotConsumable(t *testing.T) {
	got := demoCreateRefusals(dashboard.CreateServicePath{}, createInput(nil), nil, false, "")
	if got != nil {
		t.Fatalf("no picks = %+v", got)
	}
	in := createInput(nil)
	in.Offerings = []dashboard.CreateServiceOffering{{Offering: "redis"}}
	if got = demoCreateRefusals(dashboard.CreateServicePath{}, in, nil, false, ""); len(got) != 1 || got[0].Code != "OfferingNotConsumable" {
		t.Fatalf("refusals = %+v", got)
	}
	if demoProviderConfiguration(fleet.ConsumableProvider{}) != "" {
		t.Fatal("a provider with no parameters names a configuration")
	}
}
