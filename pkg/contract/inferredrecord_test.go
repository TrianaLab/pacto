package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"regexp"
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
	cases := map[string]struct {
		edit func(*InferredRecord)
		want string
	}{
		"self":     {func(r *InferredRecord) { r.Inputs.Sources[0].Path = InferredRecordFile }, "itself"},
		"missing":  {func(r *InferredRecord) { r.Inputs.Sources[0].Path = "gone.yaml" }, "not in the bundle"},
		"digest":   {func(r *InferredRecord) { r.Inputs.Sources[1].Digest = digestOf("other") }, "has digest"},
		"unlisted": {func(r *InferredRecord) { r.Fields[1].Source = "chart/values.yaml:3" }, "chart/values.yaml"},
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
