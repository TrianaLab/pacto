package app

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
	"github.com/trianalab/pacto/v4/pkg/plugin"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// pgSchema is the closed provisioning schema of the postgresql offering:
// a class, a tunable bounded size, an untiered engine and a sensitive key.
const pgSchema = `{"type":"object","additionalProperties":false,"properties":{
"class":{"type":"string","enum":["small","large"]},
"storageGi":{"type":"integer","minimum":10,"maximum":500,"x-pacto-tier":"tunable"},
"engine":{"type":"string","enum":["postgres"]},
"version":{"type":"string"},
"dbPassword":{"type":"string"}}}`

// createEnv is a declareEnv whose registry holds the postgresql offering
// and the go-http golden path, and whose service pulls from it.
type createEnv struct {
	*declareEnv
	path string
}

// goldenHTTP is the go-http golden path at pactoVersion: a database on the
// postgresql offering and a prod environment.
func goldenHTTP(host, pactoVersion, extra string) map[string]string {
	return map[string]string{
		"pacto.yaml": "pactoVersion: \"" + pactoVersion + "\"\nservice:\n  name: go-http\n  version: 1.2.0\n  owner: {team: platform}\n  template: {}\n" +
			"configurations:\n  - name: database\n    ref: oci://" + host + "/acme/postgresql:17.0.0\n    configuration: provisioning\n" +
			"    values: {class: small, storageGi: 20, engine: postgres}\n    required: true\n" +
			"environments:\n  - name: prod\n    file: environments/prod.yaml\n" + extra,
		"environments/prod.yaml": "database:\n  storageGi: 50\n",
	}
}

func newCreateEnv(t *testing.T, golden map[string]string) *createEnv {
	t.Helper()
	e := newDeclareEnv(t)
	e.publishBundle(t, e.host+"/acme/postgresql", "17.0.0", map[string]string{
		"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: postgresql\n  version: 17.0.0\n  offering: postgresql\n" +
			"configurations:\n  - name: provisioning\n    schema: configuration/provisioning.schema.json\n    required: true\n",
		"configuration/provisioning.schema.json": pgSchema,
	}, nil)
	if golden == nil {
		golden = goldenHTTP(e.host, "2.4", "")
	}
	digest := e.publishBundle(t, e.host+"/acme/paths/go-http", "1.2.0", golden, nil)
	e.svc.BundleStore = oci.NewClient(anonymous, oci.WithInsecureRegistries(e.host))
	return &createEnv{e, "oci://" + e.host + "/acme/paths/go-http@" + digest}
}

// consumable is the caller's consumable read: postgresql, and redis with a
// consume snippet the golden path does not request.
func (e *createEnv) consumable(t *testing.T) map[string]CreateOffering {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal([]byte(pgSchema), &schema); err != nil {
		t.Fatal(err)
	}
	redis := "oci://" + e.host + "/acme/redis:7.0.0"
	return map[string]CreateOffering{
		"postgresql": {Repository: "oci://" + e.host + "/acme/postgresql", Schemas: map[string]map[string]any{"provisioning": schema}},
		"redis": {Repository: e.host + "/acme/redis", Schemas: map[string]map[string]any{"provisioning": {"type": "object", "additionalProperties": false,
			"properties": map[string]any{"memoryMi": map[string]any{"type": "integer", "minimum": 64, "x-pacto-tier": "tunable"}}}},
			Snippet: &fleet.ConsumeSnippet{Entries: fleet.ConsumeEntries{Dependency: &contract.Dependency{Name: "redis", Ref: redis, Compatibility: "^7.0.0"},
				Configurations: []fleet.ConsumeConfiguration{{Configuration: contract.Configuration{Name: "cache", Ref: redis, ProviderConfiguration: "provisioning", Required: true}}}}}},
	}
}

// createReq is ana's create of invoices owned by billing.
func (e *createEnv) createReq(t *testing.T, picks ...OfferingPick) DeclareRequest {
	req := e.req()
	req.Service, req.Namespace = "invoices", ""
	req.Create = &CreateFrom{Path: e.path, Owner: "billing", Namespace: "team-billing", Caller: "ana@acme.com", Offerings: picks,
		Consumable: e.consumable(t), ConsumableAsOf: "2026-11-23T10:00:00Z"}
	return req
}

// planRefused is req's plan refusal.
func (e *createEnv) planRefused(t *testing.T, req DeclareRequest) (*DeclarePlan, *DeclareError) {
	t.Helper()
	p, _, err := e.svc.PlanDeclare(t.Context(), req)
	return p, refusal(t, err)
}

