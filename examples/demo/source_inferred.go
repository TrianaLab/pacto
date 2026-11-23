package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// This file holds the demo's one inferred external contract. It has no build
// tag, so TestDemoInferredStripeProvider builds a snapshot from it on the
// host; buildDemoFleet (wasm only) feeds it to the browser demo.

// demoInferredRepository is where an installation's operator would publish
// stripe's inferred contract.
const demoInferredRepository = "registry.pacto.demo/demo/pacto-inferred/stripe"

// demoStripeOpenAPI is the vendor OpenAPI document each inferred revision
// links, by version: the digest moved between 0.0.1 and 0.0.2.
var demoStripeOpenAPI = map[string]string{
	"0.0.1": "oci://registry.pacto.demo/vendor/stripe-openapi@sha256:3e9a51c07d2b8f64a1e0c95d7b3f28e6014ad9c7b5e2f830a6d1c4b97e05f2a8",
	"0.0.2": "oci://registry.pacto.demo/vendor/stripe-openapi@sha256:7c21d8e4a09b3f5c6e1a7d2b8f04c93e5a6b1d7f2c8e09a4b3d5f61e7c2a9b04",
}

// demoInferredYAML is the pacto.yaml of stripe's inferred contract at version.
func demoInferredYAML(version string) string {
	return "pactoVersion: \"2.5\"\nservice:\n  name: stripe\n  version: " + version + "\n  external: stripe\n" +
		"metadata:\n  " + fleet.ExternalOpenAPIMetadataKey + ": \"" + demoStripeOpenAPI[version] + "\"\n"
}

// demoInferredAsOf is when each revision's surface was read: 0.0.2 now,
// 0.0.1 a day earlier.
func demoInferredAsOf(version string) time.Time {
	if version == "0.0.1" {
		return demoNow().Add(-24 * time.Hour)
	}
	return demoNow()
}

// demoInferredSurface is what the cluster showed of Stripe at version: the
// host and port notification-worker's pod spec names.
func demoInferredSurface(version string) contract.InferredSurface {
	asOf := demoInferredAsOf(version)
	seen, cat := stamp(asOf.Add(-30*time.Minute)), externalcatalog.DefaultDigest()
	return contract.InferredSurface{
		Identity: "stripe", Class: externalcatalog.ClassVendorAPI, AsOf: stamp(asOf),
		Completeness: &contract.SurfaceCompleteness{State: "complete"},
		Catalog:      &contract.InferredCatalogMatch{Origin: externalcatalog.OriginDefault, Digest: cat, Entry: "stripe"},
		Hosts:        []contract.SurfaceValue{{Value: "api.stripe.com:443", Sources: []string{"k8s-production"}, Catalog: cat, FirstSeen: seen, LastSeen: seen}},
		Ports:        []contract.SurfaceValue{{Value: "443", Sources: []string{"k8s-production"}, Catalog: cat, FirstSeen: seen, LastSeen: seen}},
	}
}

// demoInferredRevisions are stripe's inferred contracts as the registry
// mirror reads them: 0.0.2 moved the linked OpenAPI digest. They carry no
// provenance: the demo never claims a signature check it did not run, so the
// External page says no source checked them. The change line therefore reads
// unchecked and no EXTERNAL_CHANGED item is raised.
//
// ponytail: built by hand, not with inferredcontract.Bundle, which links
// pkg/publish and sigstore (and klog) into the wasm build;
// TestDemoInferredMatchesPublisher pins the bytes to Bundle's.
func demoInferredRevisions() []fleet.RawRevision {
	return []fleet.RawRevision{demoInferredRevision("0.0.1"), demoInferredRevision("0.0.2")}
}

// demoInferredRevision is stripe's inferred contract at version.
func demoInferredRevision(version string) fleet.RawRevision {
	s, raw := demoInferredSurface(version), demoInferredYAML(version)
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Fields: []contract.Inferred{}, Surface: &s,
		Inputs: contract.InferredInputs{Pacto: "demo", Discovery: "1", Sources: []contract.InferredSourceFile{}}}
	c, err := contract.Parse(strings.NewReader(raw))
	if err != nil {
		panic(err) // a constant: TestDemoInferredStripeProvider fails first
	}
	b := &contract.Bundle{Contract: c, RawYAML: []byte(raw), FS: fstest.MapFS{
		"pacto.yaml": {Data: []byte(raw)}, contract.InferredRecordFile: {Data: rec.Marshal()},
	}}
	sum := sha256.Sum256(b.RawYAML)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	return fleet.RawRevision{
		Bundle: b, Domain: "registry.pacto.demo/demo/pacto-inferred",
		RequestedRef: "oci://" + demoInferredRepository + ":" + version, ResolvedRef: "oci://" + demoInferredRepository + "@" + digest, Digest: digest,
	}
}
