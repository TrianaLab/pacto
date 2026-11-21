package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// agentDraft writes, as an agent with no token, the draft of opts and
// returns its ref.
func (e *acceptEnv) agentDraft(t *testing.T, opts AcceptOptions) string {
	t.Helper()
	opts.Agent, opts.OnBehalfOf = true, "ana"
	res := e.accept(t, opts)
	if res.Draft == "" || res.Record != "" || res.Signer.Source != signerNone {
		t.Fatalf("%+v", res)
	}
	return res.Draft
}

// draftOpts accepts draft as the test person.
func (e *acceptEnv) draftOpts(draft string) AcceptOptions {
	opts := e.trust()
	opts.Draft, opts.Signing = draft, e.signing()
	return opts
}

func refusalCode(err error) string {
	var de *DeclareError
	if errors.As(err, &de) {
		return de.Code
	}
	return ""
}

// An agent run writes an unsigned draft and never logs in, even with a
// person's ambient token.
func TestAcceptAsAgentWritesUnsignedDraft(t *testing.T) {
	e := newAcceptEnv(t, false)
	calls := e.login(personToken("ana@acme.com"))
	e.agentDraft(t, e.proposedOpts())
	t.Setenv("SIGSTORE_ID_TOKEN", personToken("ana@acme.com"))
	opts := e.proposedOpts()
	opts.Agent, opts.Fields = true, []string{"dependencies[ledger].ref"}
	res := e.accept(t, opts)
	if res.Draft != e.repo+":pacto-accept-draft-2" || res.Signer.Source != signerNone || *calls != 0 {
		t.Fatalf("%+v %d", res, *calls)
	}
}