var createNow = time.Date(2026, 11, 23, 10, 2, 11, 0, time.UTC)

func (e *createEnv) at() { e.svc.sys.now = func() time.Time { return createNow } }

func TestCreatePlanFromGoldenPath(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.at()
	p, d := e.plan(t, e.createReq(t))
	repo, c, b, rec := e.host+"/acme/invoices", p.Create, d.bundle, d.record
	for name, ok := range map[string]bool{
		"service":          p.Service == "invoices",
		"version":          p.Version == firstDeclaration,
		"deploy":           p.Deploy == "none",
		"repo":             p.BundleRepo == repo,
		"declared":         p.State == contract.DeclarationDeclared,
		"no workload":      p.InferredFrom == "" && p.Cluster == "",
		"owner":            p.Owner == "team:billing",
		"intent":           p.IntentDigest != "",
		"no accept":        p.AcceptCLI == "",
		"path":             c.Path == e.path,
		"revision":         c.Revision == "pacto-create:ana@acme.com:2026-11-23T10:02:11Z" && d.input.Revision == c.Revision,
		"namespace":        c.Namespace == "team-billing",
		"signature":        c.Signature.Status == fleet.StepStatusUnknown && c.Signature.Detail == noTemplateKey,
		"completeness":     c.ConsumableCompleteness == "complete" && c.ConsumableAsOf == "2026-11-23T10:00:00Z",
		"values file":      c.ValuesFile == "",
		"init":             c.InitCLI == "pacto init invoices --from "+e.path+" --owner billing --registry "+e.host+"/acme",
		"raised":           b.Contract.PactoVersion == "2.5",
		"origin":           b.Contract.Service.Origin != nil && b.Contract.Service.Origin.Ref == e.path,
		"no template":      b.Contract.Service.Template == nil,
		"publish":          d.input.Publish.Bundle == "oci://"+repo,
		"no cluster":       rec.Inputs.Cluster == nil && rec.Declaration.InferredFrom == "" && rec.Declaration.Cluster == "",
		"discovery":        rec.Inputs.Discovery != "",
		"note":             slices.Contains(p.Notes, "a created service: every value comes from the golden path "+e.path+" and the values set here; review pacto.yaml"),
		"no cluster note":  !slices.ContainsFunc(p.Notes, func(n string) bool { return strings.Contains(n, "cluster") }),
		"no workload rung": slices.ContainsFunc(p.RepositoryTried, func(r RungNote) bool { return r.Reason == noWorkloadYet }),
	} {
		if !ok {
			t.Errorf("%s: %+v\n%s\n%s", name, p, d.raw, rec.Marshal())
		}
	}
	for _, a := range e.dyn.Actions() {
		if r := a.GetResource().Resource; r != "configmaps" {
			t.Fatalf("read %s", r) // the workload is never read
		}
	}
	if err := validDeclaration(b); err != nil {
		t.Fatal(err)
	}
}

// TestCreateKeepsNewerPactoVersion: a path already at 2.5 is not touched.
func TestCreateKeepsNewerPactoVersion(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.path = "oci://" + e.host + "/acme/paths/go-http@" + e.publishBundle(t, e.host+"/acme/paths/go-http", "1.3.0", goldenHTTP(e.host, "2.6", ""), nil)
	if _, d := e.plan(t, e.createReq(t)); d.bundle.Contract.PactoVersion != "2.6" {
		t.Fatalf("%s", d.raw)
	}
}

func TestCreateRunsNoPlugin(t *testing.T) {
	e := newCreateEnv(t, nil)
	golden := goldenHTTP(e.host, "2.5", "")
	golden["pacto.yaml"] = strings.Replace(golden["pacto.yaml"], "template: {}", "template:\n    plugins:\n      - name: scaffold", 1)
	e.path = "oci://" + e.host + "/acme/paths/go-http@" + e.publishBundle(t, e.host+"/acme/paths/go-http", "1.4.0", golden, nil)
	e.svc.PluginRunner = &mockPluginRunner{RunFn: func(context.Context, string, plugin.GenerateRequest) (*plugin.GenerateResponse, error) {
		t.Fatal("a plugin ran on the server")
		return nil, nil
	}}
	e.plan(t, e.createReq(t))
}

