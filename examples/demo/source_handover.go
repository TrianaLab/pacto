package main

import (
	"crypto/sha256"
	"encoding/hex"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/lock"
)

// handoverAsker and handoverIssuer are the person who declared invoices and
// the issuer of her keyless signature.
const (
	handoverAsker  = "ana@acme.example"
	handoverIssuer = "https://accounts.google.com"
)

// handoverPacto is invoices 0.1.0 as ana declared it: owned by billing, it
// accepts loyalty-service ^2.0.0 and leaves shipping-rates unknown.
const handoverPacto = `pactoVersion: "2.5"
service:
  name: invoices
  version: 0.1.0
  owner:
    team: billing
dependencies:
- name: loyalty-service
  ref: oci://ghcr.io/trianalab/pacto/loyalty-service
  required: true
  compatibility: ^2.0.0
- name: shipping-rates
  ref: oci://ghcr.io/trianalab/pacto/shipping-rates
  required: false
  compatibility: unknown
`

// handoverManifest is the Deployment manifest ana's folder shipped, the
// source of the one field the record infers from the folder.
const handoverManifest = "kind: Deployment\nmetadata:\n  name: invoices\n"

// demoHandoverSigners is the installation signer policy the demo reads:
// every acme.example person of handoverIssuer may declare a service.
func demoHandoverSigners() *fleet.DeclarationSignerPolicy {
	return &fleet.DeclarationSignerPolicy{Signers: []fleet.PolicySigner{{Issuer: handoverIssuer, Subjects: `[^@]+@acme\.example`}}}
}

// demoHandover is a person declaring a running workload: the invoices
// Deployment ran in billing with no contract, then ana published invoices
// 0.1.0 from it, signed with her own identity. The revision is built in
// memory and never published; its pacto.inferred.yaml names the workload,
// so the fleet lists one service invoices and records that it replaced the
// inferred one. cluster is the kube-system uid of the cluster ana read.
func demoHandover(now time.Time, cluster string) (fleet.RawRevision, *contract.Bundle, fleet.RawInferredService) {
	asOf := now.Add(-3 * time.Hour)
	sum := sha256.Sum256([]byte(handoverManifest))
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1",
			Sources: []contract.InferredSourceFile{{Path: "deploy/invoices.yaml", Digest: "sha256:" + hex.EncodeToString(sum[:])}},
			Cluster: &contract.InferredClusterInput{AsOf: asOf.Format(time.RFC3339), Object: "billing/Deployment/invoices", CallWindow: "24h"}},
		Fields: []contract.Inferred{{Field: "workload", Value: "service", Source: "deploy/invoices.yaml:1", Confidence: contract.ConfidenceLikely}, {Field: "dependencies[shipping-rates].compatibility", Value: "compatibility unknown", Proposed: "^2.0.0",
			Source: "cluster:fleet reported versions 2.0.3", Confidence: contract.ConfidenceLikely,
			AsOf: asOf.Format(time.RFC3339), Completeness: contract.CoveragePartial}},
		Declaration: &contract.InferredDeclaration{State: contract.DeclarationDeclared,
			Asker:        contract.InferredSigner{Subject: handoverAsker, Issuer: handoverIssuer, Class: contract.SignerClassHuman},
			InferredFrom: fleet.K8sCaller("", "billing", "Deployment", "invoices"), Cluster: cluster,
			Accepted: []contract.InferredAcceptance{{Field: "dependencies[loyalty-service].compatibility", Range: "^2.0.0"}}},
	}
	fsys := fstest.MapFS{"pacto.yaml": {Data: []byte(handoverPacto)}, "deploy/invoices.yaml": {Data: []byte(handoverManifest)}, contract.InferredRecordFile: {Data: rec.Marshal()}}
	c, doc, err := contract.ParseDocument([]byte(handoverPacto))
	if err != nil {
		panic(err) // a constant above
	}
	h, err := lock.HashFS(fsys)
	if err != nil {
		panic(err) // an in-memory FS
	}
	bundle := &contract.Bundle{Contract: c, RawYAML: []byte(handoverPacto), FS: fsys}
	rev := fleet.RawRevision{
		Bundle: bundle, RequestedRef: requestedRef(demoRegistry, "invoices", "0.1.0"),
		ResolvedRef: demoRegistry + "invoices@" + h, Digest: h, Document: doc,
		Provenance: &fleet.BundleProvenance{Signature: fleet.BundleSignature{Status: fleet.SignatureUnverified},
			Signers: []fleet.VerifiedSigner{{Subject: handoverAsker, Issuer: handoverIssuer, Class: contract.SignerClassHuman}},
			Source:  "local", CheckedAt: now},
	}
	created, seen := now.Add(-30*24*time.Hour), now.Add(-2*time.Minute)
	ct := "spec.template.spec.containers[name=invoices]"
	twin := fleet.RawInferredService{
		Namespace: "billing", Kind: "Deployment", Name: "invoices", ObservedAt: &seen, CreatedAt: &created,
		Image: &fleet.InferredValue{Value: "registry.example.com/billing/invoices:0.9.0", From: ct + ".image"},
		Ports: []fleet.InferredPort{{Port: 8080, Name: "http", Protocol: "TCP", From: ct + ".ports[0]"}},
	}
	return rev, bundle, twin
}
