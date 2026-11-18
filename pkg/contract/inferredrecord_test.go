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