func TestCreateRefusesUnpinnedOrInvalid(t *testing.T) {
	e := newCreateEnv(t, nil)
	for want, edit := range map[string]func(*DeclareRequest){
		codeGoldenPathNotDeclared: func(r *DeclareRequest) { r.Create.Path = "oci://" + e.host + "/acme/paths/go-http:1.2.0" },
		codeFieldNotAcceptable:    func(r *DeclareRequest) { r.Service = "Not A Name" },
		codeOwnerRequired:         func(r *DeclareRequest) { r.Create.Owner = "" },
	} {
		req := e.createReq(t)
		edit(&req)
		if p, de := e.planRefused(t, req); de.Code != want || p.IntentDigest != "" {
			t.Errorf("%s: %+v", want, de)
		}
	}
	// A path some signature check refuses creates nothing.
	req := e.createReq(t)
	req.Create.Path = "oci://" + e.host + "/acme/paths/missing@sha256:" + strings.Repeat("0", 64)
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); err == nil {
		t.Fatal("a missing golden path planned")
	}
}

func TestCreateRepositoryRules(t *testing.T) {
	for _, c := range []struct{ repo, want string }{
		{"", "r.io/acme/invoices"},
		{"oci://r.io/acme/team/invoices", "r.io/acme/team/invoices"},
		{"r.io/other/invoices", ""},
		{"r.io/acme/payments", ""},
		{"r.io/acme/invoices:1.0.0", ""},
		{"r.io/acme/../x/invoices", ""},
	} {
		got, err := createRepo("oci://r.io/acme/", c.repo, "invoices")
		if got != c.want || (c.want == "") != (err != nil) {
			t.Errorf("%q: %q %v", c.repo, got, err)
		}
		if err != nil && refusal(t, err).Code != codeRepositoryOutsideRegistry {
			t.Errorf("%q: %v", c.repo, err)
		}
	}
}

func TestCreateRevisionRules(t *testing.T) {
	for _, c := range []struct {
		rev string
		ok  bool
	}{
		{"", true}, {"a1b2c3d", true}, {"pacto-create:ana@acme.com:2026-11-23T10:02:11Z", true},
		{strings.Repeat("a", 129), false}, {"has space", false}, {"tab\there", false},
		{"pacto-create:bob@acme.io:2026-11-23T10:02:11Z", false}, {"pacto-create:ana@acme.com:yesterday", false},
	} {
		got, err := createRevision(c.rev, "ana@acme.com", createNow)
		if c.ok != (err == nil) || c.ok && got == "" {
			t.Errorf("%q: %q %v", c.rev, got, err)
		}
	}
	// An edited revision is bound into the intent.
	e := newCreateEnv(t, nil)
	p1, _ := e.plan(t, e.createReq(t))
	req := e.createReq(t)
	req.Create.Revision = "a1b2c3d"
	p2, d := e.plan(t, req)
	if p1.IntentDigest == p2.IntentDigest || d.input.Revision != "a1b2c3d" {
		t.Fatalf("%s %s %s", p1.IntentDigest, p2.IntentDigest, d.input.Revision)
	}
}

func TestCreateServiceExistsHiddenRevealsNoDetail(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.publishBundle(t, e.host+"/acme/invoices", "1.0.0", e.invoicesBaseline("1.0.0", ""), nil)
	p, de := e.planRefused(t, e.createReq(t))
	want := "invoices is already taken in this registry: pick another name, or adopt your own service with pacto declare"
	if de.Code != codeServiceExists || de.Message != want || de.CLI != "" || len(p.Drafts) != 0 || p.DraftsTotal != 0 || p.IntentDigest != "" {
		t.Fatalf("%+v %+v", de, p)
	}
}

// draftFiles is a created draft of invoices asked by asker for onBehalfOf.
func (e *createEnv) draftOf(t *testing.T, repo, tag, onBehalfOf string) {
	t.Helper()
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "4.0.0"}, Fields: []contract.Inferred{},
		Declaration: &contract.InferredDeclaration{State: contract.DeclarationDraft, Asker: contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent},
			OnBehalfOf: onBehalfOf}}
	e.publishBundle(t, repo, tag, map[string]string{"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: invoices\n  version: 0.1.0\n",
		contract.InferredRecordFile: string(rec.Marshal())}, nil)
}

