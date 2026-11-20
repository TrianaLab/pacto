package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// breakRepo is a repository where payments 1.0.0 is published and its
// checkout drops the interface web depends on, announcing the break for
// web; web 1.0.0, owned by ana, is published and runs.
type breakRepo struct {
	*ciRepoEnv
	ss   *testutil.Sigstore
	root string
}

func newBreakRepo(t *testing.T, expires string) *breakRepo {
	t.Helper()
	noAmbient(t)
	e := &breakRepo{ciRepoEnv: newCIRepo(t), ss: testutil.New(t)}
	e.svc.sys.refusalRepo = func(path string) (*remote.Repository, error) {
		return ocireferrer.Open(path, ocireferrer.Options{Keychain: anonymous})
	}
	e.svc.sys.now = func() time.Time { return acceptNow }
	e.root = filepath.Join(t.TempDir(), "trusted_root.json")
	if err := os.WriteFile(e.root, e.ss.RootJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	e.write(t, map[string]string{"payments/pacto.yaml": paymentsV1, "payments/openapi.yaml": openAPIFile,
		"web/pacto.yaml": "service: {name: web, version: 1.0.0, owner: {team: web, dri: ana@acme.com}}\ndependencies:\n  - ref: oci://" + e.host + "/acme/payments\n    compatibility: \"^1.0.0\"\npublish:\n  bundle: oci://" + e.host + "/acme/web\n",
		".git/HEAD":      "0123456789abcdef0123456789abcdef01234567\n"})
	e.publish(t, "payments", "acme/payments", "1.0.0", nil)
	e.publish(t, "web", "acme/web", "1.0.0", nil)
	brk := "breaks:\n  - consumers: [web]\n    reason: the v1 API is removed\n"
	if expires != "" {
		brk += "    expires: \"" + expires + "\"\n"
	}
	e.write(t, map[string]string{"payments/pacto.yaml": "pactoVersion: \"2.5\"\nservice: {name: payments}\n" + brk})
	if err := os.Remove(filepath.Join(e.ciRepoEnv.root, "payments/openapi.yaml")); err != nil {
		t.Fatal(err)
	}
	return e
}

// policy lets the test Sigstore's people sign.
func (e *breakRepo) policy() *delivery.ClassPolicy {
	return &delivery.ClassPolicy{Signers: []delivery.PersonSigner{{Issuer: personIssuer, Subjects: ".*", TrustedRoot: string(e.ss.RootJSON)}}}
}

// planCI plans the repository with web running and env as the CI context.
func (e *breakRepo) planCI(t *testing.T, env map[string]string) (*ci0, *impact.Result) {
	t.Helper()
	p, res, err := e.svc.CIPlan(context.Background(), CIOptions{Root: e.ciRepoEnv.root, Registry: e.host + "/acme", Signers: e.policy(), Getenv: mapEnv(env),
		Fleet: FleetOptions{TargetStateFiles: []string{e.webRuns(t)}}})
	if err != nil {
		t.Fatal(err)
	}
	return &ci0{row(t, p, "payments").Impact.Text}, res["payments"]
}

type ci0 struct{ text string }

// webRuns is a target-state file running web of the registry's domain.
func (e *breakRepo) webRuns(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "targets.yaml")
	writeCIFile(t, filepath.Dir(file), "targets.yaml", "schemaVersion: pacto.dev/fleet-targets/v1\ntargets:\n"+
		"  - scope: prod\n    kind: k8s\n    name: web\n    service: web\n    requestedRef: oci://"+e.host+"/acme/web:1.0.0\n    compliance: Compliant\n")
	return file
}

// webOf is the running web consumer.
func webOf(t *testing.T, res *impact.Result) impact.AffectedConsumer {
	t.Helper()
	for _, c := range res.Consumers {
		if c.Service == "web" && len(c.Targets) > 0 {
			return c
		}
	}
	t.Fatalf("no web in %+v", res.Consumers)
	return impact.AffectedConsumer{}
}

