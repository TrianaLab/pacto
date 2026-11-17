package impact

import (
	"encoding/json"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestImpactUnchangedByExternals: consumers that also declare ref-less
// external dependencies, which the fleet joins on external nodes, give the
// same consumers, gate and completeness as consumers that declare none. An
// external never becomes a provider or a dependent.
func TestImpactUnchangedByExternals(t *testing.T) {
	stripe := contract.Dependency{Name: "Stripe", External: true, Version: "2024-06-20", Required: true}
	mq := contract.Dependency{Name: "legacy-mq", External: true}
	plain := evidenceSnap(t, []fleet.RawRevision{gwRev(authDep), {Bundle: mkBundle(svcContract("billing", "1.0.0", contract.Owner{Team: "billing"}, authDep))}},
		[]fleet.RawTarget{gwTarget(ago(0))})
	joined := evidenceSnap(t, []fleet.RawRevision{gwRev(authDep, stripe, mq), {Bundle: mkBundle(svcContract("billing", "1.0.0", contract.Owner{Team: "billing"}, stripe, authDep))}},
		[]fleet.RawTarget{gwTarget(ago(0))})
	if len(joined.Externals) != 2 {
		t.Fatalf("the joined fleet holds %d externals, want 2", len(joined.Externals))
	}
	a, b := analyzeAuth(plain, Options{}), analyzeAuth(joined, Options{})
	ca, _ := json.Marshal(a.Consumers)
	cb, _ := json.Marshal(b.Consumers)
	if string(ca) != string(cb) {
		t.Errorf("consumers differ:\n%s\n%s", ca, cb)
	}
	if a.Gate != b.Gate || a.GateReason != b.GateReason || a.Completeness != b.Completeness {
		t.Errorf("gate %q/%q %q, want %q/%q %q", b.Gate, b.GateReason, b.Completeness, a.Gate, a.GateReason, a.Completeness)
	}
	codes := func(r *Result) (out []string) {
		for _, l := range r.Limitations {
			out = append(out, l.Code)
		}
		return out
	}
	la, _ := json.Marshal(codes(a))
	lb, _ := json.Marshal(codes(b))
	if string(la) != string(lb) {
		t.Errorf("limitations differ: %s, want %s", lb, la)
	}
}
