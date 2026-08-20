package dashboard

import (
	"testing"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// The canonical contract-status vocabulary is declared three times in this
// repository, because no single package can own it:
//
//   - here, as the typed ContractStatus (pkg/dashboard/model.go);
//   - in pkg/fleet, as bare strings, because the fleet layer must not import the
//     dashboard and so restates them ("aligned with the operator and dashboard
//     vocabulary" is all that comment can promise);
//   - in the operator CRD, as a kubebuilder Enum marker, which the API server
//     enforces on admission.
//
// The CRD copy is enforced by Kubernetes. The fleet copy was enforced by nothing.
// This file makes that alignment executable from the one package that can see
// both sides.
//
// The regression it prevents is silent, which is why a runtime conformance test
// cannot find it. NormalizeContractStatus folds any value it does not recognize
// into Unknown, and that is correct for genuinely foreign input arriving from a
// cluster (source_k8s.go passes the operator's raw status string through it). It
// is wrong for a canonical value the two sides have stopped spelling identically:
// "we have not evaluated this contract" silently becomes "we evaluated it and
// cannot tell", every affected service reads as inconclusive rather than
// untouched, and Unknown is a member of every emitted enum domain so
// TestProductHTTP_EmittedEnumsConform and the OpenAPI enum assertions all stay
// green.
//
// TestNormalizeContractStatus (graph_test.go) is the function's own test and
// remains the right home for the folding behaviour. It cannot stand in for this
// one: it checks five of the seven statuses -- Invalid and NotEvaluated are absent
// from its table -- and it compares each constant against itself, so it holds
// under any spelling drift as long as both sides of its table drift together.
func TestContractStatusVocabularyMatchesFleet(t *testing.T) {
	pairs := []struct {
		dashboard ContractStatus
		fleet     string
	}{
		{StatusCompliant, fleet.StatusCompliant},
		{StatusWarning, fleet.StatusWarning},
		{StatusNonCompliant, fleet.StatusNonCompliant},
		{StatusUnknown, fleet.StatusUnknown},
		{StatusReference, fleet.StatusReference},
		{StatusInvalid, fleet.StatusInvalid},
		{StatusNotEvaluated, fleet.StatusNotEvaluated},
	}

	seen := make(map[ContractStatus]bool, len(pairs))
	for _, p := range pairs {
		if string(p.dashboard) != p.fleet {
			t.Errorf("the dashboard says %q where the fleet says %q", p.dashboard, p.fleet)
		}
		// Every canonical status must survive normalization AS ITSELF. Folding one
		// into Unknown is not a smaller answer, it is a different one.
		if got := NormalizeContractStatus(ContractStatus(p.fleet)); got != p.dashboard {
			t.Errorf("NormalizeContractStatus(%q) = %q, want %q: a canonical fleet status must never be folded into another status", p.fleet, got, p.dashboard)
		}
		// And the fleet must recognize what the dashboard emits, so neither side can
		// gain a status the other has never heard of.
		if !fleet.ValidStatus(string(p.dashboard)) {
			t.Errorf("the fleet does not accept the dashboard status %q as canonical", p.dashboard)
		}
		// Seven statuses, seven meanings. Unknown ("evaluated, cannot tell") and
		// NotEvaluated ("never looked") are the pair most easily collapsed, and a
		// collapse here would make every aggregate over them wrong in the same
		// direction.
		if seen[p.dashboard] {
			t.Errorf("two canonical statuses share the value %q; the vocabulary must stay one meaning per value", p.dashboard)
		}
		seen[p.dashboard] = true
	}
}
