package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

func TestAcceptPlanListsDrafts(t *testing.T) {
	e := newAcceptEnv(t, false)
	if p, err := e.svc.AcceptPlan(t.Context(), e.proposedOpts()); err != nil || p.Drafts != nil || p.DraftsTotal != 0 {
		t.Fatalf("%+v %v", p, err)
	}
	first := e.agentDraft(t, e.proposedOpts())
	// A tag that holds no acceptance draft is listed as unreadable, never dropped.
	if _, err := e.svc.writeAcceptanceDraft(t.Context(), nil, e.repo, delivery.AcceptanceDraft{Record: e.rangesRecord(), Subject: e.subject, OnBehalfOf: "bo"}, nil); err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.AcceptPlan(t.Context(), e.proposedOpts())
	if err != nil || p.DraftsTotal != 2 || len(p.Drafts) != 2 {
		t.Fatalf("%+v %v", p, err)
	}
	if d := p.Drafts[1]; d.Ref != first || d.State != "draft" || d.OnBehalfOf != "ana" || d.Signed || d.Intent == "" || len(d.Ranges) == 0 {
		t.Fatalf("%+v", d)
	}
	if !strings.HasSuffix(p.Drafts[0].Ref, ":pacto-accept-draft-2") {
		t.Fatalf("newest first: %+v", p.Drafts)
	}
}

func TestAcceptDraftPlanListsNoDrafts(t *testing.T) {
	e := newAcceptEnv(t, false)
	first := e.agentDraft(t, e.proposedOpts())
	// A plan of a draft lists none: the person reads that draft alone.
	dp, err := e.svc.AcceptPlan(t.Context(), e.draftOpts(first))
	if err != nil || dp.Drafts != nil {
		t.Fatalf("%+v %v", dp, err)
	}
}

func TestAcceptPlanDraftsBoundedAndUnreadable(t *testing.T) {
	e := newAcceptEnv(t, false)
	for i := range MaxListedDrafts + 2 {
		if _, err := e.svc.writeAcceptanceDraft(t.Context(), nil, e.repo, delivery.AcceptanceDraft{Record: e.rangesRecord(), Subject: e.subject, OnBehalfOf: strings.Repeat("a", i+1)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	e.failWhen(func(r *http.Request) bool {
		return strings.HasSuffix(r.URL.Path, "/manifests/pacto-accept-draft-22")
	})
	p, err := e.svc.AcceptPlan(t.Context(), e.proposedOpts())
	if err != nil || p.DraftsTotal != MaxListedDrafts+2 || len(p.Drafts) != MaxListedDrafts {
		t.Fatalf("%d %d %v", p.DraftsTotal, len(p.Drafts), err)
	}
	if d := p.Drafts[0]; d.State != draftUnreadable || d.Reason == "" || !strings.HasSuffix(d.Ref, "-22") {
		t.Fatalf("%+v", d)
	}
}

func TestAcceptPlanDraftListingFailsIsANote(t *testing.T) {
	e := newAcceptEnv(t, false)
	e.failWhen(func(r *http.Request) bool {
		return strings.HasSuffix(r.URL.Path, "/tags/list") && strings.Contains(r.URL.RawQuery, "draft")
	})
	p, err := e.svc.AcceptPlan(t.Context(), e.proposedOpts())
	if err != nil || p.Drafts != nil || !strings.Contains(strings.Join(p.Notes, "\n"), "could not be listed") {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestAcceptWritesThroughOpts(t *testing.T) {
	e := newAcceptEnv(t, false)
	opened := []string{}
	open := func(path string) (*remote.Repository, error) {
		opened = append(opened, path)
		return ocireferrer.Open(path, ocireferrer.Options{Keychain: anonymous})
	}
	opts := e.proposedOpts()
	// The dashboard's seam: the person's fresh login, taken by the caller.
	opts.Write, opts.Signing.Login = open, func(context.Context) (string, error) { return personToken("ana@acme.com"), nil }
	if res := e.accept(t, opts); res.Record == "" || len(opened) != 1 || opened[0] != e.repo {
		t.Fatalf("%+v %v", res, opened)
	}
	draft := e.proposedOpts()
	draft.Agent, draft.OnBehalfOf, draft.Write, draft.Signing.Draft = true, "ana", open, true
	if res := e.accept(t, draft); res.Draft == "" || len(opened) != 2 {
		t.Fatalf("%+v %v", res, opened)
	}
	draft.Write = func(string) (*remote.Repository, error) { return nil, errors.New("no push credential") }
	draft.OnBehalfOf = "bo"
	if _, err := e.svc.Accept(t.Context(), draft); err == nil || !strings.Contains(err.Error(), "no push credential") {
		t.Fatal(err)
	}
}
