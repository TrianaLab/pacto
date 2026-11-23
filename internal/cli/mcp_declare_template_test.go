package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

const springSubject = "template:default/springboot-service"

// templateFleet is a caller's fleet view whose catalog lists
// springboot-service, acceptable as acceptable says.
func templateFleet(t *testing.T, acceptable bool) *pactomcp.FleetMode {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("catalog", "catalog", &fleet.Collection{ListsTemplates: true,
		InferredTemplates: []fleet.RawInferredTemplate{{Name: "springboot-service", Entity: springSubject, From: "Catalog", Acceptable: acceptable}}}))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	return &pactomcp.FleetMode{Src: func(context.Context) (*fleet.Query, error) { return q, nil }}
}

// springPulled is the record of the springboot-service bundle tagged tag.
func springPulled(t *testing.T, w *declareWorld, tag string) *contract.InferredRecord {
	t.Helper()
	b, err := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), w.host).Pull(t.Context(), w.host+"/acme/springboot-service:"+tag)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := contract.ReadInferredRecord(b.FS)
	if err != nil || rec == nil || rec.Declaration == nil || b.Contract.Service.Template == nil {
		t.Fatalf("%v %+v", err, rec)
	}
	return rec
}

func TestMCPDeclareTemplateStdioNeedsCatalog(t *testing.T) {
	w := newDeclareWorld(t)
	_, refused, err := newMCPDeclarer(app.NewService(nil, nil), "", "").Plan(context.Background(), pactomcp.DeclareArgs{Service: springSubject, Registry: w.host + "/acme"})
	expect(t, err == nil && refused != nil && refused.Code == pactomcp.RefusalTemplateCatalogUnset, "%+v %v", refused, err)
	_, refused, err = newMCPDeclarer(app.NewService(nil, nil), "", "").Execute(context.Background(), pactomcp.DeclareArgs{Service: springSubject, Registry: w.host + "/acme"})
	expect(t, err == nil && refused != nil && refused.Code == pactomcp.RefusalTemplateCatalogUnset, "execute %+v %v", refused, err)
}

func TestMCPDeclareTemplateStdioResolvesFromCatalog(t *testing.T) {
	w := newDeclareWorld(t)
	decl := newMCPDeclarer(app.NewService(nil, nil), "", templateCatalog(t))
	raw, refused, err := decl.Plan(context.Background(), pactomcp.DeclareArgs{Service: springSubject, Registry: w.host + "/acme"})
	if err != nil || refused != nil {
		t.Fatalf("%s %+v %v", raw, refused, err)
	}
	var p app.DeclarePlan
	_ = json.Unmarshal(raw, &p)
	expect(t, p.Service == "springboot-service" && p.BundleRepo == w.host+"/acme/springboot-service" && p.InferredFrom == "", "plan %+v", p)
}

func TestMCPDeclareTemplateAgentWritesDraft(t *testing.T) {
	w := newDeclareWorld(t)
	decl := newMCPDeclarer(app.NewService(nil, nil), "", templateCatalog(t))
	a := pactomcp.DeclareArgs{Service: springSubject, Registry: w.host + "/acme", Draft: true, OnBehalfOf: "ana@acme.com"}
	raw, refused, err := decl.Plan(context.Background(), a)
	if err != nil || refused != nil {
		t.Fatalf("%s %+v %v", raw, refused, err)
	}
	var p app.DeclarePlan
	_ = json.Unmarshal(raw, &p)
	a.IntentDigest = p.IntentDigest
	raw, refused, err = decl.Execute(context.Background(), a)
	var res app.DeclareResult
	_ = json.Unmarshal(raw, &res)
	expect(t, err == nil && refused == nil && res.Draft && !res.Signed && res.Tag == "draft-1", "%s %+v %v", raw, refused, err)
	expect(t, strings.Contains(res.Plan.AcceptCLI, springSubject+" --backstage-url ") && strings.Contains(res.Plan.AcceptCLI, "--from-draft draft-1"), "accept %q", res.Plan.AcceptCLI)
	d := springPulled(t, w, "draft-1").Declaration
	expect(t, d.State == contract.DeclarationDraft && d.Asker.Class == contract.SignerClassAgent && d.InferredFrom == "", "%+v", d)
}

