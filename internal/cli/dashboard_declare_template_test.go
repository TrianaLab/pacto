package cli

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// springTemplate is a golden path a catalog holds, as the viewer's
// snapshot lists it; acceptable says whether the catalog was configured.
func springTemplate(acceptable bool) dashboard.DeclareWorkload {
	return dashboard.DeclareWorkload{Key: "template:default/springboot-service", Name: "springboot-service",
		Template: &fleet.InferredTemplate{Name: "springboot-service", Entity: "template:default/springboot-service", Summary: "A Spring Boot service",
			From: "Backstage (catalog.acme.io)", OwnerKey: "group:default/platform-team", Acceptable: acceptable, Provenance: "inferred"}}
}

// springTags are the tags of the springboot-service repository.
func (h *declareHost) springTags(t *testing.T) []string {
	t.Helper()
	tags, _ := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), h.w.host).Tags(t.Context(), h.w.host+"/acme/springboot-service")
	return slices.DeleteFunc(tags, func(t string) bool { return strings.HasPrefix(t, "sha256-") || strings.HasPrefix(t, "pacto-sig-") })
}

func forbidden(err error, code string) bool {
	var de *dashboard.DeliveryError
	return errors.As(err, &de) && de.Status == 403 && de.Code == code
}

func TestDashboardDeclareTemplateNeedsAuthorization(t *testing.T) {
	tw := springTemplate(true)
	for _, c := range []struct {
		name  string
		allow func(string, string) (bool, error)
		code  string
	}{
		{"not allowed", func(_, verb string) (bool, error) { return verb != "update", nil }, "NotAllowed"},
		{"review failed", func(string, string) (bool, error) { return false, errors.New("apiserver down") }, "ReviewFailed"},
	} {
		h := newDeclareHost(t, false, true)
		h.allow = c.allow
		_, err := h.d.Plan(h.req(), tw, dashboard.DeclareInput{ServiceKey: tw.Key})
		expect(t, forbidden(err, c.code), "%s: plan %v", c.name, err)
		for _, mode := range []string{"draft", "sign"} {
			_, err = h.d.Accept(h.req(), tw, dashboard.DeclareAcceptInput{ServiceKey: tw.Key, IntentDigest: "sha256:x", Mode: mode})
			expect(t, forbidden(err, c.code), "%s: %s %v", c.name, mode, err)
		}
		expect(t, len(h.springTags(t)) == 0 && !h.d.MayDeclareTemplate(h.req().Context()), "%s: wrote %v", c.name, h.springTags(t))
	}
	// The right asked is update pactos in the installation namespace.
	h := newDeclareHost(t, false, true)
	h.d.id.policyNS = "pacto-system"
	expect(t, h.d.MayDeclareTemplate(h.req().Context()), "allowed")
	expect(t, slices.Equal(h.reviews, []string{"/pacto-system/pactos/update/"}), "reviews %v", h.reviews)
	// Two clusters: nobody tells which one Pacto is installed in.
	h = newDeclareHost(t, false, true, "uid-prod", "uid-dev")
	_, err := h.d.Plan(h.req(), tw, dashboard.DeclareInput{ServiceKey: tw.Key})
	expect(t, forbidden(err, "ClusterNotReviewable"), "two clusters %v", err)
	// A host that reviews nobody accepts nothing.
	h.d.id.viewer = nil
	expect(t, !h.d.MayDeclareTemplate(h.req().Context()), "no viewer")
	// A local host acts with the person's own kubeconfig: no review.
	h = newDeclareHost(t, true, true)
	h.allow = func(string, string) (bool, error) { return false, nil }
	p, err := h.d.Plan(h.req(), tw, dashboard.DeclareInput{ServiceKey: tw.Key})
	expect(t, err == nil && p.Refusal == nil && len(h.reviews) == 0 && h.d.MayDeclareTemplate(context.Background()), "local %v %+v %v", err, p, h.reviews)
}

