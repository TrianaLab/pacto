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

// demoInferredYAML is the pacto.yaml of stripe's inferred contract.
const demoInferredYAML = "pactoVersion: \"2.5\"\nservice:\n  name: stripe\n  version: 0.0.1\n  external: stripe\n"

// demoInferredSurface is what the cluster shows of Stripe: the host and port
// notification-worker's pod spec names.
func demoInferredSurface() contract.InferredSurface {
	seen, cat := stamp(demoNow().Add(-30*time.Minute)), externalcatalog.DefaultDigest()
	return contract.InferredSurface{
		Identity: "stripe", Class: externalcatalog.ClassVendorAPI, AsOf: stamp(demoNow()),
		Completeness: &contract.SurfaceCompleteness{State: "complete"},
		Catalog:      &contract.InferredCatalogMatch{Origin: externalcatalog.OriginDefault, Digest: cat, Entry: "stripe"},
		Hosts:        []contract.SurfaceValue{{Value: "api.stripe.com:443", Sources: []string{"k8s-production"}, Catalog: cat, FirstSeen: seen, LastSeen: seen}},
		Ports:        []contract.SurfaceValue{{Value: "443", Sources: []string{"k8s-production"}, Catalog: cat, FirstSeen: seen, LastSeen: seen}},
	}
}

// demoInferredRevision is stripe's inferred contract as the registry mirror
// reads it. It carries no provenance: the demo never claims a signature
// check it did not run, so the External page says no source checked it.
//
// ponytail: built by hand, not with inferredcontract.Bundle, which links
// pkg/publish and sigstore (and klog) into the wasm build;
// TestDemoInferredMatchesPublisher pins the bytes to Bundle's.
func demoInferredRevision() fleet.RawRevision {
	s := demoInferredSurface()
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Fields: []contract.Inferred{}, Surface: &s,
		Inputs: contract.InferredInputs{Pacto: "demo", Discovery: "1", Sources: []contract.InferredSourceFile{}}}
	c, err := contract.Parse(strings.NewReader(demoInferredYAML))
	if err != nil {
		panic(err) // a constant: TestDemoInferredStripeProvider fails first
	}
	b := &contract.Bundle{Contract: c, RawYAML: []byte(demoInferredYAML), FS: fstest.MapFS{
		"pacto.yaml": {Data: []byte(demoInferredYAML)}, contract.InferredRecordFile: {Data: rec.Marshal()},
	}}
	sum := sha256.Sum256(b.RawYAML)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	return fleet.RawRevision{
		Bundle: b, Domain: "registry.pacto.demo/demo/pacto-inferred",
		RequestedRef: "oci://" + demoInferredRepository + ":0.0.1", ResolvedRef: "oci://" + demoInferredRepository + "@" + digest, Digest: digest,
	}
}
