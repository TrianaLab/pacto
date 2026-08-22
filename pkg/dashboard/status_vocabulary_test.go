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
//
// The comparison is between the two COMPLETE vocabularies, not between a list of
// pairs someone remembered to extend. A pairwise table proves the pairs in it
// agree and says nothing about a value that exists on one side only: adding
// StatusDeferred here, accepting it in NormalizeContractStatus and never telling
// pkg/fleet left every status test in this repository green while the dashboard
// emitted a status the fleet rejects as non-canonical. Both sets are therefore
// read from the structures PRODUCTION uses -- canonicalStatuses is what
// NormalizeContractStatus accepts, fleet.CanonicalStatuses enumerates the table
// fleet.ValidStatus accepts from -- so neither side can gain or lose a value
// without this test seeing it, and there is no third list here to drift.
func TestContractStatusVocabularyMatchesFleet(t *testing.T) {
	fleetVocabulary := fleet.CanonicalStatuses()
	inFleet := make(map[string]bool, len(fleetVocabulary))
	for _, s := range fleetVocabulary {
		inFleet[s] = true
	}

	inDashboard := make(map[ContractStatus]bool, len(canonicalStatuses))
	for _, s := range canonicalStatuses {
		// Seven statuses, seven meanings. Unknown ("evaluated, cannot tell") and
		// NotEvaluated ("never looked") are the pair most easily collapsed, and a
		// collapse here would make every aggregate over them wrong in the same
		// direction.
		if inDashboard[s] {
			t.Errorf("two canonical statuses share the value %q; the vocabulary must stay one meaning per value", s)
		}
		inDashboard[s] = true
	}

	// Direction one: nothing the dashboard treats as canonical may be foreign to
	// the fleet, which would emit a status the fleet's own filters reject.
	for _, s := range canonicalStatuses {
		if !inFleet[string(s)] {
			t.Errorf("the dashboard declares %q canonical, but the fleet vocabulary is %v", s, fleetVocabulary)
		}
		// ValidStatus is the predicate production filtering actually calls; assert it
		// as well as the enumeration, so a future split between the two is caught here
		// rather than in a query that silently rejects a legitimate filter.
		if !fleet.ValidStatus(string(s)) {
			t.Errorf("the fleet does not accept the dashboard status %q as canonical", s)
		}
	}

	// Direction two: nothing the fleet treats as canonical may be foreign to the
	// dashboard, which would fold a real status into Unknown on the way in.
	for _, s := range fleetVocabulary {
		if !inDashboard[ContractStatus(s)] {
			t.Errorf("the fleet declares %q canonical, but the dashboard vocabulary does not know it", s)
		}
		// Every canonical status must survive normalization AS ITSELF. Folding one
		// into Unknown is not a smaller answer, it is a different one.
		if got := NormalizeContractStatus(ContractStatus(s)); string(got) != s {
			t.Errorf("NormalizeContractStatus(%q) = %q, want %q: a canonical fleet status must never be folded into another status", s, got, s)
		}
	}
}
