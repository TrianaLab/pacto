package fleet

import (
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// TestInferredFromGrammarMatchesK8sCaller keeps the record's inferredFrom
// check (pkg/contract cannot import fleet) in step with ParseK8sCaller: both
// accept and refuse the same identities.
func TestInferredFromGrammarMatchesK8sCaller(t *testing.T) {
	cases := []string{
		K8sCaller("", "billing", "Deployment", "invoices"),
		K8sCaller("prod/eu", "billing", "StatefulSet", "invoices"),
		K8sCaller("3f1c0d2e-9a8b", "billing", "Deployment", "invoices"),
		"k8s:a/b", "k8s:a/b/c/d/e", "k8s:a", "svc:x", "invoices", "k8s:billing/Deployment/invoices",
	}
	for _, s := range cases {
		_, _, _, _, ok := ParseK8sCaller(s)
		rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Declaration: &contract.InferredDeclaration{
			State: contract.DeclarationDraft, InferredFrom: s,
			Asker: contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent}}}
		_, err := contract.ParseInferredRecord(rec.Marshal())
		if (err == nil) != ok {
			t.Errorf("%q: ParseK8sCaller ok=%v, record err=%v", s, ok, err)
		}
	}
}
