package validation_test

import (
	"fmt"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/validation"
)

// ExampleEvaluate is the canonical "custom collector" flow shown in
// docs/collectors.md: construct a Contract, have a collector produce a validated
// EvidenceSet, then call the pure engine and inspect Findings + Coverage. It is a
// compiled, run test (the // Output line is asserted by `go test`), so the
// documentation snippet can never drift from an API that actually exists.
func ExampleEvaluate() {
	// 1. The contract declares intent (loaded from a bundle in real use).
	c := contract.Contract{
		Service:    contract.Service{Name: "orders", Version: "1.0.0"},
		Interfaces: []contract.Interface{{Name: "public-api", Type: "openapi", Ref: "interfaces/openapi.yaml"}},
	}

	// 2. A collector observes the running environment and produces an EvidenceSet.
	prov := evidence.Provenance{Collector: "example", DetectedAt: time.Unix(0, 0)}
	ev := evidence.EvidenceSet{
		Subject:     evidence.SubjectRef{Kind: "service", Name: "orders"},
		ContractRef: "oci://example/orders:1.0.0",
		Source:      "example",
		ObservedAt:  time.Unix(0, 0),
		Observations: []evidence.Observation{
			evidence.NewInterfaceObserved(evidence.SubjectRef{Kind: "interface", Name: "public-api"}, "openapi", true, prov),
		},
	}

	// 3. The pure engine evaluates Contract x Evidence.
	findings, coverage := validation.Evaluate(c, ev)

	fmt.Printf("findings=%d evaluated=%d/%d\n", len(findings), coverage.Evaluated, coverage.Required)
	// Output: findings=0 evaluated=1/1
}
