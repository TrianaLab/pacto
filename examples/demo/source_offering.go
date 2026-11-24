package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"strings"
	"testing/fstest"
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/lock"
	"github.com/trianalab/pacto/v4/pkg/schemax"
)

// This file holds the inferred offerings the production cluster's operator
// maps from its Crossplane XRDs (515-S4): signed with the installation key,
// pushed to the installation's own contribution repository and read through
// the cluster's detection, as fleetsrc.DetectionSource reads them. xbucket
// is inferred only; objectstore was accepted at generation 3 as the declared
// objectstore 1.0.0 and its XRD has since changed, so that offering drifts.
// Nothing here is a published bundle: every revision is built in memory.

const (
	demoOfferingProducer = "crossplane-" + prodClusterID
	// demoOfferingRepository is the installation's own repository, under
	// the registry its detection advertises.
	demoOfferingRepository = "ghcr.io/acme/" + contract.InferredRepository + "/" + demoOfferingProducer + "/" + contract.ContributionsRepository
	// demoOfferingKeyID is the installation key detected.json advertises.
	demoOfferingKeyID   = "339e2ff917630507b6a423b5ce084e285d1fa65d93b3e27a6195d3bbebc9ae23"
	demoOfferingSchema  = "configuration/provisioning.schema.json"
	demoOfferingObject  = "source/object.yaml"
	demoOfferingClasses = "source/classes.yaml"
	// demoOfferingNoDraft is why the demo saves no offering draft.
	demoOfferingNoDraft = "the demo accepts an offering only with a person's signature, and nobody signs in the demo"
	// demoObjectstoreAsker is the person who accepted objectstore.
	demoObjectstoreAsker = "dana@acme.example"
)

// demoXRD is one XRD as the operator mapped it.
type demoXRD struct {
	slug, kind, name, summary string
	generation                int64
	classes                   []string // sorted Composition names
	properties                string   // the JSON of the spec's properties, closed
}

var (
	demoXBucket = demoXRD{slug: "xbucket", kind: "CompositeResourceDefinition", name: "xbuckets.storage.acme.io", generation: 2,
		summary: "An object storage bucket.", classes: []string{"large", "small"},
		properties: `"region": {"type": "string", "enum": ["eu-west-1", "us-east-1"], "x-pacto-tier": "contract"},
    "storageGB": {"type": "integer", "minimum": 1, "maximum": 500, "x-pacto-tier": "contract"}`}
	demoObjectstore3 = demoXRD{slug: "objectstore", kind: "CompositeResourceDefinition", name: "xobjectstores.storage.acme.io", generation: 3,
		summary: "A shared object store.", classes: []string{"archive", "standard"},
		properties: `"region": {"type": "string", "enum": ["eu-west-1"], "x-pacto-tier": "contract"}`}
	// demoObjectstore4 is the XRD after a region was added: what drifted.
	demoObjectstore4 = demoXRD{slug: "objectstore", kind: demoObjectstore3.kind, name: demoObjectstore3.name, generation: 4,
		summary: demoObjectstore3.summary, classes: demoObjectstore3.classes,
		properties: `"region": {"type": "string", "enum": ["eu-west-1", "us-east-1"], "x-pacto-tier": "contract"}`}
)

// version is the inferred version of x, 0.0.0-inferred.<generation>.0.
func (x demoXRD) version() string { return fmt.Sprintf("0.0.0-inferred.%d.0", x.generation) }