func TestBreakAcceptedBeforeProviderPublish(t *testing.T) {
	e := newBreakRepo(t, "2999-01-01")
	p, res := e.planCI(t, nil)
	if c := webOf(t, res); p.text != "blocked (web)" || c.Break == nil || c.Break.State != impact.BreakAnnounced {
		t.Fatalf("%s %+v", p.text, c.Break)
	}
	// ana accepts payments 2.0.0 from its checkout: it is not published.
	e.svc.sys.login = func(context.Context, declareLogin) (string, error) { return personToken("ana@acme.com"), nil }
	pol := e.policy()
	acc, err := e.svc.Accept(t.Context(), AcceptOptions{Ref: e.host + "/acme/web", Break: "payments@" + res.NewVersion, From: filepath.Join(e.ciRepoEnv.root, "payments"),
		Signing: declareSigning{TrustedRoot: e.root, FulcioURL: e.ss.Fulcio, TimestampURL: e.ss.TSA},
		Trust:   func(context.Context) (Installation, error) { return Installation{Signers: pol.Signers}, nil }})
	if err != nil || acc.State != impact.AcceptCounted {
		t.Fatalf("%v %+v", err, acc)
	}
	p, res = e.planCI(t, nil)
	c := webOf(t, res)
	if p.text == "blocked (web)" || res.Gate == impact.GateBlocked || c.Break.State != impact.BreakAccepted || c.Break.By != "ana@acme.com" || c.Break.Record != acc.Record {
		t.Fatalf("%s %s %+v %s %v", p.text, res.GateReason, c.Break, c.Domain, c.Path)
	}
}

func TestImpactAndCIAgreeOnUndatedBreak(t *testing.T) {
	e := newBreakRepo(t, "")
	env := map[string]string{"SOURCE_DATE_EPOCH": "1795000000"}
	_, ciRes := e.planCI(t, env)
	want, _ := discover.BreakExpiry(1795000000, "0123456789abcdef0123456789abcdef01234567")
	// pacto impact on the checkout derives the expiry with the same helper.
	got, src := e.svc.checkoutExpiry(t.Context(), ImpactOptions{NewPath: filepath.Join(e.ciRepoEnv.root, "payments"), Getenv: mapEnv(env)})()
	if ciGot := webOf(t, ciRes).Break; ciGot == nil || ciGot.Expires != want || got != want || src != "commit:0123456789ab +30d" {
		t.Fatalf("impact %s (%s), ci %+v, want %s", got, src, ciGot, want)
	}
	// A published new revision is no checkout: its undated break has no expiry.
	if exp, src := e.svc.checkoutExpiry(t.Context(), ImpactOptions{NewPath: "oci://" + e.host + "/acme/payments:1.0.0"})(); exp != "" || src != "" {
		t.Fatal(exp, src)
	}
}

func TestDeclaredExpiryWinsOverRecord(t *testing.T) {
	rec := acceptRecordOf(contract.Inferred{Field: "breaks[0].expires", Value: "2026-12-14", Source: "commit:0123456789ab +30d", Confidence: contract.ConfidenceLikely},
		contract.Inferred{Field: "breaks[1].expires", Value: "2026-12-14", Source: "commit:0123456789ab +30d", Confidence: contract.ConfidenceLikely})
	c := &contract.Contract{Service: contract.Service{Name: "payments"}, Breaks: []contract.Break{
		{Consumers: []string{"web"}, Reason: "a", Expires: "2027-01-01"}, {Consumers: []string{"api"}, Reason: "b"}, {Consumers: []string{"cli"}, Reason: "c"}}}
	b := &contract.Bundle{Contract: c, FS: contract.WithInferredRecord(nil, rec.Marshal())}
	in := (&Service{}).breakInput(t.Context(), ImpactOptions{NewPath: "oci://reg/acme/payments:2.0.0"}, b, &fleet.FleetSnapshot{})
	if in.Announced["web"].Expires != "2027-01-01" || in.Announced["api"].Expires != "2026-12-14" || in.Announced["api"].ExpiresSource != "pacto.inferred.yaml" ||
		in.Announced["cli"].Expires != "" || in.Acceptances != nil {
		t.Fatalf("%+v", in.Announced)
	}
	if (&Service{}).breakInput(t.Context(), ImpactOptions{}, &contract.Bundle{Contract: &contract.Contract{}}, nil) != nil {
		t.Fatal("no break announced is no input")
	}
}

func TestImpactTrust(t *testing.T) {
	if impactTrust(ImpactOptions{}) != nil {
		t.Fatal("no trust")
	}
	pc := &ProvenanceChecker{Trust: func(context.Context) (Installation, error) { return Installation{}, nil }}
	if impactTrust(ImpactOptions{Fleet: FleetOptions{Provenance: pc}}) == nil {
		t.Fatal("the provenance checker's trust")
	}
	in, _ := impactTrust(ImpactOptions{Signers: &delivery.ClassPolicy{Signers: []delivery.PersonSigner{{Issuer: "x"}}}})(t.Context())
	if len(in.Signers) != 1 {
		t.Fatal(in)
	}
}

func TestRevisionRepos(t *testing.T) {
	rev := &fleet.ContractRevision{ResolvedRef: "oci://reg/acme/web@sha256:00",
		Contract: &contract.Contract{Publish: &contract.Publish{Bundle: "oci://reg/acme/web-bundle"}}}
	if got := revisionRepos(rev); strings.Join(got, " ") != "reg/acme/web-bundle reg/acme/web" {
		t.Fatal(got)
	}
}