func TestCreateServiceExistsForAnotherPersonsDraft(t *testing.T) {
	e := newCreateEnv(t, nil)
	repo := e.host + "/acme/invoices"
	e.draftOf(t, repo, "draft-1", "ana@acme.com")
	p, _ := e.plan(t, e.createReq(t))
	if len(p.Drafts) != 1 || p.Drafts[0].Tag != "draft-1" || p.DraftsTotal != 1 {
		t.Fatalf("own draft: %+v", p.Drafts)
	}
	e.draftOf(t, repo, "draft-2", "bob@acme.com")
	p, de := e.planRefused(t, e.createReq(t))
	blob, _ := json.Marshal(struct {
		P *DeclarePlan
		E *DeclareError
	}{p, de})
	if de.Code != codeServiceExists || strings.Contains(string(blob), "bob") || strings.Contains(string(blob), "sha256:") {
		t.Fatalf("%s", blob)
	}
	// With no caller named, no draft is anyone's own.
	req := e.createReq(t)
	req.Create.Caller = ""
	if _, de := e.planRefused(t, req); de.Code != codeServiceExists {
		t.Fatalf("%+v", de)
	}
}

func TestCreateServiceRegistryUnreadable(t *testing.T) {
	e := newCreateEnv(t, nil)
	req := e.createReq(t)
	req.Store = &faultyRegistry{Registry: e.reg, tags: errors.New("denied")}
	if _, de := e.planRefused(t, req); de.Code != codeRegistryUnreadable {
		t.Fatalf("%+v", de)
	}
}

func TestCreateSetsRequestedOfferingValues(t *testing.T) {
	e := newCreateEnv(t, nil)
	p, d := e.plan(t, e.createReq(t, OfferingPick{Offering: "postgresql", Values: map[string]any{"storageGi": 100, "version": "16"},
		Environments: map[string]map[string]any{"prod": {"storageGi": 200}}}))
	db := d.bundle.Contract.Configurations[0]
	if db.Values["storageGi"] != 100 || db.Values["version"] != "16" || db.Values["class"] != "small" {
		t.Fatalf("%s", d.raw)
	}
	prod, _ := fs.ReadFile(d.bundle.FS, "environments/prod.yaml")
	if !strings.Contains(string(prod), "storageGi: 200") {
		t.Fatalf("%s", prod)
	}
	if !strings.HasSuffix(p.Create.InitCLI, " --values "+createValuesFile) || !strings.Contains(p.Create.ValuesFile, "version: \"16\"") {
		t.Fatalf("%+v", p.Create) // a string stays a string in the replay
	}
}

func TestCreateAppendsAcceptedOffering(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.publishBundle(t, e.host+"/acme/redis", "7.0.0", map[string]string{
		"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: redis\n  version: 7.0.0\n  offering: redis\n" +
			"configurations:\n  - name: provisioning\n    schema: schema.json\n    required: true\n",
		"schema.json": `{"type":"object","additionalProperties":false,"properties":{"memoryMi":{"type":"integer","minimum":64}}}`,
	}, nil)
	_, d := e.plan(t, e.createReq(t, OfferingPick{Offering: "redis", Values: map[string]any{"memoryMi": 128}}))
	c := d.bundle.Contract
	if len(c.Dependencies) != 1 || c.Dependencies[0].Name != "redis" || len(c.Configurations) != 2 || c.Configurations[1].Name != "cache" ||
		c.Configurations[1].Values["memoryMi"] != 128 {
		t.Fatalf("%s", d.raw)
	}
}

func TestCreateCollectsEveryRefusal(t *testing.T) {
	e := newCreateEnv(t, nil)
	p, de := e.planRefused(t, e.createReq(t,
		OfferingPick{Offering: "postgresql", Values: map[string]any{"storageGi": 900, "class": "medium", "storegeGi": 1, "dbPassword": "hunter2"},
			Environments: map[string]map[string]any{"prod": {"engine": "postgres"}, "staging": {"storageGi": 20}}},
		OfferingPick{Offering: "mysql"}))
	got := map[string]string{}
	for _, r := range de.Refusals {
		got[r.Field] = r.Code
	}
	want := map[string]string{
		"configurations[database].values.storageGi":  codeParameterOutOfRange,
		"configurations[database].values.class":      codeClassNotOffered,
		"configurations[database].values.storegeGi":  codeParameterNotDeclared,
		"configurations[database].values.dbPassword": codeParameterSensitive,
		"environments[prod].database.engine":         codeTierNotTunable,
		"environments[staging]":                      codeEnvironmentNotDeclared,
		"offerings[mysql]":                           codeOfferingNotConsumable,
	}
	for f, c := range want {
		if got[f] != c {
			t.Errorf("%s: %q, want %q (%+v)", f, got[f], c, de.Refusals)
		}
	}
	if p.IntentDigest != "" || strings.Contains(de.Message, "hunter2") {
		t.Fatalf("%+v", de)
	}
	for _, r := range de.Refusals {
		if r.Field != "configurations[database].values.storageGi" {
			continue
		}
		if r.Closest != 500.0 && r.Closest != 500 {
			t.Errorf("closest %v", r.Closest)
		}
		if !strings.HasSuffix(r.CLI, " --set 'configurations[0].values.storageGi=500'") || !strings.HasPrefix(r.CLI, "pacto init invoices --from ") ||
			r.MCP != `{"offerings":[{"offering":"postgresql","values":{"storageGi":500}}]}` {
			t.Errorf("%+v", r)
		}
	}
}

