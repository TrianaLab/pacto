package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const (
	offRoot     = "registry.pacto.demo/demo"
	offCluster  = "11111111-2222-3333-4444-555555555555"
	offProducer = "idp-" + offCluster
	offRepo     = offRoot + "/pacto-inferred/" + offProducer + "/services"
	offSchema   = "configuration/provisioning.json"
	offObject   = "source/object.yaml"
)

func offSum(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// offContribution is producer's inferred offering slug at generation gen
// under repo, made asOf and advertised in cluster ("" for a grant).
func offContribution(slug string, gen int, repo, producer, cluster string, asOf time.Time) RawRevision {
	object := []byte(fmt.Sprintf("apiVersion: example.io/v1\nkind: SourceKind\nmetadata:\n  name: %ss.example.io\n  generation: %d\nspec: {}\n", slug, gen))
	schema := []byte(`{"type":"object","properties":{"class":{"type":"string","enum":["small","large"]}},"additionalProperties":false}`)
	return offContributionOf(slug, gen, repo, producer, cluster, asOf, object, schema)
}

// offContributionOf is offContribution with its source object and schema.
func offContributionOf(slug string, gen int, repo, producer, cluster string, asOf time.Time, object, schema []byte) RawRevision {
	version := fmt.Sprintf("0.0.0-inferred.%d.0", gen)
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1", Sources: []contract.InferredSourceFile{{Path: offObject, Digest: offSum(object)}}},
		Fields: []contract.Inferred{{Field: "service.offering", Value: slug, Source: offObject, Confidence: contract.ConfidenceLikely}}}
	raw := "pactoVersion: \"2.5\"\nservice:\n  name: " + slug + "\n  version: " + version + "\n  offering: " + slug +
		"\n  summary: An " + slug + "\nconfigurations:\n  - name: provisioning\n    schema: " + offSchema + "\n    required: true\n"
	c := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: slug, Version: version, Offering: slug, Summary: "An " + slug},
		Configurations: []contract.Configuration{{Name: "provisioning", Schema: offSchema, Required: true}}}
	fsys := fstest.MapFS{offSchema: {Data: schema}, offObject: {Data: object}, contract.InferredRecordFile: {Data: rec.Marshal()}}
	digest := offSum([]byte(producer + slug + version))
	return RawRevision{Bundle: &contract.Bundle{Contract: c, RawYAML: []byte(raw), FS: fsys}, Domain: "wrong.example/x",
		ResolvedRef: repo + "@" + digest, Digest: digest,
		Contributor: &Contributor{Producer: producer, KeyID: "installation", ReadAt: fixedNow(), AsOf: asOf, Cluster: cluster}}
}

// xbucket is the installation's xbucket at generation gen, made an hour ago.
func xbucket(gen int) RawRevision {
	return offContribution("xbucket", gen, offRepo, offProducer, offCluster, fixedNow().Add(-time.Hour))
}

// offAccepted is ana's declared offering slug at version under domain,
// accepted from from (none when "").
func offAccepted(slug, version, domain, from string) RawRevision {
	ct := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: slug, Version: version, Offering: slug, Owner: contract.Owner{Team: "team-storage"}}}
	fsys := fstest.MapFS{}
	if from != "" {
		rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1"}, Fields: []contract.Inferred{},
			Declaration: &contract.InferredDeclaration{State: contract.DeclarationDeclared, Asker: ana, InferredFrom: from}}
		fsys[contract.InferredRecordFile] = &fstest.MapFile{Data: rec.Marshal()}
	}
	return RawRevision{Bundle: &contract.Bundle{Contract: ct, FS: fsys}, Domain: domain, ResolvedRef: domain + "/" + slug + ":" + version,
		Digest: "sha256:" + slug + "-" + version, Provenance: anaSigned()}
}

// fromOf is the inferredFrom of an offering accepted from rev.
func fromOf(rev RawRevision) string {
	return "offering:" + rev.Contributor.Producer + "/" + rev.Bundle.Contract.Service.Name + "@" + rev.Digest
}

