package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestProvenanceListsVerifiedSigners: the check lists who signed, from the
// same read that judged the signature, whatever the verdict.
func TestProvenanceListsVerifiedSigners(t *testing.T) {
	_, s := keylessSigstore(t)
	asGHCR(t)
	reg := provenanceTestRegistry(t)
	p := checkerFor(reg).Check(t.Context(), "oci", pushSigned(t, reg, s), nil)
	if p.Signature.Status != fleet.SignatureUnverified || len(p.Signers) != 1 {
		t.Fatalf("provenance %+v", p)
	}
	if got := p.Signers[0]; got.Subject != provIdentity || got.Issuer != provIssuer || got.Class == "" {
		t.Fatalf("signer %+v", got)
	}
	if c := cloneProvenance(p); &c.Signers[0] == &p.Signers[0] {
		t.Fatal("a clone shares the signer list")
	}
}

func TestProvenanceSignersBounded(t *testing.T) {
	var ids []delivery.PublisherIdentity
	for i := 7; i > 0; i-- {
		ids = append(ids, delivery.PublisherIdentity{Subject: fmt.Sprintf("p%d@acme.com", i), Issuer: provIssuer, Class: delivery.ClassHuman})
	}
	ids = append(ids, ids[0]) // read twice
	got := verifiedSigners(ids)
	if len(got) != fleet.MaxProvenanceSigners || got[0].Subject != "p1@acme.com" || got[4].Subject != "p5@acme.com" || got[0].Class != "human" {
		t.Fatalf("signers %+v", got)
	}
	if verifiedSigners(nil) != nil {
		t.Fatal("no signer is nil")
	}
}

// TestProvenanceDeclarationSigners: the installation signer policy comes
// with the installation's trust; none is read without a Trust reader, and a
// trust that cannot be read is an unread policy.
func TestProvenanceDeclarationSigners(t *testing.T) {
	pc := newProvenanceChecker(descRegistry{})
	if pc.DeclarationSigners(t.Context()) != nil {
		t.Fatal("no Trust reader read a policy")
	}
	trustingIn(pc, Installation{Signers: []delivery.PersonSigner{{Issuer: provIssuer, Subjects: `.+@acme\.com`, ClientID: "x"}}, SignersUnread: ""}, nil)
	want := &fleet.DeclarationSignerPolicy{Signers: []fleet.PolicySigner{{Issuer: provIssuer, Subjects: `.+@acme\.com`}}}
	if got := pc.DeclarationSigners(t.Context()); !reflect.DeepEqual(got, want) {
		t.Fatalf("policy %+v", got)
	}
	pc.trust = nil
	trustingIn(pc, Installation{}, errors.New("forbidden"))
	if got := pc.DeclarationSigners(t.Context()); got == nil || got.Unread != "forbidden" {
		t.Fatalf("unread policy %+v", got)
	}
}

// TestInstallationTrustReadsSigners: the client reads the installation
// signer policy beside the publisher trust config, and says why when it
// cannot.
func TestInstallationTrustReadsSigners(t *testing.T) {
	e := newAtEnv(t, testutil.ReferrersOptions{})
	e.d.Policy = delivery.ClassPolicy{Signers: []delivery.PersonSigner{{Issuer: provIssuer, Subjects: `.+@acme\.com`}}}
	e.publishing(&delivery.PublisherConfig{Registry: "ghcr.io/acme"})
	in, err := e.d.InstallationTrust(context.Background())
	if err != nil || len(in.Signers) != 1 || in.Signers[0].Issuer != provIssuer || in.SignersUnread != "" {
		t.Fatalf("%+v %v", in, err)
	}
	e.publishingRaw(`{"registry":"ghcr.io/acme"}`)
	in, err = e.d.InstallationTrust(context.Background())
	if err != nil || in.Signers != nil || !strings.Contains(in.SignersUnread, "delivery identity policy") {
		t.Fatalf("%+v %v", in, err)
	}
}

// TestExplainPrintsDeclaration: explain carries a declaration's coverage,
// hidden counts and declarer as the record holds them.
func TestExplainPrintsDeclaration(t *testing.T) {
	dir := completionFolder(t, `pactoVersion: "2.5"`+"\n")
	rec, err := contract.ParseInferredRecord([]byte(completed(t, dir, commitA)))
	if err != nil {
		t.Fatal(err)
	}
	const obj = "billing/Deployment/invoices"
	rec.Inputs.Cluster = &contract.InferredClusterInput{AsOf: "2026-11-14T10:00:00Z", Object: obj, CallWindow: "24h"}
	rec.Fields = append(rec.Fields, contract.Inferred{Field: "dependencies[ledger].compatibility", Value: "compatibility unknown", Proposed: "^2.3.0",
		Source: "cluster:fleet reported versions 2.3.1", Confidence: contract.ConfidenceLikely, AsOf: "2026-11-14T09:58:12Z", Completeness: contract.CoveragePartial})
	rec.Coverage = []contract.InferredCoverage{{Source: contract.CoverageSources[0], State: contract.CoverageComplete}}
	rec.Hidden = &contract.InferredHidden{Services: 1}
	rec.Declaration = &contract.InferredDeclaration{State: contract.DeclarationDeclared, InferredFrom: "k8s:" + obj,
		Asker: contract.InferredSigner{Subject: "ana@acme.com", Issuer: "https://accounts.google.com", Class: contract.SignerClassHuman}}
	got, err := NewService(registryBundle(t, dir, string(rec.Marshal())), nil).Explain(context.Background(), ExplainOptions{Path: "oci://ghcr.io/acme/app:1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Declaration == nil || got.Declaration.Asker.Subject != "ana@acme.com" || len(got.InferredCoverage) != 1 || got.InferredHidden == nil || got.InferredHidden.Services != 1 {
		t.Fatalf("explain %+v %+v %+v", got.Declaration, got.InferredCoverage, got.InferredHidden)
	}
}