// schema is x's provisioning schema: its properties plus the class enum,
// every object closed.
func (x demoXRD) schema() string {
	classes, _ := json.Marshal(x.classes)
	return fmt.Sprintf(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["region", "class"],
  "properties": {
    %s,
    "class": {"type": "string", "enum": %s, "x-pacto-tier": "contract"}
  }
}
`, x.properties, classes)
}

// files are x's bundle in the public inferred-offering format: pacto.yaml
// with the offering section, the schema, the source object and classes and
// the record citing them.
func (x demoXRD) files() fstest.MapFS {
	pacto := fmt.Sprintf("pactoVersion: \"2.5\"\nservice:\n  name: %s\n  version: %s\n  offering: %s\n  summary: %s\nconfigurations:\n- name: provisioning\n  schema: %s\n  required: true\n",
		x.slug, x.version(), x.slug, x.summary, demoOfferingSchema)
	object := fmt.Sprintf("apiVersion: apiextensions.crossplane.io/v1\nkind: %s\nmetadata:\n  name: %s\n  generation: %d\n", x.kind, x.name, x.generation)
	var classes strings.Builder
	classes.WriteString("compositions:\n")
	for _, c := range x.classes {
		fmt.Fprintf(&classes, "- apiVersion: apiextensions.crossplane.io/v1\n  kind: Composition\n  name: %s\n", c)
	}
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "6.5.0", Discovery: "xrdoffering/1", Sources: []contract.InferredSourceFile{
			{Path: demoOfferingClasses, Digest: digestOf(classes.String())}, {Path: demoOfferingObject, Digest: digestOf(object)}}},
		Fields: []contract.Inferred{
			{Field: "service.offering", Value: x.slug, Source: demoOfferingObject, Confidence: contract.ConfidenceLikely},
			{Field: "configurations[provisioning]", Value: demoOfferingSchema, Source: demoOfferingObject, Confidence: contract.ConfidenceLikely},
			{Field: "configurations[provisioning].class", Value: strings.Join(x.classes, ", "), Source: demoOfferingClasses, Confidence: contract.ConfidenceLikely},
		}}
	return fstest.MapFS{"pacto.yaml": {Data: []byte(pacto)}, demoOfferingSchema: {Data: []byte(x.schema())},
		demoOfferingObject: {Data: []byte(object)}, demoOfferingClasses: {Data: []byte(classes.String())},
		contract.InferredRecordFile: {Data: rec.Marshal()}}
}

// bundle is fsys as a bundle with its content digest.
func offeringBundle(fsys fstest.MapFS) (*contract.Bundle, *contract.Document, string) {
	raw := fsys["pacto.yaml"].Data
	c, doc, err := contract.ParseDocument(raw)
	if err != nil {
		panic(err) // the constants above
	}
	h, err := lock.HashFS(fsys)
	if err != nil {
		panic(err) // an in-memory FS
	}
	return &contract.Bundle{Contract: c, RawYAML: raw, FS: fsys}, doc, h
}

// digest is the content digest of x's bundle.
func (x demoXRD) digest() string {
	_, _, h := offeringBundle(x.files())
	return h
}

// revision is x as the detection's contribution reader returns it once its
// signature verified with the installation key, made recorded.
func (x demoXRD) revision(recorded time.Time) fleet.RawRevision {
	b, doc, h := offeringBundle(x.files())
	resolved := contract.OCIScheme + demoOfferingRepository + "@" + h
	return fleet.RawRevision{Bundle: b, Document: doc, Domain: contract.OCIDomain(resolved),
		RequestedRef: contract.OCIScheme + demoOfferingRepository + ":" + x.slug + "_" + x.version(), ResolvedRef: resolved, Digest: h,
		Contributor: &fleet.Contributor{Producer: demoOfferingProducer, KeyID: demoOfferingKeyID, Cluster: prodClusterID, ReadAt: demoNow(), AsOf: recorded}}
}

// demoOfferingTimes are when the operator pushed its newest record and when
// its last complete run finished.
func demoOfferingTimes() (recorded, observed time.Time) {
	return demoNow().Add(-40 * time.Minute), demoNow().Add(-2 * time.Minute)
}

// demoInferredOfferings are the operator's newest records: xbucket and
// objectstore at generation 4.
func demoInferredOfferings() []fleet.RawRevision {
	recorded, _ := demoOfferingTimes()
	return []fleet.RawRevision{demoXBucket.revision(recorded), demoObjectstore4.revision(recorded)}
}

// demoOfferingObservation is what the installation advertised it observed.
func demoOfferingObservation() fleet.OfferingObservation {
	_, observed := demoOfferingTimes()
	return fleet.OfferingObservation{Producer: demoOfferingProducer, Cluster: prodClusterID, Observed: []string{"objectstore", "xbucket"}, AsOf: &observed}
}

// demoOfferingRow is the producer row of the installation's offerings.
func demoOfferingRow() fleet.Provider {
	recorded, _ := demoOfferingTimes()
	row, _ := fleet.ProducerRow(fleet.ProducerFacts{Producer: demoOfferingProducer, Repository: demoOfferingRepository,
		Read: 2, NewestContribution: &recorded}, demoNow(), demoFreshness)
	row.Cluster, row.ClusterID = "production", prodClusterID
	return row
}

// demoObjectstorePacto is objectstore 1.0.0 as dana accepted it.
const demoObjectstorePacto = `pactoVersion: "2.5"
service:
  name: objectstore
  version: 1.0.0
  offering: objectstore
  summary: A shared object store.
  owner:
    team: team-storage
configurations:
- name: provisioning
  schema: configuration/provisioning.schema.json
  required: true
`

// demoDeclaredObjectstore is objectstore 1.0.0, which dana accepted from
// the generation 3 record, signed with her own identity. Built in memory
// and never published.
func demoDeclaredObjectstore() (fleet.RawRevision, *contract.Bundle) {
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "4.6.0"},
		Declaration: &contract.InferredDeclaration{State: contract.DeclarationDeclared,
			Asker:        contract.InferredSigner{Subject: demoObjectstoreAsker, Issuer: handoverIssuer, Class: contract.SignerClassHuman},
			InferredFrom: contract.OfferingInferredFrom(demoOfferingProducer, "objectstore", demoObjectstore3.digest())}}
	b, doc, h := offeringBundle(fstest.MapFS{"pacto.yaml": {Data: []byte(demoObjectstorePacto)},
		demoOfferingSchema: {Data: []byte(demoObjectstore3.schema())}, contract.InferredRecordFile: {Data: rec.Marshal()}})
	return fleet.RawRevision{Bundle: b, Document: doc, RequestedRef: requestedRef(demoRegistry, "objectstore", "1.0.0"),
		ResolvedRef: demoRegistry + "objectstore@" + h, Digest: h,
		Provenance: &fleet.BundleProvenance{Signature: fleet.BundleSignature{Status: fleet.SignatureUnverified},
			Signers: []fleet.VerifiedSigner{{Subject: demoObjectstoreAsker, Issuer: handoverIssuer, Class: contract.SignerClassHuman}},
			Source:  "local", CheckedAt: demoNow()}}, b
}

// demoDeclaredOfferings are the offerings already declared, by name, with
// their version: a later acceptance must be above it and names no owner.
var demoDeclaredOfferings = map[string]string{"objectstore": "1.0.0"}

// planOffering is what accepting the inferred offering w as a declared
// offering would publish: planned in the tab, signed by nobody.
func planOffering(w dashboard.DeclareWorkload, in *dashboard.OfferingInput) *dashboard.DeclareAnswer {
	in = cmp.Or(in, &dashboard.OfferingInput{})
	name := cmp.Or(in.Name, w.Offering)
	repo, _, digest := contract.SplitOCIRef(w.From)
	producer, _ := contract.ContributionRef(repo)
	registry := cmp.Or(in.Registry, demoDeclareRegistry)
	command := func(intent string) string {
		parts := []string{"pacto declare", "offering:" + name, "--from", contract.OCIScheme + w.From,
			"--detected", "k8s:pacto-system/pacto-operator-detected", "--registry", registry, "--version", cmp.Or(in.Version, "<version>")}
		if in.Owner != "" {
			parts = append(parts, "--owner", in.Owner)
		}
		if intent != "" {
			parts = append(parts, "--intent", intent)
		}
		return strings.Join(parts, " ")
	}
	ans := &dashboard.DeclareAnswer{Signing: demoSigning(), Write: dashboard.DeclareWrite{Available: true}, NoDraft: demoOfferingNoDraft}
	refuse := func(code, msg string) *dashboard.DeclareAnswer {
		ans.Refusal = &dashboard.DeclareRefusal{Code: code, Message: msg, Command: command("")}
		return ans
	}
	x, ok := demoOfferingSources()[w.From]
	declared := demoDeclaredOfferings[name]
	v, verr := semver.StrictNewVersion(in.Version)
	switch {
	case !ok:
		return refuse("OfferingSourceInvalid", w.From+" is no inferred offering this tab read")
	case name != x.slug:
		return refuse("OfferingMismatch", "the inferred offering offers "+x.slug+", not "+name)
	case in.Version == "":
		return refuse("VersionRequired", "set the version the declared offering is published at")
	case verr != nil || v.Prerelease() != "" || (declared != "" && !v.GreaterThan(semver.MustParse(declared))):
		return refuse("VersionConflict", in.Version+" is no release version above "+cmp.Or(declared, "0.0.0"))
	}
	var doc any
	_ = json.Unmarshal([]byte(x.schema()), &doc) // a constant above
	if open := schemax.OpenPaths(doc); len(open) > 0 {
		return refuse("OfferingSchemaOpen", "the offering's parameters are not a closed space, open at provisioning: "+strings.Join(open, ", provisioning: ")+
			": close each path in a folder (additionalProperties: false, a type, a class enum) and accept it with --dir")
	}
	if in.Owner == "" && declared == "" {
		return refuse("OwnerRequired", "name the team that owns "+name+": it is accepted for the first time")
	}
	text := fmt.Sprintf("pactoVersion: \"2.5\"\nservice:\n  name: %s\n  version: %s\n  offering: %s\n  summary: %s\n", name, in.Version, name, x.summary)
	if in.Owner != "" {
		text += "  owner:\n    team: " + in.Owner + "\n"
	}
	text += "configurations:\n- name: provisioning\n  schema: " + demoOfferingSchema + "\n  required: true\n"
	from := contract.OfferingInferredFrom(producer, name, digest)
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "4.6.0"},
		Declaration: &contract.InferredDeclaration{State: contract.DeclarationDeclared, Asker: unsignedAsker, InferredFrom: from}}
	p := &dashboard.DeclarePlan{Service: name, Version: in.Version, BundleRepo: registry + "/" + name, State: contract.DeclarationDeclared,
		InferredFrom: from, Cluster: w.Cluster, Deploy: "none", Ranges: []dashboard.DeclareRange{}, Owner: in.Owner,
		Signer:   dashboard.DeclareSigner{Class: contract.SignerClassAgent, Source: "none", Why: demoNoSignIn},
		Contract: dashboard.NewDeclareText([]byte(text)), Record: dashboard.NewDeclareText(rec.Marshal()),
		Notes: []string{"Accepted from " + x.kind + " " + x.name + " generation " + fmt.Sprint(x.generation) + ", signed by " + producer}}
	p.IntentDigest = digestOf(p.Contract.Text + p.Record.Text)
	ans.Plan, ans.Command = p, command(p.IntentDigest)
	return ans
}

// demoOfferingSources are the inferred offerings the tab read, by the
// repository@digest the fleet shows.
func demoOfferingSources() map[string]demoXRD {
	out := map[string]demoXRD{}
	for _, x := range []demoXRD{demoXBucket, demoObjectstore4} {
		out[demoOfferingRepository+"@"+x.digest()] = x
	}
	return out
}