// freshObs is the installation's complete observation of slugs a minute ago.
func freshObs(slugs ...string) OfferingObservation {
	return OfferingObservation{Producer: offProducer, Cluster: offCluster, Observed: slugs, AsOf: ptrTime(fixedNow().Add(-time.Minute))}
}

// offFleet builds the declared revisions (a registry source), the
// contributions and observations (a detection source) and extra sources.
func offFleet(t *testing.T, declared, contributed []RawRevision, obs []OfferingObservation, extra ...Source) *Query {
	t.Helper()
	sources := append([]Source{NewMemorySource("oci", "oci", &Collection{Revisions: declared}),
		NewMemorySource("detection", SourceKindDetection, &Collection{Revisions: contributed, Offerings: obs, Advisory: true})}, extra...)
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: 24 * time.Hour, DeclarationSigners: anaPolicy()}, sources...)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func offEntities(t *testing.T, q *Query, f EntityFilter) *EntityList {
	t.Helper()
	f.Kinds = []EntityKind{KindOffering}
	res, err := q.Entities(f)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

var xbucketKey = NewServiceKeyDomain(offRoot+"/pacto-inferred/"+offProducer, "xbucket")

// declaredPostgres is a declared offering with no inferred source.
func declaredPostgres() RawRevision { return offAccepted("postgresql", "16.1.0", offRoot, "") }

func TestInferredOfferingNotInOfferingList(t *testing.T) {
	q := offFleet(t, []RawRevision{declaredPostgres()}, []RawRevision{xbucket(2)}, []OfferingObservation{freshObs("xbucket")})
	res := offEntities(t, q, EntityFilter{})
	if len(res.Entities) != 1 || res.Entities[0].Key != "postgresql" || res.Entities[0].Kind != KindOffering {
		t.Fatalf("offerings = %+v", res.Entities)
	}
	if s := q.snap.Services[xbucketKey]; s == nil || s.Offering != "xbucket" || !s.Inferred() {
		t.Fatalf("the contributed offering = %+v", s)
	}
}

func TestDeclarationFilterOfferingCase(t *testing.T) {
	q := offFleet(t, []RawRevision{declaredPostgres()}, []RawRevision{xbucket(2)}, []OfferingObservation{freshObs("xbucket")})
	res := offEntities(t, q, EntityFilter{Declaration: DeclarationInferred})
	want := &InferredOfferingFacts{Producer: offProducer, Offering: "xbucket", From: "SourceKind xbuckets.example.io", Generation: 2,
		Classes: []string{"large", "small"}, AsOf: ptrTime(fixedNow().Add(-time.Hour))}
	if len(res.Entities) != 1 || res.Entities[0].Kind != KindService || res.Entities[0].Key != string(xbucketKey) ||
		res.Entities[0].Declaration != DeclarationInferred || !reflect.DeepEqual(res.Entities[0].InferredOffering, want) {
		t.Fatalf("inferred offerings = %+v", res.Entities)
	}
	declared, empty := offEntities(t, q, EntityFilter{Declaration: DeclarationDeclared}), offEntities(t, q, EntityFilter{})
	if !reflect.DeepEqual(declared.Entities, empty.Entities) || declared.Total != 1 || declared.Entities[0].InferredOffering != nil {
		t.Fatalf("declared %+v, empty %+v", declared.Entities, empty.Entities)
	}
	// Asked with services too, the inferred offering is one row with its facts.
	both, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService, KindOffering}, Declaration: DeclarationInferred})
	if err != nil || len(both.Entities) != 1 || both.Entities[0].InferredOffering == nil {
		t.Fatalf("services and offerings = %+v, %v", both, err)
	}
}

