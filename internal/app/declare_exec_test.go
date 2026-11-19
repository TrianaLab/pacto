package app

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// ciWorkflowToken is acme's release workflow as an ambient token whose
// identity Pacto reads: a ci-class signer.
func ciWorkflowToken() string {
	const wf = "acme/invoices/.github/workflows/release.yml@refs/heads/main"
	return testutil.TokenWith(map[string]string{"sub": "https://github.com/" + wf, "iss": "https://token.actions.githubusercontent.com",
		"job_workflow_ref": wf, "repository": "https://github.com/acme/invoices", "ref": "refs/heads/main"})
}

// login makes the env's interactive login return tok and counts its calls.
func (e *declareEnv) login(tok string) *int {
	calls := 0
	e.svc.sys.login = func(context.Context, declareLogin) (string, error) {
		calls++
		return tok, nil
	}
	return &calls
}

// declare runs a declaration and fails the test on an error.
func (e *declareEnv) declare(t *testing.T, req DeclareRequest) *DeclareResult {
	t.Helper()
	res, err := e.svc.Declare(t.Context(), req)
	if err != nil {
		t.Fatalf("%v\n%+v", err, res)
	}
	return res
}

// pulled is the bundle published under tag and its record.
func (e *declareEnv) pulled(t *testing.T, tag string) (*contract.Bundle, *contract.InferredRecord) {
	t.Helper()
	b, err := e.reg.Pull(t.Context(), e.host+"/acme/invoices:"+tag)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := contract.ReadInferredRecord(b.FS)
	if err != nil || rec == nil {
		t.Fatalf("%v %v", err, rec)
	}
	return b, rec
}

// tags are the invoices bundle repository's version and draft tags: the
// signature fallback tags left out.
func (e *declareEnv) tags(t *testing.T) []string {
	t.Helper()
	tags, err := e.reg.Tags(t.Context(), e.host+"/acme/invoices")
	if err != nil && !strings.Contains(err.Error(), "NAME_UNKNOWN") {
		t.Fatal(err)
	}
	return slices.DeleteFunc(tags, func(tag string) bool {
		return strings.HasPrefix(tag, "pacto-sig-") || strings.HasPrefix(tag, "sha256-")
	})
}