func TestAcceptDraftSignsAsPerson(t *testing.T) {
	e := newAcceptEnv(t, true)
	opts := e.proposedOpts()
	opts.Fields = []string{"dependencies[ledger].compatibility=^2.4.0"}
	draft := e.agentDraft(t, opts)
	calls := e.login(personToken("ana@acme.com"))
	p := e.plan(t, e.draftOpts(draft))
	if p.Draft != draft || p.OnBehalfOf != "ana" || !strings.Contains(strings.Join(p.Notes, "\n"), "unsigned draft, proposer asserted") || *calls != 0 {
		t.Fatalf("%+v", p)
	}
	res := e.accept(t, e.draftOpts(draft))
	if !res.Executed || res.State != impact.AcceptCounted || res.Signer.Subject != "ana@acme.com" || res.Plan.IntentDigest != p.IntentDigest ||
		len(res.Plan.Ranges) != 2 || res.Plan.Ranges[1].Value != "^2.4.0" || *calls != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestAcceptDraftOfBreak(t *testing.T) {
	e := newAcceptEnv(t, false)
	opts := e.trust()
	opts.Signing = e.signing()
	opts.Break, opts.From = "ledger@3.0.0", writeDir(t, ledgerFiles("3.0.0", "2026-12-31", "reports"))
	draft := e.agentDraft(t, opts)
	do := e.draftOpts(draft)
	do.From = opts.From
	if p := e.plan(t, do); p.Break == nil || p.Break.Version != "3.0.0" || p.Kind != delivery.AcceptKindBreak {
		t.Fatalf("%+v", p)
	}
}

func TestAcceptDraftStale(t *testing.T) {
	e := newAcceptEnv(t, false)
	draft := e.agentDraft(t, e.proposedOpts())
	e.publishBundle(t, e.repo, "1.7.0", e.reportsFiles("1.7.0", true), nil)
	calls := e.login(personToken("ana@acme.com"))
	_, err := e.svc.Accept(t.Context(), e.draftOpts(draft))
	if refusalCode(err) != codePlanStale || *calls != 0 {
		t.Fatal(err)
	}
}

func TestAcceptDraftNonHumanRefused(t *testing.T) {
	e := newAcceptEnv(t, false)
	draft := e.agentDraft(t, e.proposedOpts())
	// An ambient CI token is refused before any signer is built.
	t.Setenv("SIGSTORE_ID_TOKEN", ciWorkflowToken())
	_, err := e.svc.Accept(t.Context(), e.draftOpts(draft))
	var de *DeclareError
	if !errors.As(err, &de) || de.Code != codePersonSigningUnavailable || de.CLI != "pacto accept "+e.repo+" --draft "+draft {
		t.Fatal(err)
	}
	// A key that cannot be read fails before anything.
	noAmbient(t)
	opts := e.draftOpts(draft)
	opts.Signing.Key = "missing.key"
	if _, err := e.svc.Accept(t.Context(), opts); err == nil || refusalCode(err) != "" {
		t.Fatal(err)
	}
	// A login that names no person is refused after it.
	e.login(ciWorkflowToken())
	if _, err := e.svc.Accept(t.Context(), e.draftOpts(draft)); refusalCode(err) != codePersonSigningUnavailable {
		t.Fatal(err)
	}
	if tags := e.acceptTags(t); len(tags) != 1 {
		t.Fatal(tags)
	}
}

// A CI-signed draft verifies as its asker; a draft whose asker is not its
// signer is refused.
func TestAcceptDraftSignatureChecked(t *testing.T) {
	e := newAcceptEnv(t, false)
	t.Setenv("SIGSTORE_ID_TOKEN", ciWorkflowToken())
	opts := e.proposedOpts()
	opts.OnBehalfOf = "ana"
	draft := e.accept(t, opts).Draft
	noAmbient(t)
	p := e.plan(t, e.draftOpts(draft))
	if !strings.Contains(strings.Join(p.Notes, "\n"), "the draft is signed by https://github.com/acme/invoices") {
		t.Fatalf("%+v", p.Notes)
	}
	bad := e.draftOpts(draft)
	bad.Signing.TrustedRoot = "missing.json"
	if _, err := e.svc.AcceptPlan(t.Context(), bad); err == nil || refusalCode(err) != "" {
		t.Fatal(err)
	}
	d, bundle, err := e.svc.readAcceptanceDraft(t.Context(), draft)
	if err != nil || bundle == nil {
		t.Fatal(err)
	}
	d.Asker.Subject = "mallory@acme.com"
	if _, err := draftSignature(d, bundle, e.root); refusalCode(err) != codeSignerUnverified {
		t.Fatal(err)
	}
	d.Asker = contract.InferredSigner{Issuer: signerKey}
	if note, err := draftSignature(d, bundle, e.root); err != nil || !strings.Contains(note, "names no identity") {
		t.Fatal(note, err)
	}
}

func TestAcceptDraftNotFound(t *testing.T) {
	e := newAcceptEnv(t, false)
	draft := e.agentDraft(t, e.proposedOpts())
	// A draft in another repository, an unknown tag, a draft for another
	// subject and one naming no intent are no draft of this service.
	for name, write := range map[string]delivery.AcceptanceDraft{
		"other subject": {Record: e.rangesRecord(), Subject: e.host + "/acme/billing@" + e.digest},
		"no intent":     {Record: e.rangesRecord(), Subject: e.subject},
	} {
		tag, err := e.svc.writeAcceptanceDraft(t.Context(), e.repo, write, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.AcceptPlan(t.Context(), e.draftOpts(e.repo+":"+tag)); refusalCode(err) != codeDraftNotFound {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, ref := range []string{e.host + "/acme/billing:pacto-accept-draft-1", e.repo + ":pacto-accept-draft-9"} {
		if _, err := e.svc.AcceptPlan(t.Context(), e.draftOpts(ref)); refusalCode(err) != codeDraftNotFound {
			t.Errorf("%s: %v", ref, err)
		}
	}
	opts := e.draftOpts(draft)
	opts.Ref, opts.Dir = "", writeDir(t, map[string]string{"pacto.yaml": "pactoVersion: \"2.5\"\nservice: {name: reports, version: 1.0.0}\n"})
	if _, err := e.svc.AcceptPlan(t.Context(), opts); refusalCode(err) != codeNotPublished {
		t.Fatal(err)
	}
}