func TestOfferingNotConsumableHiddenReadsAsAbsent(t *testing.T) {
	e := newCreateEnv(t, nil)
	// The path's own offering hidden from (or absent in) the caller's view.
	req := e.createReq(t)
	delete(req.Create.Consumable, "postgresql")
	_, de := e.planRefused(t, req)
	req2 := e.createReq(t, OfferingPick{Offering: "postgresql"})
	req2.Create.Consumable = nil
	_, de2 := e.planRefused(t, req2)
	if de.Code != codeOfferingNotConsumable || de2.Code != codeOfferingNotConsumable || de.Refusals[0].Message != de2.Refusals[0].Message {
		t.Fatalf("%+v %+v", de, de2)
	}
}

func TestCreatePartialConsumableReadsUnknown(t *testing.T) {
	e := newCreateEnv(t, nil)
	req := e.createReq(t, OfferingPick{Offering: "kafka"})
	req.Create.ConsumablePartial = true
	_, de := e.planRefused(t, req)
	if de.Code != codeOfferingUnknown || !strings.Contains(de.Message, "Pacto could not confirm kafka is consumable: the offerings list is partial") {
		t.Fatalf("%+v", de)
	}
	// A partial read still plans what it lists, and says it is partial.
	req = e.createReq(t)
	req.Create.ConsumablePartial = true
	p, _ := e.plan(t, req)
	if p.Create.ConsumableCompleteness != "partial" || !slices.Contains(p.Notes, partialNote) {
		t.Fatalf("%+v", p)
	}
}

func TestCreateOfferingWithoutConfiguration(t *testing.T) {
	e := newCreateEnv(t, nil)
	req := e.createReq(t, OfferingPick{Offering: "queue", Values: map[string]any{"x": 1}})
	req.Create.Consumable["queue"] = CreateOffering{Repository: e.host + "/acme/queue"}
	if _, de := e.planRefused(t, req); de.Code != codeParameterNotDeclared || de.Refusals[0].Field != "offerings[queue]" {
		t.Fatalf("%+v", de)
	}
	// A provider schema the read did not carry reads as not consumable.
	req = e.createReq(t)
	req.Create.Consumable["postgresql"] = CreateOffering{Repository: e.host + "/acme/postgresql", Schemas: map[string]map[string]any{"a": {}, "b": {}}}
	if _, de := e.planRefused(t, req); de.Code != codeOfferingNotConsumable {
		t.Fatalf("%+v", de)
	}
}

func TestCreateEnvironmentWithoutFile(t *testing.T) {
	e := newCreateEnv(t, nil)
	golden := goldenHTTP(e.host, "2.6", "")
	golden["pacto.yaml"] = strings.Replace(golden["pacto.yaml"], "  - name: prod\n    file: environments/prod.yaml\n", "  - name: prod\n", 1)
	delete(golden, "environments/prod.yaml")
	e.path = "oci://" + e.host + "/acme/paths/go-http@" + e.publishBundle(t, e.host+"/acme/paths/go-http", "2.0.0", golden, nil)
	_, d := e.plan(t, e.createReq(t, OfferingPick{Offering: "postgresql", Environments: map[string]map[string]any{"prod": {"storageGi": 200}}}))
	if d.bundle.Contract.Environments[0].File != "prod.values.yaml" {
		t.Fatalf("%s", d.raw)
	}
	if data, err := fs.ReadFile(d.bundle.FS, "prod.values.yaml"); err != nil || !strings.Contains(string(data), "storageGi: 200") {
		t.Fatalf("%v %s", err, data)
	}
}

