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
