package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// ciPubEnv is a repository root, an in-memory registry behind a hookable
// one, a key every service signs with and the Service publishing through it.
type ciPubEnv struct {
	root, host, key string
	inner           publish.Registry
	reg             *hookRegistry
	run             *fakeRun
	svc             *Service
	records         []string
}

// hookRegistry counts the writes reaching the registry and lets a test act
// when a tag is written: tag returns true when it handled the write itself.
type hookRegistry struct {
	publish.Registry
	writes atomic.Int64
	tag    func(repo, digest, tag string) bool
}

func (h *hookRegistry) Write(ctx context.Context, repo string, a publish.Artifact) (string, error) {
	h.writes.Add(1)
	return h.Registry.Write(ctx, repo, a)
}

func (h *hookRegistry) Tag(ctx context.Context, repo, digest, tag string) error {
	h.writes.Add(1)
	if h.tag != nil && h.tag(repo, digest, tag) {
		return nil
	}
	return h.Registry.Tag(ctx, repo, digest, tag)
}

func newCIPub(t *testing.T) *ciPubEnv {
	t.Helper()
	host, inner := publishRegistry(t)
	keys := t.TempDir()
	keyFiles(t, keys)
	e := &ciPubEnv{root: t.TempDir(), host: host, key: filepath.Join(keys, "cosign.key"), inner: inner, reg: &hookRegistry{Registry: inner}, run: &fakeRun{}}
	e.svc = publishService(e.run, e.reg)
	return e
}

// contract is a 2.5 contract for name at version ("" leaves it undeclared)
// with extra top-level YAML, publishing its bundle to host/acme/<name>,
// signed with the key, plus pub inside the publish section.
func (e *ciPubEnv) contract(name, version, extra, pub string) string {
	y := "pactoVersion: " + strconv.Quote(contract.Current()) + "\nservice:\n  name: " + name + "\n"
	if version != "" {
		y += "  version: " + version + "\n"
	}
	return y + extra + "publish:\n  bundle: oci://" + e.host + "/acme/" + name + "\n  sign: {key: " + e.key + ", transparencyLog: false}\n" + pub
}

// deployPub is a publish section part with a manifests deploy artifact
// rendered by ./render.sh.
func (e *ciPubEnv) deployPub(name string) string {
	return "  deploy: manifests\n  artifacts:\n    - {name: manifests, type: manifests, ref: \"oci://" + e.host + "/acme/deploy/" + name + "\", build: {command: [./render.sh]}}\n"
}

func (e *ciPubEnv) write(t *testing.T, files map[string]string) {
	t.Helper()
	writeFiles(t, e.root, files)
}

// publishAs publishes the folder rel on its own, as revision rev.
func (e *ciPubEnv) publishAs(t *testing.T, rel, rev string) {
	t.Helper()
	if _, err := publishService(&fakeRun{}, e.inner).Publish(t.Context(), PublishOptions{Path: filepath.Join(e.root, rel), Revision: rev}); err != nil {
		t.Fatal(err)
	}
}