func TestInferredOfferingNotInTotal(t *testing.T) {
	q := offFleet(t, []RawRevision{declaredPostgres()},
		[]RawRevision{xbucket(2), offContribution("xqueue", 1, offRepo, offProducer, offCluster, fixedNow())}, []OfferingObservation{freshObs("xbucket", "xqueue")})
	declared := offEntities(t, q, EntityFilter{})
	if declared.Total != 1 || declared.InferredTotal != 2 {
		t.Fatalf("declared total %d, inferred %d", declared.Total, declared.InferredTotal)
	}
	inferred := offEntities(t, q, EntityFilter{Declaration: DeclarationInferred, Limit: 1})
	if inferred.Total != 2 || inferred.Count != 1 || !inferred.Truncated || inferred.InferredTotal != 0 {
		t.Fatalf("inferred page = %+v", inferred)
	}
}

func TestInferredOfferingNotInAggregates(t *testing.T) {
	with := offFleet(t, []RawRevision{declaredPostgres()}, []RawRevision{xbucket(2)}, []OfferingObservation{freshObs("xbucket")})
	without := offFleet(t, []RawRevision{declaredPostgres()}, nil, nil)
	if a, b := offEntities(t, with, EntityFilter{}).Aggregate, offEntities(t, without, EntityFilter{}).Aggregate; !reflect.DeepEqual(a, b) {
		t.Fatalf("aggregate with an inferred offering %+v, without %+v", a, b)
	}
}

func TestInferredOfferingHasNoConsumeSnippet(t *testing.T) {
	q := offFleet(t, nil, []RawRevision{xbucket(2)}, []OfferingObservation{freshObs("xbucket")})
	d, err := q.EntityDetail(KindOffering, "xbucket")
	if err == nil && d.Offering != nil && len(d.Offering.Providers.Items) > 0 {
		t.Fatalf("an inferred offering has a provider: %+v", d.Offering.Providers.Items)
	}
}

// offConsumer is a consumer running in shop that depends on dep.
func offConsumer(dep contract.Dependency) func() (RawRevision, RawTarget) {
	return svc("checkout", "shop", dep)
}

func TestInferredOfferingNeverSatisfiesRange(t *testing.T) {
	for name, dep := range map[string]contract.Dependency{
		"by name": {Name: "xbucket", Compatibility: ">=0.0.0-0"},
		"by ref":  {Name: "bucket", Ref: "oci://" + offRepo, Compatibility: ">=0.0.0-0"},
	} {
		r, tg := offConsumer(dep)()
		q := offFleet(t, nil, []RawRevision{xbucket(2)}, []OfferingObservation{freshObs("xbucket")},
			NewMemorySource("k8s", SourceKindKubernetes, &Collection{Revisions: []RawRevision{r}, Targets: []RawTarget{tg}}))
		for _, rel := range q.snap.Relationships {
			if rel.FromService == NewServiceKey("checkout") && rel.RangeStatus == "satisfied" {
				t.Errorf("%s: an inferred offering satisfied a range: %+v", name, rel)
			}
		}
	}
}

func TestInferredOfferingNeverClearsGate(t *testing.T) {
	r, tg := offConsumer(contract.Dependency{Name: "bucket", Ref: "oci://" + offRepo, Compatibility: "^0.0.0-0"})()
	q := offFleet(t, nil, []RawRevision{xbucket(2)}, []OfferingObservation{freshObs("xbucket")},
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Revisions: []RawRevision{r}, Targets: []RawTarget{tg}}))
	if slices.Contains(q.snap.reverseDeps[xbucketKey], NewServiceKey("checkout")) {
		t.Fatal("a use of an inferred offering entered the declared index a gate reads")
	}
	for _, rel := range q.snap.Relationships {
		if rel.FromService == NewServiceKey("checkout") && rel.ToService == xbucketKey && rel.Compatibility != contract.CompatibilityUnknown {
			t.Errorf("edge to an inferred offering = %+v", rel)
		}
	}
}

func TestExternalOfferingContributionThroughGrant(t *testing.T) {
	const repo = offRoot + "/pacto-inferred/my-idp/services"
	grant := offContribution("xcache", 1, repo, "my-idp", "", fixedNow())
	q := offFleet(t, nil, nil, nil, NewMemorySource("evidence", SourceKindEvidenceHTTP, &Collection{Revisions: []RawRevision{grant}}))
	res := offEntities(t, q, EntityFilter{Declaration: DeclarationInferred})
	if len(res.Entities) != 1 || res.Entities[0].InferredOffering.Producer != "my-idp" || res.Entities[0].InferredOffering.From != "SourceKind xcaches.example.io" {
		t.Fatalf("grant offering = %+v", res.Entities)
	}
}

