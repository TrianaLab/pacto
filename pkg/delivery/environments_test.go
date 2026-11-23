package delivery

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var ladderServices = []string{"payments", "orders", "cart", "search", "payments-api"}

func TestProposeLadderFromNamespaceSuffixes(t *testing.T) {
	got := ProposeLadder([]string{"staging", "production"}, LadderInput{Services: ladderServices,
		Namespaces: []string{"payments-preview", "orders-preview", "cart-preview", "search-preview", "kube-system"}})
	want := LadderProposal{{Name: "preview", Source: "namespaces *-preview, 4 found", Provenance: RungInferred}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	// One namespace is not a pattern; an unknown prefix does not count.
	if got := ProposeLadder(nil, LadderInput{Services: ladderServices,
		Namespaces: []string{"payments-preview", "ghost-preview", "other-preview"}}); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
	// The longest known service is the namespace's: payments-api-preview is
	// payments-api's preview, never payments' api-preview.
	got = ProposeLadder(nil, LadderInput{Services: ladderServices, Namespaces: []string{"payments-api-preview", "cart-preview"}})
	if len(got) != 1 || got[0].Name != "preview" || got[0].Source != "namespaces *-preview, 2 found" {
		t.Fatalf("got %+v", got)
	}
}

func TestProposeLadderFromAdvertisedEnvironments(t *testing.T) {
	got := ProposeLadder([]string{"staging", "production"}, LadderInput{
		Advertised: []AdvertisedEnvironment{{Name: "qa", Source: "the cluster name eu-qa"}}})
	want := LadderProposal{{Name: "qa", Source: "the cluster name eu-qa", Provenance: RungInferred}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestInferredRungIsALeaf(t *testing.T) {
	got := ProposeLadder([]string{"staging"}, LadderInput{Services: ladderServices,
		Namespaces: []string{"payments-preview", "orders-preview"},
		Advertised: []AdvertisedEnvironment{{Name: "qa", Source: "x"}}})
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	for _, r := range got {
		if r.Upstream != "" || r.Provenance != RungInferred {
			t.Fatalf("an inferred rung must be a leaf: %+v", r)
		}
	}
}

func TestProposalNeverChangesDeclaredChain(t *testing.T) {
	declared := []string{"staging", "production"}
	_ = ProposeLadder(declared, LadderInput{Services: ladderServices,
		Namespaces: []string{"payments-staging", "orders-staging", "payments-production", "orders-production"},
		Advertised: []AdvertisedEnvironment{{Name: "staging", Source: "x"}, {Name: "production", Source: "y"}}})
	if !reflect.DeepEqual(declared, []string{"staging", "production"}) {
		t.Fatalf("declared chain changed: %v", declared)
	}
	if got := ProposeLadder(declared, LadderInput{Services: ladderServices,
		Namespaces: []string{"payments-staging", "orders-staging"}}); len(got) != 0 {
		t.Fatalf("a declared rung must not be proposed: %+v", got)
	}
}

func TestProposalSkipsDeclaredAndConfirmed(t *testing.T) {
	// The confirmed rung preview sits beside the declared chain in known.
	got := ProposeLadder([]string{"staging", "production", "preview"}, LadderInput{Services: ladderServices,
		Namespaces: []string{"payments-preview", "orders-preview", "payments-qa", "orders-qa"}})
	if len(got) != 1 || got[0].Name != "qa" {
		t.Fatalf("got %+v", got)
	}
}

func TestProposalDigestStable(t *testing.T) {
	a := LadderProposal{{Name: "preview", Source: "s", Provenance: RungInferred}, {Name: "qa", Source: "t", Provenance: RungInferred}}
	b := LadderProposal{a[1], a[0]}
	if ProposalDigest(a) != ProposalDigest(b) || !strings.HasPrefix(ProposalDigest(a), "sha256:") || len(ProposalDigest(a)) != 71 {
		t.Fatalf("digest not stable: %s %s", ProposalDigest(a), ProposalDigest(b))
	}
	if ProposalDigest(LadderProposal{a[0]}) == ProposalDigest(a) {
		t.Fatal("a different proposal must digest differently")
	}
	if ProposalDigest(nil) != "" {
		t.Fatal("an empty proposal has no digest")
	}
}

func TestProposalSkipsSynonymOfDeclaredRung(t *testing.T) {
	adv := AdvertisedEnvironments(nil, "eu-prod-stage", "node-label")
	if len(adv) != 2 {
		t.Fatalf("advertised %+v", adv)
	}
	if got := ProposeLadder([]string{"staging", "production"}, LadderInput{Advertised: adv}); len(got) != 0 {
		t.Fatalf("a synonym of a declared rung must not be proposed: %+v", got)
	}
	// Undeclared, the synonym is proposed under its canonical name.
	got := ProposeLadder([]string{"staging"}, LadderInput{Advertised: adv})
	if len(got) != 1 || got[0].Name != "production" || got[0].Source != "the cluster name eu-prod-stage" {
		t.Fatalf("got %+v", got)
	}
	// dev also canonicalizes, from a namespace suffix too.
	got = ProposeLadder(nil, LadderInput{Services: ladderServices, Namespaces: []string{"payments-dev", "orders-dev"}})
	if len(got) != 1 || got[0].Name != "development" {
		t.Fatalf("got %+v", got)
	}
}

func TestRungNamesAreDNSLabels(t *testing.T) {
	got := ProposeLadder(nil, LadderInput{Advertised: []AdvertisedEnvironment{
		{Name: "Bad_Name", Source: "x"}, {Name: "ok", Source: "y"}, {Name: strings.Repeat("a", 64), Source: "z"}}})
	if len(got) != 1 || got[0].Name != "ok" {
		t.Fatalf("got %+v", got)
	}
	for _, r := range got {
		if !dnsLabel(r.Name) {
			t.Fatalf("%q is not a DNS label", r.Name)
		}
	}
}

func TestProposalBounded(t *testing.T) {
	var adv []AdvertisedEnvironment
	for i := range 20 {
		adv = append(adv, AdvertisedEnvironment{Name: fmt.Sprintf("env%02d", i), Source: "x"})
	}
	if got := ProposeLadder(nil, LadderInput{Advertised: adv}); len(got) != maxProposedRungs {
		t.Fatalf("got %d rungs", len(got))
	}
}

var chain = []LadderRung{{Name: "staging", Provenance: RungDeclared}, {Name: "production", Upstream: "staging", Provenance: RungDeclared},
	{Name: "preview", Provenance: RungConfirmed}}

func TestHostedRungsFromClusterName(t *testing.T) {
	for name, want := range map[string][]string{
		"eu-prod":     {"production"},
		"stage-eu":    {"staging"},
		"eu-1":        {"staging", "production", "preview"},
		"dev-sandbox": nil,
	} {
		if got := HostedRungs(chain, AdvertisedEnvironments(nil, name, "node-label")); !reflect.DeepEqual(got, want) {
			t.Errorf("%s hosts %v, want %v", name, got, want)
		}
	}
	if got := HostedRungs(chain, AdvertisedEnvironments(nil, "prod-uid", fleet.ClusterNameFromUID)); len(got) != 3 {
		t.Errorf("a uid-named cluster hosts %v", got)
	}
}

func TestNoDeclaredEnvironmentsTakesTheLadder(t *testing.T) {
	got := DesiredEnvironments(chain, nil, time.Now())
	want := []DesiredEnvironment{{Name: "staging", Source: DesiredFromLadder}, {Name: "production", Upstream: "staging", Source: DesiredFromLadder},
		{Name: "preview", Source: DesiredFromConfirmed}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestDeclaredEnvironmentsAreTheExactSet(t *testing.T) {
	got := DesiredEnvironments(chain, []contract.Environment{{Name: "staging"}}, time.Now())
	if !reflect.DeepEqual(got, []DesiredEnvironment{{Name: "staging", Source: DesiredFromContract}}) {
		t.Fatalf("got %+v", got)
	}
	// A declared rung keeps its upstream, so its gate is never skipped.
	got = DesiredEnvironments(chain, []contract.Environment{{Name: "production"}}, time.Now())
	if len(got) != 1 || got[0].Upstream != "staging" {
		t.Fatalf("got %+v", got)
	}
}

func TestDeclaredExtraIsARegistryLeaf(t *testing.T) {
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	got := DesiredEnvironments(chain, []contract.Environment{{Name: "staging"},
		{Name: "pr-412", Lifetime: contract.EnvironmentTemporary, Expires: "2026-11-20T00:00:00Z"}}, now)
	if len(got) != 2 || got[1] != (DesiredEnvironment{Name: "pr-412", Source: DesiredFromContract}) {
		t.Fatalf("got %+v", got)
	}
}

func TestExpiredTemporaryIsRetiring(t *testing.T) {
	envs := []contract.Environment{{Name: "pr-412", Lifetime: contract.EnvironmentTemporary, Expires: "2026-11-20T00:00:00+01:00"},
		{Name: "staging", Expires: "2026-11-20T00:00:00Z"}}
	got := DesiredEnvironments(chain, envs, time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC))
	if got[0].RetireFrom != "2026-11-19T23:00:00Z" || got[1].RetireFrom != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestDesiredSourceLabels(t *testing.T) {
	got := DesiredEnvironments(chain, nil, time.Now())
	if got[0].Source != "ladder" || got[2].Source != "inferred-confirmed" {
		t.Fatalf("got %+v", got)
	}
	if got := DesiredEnvironments(chain, []contract.Environment{{Name: "preview"}}, time.Now()); got[0].Source != "declared" {
		t.Fatalf("got %+v", got)
	}
}