// ciPublish runs pacto ci --publish over the root in env, recording one
// "<service> <state> <outcome>" line per decided row.
func (e *ciPubEnv) ciPublish(t *testing.T, env map[string]string) *ci.Plan {
	t.Helper()
	p, err := e.svc.CIPublish(t.Context(), CIPublishOptions{CI: CIOptions{Root: e.root, Registry: e.host + "/acme", Getenv: mapEnv(env)}, Revision: "r2",
		Record: func(r ci.Row, res *publish.Result, err error) {
			e.records = append(e.records, fmt.Sprintf("%s %s %v", r.Service, r.State, err != nil))
		}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mapEnv(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

// pubRow is the row of service in p.
func pubRow(t *testing.T, p *ci.Plan, service string) ci.Row {
	t.Helper()
	for _, r := range p.Rows {
		if r.Service == service {
			return r
		}
	}
	t.Fatalf("no row %s in %+v", service, p.Rows)
	return ci.Row{}
}

func services(p *ci.Plan) []string {
	var out []string
	for _, r := range p.Rows {
		out = append(out, r.Service)
	}
	return out
}

func TestCIPublishRefusesOnPullRequest(t *testing.T) {
	var tokens atomic.Int64
	srv := probedServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { tokens.Add(1) }))
	t.Cleanup(srv.Close)
	t.Setenv("SIGSTORE_ID_TOKEN", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", srv.URL+"?x=1")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "t")
	for name, env := range map[string]map[string]string{
		"github": {"GITHUB_ACTIONS": "true", "GITHUB_BASE_REF": "main"},
		"gitlab": {"GITLAB_CI": "true", "CI_MERGE_REQUEST_DIFF_BASE_SHA": "0123456789abcdef"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newCIPub(t)
			e.write(t, map[string]string{"payments/pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: payments\n", "payments/Dockerfile": "FROM scratch\n"})
			p := e.ciPublish(t, env)
			r := pubRow(t, p, "payments")
			if p.Exit != 1 || p.Refused != RefusedPullRequest || p.Reason != "refused: "+RefusedPullRequest || r.State != ci.StateRefused || r.Reason != RefusedPullRequest {
				t.Fatalf("plan %+v", p)
			}
			if e.reg.writes.Load() != 0 || tokens.Load() != 0 || len(e.run.calls) != 0 || !slices.Equal(e.records, []string{"payments refused false"}) {
				t.Fatalf("writes %d tokens %d calls %v records %v", e.reg.writes.Load(), tokens.Load(), e.run.calls, e.records)
			}
			_, err := e.svc.Publish(t.Context(), PublishOptions{Path: filepath.Join(e.root, "payments"), Getenv: mapEnv(env)})
			if err == nil || err.Error() != "refused: "+RefusedPullRequest {
				t.Fatalf("pacto publish: %v", err)
			}
		})
	}
}

// ledgerReports writes ledger and reports, reports depending on ledger by
// name with compatibility, in folders whose order is the reverse of the
// dependency order.
func ledgerReports(t *testing.T, e *ciPubEnv, compatibility string) {
	t.Helper()
	e.write(t, map[string]string{
		"a/pacto.yaml": e.contract("reports", "1.0.0", "dependencies:\n  - name: ledger\n    ref: oci://"+e.host+"/acme/ledger\n    compatibility: \""+compatibility+"\"\n", ""),
		"b/pacto.yaml": e.contract("ledger", "1.0.0", "", ""),
	})
}

func TestCIPublishDependencyOrder(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	ledgerReports(t, e, "^1")
	p := e.ciPublish(t, nil)
	if !slices.Equal(services(p), []string{"ledger", "reports"}) || p.Exit != 0 {
		t.Fatalf("order %v exit %d %+v", services(p), p.Exit, p.Rows)
	}
	for _, r := range p.Rows {
		if r.State != ci.StatePublished || r.To != "1.0.0" || r.Identity != "key "+e.key || !strings.HasPrefix(r.Published, e.host+"/acme/"+r.Service+":1.0.0@sha256:") || r.Attempts != 1 {
			t.Fatalf("row %+v", r)
		}
	}
}

func TestCIPublishCycleInNameOrder(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	e.write(t, map[string]string{
		"x/pacto.yaml": e.contract("zeta", "1.0.0", "dependencies:\n  - name: alpha\n    ref: oci://"+e.host+"/acme/alpha\n    compatibility: \"^1\"\n", ""),
		"y/pacto.yaml": e.contract("alpha", "1.0.0", "dependencies:\n  - name: zeta\n    ref: oci://"+e.host+"/acme/zeta\n    compatibility: \"^1\"\n", ""),
	})
	p := e.ciPublish(t, nil)
	if !slices.Equal(services(p), []string{"alpha", "zeta"}) || !slices.Contains(p.Notes, "dependency cycle: alpha, zeta (published in name order)") {
		t.Fatalf("order %v notes %v", services(p), p.Notes)
	}
}

func TestCIPublishFailureBlocksOnlyDependents(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	ledgerReports(t, e, "^1")
	e.write(t, map[string]string{
		"b/pacto.yaml": e.contract("ledger", "1.0.0", "", e.deployPub("ledger")),
		"c/pacto.yaml": e.contract("payments", "1.0.0", "", ""),
	})
	e.run.fail = "./render.sh"
	p := e.ciPublish(t, nil)
	if l := pubRow(t, p, "ledger"); l.State != ci.StateFailed || !strings.Contains(l.Reason, "injected: ./render.sh") {
		t.Fatalf("ledger %+v", l)
	}
	if r := pubRow(t, p, "reports"); r.State != ci.StateBlocked || r.Reason != "depends on ledger 1.0.0, which failed to publish" {
		t.Fatalf("reports %+v", r)
	}
	if r := pubRow(t, p, "payments"); r.State != ci.StatePublished || p.Exit != 1 || p.Reason != "not published: 2 of 3 services" {
		t.Fatalf("payments %+v exit %d %q", r, p.Exit, p.Reason)
	}
}

// breakingLedger publishes ledger 2.3.0 with an interface, then removes it
// at 3.0.0, with reports depending on ledger within compatibility.
func breakingLedger(t *testing.T, compatibility string) *ci.Plan {
	t.Helper()
	e := newCIPub(t)
	ledgerReports(t, e, compatibility)
	e.write(t, map[string]string{"b/pacto.yaml": e.contract("ledger", "2.3.0", interfaceYAML, ""), "b/openapi.yaml": openAPIFile})
	e.publishAs(t, "b", "r1")
	e.write(t, map[string]string{"b/pacto.yaml": e.contract("ledger", "3.0.0", "", "")})
	return e.ciPublish(t, nil)
}

func TestCIPublishBreakingBlocksUnadaptedConsumer(t *testing.T) {
	t.Parallel()
	p := breakingLedger(t, "^2")
	if l := pubRow(t, p, "ledger"); l.State != ci.StatePublished || l.Classification != "BREAKING" {
		t.Fatalf("ledger %+v", l)
	}
	if r := pubRow(t, p, "reports"); r.State != ci.StateBlocked || r.Reason != "depends on ledger 3.0.0, which is BREAKING for it" {
		t.Fatalf("reports %+v", r)
	}
}

func TestCIPublishBreakingAdaptedConsumerPublishes(t *testing.T) {
	t.Parallel()
	p := breakingLedger(t, "^3")
	if r := pubRow(t, p, "reports"); r.State != ci.StatePublished || p.Exit != 0 {
		t.Fatalf("reports %+v", r)
	}
}

func TestCIPublishUnknownRangeBlocks(t *testing.T) {
	t.Parallel()
	p := breakingLedger(t, "unknown")
	if r := pubRow(t, p, "reports"); r.State != ci.StateBlocked || r.Reason != "depends on ledger 3.0.0, which is BREAKING; its range is unknown" {
		t.Fatalf("reports %+v", r)
	}
}

// racing publishes payments 1.4.2, then leaves its version undeclared with a
// new file, and makes the registry lose the payments bundle tag to a
// derived publish of a copy of the folder, at most limit times.
func racing(t *testing.T, limit int) *ciPubEnv {
	t.Helper()
	e := newCIPub(t)
	e.write(t, map[string]string{"payments/pacto.yaml": e.contract("payments", "1.4.2", "", "")})
	e.publishAs(t, "payments", "r1")
	e.write(t, map[string]string{"payments/pacto.yaml": e.contract("payments", "", "", ""), "payments/notes.md": "# payments\n"})
	winner := t.TempDir()
	writeFiles(t, winner, map[string]string{"pacto.yaml": e.contract("payments", "", "", "")})
	raced := 0
	e.reg.tag = func(repo, _, tag string) bool {
		if repo != e.host+"/acme/payments" || raced == limit {
			return false
		}
		raced++
		writeFiles(t, winner, map[string]string{"winner.md": fmt.Sprint(raced)})
		res, err := publishService(&fakeRun{}, e.inner).Publish(t.Context(), PublishOptions{Path: winner, Revision: "rw"})
		if err != nil || res.Plan.Version != tag {
			t.Fatalf("winner %v %+v, want %s", err, res, tag)
		}
		return true
	}
	return e
}

func TestCIPublishReplansOnLostRace(t *testing.T) {
	t.Parallel()
	e := racing(t, 1)
	r := pubRow(t, e.ciPublish(t, nil), "payments")
	if r.State != ci.StatePublished || r.To != "1.4.4" || r.Baseline != "1.4.3" || r.Attempts != 2 || !slices.Contains(r.Notes, "payments 1.4.3 published by another run; re-planned") {
		t.Fatalf("row %+v", r)
	}
}

func TestCIPublishGivesUpAfterThreeAttempts(t *testing.T) {
	t.Parallel()
	e := racing(t, 3)
	r := pubRow(t, e.ciPublish(t, nil), "payments")
	if r.State != ci.StateFailed || r.Attempts != 3 || !strings.Contains(r.Reason, publish.ErrTagRace.Error()) {
		t.Fatalf("row %+v", r)
	}
}

func TestCIPublishReplanSupersededByNewerRun(t *testing.T) {
	t.Parallel()
	e := racing(t, 1)
	e.run.fail = "git merge-base"
	r := pubRow(t, e.ciPublish(t, nil), "payments")
	if r.State != ci.StateFailed || r.Reason != "superseded: payments 1.4.3 was published from rw, which this checkout does not contain" || r.Attempts != 2 {
		t.Fatalf("row %+v", r)
	}
}

func TestCIPublishDeclaredVersionNeverReplans(t *testing.T) {
	t.Parallel()
	e := racing(t, 1)
	e.write(t, map[string]string{"payments/pacto.yaml": e.contract("payments", "1.4.3", "", "")})
	r := pubRow(t, e.ciPublish(t, nil), "payments")
	if r.State != ci.StateFailed || r.Attempts != 1 || !strings.Contains(r.Reason, publish.ErrTagRace.Error()) {
		t.Fatalf("row %+v", r)
	}
}

func TestCIPublishForeignSignatureNeverReplans(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	e.write(t, map[string]string{"payments/pacto.yaml": e.contract("payments", "", "", e.deployPub("payments"))})
	// Something unsigned holds 1.0.0, claiming this run's version and revision.
	img, _ := random.Image(32, 1)
	ann := mutate.Annotations(img, map[string]string{publish.AnnotationVersion: "1.0.0", publish.AnnotationRevision: "r2"}).(v1.Image)
	repo := e.host + "/acme/deploy/payments"
	d, err := e.inner.Write(t.Context(), repo, ann)
	if err == nil {
		err = e.inner.Tag(t.Context(), repo, d, "1.0.0")
	}
	if err != nil {
		t.Fatal(err)
	}
	r := pubRow(t, e.ciPublish(t, nil), "payments")
	if r.State != ci.StateRefused || r.Attempts != 1 || !strings.Contains(r.Reason, "no signature by the configured identity") {
		t.Fatalf("row %+v", r)
	}
}

func TestCIPublishFinalReadBackCatchesLateOverwrite(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	e.write(t, map[string]string{"ledger/pacto.yaml": e.contract("ledger", "1.0.0", "", e.deployPub("ledger"))})
	bundleRepo := e.host + "/acme/ledger"
	other := pushImage(t, e.inner, bundleRepo)
	// Right after the deploy tag, another run rewrites the bundle tag.
	e.reg.tag = func(repo, digest, tag string) bool {
		if repo != e.host+"/acme/deploy/ledger" {
			return false
		}
		_, d, _ := strings.Cut(other, "@")
		if err := e.inner.Tag(t.Context(), repo, digest, tag); err != nil {
			t.Fatal(err)
		}
		if err := e.inner.Tag(t.Context(), bundleRepo, d, tag); err != nil {
			t.Fatal(err)
		}
		return true
	}
	p := e.ciPublish(t, nil)
	r := pubRow(t, p, "ledger")
	if r.State != ci.StateFailed || !strings.Contains(r.Reason, publish.ErrTagRace.Error()) || !strings.Contains(r.Reason, bundleRepo+":1.0.0 no longer holds") || r.Published != "" || p.Exit != 1 {
		t.Fatalf("row %+v", r)
	}
	if !slices.Equal(e.records, []string{"ledger failed true"}) {
		t.Fatalf("records %v", e.records)
	}
}

func TestCIPublishStaleRunRefused(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	e.write(t, map[string]string{"payments/pacto.yaml": e.contract("payments", "", "", "")})
	e.publishAs(t, "payments", "r1") // derived 1.0.0, its record naming r1
	e.write(t, map[string]string{"payments/notes.md": "# payments\n"})
	before := e.reg.writes.Load()
	e.run.fail = "git merge-base"
	r := pubRow(t, e.ciPublish(t, nil), "payments")
	if r.State != ci.StateFailed || r.Reason != "superseded: payments 1.0.0 was published from r1, which this checkout does not contain" || e.reg.writes.Load() != before {
		t.Fatalf("row %+v writes %d", r, e.reg.writes.Load()-before)
	}
	if got := ci.Markdown(&ci.Plan{Rows: []ci.Row{r}}); !strings.Contains(got, "`payments  superseded: payments 1.0.0") {
		t.Fatalf("markdown:\n%s", got)
	}
}

func TestCIPublishBehindIsAPlanLine(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	// A declared build is deprecated from 2.5 and still honoured: the raise
	// keeps its meaning, so the contract publishes as the current version.
	e.write(t, map[string]string{"legacy/pacto.yaml": strings.Replace(e.contract("legacy", "1.0.0", "", e.deployPub("legacy")), strconv.Quote(contract.Current()), `"2.3"`, 1), "legacy/render.sh": "#!/bin/sh\n"})
	p := e.ciPublish(t, nil)
	r := pubRow(t, p, "legacy")
	if r.State != ci.StatePublished || !slices.Equal(r.Notes, []string{"pacto.yaml is 2.3, published as " + contract.Current() + " (migrated in memory)"}) || p.Exit != 0 {
		t.Fatalf("row %+v", r)
	}
}

func TestCIPublishBehindWithoutPublishFailsThatServiceOnly(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	e.write(t, map[string]string{
		// An escaped version is not raised in memory: it stays behind.
		"legacy/pacto.yaml":   "pactoVersion: \"2\\x2E3\"\nservice:\n  name: legacy\n  version: 1.0.0\n",
		"payments/pacto.yaml": e.contract("payments", "1.0.0", "", ""),
	})
	p := e.ciPublish(t, nil)
	if r := pubRow(t, p, "legacy"); r.State != ci.StateFailed || !strings.HasPrefix(r.Reason, "declares no publish section, and pactoVersion stays at 2.3 (parse error at pactoVersion: ") {
		t.Fatalf("legacy %+v", r)
	}
	if r := pubRow(t, p, "payments"); r.State != ci.StatePublished || p.Exit != 1 {
		t.Fatalf("payments %+v", r)
	}
}

func TestCIPublishUnchangedNotRepublished(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	e.write(t, map[string]string{"payments/pacto.yaml": e.contract("payments", "", "", "")})
	e.publishAs(t, "payments", "r1")
	before := e.reg.writes.Load()
	p := e.ciPublish(t, nil)
	if r := pubRow(t, p, "payments"); r.State != ci.StateUnchanged || e.reg.writes.Load() != before || p.Exit != 0 {
		t.Fatalf("row %+v writes %d", r, e.reg.writes.Load()-before)
	}
}

// TestPublishEqualsOneCIRow: pacto publish --plan on one folder and its
// pacto ci --publish row agree on the version and the bundle, and an
// unchanged folder is unchanged in both.
func TestPublishEqualsOneCIRow(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	e.write(t, map[string]string{"payments/pacto.yaml": e.contract("payments", "1.4.2", "", "")})
	e.publishAs(t, "payments", "r1")
	e.write(t, map[string]string{"payments/pacto.yaml": e.contract("payments", "", "", ""), "payments/notes.md": "# payments\n"})
	plan, err := e.svc.Publish(t.Context(), PublishOptions{Path: filepath.Join(e.root, "payments"), Revision: "r2", Plan: true})
	if err != nil || !plan.Plan.Ready {
		t.Fatalf("%v %+v", err, plan)
	}
	r := pubRow(t, e.ciPublish(t, nil), "payments")
	bundle := plan.Plan.Items[len(plan.Plan.Items)-1]
	if r.State != ci.StatePublished || r.To != plan.Plan.Version || r.Published != bundle.Ref+"@"+bundle.Digest || r.Baseline != plan.Plan.Baseline {
		t.Fatalf("plan %+v row %+v", plan.Plan, r)
	}
	again, err := e.svc.Publish(t.Context(), PublishOptions{Path: filepath.Join(e.root, "payments"), Revision: "r3", Plan: true})
	if err != nil || len(again.Plan.Checks) != 1 || !strings.HasPrefix(again.Plan.Checks[0].Message, "unchanged since") || again.Index != "" {
		t.Fatalf("%v %+v", err, again)
	}
	if r := pubRow(t, e.ciPublish(t, nil), "payments"); r.State != ci.StateUnchanged {
		t.Fatalf("row %+v", r)
	}
}

func TestCIPublishImageFlag(t *testing.T) {
	t.Parallel()
	rows := []ci.Row{{Service: "a", Changed: "yes"}, {Service: "b", Changed: "yes"}, {Service: "c", Changed: "no"}}
	ref := "r.io/x@" + digest64
	got, err := rowImages(rows, map[string]string{"a": ref})
	if err != nil || got["a"] != ref {
		t.Fatalf("%v %v", err, got)
	}
	if _, err := rowImages(rows, map[string]string{"": ref}); err == nil || !strings.Contains(err.Error(), "needs exactly one changed service; 2 changed") {
		t.Fatal(err)
	}
	if _, err := rowImages(rows, map[string]string{"c": ref}); err == nil || !strings.Contains(err.Error(), "not changed in this run") {
		t.Fatal(err)
	}
	if got, err := rowImages(rows[1:], map[string]string{"": ref}); err != nil || got["b"] != ref {
		t.Fatalf("%v %v", err, got)
	}
	for range 20 { // map order is random: the conflict is refused every time
		if _, err := rowImages(rows[1:], map[string]string{"": ref, "b": "r.io/y@" + digest64}); err == nil || !strings.Contains(err.Error(), "both name b") {
			t.Fatal(err)
		}
	}
}

// TestCIPublishDerivesASection: a section-less service publishes with the
// tool its folder uses, signed with --sign-key, under --registry.
func TestCIPublishDerivesASection(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	e.svc.withRun(&buildRun{})
	e.write(t, map[string]string{
		"payments/pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: payments\n  version: 1.0.0\n",
		"payments/.ko.yaml":   "builds:\n  - main: ./cmd/api\n", "payments/k8s/deploy.yaml": deployment(e.host + "/acme/payments:1.0"),
	})
	p, err := e.svc.CIPublish(t.Context(), CIPublishOptions{CI: CIOptions{Root: e.root, Registry: e.host + "/acme"}, Revision: "r1", SignKey: e.key, NoTransparencyLog: true})
	if err != nil {
		t.Fatal(err)
	}
	if r := pubRow(t, p, "payments"); r.State != ci.StatePublished || r.Tool != "ko" || r.Identity != "key "+e.key {
		t.Fatalf("row %+v", r)
	}
}

func TestCIPublishRunErrors(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	if _, err := e.svc.CIPublish(t.Context(), CIPublishOptions{CI: CIOptions{Root: filepath.Join(e.root, "nope")}}); err == nil {
		t.Fatal("a missing root is an error")
	}
	e.write(t, map[string]string{"a/pacto.yaml": e.contract("a", "1.0.0", "", ""), "b/pacto.yaml": e.contract("b", "1.0.0", "", "")})
	_, err := e.svc.CIPublish(t.Context(), CIPublishOptions{CI: CIOptions{Root: e.root, Registry: e.host + "/acme"}, Images: map[string]string{"": "r.io/x@" + digest64}})
	if err == nil || !strings.Contains(err.Error(), "2 changed") || e.reg.writes.Load() != 0 {
		t.Fatalf("%v writes %d", err, e.reg.writes.Load())
	}
}

// TestCIPublishUndecidedProviderBlocks: a provider the plan could not judge
// is not published, and holds the services of the run that depend on it.
func TestCIPublishUndecidedProviderBlocks(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	ledgerReports(t, e, "^1")
	e.write(t, map[string]string{"b/pacto.yaml": e.contract("ledger", "1.0.0", "dependencies:\n  - name: x\n    ref: oci://"+e.host+"/acme/x\n", ""), "c/pacto.yaml": "{"})
	p := e.ciPublish(t, nil)
	if l := pubRow(t, p, "ledger"); l.State != ci.StateFailed || !strings.HasPrefix(l.Reason, "not published: blocked (invalid") {
		t.Fatalf("ledger %+v", l)
	}
	if r := pubRow(t, p, "reports"); r.State != ci.StateBlocked || r.Reason != "depends on ledger, which failed to publish" {
		t.Fatalf("reports %+v", r)
	}
}

func TestToolOf(t *testing.T) {
	t.Parallel()
	art := func(a contract.PublishArtifact) *contract.Bundle {
		return &contract.Bundle{Contract: &contract.Contract{Publish: &contract.Publish{Artifacts: []contract.PublishArtifact{{Name: "m", Type: contract.ArtifactManifests}, a}}}}
	}
	for want, b := range map[string]*contract.Bundle{
		"docker buildx": art(contract.PublishArtifact{Type: contract.ArtifactImage, Build: contract.PublishBuild{Dockerfile: &contract.DockerfileBuild{}}}),
		"./build.sh":    art(contract.PublishArtifact{Type: contract.ArtifactImage, Build: contract.PublishBuild{Command: []string{"./build.sh"}}}),
		"":              {Contract: &contract.Contract{}},
	} {
		if got := toolOf(b, publish.Plan{Items: []publish.Item{{Type: contract.ArtifactImage, State: publish.Absent}}}); got != want {
			t.Errorf("toolOf = %q, want %q", got, want)
		}
	}
	// A resume that adopts the image an earlier run pushed builds nothing.
	b := art(contract.PublishArtifact{Type: contract.ArtifactImage, Build: contract.PublishBuild{Command: []string{"./build.sh"}}})
	if got := toolOf(b, publish.Plan{Items: []publish.Item{{Type: contract.ArtifactImage, State: publish.Adopt}}}); got != "" {
		t.Errorf("toolOf on a resume = %q", got)
	}
}

func TestFillRowAlreadyPublished(t *testing.T) {
	t.Parallel()
	r := ci.Row{}
	fillRow(&r, nil, &publish.Result{Plan: publish.Plan{Version: "1.0.0"}}, nil)
	if r.State != ci.StateUnchanged || r.Reason != "already published" || r.To != "1.0.0" {
		t.Fatalf("%+v", r)
	}
}

func TestAcceptsVersion(t *testing.T) {
	t.Parallel()
	if !acceptsVersion("", "9.0.0") || acceptsVersion("not a range", "1.0.0") || acceptsVersion("^1", "2.0.0") || !acceptsVersion("^2", "2.1.0") {
		t.Fatal("acceptsVersion")
	}
}

func TestPublishOrderOutsideProvider(t *testing.T) {
	t.Parallel()
	rows := []ci.Row{{Service: "b", Dir: "b"}, {Service: "a", Dir: "a"}}
	dep := &contract.Bundle{Contract: &contract.Contract{Dependencies: []contract.Dependency{{Name: "stripe", Ref: "oci://r.io/stripe"}}}}
	order, note := publishOrder(rows, map[string]*contract.Bundle{"b": dep})
	if !slices.Equal(order, []int{1, 0}) || note != "" {
		t.Fatalf("%v %q", order, note)
	}
}

// headErr is a registry whose Head fails.
type headErr struct{ publish.Registry }

func (headErr) Head(context.Context, string) (*publish.Remote, error) { return nil, errors.New("down") }

func TestReadBackUnreadable(t *testing.T) {
	t.Parallel()
	res := &publish.Result{Plan: publish.Plan{Items: []publish.Item{{Name: "bundle", Ref: "r.io/a:1.0.0"}}}}
	if err := readBack(t.Context(), headErr{}, res); err == nil || err.Error() != "reading r.io/a:1.0.0 back: down" {
		t.Fatal(err)
	}
}

func TestExecToolsSigner(t *testing.T) {
	t.Parallel()
	tools := newExecTools("", nil, nil)
	kms := contract.PublishSign{KMS: "awskms:///k"}
	if tools.signer(kms) != "" {
		t.Fatal("a signer that never loaded names nobody")
	}
	tools.signers[kms] = loadedSigner{}
	if got := tools.signer(kms); got != "kms awskms:///k" {
		t.Fatal(got)
	}
	// A reusable workflow's policy keeps a signature any workflow of the
	// caller wrote on that ref: the row never names the current pin as its
	// signer.
	keyless := contract.PublishSign{Keyless: true}
	tools.signers[keyless] = loadedSigner{p: signing.Policy{Identity: "https://github.com/TrianaLab/pacto-actions/.github/workflows/ci.yml@b",
		Repository: "https://github.com/acme/app", Ref: "refs/heads/main"}}
	if got := tools.signer(keyless); got != "keyless (repository https://github.com/acme/app at refs/heads/main)" {
		t.Fatal(got)
	}
}

// TestPublishRecordsImageDigests: a ko service and an --image service each
// leave one images record on their bundle, listing the image the deploy
// pins with where it came from, signed by the publisher.
func TestPublishRecordsImageDigests(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	e.svc.withRun(&buildRun{})
	supplied := e.host + "/other/ledger@" + digest64
	e.write(t, map[string]string{
		"payments/pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: payments\n  version: 1.0.0\n",
		"payments/.ko.yaml":   "builds:\n  - main: ./cmd/api\n", "payments/k8s/deploy.yaml": deployment(e.host + "/acme/payments:1.0"),
		"ledger/pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: ledger\n  version: 1.0.0\n",
		"ledger/Dockerfile": "FROM scratch\n", "ledger/k8s/deploy.yaml": deployment("r.io/ledger:1"),
	})
	p, err := e.svc.CIPublish(t.Context(), CIPublishOptions{CI: CIOptions{Root: e.root, Registry: e.host + "/acme"}, Revision: "r1",
		SignKey: e.key, NoTransparencyLog: true, Images: map[string]string{"ledger": supplied}})
	if err != nil {
		t.Fatal(err)
	}
	off := false
	tools := newExecTools(e.root, nil, e.inner)
	for service, source := range map[string]string{"payments": publish.SourceBuilt, "ledger": "flag:--image"} {
		r := pubRow(t, p, service)
		bundle, digest, _ := strings.Cut(r.Published, "@")
		repo := bundle[:strings.LastIndex(bundle, ":")]
		have, err := e.inner.Referrers(t.Context(), repo, digest, publish.ImagesType)
		if err != nil || len(have) != 1 || len(r.Images) != 1 {
			t.Fatalf("%s: %v %v row %+v", service, have, err, r)
		}
		body, _, err := e.inner.Layer(t.Context(), repo, have[0])
		img, _, _ := strings.Cut(r.Images[0], "@")
		if err != nil || !strings.Contains(string(body), `"source":"`+source+`"`) || !strings.Contains(string(body), `"repository":"`+img+`"`) {
			t.Fatalf("%s: %s %v", service, body, err)
		}
		if err := tools.Verify(t.Context(), contract.PublishSign{Key: e.key, TransparencyLog: &off}, repo+"@"+have[0]); err != nil {
			t.Fatalf("%s: the images record does not verify: %v", service, err)
		}
	}
	if r := pubRow(t, p, "ledger"); r.Images[0] != supplied {
		t.Fatalf("ledger images %v", r.Images)
	}
}

// TestCIPlanReadsTheDerivedBundleRepository: a section-less folder whose
// workload image lives at <registry>/<name> is planned against the
// repository its derived section publishes to, <registry>/contracts/<name>,
// never against the image.
func TestCIPlanReadsTheDerivedBundleRepository(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	e.write(t, map[string]string{
		"ledger/pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: ledger\n",
		"ledger/.ko.yaml":   "builds:\n  - main: ./cmd/api\n", "ledger/k8s/deploy.yaml": deployment(e.host + "/acme/ledger:1.0"),
	})
	p, _, err := e.svc.CIPlan(t.Context(), CIOptions{Root: e.root, Registry: e.host + "/acme"})
	if err != nil {
		t.Fatal(err)
	}
	if r := pubRow(t, p, "ledger"); r.Repository != e.host+"/acme/contracts/ledger" {
		t.Fatalf("row %+v", r)
	}
}

// TestDerivedBundleRepoUnreadableFolder: a folder discovery cannot read has
// no workload image, so its bundle is <registry>/<name>.
func TestDerivedBundleRepoUnreadableFolder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{".pactoignore/x": ""})
	if got := derivedBundleRepo(dir, "r.io/acme", "ledger"); got != "r.io/acme/ledger" {
		t.Fatal(got)
	}
}

