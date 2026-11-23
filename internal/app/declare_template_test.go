package app

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const templateSubject = "template:default/springboot-service"

// springboot is the inferred golden path a configured catalog read.
func springboot() *fleet.InferredTemplate {
	at := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	return &fleet.InferredTemplate{Name: "springboot-service", Entity: templateSubject, Title: "Spring Boot service",
		Summary: "A Spring Boot service with Postgres", OwnerKey: "team:platform-team", From: "Backstage (catalog.acme.io)",
		URL: "https://catalog.acme.io/create/templates/default/springboot-service", Source: "backstage-catalog", ObservedAt: &at,
		Acceptable: true, Provenance: fleet.ProvenanceInferred}
}

// templateReq is a person's acceptance of springboot; the cluster client
// only reads the installation signer policy.
func (e *declareEnv) templateReq() DeclareRequest {
	return DeclareRequest{Service: "springboot-service", TemplateSubject: templateSubject, BackstageURL: "https://catalog.acme.io", Template: springboot(),
		Registry: e.host + "/acme", Dynamic: e.dyn, Signing: declareSigning{TrustedRoot: e.root, FulcioURL: e.ss.Fulcio, TimestampURL: e.ss.TSA}}
}

// springbootTags are the springboot-service repository's version and draft
// tags.
func (e *declareEnv) springbootTags(t *testing.T) []string {
	t.Helper()
	tags, _ := e.reg.Tags(t.Context(), e.host+"/acme/springboot-service")
	return slices.DeleteFunc(tags, func(tag string) bool {
		return strings.HasPrefix(tag, "pacto-sig-") || strings.HasPrefix(tag, "sha256-")
	})
}

func TestDeclareTemplatePlanDraftsGoldenPath(t *testing.T) {
	e := newDeclareEnv(t)
	p, d := e.plan(t, e.templateReq())
	if p.Service != "springboot-service" || p.Version != firstDeclaration || p.BundleRepo != e.host+"/acme/springboot-service" || p.InferredFrom != "" ||
		p.Cluster != "" || p.Owner != "" || p.State != contract.DeclarationDeclared {
		t.Fatalf("%+v", p)
	}
	c := d.bundle.Contract
	if c.Service.Template == nil || c.Service.Summary != "A Spring Boot service with Postgres" || !c.Service.Owner.IsEmpty() || len(c.Dependencies) != 0 ||
		d.record.Inputs.Cluster != nil || len(d.record.Fields) != 0 || d.record.Declaration.InferredFrom != "" {
		t.Fatalf("%s\n%s", d.raw, d.record.Marshal())
	}
	templatePlanReadsNoWorkload(t, e, p)
}

// templatePlanReadsNoWorkload checks a template plan's notes say where the
// template was read and name no cluster, that no rung read a workload, that
// the cluster was read for the signer policy alone and nothing was written.
func templatePlanReadsNoWorkload(t *testing.T, e *declareEnv, p *DeclarePlan) {
	t.Helper()
	for _, want := range []string{"drafted from " + templateSubject + " in Backstage (catalog.acme.io), read 2026-11-23T09:00:00Z",
		"the golden path holds the contract only: Backstage keeps the template's steps and skeleton; open it there to scaffold code"} {
		if !slices.Contains(p.Notes, want) {
			t.Fatalf("%q not in %v", want, p.Notes)
		}
	}
	if slices.ContainsFunc(p.Notes, func(n string) bool { return strings.Contains(n, "cluster") }) {
		t.Fatalf("cluster note in %v", p.Notes)
	}
	if !slices.ContainsFunc(p.RepositoryTried, func(r RungNote) bool { return r.Reason == templateReadsNoWorkload }) {
		t.Fatalf("%+v", p.RepositoryTried)
	}
	for _, a := range e.dyn.Actions() {
		if r := a.GetResource().Resource; r != "configmaps" {
			t.Fatalf("read %s", r)
		}
	}
	if len(e.springbootTags(t)) != 0 {
		t.Fatal("a plan wrote")
	}
}

