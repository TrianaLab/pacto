package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
	"github.com/trianalab/pacto/v4/pkg/override"
	"github.com/trianalab/pacto/v4/pkg/plugin"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
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
	pg   string // the postgresql offering's digest
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
	pg := e.publishBundle(t, e.host+"/acme/postgresql", "17.0.0", map[string]string{
		"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: postgresql\n  version: 17.0.0\n  offering: postgresql\n" +
			"configurations:\n  - name: provisioning\n    schema: configuration/provisioning.schema.json\n    required: true\n",
		"configuration/provisioning.schema.json": pgSchema,
	}, nil)
	e.publishBundle(t, e.host+"/acme/redis", "7.0.0", map[string]string{
		"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: redis\n  version: 7.0.0\n  offering: redis\n" +
			"configurations:\n  - name: provisioning\n    schema: schema.json\n    required: true\n",
		"schema.json": `{"type":"object","additionalProperties":false,"properties":{"memoryMi":{"type":"integer","minimum":64,"x-pacto-tier":"tunable"}}}`,
	}, nil)
	if golden == nil {
		golden = goldenHTTP(e.host, "2.4", "")
	}
	digest := e.publishBundle(t, e.host+"/acme/paths/go-http", "1.2.0", golden, nil)
	e.svc.BundleStore = oci.NewClient(anonymous, oci.WithInsecureRegistries(e.host))
	return &createEnv{e, "oci://" + e.host + "/acme/paths/go-http@" + digest, pg}
}