// signers are the verified signers of the published digest repo@digest.
func (e *declareEnv) signers(t *testing.T, published string) []delivery.PublisherIdentity {
	t.Helper()
	repo, digest, _ := strings.Cut(published, "@")
	got, err := publish.SignersOf(t.Context(), e.reg, e.ss.Root, false, repo, digest)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestDeclareLoginWritesDeclared(t *testing.T) {
	e := newDeclareEnv(t)
	calls := e.login(personToken("ana@acme.com"))
	res := e.declare(t, e.req("ledger"))
	if !res.Executed || res.Draft || !res.Signed || res.Tag != firstDeclaration || *calls != 1 || res.Plan.Publish.Version != firstDeclaration {
		t.Fatalf("%+v", res)
	}
	_, rec := e.pulled(t, firstDeclaration)
	want := contract.InferredSigner{Subject: "ana@acme.com", Issuer: personIssuer, Class: contract.SignerClassHuman}
	if d := rec.Declaration; d.State != contract.DeclarationDeclared || d.Asker != want || len(d.Accepted) != 1 || d.Accepted[0].Range != "^2.3.0" {
		t.Fatalf("%s", rec.Marshal())
	}
	if got := e.signers(t, res.Published); len(got) != 1 || got[0].Subject != want.Subject || got[0].Issuer != want.Issuer {
		t.Fatalf("%+v", got)
	}
}

func TestDeclareOneSignature(t *testing.T) {
	e := newDeclareEnv(t)
	e.login(personToken("ana@acme.com"))
	res := e.declare(t, e.req())
	repo, digest, _ := strings.Cut(res.Published, "@")
	refs, err := e.reg.Referrers(t.Context(), repo, digest, delivery.BundleMediaType)
	if err != nil || len(refs) != 1 {
		t.Fatalf("%v %v", err, refs)
	}
}

func TestDeclarePrintsPlanBeforeLogin(t *testing.T) {
	e := newDeclareEnv(t)
	var order []string
	e.svc.sys.login = func(context.Context, declareLogin) (string, error) {
		order = append(order, "login")
		return personToken("ana@acme.com"), nil
	}
	req := e.req()
	req.OnPlan = func(p *DeclarePlan) { order = append(order, "plan "+p.Version) }
	e.declare(t, req)
	if !slices.Equal(order, []string{"plan " + firstDeclaration, "login"}) {
		t.Fatal(order)
	}
}

func TestDeclareAmbientHumanTokenWritesDraft(t *testing.T) {
	e := newDeclareEnv(t)
	t.Setenv("SIGSTORE_ID_TOKEN", personToken("ana@acme.com"))
	calls := e.login(personToken("ana@acme.com"))
	res := e.declare(t, e.req("ledger"))
	if !res.Draft || !res.Signed || res.Tag != "draft-1" || *calls != 0 || res.Plan.AcceptCLI != "pacto declare invoices --registry "+e.host+"/acme --from-draft draft-1" ||
		!slices.ContainsFunc(res.Plan.Notes, func(n string) bool { return strings.Contains(n, "--from-draft") }) {
		t.Fatalf("%+v", res)
	}
	_, rec := e.pulled(t, "draft-1")
	if d := rec.Declaration; d.State != contract.DeclarationDraft || d.Asker.Subject != "ana@acme.com" || d.Asker.Class != contract.SignerClassAgent ||
		len(d.Requested) != 1 || len(d.Accepted) != 0 {
		t.Fatalf("%s", rec.Marshal())
	}
	if got := e.signers(t, res.Published); len(got) != 1 || got[0].Subject != "ana@acme.com" {
		t.Fatalf("%+v", got)
	}
}

func TestDeclareIgnoresSigstoreIDTokenForDeclared(t *testing.T) {
	for _, agent := range []bool{false, true} {
		e := newDeclareEnv(t)
		t.Setenv("SIGSTORE_ID_TOKEN", personToken("ana@acme.com"))
		calls := e.login(personToken("ana@acme.com"))
		req := e.req()
		req.Agent = agent
		res := e.declare(t, req)
		_, rec := e.pulled(t, res.Tag)
		if *calls != 0 || rec.Declaration.State != contract.DeclarationDraft || slices.Contains(e.tags(t), firstDeclaration) {
			t.Fatalf("agent %v: %d %s", agent, *calls, rec.Marshal())
		}
	}
}

func TestDeclareCITokenWritesDraft(t *testing.T) {
	e := newDeclareEnv(t)
	t.Setenv("SIGSTORE_ID_TOKEN", ciWorkflowToken())
	req := e.req("ledger")
	req.OnBehalfOf = "ana"
	res := e.declare(t, req)
	if res.Tag != "draft-1" || !res.Signed || res.Plan.AcceptCLI != "pacto declare invoices --registry "+e.host+"/acme --from-draft draft-1" {
		t.Fatalf("%+v", res)
	}
	_, rec := e.pulled(t, "draft-1")
	if d := rec.Declaration; d.State != contract.DeclarationDraft || d.Asker.Class != contract.SignerClassCI || d.OnBehalfOf != "ana" ||
		len(d.Requested) != 1 || d.Requested[0].Field != "dependencies[ledger].compatibility" {
		t.Fatalf("%s", rec.Marshal())
	}
	// The next draft takes the next number.
	if res := e.declare(t, req); res.Tag != "draft-2" {
		t.Fatalf("%+v", res)
	}
}

func TestDeclareWorkloadIdentityWritesDraft(t *testing.T) {
	e := newDeclareEnv(t)
	t.Setenv("SIGSTORE_ID_TOKEN", testutil.Token("system:serviceaccount:billing:deployer", "https://kubernetes.default.svc"))
	res := e.declare(t, e.req())
	if !res.Draft || !res.Signed || !strings.Contains(res.Plan.Signer.Why, "names no person") {
		t.Fatalf("%+v", res)
	}
	if _, rec := e.pulled(t, res.Tag); rec.Declaration.Asker.Class != contract.SignerClassAgent {
		t.Fatalf("%s", rec.Marshal())
	}
}

func TestDeclareKeyWritesDraft(t *testing.T) {
	e := newDeclareEnv(t)
	dir := t.TempDir()
	keyFiles(t, dir)
	t.Setenv("COSIGN_PASSWORD", "pw")
	req := e.req()
	req.Signing.Key = filepath.Join(dir, "cosign.key")
	res := e.declare(t, req)
	if !res.Draft || !res.Signed || res.Tag != "draft-1" {
		t.Fatalf("%+v", res)
	}
	if _, rec := e.pulled(t, res.Tag); rec.Declaration.Asker.Issuer != signerKey {
		t.Fatalf("%s", rec.Marshal())
	}
}

func TestDeclareAgentUnsignedDraft(t *testing.T) {
	e := newDeclareEnv(t)
	req := e.req()
	req.Agent, req.OnBehalfOf = true, "ana"
	res := e.declare(t, req)
	if !res.Draft || res.Signed || res.Tag != "draft-1" {
		t.Fatalf("%+v", res)
	}
	_, rec := e.pulled(t, res.Tag)
	if rec.Declaration.Asker != (contract.InferredSigner{Subject: "", Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent}) || rec.Declaration.OnBehalfOf != "ana" {
		t.Fatalf("%s", rec.Marshal())
	}
}

func TestDeclareLoginOfNoPersonWritesDraft(t *testing.T) {
	e := newDeclareEnv(t)
	e.login(ciWorkflowToken())
	res := e.declare(t, e.req())
	if !res.Draft || !res.Signed {
		t.Fatalf("%+v", res)
	}
	if _, rec := e.pulled(t, res.Tag); rec.Declaration.Asker.Class != contract.SignerClassCI {
		t.Fatalf("%s", rec.Marshal())
	}
}

func TestDeclareSignerNotAllowedRefused(t *testing.T) {
	other := policyConfigMap(map[string]any{delivery.PolicyKey: `{"signers":[{"issuer":"https://other.example","subjects":".*"}]}`})
	e := newDeclareEnv(t, append(billing(time.Now()), other)...)
	e.login(personToken("ana@acme.com"))
	_, err := e.svc.Declare(t.Context(), e.req())
	if de := refusal(t, err); de.Code != codeSignerNotAllowed || len(e.tags(t)) != 0 {
		t.Fatal(err, e.tags(t))
	}
}

func TestDeclareCertIdentityMismatchStopsBeforeTag(t *testing.T) {
	e := newDeclareEnv(t)
	// The token names ana, and the Fulcio issues for mallory.
	e.login(testutil.TokenWith(map[string]string{"sub": "mallory@acme.com", "email": "ana@acme.com", "email_verified": "true", "iss": personIssuer}))
	_, err := e.svc.Declare(t.Context(), e.req())
	if de := refusal(t, err); de.Code != codeSignerUnverified || len(e.tags(t)) != 0 {
		t.Fatal(err, e.tags(t))
	}
}

func TestDeclareRecordInvalidRefused(t *testing.T) {
	// No installation policy is read, and the login's token names no issuer.
	e := newDeclareEnv(t, billing(time.Now())...)
	e.login(testutil.TokenWith(map[string]string{"sub": "ana@acme.com", "email": "ana@acme.com", "email_verified": "true"}))
	if _, err := e.svc.Declare(t.Context(), e.req()); err == nil || !strings.Contains(err.Error(), "issuer is empty") || len(e.tags(t)) != 0 {
		t.Fatal(err)
	}
}

func TestDeclarePlanStale(t *testing.T) {
	e := newDeclareEnv(t)
	calls := e.login(personToken("ana@acme.com"))
	req := e.req()
	req.IntentDigest = "sha256:old"
	res, err := e.svc.Declare(t.Context(), req)
	if de := refusal(t, err); de.Code != codePlanStale || de.CLI != "pacto declare invoices --intent "+res.Plan.IntentDigest || *calls != 0 {
		t.Fatal(err)
	}
	req.IntentDigest = res.Plan.IntentDigest
	if res := e.declare(t, req); !res.Executed {
		t.Fatalf("%+v", res)
	}
}

func TestDeclareIdempotentRerun(t *testing.T) {
	e := newDeclareEnv(t)
	calls := e.login(personToken("ana@acme.com"))
	first := e.declare(t, e.req())
	again := e.declare(t, e.req())
	if again.Executed || again.Plan.Unchanged != firstDeclaration || again.Tag != "" || *calls != 1 || !slices.Equal(e.tags(t), []string{firstDeclaration}) {
		t.Fatalf("%+v", again)
	}
	if len(e.signers(t, first.Published)) != 1 {
		t.Fatal("a second signature")
	}
}

func TestDeclareVersionConflict(t *testing.T) {
	e := newDeclareEnv(t)
	calls := e.login(personToken("ana@acme.com"))
	e.publishBundle(t, e.host+"/acme/invoices", firstDeclaration, e.invoicesBaseline(firstDeclaration, ""), nil)
	if _, err := e.svc.Declare(t.Context(), e.req()); refusal(t, err).Code != codeVersionConflict || *calls != 0 {
		t.Fatal(err)
	}
	// A registry that cannot say what the version holds refuses it too.
	e2 := newDeclareEnv(t)
	calls = e2.login(personToken("ana@acme.com"))
	e2.svc.sys.publishRegistry = func() publish.Registry { return headFails{e2.reg} }
	if _, err := e2.svc.Declare(t.Context(), e2.req()); refusal(t, err).Code != codeVersionConflict || *calls != 0 {
		t.Fatal(err)
	}
}

// headFails cannot read the first declaration's tag.
type headFails struct{ publish.Registry }

func (h headFails) Head(ctx context.Context, ref string) (*publish.Remote, error) {
	if strings.HasSuffix(ref, ":"+firstDeclaration) {
		return nil, errors.New("down")
	}
	return h.Registry.Head(ctx, ref)
}

// writeFails refuses every push.
type writeFails struct{ publish.Registry }

func (writeFails) Write(context.Context, string, publish.Artifact) (string, error) {
	return "", errors.New("push refused")
}

func TestDeclareWriteFails(t *testing.T) {
	for _, agent := range []bool{false, true} {
		e := newDeclareEnv(t)
		e.login(personToken("ana@acme.com"))
		e.svc.sys.publishRegistry = func() publish.Registry { return writeFails{e.reg} }
		req := e.req()
		req.Agent = agent
		if _, err := e.svc.Declare(t.Context(), req); err == nil || !strings.Contains(err.Error(), "push refused") {
			t.Fatal(agent, err)
		}
	}
}

func TestDeclareLoginFails(t *testing.T) {
	e := newDeclareEnv(t)
	e.svc.sys.login = func(context.Context, declareLogin) (string, error) { return "", errors.New("cancelled") }
	if _, err := e.svc.Declare(t.Context(), e.req()); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatal(err)
	}
}