// TestDeclareTemplateSummary: the title stands in for a missing
// description, cut to 200 characters; with neither, no summary.
func TestDeclareTemplateSummary(t *testing.T) {
	tpl := springboot()
	tpl.Summary, tpl.Title = "", strings.Repeat("é", 250)
	if got := string(templateContract(tpl)); !strings.Contains(got, "summary: '"+strings.Repeat("é", 200)+"'\n") {
		t.Fatal(got)
	}
	tpl.Title = ""
	if got := string(templateContract(tpl)); strings.Contains(got, "summary") {
		t.Fatal(got)
	}
}

func TestDeclareTemplatePublishesContractOnly(t *testing.T) {
	e := newDeclareEnv(t)
	e.login(personToken("ana@acme.com"))
	res := e.declare(t, e.templateReq())
	if !res.Executed || res.Draft || !res.Signed || res.Tag != firstDeclaration {
		t.Fatalf("%+v", res)
	}
	b, err := e.reg.Pull(t.Context(), e.host+"/acme/springboot-service:"+firstDeclaration)
	if err != nil {
		t.Fatal(err)
	}
	if names, _ := fs.Glob(b.FS, "*"); !slices.Equal(names, []string{"pacto.inferred.yaml", "pacto.yaml"}) {
		t.Fatalf("%v", names)
	}
	rec, err := contract.ReadInferredRecord(b.FS)
	want := contract.InferredSigner{Subject: "ana@acme.com", Issuer: personIssuer, Class: contract.SignerClassHuman}
	if err != nil || rec.Declaration.State != contract.DeclarationDeclared || rec.Declaration.Asker != want || b.Contract.Service.Template == nil {
		t.Fatalf("%v %s", err, rec.Marshal())
	}
	if got := e.signers(t, res.Published); len(got) != 1 || got[0].Subject != want.Subject {
		t.Fatalf("%+v", got)
	}
}

func TestDeclareTemplateKeyWritesDraft(t *testing.T) {
	e := newDeclareEnv(t)
	dir := t.TempDir()
	keyFiles(t, dir)
	req := e.templateReq()
	req.Signing.Key, req.OnBehalfOf = filepath.Join(dir, "cosign.key"), "ana"
	res := e.declare(t, req)
	want := "pacto declare " + templateSubject + " --backstage-url https://catalog.acme.io --registry " + e.host + "/acme --from-draft draft-1"
	if !res.Draft || !res.Signed || res.Tag != "draft-1" || res.Plan.AcceptCLI != want {
		t.Fatalf("%+v", res)
	}
}

func TestDeclareTemplateOwnerLeftAsNote(t *testing.T) {
	e := newDeclareEnv(t)
	p, d := e.plan(t, e.templateReq())
	if !slices.Contains(p.Notes, "Backstage names team:platform-team as owner: declare service.owner in a later version") ||
		strings.Contains(string(d.raw), "owner") || p.Owner != "" || p.MayAccept != nil {
		t.Fatalf("%v\n%s", p.Notes, d.raw)
	}
	req := e.templateReq()
	req.Template.OwnerKey = ""
	if p, _ := e.plan(t, req); slices.ContainsFunc(p.Notes, func(n string) bool { return strings.Contains(n, "as owner") }) {
		t.Fatalf("%v", p.Notes)
	}
}