// otherCluster may read every scope but offCluster's.
func otherCluster(c, _ string) bool { return c != offCluster }

func TestRestrictInferredOfferingHiddenWithoutClusterScope(t *testing.T) {
	// xother was accepted from a record nobody read: its drift stays.
	xother := offAccepted("xother", "1.0.0", offRoot, "offering:my-idp/xother@"+offSum([]byte("x")))
	q := offFleet(t, []RawRevision{declaredPostgres(), xother}, []RawRevision{xbucket(2)}, []OfferingObservation{freshObs("xbucket")}).Restrict(otherCluster, everyKind)
	unknownBecause(t, q.snap.Services[NewServiceKeyDomain(offRoot, "xother")].OfferingDrift, "the producer reports no observation")
	if q.snap.Services[xbucketKey] != nil || len(limitationsCoded(q.snap.Limitations, LimitationHiddenByPermissions)) != 1 {
		t.Fatalf("hidden inferred offering: %+v / %+v", q.snap.Services[xbucketKey], q.snap.Limitations)
	}
	if n := offEntities(t, q, EntityFilter{}).InferredTotal; n != 0 {
		t.Errorf("a hidden inferred offering is counted: %d", n)
	}
}

func TestRestrictInferredOfferingNamespaceViewerHidden(t *testing.T) {
	inShop := func(_, ns string) bool { return ns == "shop" }
	q := offFleet(t, nil, []RawRevision{xbucket(2)}, []OfferingObservation{freshObs("xbucket")}).Restrict(inShop, everyKind)
	if q.snap.Services[xbucketKey] != nil {
		t.Fatal("a namespace viewer sees an inferred offering of a cluster")
	}
}

func TestRestrictContributionWithoutClusterUnchanged(t *testing.T) {
	grant := offContribution("xcache", 1, offRoot+"/pacto-inferred/my-idp/services", "my-idp", "", fixedNow())
	q := offFleet(t, nil, nil, nil, NewMemorySource("evidence", SourceKindEvidenceHTTP, &Collection{Revisions: []RawRevision{grant}})).Restrict(otherCluster, everyKind)
	if q.snap.Services[NewServiceKeyDomain(offRoot+"/pacto-inferred/my-idp", "xcache")] == nil {
		t.Fatal("a contribution with no cluster was hidden")
	}
}

func TestOfferingDeclarationVisibleToRestrictedViewer(t *testing.T) {
	from := fromOf(xbucket(2))
	rev := &ContractRevision{Record: &contract.InferredRecord{Declaration: &contract.InferredDeclaration{InferredFrom: from}}}
	if declarationSourceHidden(&FleetSnapshot{}, rev, func(_, _, _ string) bool { return true }) {
		t.Fatal("an offering declaration is hidden by workload scope")
	}
}

func TestOfferingDeclarationNamesNoWorkload(t *testing.T) {
	src := xbucket(2)
	if namesWorkload(fromOf(src)) {
		t.Fatal("an offering inferredFrom names a workload")
	}
	q := offFleet(t, []RawRevision{offAccepted("xbucket", "1.0.0", offRoot+"/offerings", fromOf(src))}, []RawRevision{src}, []OfferingObservation{freshObs("xbucket")})
	for _, l := range q.snap.Limitations {
		if strings.Contains(l.Message, "workload") {
			t.Errorf("limitation about a workload: %+v", l)
		}
	}
}

// acceptedDrift is the drift of the xbucket ana accepted under root from
// accepted, with the installation's contributions and observations.
func acceptedDrift(t *testing.T, root string, accepted RawRevision, contributed []RawRevision, obs []OfferingObservation) (*Query, *OfferingDrift) {
	t.Helper()
	q := offFleet(t, []RawRevision{offAccepted("xbucket", "1.0.0", root, fromOf(accepted))}, contributed, obs)
	s := q.snap.Services[NewServiceKeyDomain(root, "xbucket")]
	if s == nil {
		t.Fatal("no declared xbucket")
	}
	return q, s.OfferingDrift
}