func TestDeclareNoRegistry(t *testing.T) {
	e := newDeclareEnv(t)
	req := e.req()
	req.Registry, req.FromDraft = "", "draft-1"
	res, err := e.svc.Declare(t.Context(), req)
	if refusal(t, err).Code != codeNoRegistry || res.Executed {
		t.Fatal(err)
	}
}

func TestDeclareWriteError(t *testing.T) {
	de := &DeclareError{Code: codeSignerUnverified}
	for in, want := range map[error]string{
		nil: "", de: codeSignerUnverified, publish.ErrTagRace: codeVersionConflict, publish.ErrNotReady: codeVersionConflict, errors.New("x"): "",
	} {
		got := declareWriteError(in)
		var d *DeclareError
		if errors.As(got, &d) != (want != "") || (d != nil && d.Code != want) || (in == nil) != (got == nil) {
			t.Errorf("%v: %v", in, got)
		}
	}
}

// draftFiles are the files of the draft at tag, with mutate applied to its
// pacto.yaml and record.
func (e *declareEnv) draftFiles(t *testing.T, tag string, mutate func(raw string, rec *contract.InferredRecord) string) map[string]string {
	t.Helper()
	b, rec := e.pulled(t, tag)
	files := walkBundle(t, b)
	files[DefaultContractPath] = mutate(files[DefaultContractPath], rec)
	files[contract.InferredRecordFile] = string(rec.Marshal())
	return files
}