func TestMCPDeclareTemplateHTTPResolvesFromView(t *testing.T) {
	w := newDeclareWorld(t)
	s := newSelfReviewed(true)
	rd := httpDeclarer(w).withFleet(templateFleet(t, true))
	rd.installNS = "pacto-system"
	raw, refused := planThenWrite(t, rd, callerCtx("alice", s, &bytes.Buffer{}), pactomcp.DeclareArgs{Service: springSubject, Registry: w.host + "/acme"})
	var run struct {
		Tag       string `json:"tag"`
		AcceptCLI string `json:"acceptCLI"`
	}
	_ = json.Unmarshal(raw, &run)
	expect(t, refused == nil && run.Tag == "draft-1" && strings.Contains(run.AcceptCLI, springSubject), "%s %+v", raw, refused)
	expect(t, springPulled(t, w, "draft-1").Declaration.OnBehalfOf == "alice", "on behalf")
	// Plan and write each asked update pactos in the installation namespace.
	expect(t, len(s.asked) == 2 && s.asked[0]["resource"] == "pactos" && s.asked[0]["verb"] == "update" && s.asked[0]["namespace"] == "pacto-system", "asked %v", s.asked)
	// A template the view does not list is not found.
	_, refused, err := rd.Plan(callerCtx("alice", s, &bytes.Buffer{}), pactomcp.DeclareArgs{Service: "template:default/nope", Registry: w.host + "/acme"})
	expect(t, err == nil && refused != nil && refused.Code == pactomcp.RefusalTemplateNotFound, "%+v %v", refused, err)
	// A view no catalog lists templates in, and no fleet view: not found.
	rd.fleet = &pactomcp.FleetMode{Src: func(context.Context) (*fleet.Query, error) { return fleet.NewQuery(&fleet.FleetSnapshot{}), nil }}
	_, refused, _ = rd.Plan(callerCtx("alice", s, &bytes.Buffer{}), pactomcp.DeclareArgs{Service: springSubject, Registry: w.host + "/acme"})
	expect(t, refused != nil && refused.Code == pactomcp.RefusalTemplateNotFound, "no lister %+v", refused)
	rd.fleet = nil
	_, refused, _ = rd.Plan(callerCtx("alice", s, &bytes.Buffer{}), pactomcp.DeclareArgs{Service: springSubject, Registry: w.host + "/acme"})
	expect(t, refused != nil && refused.Code == pactomcp.RefusalTemplateNotFound, "no view %+v", refused)
	// A detected catalog is never accepted from.
	rd = httpDeclarer(w).withFleet(templateFleet(t, false))
	_, refused, _ = rd.Plan(callerCtx("alice", s, &bytes.Buffer{}), pactomcp.DeclareArgs{Service: springSubject, Registry: w.host + "/acme"})
	expect(t, refused != nil && refused.Code == pactomcp.RefusalTemplateSourceUnverified, "detected %+v", refused)
}

func TestMCPDeclareTemplateDraftNeedsAuthorization(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer func() (map[string]any, error)
		code   pactomcp.WriteRefusal
	}{
		{"denied", func() (map[string]any, error) { return map[string]any{"allowed": false}, nil }, pactomcp.RefusalNotAllowed},
		{"failed", func() (map[string]any, error) { return nil, context.DeadlineExceeded }, pactomcp.RefusalReviewFailed},
		{"unevaluated", func() (map[string]any, error) {
			return map[string]any{"allowed": false, "evaluationError": "webhook"}, nil
		}, pactomcp.RefusalReviewFailed},
	} {
		w := newDeclareWorld(t)
		s := newSelfReviewed(true)
		s.answer = c.answer
		rd := httpDeclarer(w).withFleet(templateFleet(t, true))
		a := pactomcp.DeclareArgs{Service: springSubject, Registry: w.host + "/acme", IntentDigest: "sha256:x", OnBehalfOf: "alice"}
		_, refused, err := rd.Plan(callerCtx("alice", s, &bytes.Buffer{}), a)
		expect(t, err == nil && refused != nil && refused.Code == c.code, "%s plan %+v %v", c.name, refused, err)
		_, refused, err = rd.Execute(callerCtx("alice", s, &bytes.Buffer{}), a)
		expect(t, err == nil && refused != nil && refused.Code == c.code, "%s execute %+v %v", c.name, refused, err)
		expect(t, s.asked[0]["namespace"] == "pacto-delivery", "%s asked %v", c.name, s.asked)
		tags, _ := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), w.host).Tags(t.Context(), w.host+"/acme/springboot-service")
		expect(t, len(tags) == 0, "%s wrote %v", c.name, tags)
	}
}
