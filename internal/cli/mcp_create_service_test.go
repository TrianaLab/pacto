package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// emptyDockerConfig is a push credential file naming no registry: every
// registry answers anonymously, as the test registry needs.
func emptyDockerConfig(t *testing.T) string {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfg, []byte(`{"auths":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// creator is h's pacto_create_service as the dashboard's /mcp builds it:
// h's registry and fleet view, with the dashboard's push credential when
// push is set.
func (h *createHost) creator(t *testing.T, push bool) remoteCreator {
	t.Helper()
	v := viper.New()
	v.Set("dashboard.registry", h.d.registry)
	if push {
		v.Set("dashboard.declare-registry-config", emptyDockerConfig(t))
	}
	fm := &pactomcp.FleetMode{Src: func(context.Context) (*fleet.Query, error) { return h.path.View, nil },
		Opts: pactomcp.FleetOptions{DashboardURL: "https://pacto.example/"}}
	c, err := dashboardMCPCreator(v, h.d.svc, fm, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c.(remoteCreator)
}

func (h *createHost) createArgs() pactomcp.CreateServiceArgs {
	return pactomcp.CreateServiceArgs{Path: string(h.path.Key), Name: "invoices", Owner: "billing", Namespace: "team-billing"}
}

// planCreate plans a and fails t on a refusal.
func planCreate(t *testing.T, c remoteCreator, ctx context.Context, a pactomcp.CreateServiceArgs) app.DeclarePlan {
	t.Helper()
	raw, refused, err := c.Plan(ctx, a)
	if err != nil || refused != nil {
		t.Fatalf("%s %+v %v", raw, refused, err)
	}
	var p app.DeclarePlan
	_ = json.Unmarshal(raw, &p)
	return p
}

// createRun is the part of an Execute answer the tests read.
type createRun struct {
	Executed  bool   `json:"executed"`
	Tag       string `json:"tag"`
	Published string `json:"published"`
	Draft     bool   `json:"draft"`
	Signed    bool   `json:"signed"`
	AcceptURL string `json:"acceptURL"`
}

// executeCreate plans a, then writes it with the plan's intent and revision.
func executeCreate(t *testing.T, c remoteCreator, ctx context.Context, a pactomcp.CreateServiceArgs) (createRun, *pactomcp.Refused) {
	t.Helper()
	p := planCreate(t, c, ctx, a)
	a.IntentDigest, a.Revision = p.IntentDigest, p.Create.Revision
	raw, refused, err := c.Execute(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	var run createRun
	_ = json.Unmarshal(raw, &run)
	return run, refused
}

func TestCreateServiceAgentWritesDraftOnly(t *testing.T) {
	h := newCreateHost(t, false, true)
	log := &bytes.Buffer{}
	ctx := callerCtx("alice@acme.com", newSelfReviewed(true), log)
	run, refused := executeCreate(t, h.creator(t, true), ctx, h.createArgs())
	if refused != nil || !run.Executed || run.Tag != "draft-1" || !run.Draft || run.Signed ||
		!strings.HasPrefix(run.AcceptURL, "https://pacto.example/#/fleet/services/") || !strings.Contains(run.AcceptURL, "draft=draft-1") {
		t.Fatalf("%+v %+v", run, refused)
	}
	expect(t, slices.Equal(h.repoTags(t, "invoices"), []string{"draft-1"}), "tags %v: no version is ever written", h.repoTags(t, "invoices"))
	d := h.w.pulled(t, "draft-1").Declaration
	expect(t, d.State == contract.DeclarationDraft && d.OnBehalfOf == "alice@acme.com" && d.Asker.Issuer == contract.SignerUnsigned, "%+v", d)
	expect(t, strings.Contains(log.String(), "via=mcp") && strings.Contains(log.String(), "verb=create") && strings.Contains(log.String(), "namespace=team-billing"),
		"audit %s", log.String())
}

func TestCreateServiceDraftDeduped(t *testing.T) {
	h := newCreateHost(t, false, true)
	c, ctx := h.creator(t, true), callerCtx("alice@acme.com", newSelfReviewed(true), &bytes.Buffer{})
	first, _ := executeCreate(t, c, ctx, h.createArgs())
	second, refused := executeCreate(t, c, ctx, h.createArgs())
	expect(t, refused == nil && second.Tag == "draft-1" && !second.Executed && second.Published == "" && second.AcceptURL == first.AcceptURL, "deduped %+v %+v", second, refused)
	expect(t, slices.Equal(h.repoTags(t, "invoices"), []string{"draft-1"}), "tags %v", h.repoTags(t, "invoices"))
}

func TestCreateServiceOnBehalfOfMustBeCaller(t *testing.T) {
	h := newCreateHost(t, false, true)
	a := h.createArgs()
	a.OnBehalfOf, a.IntentDigest = "bob@acme.com", "sha256:x"
	_, refused, err := h.creator(t, true).Execute(callerCtx("alice@acme.com", newSelfReviewed(true), &bytes.Buffer{}), a)
	expect(t, err == nil && refused != nil && refused.Code == pactomcp.RefusalNotAllowed && strings.Contains(refused.Message, "never bob@acme.com"), "%+v %v", refused, err)
	expect(t, len(h.repoTags(t, "invoices")) == 0, "a draft was written")
}

func TestSelfMayCreateServiceNamesService(t *testing.T) {
	h := newCreateHost(t, false, true)
	s := newSelfReviewed(true)
	planCreate(t, h.creator(t, true), callerCtx("alice@acme.com", s, &bytes.Buffer{}), h.createArgs())
	want := map[string]any{"verb": "create", "group": "pacto.trianalab.io", "resource": "services", "name": "invoices", "namespace": "team-billing"}
	expect(t, len(s.asked) == 1 && len(s.asked[0]) == len(want), "reviews %v", s.asked)
	for k, v := range want {
		expect(t, s.asked[0][k] == v, "review %v: %s", s.asked[0], k)
	}
}

func TestCreateServiceSelfReviewDenied(t *testing.T) {
	h := newCreateHost(t, false, true)
	c, a := h.creator(t, true), h.createArgs()
	ctx := callerCtx("alice@acme.com", newSelfReviewed(false), &bytes.Buffer{})
	_, refused, err := c.Plan(ctx, a)
	expect(t, err == nil && refused != nil && refused.Code == pactomcp.RefusalNotAllowed && refused.Message == "you may not create services in team-billing", "%+v %v", refused, err)
	a.IntentDigest = "sha256:x"
	_, refused, _ = c.Execute(ctx, a)
	expect(t, refused != nil && refused.Code == pactomcp.RefusalNotAllowed, "execute %+v", refused)
	expect(t, len(h.repoTags(t, "invoices")) == 0, "a draft was written")
}

func TestCreateServiceSelfReviewFailed(t *testing.T) {
	h := newCreateHost(t, false, true)
	for name, answer := range map[string]func() (map[string]any, error){
		"failed": func() (map[string]any, error) { return nil, errors.New("apiserver down") },
		"unevaluated": func() (map[string]any, error) {
			return map[string]any{"allowed": false, "evaluationError": "webhook"}, nil
		},
	} {
		s := newSelfReviewed(true)
		s.answer = answer
		_, refused, err := h.creator(t, true).Plan(callerCtx("alice@acme.com", s, &bytes.Buffer{}), h.createArgs())
		expect(t, err == nil && refused != nil && refused.Code == pactomcp.RefusalReviewFailed, "%s: %+v %v", name, refused, err)
	}
}

func TestCreateServiceNoPushRefuses(t *testing.T) {
	h := newCreateHost(t, false, true)
	c, ctx := h.creator(t, false), callerCtx("alice@acme.com", newSelfReviewed(true), &bytes.Buffer{})
	expect(t, !c.writes(), "a creator without a push credential writes")
	run, refused := executeCreate(t, c, ctx, h.createArgs())
	expect(t, !run.Executed && refused != nil && refused.Code == pactomcp.RefusalExecuteNeedsStdio &&
		strings.HasPrefix(refused.CLI, "pacto init invoices --from") && strings.Contains(refused.Message, "pacto publish"), "%+v %+v", run, refused)
	expect(t, len(h.repoTags(t, "invoices")) == 0, "a draft was written")
}

func TestCreateServiceExecutePlanStale(t *testing.T) {
	h := newCreateHost(t, false, true)
	c, ctx := h.creator(t, true), callerCtx("alice@acme.com", newSelfReviewed(true), &bytes.Buffer{})
	p := planCreate(t, c, ctx, h.createArgs())
	a := h.createArgs()
	a.IntentDigest, a.Revision, a.Owner = p.IntentDigest, p.Create.Revision, "platform"
	_, refused, err := c.Execute(ctx, a)
	expect(t, err == nil && refused != nil && refused.Code == pactomcp.RefusalPlanStale, "%+v %v", refused, err)
	expect(t, len(h.repoTags(t, "invoices")) == 0, "a draft was written")
}

func TestCreateServiceParameterRefusalsOverMCP(t *testing.T) {
	h := newCreateHost(t, false, true)
	a := h.createArgs()
	a.Offerings = []pactomcp.CreateServiceOffering{{Offering: "postgresql", Values: map[string]any{"storageGB": 900}}}
	ctx := callerCtx("alice@acme.com", newSelfReviewed(true), &bytes.Buffer{})
	raw, refused, err := h.creator(t, true).Plan(ctx, a)
	var out struct{ Refusals []app.CreateRefusal }
	_ = json.Unmarshal(raw, &out)
	expect(t, err == nil && refused != nil && refused.Code == pactomcp.RefusalParameterOutOfRange && len(out.Refusals) == 1 &&
		out.Refusals[0].Field == "configurations[database].values.storageGB" && strings.Contains(out.Refusals[0].MCP, `"storageGB":500`), "%s %+v %v", raw, refused, err)
	// Execute refuses the same, with nothing written.
	a.IntentDigest = "sha256:x"
	_, refused, _ = h.creator(t, true).Execute(ctx, a)
	expect(t, refused != nil && refused.Code == pactomcp.RefusalParameterOutOfRange, "execute %+v", refused)
}

func TestCreateServicePathFromCallerView(t *testing.T) {
	h := newCreateHost(t, false, true)
	ctx := callerCtx("alice@acme.com", newSelfReviewed(true), &bytes.Buffer{})
	hidden := h.creator(t, true)
	hidden.fleet.Opts.View = func(context.Context, *auth.TokenInfo, *fleet.Query) (*fleet.Query, error) {
		return fleet.NewQuery(&fleet.FleetSnapshot{}), nil
	}
	_, refused, err := hidden.Plan(ctx, h.createArgs())
	expect(t, err == nil && refused != nil && refused.Code == pactomcp.RefusalGoldenPathNotDeclared && strings.HasPrefix(refused.Message, "no golden path with this key"),
		"a hidden path: %+v %v", refused, err)
	unreadable := h.creator(t, true)
	unreadable.fleet.Src = func(context.Context) (*fleet.Query, error) { return nil, errors.New("down") }
	_, refused, _ = unreadable.Plan(ctx, h.createArgs())
	expect(t, refused != nil && refused.Code == pactomcp.RefusalGoldenPathNotDeclared, "no view: %+v", refused)
	_, refused, err = h.creator(t, true).Plan(context.Background(), h.createArgs())
	expect(t, refused == nil && errors.Is(err, errNobodyReviewed), "nobody reviewed: %+v %v", refused, err)
}

// TestCreateServiceTakesNoReference: a reference in path is no key of the
// caller's view, so nothing a caller names is ever pulled.
func TestCreateServiceTakesNoReference(t *testing.T) {
	h := newCreateHost(t, false, true)
	a := h.createArgs()
	a.Path = h.path.Ref
	_, refused, err := h.creator(t, true).Plan(callerCtx("alice@acme.com", newSelfReviewed(true), &bytes.Buffer{}), a)
	expect(t, err == nil && refused != nil && refused.Code == pactomcp.RefusalGoldenPathNotDeclared, "%+v %v", refused, err)
}

func TestCreateServiceReviewURL(t *testing.T) {
	expect(t, remoteCreator{}.reviewURL("k") == "", "a review link with no fleet")
	expect(t, remoteCreator{fleet: &pactomcp.FleetMode{}}.reviewURL("k") == "", "a review link with no dashboard")
	got := remoteCreator{fleet: &pactomcp.FleetMode{Opts: pactomcp.FleetOptions{DashboardURL: "https://d/"}}}.reviewURL("ghcr.io/acme/go-http")
	expect(t, got == "https://d/#/fleet/services/ghcr.io%2Facme%2Fgo-http", "review link %q", got)
}

func TestNoRegistryNoCreateService(t *testing.T) {
	expect(t, newRemoteCreator(nil, nil, "", nil, nil) == nil, "a creator without a registry")
	c, err := dashboardMCPCreator(viper.New(), nil, nil, nil)
	expect(t, err == nil && c == nil, "a dashboard creator without a registry: %v %v", c, err)
	v := viper.New()
	v.Set("dashboard.registry", "ghcr.io/acme")
	v.Set("dashboard.declare-registry-config", filepath.Join(t.TempDir(), "missing.json"))
	_, err = dashboardMCPCreator(v, nil, nil, nil)
	expect(t, err != nil, "an unreadable push credential was accepted")
	// Neither HTTP server lists the tool without a registry.
	w := newRemoteFleet(t)
	expect(t, !slices.Contains(remoteToolNames(t, w.h, "good"), "pacto_create_service"), "pacto mcp -t http lists pacto_create_service with no registry")
	_, h := newDashMCP(t)
	expect(t, !slices.Contains(remoteToolNames(t, h, "person"), "pacto_create_service"), "/mcp lists pacto_create_service with no registry")
}

func TestPactoMCPHTTPServesCreateService(t *testing.T) {
	w := newRemoteFleetFlags(t, map[string]string{"registry": "ghcr.io/acme", "declare-registry-config": emptyDockerConfig(t)})
	expect(t, slices.Contains(remoteToolNames(t, w.h, "good"), "pacto_create_service"), "a credentialed caller does not list pacto_create_service")
	names := remoteToolNames(t, newRemoteFleetFlags(t, map[string]string{"registry": "ghcr.io/acme", "declare-registry-config": emptyDockerConfig(t)},
		newCredentiallessAuth()).h, "viewer")
	expect(t, !slices.Contains(names, "pacto_create_service"), "a fleet-only caller lists pacto_create_service: %v", names)
	// The trust file is read once, at start: an unreadable one fails it.
	cmd := newMCPCommand(app.NewService(nil, nil), "v", defaultDeps())
	expect(t, cmd.Flags().Set("delivery-trust", filepath.Join(t.TempDir(), "missing.pem")) == nil, "flag")
	_, err := remoteCreatorFor(cmd, nil, nil, "ghcr.io/acme", nil)
	expect(t, err != nil, "an unreadable trust file was accepted")
}

func TestDashboardMCPServesCreateService(t *testing.T) {
	w := newDashMCPWorld(t, func(v *viper.Viper) {
		v.Set("dashboard.registry", "ghcr.io/acme")
		v.Set("dashboard.declare-registry-config", emptyDockerConfig(t))
	})
	h := w.mcp(t)
	names := remoteToolNames(t, h, "person")
	expect(t, slices.Contains(names, "pacto_create_service") && slices.Contains(names, "pacto_declare"), "person tools: %v", names)
	expect(t, !slices.Contains(remoteToolNames(t, h, "viewer"), "pacto_create_service"), "a fleet-only caller lists pacto_create_service")
	// pacto_declare on /mcp still plans only: it holds no registry or push
	// credential, and only the creator writes with the dashboard's.
	a := dashboardAuthoring(w.v, app.NewService(nil, nil), nil, nil, "")
	rd := a.Declare.(remoteDeclarer)
	expect(t, rd.registry == "" && rd.push == nil && !rd.writes(), "pacto_declare on /mcp writes: %+v", rd)
	c, err := dashboardMCPCreator(w.v, nil, nil, nil)
	rc, _ := c.(remoteCreator)
	expect(t, err == nil && rc.registry == "ghcr.io/acme" && rc.writes(), "the creator holds no push credential: %+v %v", rc, err)
	// An unreadable push credential fails the endpoint at start.
	w.v.Set("dashboard.declare-registry-config", filepath.Join(t.TempDir(), "missing.json"))
	_, err = w.build(t)
	expect(t, err != nil, "an unreadable push credential was accepted")
}

func TestFleetOnlyAuthoringDropsCreateService(t *testing.T) {
	a := fleetOnlyAuthoring(pactomcp.Authoring{CreateService: remoteCreator{}, Declare: remoteDeclarer{}})
	expect(t, a.CreateService == nil && a.Declare == nil, "%+v", a)
}