// agentDraft writes an unsigned agent draft requesting accept.
func (e *declareEnv) agentDraft(t *testing.T, accept ...string) {
	t.Helper()
	req := e.req(accept...)
	req.Agent, req.OnBehalfOf = true, "ana"
	if res := e.declare(t, req); res.Tag != "draft-1" {
		t.Fatalf("%+v", res)
	}
}

// fromDraft is a person's request accepting tag.
func (e *declareEnv) fromDraft(tag string, accept ...string) DeclareRequest {
	req := e.req(accept...)
	req.Namespace, req.FromDraft = "", tag
	return req
}

func TestFromDraftPublishesDeclared(t *testing.T) {
	e := newDeclareEnv(t)
	t.Setenv("SIGSTORE_ID_TOKEN", ciWorkflowToken())
	req := e.req("ledger", "audit")
	req.OnBehalfOf = "ana"
	draft := e.declare(t, req)
	noAmbient(t)
	e.login(personToken("ana@acme.com"))
	res := e.declare(t, e.fromDraft("draft-1", "ledger"))
	if res.Draft || res.Tag != firstDeclaration || len(res.Plan.Unbacked) != 0 ||
		!slices.ContainsFunc(res.Plan.Notes, func(n string) bool { return strings.HasPrefix(n, "requested by the agent: audit ^1.4.0") }) ||
		!slices.Contains(res.Plan.Notes, "the draft is signed by https://github.com/acme/invoices/.github/workflows/release.yml@refs/heads/main (https://token.actions.githubusercontent.com)") {
		t.Fatalf("%+v", res.Plan)
	}
	checkAcceptedDraft(t, e, draft.Published)
}

