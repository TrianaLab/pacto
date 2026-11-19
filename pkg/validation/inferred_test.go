package validation_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

const deployYAML = "kind: Deployment\n"

func deployDigest() string {
	sum := sha256.Sum256([]byte(deployYAML))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func recordBundle(rec *contract.InferredRecord) fstest.MapFS {
	return fstest.MapFS{
		"deploy/app.yaml":           &fstest.MapFile{Data: []byte(deployYAML)},
		contract.InferredRecordFile: &fstest.MapFile{Data: rec.Marshal()},
	}
}

func goodRecord() *contract.InferredRecord {
	return &contract.InferredRecord{
		Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1",
			Sources: []contract.InferredSourceFile{{Path: "deploy/app.yaml", Digest: deployDigest()}}},
		Fields: []contract.Inferred{{Field: "workload", Value: "service", Source: "deploy/app.yaml:1", Confidence: "likely"}},
	}
}

func codesAt(r validation.ValidationResult, path string) []string {
	var out []string
	for _, e := range r.Errors {
		if e.Path == path {
			out = append(out, e.Code)
		}
	}
	return out
}

func TestValidateInferredRecordValid(t *testing.T) {
	data, c := parseString(t, "pactoVersion: \"2.5\"\n")
	if r := validation.Validate(c, data, recordBundle(goodRecord())); !r.IsValid() {
		t.Fatalf("errors: %+v", r.Errors)
	}
	// A contract built in code, with no pactoVersion, reads as the newest.
	c.PactoVersion = ""
	if r := validation.Validate(c, data, recordBundle(goodRecord())); !r.IsValid() {
		t.Fatalf("errors: %+v", r.Errors)
	}
}

func TestValidateInferredRecordNeedsPacto25(t *testing.T) {
	data, c := parseString(t, "pactoVersion: \"2.4\"\nservice:\n  name: orders\n  version: 1.0.0\n")
	r := validation.Validate(c, data, recordBundle(goodRecord()))
	if got := codesAt(r, contract.InferredRecordFile); !reflect.DeepEqual(got, []string{"NEEDS_PACTO_2_5"}) {
		t.Fatalf("codes = %v, errors %+v", got, r.Errors)
	}
}

func TestValidateStagesReportInferredRecord(t *testing.T) {
	data, c := parseString(t, "pactoVersion: \"2.4\"\nservice:\n  name: orders\n  version: 1.0.0\n")
	stages := validation.ValidateStagesWithResolver(context.Background(), c, data, recordBundle(goodRecord()), nil)
	if stages[0].Name != "structural" || stages[0].Status != validation.StageFail {
		t.Fatalf("stages = %+v", stages)
	}
	if got := codesAt(validation.Merged(stages), contract.InferredRecordFile); !reflect.DeepEqual(got, []string{"NEEDS_PACTO_2_5"}) {
		t.Fatalf("codes = %v", got)
	}
}

func TestValidateInferredRecordInvalid(t *testing.T) {
	data, c := parseString(t, "pactoVersion: \"2.5\"\n")
	fsys := fstest.MapFS{contract.InferredRecordFile: &fstest.MapFile{Data: []byte("format: pacto.inferred/v2\n")}}
	r := validation.Validate(c, data, fsys)
	if got := codesAt(r, contract.InferredRecordFile); !reflect.DeepEqual(got, []string{"INFERRED_RECORD_INVALID"}) {
		t.Fatalf("codes = %v", got)
	}
}

func TestValidateInferredRecordDigestMismatch(t *testing.T) {
	data, c := parseString(t, "pactoVersion: \"2.5\"\n")
	cases := map[string]func(*contract.InferredRecord){
		"digest": func(r *contract.InferredRecord) {
			r.Inputs.Sources[0].Digest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
		},
		"missing":  func(r *contract.InferredRecord) { r.Inputs.Sources[0].Path = "deploy/gone.yaml" },
		"unlisted": func(r *contract.InferredRecord) { r.Fields[0].Source = "chart/values.yaml:4" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			rec := goodRecord()
			edit(rec)
			r := validation.Validate(c, data, recordBundle(rec))
			if got := codesAt(r, contract.InferredRecordFile); !reflect.DeepEqual(got, []string{"INFERRED_RECORD_INVALID"}) {
				t.Fatalf("codes = %v", got)
			}
		})
	}
}