func TestDashboardDeclareDetectedTemplateRefused(t *testing.T) {
	h := newDeclareHost(t, false, true)
	tw := springTemplate(false)
	p, err := h.d.Plan(h.req(), tw, dashboard.DeclareInput{ServiceKey: tw.Key})
	expect(t, err == nil && p.Plan == nil && p.Refusal != nil && p.Refusal.Code == "TemplateSourceUnverified" &&
		strings.Contains(p.Refusal.Message, "dashboard.backstage.url"), "plan %v %+v", err, p)
	out, err := h.d.Accept(h.req(), tw, dashboard.DeclareAcceptInput{ServiceKey: tw.Key, IntentDigest: "sha256:x", Mode: "draft"})
	expect(t, err == nil && out.Refusal != nil && out.Refusal.Code == "TemplateSourceUnverified" && len(h.springTags(t)) == 0, "accept %v %+v", err, out)
	// A name a service already holds is never accepted either.
	tw = springTemplate(true)
	tw.Template.NameTaken = true
	p, _ = h.d.Plan(h.req(), tw, dashboard.DeclareInput{ServiceKey: tw.Key})
	expect(t, p.Refusal != nil && p.Refusal.Code == "TemplateNameTaken", "name taken %+v", p)
}

// templatePlan plans tw on h and fails on any refusal.
func (h *declareHost) templatePlan(t *testing.T, tw dashboard.DeclareWorkload) *dashboard.DeclareAnswer {
	t.Helper()
	p, err := h.d.Plan(h.req(), tw, dashboard.DeclareInput{ServiceKey: tw.Key})
	if err != nil || p.Refusal != nil {
		t.Fatalf("%v %+v", err, p)
	}
	return p
}

func TestDashboardDeclareTemplateAcceptSignsOrDrafts(t *testing.T) {
	tw := springTemplate(true)
	h := newDeclareHost(t, false, true)
	h.d.base.BackstageURL = "https://catalog.acme.io"
	p := h.templatePlan(t, tw)
	expect(t, p.Plan.Service == "springboot-service" && p.Plan.BundleRepo == h.w.host+"/acme/springboot-service" && p.Plan.InferredFrom == "" &&
		strings.Contains(p.Plan.Contract.Text, "template: {}") && p.PRCLI == "", "plan %+v pr %q", p.Plan, p.PRCLI)
	expect(t, strings.HasPrefix(p.Command, "pacto declare template:default/springboot-service --backstage-url https://catalog.acme.io --registry "), "command %q", p.Command)
	out := h.accept(t, tw, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	expect(t, out.Refusal == nil && out.Tag == "draft-1" && out.Draft && !out.Signed, "draft %+v", out)
	expect(t, strings.Contains(out.Command, "template:default/springboot-service") && strings.Contains(out.Command, "--from-draft draft-1"), "accept command %q", out.Command)
	// The draft is listed again in the next plan, and a fresh login signs.
	h.freshLogin(t, personToken())
	p = h.templatePlan(t, tw)
	expect(t, len(p.Drafts) == 1 && p.Plan.State == contract.DeclarationDeclared, "drafts %+v state %q", p.Drafts, p.Plan.State)
	out = h.accept(t, tw, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "sign"})
	expect(t, out.Refusal == nil && out.Tag == "0.1.0" && out.Signed, "sign %+v", out)
	repo, digest, _ := strings.Cut(out.Published, "@")
	got, err := publish.SignersOf(t.Context(), publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), h.w.host), h.ss.Root, false, repo, digest)
	expect(t, err == nil && len(got) == 1 && got[0].Subject == "ana@acme.com", "signers %+v %v", got, err)
	expect(t, slices.Contains(h.reviews, "/pacto-delivery/pactos/update/"), "reviews %v", h.reviews)
	// A second acceptance into the same repository is refused.
	p, _ = h.d.Plan(h.req(), tw, dashboard.DeclareInput{ServiceKey: tw.Key})
	expect(t, p.Refusal != nil && p.Refusal.Code == "TemplateNameTaken", "again %+v", p)
}