// checkAcceptedDraft: the published record is ana's, accepts ledger alone
// and names the draft it accepted; audit stays unknown.
func checkAcceptedDraft(t *testing.T, e *declareEnv, draftDigest string) {
	t.Helper()
	b, rec := e.pulled(t, firstDeclaration)
	d := rec.Declaration
	if d.State != contract.DeclarationDeclared || d.Asker.Subject != "ana@acme.com" || len(d.Accepted) != 1 || d.Accepted[0].Field != "dependencies[ledger].compatibility" {
		t.Fatalf("%s", rec.Marshal())
	}
	if d.Draft == nil || d.Draft.Tag != "draft-1" || d.Draft.Digest != draftDigest || d.Draft.Asker.Class != contract.SignerClassCI || d.Draft.OnBehalfOf != "ana" {
		t.Fatalf("%s", rec.Marshal())
	}
	if !strings.Contains(string(b.RawYAML), "compatibility: unknown") {
		t.Fatalf("%s", b.RawYAML)
	}
}

func TestFromDraftReReadsClusterFacts(t *testing.T) {
	e := newDeclareEnv(t)
	e.agentDraft(t)
	const stale = "2020-01-01T00:00:00Z"
	files := e.draftFiles(t, "draft-1", func(raw string, rec *contract.InferredRecord) string {
		rec.Inputs.Cluster.AsOf = stale
		for i := range rec.Fields {
			if rec.Fields[i].AsOf != "" {
				rec.Fields[i].AsOf = stale
			}
			rec.Fields[i].Source = strings.Replace(rec.Fields[i].Source, "reported versions 1.4.0", "reported versions 9.9.9", 1)
		}
		for i := range rec.Coverage {
			rec.Coverage[i].State = contract.CoverageComplete
		}
		return raw
	})
	e.publishBundle(t, e.host+"/acme/invoices", "draft-2", files, nil)
	e.login(personToken("ana@acme.com"))
	res := e.declare(t, e.fromDraft("draft-2"))
	_, rec := e.pulled(t, res.Tag)
	if rec.Inputs.Cluster.AsOf == stale || strings.Contains(string(rec.Marshal()), "9.9.9") ||
		!slices.Contains(res.Plan.Notes, "the draft's signer names no identity Pacto can check: its proposer is asserted") {
		t.Fatalf("%s", rec.Marshal())
	}
}

func TestFromDraftShowsUnbackedDeclaredChanges(t *testing.T) {
	e := newDeclareEnv(t)
	e.agentDraft(t)
	files := e.draftFiles(t, "draft-1", func(raw string, _ *contract.InferredRecord) string {
		raw = strings.Replace(raw, "name: audit\n", "name: audit\n    # forged\n", 1)
		out, err := contract.SetCompatibility([]byte(raw), "audit", "^1.0.0")
		if err != nil {
			t.Fatal(err)
		}
		return string(out) + "publish:\n  bundle: oci://" + e.host + "/acme/invoices\n"
	})
	e.publishBundle(t, e.host+"/acme/invoices", "draft-2", files, nil)
	e.login(personToken("ana@acme.com"))
	p, _, err := e.svc.planToDeclare(t.Context(), e.fromDraft("draft-2"))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(p.Unbacked, "\n")
	if !strings.Contains(joined, "dependencies.compatibility.declared audit: ^1.0.0") || !strings.HasSuffix(joined, "not backed by the cluster read or the baseline: publish") {
		t.Fatalf("%q", p.Unbacked)
	}
}

