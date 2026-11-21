package cli

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/registry"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/internal/app"
	pactoauthn "github.com/trianalab/pacto/v4/internal/authn"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// acceptE2E is a registry holding reports 1.6.0, whose record proposes
// ledger's range, a private Sigstore whose trusted root is on disk, no
// ambient token and a person whose browser login answers as ana.
type acceptE2E struct {
	*ciEnv
	ss     *testutil.Sigstore
	trust  string
	repo   string
	logins int
}

func newAcceptE2E(t *testing.T) *acceptE2E {
	t.Helper()
	for _, env := range []string{"SIGSTORE_ID_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_URL", "ACTIONS_ID_TOKEN_REQUEST_TOKEN"} {
		t.Setenv(env, "")
	}
	e := &acceptE2E{ciEnv: newCIEnv(t), ss: testutil.New(t)}
	e.trust = filepath.Join(t.TempDir(), "trusted_root.json")
	if err := os.WriteFile(e.trust, e.ss.RootJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	e.repo = e.host + "/acme/reports"
	e.publishReports(t, "1.6.0")
	return e
}

// publishReports publishes reports at version, owned by ana, proposing
// ^2.3.0 for ledger.
func (e *acceptE2E) publishReports(t *testing.T, version string) {
	t.Helper()
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Commit: "4e1a9c0d", Pacto: "4.6.0", Discovery: "1"},
		Fields: []contract.Inferred{{Field: "dependencies[ledger].compatibility", Value: "compatibility unknown", Proposed: "^2.3.0",
			Source: "snapshot:" + e.host + "/acme/pacto-target-snapshots@sha256:" + strings.Repeat("0", 64), Confidence: contract.ConfidenceLikely}}}
	y := "pactoVersion: \"2.5\"\nservice:\n  name: reports\n  version: " + version +
		"\n  owner: {team: reporting, dri: ana@acme.com}\ndependencies:\n  - name: ledger\n    required: true\n"
	c, err := contract.Parse(strings.NewReader(y))
	if err != nil {
		t.Fatal(err)
	}
	// The record rides in the bundle as pacto ci publishes it.
	b := &contract.Bundle{Contract: c, RawYAML: []byte(y), FS: fstest.MapFS{"pacto.yaml": {Data: []byte(y)},
		contract.InferredRecordFile: {Data: rec.Marshal()}}}
	if _, err := oci.NewClient(authn.DefaultKeychain, oci.WithInsecureRegistries(e.host)).Push(t.Context(), e.repo+":"+version, b); err != nil {
		t.Fatal(err)
	}
}

// deps is the CLI's deps with ana's browser login.
func (e *acceptE2E) deps() deps {
	d := defaultDeps()
	d.freshLogin = func(context.Context, pactoauthn.FreshConfig) (string, error) {
		e.logins++
		return testutil.TokenWith(map[string]string{"sub": "ana@acme.com", "email": "ana@acme.com", "email_verified": "true", "iss": e2eIssuer}), nil
	}
	return d
}

// accept runs pacto accept with the private Sigstore's flags and decodes
// its JSON answer.
func (e *acceptE2E) accept(t *testing.T, args ...string) (app.AcceptResult, *app.DeclareError, error) {
	t.Helper()
	base := []string{"accept", "oci://" + e.repo, "--fulcio-url", e.ss.Fulcio, "--timestamp-url", e.ss.TSA, "--rekor-url", "",
		"--trusted-root", e.trust, "--output-format", "json"}
	out, _, err := runRoot(t, e.deps(), nil, append(base, args...)...)
	var got struct {
		app.AcceptResult
		Refusal *app.DeclareError `json:"refusal"`
	}
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
		t.Fatalf("%v\n%s", jerr, out)
	}
	return got.AcceptResult, got.Refusal, err
}

// agentDraft plans and drafts every proposed range through pacto_accept
// over stdio and returns the plan's answer and the run's.
func (e *acceptE2E) agentDraft(t *testing.T) (pactomcp.WriteResult, pactomcp.WriteResult) {
	t.Helper()
	args := map[string]any{"service": "oci://" + e.repo, "proposed": true}
	plan, res := callTool(t, stdioDeclareServer(t), "pacto_accept", args)
	if res.IsError || plan.IntentDigest == "" || plan.Executed {
		t.Fatalf("%+v %s", plan, res.Content[0].(*mcpsdk.TextContent).Text)
	}
	args["dry_run"], args["intent_digest"], args["on_behalf_of"] = false, plan.IntentDigest, "ana"
	run, res := callTool(t, stdioDeclareServer(t), "pacto_accept", args)
	if res.IsError || !run.Executed || run.Draft == nil || run.Draft.Tag != "pacto-accept-draft-1" || run.Draft.Signed {
		t.Fatalf("%+v %s", run, res.Content[0].(*mcpsdk.TextContent).Text)
	}
	return plan, run
}

// The same plan drafts through pacto_accept and signs through pacto accept
// --draft, with the same intent digest.
func TestAcceptAsAgentAndAsPerson(t *testing.T) {
	e := newAcceptE2E(t)
	plan, run := e.agentDraft(t)
	draft := e.repo + ":pacto-accept-draft-1"
	if run.AcceptCLI != "pacto accept oci://"+e.repo+" --draft "+draft {
		t.Fatal(run.AcceptCLI)
	}
	res, refusal, err := e.accept(t, "--draft", draft)
	if err != nil || refusal != nil || !res.Executed || res.Plan.IntentDigest != plan.IntentDigest || res.Signer.Subject != "ana@acme.com" ||
		res.State != impact.AcceptCounted || res.Draft != "" || res.Plan.OnBehalfOf != "ana" || e.logins != 1 {
		t.Fatalf("%v %+v %+v", err, refusal, res)
	}
}

