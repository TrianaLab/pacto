package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// orgDigest is an organization catalog no test snapshot classifies with.
const orgDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000001"

// repoSvc is one Kubernetes service of repoFleet: its name, version, the
// namespace it runs in (none: no target) and its bundle files, the inferred
// record included.
type repoSvc struct {
	name, version, ns string
	fsys              fstest.MapFS
}

// repoFleet builds a Kubernetes fleet of svcs at pactoVersion 2.5, one
// revision per entry.
func repoFleet(t *testing.T, svcs ...repoSvc) *FleetSnapshot {
	t.Helper()
	col := &Collection{}
	for _, s := range svcs {
		c := extContract(s.name, s.version)
		c.PactoVersion = "2.5"
		digest := "sha256:" + s.name + "-" + s.version
		col.Revisions = append(col.Revisions, RawRevision{Bundle: &contract.Bundle{Contract: c, FS: s.fsys}, Digest: digest})
		if s.ns != "" {
			col.Targets = append(col.Targets, RawTarget{Scope: s.ns, Kind: SourceKindKubernetes, Name: s.name, Service: s.name, Digest: digest, Compliance: StatusCompliant})
		}
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("k8s", SourceKindKubernetes, col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// repoRecord is a bundle holding a valid inferred record of fields,
// classified with cats ("<origin> <digest>"), and every file a field cites,
// each holding files[path] when set.
func repoRecord(cats []string, files map[string]string, fields ...contract.Inferred) fstest.MapFS {
	fsys := fstest.MapFS{}
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Fields: fields,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1", Catalog: cats}}
	for _, f := range fields {
		paths := []string{f.Source[:strings.LastIndexByte(f.Source+":", ':')]}
		for _, s := range f.External.Sources {
			paths = append(paths, s.Path)
		}
		for _, p := range paths {
			if fsys[p] != nil {
				continue
			}
			data := cmpOr(files[p], "data "+p)
			fsys[p] = &fstest.MapFile{Data: []byte(data)}
			sum := sha256.Sum256([]byte(data))
			rec.Inputs.Sources = append(rec.Inputs.Sources, contract.InferredSourceFile{Path: p, Digest: "sha256:" + hex.EncodeToString(sum[:])})
		}
	}
	fsys[contract.InferredRecordFile] = &fstest.MapFile{Data: rec.Marshal()}
	return fsys
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// extField is the record field of an external dependency name with
// identity, classified by origin (none: unclassified), at confidence, read
// from srcs.
func extField(name, identity, origin, confidence string, srcs ...contract.InferredExternalSource) contract.Inferred {
	return contract.Inferred{Field: "dependencies[" + name + "]", Value: identity, Source: srcs[0].Path, Confidence: confidence,
		External: &contract.InferredExternal{Identity: identity, Catalog: origin, Sources: srcs}}
}

// defaultCats is the record catalog input of the snapshot's default catalog.
func defaultCats() []string { return []string{"default " + externalcatalog.DefaultDigest()} }

// stripeHelm and stripeSDK name stripe through a Helm values host and a
// Python requirement.
var (
	stripeHelm = contract.InferredExternalSource{Kind: "helmValues", Path: "chart/values.yaml", Line: 3, Key: "STRIPE_API_BASE", Host: "api.stripe.com"}
	stripeSDK  = contract.InferredExternalSource{Kind: "sdk", Path: "requirements.txt"}
)

func stripeRecord() fstest.MapFS {
	return repoRecord(defaultCats(), nil, extField("stripe", "stripe", "default", "likely", stripeHelm, stripeSDK))
}

func TestRepositoryExternalsBecomeInferredEdges(t *testing.T) {
	fsys := repoRecord(defaultCats(), nil,
		extField("stripe", "stripe", "default", "likely", stripeHelm, stripeSDK),
		extField("legacy", "host/legacy.example.com", "", "likely",
			contract.InferredExternalSource{Kind: "env", Path: "deploy/app.yaml", Line: 9, Key: "LEGACY_URL", Host: "billing.legacy.example.com:8443"}))
	snap := repoFleet(t, repoSvc{"checkout", "1.0.0", "shop", fsys})
	rk := snap.Services["checkout"].Revisions[0]
	rels := relsTo(snap, "stripe")
	if len(rels) != 1 || rels[0].Provenance != ProvenanceInferred || rels[0].Confidence != "likely" || rels[0].FromRevision != "" {
		t.Fatalf("edges = %+v", rels)
	}
	cat := externalcatalog.DefaultDigest()
	want := []InferredSource{
		{Source: "k8s", Kind: "helmValues", Where: "chart/values.yaml:3 STRIPE_API_BASE", Host: "api.stripe.com", Confidence: "likely", Entry: "stripe", Catalog: cat, Revision: rk},
		{Source: "k8s", Kind: "sdk", Where: "requirements.txt", Confidence: "possible", Entry: "stripe", Catalog: cat, Revision: rk},
	}
	if !reflect.DeepEqual(rels[0].InferredSources, want) {
		t.Errorf("sources = %+v\nwant %+v", rels[0].InferredSources, want)
	}
	checkRepositorySources(t, snap, rk)
}

// checkRepositorySources: TestRepositoryExternalsBecomeInferredEdges'
// stripe record sources.
func checkRepositorySources(t *testing.T, snap *FleetSnapshot, rk RevisionKey) {
	t.Helper()
	rec := snap.Externals["stripe"]
	wantSrc := []ExternalSource{
		{Kind: ExternalSourceRepository, Service: "checkout", Revision: rk, Provenance: ProvenanceInferred, Match: externalcatalog.KindHost},
		{Kind: ExternalSourceRepository, Service: "checkout", Revision: rk, Provenance: ProvenanceInferred, Match: externalcatalog.KindSDK},
	}
	if rec == nil || rec.Class != externalcatalog.ClassVendorAPI || !reflect.DeepEqual(rec.Sources, wantSrc) {
		t.Fatalf("record = %+v", rec)
	}
	checkRepositoryRecord(t, snap, rk)
}

// checkRepositoryRecord: TestRepositoryExternalsBecomeInferredEdges'
// unclassified host and explain answer, with no catalog limitation.
func checkRepositoryRecord(t *testing.T, snap *FleetSnapshot, rk RevisionKey) {
	t.Helper()
	legacy := relsTo(snap, "host/legacy.example.com")
	if len(legacy) != 1 || legacy[0].InferredSources[0].Catalog != "" || legacy[0].InferredSources[0].Entry != "" ||
		legacy[0].InferredSources[0].Where != "deploy/app.yaml:9 LEGACY_URL" || snap.Externals["host/legacy.example.com"].Class != externalcatalog.ClassUnclassified {
		t.Errorf("unclassified host = %+v", legacy)
	}
	res, err := NewQuery(snap).Explain("external:stripe")
	if err != nil || !slices.ContainsFunc(res.Reasons, func(r Reason) bool {
		return r.Code == ReasonExternalInferred && strings.Contains(r.Message, "revision "+string(rk))
	}) {
		t.Errorf("explain = %+v, %v", res, err)
	}
	for _, l := range snap.Limitations {
		if strings.HasPrefix(l.Code, "EXTERNAL_") {
			t.Errorf("limitation %+v", l)
		}
	}
}

// TestRecordExternalNotProjectedAsServiceEdge: a fleet that also runs a
// service called stripe never resolves the record's external to it.
func TestRecordExternalNotProjectedAsServiceEdge(t *testing.T) {
	snap := repoFleet(t, repoSvc{"checkout", "1.0.0", "shop", stripeRecord()}, repoSvc{"stripe", "1.0.0", "shop", fstest.MapFS{}})
	ext := 0
	for _, r := range snap.Relationships {
		switch {
		case r.ToExternal == "stripe":
			ext++
		case r.FromService == "checkout":
			t.Errorf("service edge %+v", r)
		}
	}
	if ext != 1 {
		t.Errorf("external edges = %d", ext)
	}
	for _, l := range snap.Limitations {
		if l.Code == LimitationUnresolvedDep {
			t.Errorf("limitation %+v", l)
		}
	}
}

func TestRepositoryExternalsOnlyNewestRevision(t *testing.T) {
	twilio := repoRecord(defaultCats(), nil, extField("twilio", "twilio", "default", "likely",
		contract.InferredExternalSource{Kind: "env", Path: "deploy/app.yaml", Key: "TWILIO_URL", Host: "api.twilio.com"}))
	snap := repoFleet(t, repoSvc{"checkout", "1.0.0", "shop", stripeRecord()}, repoSvc{"checkout", "1.1.0", "", twilio})
	if len(relsTo(snap, "stripe")) != 0 || snap.Externals["stripe"] != nil || len(relsTo(snap, "twilio")) != 1 {
		t.Errorf("externals = %+v", snap.Externals)
	}
}

// mismatches is the EXTERNAL_CATALOG_MISMATCH messages of snap.
func mismatches(snap *FleetSnapshot) []string {
	var out []string
	for _, l := range snap.Limitations {
		if l.Code == LimitationExternalCatalogMismatch {
			out = append(out, l.Message)
		}
	}
	return out
}

func TestRepositoryExternalCatalogMismatch(t *testing.T) {
	other := "sha256:" + strings.Repeat("ab", 32)
	fsys := repoRecord([]string{"default " + other}, nil, extField("stripe", "stripe", "default", "likely", stripeHelm))
	snap := repoFleet(t, repoSvc{"checkout", "1.0.0", "shop", fsys})
	if m := mismatches(snap); len(m) != 1 || !strings.Contains(m[0], other) {
		t.Errorf("mismatch = %v", m)
	}
	if c := relsTo(snap, "stripe")[0].InferredSources[0].Catalog; c != other {
		t.Errorf("catalog = %s", c)
	}
}

// TestRepositoryExternalOrgCatalogMismatch: an external the organization
// catalog classified names that catalog's digest, which the snapshot lacks;
// a default-classified external of the same record is flagged too, since
// the record classified with a catalog the snapshot does not hold.
func TestRepositoryExternalOrgCatalogMismatch(t *testing.T) {
	fsys := repoRecord(append(defaultCats(), "organization "+orgDigest), nil,
		extField("acme-mq", "acme-mq", "organization", "likely",
			contract.InferredExternalSource{Kind: "env", Path: "deploy/app.yaml", Key: "MQ_URL", Host: "mq.acme.example"}),
		extField("stripe", "stripe", "default", "likely", stripeHelm))
	snap := repoFleet(t, repoSvc{"checkout", "1.0.0", "shop", fsys})
	if m := mismatches(snap); len(m) != 1 || !strings.Contains(m[0], orgDigest) {
		t.Errorf("mismatch = %v", m)
	}
	src := relsTo(snap, "acme-mq")[0].InferredSources[0]
	if src.Catalog != orgDigest || src.Entry != "" {
		t.Errorf("source = %+v", src)
	}
	if c := relsTo(snap, "stripe")[0].InferredSources[0].Catalog; c != externalcatalog.DefaultDigest() {
		t.Errorf("stripe catalog = %s", c)
	}
}

func TestRepositoryExternalsStayOutOfServiceIndexes(t *testing.T) {
	snap := repoFleet(t, repoSvc{"checkout", "1.0.0", "shop", stripeRecord()}, repoSvc{"stripe", "1.0.0", "shop", fstest.MapFS{}})
	checkIndexesEmpty(t, snap)
	for _, r := range snap.Relationships {
		if ConsumesEdge(r) {
			t.Errorf("consumes edge %+v", r)
		}
	}
	g, err := NewQuery(snap).Graph(GraphQuery{Service: "stripe", Direction: DirectionDependents})
	if err != nil || len(g.Edges) != 0 {
		t.Errorf("dependents of the stripe service = %+v, %v", g, err)
	}
}

// TestRepositoryPossibleSDKOnlyStaysPossible: a record that claims likely
// for an external only a client library names reads possible.
func TestRepositoryPossibleSDKOnlyStaysPossible(t *testing.T) {
	fsys := repoRecord(defaultCats(), nil, extField("stripe", "stripe", "default", "likely", stripeSDK))
	r := relsTo(repoFleet(t, repoSvc{"checkout", "1.0.0", "shop", fsys}), "stripe")
	if len(r) != 1 || r[0].Confidence != "possible" || r[0].InferredSources[0].Confidence != "possible" {
		t.Errorf("edge = %+v", r)
	}
}

func TestManagedFiveSegmentGetsCatalogClass(t *testing.T) {
	id := "aws/sqs/123456789012/eu-west-1/orders"
	fsys := repoRecord(defaultCats(), nil, extField("orders", id, "default", "likely",
		contract.InferredExternalSource{Kind: "terraform", Path: "infra/main.tf", Line: 4, Key: "aws_sqs_queue.orders"},
		contract.InferredExternalSource{Kind: "crossplane", Path: "k8s/queue.yaml", Line: 1, Key: "orders"}))
	snap := repoFleet(t, repoSvc{"checkout", "1.0.0", "shop", fsys})
	rec := snap.Externals[ExternalKey(id)]
	if rec == nil || rec.Class != externalcatalog.ClassManagedCloud || rec.Catalog == nil || rec.Catalog.Entry != "aws/sqs" || rec.Label != id {
		t.Fatalf("record = %+v", rec)
	}
	srcs := relsTo(snap, ExternalKey(id))[0].InferredSources
	if len(srcs) != 2 || srcs[0].Kind != "crossplane" || srcs[0].Confidence != "likely" || srcs[1].Confidence != "possible" || srcs[1].Entry != "aws/sqs" {
		t.Errorf("sources = %+v", srcs)
	}
	if rec.Sources[0].Match != externalcatalog.KindResource {
		t.Errorf("match = %+v", rec.Sources)
	}
}

func TestExternalSourceKindEnum(t *testing.T) {
	f, _ := reflect.TypeFor[ExternalSource]().FieldByName("Kind")
	want := strings.Join([]string{ExternalSourceContract, ExternalSourceObserved, ExternalSourcePodSpec, ExternalSourceRepository}, ",")
	if got := f.Tag.Get("enum"); got != want {
		t.Errorf("enum = %q, want %q", got, want)
	}
	r, _ := reflect.TypeFor[InferredSource]().FieldByName("Revision")
	if r.Tag.Get("json") != "revision,omitempty" {
		t.Errorf("revision tag = %q", r.Tag)
	}
}

// secretMarkers are what TestFolderExternalsNeverLeakFleet's files hold
// beside the hosts the record keeps.
var secretMarkers = []string{"admin", "hunter2", "s3cr3t", "/app", "tf-hunter2", "pw-hunter2", "npm-token", "state-hunter2"}

// TestFolderExternalsNeverLeakFleet: the bundle files hold credentials and
// the record holds hosts only, and a forged record carrying userinfo in a
// host is refused: no answer echoes a secret.
func TestFolderExternalsNeverLeakFleet(t *testing.T) {
	rds := func(kind, path, key string, line int) contract.InferredExternalSource {
		return contract.InferredExternalSource{Kind: kind, Path: path, Line: line, Key: key, Host: "db.xyz.rds.amazonaws.com:5432"}
	}
	files := map[string]string{
		"chart/values-production.yaml": "DATABASE_URL: postgres://admin:hunter2@db.xyz.rds.amazonaws.com:5432/app?password=s3cr3t\n",
		"compose.yaml":                 "services:\n  app:\n    environment:\n      - postgres://admin:pw-hunter2@host\n      - DATABASE_URL=postgres://admin:hunter2@db.xyz.rds.amazonaws.com:5432/app\n",
		"infra/main.tf":                "resource \"aws_db_instance\" \"main\" {\n  identifier = \"orders\"\n  password = \"tf-hunter2\"\n}\n",
		"package-lock.json":            `{"packages":{"node_modules/stripe":{"version":"14.0.0","resolved":"https://npm-user:npm-token@registry.example.com/stripe.tgz"}}}`,
		"infra/terraform.tfstate":      `{"password": "state-hunter2"}`,
	}
	good := repoRecord(defaultCats(), files,
		extField("rds", "aws/rds", "default", "likely", rds("helmValues", "chart/values-production.yaml", "DATABASE_URL", 1),
			rds("compose", "compose.yaml", "DATABASE_URL", 5),
			contract.InferredExternalSource{Kind: "terraform", Path: "infra/main.tf", Line: 1, Key: "aws_db_instance.main"}),
		extField("stripe", "stripe", "default", "possible", contract.InferredExternalSource{Kind: "sdk", Path: "package-lock.json"}))
	good["infra/terraform.tfstate"] = &fstest.MapFile{Data: []byte(files["infra/terraform.tfstate"])}
	forged := stripeRecord()
	forged[contract.InferredRecordFile] = &fstest.MapFile{Data: []byte(strings.Replace(string(forged[contract.InferredRecordFile].Data),
		"host: api.stripe.com", "host: admin:pw-hunter2@db.example.com", 1))}
	snap := repoFleet(t, repoSvc{"checkout", "1.0.0", "shop", good}, repoSvc{"billing", "1.0.0", "shop", forged})
	if !slices.ContainsFunc(snap.Limitations, func(l Limitation) bool { return l.Code == LimitationInferredRecordUnreadable }) {
		t.Fatalf("the forged record was read: %+v", snap.Limitations)
	}
	q := NewQuery(snap)
	v, err := q.GetExternal("aws/rds")
	if err != nil {
		t.Fatal(err)
	}
	ex, err := q.Explain("external:aws/rds")
	if err != nil {
		t.Fatal(err)
	}
	for name, x := range map[string]any{"snapshot": snap, "get": v, "explain": ex} {
		b, _ := json.Marshal(x)
		for _, m := range secretMarkers {
			if strings.Contains(string(b), m) {
				t.Errorf("%s JSON holds %q", name, m)
			}
		}
	}
}

// TestRecordExternalUsesDropsForgedSources: a record that bypassed the
// decoder (a host with userinfo, a key that is a URL, a malformed catalog
// input) gives no use and one limitation that quotes none of it; a golden
// path's record and a revision the snapshot lost give none.
func TestRecordExternalUsesDropsForgedSources(t *testing.T) {
	snap := repoFleet(t, repoSvc{"checkout", "1.0.0", "shop", fstest.MapFS{}}, repoSvc{"template", "1.0.0", "", stripeRecord()})
	snap.Services["template"].Template = true
	svc := snap.Services["checkout"]
	rev := snap.Revisions[svc.Revisions[0]]
	svc.Revisions = append(svc.Revisions, "checkout@sha256:gone")
	rev.Record = &contract.InferredRecord{Inputs: contract.InferredInputs{Catalog: []string{"garbage"}}, Fields: []contract.Inferred{
		{Field: "service.owner"},
		extField("db", "aws/rds", "default", "likely",
			contract.InferredExternalSource{Kind: "env", Path: "a.yaml", Key: "DB", Host: "admin:pw-hunter2@db.example.com"},
			contract.InferredExternalSource{Kind: "env", Path: "a.yaml", Key: "postgres://admin:hunter2@x"}),
	}}
	before := len(snap.Limitations)
	if uses := recordExternalUses(snap); len(uses) != 0 {
		t.Errorf("uses = %+v", uses)
	}
	lims := snap.Limitations[before:]
	if len(lims) != 1 || lims[0].Code != LimitationExternalUseUnattributed || !strings.Contains(lims[0].Message, "names 2 external sources") {
		t.Fatalf("limitations = %+v", lims)
	}
	for _, m := range secretMarkers {
		if strings.Contains(lims[0].Message, m) {
			t.Errorf("limitation holds %q", m)
		}
	}
}
