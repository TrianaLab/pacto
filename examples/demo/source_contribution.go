package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/examples/story"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// This file holds what one producer, acme-idp, sends Pacto through the open
// contribution seams: a signed ledger-sync record in its contribution
// repository and one target it reports through the Evidence Server. Both are
// read by one Evidence Server source, as fleetsrc.EvidenceHTTPSource reads
// them, and its row is fleet.ProducerRow's, so the demo shares the wording.
//
// The repository lies under the story's registry: a contribution resolves its
// names in the registry before /pacto-inferred, and the story's is the demo
// registry with services in a domain of their own.

const (
	demoProducer               = "acme-idp"
	demoEvidenceSource         = "evidence-server"
	demoContributionRepository = story.Domain + "/" + contract.InferredRepository + "/" + demoProducer + "/" + contract.ContributionsRepository
	demoFreshness              = 24 * time.Hour
	// demoContributionYAML is ledger-sync's pacto.yaml: a contribution
	// declares only its name and version.
	demoContributionYAML = "pactoVersion: \"2.5\"\nservice:\n  name: ledger-sync\n  version: 0.1.0\n"
	// demoContributionExport is the export acme-idp mapped the record from.
	demoContributionExport = "{\"ledger-sync\":{\"dependsOn\":[\"fraud\"]}}\n"
)

// demoContributionTimes are when acme-idp made its record and last reported.
func demoContributionTimes() (recorded, reported time.Time) {
	return demoNow().Add(-3 * time.Hour), demoNow().Add(-3 * time.Minute)
}

// demoContribution is ledger-sync 0.1.0 as the contribution reader returns it
// once its signature verified with acme-idp's key.
func demoContribution() fleet.RawRevision {
	sum := sha256.Sum256([]byte(demoContributionExport))
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "demo", Discovery: "1",
			Sources: []contract.InferredSourceFile{{Path: "export.json", Digest: "sha256:" + hex.EncodeToString(sum[:])}}},
		Fields: []contract.Inferred{{Field: "dependencies[fraud]", Value: "fraud", Source: "export.json:1", Confidence: contract.ConfidenceLikely}}}
	c, err := contract.Parse(strings.NewReader(demoContributionYAML))
	if err != nil {
		panic(err) // a constant: TestDemoContributionIsInferred fails first
	}
	b := &contract.Bundle{Contract: c, RawYAML: []byte(demoContributionYAML), FS: fstest.MapFS{
		"pacto.yaml": {Data: []byte(demoContributionYAML)}, "export.json": {Data: []byte(demoContributionExport)},
		contract.InferredRecordFile: {Data: rec.Marshal()},
	}}
	yamlSum := sha256.Sum256(b.RawYAML)
	digest := "sha256:" + hex.EncodeToString(yamlSum[:])
	recorded, _ := demoContributionTimes()
	resolved := contract.OCIScheme + demoContributionRepository + "@" + digest
	return fleet.RawRevision{Bundle: b, Domain: contract.OCIDomain(resolved),
		RequestedRef: contract.OCIScheme + demoContributionRepository + ":ledger-sync_0.1.0", ResolvedRef: resolved, Digest: digest,
		Contributor: &fleet.Contributor{Producer: demoProducer, KeyID: demoProducer + "-2026", ReadAt: demoNow(), AsOf: recorded}}
}

// demoProducerFacts is what the Evidence Server source knows of acme-idp.
func demoProducerFacts() fleet.ProducerFacts {
	recorded, reported := demoContributionTimes()
	return fleet.ProducerFacts{Producer: demoProducer, NewestReport: &reported, Repository: demoContributionRepository,
		Read: 1, NewestContribution: &recorded}
}

// demoEvidenceServer is the Evidence Server source: acme-idp's contribution,
// the story's fraud as the external target it reports, and its row.
func demoEvidenceServer(cluster *fleet.Cluster) (fleet.Source, error) {
	fraud, err := story.Revision("fraud", "1.0.0")
	if err != nil {
		return nil, err
	}
	_, reported := demoContributionTimes()
	_, digest := contract.SplitOCIDigest(fraud.ContractRef)
	row, _ := fleet.ProducerRow(demoProducerFacts(), demoNow(), demoFreshness)
	return fleet.NewMemorySource(demoEvidenceSource, fleet.SourceKindEvidenceHTTP, &fleet.Collection{
		Cluster:   cluster,
		Revisions: []fleet.RawRevision{demoContribution()},
		Targets: []fleet.RawTarget{{Scope: demoProducer, Kind: "external", Name: "fraud", Service: "fraud", Domain: story.Domain,
			ResolvedRef: fraud.ContractRef, Digest: digest, Compliance: fleet.StatusCompliant,
			Coverage: &fleet.Coverage{Evaluated: 2, Required: 2}, EvidenceAt: &reported, ReconciledAt: &reported}},
		Providers: []fleet.Provider{row},
	}), nil
}