func TestFromDraftUnbackedUnchecked(t *testing.T) {
	e := newDeclareEnv(t)
	e.agentDraft(t)
	e.login(personToken("ana@acme.com"))
	// No cluster: the draft's facts are dropped and nothing is checked.
	req := e.fromDraft("draft-1")
	req.Dynamic = nil
	p, _, err := e.svc.planToDeclare(t.Context(), req)
	if err != nil || len(p.Unbacked) != 1 || !strings.HasPrefix(p.Unbacked[0], "nothing was checked against the cluster and the baseline (") {
		t.Fatalf("%v %q", err, p.Unbacked)
	}
	// No scratch folder for the check.
	n := 0
	e.svc.sys.mkdirTemp = func(dir, pattern string) (string, error) {
		if n++; n == 3 {
			return "", errors.New("disk full")
		}
		return t.TempDir(), nil
	}
	if p, _, err = e.svc.planToDeclare(t.Context(), e.fromDraft("draft-1")); err != nil || len(p.Unbacked) != 1 || !strings.Contains(p.Unbacked[0], "disk full") {
		t.Fatalf("%v %q", err, p.Unbacked)
	}
}

func TestFromDraftNotFound(t *testing.T) {
	e := newDeclareEnv(t)
	e.login(personToken("ana@acme.com"))
	for _, tag := range []string{"draft-9", firstDeclaration} {
		if tag == firstDeclaration {
			e.declare(t, e.req())
		}
		if _, err := e.svc.Declare(t.Context(), e.fromDraft(tag)); refusal(t, err).Code != codeDraftNotFound {
			t.Fatal(tag, err)
		}
	}
	for name, f := range map[string]*faultyRegistry{"head": {head: errors.New("down")}, "pull": {failPull: 1}} {
		e := newDeclareEnv(t)
		e.agentDraft(t)
		e.faulty(f)
		if _, err := e.svc.Declare(t.Context(), e.fromDraft("draft-1")); refusal(t, err).Code != codeRegistryUnreadable {
			t.Fatal(name, err)
		}
	}
}

func TestFromDraftRefusesDeclaredRecord(t *testing.T) {
	e := newDeclareEnv(t)
	e.login(personToken("ana@acme.com"))
	res := e.declare(t, e.req())
	_, digest, _ := strings.Cut(res.Published, "@")
	if err := e.reg.Tag(t.Context(), e.host+"/acme/invoices", digest, "draft-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Declare(t.Context(), e.fromDraft("draft-1")); refusal(t, err).Code != codeDraftNotFound || !strings.Contains(err.Error(), "holds no draft declaration") {
		t.Fatal(err)
	}
}

func TestFromDraftSignatureChecked(t *testing.T) {
	e := newDeclareEnv(t)
	t.Setenv("SIGSTORE_ID_TOKEN", ciWorkflowToken())
	e.declare(t, e.req())
	noAmbient(t)
	e.login(personToken("ana@acme.com"))
	// A draft whose record names another asker than its signer.
	files := e.draftFiles(t, "draft-1", func(raw string, rec *contract.InferredRecord) string {
		rec.Declaration.Asker.Subject = "https://github.com/acme/other/.github/workflows/release.yml@refs/heads/main"
		return raw
	})
	ci := e.signer(t, ciWorkflowToken())
	e.publishBundle(t, e.host+"/acme/invoices", "draft-2", files, &ci)
	if _, err := e.svc.Declare(t.Context(), e.fromDraft("draft-2")); refusal(t, err).Code != codeSignerUnverified {
		t.Fatal(err)
	}
	req := e.fromDraft("draft-1")
	req.Signing.TrustedRoot = filepath.Join(t.TempDir(), "missing.json")
	if _, err := e.svc.Declare(t.Context(), req); err == nil || !strings.Contains(err.Error(), "the trusted root") {
		t.Fatal(err)
	}
}

