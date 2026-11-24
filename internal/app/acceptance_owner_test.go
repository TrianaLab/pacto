package app

import (
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// ownerRuledStore is the env's store under the owner rule, its provenance
// checker remembering the subject as signed by acme's CI at ownerCommit.
func (e *acceptEnv) ownerRuledStore(t *testing.T) *acceptanceStore {
	t.Helper()
	prov := newProvenanceChecker(e.prov.client)
	prov.remember("oci://"+e.subject, attestedProvenance())
	return e.svc.ownerRuled(e.store(t), prov)
}

func TestAcceptanceByAttestedOwnerCountsUnderOwnerPolicy(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, true)
	e.attach(t, attachment{record: e.rangesRecord(), token: personTok("ana@acme.com")})
	q := e.query()
	q.Owner, q.Record = contract.Owner{}, attestedRecord("billing")
	// No owner policy: an attested owner never accepts.
	if got, _ := e.read(t, e.ownerRuledStore(t), q); !slices.Equal(states(got), []string{impact.AcceptRouted}) {
		t.Fatalf("no policy: %v", states(got))
	}
	e.withOwnerPolicy(t)
	if got, _ := e.read(t, e.ownerRuledStore(t), q); !slices.Equal(states(got), []string{impact.AcceptCounted}) {
		t.Fatalf("a billing member: %v", states(got))
	}
}

func TestAcceptanceByDefaultApproverCountsUnderOwnerPolicy(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, true)
	e.attach(t, attachment{record: e.rangesRecord(), token: personTok("admin@acme.com")})
	if got, _ := e.read(t, e.ownerRuledStore(t), e.query()); !slices.Equal(states(got), []string{impact.AcceptRouted}) {
		t.Fatalf("no policy: %v", states(got))
	}
	e.withOwnerPolicy(t)
	if got, _ := e.read(t, e.ownerRuledStore(t), e.query()); !slices.Equal(states(got), []string{impact.AcceptCounted}) {
		t.Fatalf("a default approver: %v", states(got))
	}
}

func TestAcceptProvenanceNeedsReferrers(t *testing.T) {
	t.Parallel()
	e := newAcceptEnv(t, false)
	env := e.svc.env()
	if acceptProvenance(env, nil) == nil {
		t.Fatal("a registry that lists referrers got no checker")
	}
	env.publishRegistry = func() publish.Registry { return headFails{e.reg} }
	if acceptProvenance(env, nil) != nil {
		t.Fatal("a registry with no referrer listing got a checker")
	}
}