func TestOfferingDriftDetected(t *testing.T) {
	_, d := acceptedDrift(t, offRoot+"/offerings", xbucket(3), []RawRevision{xbucket(4)}, []OfferingObservation{freshObs("xbucket")})
	if d == nil || d.State != DriftDrifted || d.Reason != "source object changed since accepted (now generation 4)" ||
		d.NewestVersion != "0.0.0-inferred.4.0" || d.NewestRef != offRepo+"@"+xbucket(4).Digest || d.AcceptedDigest != xbucket(3).Digest || d.Producer != offProducer {
		t.Fatalf("drift = %+v", d)
	}
}

func TestOfferingDriftNoneWhenEqual(t *testing.T) {
	if _, d := acceptedDrift(t, offRoot+"/offerings", xbucket(3), []RawRevision{xbucket(3)}, []OfferingObservation{freshObs("xbucket")}); d != nil {
		t.Fatalf("drift = %+v", d)
	}
}

// unknownBecause asserts d is unknown for reason.
func unknownBecause(t *testing.T, d *OfferingDrift, reason string) {
	t.Helper()
	if d == nil || d.State != DriftUnknown || !strings.Contains(d.Reason, reason) {
		t.Fatalf("drift = %+v, want unknown because %q", d, reason)
	}
}

func TestDriftUnknownWhenProducerUnread(t *testing.T) {
	obs := freshObs("xbucket")
	obs.Unread = "registry unavailable"
	_, d := acceptedDrift(t, offRoot+"/offerings", xbucket(3), nil, []OfferingObservation{obs})
	unknownBecause(t, d, "not all read: registry unavailable")
}

func TestDriftUnknownWhenSourceDeleted(t *testing.T) {
	_, d := acceptedDrift(t, offRoot+"/offerings", xbucket(3), []RawRevision{xbucket(3)}, []OfferingObservation{freshObs("xqueue")})
	unknownBecause(t, d, "source object no longer observed")
}

func TestDriftUnknownWhenAdvertisementStale(t *testing.T) {
	obs := freshObs("xbucket")
	obs.AsOf = ptrTime(fixedNow().Add(-48 * time.Hour))
	_, d := acceptedDrift(t, offRoot+"/offerings", xbucket(3), []RawRevision{xbucket(3)}, []OfferingObservation{obs})
	unknownBecause(t, d, "observation is stale")
	obs.AsOf = nil
	_, d = acceptedDrift(t, offRoot+"/offerings", xbucket(3), []RawRevision{xbucket(3)}, []OfferingObservation{obs})
	unknownBecause(t, d, "observation is stale")
}

func TestDriftUnknownWhenSlugPastBound(t *testing.T) {
	obs := freshObs()
	obs.Partial = "201 source objects, at most 200 read"
	_, d := acceptedDrift(t, offRoot+"/offerings", xbucket(3), []RawRevision{xbucket(3)}, []OfferingObservation{obs})
	unknownBecause(t, d, "stopped short: 201 source objects")
}

func TestDriftUnknownWithoutObservation(t *testing.T) {
	_, d := acceptedDrift(t, offRoot+"/offerings", xbucket(3), []RawRevision{xbucket(3)}, nil)
	unknownBecause(t, d, "the producer reports no observation")
}

func TestDriftUnknownWithoutHead(t *testing.T) {
	_, d := acceptedDrift(t, offRoot+"/offerings", xbucket(3), nil, []OfferingObservation{freshObs("xbucket")})
	unknownBecause(t, d, "no record of the source object was read")
}

func TestDriftUnknownWhenTwoInstallationsClaimProducer(t *testing.T) {
	_, d := acceptedDrift(t, offRoot+"/offerings", xbucket(3), []RawRevision{xbucket(3)}, []OfferingObservation{freshObs("xbucket"), freshObs("xbucket")})
	unknownBecause(t, d, "more than one installation advertises producer")
}