func TestFromDraftNeedsAPerson(t *testing.T) {
	e := newDeclareEnv(t)
	e.agentDraft(t)
	t.Setenv("SIGSTORE_ID_TOKEN", ciWorkflowToken())
	if _, err := e.svc.Declare(t.Context(), e.fromDraft("draft-1")); refusal(t, err).Code != codeSignerNotAllowed {
		t.Fatal(err)
	}
	noAmbient(t)
	e.login(ciWorkflowToken()) // a login that names no person
	if _, err := e.svc.Declare(t.Context(), e.fromDraft("draft-1")); refusal(t, err).Code != codeSignerNotAllowed {
		t.Fatal(err)
	}
}

func TestFromDraftReplansVersion(t *testing.T) {
	e := newDeclareEnv(t)
	e.agentDraft(t)
	e.withBaseline(t) // the baseline moved since the draft
	e.login(personToken("ana@acme.com"))
	p, _, err := e.svc.planToDeclare(t.Context(), e.fromDraft("draft-1"))
	if err != nil || p.Baseline == nil || p.Baseline.Version != "1.4.0" || p.Version == firstDeclaration {
		t.Fatalf("%v %+v", err, p)
	}
}

func TestFromDraftOwnerRuleReapplied(t *testing.T) {
	e := newDeclareEnv(t)
	e.agentDraft(t)
	files := e.draftFiles(t, "draft-1", func(raw string, _ *contract.InferredRecord) string {
		return strings.Replace(raw, "service:\n", "service:\n  owner:\n    team: payments\n", 1)
	})
	e.publishBundle(t, e.host+"/acme/invoices", "draft-2", files, nil)
	e.login(personToken("ana@acme.com"))
	if _, err := e.svc.Declare(t.Context(), e.fromDraft("draft-2")); refusal(t, err).Code != codeOwnerNeedsApprovers {
		t.Fatal(err)
	}
}

func TestFromDraftUnchanged(t *testing.T) {
	e := newDeclareEnv(t)
	e.login(personToken("ana@acme.com"))
	e.declare(t, e.req())
	files := e.draftFiles(t, firstDeclaration, func(raw string, rec *contract.InferredRecord) string {
		rec.Declaration.State, rec.Declaration.Asker = contract.DeclarationDraft, contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent}
		return raw
	})
	e.publishBundle(t, e.host+"/acme/invoices", "draft-1", files, nil)
	res, err := e.svc.Declare(t.Context(), e.fromDraft("draft-1"))
	if err != nil || res.Executed || res.Plan.Unchanged != firstDeclaration {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestFromDraftFolderFails(t *testing.T) {
	e := newDeclareEnv(t)
	e.agentDraft(t, "ledger")
	e.login(personToken("ana@acme.com"))
	e.svc.sys.mkdirTemp = func(string, string) (string, error) { return "", errors.New("disk full") }
	if _, err := e.svc.Declare(t.Context(), e.fromDraft("draft-1")); err == nil || err.Error() != "disk full" {
		t.Fatal(err)
	}
	e.svc.sys.mkdirTemp = nil
	// A request for a dependency pacto.yaml does not declare.
	files := e.draftFiles(t, "draft-1", func(raw string, rec *contract.InferredRecord) string {
		rec.Declaration.Requested = append(rec.Declaration.Requested, contract.InferredAcceptance{Field: "dependencies[ghost].compatibility", Range: "^1.0.0"})
		return raw
	})
	e.publishBundle(t, e.host+"/acme/invoices", "draft-2", files, nil)
	if _, err := e.svc.Declare(t.Context(), e.fromDraft("draft-2")); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatal(err)
	}
}

func TestDeclareDraftRecordInvalidRefused(t *testing.T) {
	e := newDeclareEnv(t)
	// A login of no person whose token claims the unsigned issuer.
	e.login(testutil.TokenWith(map[string]string{"sub": "https://ci.example/acme/invoices", "iss": contract.SignerUnsigned, "ci_config_ref_uri": "ci.example/acme/invoices"}))
	if _, err := e.svc.Declare(t.Context(), e.req()); err == nil || !strings.Contains(err.Error(), "of an unsigned draft is exactly") || len(e.tags(t)) != 0 {
		t.Fatal(err)
	}
}