// consumable is the caller's consumable read: postgresql, and redis with a
// consume snippet the golden path does not request.
func (e *createEnv) consumable(t *testing.T) map[string]CreateOffering {
	t.Helper()
	redis := "oci://" + e.host + "/acme/redis:7.0.0"
	return map[string]CreateOffering{
		"postgresql": {Repository: "oci://" + e.host + "/acme/postgresql"},
		"redis": {Repository: e.host + "/acme/redis",
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
	if files := d.Files(); string(files[DefaultContractPath]) != string(d.raw) || len(files) != len(d.files) {
		t.Errorf("files %v", slices.Collect(maps.Keys(files)))
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

// repoCases checks createRepo on cases: want "" is a refusal.
func repoCases(t *testing.T, cases []struct{ repo, want string }) {
	t.Helper()
	for _, c := range cases {
		got, err := createRepo("oci://r.io/acme/", c.repo, "invoices")
		if got != c.want || (c.want == "") != (err != nil) {
			t.Errorf("%q: %q %v", c.repo, got, err)
		}
		if err != nil && refusal(t, err).Code != codeRepositoryOutsideRegistry {
			t.Errorf("%q: %v", c.repo, err)
		}
	}
}

// TestCreateRepositoryOutsideRegistry: the push goes only under the
// installation registry, never to a repository a caller steers elsewhere.
func TestCreateRepositoryOutsideRegistry(t *testing.T) {
	repoCases(t, []struct{ repo, want string }{
		{"", "r.io/acme/invoices"},
		{"oci://r.io/acme/team/invoices", "r.io/acme/team/invoices"},
		{"r.io/other/invoices", ""},
		{"other.io/acme/invoices", ""},
		{"r.io/acme/invoices:1.0.0", ""},
		{"r.io/acme/invoices@sha256:" + strings.Repeat("0", 64), ""},
		{"r.io/acme/../x/invoices", ""},
	})
	e := newCreateEnv(t, nil)
	req := e.createReq(t)
	req.Create.Repository = "evil.io/acme/invoices"
	if p, de := e.planRefused(t, req); de.Code != codeRepositoryOutsideRegistry || p.IntentDigest != "" {
		t.Fatalf("%+v", de)
	}
}

// TestCreateRepositoryNameMismatch: the repository ends in the service name.
func TestCreateRepositoryNameMismatch(t *testing.T) {
	repoCases(t, []struct{ repo, want string }{
		{"r.io/acme/team/invoices", "r.io/acme/team/invoices"},
		{"r.io/acme/payments", ""},
		{"r.io/acme/invoices/payments", ""},
		{"r.io/acme/invoices-v2", ""},
	})
	e := newCreateEnv(t, nil)
	req := e.createReq(t)
	req.Create.Repository = e.host + "/acme/payments"
	if p, de := e.planRefused(t, req); de.Code != codeRepositoryOutsideRegistry || p.IntentDigest != "" {
		t.Fatalf("%+v", de)
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

// TestCreateDefaultRevisionKeepsTheIntent: the default revision names the
// plan's clock, so a write in a later second keeps the reviewed intent,
// whether it sends the planned revision back or none; the planned one is
// what gets published when it is sent back.
func TestCreateDefaultRevisionKeepsTheIntent(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.at()
	p, _ := e.plan(t, e.createReq(t))
	e.svc.sys.now = func() time.Time { return createNow.Add(2 * time.Second) }
	later, _ := e.plan(t, e.createReq(t))
	back := e.createReq(t)
	back.Create.Revision = p.Create.Revision
	sent, d := e.plan(t, back)
	if later.IntentDigest != p.IntentDigest || sent.IntentDigest != p.IntentDigest || d.input.Revision != p.Create.Revision {
		t.Fatalf("%s %s %s %s", p.IntentDigest, later.IntentDigest, sent.IntentDigest, d.input.Revision)
	}
	e.withOwnerPolicy(t)
	e.login(personToken("ana@acme.com"))
	back.IntentDigest = p.IntentDigest
	if res := e.declare(t, back); res.Tag != firstDeclaration {
		t.Fatalf("%+v", res)
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
	// pacto init replays no per-environment value, and the plan says so.
	if !slices.Contains(p.Notes, envValuesNote) {
		t.Fatalf("%v", p.Notes)
	}
	if p, _ := e.plan(t, e.createReq(t)); slices.Contains(p.Notes, envValuesNote) {
		t.Fatalf("noted with no per-environment value: %v", p.Notes)
	}
	if !strings.HasSuffix(p.Create.InitCLI, " --values "+CreateValuesFile) || !strings.Contains(p.Create.ValuesFile, "version: \"16\"") {
		t.Fatalf("%+v", p.Create) // a string stays a string in the replay
	}
}

func TestCreateAppendsAcceptedOffering(t *testing.T) {
	e := newCreateEnv(t, nil)
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
	if tags, _ := e.reg.Tags(t.Context(), e.host+"/acme/invoices"); len(tags) != 0 {
		t.Fatalf("written: %v", tags)
	}
}

// TestCreateConsumablePartialNote: a partial read still plans what it
// lists, and says it is partial.
func TestCreateConsumablePartialNote(t *testing.T) {
	e := newCreateEnv(t, nil)
	req := e.createReq(t)
	req.Create.ConsumablePartial = true
	p, _ := e.plan(t, req)
	if p.Create.ConsumableCompleteness != "partial" || !slices.Contains(p.Notes, partialNote) {
		t.Fatalf("%+v", p)
	}
	if p, _ := e.plan(t, e.createReq(t)); slices.Contains(p.Notes, partialNote) {
		t.Fatalf("a complete read noted partial: %v", p.Notes)
	}
}

func TestCreateOfferingWithoutConfiguration(t *testing.T) {
	e := newCreateEnv(t, nil)
	req := e.createReq(t, OfferingPick{Offering: "queue", Values: map[string]any{"x": 1}})
	req.Create.Consumable["queue"] = CreateOffering{Repository: e.host + "/acme/queue"}
	if _, de := e.planRefused(t, req); de.Code != codeParameterNotDeclared || de.Refusals[0].Field != "offerings[queue]" {
		t.Fatalf("%+v", de)
	}
}

// goldenRef is the go-http golden path whose database references ref,
// selecting provider configuration sel with values.
func (e *createEnv) goldenRef(t *testing.T, ref, sel, values string) {
	t.Helper()
	golden := map[string]string{"pacto.yaml": "pactoVersion: \"2.4\"\nservice:\n  name: go-http\n  version: 1.2.0\n  owner: {team: platform}\n  template: {}\n" +
		"configurations:\n  - name: database\n    ref: " + ref + "\n    configuration: " + sel + "\n    values: " + values + "\n    required: true\n"}
	e.path = "oci://" + e.host + "/acme/paths/go-http@" + e.publishBundle(t, e.host+"/acme/paths/go-http", "3.0.0", golden, nil)
}

// pgProvider publishes a postgresql provider at repo and version whose
// required provisioning caps storageGi at max, beside an optional tuning.
func (e *createEnv) pgProvider(t *testing.T, repo, version string, maxGi int) {
	t.Helper()
	e.publishBundle(t, repo, version, map[string]string{
		"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: " + path.Base(repo) + "\n  version: " + version + "\n  offering: postgresql\n" +
			"configurations:\n  - name: provisioning\n    schema: p.json\n    required: true\n  - name: tuning\n    schema: t.json\n",
		"p.json": fmt.Sprintf(`{"type":"object","additionalProperties":false,"properties":{"storageGi":{"type":"integer","maximum":%d,"x-pacto-tier":"tunable"}}}`, maxGi),
		"t.json": `{"type":"object","additionalProperties":false,"properties":{"maxConnections":{"type":"integer","maximum":200,"x-pacto-tier":"tunable"}}}`,
	}, nil)
}

// TestCreateChecksTheSchemaTheRefSelects: values are checked against the
// provider configuration and revision the contract references, whichever
// consumable provider it is, never another configuration's or revision's.
func TestCreateChecksTheSchemaTheRefSelects(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.pgProvider(t, e.host+"/acme/pg-b", "1.0.0", 1000)
	e.pgProvider(t, e.host+"/acme/postgresql", "16.0.0", 100)
	// An optional provider configuration, named by the selector.
	e.goldenRef(t, "oci://"+e.host+"/acme/pg-b:1.0.0", "tuning", "{maxConnections: 100}")
	req := e.createReq(t)
	req.Create.Consumable["postgresql"] = CreateOffering{Repository: e.host + "/acme/postgresql", Repositories: []string{e.host + "/acme/postgresql", e.host + "/acme/pg-b"}}
	e.plan(t, req) // pg-b is a consumable provider too
	req.Create.Offerings = []OfferingPick{{Offering: "postgresql", Values: map[string]any{"maxConnections": 500}}}
	if _, de := e.planRefused(t, req); de.Code != codeParameterOutOfRange || de.Refusals[0].Field != "configurations[database].values.maxConnections" {
		t.Fatalf("checked against another configuration: %+v", de)
	}
	// An older revision of the offering keeps its own bound.
	e.goldenRef(t, "oci://"+e.host+"/acme/postgresql:16.0.0", "provisioning", "{storageGi: 50}")
	req = e.createReq(t, OfferingPick{Offering: "postgresql", Values: map[string]any{"storageGi": 300}})
	if _, de := e.planRefused(t, req); de.Code != codeParameterOutOfRange {
		t.Fatalf("checked against the newest revision: %+v", de)
	}
	// A provider that is not consumable is refused.
	req = e.createReq(t)
	e.goldenRef(t, "oci://"+e.host+"/acme/pg-b:1.0.0", "tuning", "{maxConnections: 100}")
	req.Create.Path = e.path
	if _, de := e.planRefused(t, req); de.Code != codeOfferingNotConsumable {
		t.Fatalf("%+v", de)
	}
}

// TestCreateLeavesSchemaLibraryRefs: a configuration whose ref reaches no
// offering (a shared schema library) is not judged as an offering.
func TestCreateLeavesSchemaLibraryRefs(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.publishBundle(t, e.host+"/acme/schemas/logging", "1.0.0", map[string]string{
		"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: logging\n  version: 1.0.0\n" +
			"configurations:\n  - name: standard\n    schema: s.json\n",
		"s.json": `{"type":"object","properties":{"level":{"type":"string"}}}`,
	}, nil)
	e.goldenRef(t, "oci://"+e.host+"/acme/schemas/logging:1.0.0", "standard", "{level: info}")
	e.plan(t, e.createReq(t))
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
	want := `pacto init invoices --from oci://r.io/p@sha256:1 --owner 'bill'\''; rm -rf / $(x)next' --registry r.io/acme --values ` + CreateValuesFile
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
	if !slices.ContainsFunc(p.Notes, func(n string) bool {
		return strings.Contains(n, "nobody can be confirmed to create it until a verified owner policy is read")
	}) ||
		slices.ContainsFunc(p.Notes, func(n string) bool { return strings.Contains(n, "billing members") }) || len(p.MayAccept) != 0 {
		t.Fatalf("an unverifiable membership is never named as a right: %v %v", p.Notes, p.MayAccept)
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
	if cr == nil || !strings.HasPrefix(cr.Index, "indexed as "+index+":") || !cr.Indexed ||
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
	if !res.Executed || res.Tag != firstDeclaration || !strings.HasPrefix(res.Created.Index, "not indexed in ") || res.Created.Indexed {
		t.Fatalf("%+v", res.Created)
	}
	for _, r := range opened {
		if r != e.host+"/acme/invoices" && r != e.host+"/acme/pacto-index" {
			t.Errorf("opened %s", r) // only the repository and the index
		}
	}
}

// agentDraft writes an agent's created draft of invoices on behalf of who,
// reviewed at the dashboard's golden path page.
func (e *createEnv) agentDraft(t *testing.T, who string, picks ...OfferingPick) *DeclareResult {
	t.Helper()
	req := e.createReq(t, picks...)
	req.Agent, req.OnBehalfOf, req.Create.Caller, req.Create.ReviewURL = true, who, who, reviewURL
	return e.declare(t, req)
}

const reviewURL = "https://dash.example/#/fleet/services/go-http"

// accept is who's request accepting the created draft tag.
func (e *createEnv) accept(t *testing.T, who, tag string) DeclareRequest {
	req := e.createReq(t)
	req.Create.Caller, req.FromDraft = who, tag
	return req
}

func TestCreateAgentWritesDraft(t *testing.T) {
	e := newCreateEnv(t, nil)
	res := e.agentDraft(t, "ana@acme.com")
	if !res.Draft || res.Tag != "draft-1" || res.Plan.AcceptCLI != "" || res.Created != nil ||
		res.Plan.AcceptURL != reviewURL+"?create=invoices&draft=draft-1" {
		t.Fatalf("%+v", res)
	}
	if tags, _ := e.reg.Tags(t.Context(), e.host+"/acme/invoices"); slices.ContainsFunc(tags, func(tg string) bool { return tg == firstDeclaration }) {
		t.Fatalf("an agent wrote a version: %v", tags)
	}
	_, rec := e.pulled(t, "draft-1")
	if d := rec.Declaration; d.State != contract.DeclarationDraft || d.OnBehalfOf != "ana@acme.com" || d.InferredFrom != "" || rec.Inputs.Cluster != nil {
		t.Fatalf("%s", rec.Marshal())
	}
	// A repository other than the registry's default is named; no review
	// page, no URL.
	req := e.createReq(t)
	req.Agent, req.OnBehalfOf, req.Create.ReviewURL, req.Create.Repository = true, "ana@acme.com", reviewURL, e.host+"/acme/team/invoices"
	if got := e.declare(t, req).Plan.AcceptURL; got != reviewURL+"?create=invoices&draft=draft-1&repository="+url.QueryEscape(e.host+"/acme/team/invoices") {
		t.Fatal(got)
	}
	req.Create.ReviewURL = ""
	if got := e.declare(t, req); got.Tag != "draft-2" || got.Plan.AcceptURL != "" {
		t.Fatalf("%+v", got.Plan)
	}
}

// TestCreatedDraftIsNotAVersion: the fleet's one declaration predicate
// leaves a created draft out.
func TestCreatedDraftIsNotAVersion(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.agentDraft(t, "ana@acme.com")
	_, rec := e.pulled(t, "draft-1")
	if _, lim, ok := fleet.DeclarationAccepted(nil, rec, nil); ok || lim.Code != fleet.LimitationDeclarationDraftIgnored {
		t.Fatalf("%+v %v", lim, ok)
	}
}

// TestCreatedDraftAccepted: the person's acceptance regenerates the draft
// from its own values, signs it and cites the draft.
func TestCreatedDraftAccepted(t *testing.T) {
	e := newCreateEnv(t, nil)
	draft := e.agentDraft(t, "ana@acme.com",
		OfferingPick{Offering: "postgresql", Values: map[string]any{"storageGi": 40}, Environments: map[string]map[string]any{"prod": {"storageGi": 60}}},
		OfferingPick{Offering: "redis", Values: map[string]any{"memoryMi": 128}})
	e.withOwnerPolicy(t)
	e.login(personToken("ana@acme.com"))
	res := e.declare(t, e.accept(t, "ana@acme.com", "draft-1"))
	if res.Draft || res.Tag != firstDeclaration || res.Created == nil || res.Plan.AcceptCLI != "" ||
		!slices.Contains(res.Plan.Notes, "a created service: every value comes from the golden path "+e.path+" and the values the agent set; review pacto.yaml") ||
		!slices.Contains(res.Plan.Notes, "the draft's signer names no identity Pacto can check: its proposer is asserted") {
		t.Fatalf("%+v", res.Plan)
	}
	b, rec := e.pulled(t, firstDeclaration)
	d := rec.Declaration
	if d.State != contract.DeclarationDeclared || d.Asker.Subject != "ana@acme.com" || d.Draft == nil || d.Draft.Tag != "draft-1" ||
		d.Draft.Digest != draft.Published || d.Draft.OnBehalfOf != "ana@acme.com" {
		t.Fatalf("%s", rec.Marshal())
	}
	if raw := string(b.RawYAML); !strings.Contains(raw, "storageGi: 40") || !strings.Contains(raw, "memoryMi: 128") {
		t.Fatalf("%s", raw)
	}
}

// TestCreatedDraftExtraContentRefused: a hand-pushed draft holding anything
// its golden path and values do not produce is refused.
func TestCreatedDraftExtraContentRefused(t *testing.T) {
	for name, mutate := range map[string]func(doc map[string]any, files map[string]string){
		"dependency": func(doc map[string]any, _ map[string]string) {
			doc["dependencies"] = []any{map[string]any{"name": "ledger", "ref": "oci://r.io/ledger:1.0.0", "compatibility": "^1.0.0"}}
		},
		"interface": func(doc map[string]any, _ map[string]string) {
			doc["interfaces"] = []any{map[string]any{"name": "admin", "type": "http", "port": 9000}}
		},
		"publish": func(doc map[string]any, _ map[string]string) {
			doc["publish"] = map[string]any{"bundle": "oci://elsewhere.io/invoices"}
		},
		"extra file": func(_ map[string]any, files map[string]string) { files["docs/extra.md"] = "# extra\n" },
	} {
		t.Run(name, func(t *testing.T) {
			e := newCreateEnv(t, nil)
			e.agentDraft(t, "ana@acme.com")
			e.pushEdited(t, "draft-2", mutate)
			e.login(personToken("ana@acme.com"))
			_, de := e.planRefused(t, e.accept(t, "ana@acme.com", "draft-2"))
			if want := e.host + "/acme/invoices:draft-2 holds content its golden path and values do not produce: create the service again"; de.Code != codeDraftNotFound || de.Message != want {
				t.Fatalf("%+v", de)
			}
		})
	}
}

// pushEdited pushes draft-1's files, edited by mutate, as tag.
func (e *createEnv) pushEdited(t *testing.T, tag string, mutate func(doc map[string]any, files map[string]string)) {
	t.Helper()
	b, _ := e.pulled(t, "draft-1")
	files := walkBundle(t, b)
	var doc map[string]any
	if err := contract.DecodeYAML([]byte(files[DefaultContractPath]), &doc); err != nil {
		t.Fatal(err)
	}
	mutate(doc, files)
	raw, _ := yaml.Marshal(doc)
	files[DefaultContractPath] = string(raw)
	e.publishBundle(t, e.host+"/acme/invoices", tag, files, nil)
}

// TestCreatedDraftAcceptRechecksValues: the values are judged again against
// the offering's schema and the person's consumable read.
func TestCreatedDraftAcceptRechecksValues(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.agentDraft(t, "ana@acme.com", OfferingPick{Offering: "postgresql", Values: map[string]any{"storageGi": 400}})
	e.pushEdited(t, "draft-2", func(doc map[string]any, _ map[string]string) {
		doc["configurations"].([]any)[0].(map[string]any)["values"].(map[string]any)["storageGi"] = 900
	})
	e.login(personToken("ana@acme.com"))
	req := e.accept(t, "ana@acme.com", "draft-2")
	_, de := e.planRefused(t, req)
	if de.Code != codeParameterOutOfRange || len(de.Refusals) != 1 || de.Refusals[0].Field != "configurations[database].values.storageGi" {
		t.Fatalf("%+v", de)
	}
	// An offering the person may not consume is refused the same way.
	req = e.accept(t, "ana@acme.com", "draft-1")
	delete(req.Create.Consumable, "postgresql")
	if _, de := e.planRefused(t, req); de.Code != codeOfferingNotConsumable {
		t.Fatalf("%+v", de)
	}
}

func TestCreatedDraftAcceptRechecksPath(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.agentDraft(t, "ana@acme.com")
	req := e.accept(t, "ana@acme.com", "draft-1")
	req.Create.Path = "oci://" + e.host + "/acme/paths/go-http@" + e.publishBundle(t, e.host+"/acme/paths/go-http", "1.3.0", goldenHTTP(e.host, "2.5", ""), nil)
	if _, de := e.planRefused(t, req); de.Code != codeGoldenPathNotDeclared || strings.Contains(de.Message, "sha256:") {
		t.Fatalf("%+v", de)
	}
}

// TestCreatedDraftAcceptChecksOwner: the person who signs must belong to
// the owner team the draft names.
func TestCreatedDraftAcceptChecksOwner(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.agentDraft(t, "eve@acme.com")
	e.withOwnerPolicy(t)
	e.login(personToken("eve@acme.com"))
	_, err := e.svc.Declare(t.Context(), e.accept(t, "eve@acme.com", "draft-1"))
	if de := refusal(t, err); de.Code != "OWNER_SIGNER_NOT_ALLOWED" {
		t.Fatalf("%+v", de)
	}
	if tags := e.tags(t); slices.Contains(tags, firstDeclaration) {
		t.Fatalf("written: %v", tags)
	}
}

func TestCreatedDraftAcceptRefusals(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.agentDraft(t, "ana@acme.com")
	// An agent never accepts a draft.
	agent := e.accept(t, "ana@acme.com", "draft-1")
	agent.Agent = true
	if _, de := e.planRefused(t, agent); de.Code != codeSignerNotAllowed {
		t.Fatalf("%+v", de)
	}
	e.login(personToken("ana@acme.com"))
	req := e.accept(t, "ana@acme.com", "draft-9")
	if _, de := e.planRefused(t, req); de.Code != codeDraftNotFound || !strings.HasPrefix(de.CLI, "pacto init invoices") {
		t.Fatalf("%+v", de)
	}
	req.Create.Repository = "elsewhere.io/invoices"
	if _, de := e.planRefused(t, req); de.Code != codeRepositoryOutsideRegistry {
		t.Fatalf("%+v", de)
	}
	// Another person's draft: the name is taken.
	if _, de := e.planRefused(t, e.accept(t, "bob@acme.com", "draft-1")); de.Code != codeServiceExists {
		t.Fatalf("%+v", de)
	}
	// An environment file the draft declares but does not hold.
	e.pushEdited(t, "draft-2", func(doc map[string]any, _ map[string]string) {
		doc["environments"] = []any{map[string]any{"name": "prod", "file": "environments/missing.yaml"}}
	})
	if _, de := e.planRefused(t, e.accept(t, "ana@acme.com", "draft-2")); de.Code != codeDraftNotFound {
		t.Fatalf("%+v", de)
	}
}

// TestCreateAcceptsOnlyCreatedDraft: a create never accepts a cluster draft,
// and a run that is no create never accepts a created draft.
func TestCreateAcceptsOnlyCreatedDraft(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.declareEnv.agentDraft(t)
	e.login(personToken("ana@acme.com"))
	want := e.host + "/acme/invoices:draft-1 holds no created draft of invoices"
	if _, de := e.planRefused(t, e.accept(t, "ana@acme.com", "draft-1")); de.Code != codeDraftNotFound || de.Message != want {
		t.Fatalf("%+v", de)
	}
}

func TestCLIDeclareRefusesCreatedDraft(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.agentDraft(t, "ana@acme.com")
	e.login(personToken("ana@acme.com"))
	_, de := e.planRefused(t, e.fromDraft("draft-1"))
	want := e.host + "/acme/invoices:draft-1 is a created-service draft: review and sign it in the dashboard's Create service, or create the service yourself with pacto init --from"
	if de.Code != codeDraftNotFound || de.Message != want {
		t.Fatalf("%+v", de)
	}
}

func TestDraftPicks(t *testing.T) {
	raw := "pactoVersion: \"2.5\"\nservice: {name: invoices, version: 0.1.0}\nconfigurations:\n" +
		"  - {name: local, schema: s.json}\n" +
		"  - {name: empty, ref: oci://r.io/acme/postgresql:17.0.0}\n" +
		"  - {name: a, ref: oci://r.io/acme/postgresql:17.0.0, values: {storageGi: 20}}\n" +
		"  - {name: b, ref: oci://r.io/acme/postgresql:17.0.0}\n" +
		"  - {name: c, ref: oci://r.io/acme/other:1.0.0, values: {x: 1}}\n" +
		"environments:\n  - {name: prod, file: prod.yaml}\n"
	b := &contract.Bundle{RawYAML: []byte(raw), FS: fstest.MapFS{"prod.yaml": {Data: []byte("a: {storageGi: 30}\nb: {class: large}\n")}}}
	got, err := draftPicks(b, map[string]CreateOffering{"postgresql": {Repository: "r.io/acme/postgresql"}})
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(got)
	// A ref to no consumable offering is the golden path's own or content
	// it never had, never a pick.
	if want := `[{"offering":"postgresql","values":{"storageGi":20},"environments":{"prod":{"class":"large","storageGi":30}}}]`; string(blob) != want {
		t.Fatal(string(blob))
	}
}

func TestCreatedDraftSameFiles(t *testing.T) {
	base := fstest.MapFS{"pacto.yaml": {Data: []byte("a: 1\nb: [x]\n")}, contract.InferredRecordFile: {Data: []byte("r")}, "docs/x.md": {Data: []byte("x")}}
	for name, c := range map[string]struct {
		other fstest.MapFS
		same  bool
	}{
		"re-marshalled": {fstest.MapFS{"pacto.yaml": {Data: []byte("b:\n  - x\na: 1\n")}, contract.InferredRecordFile: {Data: []byte("other")}, "docs/x.md": {Data: []byte("x")}}, true},
		"value":         {fstest.MapFS{"pacto.yaml": {Data: []byte("a: 2\nb: [x]\n")}, "docs/x.md": {Data: []byte("x")}}, false},
		"renamed":       {fstest.MapFS{"pacto.yaml": {Data: []byte("a: 1\nb: [x]\n")}, "docs/y.md": {Data: []byte("x")}}, false},
		"extra":         {fstest.MapFS{"pacto.yaml": {Data: []byte("a: 1\nb: [x]\n")}, "docs/x.md": {Data: []byte("x")}, "z": {Data: []byte("z")}}, false},
		"not yaml":      {fstest.MapFS{"pacto.yaml": {Data: []byte("a: 1\nb: [x]\n")}, "docs/x.md": {Data: []byte(": [")}}, false},
	} {
		if got := sameFiles(base, c.other); got != c.same {
			t.Errorf("%s: %v", name, got)
		}
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
	if r := createRefusal(validationRefusal("values", nil), 0, "db", "pg", "pacto init"); r.Field != "configurations[db]" || r.CLI != "" {
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
	d := &createDoc{doc: map[string]any{
		"dependencies":   []any{map[string]any{"name": "redis"}},
		"configurations": []any{map[string]any{"name": "local", "schema": "s.json"}, "not a map", map[string]any{"name": "db", "ref": "oci://r.io/pg:1", "configuration": "other", "values": map[string]any{"size": 1}}},
		"environments":   []any{map[string]any{"name": "dev"}, "not a map"},
	}, envFiles: map[string]map[string]any{"dev": {}}, c: CreateFrom{Consumable: map[string]CreateOffering{
		"pg": {Repository: "r.io/pg"},
	}}}
	// A dependency of the snippet's name is kept; a configuration with no
	// ref is never an offering's, nor is a ref that reaches no offering.
	d.appendOffering(CreateOffering{Snippet: &fleet.ConsumeSnippet{Entries: fleet.ConsumeEntries{Dependency: &contract.Dependency{Name: "redis"}}}})
	d.appendOffering(CreateOffering{})
	d.check(validation.ConfigRefs{}, "pacto init")
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

// TestCreatedServiceListedByWholeIndex: after the publish, the whole-index
// read of the installation index lists the created service at its first
// version, and a fleet over that index shows it declared with no target.
func TestCreatedServiceListedByWholeIndex(t *testing.T) {
	e := newCreateEnv(t, nil)
	t.Setenv("PACTO_INSECURE_REGISTRIES", e.host)
	e.withOwnerPolicy(t)
	e.login(personToken("ana@acme.com"))
	e.declare(t, e.createReq(t))
	index := e.host + "/acme/" + orgindex.RepositoryName
	svc := NewService(oci.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), oci.WithInsecureRegistries(e.host)), nil)
	svc.sys.refusalRepo = anonRepo
	coll := collectWhole(t, &indexFleetSource{svc: svc, repo: index})
	if v := versions(coll); v["invoices"] != firstDeclaration {
		t.Fatalf("%v", v)
	}
	oldRekor := rekorURL
	t.Cleanup(func() { rekorURL = oldRekor })
	rekorURL = ""
	pc := newProvenanceChecker(publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), e.host))
	pc.root, pc.rootAt = e.ss.Root, time.Now()
	trustingIn(pc, Installation{Signers: []delivery.PersonSigner{{Issuer: personIssuer, Subjects: `.+@acme\.com`}}}, nil)
	snap, err := svc.Fleet(t.Context(), FleetOptions{ReadIndex: index, Provenance: pc})
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	view, err := q.GetService("invoices")
	if err != nil || len(view.Revisions) != 1 || len(view.Targets) != 0 || view.Revisions[0].Version != firstDeclaration {
		t.Fatalf("%v %+v", err, view)
	}
	detail, err := q.EntityDetail(fleet.KindRevision, string(view.Revisions[0].Key))
	if err != nil {
		t.Fatal(err)
	}
	if d := detail.Revision.Declaration; d == nil || d.State != contract.DeclarationDeclared || !d.SignatureChecked || d.InferredFrom != "" {
		t.Fatalf("%+v %+v", d, snap.Limitations)
	}
}

// TestInitCLIRecreatesPlannedContract: the planned InitCLI run locally with
// the values file recreates the planned dependencies, configurations and
// publish bundle.
func TestInitCLIRecreatesPlannedContract(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.path = "oci://" + e.host + "/acme/paths/go-http@" + e.publishBundle(t, e.host+"/acme/paths/go-http", "1.5.0",
		goldenHTTP(e.host, "2.5", "publish:\n  bundle: oci://registry.example/go-http\n"), nil)
	e.publishBundle(t, e.host+"/acme/redis", "7.0.0", map[string]string{
		"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: redis\n  version: 7.0.0\n  offering: redis\n" +
			"configurations:\n  - name: provisioning\n    schema: configuration/provisioning.schema.json\n    required: true\n",
		"configuration/provisioning.schema.json": `{"type":"object","additionalProperties":false,"properties":{"memoryMi":{"type":"integer","minimum":64}}}`,
	}, nil)
	p, d := e.plan(t, e.createReq(t,
		OfferingPick{Offering: "postgresql", Values: map[string]any{"storageGi": 40}},
		OfferingPick{Offering: "redis", Values: map[string]any{"memoryMi": 128}}))
	c := p.Create
	if !strings.HasSuffix(c.InitCLI, " --values "+CreateValuesFile) || c.ValuesFile == "" {
		t.Fatalf("%+v", c)
	}
	dir := t.TempDir()
	values := filepath.Join(dir, CreateValuesFile)
	if err := os.WriteFile(values, []byte(c.ValuesFile), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "invoices")
	if _, err := e.svc.InitFrom(t.Context(), InitOptions{Dir: target, Name: "invoices", Owner: "billing", Registry: e.host + "/acme"},
		InitFrom{From: e.path, Overrides: override.Overrides{ValueFiles: []string{values}}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(target, DefaultContractPath))
	if err != nil {
		t.Fatal(err)
	}
	local, err := contract.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	planned := d.bundle.Contract
	if local.Publish == nil || planned.Publish == nil {
		t.Fatalf("no publish section: %+v %+v", local.Publish, planned.Publish)
	}
	for name, pair := range map[string][2]any{
		"dependencies":   {local.Dependencies, planned.Dependencies},
		"configurations": {local.Configurations, planned.Configurations},
		"publish":        {local.Publish, planned.Publish},
		"publish.bundle": {local.Publish.Bundle, d.input.Publish.Bundle},
	} {
		a, _ := json.Marshal(pair[0])
		b, _ := json.Marshal(pair[1])
		if string(a) != string(b) {
			t.Errorf("%s:\nlocal   %s\nplanned %s", name, a, b)
		}
	}
}

// TestCreatePublishRefusesOtherSigner: a login whose certificate names
// another person than its token stops before any tag.
func TestCreatePublishRefusesOtherSigner(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.withOwnerPolicy(t)
	e.login(testutil.TokenWith(map[string]string{"sub": "mallory@acme.com", "email": "ana@acme.com", "email_verified": "true", "iss": personIssuer}))
	_, err := e.svc.Declare(t.Context(), e.createReq(t))
	if de := refusal(t, err); de.Code != codeSignerUnverified || len(e.tags(t)) != 0 {
		t.Fatal(err, e.tags(t))
	}
}

// TestCreateRefusesUnverifiedPathWithTemplateKey: with a template key
// configured, an unsigned golden path is refused.
func TestCreateRefusesUnverifiedPathWithTemplateKey(t *testing.T) {
	e := newCreateEnv(t, nil)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	req := e.createReq(t)
	req.Create.Trust = &delivery.TrustConfig{Keys: []delivery.TrustedKey{templateKey(t, "platform", pub)}}
	p, _, err := e.svc.PlanDeclare(t.Context(), req)
	if err == nil || !strings.Contains(err.Error(), "no template key the trust config names verifies its signature") || (p != nil && p.IntentDigest != "") {
		t.Fatalf("planned %+v: %v", p, err)
	}
	if len(e.tags(t)) != 0 {
		t.Fatal(e.tags(t))
	}
}

// TestCreateBundleHoldsNoSensitiveValue: a sensitive value is refused at
// its field with nothing written, and no answer echoes the literal.
func TestCreateBundleHoldsNoSensitiveValue(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.withOwnerPolicy(t)
	e.login(personToken("ana@acme.com"))
	req := e.createReq(t, OfferingPick{Offering: "postgresql", Values: map[string]any{"dbPassword": "hunter2"}})
	res, err := e.svc.Declare(t.Context(), req)
	de := refusal(t, err)
	if len(de.Refusals) != 1 || de.Refusals[0].Code != codeParameterSensitive || de.Refusals[0].Field != "configurations[database].values.dbPassword" {
		t.Fatalf("%+v", de.Refusals)
	}
	answer, _ := json.Marshal(struct {
		E *DeclareError
		R *DeclareResult
	}{de, res})
	if strings.Contains(string(answer), "hunter2") || strings.Contains(de.Error(), "hunter2") || len(e.tags(t)) != 0 {
		t.Fatalf("%s %v", answer, e.tags(t))
	}
}

// TestCreatedRecordHasNoInferredFrom: a created record names no workload
// and no cluster, and is a valid declaration.
func TestCreatedRecordHasNoInferredFrom(t *testing.T) {
	e := newCreateEnv(t, nil)
	_, d := e.plan(t, e.createReq(t))
	rec := d.record
	if rec.Declaration == nil || rec.Declaration.InferredFrom != "" || rec.Declaration.Cluster != "" || rec.Inputs.Cluster != nil || rec.Inputs.Discovery == "" {
		t.Fatalf("%s", rec.Marshal())
	}
	if err := validDeclaration(d.bundle); err != nil {
		t.Fatal(err)
	}
}

// TestCreateSkipsWorkloadRead: a create never reads a workload; the only
// cluster reads are the installation's policy ConfigMaps.
func TestCreateSkipsWorkloadRead(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.dyn.PrependReactor("*", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if r := a.GetResource().Resource; r != "configmaps" {
			t.Errorf("a create read %s %s", a.GetVerb(), r)
		}
		return false, nil, nil
	})
	if p, _ := e.plan(t, e.createReq(t)); !slices.ContainsFunc(p.RepositoryTried, func(r RungNote) bool { return r.Reason == noWorkloadYet }) {
		t.Fatalf("%+v", p.RepositoryTried)
	}
}

// TestCreateSignatureUnknownWithoutTemplateKey: with no template key the
// path's signature step reads Unknown, never verified, and still plans.
func TestCreateSignatureUnknownWithoutTemplateKey(t *testing.T) {
	e := newCreateEnv(t, nil)
	for _, trust := range []*delivery.TrustConfig{nil, {}} {
		req := e.createReq(t)
		req.Create.Trust = trust
		p, _ := e.plan(t, req)
		if sig := p.Create.Signature; sig.Status != fleet.StepStatusUnknown || sig.Detail != noTemplateKey || p.IntentDigest == "" {
			t.Fatalf("%+v", sig)
		}
	}
}

// TestCreateServiceExists: a version already in the repository refuses
// ServiceExists before any login, and writes nothing.
func TestCreateServiceExists(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.publishBundle(t, e.host+"/acme/invoices", "1.0.0", e.invoicesBaseline("1.0.0", ""), nil)
	logins := e.login(personToken("ana@acme.com"))
	_, err := e.svc.Declare(t.Context(), e.createReq(t))
	if de := refusal(t, err); de.Code != codeServiceExists || *logins != 0 {
		t.Fatalf("%+v logins=%d", de, *logins)
	}
	if tags := e.tags(t); !slices.Equal(tags, []string{"1.0.0"}) {
		t.Fatalf("written: %v", tags)
	}
}

// TestRefusedCreateHasNoIntent: whatever refuses a create, the plan holds
// no intent digest a caller could execute.
func TestRefusedCreateHasNoIntent(t *testing.T) {
	e := newCreateEnv(t, nil)
	for name, req := range map[string]DeclareRequest{
		"out of range":   e.createReq(t, OfferingPick{Offering: "postgresql", Values: map[string]any{"storageGi": 900}}),
		"sensitive":      e.createReq(t, OfferingPick{Offering: "postgresql", Values: map[string]any{"dbPassword": "x"}}),
		"not consumable": e.createReq(t, OfferingPick{Offering: "mysql"}),
		"environment":    e.createReq(t, OfferingPick{Offering: "postgresql", Environments: map[string]map[string]any{"qa": {"storageGi": 20}}}),
	} {
		if p, de := e.planRefused(t, req); p == nil || p.IntentDigest != "" || len(de.Refusals) == 0 {
			t.Errorf("%s: %+v %+v", name, p, de)
		}
	}
}

// TestEnvironmentNotDeclared: a value for an environment the golden path
// does not declare is refused at that environment.
func TestEnvironmentNotDeclared(t *testing.T) {
	e := newCreateEnv(t, nil)
	_, de := e.planRefused(t, e.createReq(t, OfferingPick{Offering: "postgresql", Environments: map[string]map[string]any{"staging": {"storageGi": 20}}}))
	if len(de.Refusals) != 1 || de.Code != codeEnvironmentNotDeclared {
		t.Fatalf("%+v", de)
	}
	if r := de.Refusals[0]; r.Code != codeEnvironmentNotDeclared || r.Field != "environments[staging]" || r.Message != "the golden path declares no environment staging" {
		t.Fatalf("%+v", r)
	}
}

// TestCreateKeepsStringTypedValue: a string that reads as a number or a
// bool stays a string in the bundle and in the InitCLI replay.
func TestCreateKeepsStringTypedValue(t *testing.T) {
	e := newCreateEnv(t, nil)
	for _, v := range []string{"16", "true"} {
		p, d := e.plan(t, e.createReq(t, OfferingPick{Offering: "postgresql", Values: map[string]any{"version": v}}))
		if got, ok := d.bundle.Contract.Configurations[0].Values["version"].(string); !ok || got != v {
			t.Fatalf("%q: %#v", v, d.bundle.Contract.Configurations[0].Values["version"])
		}
		if !strings.HasSuffix(p.Create.InitCLI, " --values "+CreateValuesFile) || !strings.Contains(p.Create.ValuesFile, "version: \""+v+"\"") {
			t.Fatalf("%q: %+v", v, p.Create)
		}
	}
}

// scopedWrites records the repositories a create writes to.
type scopedWrites struct {
	publish.Registry
	wrote *[]string
}

func (s scopedWrites) Write(ctx context.Context, repo string, a publish.Artifact) (string, error) {
	*s.wrote = append(*s.wrote, repo)
	return s.Registry.Write(ctx, repo, a)
}

func (s scopedWrites) Tag(ctx context.Context, repo, digest, tag string) error {
	*s.wrote = append(*s.wrote, repo)
	return s.Registry.Tag(ctx, repo, digest, tag)
}

func (s scopedWrites) PutSigTag(ctx context.Context, repo, digest string, art ocireferrer.Artifact) (string, error) {
	*s.wrote = append(*s.wrote, repo)
	return s.Registry.PutSigTag(ctx, repo, digest, art)
}

// TestCreatePushScopeHoldsRepoAndIndexOnly: a published create writes the
// chosen repository through the push client and opens only the repository
// and the installation index, where its entry lands.
func TestCreatePushScopeHoldsRepoAndIndexOnly(t *testing.T) {
	e := newCreateEnv(t, nil)
	t.Setenv("PACTO_INSECURE_REGISTRIES", e.host)
	e.withOwnerPolicy(t)
	e.login(personToken("ana@acme.com"))
	repo, index := e.host+"/acme/invoices", e.host+"/acme/"+orgindex.RepositoryName
	var wrote, opened []string
	req := e.createReq(t)
	req.Store = scopedWrites{e.reg, &wrote}
	req.Create.OpenIndex = func(r string) (*remote.Repository, error) {
		opened = append(opened, r)
		return anonRepo(r)
	}
	res := e.declare(t, req)
	if len(wrote) == 0 || len(opened) == 0 || !strings.HasPrefix(res.Created.Index, "indexed as "+index+":") {
		t.Fatalf("wrote %v opened %v %+v", wrote, opened, res.Created)
	}
	for _, r := range wrote {
		if r != repo {
			t.Errorf("wrote %s", r)
		}
	}
	for _, r := range opened {
		if r != repo && r != index {
			t.Errorf("opened %s", r)
		}
	}
}

// TestCreateIndexEntryWithNestedRepository: a repository nested under the
// registry is indexed in the registry's one installation index.
func TestCreateIndexEntryWithNestedRepository(t *testing.T) {
	e := newCreateEnv(t, nil)
	t.Setenv("PACTO_INSECURE_REGISTRIES", e.host)
	e.withOwnerPolicy(t)
	e.login(personToken("ana@acme.com"))
	index := e.host + "/acme/" + orgindex.RepositoryName
	var opened []string
	req := e.createReq(t)
	req.Create.Repository = e.host + "/acme/team/invoices"
	req.Create.OpenIndex = func(r string) (*remote.Repository, error) {
		opened = append(opened, r)
		return anonRepo(r)
	}
	res := e.declare(t, req)
	if !strings.HasPrefix(res.Created.Index, "indexed as "+index+":") || !slices.Contains(opened, index) || slices.Contains(opened, e.host+"/acme/team/"+orgindex.RepositoryName) {
		t.Fatalf("%+v %v", res.Created, opened)
	}
	svc := NewService(oci.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), oci.WithInsecureRegistries(e.host)), nil)
	svc.sys.refusalRepo = anonRepo
	coll := collectWhole(t, &indexFleetSource{svc: svc, repo: index})
	if len(coll.Revisions) != 1 || !strings.Contains(coll.Revisions[0].ResolvedRef+coll.Revisions[0].RequestedRef, "/acme/team/invoices") {
		t.Fatalf("%+v", coll.Revisions)
	}
}

// TestOfferingNotConsumableForInferred: an offering the fleet holds only as
// inferred never reaches the caller's consumable read, so a create cannot
// pick it, worded as for an offering that is not there.
func TestOfferingNotConsumableForInferred(t *testing.T) {
	e := newCreateEnv(t, nil)
	b, err := e.reg.Pull(t.Context(), e.host+"/acme/postgresql@"+e.pg)
	if err != nil {
		t.Fatal(err)
	}
	rev := fleet.RawRevision{Bundle: b, Domain: e.host + "/acme", RequestedRef: "oci://" + e.host + "/acme/postgresql:17.0.0", Digest: e.pg,
		Provenance: &fleet.BundleProvenance{Source: "oci", Signature: fleet.BundleSignature{Status: fleet.SignatureVerified, Source: "rule",
			Signer: "https://github.com/acme/pg/.github/workflows/publish.yml@refs/heads/main", Issuer: "https://token.actions.githubusercontent.com"}}}
	// consumable is the caller's read: the offerings the fleet lists as
	// consumable, with the inferred provider marked so when inferred.
	consumable := func(inferred bool) map[string]CreateOffering {
		snap, err := fleet.Build(t.Context(), fleet.BuildOptions{Now: time.Now}, fleet.NewMemorySource("oci", "oci", &fleet.Collection{Revisions: []fleet.RawRevision{rev}}))
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range snap.Services {
			if inferred && s.Name == "postgresql" {
				s.Declaration = fleet.DeclarationInferred // ponytail: as a contribution-backed service reads (pkg/fleet TestContributedOfferingNotConsumable)
			}
		}
		l, err := fleet.NewQuery(snap).Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindOffering}, Consumable: true})
		if err != nil {
			t.Fatal(err)
		}
		out, all := map[string]CreateOffering{}, e.consumable(t)
		for _, row := range l.Entities {
			if o, ok := all[row.Offering]; ok {
				out[row.Offering] = o
			}
		}
		return out
	}
	req := e.createReq(t)
	if req.Create.Consumable = consumable(false); len(req.Create.Consumable) != 1 {
		t.Fatalf("the accepted offering is not consumable: %v", req.Create.Consumable)
	}
	e.plan(t, req)
	req.Create.Consumable = consumable(true)
	_, de := e.planRefused(t, req)
	absent := e.createReq(t)
	delete(absent.Create.Consumable, "postgresql")
	_, de2 := e.planRefused(t, absent)
	if len(req.Create.Consumable) != 0 || de.Code != codeOfferingNotConsumable || de.Refusals[0].Message != de2.Refusals[0].Message {
		t.Fatalf("%v %+v %+v", req.Create.Consumable, de, de2)
	}
}

// TestCreateOwnDraftsPastTheListedFew: a caller with more of their own
// drafts than are listed still owns the name, and a draft that does not
// read is unknown, never someone else's.
func TestCreateOwnDraftsPastTheListedFew(t *testing.T) {
	e := newCreateEnv(t, nil)
	repo := e.host + "/acme/invoices"
	for i := 1; i <= MaxListedDrafts+1; i++ {
		e.draftOf(t, repo, fmt.Sprintf("draft-%d", i), "ana@acme.com")
	}
	p, _ := e.plan(t, e.createReq(t))
	if len(p.Drafts) != MaxListedDrafts || p.DraftsTotal != MaxListedDrafts+1 {
		t.Fatalf("%d of %d", len(p.Drafts), p.DraftsTotal)
	}
	e.publishBundle(t, repo, fmt.Sprintf("draft-%d", MaxListedDrafts+2), map[string]string{"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: invoices\n  version: 0.1.0\n"}, nil)
	if _, de := e.planRefused(t, e.createReq(t)); de.Code != codeRegistryUnreadable {
		t.Fatalf("an unreadable draft read as taken: %+v", de)
	}
}

// TestCreatedDraftWithValuelessOfferingAccepted: an offering the agent
// added with nothing set is added again when the draft is accepted.
func TestCreatedDraftWithValuelessOfferingAccepted(t *testing.T) {
	e := newCreateEnv(t, nil)
	e.agentDraft(t, "ana@acme.com", OfferingPick{Offering: "redis"})
	e.withOwnerPolicy(t)
	e.login(personToken("ana@acme.com"))
	if res := e.declare(t, e.accept(t, "ana@acme.com", "draft-1")); res.Tag != firstDeclaration {
		t.Fatalf("%+v", res)
	}
}

// TestCreateMergesNestedValues: a nested value set leaves its siblings as
// the golden path sets them, once and per environment; a pick with no
// values writes no empty values.
func TestCreateMergesNestedValues(t *testing.T) {
	cfg := map[string]any{"name": "db", "ref": "oci://r.io/pg:1", "values": map[string]any{"storage": map[string]any{"sizeGi": 20, "class": "ssd"}}}
	d := &createDoc{doc: map[string]any{"configurations": []any{cfg}}, envFiles: map[string]map[string]any{"prod": {"db": map[string]any{"storage": map[string]any{"sizeGi": 50, "class": "ssd"}}}},
		c: CreateFrom{Consumable: map[string]CreateOffering{"pg": {Repository: "r.io/pg"}}}}
	d.pick(OfferingPick{Offering: "pg", Values: map[string]any{"storage": map[string]any{"sizeGi": 30}},
		Environments: map[string]map[string]any{"prod": {"storage": map[string]any{"sizeGi": 60}}}})
	got := cfg["values"].(map[string]any)["storage"].(map[string]any)
	prod := d.envFiles["prod"]["db"].(map[string]any)["storage"].(map[string]any)
	if got["sizeGi"] != 30 || got["class"] != "ssd" || prod["sizeGi"] != 60 || prod["class"] != "ssd" {
		t.Fatalf("%v %v", got, prod)
	}
	bare := map[string]any{"name": "other", "ref": "oci://r.io/pg:1"}
	d.doc["configurations"] = []any{bare}
	d.pick(OfferingPick{Offering: "pg"})
	if _, ok := bare["values"]; ok {
		t.Fatalf("an empty values map was written: %v", bare)
	}
}

// TestCreateRefusalCLIRunsAsGiven: a refusal's pacto init fix names no
// values file, and an offering the golden path does not request gets none.
func TestCreateRefusalCLIRunsAsGiven(t *testing.T) {
	e := newCreateEnv(t, nil)
	_, de := e.planRefused(t, e.createReq(t, OfferingPick{Offering: "postgresql", Values: map[string]any{"storageGi": 900}},
		OfferingPick{Offering: "redis", Values: map[string]any{"memoryMi": 10}}))
	for _, r := range de.Refusals {
		switch r.Field {
		case "configurations[database].values.storageGi":
			if strings.Contains(r.CLI, "--values") || !strings.HasSuffix(r.CLI, " --set 'configurations[0].values.storageGi=500'") {
				t.Errorf("%+v", r)
			}
		case "configurations[cache].values.memoryMi":
			if r.CLI != "" || r.MCP == "" {
				t.Errorf("an appended offering's fix named pacto init: %+v", r)
			}
		default:
			t.Errorf("%+v", r)
		}
	}
	if len(de.Refusals) != 2 {
		t.Fatalf("%+v", de.Refusals)
	}
}