func TestDriftUnknownWhenHidden(t *testing.T) {
	q, _ := acceptedDrift(t, offRoot+"/offerings", xbucket(3), []RawRevision{xbucket(4)}, []OfferingObservation{freshObs("xbucket")})
	r := q.Restrict(otherCluster, everyKind)
	d := r.snap.Services[NewServiceKeyDomain(offRoot+"/offerings", "xbucket")].OfferingDrift
	unknownBecause(t, d, "the inferred offering is outside your view")
	if d.NewestRef != "" || d.NewestVersion != "" || d.AsOf != nil || d.AcceptedDigest != xbucket(3).Digest {
		t.Errorf("hidden drift names the record: %+v", d)
	}
	// The base snapshot keeps its drift.
	if q.snap.Services[NewServiceKeyDomain(offRoot+"/offerings", "xbucket")].OfferingDrift.State != DriftDrifted {
		t.Error("Restrict changed the base snapshot")
	}
}

func TestDriftSurvivesSupersede(t *testing.T) {
	q, d := acceptedDrift(t, offRoot, xbucket(3), []RawRevision{xbucket(4)}, []OfferingObservation{freshObs("xbucket")})
	if q.snap.Services[xbucketKey] != nil {
		t.Fatal("the contribution was not superseded by the declared offering")
	}
	if d == nil || d.State != DriftDrifted {
		t.Fatalf("drift = %+v", d)
	}
	// A superseded source in a cluster the caller may not read still hides.
	r := q.Restrict(otherCluster, everyKind)
	unknownBecause(t, r.snap.Services[NewServiceKeyDomain(offRoot, "xbucket")].OfferingDrift, "outside your view")
}

func TestInferredOfferingAcceptedAsOtherRegistryPath(t *testing.T) {
	q, d := acceptedDrift(t, offRoot+"/offerings", xbucket(3), []RawRevision{xbucket(4)}, []OfferingObservation{freshObs("xbucket")})
	if d == nil || d.State != DriftDrifted {
		t.Fatalf("drift = %+v", d)
	}
	row := offEntities(t, q, EntityFilter{Declaration: DeclarationInferred}).Entities[0]
	if a := row.InferredOffering.AcceptedAs; a == nil || a.Key != string(NewServiceKeyDomain(offRoot+"/offerings", "xbucket")) {
		t.Fatalf("acceptedAs = %+v", a)
	}
}

func TestInferredOfferingAcceptedAs(t *testing.T) {
	q := offFleet(t, nil, []RawRevision{xbucket(2)}, []OfferingObservation{freshObs("xbucket")})
	if row := offEntities(t, q, EntityFilter{Declaration: DeclarationInferred}).Entities[0]; row.InferredOffering.AcceptedAs != nil {
		t.Fatalf("an offering nobody accepted names one: %+v", row.InferredOffering.AcceptedAs)
	}
	// Two declared offerings accepted from one source: the first key wins.
	src := xbucket(2)
	q = offFleet(t, []RawRevision{offAccepted("xbucket", "1.0.0", offRoot+"/b", fromOf(src)), offAccepted("xbucket", "1.0.0", offRoot+"/a", fromOf(src))},
		[]RawRevision{src}, []OfferingObservation{freshObs("xbucket")})
	if a := offEntities(t, q, EntityFilter{Declaration: DeclarationInferred}).Entities[0].InferredOffering.AcceptedAs; a == nil || a.Key != string(NewServiceKeyDomain(offRoot+"/a", "xbucket")) {
		t.Fatalf("acceptedAs = %+v", a)
	}
}