// TestCIPublishUnchangedNeedsTheVerifiedBaseline: plan mode reads the newest
// tag unverified. When that tag (1.3.0, signed by another key from the same
// key path) holds the folder's content, the row is not "unchanged": publish
// measures from the signed 1.2.0 and publishes above the tag it skipped, and
// the row says the verified baseline.
func TestCIPublishUnchangedNeedsTheVerifiedBaseline(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	e.write(t, map[string]string{"payments/pacto.yaml": e.contract("payments", "1.2.0", "", "")})
	e.publishAs(t, "payments", "r1")
	own, err := os.ReadFile(e.key)
	if err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	keyFiles(t, other)
	foreign, _ := os.ReadFile(filepath.Join(other, "cosign.key"))
	_ = os.WriteFile(e.key, foreign, 0o600)
	e.write(t, map[string]string{"payments/pacto.yaml": e.contract("payments", "1.3.0", "", ""), "payments/notes.md": "# payments\n"})
	e.publishAs(t, "payments", "r1")
	_ = os.WriteFile(e.key, own, 0o600)
	e.write(t, map[string]string{"payments/pacto.yaml": e.contract("payments", "", "", "")})
	r := pubRow(t, e.ciPublish(t, nil), "payments")
	if r.Changed != "no" || r.State != ci.StatePublished || r.To != "1.3.1" || r.Baseline != "1.2.0" || slices.Contains(r.Notes, planModeNote) {
		t.Fatalf("row %+v", r)
	}
}