func TestCreateIntentBindsEveryInput(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.at()
	base, _ := e.plan(t, e.createReq(t))
	seen := map[string]bool{base.IntentDigest: true}
	for name, edit := range map[string]func(*CreateFrom){
		"owner":      func(c *CreateFrom) { c.Owner = "payments" },
		"namespace":  func(c *CreateFrom) { c.Namespace = "other" },
		"repository": func(c *CreateFrom) { c.Repository = e.host + "/acme/team/invoices" },
		"revision":   func(c *CreateFrom) { c.Revision = "abc" },
		"value": func(c *CreateFrom) {
			c.Offerings = []OfferingPick{{Offering: "postgresql", Values: map[string]any{"storageGi": 30}}}
		},
	} {
		req := e.createReq(t)
		edit(req.Create)
		p, _ := e.plan(t, req)
		if seen[p.IntentDigest] {
			t.Errorf("%s: the intent did not change", name)
		}
		seen[p.IntentDigest] = true
	}
	again, _ := e.plan(t, e.createReq(t))
	if again.IntentDigest != base.IntentDigest {
		t.Fatal("the same create planned another intent")
	}
}

func TestCreatePlanStale(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.login(personToken("ana@acme.com"))
	req := e.createReq(t)
	req.IntentDigest = "sha256:old"
	_, err := e.svc.Declare(t.Context(), req)
	if de := refusal(t, err); de.Code != codePlanStale || !strings.HasPrefix(de.CLI, "pacto init invoices --from ") {
		t.Fatalf("%+v", de)
	}
}

func TestCreateRefusalCLIIsInit(t *testing.T) {
	e := newCreateEnv(t, nil)
	req := e.createReq(t)
	req.Registry = ""
	if _, de := e.planRefused(t, req); de.Code != codeNoRegistry || !strings.HasPrefix(de.CLI, "pacto init invoices") {
		t.Fatalf("%+v", de)
	}
}

func TestCreateCLIQuotesValues(t *testing.T) {
	req := DeclareRequest{Service: "invoices", Registry: "r.io/acme", Create: &CreateFrom{Path: "oci://r.io/p@sha256:1", Owner: "bill'; rm -rf / $(x)\nnext",
		Offerings: []OfferingPick{{Offering: "pg"}}}}
	want := `pacto init invoices --from oci://r.io/p@sha256:1 --owner 'bill'\''; rm -rf / $(x)next' --registry r.io/acme --values ` + createValuesFile
	if got := req.command(""); got != want {
		t.Fatalf("%s", got)
	}
}

func TestCreateOwnerMemberAllowed(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.withOwnerPolicy(t)
	e.login(personToken("ana@acme.com"))
	res := e.declare(t, e.createReq(t))
	if !res.Executed || !res.Signed || res.Tag != firstDeclaration || res.Draft {
		t.Fatalf("%+v", res)
	}
	if !slices.ContainsFunc(res.Plan.Notes, func(n string) bool {
		return strings.HasPrefix(n, "service.owner is team:billing: only a person among billing members")
	}) {
		t.Fatalf("%v", res.Plan.Notes)
	}
}

func TestCreateOwnerNonMemberRefused(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.withOwnerPolicy(t)
	e.login(personToken("eve@acme.com"))
	_, err := e.svc.Declare(t.Context(), e.createReq(t))
	if de := refusal(t, err); de.Code != "OWNER_SIGNER_NOT_ALLOWED" || !strings.HasPrefix(de.CLI, "pacto init invoices") {
		t.Fatalf("%+v", de)
	}
	if tags, _ := e.reg.Tags(t.Context(), e.host+"/acme/invoices"); len(tags) != 0 {
		t.Fatalf("written: %v", tags)
	}
}

func TestCreateOwnerPolicyUnverified(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.login(personToken("ana@acme.com"))
	p, _ := e.plan(t, e.createReq(t))
	if !slices.ContainsFunc(p.Notes, func(n string) bool { return strings.Contains(n, "unknown until a verified owner policy is read") }) {
		t.Fatalf("%v", p.Notes)
	}
	_, err := e.svc.Declare(t.Context(), e.createReq(t))
	if de := refusal(t, err); de.Code != "OWNER_POLICY_UNVERIFIED" {
		t.Fatalf("%+v", de)
	}
}

