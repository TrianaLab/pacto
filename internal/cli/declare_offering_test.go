package cli

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

const (
	offeringFrom     = "oci://ghcr.io/acme/pacto-inferred/crossplane-6f1c/services:xbucket_0.0.0-inferred.2.0"
	offeringDigest   = "sha256:4c1e000000000000000000000000000000000000000000000000000000000000"
	offeringRecorded = "offering:crossplane-6f1c/xbucket@" + offeringDigest
)

// offeringPlanFixture is the first acceptance of xbucket, planned for a
// person's login.
func offeringPlanFixture() *app.DeclarePlan {
	return &app.DeclarePlan{Service: "xbucket", Version: "1.0.0", BundleRepo: "ghcr.io/acme/offerings/xbucket", State: contract.DeclarationDeclared,
		InferredFrom: offeringRecorded, Deploy: "none", Signer: app.SignerView{Issuer: "https://oauth2.sigstore.dev/auth", Class: "human", Source: "login"},
		IntentDigest: declareIntent}
}

func TestDeclareOfferingSubject(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	d := declareDeps(t, &got, offeringPlanFixture(), nil, nil)
	_, _, err := runRoot(t, d, nil, "declare", "offering:xbucket", "--from", offeringFrom, "--version", "1.0.0", "--owner", "team-storage",
		"--detected", "k8s:pacto-system/pacto-operator-detected", "--registry", "ghcr.io/acme/offerings")
	if err != nil || got.Service != "offering:xbucket" || got.From != offeringFrom || got.Version != "1.0.0" || got.Owner != "team-storage" ||
		got.Detected != "k8s:pacto-system/pacto-operator-detected" || got.Registry != "ghcr.io/acme/offerings" || got.Dynamic != nil {
		t.Fatalf("%v %+v", err, got)
	}
	// pacto claim keeps its own --owner: the tree builds with no duplicate flag.
	if _, _, err := runRoot(t, d, nil, "claim", "stripe", "--owner", "team-platform", "--registry", "ghcr.io/acme"); err != nil || got.Owner != "team-platform" {
		t.Fatalf("%v %+v", err, got)
	}
	if _, _, err := runRoot(t, d, nil, "declare", "template:http-service"); err == nil || !strings.Contains(err.Error(), "a later release") {
		t.Fatal(err)
	}
}

func TestDeclareOfferingPlanThenIntent(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	out, _, err := runRoot(t, declareDeps(t, &got, offeringPlanFixture(), nil, nil), nil, "declare", "offering:xbucket", "--from", offeringFrom,
		"--version", "1.0.0", "--owner", "team-storage", "--registry", "ghcr.io/acme/offerings")
	if err != nil || !strings.Contains(out, "plan: oci://ghcr.io/acme/offerings/xbucket:1.0.0 (first declaration") ||
		!strings.Contains(out, "nothing was written: run again with --yes --intent "+declareIntent) {
		t.Fatalf("%v\n%s", err, out)
	}
	run := func(req app.DeclareRequest) (*app.DeclareResult, error) {
		p := *offeringPlanFixture()
		_ = req.OnPlan(&p)
		p.Signer.Subject = "alice@acme.io"
		return &app.DeclareResult{Plan: p, Executed: true, Signed: true, Tag: "1.0.0", Published: "ghcr.io/acme/offerings/xbucket@sha256:7c1d"}, nil
	}
	out, _, err = runRoot(t, declareDeps(t, &got, nil, nil, run), nil, "declare", "offering:xbucket", "--from", offeringFrom,
		"--version", "1.0.0", "--registry", "ghcr.io/acme/offerings", "--yes", "--intent", declareIntent)
	if err != nil || got.IntentDigest != declareIntent || !strings.Contains(out, "declared offering:xbucket -> oci://ghcr.io/acme/offerings/xbucket:1.0.0\n") ||
		!strings.Contains(out, "  signed by alice@acme.io (person), inferredFrom: offering crossplane-6f1c/xbucket ("+offeringDigest+"), producer signature checked, no Git write\n") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestDeclareOfferingPrintsOfferingSource(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	out, _, err := runRoot(t, declareDeps(t, &got, offeringPlanFixture(), nil, nil), nil, "declare", "offering:xbucket", "--from", offeringFrom,
		"--version", "1.0.0", "--registry", "ghcr.io/acme/offerings")
	if err != nil || !strings.Contains(out, "inferredFrom offering crossplane-6f1c/xbucket ("+offeringDigest+")\n") || strings.Contains(out, "k8s:") {
		t.Fatalf("%v\n%s", err, out)
	}
	// A draft names the command a person accepts it with.
	run := func(req app.DeclareRequest) (*app.DeclareResult, error) {
		p := *offeringPlanFixture()
		p.State, p.AcceptCLI = contract.DeclarationDraft, "pacto declare offering:xbucket --from "+offeringFrom+" --from-draft draft-1"
		return &app.DeclareResult{Plan: p, Executed: true, Draft: true, Tag: "draft-1", Published: "ghcr.io/acme/offerings/xbucket@sha256:9a"}, nil
	}
	out, _, err = runRoot(t, declareDeps(t, &got, nil, nil, run), nil, "declare", "offering:xbucket", "--from", offeringFrom,
		"--version", "1.0.0", "--registry", "ghcr.io/acme/offerings", "--draft", "--yes", "--output-format", "text")
	if err != nil || !strings.Contains(out, "wrote draft-1 of offering:xbucket (ghcr.io/acme/offerings/xbucket@sha256:9a): a person accepts it with pacto declare offering:xbucket") {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := declarationFrom(offeringRecorded); got != "offering crossplane-6f1c/xbucket ("+offeringDigest+")" {
		t.Fatal(got)
	}
	if got := inferredFromText("k8s:billing/Deployment/invoices"); got != "k8s:billing/Deployment/invoices" {
		t.Fatal(got)
	}
	// A workload published with the offering line printer keeps its own form.
	var w strings.Builder
	p := *offeringPlanFixture()
	p.InferredFrom = "k8s:billing/Deployment/invoices"
	printDeclareResult(&w, &app.DeclareResult{Plan: p, Executed: true, Published: "x"})
	if !strings.Contains(w.String(), "published x (inferredFrom k8s:billing/Deployment/invoices)") {
		t.Fatal(w.String())
	}
}

// Explain names an offering draft's source and the acceptance command.
func TestExplainOfferingDraft(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.ExplainResult{Name: "xbucket", Declaration: &contract.InferredDeclaration{State: contract.DeclarationDraft, OnBehalfOf: "ana",
		InferredFrom: offeringRecorded, Asker: contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent}}}
	if err := printExplainResult(cmd, result, "text"); err != nil ||
		!strings.Contains(buf.String(), "from offering crossplane-6f1c/xbucket ("+offeringDigest+"), awaiting a person: accept it with pacto declare offering:xbucket --from-draft <tag>") {
		t.Fatalf("%v\n%s", err, buf.String())
	}
}
