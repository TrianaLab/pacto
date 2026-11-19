package fleet

import (
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func parseInferredFrom(s string) error {
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Declaration: &contract.InferredDeclaration{
		State: contract.DeclarationDraft, InferredFrom: s,
		Asker: contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent}}}
	_, err := contract.ParseInferredRecord(rec.Marshal())
	return err
}

// TestInferredFromGrammarMatchesK8sCaller keeps the record's inferredFrom
// check (pkg/contract cannot import fleet) in step with ParseK8sCaller:
// every identity K8sCaller writes passes both, and the record accepts nothing
// ParseK8sCaller cannot decode.
func TestInferredFromGrammarMatchesK8sCaller(t *testing.T) {
	cases := []string{
		K8sCaller("", "billing", "Deployment", "invoices"),
		K8sCaller("prod/eu", "billing", "StatefulSet", "invoices"),
		K8sCaller("3f1c0d2e-9a8b", "billing", "Deployment", "invoices"),
		"k8s:a/b", "k8s:a/b/c/d/e", "k8s:a", "svc:x", "invoices", "k8s:billing/Deployment/invoices",
		"svc:a/b/c", "k8s:prod/billing/Deployment/invoices",
	}
	for _, s := range cases {
		_, _, _, _, ok := ParseK8sCaller(s)
		if err := parseInferredFrom(s); (err == nil) != ok {
			t.Errorf("%q: ParseK8sCaller ok=%v, record err=%v", s, ok, err)
		}
	}
}

// TestInferredFromStricterThanParseK8sCaller pins where the two disagree:
// ParseK8sCaller splits on "/" and accepts empty or blank parts, the record
// refuses them, so a record never names an identity with no namespace.
func TestInferredFromStricterThanParseK8sCaller(t *testing.T) {
	for _, s := range []string{"k8s://Deployment/invoices", "k8s:billing//invoices", "k8s:billing/Deployment/",
		"k8s:/billing/Deployment/invoices", "k8s:bill ing/Deployment/invoices", "k8s:billing/Deployment/in\tvoices"} {
		if _, _, _, _, ok := ParseK8sCaller(s); !ok {
			t.Errorf("%q: ParseK8sCaller refuses it; this case no longer pins a disagreement", s)
		}
		if parseInferredFrom(s) == nil {
			t.Errorf("%q: the record must refuse an empty or blank part", s)
		}
	}
}
