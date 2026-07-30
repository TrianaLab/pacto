package impact

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// crossDomainSnap builds a fleet with the same service names in two domains, each
// with its own consumer, to prove impact answers are domain-isolated (section 3/section 10).
func crossDomainSnap(t *testing.T) *fleet.FleetSnapshot {
	t.Helper()
	rev := func(domain, name, digest string, deps ...string) fleet.RawRevision {
		c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: "1.0.0"}}
		for _, d := range deps {
			c.Dependencies = append(c.Dependencies, contract.Dependency{Name: d, Ref: "oci://x/" + d, Required: true, Compatibility: "^1.0.0"})
		}
		return fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Domain: domain, Digest: digest}
	}
	col := &fleet.Collection{Revisions: []fleet.RawRevision{
		rev("domain-a", "payments", "sha256:a1"),
		rev("domain-a", "checkout", "sha256:a2", "payments"),
		rev("domain-b", "payments", "sha256:b1"),
		rev("domain-b", "checkout", "sha256:b2", "payments"),
	}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("s", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// TestAnalyze_CrossDomainIsolation proves that analyzing domain-a/payments never
// surfaces domain-b's consumer, and vice versa.
func TestAnalyze_CrossDomainIsolation(t *testing.T) {
	snap := crossDomainSnap(t)
	pay := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "payments", Version: "1.0.0"}}

	for _, domain := range []string{"domain-a", "domain-b"} {
		res := Analyze(context.Background(), pay, pay, nil, nil, snap, Options{Domain: domain})
		if len(res.Consumers) != 1 {
			t.Fatalf("%s: expected 1 consumer, got %d: %+v", domain, len(res.Consumers), res.Consumers)
		}
		c := res.Consumers[0]
		if c.Service != "checkout" || c.Domain != domain {
			t.Errorf("%s: consumer = %q (domain %q), want checkout in %s", domain, c.Service, c.Domain, domain)
		}
	}
}