func TestValidateWithoutRecordUnchanged(t *testing.T) {
	data, c := parseString(t, "pactoVersion: \"2.4\"\nservice:\n  name: orders\n  version: 1.0.0\n")
	fsys := fstest.MapFS{"deploy/app.yaml": &fstest.MapFile{Data: []byte(deployYAML)}}
	if r := validation.Validate(c, data, fsys); !r.IsValid() {
		t.Fatalf("errors: %+v", r.Errors)
	}
}

// migratedBundle is a bundle whose pacto.yaml declares the current version
// beside a record holding one migration, from and to as given.
func migratedBundle(t *testing.T, from, to string) ([]byte, *contract.Contract, fstest.MapFS) {
	t.Helper()
	data, c := parseString(t, "pactoVersion: \""+contract.Current()+"\"\n")
	rec := goodRecord()
	rec.Migrations = []contract.InferredMigration{{From: from, To: to, Source: "pacto.yaml", By: "pacto ci test"}}
	fsys := recordBundle(rec)
	fsys["pacto.yaml"] = &fstest.MapFile{Data: data}
	return data, c, fsys
}

func TestValidateMigratedRecordValid(t *testing.T) {
	v := contract.SupportedVersions
	data, c, fsys := migratedBundle(t, v[len(v)-2], contract.Current())
	if r := validation.Validate(c, data, fsys); !r.IsValid() {
		t.Fatalf("errors: %+v", r.Errors)
	}
}

func TestValidateMigratedRecordStale(t *testing.T) {
	v := contract.SupportedVersions
	// The entry says pacto.yaml was published as the previous version, but it
	// declares the current one: a stale or hand-edited record.
	data, c, fsys := migratedBundle(t, v[len(v)-3], v[len(v)-2])
	r := validation.Validate(c, data, fsys)
	if got := codesAt(r, contract.InferredRecordFile); !reflect.DeepEqual(got, []string{"INFERRED_RECORD_INVALID"}) {
		t.Fatalf("codes = %v, errors %+v", got, r.Errors)
	}
}

func TestBundleRecordWithClusterSourceRefused(t *testing.T) {
	data, c := parseString(t, "pactoVersion: \"2.5\"\n")
	cluster := func(r *contract.InferredRecord) {
		r.Inputs.Cluster = &contract.InferredClusterInput{AsOf: "2026-11-14T10:02:00Z", Object: "shop/Deployment/invoices", CallWindow: "24h"}
	}
	cases := map[string]func(*contract.InferredRecord){
		"field source": func(r *contract.InferredRecord) {
			cluster(r)
			r.Fields = append(r.Fields, contract.Inferred{Field: "dependencies[ledger].compatibility", Value: "compatibility unknown",
				Source: "cluster:fleet reported versions 2.3.1", Confidence: contract.ConfidenceLikely, AsOf: "2026-11-14T09:58:12Z",
				Completeness: contract.CoveragePartial, Proposed: "^2.3.0"})
		},
		"input": cluster,
		"coverage": func(r *contract.InferredRecord) {
			cluster(r)
			r.Coverage = []contract.InferredCoverage{{Source: "pods", State: contract.CoverageComplete}}
		},
		"hidden": func(r *contract.InferredRecord) {
			cluster(r)
			r.Hidden = &contract.InferredHidden{Services: 1}
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			rec := goodRecord()
			edit(rec)
			// The record parses: it is refused as a bundle record, not as malformed.
			if _, err := contract.ParseInferredRecord(rec.Marshal()); err != nil {
				t.Fatal(err)
			}
			r := validation.Validate(c, data, recordBundle(rec))
			if got := codesAt(r, contract.InferredRecordFile); !reflect.DeepEqual(got, []string{"INFERRED_RECORD_INVALID"}) {
				t.Fatalf("codes = %v, errors %+v", got, r.Errors)
			}
		})
	}
}
