package main

import (
	"io/fs"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// attestedService is the unpublished demo service whose owner the demo shows
// as attested.
const attestedService = "newsletter-service"

// demoCommit stands in for the commit a CI publish would have read the
// record at; the committed record names none.
const demoCommit = "4f1c2a9e7b3d5c6a8e0f1b2d3c4e5f6a7b8c9d0e"

// attestDemoRevision stands in for a CI publish of newsletter-service: the
// demo has no registry and no Sigstore, so it gives the revision the
// signature a CI identity the publisher rule accepts would leave, issued for
// the repository and the commit its record was read at. The fleet then
// attests the record's catalog-info.yaml owner exactly as it would for a
// real publish. When the committed record names no commit, the demo commit
// is stamped on an in-memory copy; the files on disk never change.
func attestDemoRevision(c *fleet.Collection) {
	const (
		repo   = "https://github.com/trianalab/pacto-demo"
		signer = repo + "/.github/workflows/ci.yml@refs/heads/main"
		issuer = "https://token.actions.githubusercontent.com"
	)
	for i, r := range c.Revisions {
		if r.Bundle == nil || r.Bundle.Contract == nil || r.Bundle.Contract.Service.Name != attestedService {
			continue
		}
		b, commit, ok := withCommit(r.Bundle)
		if !ok {
			continue
		}
		c.Revisions[i].Bundle = b
		c.Revisions[i].Provenance = &fleet.BundleProvenance{
			Signature: fleet.BundleSignature{Status: fleet.SignatureVerified, Source: "rule", Signer: signer, Issuer: issuer,
				SignerRepository: repo, SignerCommit: commit, Bundles: 1,
				Reason: "signed by " + signer + ", which the publisher rule \"derived from ghcr.io/trianalab\" covers"},
			Signers: []fleet.VerifiedSigner{{Subject: signer, Issuer: issuer, Class: "ci"}},
			Source:  "local", CheckedAt: demoNow(),
		}
	}
}

// withCommit is b with a record that names a commit, and that commit: b
// itself when its record names one, else a copy whose record carries the
// demo commit. A bundle with no readable record is left alone (false).
func withCommit(b *contract.Bundle) (*contract.Bundle, string, bool) {
	rec, err := contract.ReadInferredRecord(b.FS)
	if err != nil || rec == nil {
		return nil, "", false
	}
	if rec.Inputs.Commit != "" {
		return b, rec.Inputs.Commit, true
	}
	rec.Inputs.Commit = demoCommit
	cp := *b
	cp.FS = recordFS{FS: b.FS, rec: rec.Marshal()}
	return &cp, demoCommit, true
}

// recordFS is a bundle FS whose inferred record reads as rec.
type recordFS struct {
	fs.FS
	rec []byte
}

func (f recordFS) Open(name string) (fs.File, error) {
	if name == contract.InferredRecordFile {
		return fstest.MapFS{name: {Data: f.rec}}.Open(name)
	}
	return f.FS.Open(name)
}