// TestCIPublishUnchangedNeedsTheSignature: a declared version holding this
// content under another identity's signature is never "unchanged".
func TestCIPublishUnchangedNeedsTheSignature(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	e.write(t, map[string]string{"payments/pacto.yaml": e.contract("payments", "1.0.0", "", "")})
	own, err := os.ReadFile(e.key)
	if err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	keyFiles(t, other)
	foreign, _ := os.ReadFile(filepath.Join(other, "cosign.key"))
	_ = os.WriteFile(e.key, foreign, 0o600)
	e.publishAs(t, "payments", "r1")
	_ = os.WriteFile(e.key, own, 0o600)
	r := pubRow(t, e.ciPublish(t, nil), "payments")
	if r.Changed != "no" || r.State != ci.StateFailed || !strings.Contains(r.Reason, "holds this content, but not signed by the publisher identity") {
		t.Fatalf("row %+v", r)
	}
}

// TestCIPublishUnchangedNeedsTheCommitPoint: a declared version whose bundle
// tag is there but whose deploy artifact, the commit point, never was is not
// "unchanged".
func TestCIPublishUnchangedNeedsTheCommitPoint(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	e.write(t, map[string]string{"ledger/pacto.yaml": e.contract("ledger", "1.0.0", "", e.deployPub("ledger"))})
	deploy := e.host + "/acme/deploy/ledger"
	e.reg.tag = func(repo, _, _ string) bool { return repo == deploy } // the run dies before the deploy tag
	if _, err := e.svc.Publish(t.Context(), PublishOptions{Path: filepath.Join(e.root, "ledger"), Revision: "r1"}); err == nil {
		t.Fatal("the read-back sees no deploy tag")
	}
	e.reg.tag = nil
	p := e.ciPublish(t, nil)
	if r := pubRow(t, p, "ledger"); r.Changed != "no" || r.State != ci.StateFailed || !strings.Contains(r.Reason, "stopped before its commit point") || p.Exit != 1 {
		t.Fatalf("row %+v", r)
	}
}

