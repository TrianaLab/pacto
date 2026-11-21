package app

import (
	"context"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var rightsNow = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

// ownedRev is version of invoices owned by team, signed by signer (a person
// when class is human), a declaration when declared.
func ownedRev(version, team, signer string, class delivery.ActorClass, declared bool) fleet.ContractRevision {
	r := fleet.ContractRevision{Key: fleet.RevisionKey("sha256:" + version), Service: "invoices", Version: version, Owner: contract.Owner{Team: team}}
	if signer != "" {
		r.Provenance = &fleet.BundleProvenance{Signature: fleet.BundleSignature{Status: fleet.SignatureUnverified, Signer: signer, Issuer: personIssuer},
			Signers: []fleet.VerifiedSigner{{Subject: signer, Issuer: personIssuer, Class: string(class)}}}
	}
	if declared {
		r.Record = &contract.InferredRecord{Declaration: &contract.InferredDeclaration{State: contract.DeclarationDeclared}}
	}
	return r
}

func rightsPolicy() *delivery.OwnerPolicy {
	return &delivery.OwnerPolicy{DefaultApprovers: []delivery.SignerRule{{Name: "platform-admins", Issuer: personIssuer, Subject: `^admin@acme\.com$`}}}
}

// TestOwnerRightsEffectiveOwnerSkipsRefused: an owner change by a person
// outside the rule never becomes the reference of the next change.
func TestOwnerRightsEffectiveOwnerSkipsRefused(t *testing.T) {
	revs := []fleet.ContractRevision{
		ownedRev("1.2.0", "payments", "eve@acme.com", delivery.ClassHuman, true), // refused: eve is no billing member
		ownedRev("1.0.0", "billing", "", "", false),                              // unsigned, no declaration: counts as it is
		ownedRev("1.3.0", "payments", "eve@acme.com", delivery.ClassHuman, true), // judged against billing again
		ownedRev("1.1.0", "billing", "eve@acme.com", delivery.ClassHuman, true),  // unchanged
	}
	cur, rights := OwnerRights(t.Context(), revs, ownerJudge("invoices", rightsPolicy(), "", rightsNow))
	if cur.Owner.Team != "billing" || cur.Provenance != fleet.OwnerProvenanceDeclared || len(rights) != 2 {
		t.Fatalf("%+v %+v", cur, rights)
	}
	for _, k := range []fleet.RevisionKey{"sha256:1.2.0", "sha256:1.3.0"} {
		if r := rights[k]; r.Allowed || r.Code != delivery.OwnerSignerNotAllowed {
			t.Fatalf("%s: %+v", k, r)
		}
	}
	// A default approver's change becomes the reference.
	revs = append(revs, ownedRev("1.4.0", "payments", "admin@acme.com", delivery.ClassHuman, true))
	if cur, _ := OwnerRights(t.Context(), revs, ownerJudge("invoices", rightsPolicy(), "", rightsNow)); cur.Owner.Team != "payments" {
		t.Fatalf("%+v", cur)
	}
}

// TestOwnerRightsIgnoresSelfNamedBaseline: a declaration naming a refused
// revision as its baseline is judged against the effective owner.
func TestOwnerRightsIgnoresSelfNamedBaseline(t *testing.T) {
	refused := ownedRev("1.1.0", "payments", "eve@acme.com", delivery.ClassHuman, true)
	laundered := ownedRev("1.2.0", "payments", "eve@acme.com", delivery.ClassHuman, true)
	laundered.Record.Declaration.Baseline = &contract.InferredBaseline{Version: "1.1.0", Bundle: "reg.io/acme/invoices@sha256:1.1.0"}
	revs := []fleet.ContractRevision{ownedRev("1.0.0", "billing", "", "", false), refused, laundered}
	cur, rights := OwnerRights(t.Context(), revs, ownerJudge("invoices", rightsPolicy(), "", rightsNow))
	if cur.Owner.Team != "billing" || rights["sha256:1.2.0"].Allowed {
		t.Fatalf("%+v %+v", cur, rights)
	}
}

func TestOwnerJudgeCases(t *testing.T) {
	judge := ownerJudge("invoices", rightsPolicy(), "", rightsNow)
	current := delivery.OwnerClaim{Owner: contract.Owner{Team: "billing"}, Provenance: fleet.OwnerProvenanceDeclared}
	// An accepted CI publish is outside the rule.
	ci := ownedRev("1.1.0", "payments", "https://github.com/acme/invoices/.github/workflows/ci.yml", delivery.ClassCI, false)
	ci.Provenance.Signature.Status = fleet.SignatureVerified
	if v := judge(context.Background(), &ci, current); v != nil {
		t.Fatalf("%+v", v)
	}
	// Where no installation rule was read, the inferred rule's accept (status
	// unknown) of a CI identity is outside the rule too.
	ci.Provenance.Signature.Status, ci.Provenance.Signature.Source = fleet.SignatureUnknown, delivery.PublisherSourceInferred
	if v := judge(context.Background(), &ci, current); v != nil {
		t.Fatalf("inferred CI accept = %+v", v)
	}
	ci.Provenance.Signature.Source = ""
	// A CI signature no rule accepted is no person.
	ci.Provenance.Signature.Status = fleet.SignatureUnverified
	if v := judge(context.Background(), &ci, current); v == nil || v.Code != delivery.OwnerSignerNotPerson {
		t.Fatalf("%+v", v)
	}
	// A declaration with no verified signer is unverified.
	bare := ownedRev("1.2.0", "payments", "", "", true)
	if v := judge(context.Background(), &bare, current); v == nil || v.Code != delivery.OwnerSignerUnverified || v.Allowed {
		t.Fatalf("%+v", v)
	}
	// Of two signers, the person outside the rule is the more decisive refusal.
	two := ownedRev("1.3.0", "payments", "bot", delivery.ClassAgent, true)
	two.Provenance.Signers = append(two.Provenance.Signers, fleet.VerifiedSigner{Subject: "eve@acme.com", Issuer: personIssuer, Class: "human"})
	if v := judge(context.Background(), &two, current); v.Code != delivery.OwnerSignerNotAllowed {
		t.Fatalf("%+v", v)
	}
}

func TestOwnerRightsAttestedAndRemoval(t *testing.T) {
	att := fleet.ContractRevision{Key: "sha256:a", Version: "1.0.0", Record: attestedRecord("payments"), Provenance: attestedProvenance()}
	cur, _ := OwnerRights(t.Context(), []fleet.ContractRevision{att}, ownerJudge("invoices", nil, "x", rightsNow))
	if cur.Owner.Team != "payments" || cur.Provenance != fleet.OwnerProvenanceAttested || cur.Source == "" {
		t.Fatalf("%+v", cur)
	}
	// A declaration that names no owner leaves an attested owner.
	empty := ownedRev("1.1.0", "", "eve@acme.com", delivery.ClassHuman, true)
	if cur, rights := OwnerRights(t.Context(), []fleet.ContractRevision{att, empty}, ownerJudge("invoices", nil, "x", rightsNow)); cur.Owner.Team != "payments" || len(rights) != 0 {
		t.Fatalf("%+v %+v", cur, rights)
	}
	// It removes a declared one, which takes the right.
	declared := ownedRev("1.0.0", "billing", "", "", false)
	if cur, rights := OwnerRights(t.Context(), []fleet.ContractRevision{declared, empty}, ownerJudge("invoices", nil, "x", rightsNow)); cur.Owner.Team != "billing" ||
		rights["sha256:1.1.0"].Code != delivery.OwnerPolicyUnverified {
		t.Fatalf("%+v %+v", cur, rights)
	}
	// Two revisions of one version keep their key order.
	twin := ownedRev("1.0.0", "billing", "", "", false)
	twin.Key = "sha256:0"
	if cur, _ := OwnerRights(t.Context(), []fleet.ContractRevision{declared, twin}, ownerJudge("invoices", nil, "x", rightsNow)); cur.Owner.Team != "billing" {
		t.Fatalf("%+v", cur)
	}
}

// attestedRecord and attestedProvenance attest team at ownerCommit.
func attestedRecord(team string) *contract.InferredRecord {
	return &contract.InferredRecord{Inputs: contract.InferredInputs{Commit: ownerCommit},
		Fields: []contract.Inferred{{Field: "service.owner", Source: "repository:CODEOWNERS:7", Class: contract.InferredClassOwnerFile,
			Declare: "service:\n  owner:\n    team: " + team + "\n"}}}
}

func attestedProvenance() *fleet.BundleProvenance {
	const wf = "https://github.com/acme/invoices/.github/workflows/ci.yml"
	return &fleet.BundleProvenance{Signature: fleet.BundleSignature{Status: fleet.SignatureVerified, Source: "rule", Signer: wf, Issuer: "https://token.actions.githubusercontent.com",
		SignerRepository: "https://github.com/acme/invoices", SignerCommit: ownerCommit},
		Signers: []fleet.VerifiedSigner{{Subject: wf, Issuer: "https://token.actions.githubusercontent.com", Class: "ci"}}}
}
