package validation_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// declaredRecord is goodRecord with a cluster read proposing ^2.3.0 for
// ledger and a person's declaration accepting accepted.
func declaredRecord(accepted ...contract.InferredAcceptance) *contract.InferredRecord {
	rec := goodRecord()
	rec.Inputs.Cluster = &contract.InferredClusterInput{AsOf: "2026-11-14T10:02:00Z", Object: "shop/Deployment/invoices", CallWindow: "24h"}
	rec.Fields = append(rec.Fields, contract.Inferred{Field: "dependencies[ledger].compatibility", Value: "compatibility unknown",
		Source: "cluster:fleet reported versions 2.3.1", Confidence: contract.ConfidenceLikely, AsOf: "2026-11-14T09:58:12Z",
		Completeness: contract.CoveragePartial, Proposed: "^2.3.0"})
	rec.Declaration = &contract.InferredDeclaration{State: contract.DeclarationDeclared,
		Asker:        contract.InferredSigner{Subject: "ana@acme.com", Issuer: "https://accounts.google.com", Class: contract.SignerClassHuman},
		InferredFrom: "k8s:shop/Deployment/invoices", Accepted: accepted}
	return rec
}

// ledgerContract is a 2.5 pacto.yaml declaring ledger with compatibility.
func ledgerContract(t *testing.T, compatibility string) ([]byte, *contract.Contract) {
	t.Helper()
	return parseString(t, "pactoVersion: \"2.5\"\ndependencies:\n  - name: ledger\n    ref: oci://ghcr.io/acme/ledger\n    required: true\n    compatibility: \""+compatibility+"\"\n")
}

func declarationCodes(t *testing.T, compatibility string, rec *contract.InferredRecord) ([]string, validation.ValidationResult) {
	t.Helper()
	data, c := ledgerContract(t, compatibility)
	r := validation.Validate(c, data, recordBundle(rec))
	return codesAt(r, contract.InferredRecordFile), r
}

func TestDeclarationStageRefusesUnacceptedRange(t *testing.T) {
	got, r := declarationCodes(t, "^2.3.0", declaredRecord())
	if !reflect.DeepEqual(got, []string{"DECLARATION_RANGE_NOT_ACCEPTED"}) {
		t.Fatalf("codes = %v, errors %+v", got, r.Errors)
	}
	if msg := r.Errors[0].Message; !strings.Contains(msg, "dependencies[ledger] declares compatibility ^2.3.0") || !strings.Contains(msg, "accept the proposal ^2.3.0") {
		t.Fatalf("message = %q", msg)
	}
}

func TestDeclarationStageRefusesAcceptedMismatch(t *testing.T) {
	ledger := func(r string) contract.InferredAcceptance {
		return contract.InferredAcceptance{Field: "dependencies[ledger].compatibility", Range: r}
	}
	for name, tc := range map[string]struct {
		compatibility string
		accepted      contract.InferredAcceptance
		want          string
		codes         []string
	}{
		"other range":   {"^2.0.0", ledger("^2.3.0"), "pacto.yaml declares compatibility ^2.0.0", []string{"DECLARATION_ACCEPTED_MISMATCH"}},
		"still unknown": {"unknown", ledger("^2.3.0"), "pacto.yaml declares compatibility unknown", []string{"DECLARATION_ACCEPTED_MISMATCH"}},
		// ledger's ^2.3.0 is then a proposal nobody accepted, too.
		"no dependency": {"^2.3.0", contract.InferredAcceptance{Field: "dependencies[audit].compatibility", Range: "^1.0.0"}, "pacto.yaml declares no such dependency",
			[]string{"DECLARATION_ACCEPTED_MISMATCH", "DECLARATION_RANGE_NOT_ACCEPTED"}},
	} {
		t.Run(name, func(t *testing.T) {
			got, r := declarationCodes(t, tc.compatibility, declaredRecord(tc.accepted))
			if !reflect.DeepEqual(got, tc.codes) {
				t.Fatalf("codes = %v, errors %+v", got, r.Errors)
			}
			if !strings.Contains(r.Errors[0].Message, tc.want) {
				t.Fatalf("message = %q, want %q", r.Errors[0].Message, tc.want)
			}
		})
	}
	// A dependency that declares no compatibility accepts nothing.
	data, c := parseString(t, "pactoVersion: \"2.5\"\ndependencies:\n  - name: ledger\n    ref: oci://ghcr.io/acme/ledger\n    required: true\n")
	r := validation.Validate(c, data, recordBundle(declaredRecord(ledger("^2.3.0"))))
	if got := codesAt(r, contract.InferredRecordFile); !reflect.DeepEqual(got, []string{"DECLARATION_ACCEPTED_MISMATCH"}) || !strings.Contains(r.Errors[len(r.Errors)-1].Message, "declares no compatibility") {
		t.Fatalf("codes = %v, errors %+v", got, r.Errors)
	}
	// A draft's requested ranges are checked the same way.
	rec := declaredRecord()
	rec.Declaration = &contract.InferredDeclaration{State: contract.DeclarationDraft,
		Asker:     contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent},
		Requested: []contract.InferredAcceptance{ledger("^2.3.0")}}
	if got, r := declarationCodes(t, "unknown", rec); !reflect.DeepEqual(got, []string{"DECLARATION_ACCEPTED_MISMATCH"}) ||
		!strings.Contains(r.Errors[0].Message, "the declaration requests ^2.3.0 on dependencies[ledger].compatibility") {
		t.Fatalf("codes = %v, errors %+v", got, r.Errors)
	}
}

func TestDeclarationStageAcceptsUnknownProposal(t *testing.T) {
	if got, r := declarationCodes(t, "unknown", declaredRecord()); len(got) > 0 {
		t.Fatalf("codes = %v, errors %+v", got, r.Errors)
	}
	accepted := declaredRecord(contract.InferredAcceptance{Field: "dependencies[ledger].compatibility", Range: "^2.3.0"})
	if got, r := declarationCodes(t, "^2.3.0", accepted); len(got) > 0 {
		t.Fatalf("codes = %v, errors %+v", got, r.Errors)
	}
	// A proposal for a dependency pacto.yaml no longer declares declares nothing.
	data, c := parseString(t, "pactoVersion: \"2.5\"\n")
	if r := validation.Validate(c, data, recordBundle(declaredRecord())); !r.IsValid() {
		t.Fatalf("errors: %+v", r.Errors)
	}
}

func TestDeclarationStageSilentWithoutDeclaration(t *testing.T) {
	// No declaration: the cluster record is refused as a bundle record, and
	// declarationStage adds nothing.
	rec := declaredRecord()
	rec.Declaration = nil
	if got, r := declarationCodes(t, "^2.3.0", rec); !reflect.DeepEqual(got, []string{"INFERRED_RECORD_INVALID"}) {
		t.Fatalf("codes = %v, errors %+v", got, r.Errors)
	}
	// A folder record with no declaration and a declared range is valid.
	if got, r := declarationCodes(t, "^2.3.0", goodRecord()); len(got) > 0 {
		t.Fatalf("codes = %v, errors %+v", got, r.Errors)
	}
}