// backstageStub serves body as every by-query page.
func backstageStub(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

const templatePage = `{"items":[{"apiVersion":"scaffolder.backstage.io/v1beta3","kind":"Template",` +
	`"metadata":{"name":"springboot-service","namespace":"default","title":"Spring Boot","description":"Spring"},"spec":{"owner":"group:default/platform-team"}}],"pageInfo":{}}`

func TestDeclareTemplateFoundInCatalog(t *testing.T) {
	got, err := (&Service{}).InferredTemplate(t.Context(), backstageStub(t, 200, templatePage), "", templateSubject)
	if err != nil || got.Name != "springboot-service" || got.Summary != "Spring" || got.OwnerKey != "team:platform-team" || !got.Acceptable {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestDeclareTemplateNotFound(t *testing.T) {
	_, err := (&Service{}).InferredTemplate(t.Context(), backstageStub(t, 200, templatePage), "", "template:default/other")
	if de := refusal(t, err); de.Code != codeTemplateNotFound || !strings.Contains(de.Message, "no template default/other in the catalog at 127.0.0.1") ||
		strings.Contains(de.Message, "partial") || !strings.HasPrefix(de.CLI, "pacto fleet entities --template --backstage-url http://127.0.0.1") {
		t.Fatalf("%+v", de)
	}
	// A partial read (a catalog that pages forever) says so.
	paging := strings.Replace(templatePage, `"pageInfo":{}`, `"pageInfo":{"nextCursor":"again"}`, 1)
	_, err = (&Service{}).InferredTemplate(t.Context(), backstageStub(t, 200, paging), "", "template:default/other")
	if de := refusal(t, err); de.Code != codeTemplateNotFound || !strings.Contains(de.Message, "the catalog read was partial") {
		t.Fatalf("%+v", de)
	}
}

func TestDeclareTemplateCatalogUnset(t *testing.T) {
	_, err := (&Service{}).InferredTemplate(t.Context(), "", "", templateSubject)
	if de := refusal(t, err); de.Code != codeTemplateCatalogUnset || !strings.Contains(de.Message, "pass --backstage-url") ||
		de.CLI != "pacto declare "+templateSubject+" --backstage-url <backstage-url> --registry <registry>" {
		t.Fatalf("%+v", de)
	}
	// A URL the source refuses: the token never travels in clear text.
	_, err = (&Service{}).InferredTemplate(t.Context(), "http://catalog.acme.io", "s3cret", templateSubject)
	if de := refusal(t, err); de.Code != codeTemplateCatalogUnset || strings.Contains(de.Message, "s3cret") {
		t.Fatalf("%+v", de)
	}
}

func TestDeclareTemplateNameInvalid(t *testing.T) {
	for _, s := range []string{"template:springboot-service", "template:/x", "template:default/", "springboot-service"} {
		if _, err := (&Service{}).InferredTemplate(t.Context(), "https://catalog.acme.io", "", s); refusal(t, err).Code != codeTemplateNameInvalid {
			t.Fatal(s, err)
		}
	}
	e := newDeclareEnv(t)
	req := e.templateReq()
	req.Template.Name = "Spring_Boot"
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); refusal(t, err).Code != codeTemplateNameInvalid {
		t.Fatal(err)
	}
}

func TestDeclareTemplateCatalogUnavailable(t *testing.T) {
	_, err := (&Service{}).InferredTemplate(t.Context(), backstageStub(t, 401, ""), "s3cret", templateSubject)
	if de := refusal(t, err); de.Code != codeTemplateCatalogUnavailable || !strings.HasPrefix(de.Message, "the catalog at 127.0.0.1") ||
		strings.Contains(de.Message, "s3cret") {
		t.Fatalf("%+v", de)
	}
}

// agentTemplate writes an unsigned agent draft accepting springboot on
// ana's behalf.
func (e *declareEnv) agentTemplate(t *testing.T) {
	t.Helper()
	req := e.templateReq()
	req.Agent, req.OnBehalfOf = true, "ana"
	if res := e.declare(t, req); res.Tag != "draft-1" {
		t.Fatalf("%+v", res)
	}
}

func TestDeclareTemplateFromDraftRereadsCatalog(t *testing.T) {
	e := newDeclareEnv(t)
	e.agentTemplate(t)
	req := e.templateReq()
	req.FromDraft = "draft-1"
	req.Template.Summary = "Read again"
	p, d := e.plan(t, req)
	if p.Service != "springboot-service" || p.State != contract.DeclarationDeclared || d.bundle.Contract.Service.Summary != "Read again" ||
		len(p.Unbacked) != 0 || p.UnbackedUnchecked != "" || d.record.Declaration.Draft == nil || d.record.Declaration.Draft.Tag != "draft-1" {
		t.Fatalf("%+v\n%s", p, d.raw)
	}
	e.login(personToken("ana@acme.com"))
	if res := e.declare(t, req); res.Tag != firstDeclaration || res.Draft {
		t.Fatalf("%+v", res)
	}
	// Gone from the catalog: the CLI's read finds nothing, so no template is set.
	req.Template = nil
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); refusal(t, err).Code != codeTemplateNotFound {
		t.Fatal(err)
	}
}

// TestDeclareTemplateFromDraftOtherSubject: a workload draft under the
// template's name is no draft of the golden path, and a missing draft names
// the template command.
func TestDeclareTemplateFromDraftOtherSubject(t *testing.T) {
	e := newDeclareEnv(t)
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "dev"}, Fields: []contract.Inferred{},
		Declaration: &contract.InferredDeclaration{State: contract.DeclarationDraft, Asker: contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent}}}
	e.publishBundle(t, e.host+"/acme/springboot-service", "draft-1", map[string]string{
		"pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: springboot-service\n  version: 0.1.0\n", contract.InferredRecordFile: string(rec.Marshal())}, nil)
	req := e.templateReq()
	req.FromDraft = "draft-1"
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); refusal(t, err).Code != codeDraftNotFound || !strings.Contains(err.Error(), "no draft of the golden path "+templateSubject) {
		t.Fatal(err)
	}
	req.FromDraft = "draft-9"
	_, _, err := e.svc.PlanDeclare(t.Context(), req)
	if de := refusal(t, err); de.Code != codeDraftNotFound || de.CLI != "pacto declare "+templateSubject+" --backstage-url https://catalog.acme.io --registry "+e.host+"/acme" {
		t.Fatalf("%+v", de)
	}
}

