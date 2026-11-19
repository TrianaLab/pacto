package main

import (
	"github.com/Masterminds/semver/v3"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// provenanced are the services whose newest revision the demo registry
// checked, and what accepted its signer: payments-service publishes from a
// workflow the installation's rule covers, orders-service from one a person
// pinned. Every other revision is not checked.
var provenanced = map[string]string{"payments-service": "rule", "orders-service": "pin"}

// withProvenance sets the signature check on the newest revision of each
// provenanced service, as the registry source's ProvenanceFunc would.
func withProvenance(revs []fleet.RawRevision, source string) {
	newest := map[string]int{}
	for i, r := range revs {
		name := r.Bundle.Contract.Service.Name
		if provenanced[name] == "" {
			continue
		}
		if j, ok := newest[name]; !ok || newer(r, revs[j]) {
			newest[name] = i
		}
	}
	for name, i := range newest {
		subject := "https://github.com/trianalab/" + name + "/.github/workflows/publish.yml@refs/heads/main"
		reason := "signed by " + subject + ", which the publisher rule \"derived from ghcr.io/trianalab\" covers"
		if provenanced[name] == "pin" {
			subject = "https://github.com/trianalab/" + name + "/.github/workflows/release.yml@refs/heads/release"
			reason = "signed by " + subject + ", which bob@example.com pinned for " + name
		}
		revs[i].Provenance = &fleet.BundleProvenance{
			Signature: fleet.BundleSignature{Status: fleet.SignatureVerified, Source: provenanced[name], Signer: subject,
				Issuer: "https://token.actions.githubusercontent.com", Bundles: 1, Reason: reason},
			Referrers: fleet.NewReferrersPreview([]fleet.OCIReferrer{{ArtifactType: "application/vnd.dev.sigstore.bundle.v0.3+json",
				Digest: recordID("sigstore " + revs[i].Digest)}}),
			Source: source, CheckedAt: demoNow(),
		}
	}
}

// newer reports whether a's contract version is above b's.
func newer(a, b fleet.RawRevision) bool {
	va, ea := semver.NewVersion(a.Bundle.Contract.Service.Version)
	vb, eb := semver.NewVersion(b.Bundle.Contract.Service.Version)
	return ea == nil && (eb != nil || va.GreaterThan(vb))
}
