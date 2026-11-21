package validation_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

func externalData(version, external string) map[string]any {
	return map[string]any{"pactoVersion": version, "service": map[string]any{"name": "stripe", "version": "0.0.1", "external": external}}
}

func errorCodes(r validation.ValidationResult) []string {
	var out []string
	for _, e := range r.Errors {
		out = append(out, e.Path+" "+e.Code)
	}
	return out
}

func TestServiceExternalValid(t *testing.T) {
	for _, id := range []string{"stripe", "host/corp-legacy.example", "aws/rds/123456789012/eu-west-1/orders-db"} {
		if r := validation.ValidateStructural(externalData("2.5", id)); !r.IsValid() {
			t.Errorf("%q: %v", id, r.Errors)
		}
		data, c := parseString(t, "pactoVersion: \"2.5\"\nservice:\n  name: stripe\n  version: 0.0.1\n  external: "+id+"\n")
		if c.Service.External != id {
			t.Fatalf("parsed external = %q", c.Service.External)
		}
		if r := validation.Validate(c, data, nil); !r.IsValid() {
			t.Errorf("%q: %v", id, r.Errors)
		}
	}
}

func TestServiceExternalNeeds25(t *testing.T) {
	data := externalData("2.4", "stripe")
	r := validation.ValidateStructural(data)
	if got := errorCodes(r); !reflect.DeepEqual(got, []string{"service.external NEEDS_PACTO_2_5"}) {
		t.Fatalf("errors = %v", got)
	}
	if !strings.Contains(r.Errors[0].Message, `set pactoVersion: "2.5"`) {
		t.Fatalf("message = %q", r.Errors[0].Message)
	}
	if data["service"].(map[string]any)["external"] != "stripe" {
		t.Fatal("the caller's data was changed")
	}
	// The 2.4 fields keep their own code.
	old := externalData("2.3", "stripe")
	old["service"].(map[string]any)["domain"] = "payments"
	if got := errorCodes(validation.ValidateStructural(old)); !slices.Contains(got, "service.external NEEDS_PACTO_2_5") ||
		!slices.Contains(got, "service.domain NEEDS_PACTO_2_4") || len(got) != 2 {
		t.Fatalf("errors = %v", got)
	}
}

func TestServiceExternalPattern(t *testing.T) {
	for _, id := range []string{"Stripe", "a//b", strings.Repeat("a", 254)} {
		r := validation.ValidateStructural(externalData("2.5", id))
		if got := errorCodes(r); len(got) == 0 || !strings.HasSuffix(got[0], "SCHEMA_VIOLATION") {
			t.Errorf("%q: errors = %v", id, got)
		}
	}
}

func TestServiceExternalReservedRefused(t *testing.T) {
	data, c := parseString(t, "pactoVersion: \"2.5\"\nservice:\n  name: egress\n  version: 0.0.1\n  external: other-egress\n")
	r := validation.Validate(c, data, nil)
	if got := codesAt(r, "service.external"); !reflect.DeepEqual(got, []string{"INVALID_EXTERNAL_IDENTITY"}) {
		t.Fatalf("codes = %v, errors %+v", got, r.Errors)
	}
}

// surfaceBundle is a bundle holding only a record with a surface naming
// identity.
func surfaceBundle(identity string) (*contract.InferredRecord, []byte) {
	rec := &contract.InferredRecord{
		Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1"},
		Surface: &contract.InferredSurface{Identity: identity, Class: "vendor-api", AsOf: "2026-11-17T10:00:00Z",
			Completeness: &contract.SurfaceCompleteness{State: "complete"},
			Hosts:        []contract.SurfaceValue{{Value: "api.stripe.com", Sources: []string{"config"}}}},
	}
	return rec, rec.Marshal()
}

func surfaceErrors(t *testing.T, external, identity string) []string {
	t.Helper()
	yaml := "pactoVersion: \"2.5\"\nservice:\n  name: stripe\n  version: 0.0.1\n"
	if external != "" {
		yaml += "  external: " + external + "\n"
	}
	data, c := parseString(t, yaml)
	rec, _ := surfaceBundle(identity)
	r := validation.Validate(c, data, recordBundle(rec))
	return codesAt(r, contract.InferredRecordFile)
}

func TestInferredRecordSurfaceNeedsServiceExternal(t *testing.T) {
	if got := surfaceErrors(t, "", "stripe"); !reflect.DeepEqual(got, []string{"INFERRED_RECORD_INVALID"}) {
		t.Fatalf("codes = %v", got)
	}
	if got := surfaceErrors(t, "stripe", "stripe"); len(got) != 0 {
		t.Fatalf("codes = %v", got)
	}
}

func TestInferredRecordSurfaceIdentityMismatch(t *testing.T) {
	if got := surfaceErrors(t, "stripe", "host/stripe.com"); !reflect.DeepEqual(got, []string{"INFERRED_RECORD_INVALID"}) {
		t.Fatalf("codes = %v", got)
	}
	// The decoder takes other-egress's shape; the identity parser refuses it.
	if got := surfaceErrors(t, "stripe", "other-egress"); !reflect.DeepEqual(got, []string{"INFERRED_RECORD_INVALID"}) {
		t.Fatalf("codes = %v", got)
	}
}
