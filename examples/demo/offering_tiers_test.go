package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

const offeringTiers = "testdata/offering-tiers/"

// tierResolver resolves the one ref the demo consumer takes, to the tiered
// postgresql offering kept beside it.
type tierResolver struct{ t *testing.T }

func (r tierResolver) ResolveBundle(_ context.Context, ref string) (*contract.Bundle, error) {
	if ref != "oci://registry.example/platform/postgresql:16.2.0" {
		return nil, fmt.Errorf("no bundle %s", ref)
	}
	c, _ := readContract(r.t, offeringTiers+"postgresql")
	return &contract.Bundle{Contract: c, FS: os.DirFS(offeringTiers + "postgresql")}, nil
}

func readContract(t *testing.T, dir string) (*contract.Contract, []byte) {
	t.Helper()
	raw, err := os.ReadFile(dir + "/pacto.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := contract.Parse(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return c, raw
}

// TestDemoOfferingTierRefusals: the demo's orders-db-demo, kept outside the
// published bundles, sets per-environment values the tiered postgresql
// offering does not allow, and pacto validate refuses each with its stable
// code and a next step.
func TestDemoOfferingTierRefusals(t *testing.T) {
	dir := offeringTiers + "orders-db-demo"
	c, raw := readContract(t, dir)
	res := validation.ValidateWithResolver(t.Context(), c, raw, os.DirFS(dir), tierResolver{t})
	var got []string
	for _, e := range res.Errors {
		got = append(got, fmt.Sprintf("%s %s:%d %s", e.Code, e.File, e.Line, e.Message))
	}
	slices.Sort(got)
	want := []string{
		`ClassNotOffered environments/staging.yaml:3 environment staging: database.class names a class offering postgresql 16.2.0 does not offer. Next: use one of small, medium, large (did you mean "large"?)`,
		`ParameterNotDeclared environments/production.yaml:3 environment production: database.replica is not a parameter of offering postgresql 16.2.0. Next: remove it (did you mean "replicas"?); pacto fleet get postgresql lists the parameters`,
		"ParameterOutOfRange environments/staging.yaml:2 environment staging: database.replicas is outside the parameters of offering postgresql 16.2.0. Next: use a value of type integer, >= 1, <= 5; pacto fleet get postgresql lists the parameters",
		"TierNotTunable environments/production.yaml:2 environment production: database.storageGB is contract-tier in offering postgresql 16.2.0, so it cannot change per environment. Next: set it once in configurations[0].values, or ask infra-data to mark it x-pacto-tier: tunable",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	// The offering itself is a valid contract.
	pg, raw := readContract(t, offeringTiers+"postgresql")
	if res := validation.Validate(pg, raw, os.DirFS(offeringTiers+"postgresql")); !res.IsValid() {
		t.Fatalf("postgresql: %+v", res.Errors)
	}
}
