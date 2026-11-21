package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io/fs"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sampleRecord() *InferredRecord {
	return &InferredRecord{
		Format: InferredRecordFormat,
		Inputs: InferredInputs{
			Commit: "4e1a9c0d", Pacto: "4.6.0", Discovery: "1",
			Sources: []InferredSourceFile{
				{Path: "openapi/v1.yaml", Digest: digestOf("openapi: 3.0.0\n")},
				{Path: "deploy/app.yaml", Digest: digestOf("kind: Deployment\n")},
			},
		},
		Fields: []Inferred{
			{Field: "interfaces[api]", Value: "openapi/v1.yaml", Source: "openapi/v1.yaml", Confidence: "likely", Declare: "interfaces:\n  - name: api\n"},
			{Field: "configuration.DB_PASSWORD", Value: "string, writeOnly", Source: "deploy/app.yaml:1", Confidence: "possible"},
		},
	}
}

func TestInferredRecordRoundTrip(t *testing.T) {
	rec := sampleRecord()
	got, err := ParseInferredRecord(rec.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	// Marshal sorts, so compare against the sorted form.
	again, err := ParseInferredRecord(got.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, again) || len(got.Fields) != 2 || len(got.Inputs.Sources) != 2 {
		t.Fatalf("round trip differs: %+v vs %+v", got, again)
	}
	if got.Fields[0].Field != "configuration.DB_PASSWORD" || got.Inputs.Sources[0].Path != "deploy/app.yaml" || got.Inputs.Commit != "4e1a9c0d" {
		t.Fatalf("not sorted or lost a value: %+v", got)
	}
	if got.Fields[1].Declare != rec.Fields[0].Declare {
		t.Fatalf("declare = %q", got.Fields[1].Declare)
	}
	// Marshal never reorders the caller's record.
	if rec.Fields[0].Field != "interfaces[api]" {
		t.Fatal("Marshal sorted the receiver")
	}
}

func TestInferredRecordMarshalDeterministic(t *testing.T) {
	a := sampleRecord()
	b := sampleRecord()
	b.Fields[0], b.Fields[1] = b.Fields[1], b.Fields[0]
	b.Inputs.Sources[0], b.Inputs.Sources[1] = b.Inputs.Sources[1], b.Inputs.Sources[0]
	// Same field, two sources: sorted by source.
	extra := Inferred{Field: "dependencies[db]", Value: "db", Source: "deploy/app.yaml:9", Confidence: "likely"}
	extra2 := extra
	extra2.Source = "deploy/app.yaml:1"
	a.Fields = append(a.Fields, extra, extra2)
	b.Fields = append(b.Fields, extra2, extra)
	if string(a.Marshal()) != string(b.Marshal()) {
		t.Fatalf("different bytes:\n%s\n---\n%s", a.Marshal(), b.Marshal())
	}
	if regexp.MustCompile(`T\d\d:\d\d:`).Match(a.Marshal()) {
		t.Fatalf("record holds a timestamp:\n%s", a.Marshal())
	}
}

func TestParseInferredRecordRefuses(t *testing.T) {
	good := string(sampleRecord().Marshal())
	cases := map[string]struct{ doc, want string }{
		"unknown key":  {good + "extra: 1\n", "extra"},
		"format":       {strings.Replace(good, InferredRecordFormat, "pacto.inferred/v2", 1), "format"},
		"empty field":  {strings.Replace(good, "field: configuration.DB_PASSWORD", `field: ""`, 1), "fields[0].field"},
		"empty source": {strings.Replace(good, "source: deploy/app.yaml:1", `source: ""`, 1), "fields[0].source"},
		"confidence":   {strings.Replace(good, "confidence: possible", "confidence: observed", 1), "fields[0].confidence"},
		"digest":       {strings.Replace(good, "sha256:", "md5:", 1), "inputs.sources[0].digest"},
		"empty":        {"", "pacto.inferred.yaml"},
		"named twice":  {strings.Replace(good, "field: interfaces[api]", "field: configuration.DB_PASSWORD", 1), `fields[1].field "configuration.DB_PASSWORD" is listed twice`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseInferredRecord([]byte(tc.doc))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

func TestReadInferredRecordNilFS(t *testing.T) {
	if rec, err := ReadInferredRecord(nil); rec != nil || err != nil {
		t.Fatalf("got %v, %v", rec, err)
	}
	if rec, err := ReadInferredRecord(fstest.MapFS{}); rec != nil || err != nil {
		t.Fatalf("absent: got %v, %v", rec, err)
	}
	// A directory where the record should be is unreadable, never "no record".
	dir := fstest.MapFS{InferredRecordFile + "/x": &fstest.MapFile{}}
	if _, err := ReadInferredRecord(dir); err == nil {
		t.Fatal("want an error for an unreadable record")
	}
	ok := fstest.MapFS{InferredRecordFile: &fstest.MapFile{Data: sampleRecord().Marshal()}}
	if rec, err := ReadInferredRecord(ok); err != nil || len(rec.Fields) != 2 {
		t.Fatalf("got %v, %v", rec, err)
	}
}

func TestVerifyInferredRecord(t *testing.T) {
	bundle := fstest.MapFS{
		"openapi/v1.yaml": &fstest.MapFile{Data: []byte("openapi: 3.0.0\n")},
		"deploy/app.yaml": &fstest.MapFile{Data: []byte("kind: Deployment\n")},
	}
	if err := VerifyInferredRecord(bundle, sampleRecord()); err != nil {
		t.Fatal(err)
	}
	// A repository file outside the bundle is cited, never looked up in it.
	owners := sampleRecord()
	owners.Inputs.Sources = append(owners.Inputs.Sources, InferredSourceFile{Path: "repository:.github/CODEOWNERS", Digest: digestOf("* @acme/a\n")})
	owners.Fields = append(owners.Fields, Inferred{Field: "service.owner", Value: "acme/a", Source: "repository:.github/CODEOWNERS:1", Confidence: ConfidenceLikely, Class: InferredClassOwnerFile})
	if err := VerifyInferredRecord(bundle, owners); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		edit func(*InferredRecord)
		want string
	}{
		"self":     {func(r *InferredRecord) { r.Inputs.Sources[0].Path = InferredRecordFile }, "itself"},
		"missing":  {func(r *InferredRecord) { r.Inputs.Sources[0].Path = "gone.yaml" }, "not in the bundle"},
		"digest":   {func(r *InferredRecord) { r.Inputs.Sources[1].Digest = digestOf("other") }, "has digest"},
		"unlisted": {func(r *InferredRecord) { r.Fields[1].Source = "chart/values.yaml:3" }, "chart/values.yaml"},
		"outranks": {func(r *InferredRecord) { r.Fields[1].Outranks = []string{"Dockerfile:4"} }, "cites Dockerfile"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := sampleRecord()
			tc.edit(r)
			if err := VerifyInferredRecord(bundle, r); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSourceFile(t *testing.T) {
	for in, want := range map[string]string{
		"deploy/app.yaml:31": "deploy/app.yaml",
		"deploy/app.yaml":    "deploy/app.yaml",
		"deploy/app.yaml:":   "deploy/app.yaml:",
		"c:dir/x.yaml":       "c:dir/x.yaml",
	} {
		if got := sourceFile(in); got != want {
			t.Errorf("sourceFile(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInferredJSONUnchangedWithoutNewFields(t *testing.T) {
	got, err := json.Marshal(Inferred{Field: "service.name", Value: "orders", Source: "chart/Chart.yaml:2"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"field":"service.name","value":"orders","source":"chart/Chart.yaml:2"}`; string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestRecordClassAndFindingsRoundTrip(t *testing.T) {
	rec := sampleRecord()
	rec.Fields = append(rec.Fields, Inferred{Field: "service.owner", Value: "acme/payments", Source: "catalog-info.yaml:7", Confidence: ConfidenceLikely,
		Class: InferredClassOwnerFile, Outranks: []string{"deploy/app.yaml:4", "repository:.github/CODEOWNERS:4"}})
	rec.Inputs.ChartDependencies = []InferredChartDependency{{Chart: "chart", Name: "redis", Version: "19.0.0", Repository: "https://charts.example", Digest: digestOf("x")}}
	rec.Findings = []InferredFinding{
		{Code: "DISCOVERY_UNREADABLE", Source: "b.yaml", Message: "b"},
		{Code: "DISCOVERY_AMBIGUOUS", Source: "a.yaml", Message: "a"},
		{Code: InferredFindingTruncated, Source: ".", Message: "t"},
		{Code: "DISCOVERY_AMBIGUOUS", Source: "0.yaml", Message: "0"},
	}
	rec.FindingsDropped = 3
	got, err := ParseInferredRecord(rec.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, f := range got.Findings {
		codes = append(codes, f.Code+" "+f.Source)
	}
	if strings.Join(codes, ",") != "DISCOVERY_TRUNCATED .,DISCOVERY_AMBIGUOUS 0.yaml,DISCOVERY_AMBIGUOUS a.yaml,DISCOVERY_UNREADABLE b.yaml" {
		t.Errorf("findings order = %q", codes)
	}
	owner := got.Fields[slices.IndexFunc(got.Fields, func(f Inferred) bool { return f.Field == "service.owner" })]
	if owner.Class != InferredClassOwnerFile || len(owner.Outranks) != 2 || got.FindingsDropped != 3 || !reflect.DeepEqual(got.Inputs.ChartDependencies, rec.Inputs.ChartDependencies) {
		t.Errorf("round trip lost a value: %+v", got)
	}
}

// recordWith is the sample record with one field and one finding changed.
func recordWith(f Inferred, finding *InferredFinding, source string) []byte {
	rec := sampleRecord()
	rec.Fields = append(rec.Fields, f)
	if finding != nil {
		rec.Findings = []InferredFinding{*finding}
	}
	if source != "" {
		rec.Inputs.Sources = append(rec.Inputs.Sources, InferredSourceFile{Path: source, Digest: digestOf("x")})
	}
	return rec.Marshal()
}

func TestRecordRejectsClassOutsideOwner(t *testing.T) {
	for _, f := range []Inferred{
		{Field: "ports[8080]", Value: "8080/tcp", Source: "Dockerfile:12", Confidence: ConfidenceLikely, Class: InferredClassOwnerFile},
		{Field: "service.owner", Value: "acme", Source: "CODEOWNERS:1", Confidence: ConfidenceLikely, Class: "attested"},
	} {
		if _, err := ParseInferredRecord(recordWith(f, nil, "")); err == nil || !strings.Contains(err.Error(), ".class") {
			t.Errorf("%+v: %v", f, err)
		}
	}
}

func TestRecordRepositoryPrefixedSource(t *testing.T) {
	f := Inferred{Field: "service.owner", Value: "acme/payments", Source: "repository:.github/CODEOWNERS:4", Confidence: ConfidenceLikely, Class: InferredClassOwnerFile}
	if _, err := ParseInferredRecord(recordWith(f, nil, "repository:.github/CODEOWNERS")); err != nil {
		t.Fatal(err)
	}
}

func TestRecordRefusesEscapingSource(t *testing.T) {
	for _, bad := range []string{"../x", "/etc/x", "other:x", "repository:../x:3"} {
		f := Inferred{Field: "ports[80]", Value: "80/tcp", Source: bad, Confidence: ConfidenceLikely}
		if _, err := ParseInferredRecord(recordWith(f, nil, "")); err == nil {
			t.Errorf("field source %q accepted", bad)
		}
		f.Source, f.Outranks = "Dockerfile:2", []string{bad}
		if _, err := ParseInferredRecord(recordWith(f, nil, "")); err == nil {
			t.Errorf("outranks %q accepted", bad)
		}
		ok := Inferred{Field: "ports[80]", Value: "80/tcp", Source: "Dockerfile:2", Confidence: ConfidenceLikely}
		if _, err := ParseInferredRecord(recordWith(ok, &InferredFinding{Code: "DISCOVERY_UNREADABLE", Source: bad, Message: "m"}, "")); err == nil {
			t.Errorf("finding source %q accepted", bad)
		}
		if _, err := ParseInferredRecord(recordWith(ok, nil, strings.TrimSuffix(bad, ":3"))); err == nil {
			t.Errorf("input path %q accepted", bad)
		}
	}
	ok := Inferred{Field: "ports[80]", Value: "80/tcp", Source: "Dockerfile:2", Confidence: ConfidenceLikely}
	if _, err := ParseInferredRecord(recordWith(ok, &InferredFinding{Source: "Dockerfile", Message: "m"}, "")); err == nil {
		t.Error("a finding with no code accepted")
	}
}

// sourceFormRecord parses and verifies the sample record with one field from
// source, which inputs.sources does not list.
func sourceFormRecord(source string) error {
	bundle := fstest.MapFS{
		"openapi/v1.yaml": &fstest.MapFile{Data: []byte("openapi: 3.0.0\n")},
		"deploy/app.yaml": &fstest.MapFile{Data: []byte("kind: Deployment\n")},
	}
	rec, err := ParseInferredRecord(recordWith(Inferred{Field: "service.version", Value: "1.4.3", Source: source, Confidence: ConfidenceLikely}, nil, ""))
	if err != nil {
		return err
	}
	return VerifyInferredRecord(bundle, rec)
}

// TestVerifyInferredRecordRegistrySource: a derived version cites the
// verified baseline, pinned by digest, or the bare repository when it holds
// no version; anything else under registry: is invalid.
func TestVerifyInferredRecordRegistrySource(t *testing.T) {
	for _, ok := range []string{"registry:ghcr.io/acme/payments@" + digestOf("x"), "registry:ghcr.io/acme/payments", "registry:127.0.0.1:5000/pacto/payments"} {
		if err := sourceFormRecord(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"registry:", "registry:Acme Payments", "registry:ghcr.io/acme@sha256:abc", "registry:../x"} {
		if err := sourceFormRecord(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// TestVerifyInferredRecordFlagSource: a value a flag supplied cites the flag.
func TestVerifyInferredRecordFlagSource(t *testing.T) {
	if err := sourceFormRecord("flag:--image"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"flag:x y", "flag:image", "flag:--"} {
		if err := sourceFormRecord(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

var updateInferredGolden = flag.Bool("update", false, "rewrite testdata/inferred/*.golden.yaml")

// migratedRecord is a fields-empty record holding one migration from the
// previous supported version to the current one.
func migratedRecord() *InferredRecord {
	prev := SupportedVersions[len(SupportedVersions)-2]
	return &InferredRecord{Format: InferredRecordFormat, Inputs: InferredInputs{Commit: "4e1a9c0d", Pacto: "test", Discovery: "1"},
		Migrations: []InferredMigration{{From: prev, To: Current(), Source: contractFile, By: "pacto ci test"}}}
}

func TestInferredRecordMigrationsGolden(t *testing.T) {
	const golden = "testdata/inferred/migrated.golden.yaml"
	prev := SupportedVersions[len(SupportedVersions)-2]
	got := migratedRecord().Marshal()
	// The golden names the versions as placeholders, so it never pins a version.
	generic := strings.NewReplacer(`from: "`+prev+`"`, `from: "FROM"`, `to: "`+Current()+`"`, `to: "TO"`).Replace(string(got))
	if *updateInferredGolden {
		if err := os.WriteFile(golden, []byte(generic), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if generic != string(want) {
		t.Fatalf("got:\n%s\nwant:\n%s", generic, want)
	}
	back, err := ParseInferredRecord(got)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Migrations, migratedRecord().Migrations) {
		t.Fatalf("parsed back %+v", back.Migrations)
	}
}

func TestInferredRecordMigrationsDeterministic(t *testing.T) {
	v := SupportedVersions
	m1 := InferredMigration{From: v[len(v)-3], To: Current(), Source: "a/pacto.yaml", By: "pacto ci test"}
	m2 := InferredMigration{From: v[len(v)-2], To: Current(), Source: contractFile, By: "pacto ci test"}
	a, b := migratedRecord(), migratedRecord()
	a.Migrations = []InferredMigration{m1, m2}
	b.Migrations = []InferredMigration{m2, m1}
	if string(a.Marshal()) != string(b.Marshal()) {
		t.Fatalf("different bytes:\n%s\n---\n%s", a.Marshal(), b.Marshal())
	}
	if a.Migrations[0] != m1 {
		t.Fatal("Marshal sorted the receiver")
	}
	if regexp.MustCompile(`T\d\d:\d\d:`).Match(a.Marshal()) {
		t.Fatalf("record holds a timestamp:\n%s", a.Marshal())
	}
}

func TestParseInferredRecordRefusesMigration(t *testing.T) {
	v := SupportedVersions
	prev, cur := v[len(v)-2], Current()
	good := string(migratedRecord().Marshal())
	dup := migratedRecord()
	dup.Migrations = append(dup.Migrations, InferredMigration{From: v[len(v)-3], To: cur, Source: contractFile, By: "pacto ci test"})
	cases := map[string]struct{ doc, want string }{
		"empty from":  {strings.Replace(good, `from: "`+prev+`"`, `from: ""`, 1), "migrations[0].from"},
		"empty by":    {strings.Replace(good, "by: pacto ci test", `by: ""`, 1), "migrations[0].by"},
		"empty src":   {strings.Replace(good, "source: pacto.yaml", `source: ""`, 1), "migrations[0].source"},
		"unknown to":  {strings.Replace(good, `to: "`+cur+`"`, `to: "9.9"`, 1), "migrations[0].to"},
		"from is to":  {strings.Replace(good, `from: "`+prev+`"`, `from: "`+cur+`"`, 1), "migrations[0].from"},
		"from after":  {strings.NewReplacer(`from: "`+prev+`"`, `from: "`+cur+`"`, `to: "`+cur+`"`, `to: "`+prev+`"`).Replace(good), "migrations[0].from"},
		"duplicate":   {string(dup.Marshal()), "migrations[1].source"},
		"unknown key": {strings.Replace(good, "by: pacto ci test", "by: pacto ci test\n      extra: 1", 1), "extra"},
		"escaping":    {strings.Replace(good, "source: pacto.yaml", "source: ../pacto.yaml", 1), "migrations[0].source"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseInferredRecord([]byte(tc.doc))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

func TestInferredRecordWithoutMigrationsUnchanged(t *testing.T) {
	// The bytes a record with fields and no migrations had before migrations
	// existed.
	want := `format: pacto.inferred/v1
inputs:
    commit: 4e1a9c0d
    pacto: 4.6.0
    discovery: "1"
    sources:
        - path: deploy/app.yaml
          digest: ` + digestOf("kind: Deployment\n") + `
        - path: openapi/v1.yaml
          digest: ` + digestOf("openapi: 3.0.0\n") + `
fields:
    - field: configuration.DB_PASSWORD
      value: string, writeOnly
      source: deploy/app.yaml:1
      confidence: possible
    - field: interfaces[api]
      value: openapi/v1.yaml
      source: openapi/v1.yaml
      confidence: likely
      declare: |
        interfaces:
          - name: api
`
	if got := string(sampleRecord().Marshal()); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestVerifyInferredRecordMigrationMismatch(t *testing.T) {
	prev := SupportedVersions[len(SupportedVersions)-2]
	at := func(v string) fstest.MapFS {
		return fstest.MapFS{contractFile: &fstest.MapFile{Data: []byte("# kept\npactoVersion: \"" + v + "\"\n")}}
	}
	if err := VerifyInferredRecord(at(Current()), migratedRecord()); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		fsys fs.FS
		want string
	}{
		"missing":   {fstest.MapFS{}, "migrations[0]: pacto.yaml is not in the bundle"},
		"stale":     {at(prev), "migrations[0]: pacto.yaml declares pactoVersion " + strconv.Quote(prev)},
		"undecl":    {fstest.MapFS{contractFile: &fstest.MapFile{Data: []byte("service: {}\n")}}, `declares pactoVersion ""`},
		"unreadble": {fstest.MapFS{contractFile: &fstest.MapFile{Data: []byte("pactoVersion: [\n")}}, "migrations[0]: pacto.yaml"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := VerifyInferredRecord(tc.fsys, migratedRecord()); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

const contractFile = "pacto.yaml"

func TestInferredRecordWithMigration(t *testing.T) {
	prev, cur := SupportedVersions[len(SupportedVersions)-2], Current()
	in := InferredInputs{Pacto: "test"}
	m := InferredMigration{From: prev, To: cur, Source: "pacto.yaml", By: "pacto ci test"}
	var none *InferredRecord
	got := none.WithMigration(m, in)
	if got.Format != InferredRecordFormat || got.Inputs.Pacto != "test" || len(got.Migrations) != 1 || got.Migrations[0] != m {
		t.Fatalf("nil record: %+v", got)
	}
	other := InferredMigration{From: prev, To: cur, Source: "other.yaml", By: "pacto ci test"}
	base := &InferredRecord{Format: InferredRecordFormat, Migrations: []InferredMigration{{From: "2.3", To: cur, Source: "pacto.yaml", By: "old"}, other}}
	got = base.WithMigration(m, InferredInputs{Pacto: "ignored"})
	if len(got.Migrations) != 2 || got.Migrations[0] != other || got.Migrations[1] != m || got.Inputs.Pacto != "" || base.Migrations[0].By != "old" {
		t.Fatalf("replaced: %+v, base %+v", got, base)
	}
}

// clusterRecordYAML is the Decision 5 record, one field per cluster source
// form, in Marshal's canonical form.
const clusterRecordYAML = `format: pacto.inferred/v1
inputs:
    pacto: 4.6.0
    discovery: "3"
    sources:
        - path: openapi/v1.yaml
          digest: sha256:7b3d3a1e1f6a4d8e0b2f9c6a5e4d3c2b1a0f9e8d7c6b5a4f3e2d1c0b9a8f7e6d
    cluster:
        asOf: "2026-11-14T10:02:00Z"
        object: shop/Deployment/invoices
        callWindow: 24h
        maxItems: 10000
fields:
    - field: dependencies[ledger]
      value: ledger
      source: cluster:runtime calls from shop/Deployment/invoices
      confidence: likely
      asOf: "2026-11-14T09:58:12Z"
      completeness: complete
    - field: dependencies[ledger].compatibility
      value: compatibility unknown
      source: cluster:fleet reported versions 2.3.1
      confidence: likely
      declare: 'compatibility: "^2.3.0"'
      asOf: "2026-11-14T09:58:12Z"
      completeness: partial
      proposed: ^2.3.0
    - field: image.digest[app]
      value: sha256:aa
      source: cluster:runtime pods of shop/Deployment/invoices
      confidence: likely
      asOf: "2026-11-14T10:02:00Z"
    - field: interfaces[api]
      value: openapi/v1.yaml
      source: openapi/v1.yaml
      confidence: likely
    - field: service.owner
      value: billing
      source: cluster:label shop/Deployment/invoices app.kubernetes.io/team
      confidence: likely
      asOf: "2026-11-14T10:02:00Z"
    - field: workload
      value: service
      source: cluster:manifest shop/Deployment/invoices manifests.yaml:12
      confidence: likely
      asOf: "2026-11-14T10:02:00Z"
coverage:
    - source: workloads
      state: complete
    - source: endpointslices
      state: complete
    - source: runtime-calls
      state: partial
      reason: 2 of 3 nodes observed
hidden:
    services: 1
    callers: 2
`

// clusterDoc is clusterRecordYAML with old replaced by new once.
func clusterDoc(t *testing.T, old, new string) string {
	t.Helper()
	if !strings.Contains(clusterRecordYAML, old) {
		t.Fatalf("fixture lacks %q", old)
	}
	return strings.Replace(clusterRecordYAML, old, new, 1)
}

// refuses asserts doc fails to parse with an error naming want.
func refuses(t *testing.T, doc, want string) {
	t.Helper()
	if _, err := ParseInferredRecord([]byte(doc)); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want one naming %q", err, want)
	}
}

func TestInferredRecordClusterKeys(t *testing.T) {
	rec, err := ParseInferredRecord([]byte(clusterRecordYAML))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(rec.Marshal()); got != clusterRecordYAML {
		t.Fatalf("round trip differs:\n%s", got)
	}
	if rec.Inputs.Cluster.MaxItems != 10000 || rec.Hidden.Callers != 2 || rec.Fields[1].Proposed != "^2.3.0" {
		t.Fatalf("lost a value: %+v", rec)
	}
	// A cluster record with no bound and no proposal still parses.
	if _, err := ParseInferredRecord([]byte(clusterDoc(t, "\n        maxItems: 10000\n", "\n"))); err != nil {
		t.Fatal(err)
	}
}

func TestInferredRecordRefusesClusterWithoutInput(t *testing.T) {
	noInput := clusterDoc(t, "    cluster:\n        asOf: \"2026-11-14T10:02:00Z\"\n        object: shop/Deployment/invoices\n        callWindow: 24h\n        maxItems: 10000\n", "")
	refuses(t, noInput, "coverage and hidden need inputs.cluster")
	fieldOnly := noInput[:strings.Index(noInput, "coverage:")]
	refuses(t, fieldOnly, "fields[0].source is a cluster fact, which needs inputs.cluster")
	for name, tc := range map[string]struct{ old, new, want string }{
		"asOf":       {`asOf: "2026-11-14T10:02:00Z"` + "\n        object", `asOf: 2026-11-14` + "\n        object", "inputs.cluster.asOf"},
		"object":     {"object: shop/Deployment/invoices", "object: shop/invoices", "inputs.cluster.object"},
		"callWindow": {"callWindow: 24h", "callWindow: a day", "inputs.cluster.callWindow"},
		"maxItems":   {"maxItems: 10000", "maxItems: -1", "inputs.cluster.maxItems"},
		"hidden":     {"services: 1", "services: -1", "hidden counts a negative number"},
	} {
		t.Run(name, func(t *testing.T) { refuses(t, clusterDoc(t, tc.old, tc.new), tc.want) })
	}
}

func TestInferredRecordRefusesClusterSourceWithoutAsOf(t *testing.T) {
	doc := clusterDoc(t, "      source: cluster:runtime calls from shop/Deployment/invoices\n      confidence: likely\n      asOf: \"2026-11-14T09:58:12Z\"\n",
		"      source: cluster:runtime calls from shop/Deployment/invoices\n      confidence: likely\n")
	refuses(t, doc, "fields[0].asOf")
	for _, bad := range []string{"2026-11-14T09:58:12+01:00", "2026-11-14T09:58:12.5Z"} {
		refuses(t, clusterDoc(t, "asOf: \"2026-11-14T09:58:12Z\"\n      completeness: complete", "asOf: \""+bad+"\"\n      completeness: complete"), "fields[0].asOf")
	}
	// asOf belongs to a cluster fact only.
	refuses(t, clusterDoc(t, "      source: openapi/v1.yaml\n", "      source: openapi/v1.yaml\n      asOf: \"2026-11-14T09:58:12Z\"\n"), "fields[3].asOf is only on a cluster fact")
}

func TestInferredRecordRefusesBadClusterKind(t *testing.T) {
	refuses(t, clusterDoc(t, "cluster:runtime pods of shop/Deployment/invoices", "cluster:secret x"), "fields[2].source is not a cluster source form")
	for _, bad := range []string{
		"cluster:manifest shop/Deployment/invoices manifests.yaml:x",
		"cluster:manifest shop/Deployment/invoices ../manifests.yaml:3",
		"cluster:manifest shop/Deployment/invoices repository:manifests.yaml:3",
		"cluster:label shop/Deployment/invoices team name",
		"cluster:fleet reported versions 2.3.1; DROP",
		"cluster:fleet reported versions " + strings.Repeat("1", 230),
	} {
		refuses(t, clusterDoc(t, "cluster:manifest shop/Deployment/invoices manifests.yaml:12", bad), "fields[5].source is not a cluster source form")
	}
	// A manifest line the reader could not find leaves the bare file.
	if _, err := ParseInferredRecord([]byte(clusterDoc(t, "manifests.yaml:12", "manifests.yaml"))); err != nil {
		t.Fatal(err)
	}
}

func TestInferredRecordRefusesForeignObjectInSource(t *testing.T) {
	for _, bad := range []string{"cluster:runtime calls from vault/Deployment/kms", "cluster:runtime calls seen to vault/kms"} {
		refuses(t, clusterDoc(t, "cluster:runtime calls from shop/Deployment/invoices", bad), "naming inputs.cluster.object")
	}
}

func TestInferredRecordRefusesCompletenessOnFileSource(t *testing.T) {
	refuses(t, clusterDoc(t, "      source: openapi/v1.yaml\n", "      source: openapi/v1.yaml\n      completeness: complete\n"), "fields[3].completeness is only on a cluster runtime or fleet fact")
	refuses(t, clusterDoc(t, "app.kubernetes.io/team\n      confidence: likely\n", "app.kubernetes.io/team\n      confidence: likely\n      completeness: complete\n"), "fields[4].completeness")
	refuses(t, clusterDoc(t, "completeness: complete", "completeness: whole"), `fields[0].completeness is "whole"`)
}

func TestInferredRecordRefusesProposedOnOtherKey(t *testing.T) {
	refuses(t, clusterDoc(t, "      completeness: complete\n", "      completeness: complete\n      proposed: ^2.3.0\n"), `fields[0].proposed is on "dependencies[ledger]"`)
}

// TestInferredRecordRefusesProposedFromOtherSource: only a cluster:fleet fact
// proposes a range, so a proposal citing a folder file is forged.
func TestInferredRecordRefusesProposedFromOtherSource(t *testing.T) {
	folder := strings.Replace(clusterDoc(t, "      source: cluster:fleet reported versions 2.3.1\n", "      source: openapi/v1.yaml\n"),
		"      asOf: \"2026-11-14T09:58:12Z\"\n      completeness: partial\n", "", 1)
	refuses(t, folder, "fields[1].proposed is only on a cluster:fleet fact")
	refuses(t, clusterDoc(t, "      source: cluster:fleet reported versions 2.3.1\n", "      source: cluster:runtime calls from shop/Deployment/invoices\n"), "fields[1].proposed is only on a cluster:fleet fact")
}

func TestInferredRecordRefusesBadProposedRange(t *testing.T) {
	refuses(t, clusterDoc(t, "proposed: ^2.3.0", "proposed: ^two"), "fields[1].proposed is not a semver range")
}

func TestInferredRecordRefusesUnknownCoverageSourceOrState(t *testing.T) {
	refuses(t, clusterDoc(t, "source: endpointslices", "source: secrets"), `coverage[1].source "secrets"`)
	refuses(t, clusterDoc(t, "source: endpointslices", "source: workloads"), `coverage[1].source "workloads" is not a cluster source or is listed twice`)
	refuses(t, clusterDoc(t, "state: partial", "state: half"), `coverage[2].state is "half"`)
	// A reason is a fixed template, never apiserver text naming an object.
	refuses(t, clusterDoc(t, "reason: 2 of 3 nodes observed", "reason: pods is forbidden in namespace vault"), "coverage[2].reason is not a fixed coverage reason")
}

func TestInferredRecordRefusesReasonlessPartial(t *testing.T) {
	refuses(t, clusterDoc(t, "      reason: 2 of 3 nodes observed\n", ""), "coverage[2] is partial with no reason")
	for _, reason := range []string{"stopped at the item bound (10000)", "forbidden", "not found", "timed out", "the continue token expired",
		"listing failed (503)", "listing failed (malformed observation)", "no node runtime observations", "2 called services you may not read",
		"the pods were not all read", "no running pods observed"} {
		if _, err := ParseInferredRecord([]byte(clusterDoc(t, "2 of 3 nodes observed", reason))); err != nil {
			t.Errorf("%q: %v", reason, err)
		}
	}
}

func TestInferredRecordCoverageOrder(t *testing.T) {
	rec, err := ParseInferredRecord([]byte(clusterRecordYAML))
	if err != nil {
		t.Fatal(err)
	}
	rec.Coverage = []InferredCoverage{
		{Source: "runtime-calls", State: CoverageFailed, Reason: "no node runtime observations"},
		{Source: "pactos", State: CoverageComplete},
		{Source: "workloads", State: CoveragePartial, Reason: "stopped at the item bound (2)"},
		{Source: "pods", State: CoverageComplete},
	}
	got, err := ParseInferredRecord(rec.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, c := range got.Coverage {
		order = append(order, c.Source)
	}
	if !slices.Equal(order, []string{"workloads", "pods", "pactos", "runtime-calls"}) || rec.Coverage[0].Source != "runtime-calls" {
		t.Fatalf("order = %v, receiver %v", order, rec.Coverage)
	}
}

func TestInferredRecordZeroHiddenOmitted(t *testing.T) {
	rec, err := ParseInferredRecord([]byte(clusterDoc(t, "    services: 1\n    callers: 2\n", "    services: 0\n")))
	if err != nil || rec.Hidden != nil {
		t.Fatalf("hidden = %+v, %v", rec, err)
	}
	rec.Hidden = &InferredHidden{}
	if strings.Contains(string(rec.Marshal()), "hidden") {
		t.Fatalf("zero hidden written:\n%s", rec.Marshal())
	}
}

func TestVerifyRefusesClusterRecord(t *testing.T) {
	bundle := fstest.MapFS{"openapi/v1.yaml": &fstest.MapFile{Data: []byte("openapi: 3.0.0\n")}}
	folder := func() *InferredRecord {
		return &InferredRecord{Format: InferredRecordFormat,
			Inputs: InferredInputs{Pacto: "4.6.0", Discovery: "3", Sources: []InferredSourceFile{{Path: "openapi/v1.yaml", Digest: digestOf("openapi: 3.0.0\n")}}},
			Fields: []Inferred{{Field: "interfaces[api]", Value: "openapi/v1.yaml", Source: "openapi/v1.yaml", Confidence: ConfidenceLikely}}}
	}
	cases := map[string]struct {
		edit func(*InferredRecord)
		want string
	}{
		"field source": {func(r *InferredRecord) {
			r.Fields = append(r.Fields, Inferred{Field: "image.digest[app]", Source: "cluster:runtime pods of shop/Deployment/invoices", Confidence: ConfidenceLikely})
		}, "fields[1] (image.digest[app]): "},
		"input":    {func(r *InferredRecord) { r.Inputs.Cluster = &InferredClusterInput{Object: "shop/Deployment/invoices"} }, "inputs.cluster: "},
		"coverage": {func(r *InferredRecord) { r.Coverage = []InferredCoverage{{Source: "pods", State: CoverageComplete}} }, "coverage: "},
		"hidden":   {func(r *InferredRecord) { r.Hidden = &InferredHidden{Callers: 1} }, "hidden: "},
		"proposal from a folder file": {func(r *InferredRecord) {
			r.Fields = append(r.Fields, Inferred{Field: "dependencies[ledger].compatibility", Value: "compatibility unknown", Proposed: ">=0.0.0",
				Source: "openapi/v1.yaml", Confidence: ConfidenceLikely})
		}, "fields[1] (dependencies[ledger].compatibility): "},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := folder()
			tc.edit(r)
			if err := VerifyInferredRecord(bundle, r); err == nil || err.Error() != tc.want+clusterBundleRefusal {
				t.Fatalf("err = %v, want %q", err, tc.want+clusterBundleRefusal)
			}
		})
	}
	// The parsed Decision 5 record, as a bundle would carry it, is refused too.
	rec, err := ParseInferredRecord([]byte(clusterRecordYAML))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyInferredRecord(bundle, rec); err == nil || !strings.Contains(err.Error(), clusterBundleRefusal) {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyRefusesClusterPathInSources(t *testing.T) {
	rec := sampleRecord()
	rec.Inputs.Sources = append(rec.Inputs.Sources, InferredSourceFile{Path: "cluster:manifests.yaml", Digest: digestOf("x")})
	if err := VerifyInferredRecord(fstest.MapFS{}, rec); err == nil || err.Error() != "inputs.sources[2]: "+clusterBundleRefusal {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyAcceptsFolderOnlyRecord(t *testing.T) {
	// The Decision 5 record without its cluster input, cluster fields,
	// coverage and hidden counts is a folder record and verifies.
	rec, err := ParseInferredRecord([]byte(clusterRecordYAML))
	if err != nil {
		t.Fatal(err)
	}
	rec.Inputs.Cluster, rec.Coverage, rec.Hidden = nil, nil, nil
	rec.Fields = slices.DeleteFunc(rec.Fields, func(f Inferred) bool { return strings.HasPrefix(f.Source, clusterPrefix) })
	rec.Inputs.Sources[0].Digest = digestOf("openapi: 3.0.0\n")
	again, err := ParseInferredRecord(rec.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyInferredRecord(fstest.MapFS{"openapi/v1.yaml": &fstest.MapFile{Data: []byte("openapi: 3.0.0\n")}}, again); err != nil || len(again.Fields) != 1 {
		t.Fatalf("err = %v, fields %+v", err, again.Fields)
	}
}

func TestInferredJSONKeepsKeys(t *testing.T) {
	folder, err := json.Marshal(Inferred{Field: "service.name", Value: "orders", Source: "chart/Chart.yaml:2", Confidence: ConfidenceLikely, Declare: "service:\n  name: orders\n"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"field":"service.name","value":"orders","source":"chart/Chart.yaml:2","confidence":"likely","declare":"service:\n  name: orders\n"}`; string(folder) != want {
		t.Fatalf("got %s, want %s", folder, want)
	}
	rec, err := ParseInferredRecord([]byte(clusterRecordYAML))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"cluster", "asOf", "object", "callWindow", "maxItems", "coverage", "source", "state", "reason", "hidden", "services", "callers", "completeness", "proposed"} {
		if !strings.Contains(string(data), `"`+key+`":`) {
			t.Errorf("JSON lacks %q: %s", key, data)
		}
	}
	for _, goName := range []string{"AsOf", "CallWindow", "MaxItems", "Coverage", "Hidden", "Completeness", "Proposed", "Services", "Callers"} {
		if strings.Contains(string(data), `"`+goName+`"`) {
			t.Errorf("JSON carries Go name %q: %s", goName, data)
		}
	}
	zero, err := ParseInferredRecord([]byte(clusterDoc(t, "    services: 1\n    callers: 2\n", "    callers: 0\n")))
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := json.Marshal(zero); strings.Contains(string(data), `"hidden"`) {
		t.Fatalf("zero hidden in JSON: %s", data)
	}
}

// declarationYAML is the Decision 1 block of a declared record that accepted
// a draft, in Marshal's layout.
const declarationYAML = `declaration:
    state: declared
    asker:
        subject: ana@acme.com
        issuer: https://accounts.google.com
        class: human
    inferredFrom: k8s:shop/Deployment/invoices
    cluster: 3f1c0d2e-9a8b-4c7d-8e6f-5a4b3c2d1e0f
    baseline:
        version: 1.4.0
        bundle: ghcr.io/acme/invoices@sha256:1111111111111111111111111111111111111111111111111111111111111111
        deploy: ghcr.io/acme/invoices-deploy@sha256:2222222222222222222222222222222222222222222222222222222222222222
    accepted:
        - field: dependencies[ledger].compatibility
          range: ^2.3.0
    draft:
        tag: draft-3
        digest: ghcr.io/acme/invoices@sha256:3333333333333333333333333333333333333333333333333333333333333333
        asker:
            subject: ""
            issuer: none
            class: agent
        onBehalfOf: ana
`

// draftDeclarationYAML is an unsigned agent draft's block.
const draftDeclarationYAML = `declaration:
    state: draft
    asker:
        subject: ""
        issuer: none
        class: agent
    onBehalfOf: ana
    inferredFrom: k8s:shop/Deployment/invoices
    requested:
        - field: dependencies[ledger].compatibility
          range: ^2.3.0
`

// declaredDoc is clusterRecordYAML with block before its coverage.
func declaredDoc(block string) string {
	return strings.Replace(clusterRecordYAML, "coverage:\n", block+"coverage:\n", 1)
}

// declDoc is declaredDoc(declarationYAML) with old replaced by new once.
func declDoc(t *testing.T, block, old, new string) string {
	t.Helper()
	if !strings.Contains(block, old) {
		t.Fatalf("fixture lacks %q", old)
	}
	return declaredDoc(strings.Replace(block, old, new, 1))
}

func TestDeclarationRoundTrip(t *testing.T) {
	for _, block := range []string{declarationYAML, draftDeclarationYAML} {
		doc := declaredDoc(block)
		rec, err := ParseInferredRecord([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		if got := string(rec.Marshal()); got != doc {
			t.Fatalf("round trip:\n%s\nwant:\n%s", got, doc)
		}
	}
}

func TestDeclarationMarshalSortsRanges(t *testing.T) {
	rec, err := ParseInferredRecord([]byte(declaredDoc(declarationYAML)))
	if err != nil {
		t.Fatal(err)
	}
	rec.Declaration.Accepted = []InferredAcceptance{{Field: "dependencies[z].compatibility", Range: "^1.0.0"}, {Field: "dependencies[a].compatibility", Range: "^1.0.0"}}
	got, err := ParseInferredRecord(rec.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if got.Declaration.Accepted[0].Field != "dependencies[a].compatibility" || rec.Declaration.Accepted[0].Field != "dependencies[z].compatibility" {
		t.Fatalf("accepted = %v, receiver %v", got.Declaration.Accepted, rec.Declaration.Accepted)
	}
}

func TestDeclarationJSONKeys(t *testing.T) {
	rec, err := ParseInferredRecord([]byte(declaredDoc(declarationYAML)))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(rec.Declaration)
	for _, key := range []string{"state", "asker", "subject", "issuer", "class", "inferredFrom", "cluster", "baseline", "version", "bundle", "deploy", "accepted", "field", "range", "draft", "tag", "digest", "onBehalfOf"} {
		if !strings.Contains(string(data), `"`+key+`":`) {
			t.Errorf("JSON lacks %q: %s", key, data)
		}
	}
	if regexp.MustCompile(`"[A-Z][A-Za-z]*":`).Match(data) {
		t.Errorf("JSON carries a Go name: %s", data)
	}
	data, _ = json.Marshal(InferredDeclaration{State: DeclarationDraft, Asker: InferredSigner{Issuer: SignerUnsigned, Class: SignerClassAgent}})
	if want := `{"state":"draft","asker":{"subject":"","issuer":"none","class":"agent"}}`; string(data) != want {
		t.Fatalf("empty members written: %s, want %s", data, want)
	}
}

func TestParseRefusesDeclaredByNonHuman(t *testing.T) {
	refuses(t, declDoc(t, declarationYAML, "class: human", "class: ci"), "a declared record is signed by a person")
	refuses(t, declDoc(t, declarationYAML, "subject: ana@acme.com", `subject: ""`), "a declared record is signed by a person")
	refuses(t, declDoc(t, declarationYAML, "class: human", "class: robot"), `declaration.asker.class is "robot"`)
	refuses(t, declDoc(t, declarationYAML, "issuer: https://accounts.google.com", `issuer: ""`), "declaration.asker.issuer is empty")
}

func TestParseRefusesDraftByHuman(t *testing.T) {
	signed := strings.Replace(draftDeclarationYAML, "        subject: \"\"\n        issuer: none\n", "        subject: ana@acme.com\n        issuer: https://accounts.google.com\n", 1)
	refuses(t, declDoc(t, signed, "class: agent", "class: human"), "a draft is never signed by a person")
	refuses(t, declDoc(t, declarationYAML, "            class: agent", "            class: human"), "declaration.draft.asker: a draft is never signed by a person")
	refuses(t, declDoc(t, declarationYAML, "            class: agent", "            class: robot"), `declaration.draft.asker.class is "robot"`)
}

func TestParseAcceptsUnsignedDraft(t *testing.T) {
	rec, err := ParseInferredRecord([]byte(declaredDoc(draftDeclarationYAML)))
	if err != nil {
		t.Fatal(err)
	}
	if d := rec.Declaration; d.State != DeclarationDraft || d.Asker != (InferredSigner{Issuer: SignerUnsigned, Class: SignerClassAgent}) || d.OnBehalfOf != "ana" {
		t.Fatalf("declaration = %+v", d)
	}
	// A draft signed by a CI token names it.
	ci := strings.Replace(draftDeclarationYAML, "        subject: \"\"\n        issuer: none\n        class: agent\n",
		"        subject: https://github.com/acme/invoices/.github/workflows/ci.yml@refs/heads/main\n        issuer: https://token.actions.githubusercontent.com\n        class: ci\n", 1)
	if _, err := ParseInferredRecord([]byte(declaredDoc(ci))); err != nil {
		t.Fatal(err)
	}
}

func TestParseRefusesUnsignedDraftWithAsker(t *testing.T) {
	refuses(t, declDoc(t, draftDeclarationYAML, `subject: ""`, "subject: ana@acme.com"), "of an unsigned draft is exactly")
	refuses(t, declDoc(t, draftDeclarationYAML, "class: agent", "class: ci"), "of an unsigned draft is exactly")
	refuses(t, declDoc(t, declarationYAML, `            subject: ""`, "            subject: bot"), "declaration.draft.asker of an unsigned draft is exactly")
}

func TestParseRefusesOnBehalfOfOnDeclared(t *testing.T) {
	refuses(t, declDoc(t, declarationYAML, "    inferredFrom:", "    onBehalfOf: ana\n    inferredFrom:"), "declaration.onBehalfOf is only on a draft")
}

func TestParseRefusesAcceptedOnDraft(t *testing.T) {
	refuses(t, declDoc(t, draftDeclarationYAML, "    requested:", "    accepted:"), "declaration.accepted is only on a declared record")
	refuses(t, declDoc(t, draftDeclarationYAML, "    requested:", "    draft:\n        tag: draft-1\n        digest: r@sha256:"+strings.Repeat("4", 64)+
		"\n        asker:\n            subject: \"\"\n            issuer: none\n            class: agent\n    requested:"), "declaration.draft is only on a declared record")
}

func TestParseRefusesRequestedOnDeclared(t *testing.T) {
	refuses(t, declDoc(t, declarationYAML, "    accepted:", "    requested:"), "declaration.requested is only on a draft")
}

func TestParseRefusesAcceptedOtherKey(t *testing.T) {
	for _, field := range []string{"dependencies[ledger]", "dependencies[ledger].version", "service.owner", "dependencies[].compatibility"} {
		refuses(t, declDoc(t, declarationYAML, "field: dependencies[ledger].compatibility", "field: "+field), "declaration.accepted[0].field")
	}
	twice := "        - field: dependencies[ledger].compatibility\n          range: ^2.3.0\n"
	refuses(t, declDoc(t, declarationYAML, twice, twice+twice), "declaration.accepted[1].field \"dependencies[ledger].compatibility\" is listed twice")
	refuses(t, declDoc(t, draftDeclarationYAML, "field: dependencies[ledger].compatibility", "field: x"), "declaration.requested[0].field")
}

func TestParseRefusesBadRange(t *testing.T) {
	refuses(t, declDoc(t, declarationYAML, "range: ^2.3.0", "range: ^two"), "declaration.accepted[0].range is not a semver range")
	refuses(t, declDoc(t, draftDeclarationYAML, "range: ^2.3.0", "range: ^two"), "declaration.requested[0].range is not a semver range")
}

func TestParseRefusesBadInferredFrom(t *testing.T) {
	// The escaped key form and a name without "k8s:" never reach the fleet.
	for _, bad := range []string{"k8s:a/b", "k8s:a/b/c/d/e", "svc:x", "k8s:a//c", "k8s:billing%2FDeployment%2Finvoices", "billing/Deployment/invoices"} {
		refuses(t, declDoc(t, declarationYAML, "inferredFrom: k8s:shop/Deployment/invoices", "inferredFrom: "+bad), "declaration.inferredFrom is not")
	}
	refuses(t, declDoc(t, declarationYAML, "cluster: 3f1c0d2e-9a8b-4c7d-8e6f-5a4b3c2d1e0f", "cluster: "+strings.Repeat("a", 65)), "declaration.cluster is longer than 64 bytes")
}

func TestParseRefusesBadBaselineDigest(t *testing.T) {
	for _, tc := range []struct{ old, new, want string }{
		{"bundle: ghcr.io/acme/invoices@", "bundle: ghcr.io/acme/invoices:", "declaration.baseline.bundle and .deploy"},
		{"deploy: ghcr.io/acme/invoices-deploy@sha256:2", "deploy: ghcr.io/acme/invoices-deploy@sha256:x", "declaration.baseline.bundle and .deploy"},
		{"version: 1.4.0", "version: v1", `declaration.baseline.version "v1" is not a version`},
		{"digest: ghcr.io/acme/invoices@sha256", "digest: sha256", "declaration.draft.digest"},
		{"tag: draft-3", "tag: 1.0.0", `declaration.draft.tag is "1.0.0"`},
		{"state: declared", "state: accepted", `declaration.state is "accepted"`},
	} {
		refuses(t, declDoc(t, declarationYAML, tc.old, tc.new), tc.want)
	}
	// A baseline with no deploy artifact names none.
	if _, err := ParseInferredRecord([]byte(declDoc(t, declarationYAML, "        deploy: ghcr.io/acme/invoices-deploy@sha256:"+strings.Repeat("2", 64)+"\n", ""))); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyAcceptsClusterFactsBesideDeclaration(t *testing.T) {
	bundle := fstest.MapFS{"openapi/v1.yaml": &fstest.MapFile{Data: []byte("openapi: 3.0.0\n")}}
	rec, err := ParseInferredRecord([]byte(declaredDoc(declarationYAML)))
	if err != nil {
		t.Fatal(err)
	}
	rec.Inputs.Sources[0].Digest = digestOf("openapi: 3.0.0\n")
	if err := VerifyInferredRecord(bundle, rec); err != nil {
		t.Fatal(err)
	}
	// A file source must still be in the bundle.
	rec.Inputs.Sources = nil
	if err := VerifyInferredRecord(bundle, rec); err == nil || !strings.Contains(err.Error(), "cites openapi/v1.yaml") {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyStillRefusesClusterFactsWithoutDeclaration(t *testing.T) {
	rec, err := ParseInferredRecord([]byte(declaredDoc(declarationYAML)))
	if err != nil {
		t.Fatal(err)
	}
	rec.Declaration = nil
	rec.Inputs.Sources[0].Digest = digestOf("openapi: 3.0.0\n")
	if err := VerifyInferredRecord(fstest.MapFS{"openapi/v1.yaml": &fstest.MapFile{Data: []byte("openapi: 3.0.0\n")}}, rec); err == nil || err.Error() != "inputs.cluster: "+clusterBundleRefusal {
		t.Fatalf("err = %v", err)
	}
}