// TestCIPublishRefusedKeepsThePlanModeNote: a row never published keeps
// saying its derived version was read without verifying the baseline.
func TestCIPublishRefusedKeepsThePlanModeNote(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	e.write(t, map[string]string{"payments/pacto.yaml": e.contract("payments", "", "", "")})
	e.publishAs(t, "payments", "r1")
	e.write(t, map[string]string{"payments/notes.md": "# payments\n"})
	r := pubRow(t, e.ciPublish(t, map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_BASE_REF": "main"}), "payments")
	if r.State != ci.StateRefused || !r.Derived || !slices.Contains(r.Notes, planModeNote) {
		t.Fatalf("row %+v", r)
	}
}

func TestDerivedFrom(t *testing.T) {
	t.Parallel()
	p := derivedFrom(publish.Plan{Baseline: "1.3.0", Classification: "NON_BREAKING"}, publish.Derived{Baseline: "1.2.0", Classification: "BREAKING"})
	if p.Baseline != "1.2.0" || p.Classification != "BREAKING" {
		t.Fatalf("%+v", p)
	}
	if p := derivedFrom(publish.Plan{Baseline: "1.3.0"}, publish.Derived{}); p.Baseline != "1.3.0" {
		t.Fatalf("a declared version keeps the plan's: %+v", p)
	}
	r := ci.Row{Classification: "NON_BREAKING", Notes: []string{planModeNote}}
	fillRow(&r, &contract.Bundle{Contract: &contract.Contract{}}, &publish.Result{Plan: publish.Plan{Classification: "BREAKING"}, Actions: []string{}}, nil)
	if r.Classification != "BREAKING" || len(r.Notes) != 0 {
		t.Fatalf("the row says what publish measured: %+v", r)
	}
}

// TestCIPublishUnchangedSuppliedImageNeedsNoImage: a service whose image
// comes from --image goes through publish when it is unchanged too, and
// publish asks for no image when there is nothing to write.
func TestCIPublishUnchangedSuppliedImageNeedsNoImage(t *testing.T) {
	t.Parallel()
	for name, version := range map[string]string{"derived": "", "declared": "  version: 1.0.0\n"} {
		t.Run(name, func(t *testing.T) {
			e := newCIPub(t)
			e.write(t, map[string]string{
				"ledger/pacto.yaml":      "pactoVersion: \"2.5\"\nservice:\n  name: ledger\n" + version,
				"ledger/k8s/deploy.yaml": deployment("r.io/ledger:1"),
			})
			opts := CIPublishOptions{CI: CIOptions{Root: e.root, Registry: e.host + "/acme"}, Revision: "r1", SignKey: e.key, NoTransparencyLog: true,
				Images: map[string]string{"ledger": e.host + "/other/ledger@" + digest64}}
			if p, err := e.svc.CIPublish(t.Context(), opts); err != nil || pubRow(t, p, "ledger").State != ci.StatePublished {
				t.Fatalf("%v %+v", err, p)
			}
			opts.Revision, opts.Images = "r2", nil
			p, err := e.svc.CIPublish(t.Context(), opts)
			if r := pubRow(t, p, "ledger"); err != nil || r.State != ci.StateUnchanged || p.Exit != 0 {
				t.Fatalf("%v row %+v", err, r)
			}
		})
	}
}

func TestCIPublishWritesIndexEntries(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	ledgerReports(t, e, "^1")
	e.svc.sys.refusalRepo = anonRepo
	if p := e.ciPublish(t, nil); p.Exit != 0 {
		t.Fatalf("plan %+v", p)
	}
	for _, name := range []string{"ledger", "reports"} {
		tag, _ := orgindex.Tag(fleet.NewServiceKeyDomain(e.host+"/acme", name), "1.0.0")
		if entry := readEntry(t, e.host+"/acme/pacto-index", tag); entry.Name != name {
			t.Fatalf("entry %+v", entry)
		}
	}

	failed := newCIPub(t)
	ledgerReports(t, failed, "^1")
	failed.svc.sys.refusalRepo = func(string) (*remote.Repository, error) { return nil, errors.New("denied") }
	p := failed.ciPublish(t, nil)
	if p.Exit != 0 || !slices.Equal(failed.records, []string{"ledger published false", "reports published false"}) {
		t.Fatalf("a failed index write changed the run: exit %d records %v", p.Exit, failed.records)
	}
	if r := pubRow(t, p, "ledger"); !slices.ContainsFunc(r.Notes, func(n string) bool { return strings.HasPrefix(n, "ledger 1.0.0 not indexed in ") }) {
		t.Fatalf("the failed write is not said: %v", r.Notes)
	}

	// A re-run over the published content writes the missing entries.
	failed.svc.sys.refusalRepo = anonRepo
	again := failed.ciPublish(t, nil)
	for _, name := range []string{"ledger", "reports"} {
		if r := pubRow(t, again, name); r.State != ci.StateUnchanged || len(r.Notes) != 0 {
			t.Fatalf("row %+v", r)
		}
		tag, _ := orgindex.Tag(fleet.NewServiceKeyDomain(failed.host+"/acme", name), "1.0.0")
		if entry := readEntry(t, failed.host+"/acme/pacto-index", tag); entry.Name != name {
			t.Fatalf("not backfilled: %+v", entry)
		}
	}
}

// TestPublishUnchangedDerivedBackfillsEntry: publishing a derived version
// again over unchanged content writes the entry the first publish could not.
func TestPublishUnchangedDerivedBackfillsEntry(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	e.write(t, map[string]string{"payments/pacto.yaml": e.contract("payments", "", "", "")})
	e.svc.sys.refusalRepo = func(string) (*remote.Repository, error) { return nil, errors.New("denied") }
	path := filepath.Join(e.root, "payments")
	first, err := e.svc.Publish(t.Context(), PublishOptions{Path: path, Revision: "r1"})
	if err != nil || !strings.HasPrefix(first.Index, "not indexed in ") {
		t.Fatalf("%v %q", err, first.Index)
	}
	e.svc.sys.refusalRepo = anonRepo
	again, err := e.svc.Publish(t.Context(), PublishOptions{Path: path, Revision: "r2"})
	if err != nil || len(again.Actions) != 0 || !strings.HasPrefix(again.Index, "indexed as "+e.host+"/acme/pacto-index:") {
		t.Fatalf("%v %+v", err, again)
	}
	tag, _ := orgindex.Tag(fleet.NewServiceKeyDomain(e.host+"/acme", "payments"), first.Plan.Version)
	if entry := readEntry(t, e.host+"/acme/pacto-index", tag); entry.Version != first.Plan.Version {
		t.Fatalf("entry %+v", entry)
	}
}

// TestCIPublishIndexNoneWritesNothing: --index reaches every publish of the
// run, so --index none writes no entry.
func TestCIPublishIndexNoneWritesNothing(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	ledgerReports(t, e, "^1")
	e.svc.sys.refusalRepo = anonRepo
	p, err := e.svc.CIPublish(t.Context(), CIPublishOptions{CI: CIOptions{Root: e.root, Registry: e.host + "/acme", Getenv: mapEnv(nil), Fleet: FleetOptions{Index: IndexNone}}, Revision: "r2"})
	if err != nil || p.Exit != 0 {
		t.Fatalf("%v %+v", err, p)
	}
	r, _ := anonRepo(e.host + "/acme/pacto-index")
	if tags, err := ocireferrer.Tags(t.Context(), r, ""); len(tags) != 0 || (err != nil && !ocireferrer.RepositoryNotFound(err)) {
		t.Fatalf("--index none wrote %v (%v)", tags, err)
	}
}