func TestDeclareTemplateRefusesExistingRepo(t *testing.T) {
	for name, yaml := range map[string]string{
		"workload":    "pactoVersion: \"2.5\"\nservice:\n  name: springboot-service\n  version: 1.2.0\nworkload: service\n",
		"golden path": "pactoVersion: \"2.5\"\nservice:\n  name: springboot-service\n  version: 1.2.0\n  template: {}\n",
	} {
		e := newDeclareEnv(t)
		person := e.signer(t, personToken("ana@acme.com"))
		e.publishBundle(t, e.host+"/acme/springboot-service", "1.2.0", map[string]string{"pacto.yaml": yaml}, &person)
		e.login(personToken("ana@acme.com"))
		_, err := e.svc.Declare(t.Context(), e.templateReq())
		if de := refusal(t, err); de.Code != codeTemplateNameTaken || !strings.Contains(de.Message, e.host+"/acme/springboot-service already holds springboot-service 1.2.0") ||
			!strings.Contains(de.Message, "pacto publish") || de.CLI == "" {
			t.Fatalf("%s: %+v", name, de)
		}
		if tags := e.springbootTags(t); !slices.Equal(tags, []string{"1.2.0"}) {
			t.Fatalf("%s: pushed %v", name, tags)
		}
	}
}

func TestDeclareTemplateRegistryUnreadable(t *testing.T) {
	e := newDeclareEnv(t)
	req := e.templateReq()
	req.Store = &faultyRegistry{Registry: e.reg, tags: errors.New("denied")}
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); refusal(t, err).Code != codeRegistryUnreadable {
		t.Fatal(err)
	}
}

func TestDeclareTemplateCommandsKeepSubject(t *testing.T) {
	e := newDeclareEnv(t)
	req := e.templateReq()
	reg := e.host + "/acme"
	if got := req.command("sha256:x"); got != "pacto declare "+templateSubject+" --backstage-url https://catalog.acme.io --registry "+reg+" --intent sha256:x" {
		t.Fatal(got)
	}
	noReg := e.templateReq()
	noReg.Registry, noReg.BackstageURL = "", ""
	if _, _, err := e.svc.PlanDeclare(t.Context(), noReg); refusal(t, err).CLI != "pacto declare "+templateSubject+" --backstage-url <backstage-url> --registry <registry>" {
		t.Fatal(err)
	}
	noTpl := e.templateReq()
	noTpl.Template = nil
	_, _, err := e.svc.PlanDeclare(t.Context(), noTpl)
	if de := refusal(t, err); de.Code != codeTemplateNotFound || !strings.Contains(de.CLI, templateSubject) || !strings.Contains(de.CLI, "--backstage-url") {
		t.Fatalf("%+v", de)
	}
	t.Setenv("SIGSTORE_ID_TOKEN", ciWorkflowToken())
	p, _ := e.plan(t, e.templateReq())
	want := "pacto declare " + templateSubject + " --backstage-url https://catalog.acme.io --registry " + reg + " --from-draft <tag>"
	if p.AcceptCLI != want || !slices.ContainsFunc(p.Notes, func(n string) bool { return strings.HasSuffix(n, want) }) {
		t.Fatalf("%s %v", p.AcceptCLI, p.Notes)
	}
	for _, s := range append(p.Notes, p.AcceptCLI) {
		if strings.Contains(s, "pacto declare springboot-service") {
			t.Fatal(s)
		}
	}
}