func TestAcceptDraftSignsAsPerson(t *testing.T) {
	e := newAcceptE2E(t)
	e.agentDraft(t)
	out, _, err := runRoot(t, e.deps(), nil, "accept", "oci://"+e.repo, "--draft", e.repo+":pacto-accept-draft-1", "--dry-run")
	if err != nil || !strings.Contains(out, "draft: "+e.repo+":pacto-accept-draft-1, on behalf of ana") ||
		!strings.Contains(out, "note: unsigned draft, proposer asserted") || e.logins != 0 {
		t.Fatalf("%v\n%s", err, out)
	}
	res, _, err := e.accept(t, "--draft", e.repo+":pacto-accept-draft-1")
	if err != nil || res.Record == "" || !strings.HasPrefix(res.Record, e.repo+"@sha256:") {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestAcceptDraftStale(t *testing.T) {
	e := newAcceptE2E(t)
	e.agentDraft(t)
	e.publishReports(t, "1.7.0")
	_, refusal, err := e.accept(t, "--draft", e.repo+":pacto-accept-draft-1")
	if err == nil || refusal == nil || refusal.Code != "PlanStale" || e.logins != 0 {
		t.Fatalf("%v %+v", err, refusal)
	}
}

func TestAcceptDraftNonHumanRefused(t *testing.T) {
	e := newAcceptE2E(t)
	e.agentDraft(t)
	const wf = "acme/reports/.github/workflows/accept.yml@refs/heads/main"
	t.Setenv("SIGSTORE_ID_TOKEN", testutil.TokenWith(map[string]string{"sub": "https://github.com/" + wf, "iss": "https://token.actions.githubusercontent.com",
		"job_workflow_ref": wf, "repository": "https://github.com/acme/reports", "ref": "refs/heads/main"}))
	_, refusal, err := e.accept(t, "--draft", e.repo+":pacto-accept-draft-1")
	if err == nil || refusal == nil || refusal.Code != "PersonSigningUnavailable" ||
		refusal.CLI != "pacto accept "+e.repo+" --draft "+e.repo+":pacto-accept-draft-1" || e.logins != 0 {
		t.Fatalf("%v %+v", err, refusal)
	}
}

// TestMCPImpactHTTPReadsNoAcceptances: over stdio pacto_impact reads the
// acceptances attached to an announced consumer; an HTTP server never does,
// since every caller would read them as this process.
func TestMCPImpactHTTPReadsNoAcceptances(t *testing.T) {
	ciEnvOff(t)
	statsOff(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("PACTO_NO_UPDATE_CHECK", "1")
	var reads atomic.Int32
	reg := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/acme/web/") && (strings.Contains(r.URL.Path, "/referrers/") || strings.HasSuffix(r.URL.Path, "/tags/list")) {
			reads.Add(1)
		}
		reg.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	e := &ciEnv{root: t.TempDir(), host: strings.TrimPrefix(srv.URL, "http://")}
	t.Setenv("PACTO_INSECURE_REGISTRIES", e.host)
	e.write(t, map[string]string{
		"payments/pacto.yaml":   "service: {name: payments, version: 1.0.0}\ninterfaces:\n  - name: api\n    type: openapi\n    ref: openapi.yaml\n",
		"payments/openapi.yaml": "openapi: 3.0.0\ninfo: {title: payments, version: \"1\"}\npaths: {}\n",
		"web/pacto.yaml":        consumerYAML("web", e.host),
	})
	e.publish(t, "payments", "1.0.0")
	e.publish(t, "web", "1.0.0")
	e.write(t, map[string]string{"payments/pacto.yaml": "pactoVersion: \"2.5\"\nservice: {name: payments, version: 2.0.0}\n" +
		"breaks:\n  - consumers: [web]\n    reason: the api is removed\n    expires: \"2099-01-01\"\n"})
	e.publish(t, "payments", "2.0.0")
	svc := app.NewService(oci.NewClient(authn.DefaultKeychain, oci.WithInsecureRegistries(e.host)), nil)
	ref := func(s string) string { return "oci://" + e.host + "/acme/" + s }
	snap, err := svc.Fleet(t.Context(), app.FleetOptions{OCIRefs: []string{ref("payments:1.0.0"), ref("payments:2.0.0"), ref("web:1.0.0")}})
	if err != nil {
		t.Fatal(err)
	}
	req := pactomcp.ImpactRequest{OldRef: ref("payments:1.0.0"), NewRef: ref("payments:2.0.0")}
	for transport, trusted := range map[string]bool{"stdio": true, "http": false} {
		reads.Store(0)
		res, err := mcpProvider(t, svc, transport, false, trusted)(t.Context(), snap, req)
		if err != nil {
			t.Fatalf("%s: %v", transport, err)
		}
		if (reads.Load() > 0) != trusted {
			t.Errorf("%s: %d acceptance reads", transport, reads.Load())
		}
		for _, c := range res.Consumers {
			if c.Break != nil && c.Break.State == impact.BreakAccepted {
				t.Errorf("%s: %+v", transport, c.Break)
			}
		}
		// The unread listing is said, never a checked "not accepted".
		unread := slices.ContainsFunc(res.Notes, func(n string) bool { return strings.HasPrefix(n, impact.NoteAcceptancesNotRead) })
		if unread == trusted {
			t.Errorf("%s: notes %q", transport, res.Notes)
		}
	}
}