// TestCreatePublishSignedByPerson: the first version is signed by the
// person who logged in, declared with them as asker, indexed in the
// installation index and handed over with the person's own commands.
func TestCreatePublishSignedByPerson(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.withOwnerPolicy(t)
	e.login(personToken("ana@acme.com"))
	req := e.createReq(t)
	req.Create.Repository = e.host + "/acme/team/invoices"
	res := e.declare(t, req)
	if got := e.signers(t, res.Published); len(got) != 1 || got[0].Subject != "ana@acme.com" {
		t.Fatalf("%+v", got)
	}
	b, err := e.reg.Pull(t.Context(), res.Published)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := contract.ReadInferredRecord(b.FS)
	if rec.Declaration.State != contract.DeclarationDeclared || rec.Declaration.Asker.Subject != "ana@acme.com" || rec.Declaration.InferredFrom != "" {
		t.Fatalf("%s", rec.Marshal())
	}
	cr := res.Created
	index := e.host + "/acme/" + orgindex.RepositoryName
	if cr == nil || cr.Index == "" || !strings.HasPrefix(cr.Index, "indexed as "+index+":") ||
		cr.PullCLI != "pacto pull oci://"+res.Published || !strings.HasPrefix(cr.GitCLI, "gh repo create <org>/invoices --private --clone && cd invoices && pacto init invoices") {
		t.Fatalf("%+v", cr)
	}
	if _, ok := res.Files["environments/prod.yaml"]; !ok || len(res.Files) < 3 {
		t.Fatalf("%v", slices.Collect(func(yield func(string) bool) {
			for k := range res.Files {
				yield(k)
			}
		}))
	}
}

func TestCreateIndexFailureKeepsPublish(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.withOwnerPolicy(t)
	e.login(personToken("ana@acme.com"))
	req := e.createReq(t)
	var opened []string
	req.Create.OpenIndex = func(repo string) (*remote.Repository, error) {
		opened = append(opened, repo)
		return nil, errors.New("outside the push scope")
	}
	res := e.declare(t, req)
	if !res.Executed || res.Tag != firstDeclaration || !strings.HasPrefix(res.Created.Index, "not indexed in ") {
		t.Fatalf("%+v", res.Created)
	}
	for _, r := range opened {
		if r != e.host+"/acme/invoices" && r != e.host+"/acme/pacto-index" {
			t.Errorf("opened %s", r) // only the repository and the index
		}
	}
}

func TestCreateAgentWritesDraftWithoutAcceptCLI(t *testing.T) {
	e := newCreateEnv(t, nil)
	req := e.createReq(t)
	req.Agent, req.OnBehalfOf = true, "ana@acme.com"
	res := e.declare(t, req)
	if !res.Draft || res.Tag != "draft-1" || res.Plan.AcceptCLI != "" || res.Created != nil {
		t.Fatalf("%+v", res)
	}
	if tags, _ := e.reg.Tags(t.Context(), e.host+"/acme/invoices"); slices.ContainsFunc(tags, func(tg string) bool { return tg == firstDeclaration }) {
		t.Fatalf("an agent wrote a version: %v", tags)
	}
}

func TestCreateServiceHoldsNoSCMToken(t *testing.T) {
	for _, v := range []any{CreateFrom{}, OfferingPick{}, CreateOffering{}, CreateRefusal{}, CreatePlan{}, CreatedService{}} {
		blob, _ := json.Marshal(v)
		if s := strings.ToLower(string(blob)); strings.Contains(s, "token") || strings.Contains(s, "scm") {
			t.Errorf("%T: %s", v, blob)
		}
	}
}

func TestCreateAmbientNote(t *testing.T) {
	e := newCreateEnv(t, nil)
	t.Setenv("SIGSTORE_ID_TOKEN", personToken("ana@acme.com"))
	p, _ := e.plan(t, e.createReq(t))
	if !slices.ContainsFunc(p.Notes, func(n string) bool {
		return strings.HasSuffix(n, "a person accepts it with the dashboard's Create service")
	}) {
		t.Fatalf("%v", p.Notes)
	}
}

func TestCreateHelpers(t *testing.T) {
	if got := nested("a.b", 1); got["a"].(map[string]any)["b"] != 1 {
		t.Fatal(got)
	}
	if _, _, ok := envField("values.x"); ok {
		t.Fatal("not an environment field")
	}
	if r := createRefusal(validationRefusal("values", nil), 0, "db", "pg", "pacto init"); r.Field != "configurations[db]" || r.CLI != "pacto init" {
		t.Fatalf("%+v", r)
	}
	if r := createRefusal(validationRefusal("environments[prod].values.size", 5), 0, "db", "pg", "x"); r.Field != "environments[prod].db.size" ||
		r.MCP != `{"offerings":[{"offering":"pg","environments":{"prod":{"size":5}}}]}` {
		t.Fatalf("%+v", r)
	}
}

// validationRefusal is a value check's refusal at field.
func validationRefusal(field string, closest any) validation.OfferingValueRefusal {
	return validation.OfferingValueRefusal{Code: "OfferingSchemaOpen", Field: field, Closest: closest}
}

