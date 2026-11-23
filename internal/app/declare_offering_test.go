package app

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// offeringFleet is a fleet whose postgresql offering has one provider, pg,
// owned by team, plus the bundles of extra.
func offeringFleet(t *testing.T, team string, extra ...*contract.Bundle) *fleet.FleetSnapshot {
	t.Helper()
	pg := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "pg", Version: "1.0.0", Offering: "postgresql", Owner: contract.Owner{Team: team}}}
	revs := []fleet.RawRevision{{Bundle: &contract.Bundle{Contract: pg, FS: fstest.MapFS{}}, Digest: "sha256:pg"}}
	for i, b := range extra {
		revs = append(revs, fleet.RawRevision{Bundle: b, Digest: "sha256:extra" + string(rune('a'+i))})
	}
	snap, err := fleet.Build(t.Context(), fleet.BuildOptions{}, fleet.NewMemorySource("oci", "oci", &fleet.Collection{Revisions: revs}))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// offeringProvidersOf counts the declared providers of offering in snap.
func offeringProvidersOf(snap *fleet.FleetSnapshot, offering string) []string {
	var out []string
	for k, s := range snap.Services {
		if s.Offering == offering {
			out = append(out, string(k))
		}
	}
	return out
}

func TestClaimOfferingWritesIntentMetadata(t *testing.T) {
	e := newDeclareEnv(t)
	req := e.claimReq("team-platform")
	req.Offering, req.Fleet = "postgresql", offeringFleet(t, "team-platform")
	p, d := e.plan(t, req)
	c := d.bundle.Contract
	if c.Service.Offering != "" || c.Metadata[fleet.ExternalOfferingMetadataKey] != "postgresql" || p.State != contract.DeclarationDeclared {
		t.Fatalf("%s\n%+v", d.raw, p)
	}
	if !hasNote(p.Notes, "intends offering postgresql (1 provider, owner team-platform)") || hasNote(p.Notes, "draft") {
		t.Fatalf("%v", p.Notes)
	}
	if cli := req.command(""); !strings.Contains(cli, "--offering postgresql") {
		t.Fatal(cli)
	}
}

func TestClaimOfferingDoesNotListAsProvider(t *testing.T) {
	e := newDeclareEnv(t)
	e.withOwnerPolicy(t)
	e.login(personToken("admin@acme.com"))
	req := e.claimReq("team-platform")
	req.Offering, req.Fleet = "postgresql", offeringFleet(t, "team-platform")
	if res := e.declare(t, req); res.Draft || res.Tag != firstDeclaration {
		t.Fatalf("%+v", res)
	}
	b, _ := e.pulledStripe(t, firstDeclaration)
	snap := offeringFleet(t, "team-platform", b)
	if got := offeringProvidersOf(snap, "postgresql"); len(got) != 1 || got[0] != "pg" {
		t.Fatalf("providers %v", got)
	}
	if s := snap.Services["stripe"]; s == nil || s.Offering != "" {
		t.Fatalf("%+v", s)
	}
}

func TestClaimOfferingOtherOwnerIsDraft(t *testing.T) {
	e := newDeclareEnv(t)
	e.withOwnerPolicy(t)
	e.login(personToken("admin@acme.com"))
	req := e.claimReq("team-platform")
	req.Offering, req.Fleet = "postgresql", offeringFleet(t, "team-data")
	res := e.declare(t, req)
	if !res.Draft || res.Tag != "draft-1" ||
		!hasNote(res.Plan.Notes, "intends offering postgresql (1 provider, owner team-data): the claim is written as a draft, and a person among team-data members accepts it with pacto claim stripe --registry "+e.host+"/acme --from-draft <tag>") {
		t.Fatalf("%+v", res.Plan)
	}
	// Accepting it keeps the draft's offering, and is no draft again.
	acc := e.claimDraft("draft-1")
	acc.Fleet = req.Fleet
	got := e.declare(t, acc)
	if got.Draft || got.Tag != firstDeclaration {
		t.Fatalf("%+v", got.Plan)
	}
	if b, _ := e.pulledStripe(t, firstDeclaration); b.Contract.Metadata[fleet.ExternalOfferingMetadataKey] != "postgresql" {
		t.Fatalf("%+v", b.Contract.Metadata)
	}
	acc.Offering = "mysql"
	if _, _, err := e.svc.PlanDeclare(t.Context(), acc); refusal(t, err).Code != codeOfferingInvalid {
		t.Fatal(err)
	}
}

func TestClaimOfferingProposesNew(t *testing.T) {
	e := newDeclareEnv(t)
	req := e.claimReq("team-platform")
	req.Offering, req.Fleet = "postgresql-legacy", offeringFleet(t, "team-data")
	if p, _ := e.plan(t, req); !hasNote(p.Notes, "proposes offering postgresql-legacy (no provider yet)") || p.State != contract.DeclarationDeclared {
		t.Fatalf("%+v", p)
	}
	// With no fleet given, the registry's offerings are read; unread, the
	// providers are unknown, never none.
	req.Fleet = nil
	if p, _ := e.plan(t, req); !hasNote(p.Notes, "intends offering postgresql-legacy (its providers are unknown: the offerings under "+e.host+"/acme were not all read)") {
		t.Fatalf("%v", p.Notes)
	}
}

func TestClaimOfferingInvalidSlug(t *testing.T) {
	e := newDeclareEnv(t)
	req := e.claimReq("team-platform")
	req.Offering = "Postgres_SQL"
	_, _, err := e.svc.PlanDeclare(t.Context(), req)
	if de := refusal(t, err); de.Code != codeOfferingInvalid || !strings.Contains(de.CLI, "--offering Postgres_SQL") {
		t.Fatalf("%+v", de)
	}
}

func TestOfferingProvidersFleetError(t *testing.T) {
	pl := &declarePlanner{s: &Service{}, req: DeclareRequest{Registry: "not a registry"}}
	if n, owners, why := pl.offeringProviders(t.Context(), "postgresql"); n != 0 || owners != nil || why == "" {
		t.Fatal(n, owners, why)
	}
}

func TestOfferingNote(t *testing.T) {
	for _, tc := range []struct {
		n      int
		owners []string
		want   string
	}{
		{2, nil, "intends offering pg (2 providers, no verified owner)"},
		{3, []string{"a", "b"}, "intends offering pg (3 providers, owners a, b)"},
	} {
		if got := offeringNote("pg", tc.n, tc.owners, ""); got != tc.want {
			t.Errorf("%q, want %q", got, tc.want)
		}
	}
}
