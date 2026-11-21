package app

import (
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const provCommit = "0123456789abcdef0123456789abcdef01234567"

// TestProvenanceSignerRepositoryAndCommit: an accepted CI signature names the
// repository and the commit its certificate was issued for.
func TestProvenanceSignerRepositoryAndCommit(t *testing.T) {
	_, s := keylessSigstoreFor(t, sigtest.CITokenAt(provWorkflow, provIssuer, provRepo, "refs/heads/main", provCommit))
	reg := provenanceTestRegistry(t)
	pc := checkerFor(reg)
	trusting(pc, delivery.PublisherConfig{Registry: "ghcr.io/acme"}, nil, nil)
	p := pc.Check(t.Context(), "oci", pushSigned(t, reg, s), nil)
	wantStatus(t, p, fleet.SignatureVerified, "Signed by")
	if p.Signature.SignerRepository != provRepo || p.Signature.SignerCommit != provCommit {
		t.Fatalf("signature %+v", p.Signature)
	}
}

// TestProvenanceNoCommitWhenNotAccepted: a CI signature the rule does not
// accept, or one only an inferred rule accepts, names neither.
func TestProvenanceNoCommitWhenNotAccepted(t *testing.T) {
	_, s := keylessSigstoreFor(t, sigtest.CITokenAt(provWorkflow, provIssuer, provRepo, "refs/heads/main", provCommit))
	reg := provenanceTestRegistry(t)
	pc := checkerFor(reg)
	trusting(pc, delivery.PublisherConfig{Registry: "ghcr.io/other"}, nil, nil)
	p := pc.Check(t.Context(), "oci", pushSigned(t, reg, s), nil)
	wantStatus(t, p, fleet.SignatureUnverified, "a person confirms it")
	if p.Signature.SignerRepository != "" || p.Signature.SignerCommit != "" {
		t.Fatalf("refused: %+v", p.Signature)
	}
	asGHCR(t)
	p = checkerFor(reg).Check(t.Context(), "oci", pushSigned(t, reg, s), nil)
	if p.Signature.Source != delivery.PublisherSourceInferred || p.Signature.SignerRepository != "" || p.Signature.SignerCommit != "" {
		t.Fatalf("inferred: %+v", p.Signature)
	}
}