func TestCreateRefusesRepositoryAndRevisionInPlan(t *testing.T) {
	e := newCreateEnv(t, nil)
	req := e.createReq(t)
	req.Create.Repository = "elsewhere.io/acme/invoices"
	if _, de := e.planRefused(t, req); de.Code != codeRepositoryOutsideRegistry {
		t.Fatalf("%+v", de)
	}
	req = e.createReq(t)
	req.Create.Revision = "pacto-create:bob@acme.io:2026-11-23T10:02:11Z"
	if _, de := e.planRefused(t, req); de.Code != codeFieldNotAcceptable {
		t.Fatalf("%+v", de)
	}
}

// TestCreateFolderWriteFailures: a folder InitFrom did not leave whole, or
// a write that fails, refuses the plan.
func TestCreateFolderWriteFailures(t *testing.T) {
	for name, fail := range map[string]func(path string, n int) (skip bool, err error){
		"no pacto.yaml":     func(p string, _ int) (bool, error) { return strings.HasSuffix(p, "invoices/pacto.yaml"), nil },
		"no values file":    func(p string, _ int) (bool, error) { return strings.HasSuffix(p, "environments/prod.yaml"), nil },
		"rewrite fails":     func(p string, n int) (bool, error) { return false, failOn(p, n, "invoices/pacto.yaml", 2) },
		"values file fails": func(p string, n int) (bool, error) { return false, failOn(p, n, "environments/prod.yaml", 2) },
	} {
		t.Run(name, func(t *testing.T) {
			e := newCreateEnv(t, nil)
			counts := map[string]int{}
			e.svc.sys.writeFile = func(p string, data []byte, perm os.FileMode) error {
				counts[filepath.Base(filepath.Dir(p))+"/"+filepath.Base(p)]++
				skip, err := fail(p, counts[filepath.Base(filepath.Dir(p))+"/"+filepath.Base(p)])
				if skip || err != nil {
					return err
				}
				return os.WriteFile(p, data, perm)
			}
			req := e.createReq(t, OfferingPick{Offering: "postgresql", Environments: map[string]map[string]any{"prod": {"storageGi": 60}}})
			if _, _, err := e.svc.PlanDeclare(t.Context(), req); err == nil {
				t.Fatal("planned")
			}
		})
	}
}

// failOn fails the n-th write of the file ending in suffix.
func failOn(p string, n int, suffix string, at int) error {
	if strings.HasSuffix(p, suffix) && n == at {
		return errors.New("disk full")
	}
	return nil
}

func TestCreateDocEdges(t *testing.T) {
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"size": map[string]any{"type": "integer"}}}
	d := &createDoc{doc: map[string]any{
		"dependencies":   []any{map[string]any{"name": "redis"}},
		"configurations": []any{map[string]any{"name": "local", "schema": "s.json"}, "not a map", map[string]any{"name": "db", "ref": "oci://r.io/pg:1", "configuration": "other", "values": map[string]any{"size": 1}}},
		"environments":   []any{map[string]any{"name": "dev"}, "not a map"},
	}, envFiles: map[string]map[string]any{"dev": {}}, c: CreateFrom{Consumable: map[string]CreateOffering{
		"pg": {Repository: "r.io/pg", Schemas: map[string]map[string]any{"provisioning": schema}},
	}}}
	// A dependency of the snippet's name is kept; a configuration with no
	// ref is never an offering's; one schema answers any provider name.
	d.appendOffering(CreateOffering{Snippet: &fleet.ConsumeSnippet{Entries: fleet.ConsumeEntries{Dependency: &contract.Dependency{Name: "redis"}}}})
	d.appendOffering(CreateOffering{})
	d.check("pacto init")
	if len(d.doc["dependencies"].([]any)) != 1 || len(d.refusals) != 0 {
		t.Fatalf("%+v %+v", d.doc, d.refusals)
	}
	// An environment with no values is not written.
	dir := t.TempDir()
	if err := d.write(dir, os.WriteFile); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dev.values.yaml")); err == nil {
		t.Fatal("an empty environment file was written")
	}
	// The intent sorts the picks.
	a := createIntent(CreateFrom{Offerings: []OfferingPick{{Offering: "b"}, {Offering: "a"}}})
	b := createIntent(CreateFrom{Offerings: []OfferingPick{{Offering: "a"}, {Offering: "b"}}})
	if a != b {
		t.Fatal("pick order changed the intent")
	}
}