func TestDriftKeepsInferredAndDeclaredApart(t *testing.T) {
	q, d := acceptedDrift(t, offRoot+"/offerings", xbucket(3), []RawRevision{xbucket(4)}, []OfferingObservation{freshObs("xbucket")})
	declared, inferred := q.snap.Services[NewServiceKeyDomain(offRoot+"/offerings", "xbucket")], q.snap.Services[xbucketKey]
	if d == nil || declared.Inferred() || !inferred.Inferred() || inferred.OfferingDrift != nil || declared.inferredOffering != nil {
		t.Fatalf("declared %+v, inferred %+v", declared, inferred)
	}
	if res := offEntities(t, q, EntityFilter{}); res.Total != 1 || res.InferredTotal != 1 {
		t.Fatalf("declared list = %+v", res)
	}
}

func TestOfferingDetailProviderCarriesDrift(t *testing.T) {
	q, d := acceptedDrift(t, offRoot+"/offerings", xbucket(3), []RawRevision{xbucket(4)}, []OfferingObservation{freshObs("xbucket")})
	det, err := q.EntityDetail(KindOffering, "xbucket")
	if err != nil || len(det.Offering.Providers.Items) != 1 || !reflect.DeepEqual(det.Offering.Providers.Items[0].Drift, d) || d == nil {
		t.Fatalf("providers = %+v, %v", det, err)
	}
}

func TestOfferingDetailDriftHiddenIsUnknown(t *testing.T) {
	q, _ := acceptedDrift(t, offRoot+"/offerings", xbucket(3), []RawRevision{xbucket(4)}, []OfferingObservation{freshObs("xbucket")})
	det, err := q.Restrict(otherCluster, everyKind).EntityDetail(KindOffering, "xbucket")
	if err != nil || len(det.Offering.Providers.Items) != 1 {
		t.Fatalf("providers = %+v, %v", det, err)
	}
	if d := det.Offering.Providers.Items[0].Drift; d == nil || d.State != DriftUnknown || d.NewestRef != "" {
		t.Fatalf("hidden provider drift = %+v", d)
	}
}

func TestDriftTextIsGeneric(t *testing.T) {
	provider := regexp.MustCompile(`(?i)crossplane|xrd|composition`)
	unread, partial, stale := freshObs("xbucket"), freshObs("xbucket"), freshObs("xbucket")
	unread.Unread, partial.Partial, stale.AsOf = "x", "y", nil
	for _, tc := range []struct {
		contributed []RawRevision
		obs         []OfferingObservation
	}{
		{[]RawRevision{xbucket(4)}, []OfferingObservation{freshObs("xbucket")}},
		{nil, nil}, {nil, []OfferingObservation{unread}}, {nil, []OfferingObservation{partial}}, {nil, []OfferingObservation{stale}},
		{nil, []OfferingObservation{freshObs()}}, {nil, []OfferingObservation{freshObs("xbucket")}},
	} {
		_, d := acceptedDrift(t, offRoot+"/offerings", xbucket(3), tc.contributed, tc.obs)
		if d == nil || provider.MatchString(d.Reason) {
			t.Errorf("drift reason = %+v", d)
		}
	}
}

func TestInferredOfferingFactsFromUnreadableBundle(t *testing.T) {
	rev := offContributionOf("xbucket", 4, offRepo, offProducer, offCluster, fixedNow(), []byte("kind: ["), []byte(`{"properties":{"class":{"enum":[1]}}}`))
	noClass := offContributionOf("xqueue", 1, offRepo, offProducer, offCluster, fixedNow(), []byte("kind: ["), []byte(`{}`))
	q := offFleet(t, nil, []RawRevision{rev, noClass}, nil)
	for _, row := range offEntities(t, q, EntityFilter{Declaration: DeclarationInferred}).Entities {
		if f := row.InferredOffering; f.From != "" || f.Generation != 0 || len(f.Classes) != 0 {
			t.Fatalf("facts = %+v", f)
		}
	}
	// A head whose source names no generation drifts without a note.
	q, _ = acceptedDrift(t, offRoot+"/offerings", xbucket(3), []RawRevision{rev}, []OfferingObservation{freshObs("xbucket")})
	if d := q.snap.Services[NewServiceKeyDomain(offRoot+"/offerings", "xbucket")].OfferingDrift; d.Reason != "source object changed since accepted" {
		t.Fatalf("drift = %+v", d)
	}
}
