package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// claimOffering is the offering a claim names as intent (Decision 11): the
// request's, or with req.Dir the draft's, which a different --offering may
// not change. It adds the plan note that says what joining it means, and a
// claim of an offering other teams provide is written as a draft a person
// of those teams accepts. An empty offering names none.
func (pl *declarePlanner) claimOffering(ctx context.Context, owner, drafted string) (string, error) {
	slug := pl.req.Offering
	if pl.req.Dir != "" {
		if slug != "" && slug != drafted {
			return "", &DeclareError{Code: codeOfferingInvalid, Message: "the draft names offering " + cmp.Or(drafted, "none") + ", not " + slug +
				": accept it without --offering, or claim again with the offering you want"}
		}
		slug = drafted
	}
	if slug == "" {
		return "", nil
	}
	if !contract.ValidSlug(slug) {
		return "", &DeclareError{Code: codeOfferingInvalid, CLI: pl.req.command(""),
			Message: fmt.Sprintf("%q is not an offering: use lower-case letters, digits and dashes, at most 63, as in service.offering", slug)}
	}
	providers, owners, why := pl.offeringProviders(ctx, slug)
	note := offeringNote(slug, providers, owners, why)
	if others := slices.DeleteFunc(owners, func(o string) bool { return o == owner }); len(others) > 0 && pl.req.Dir == "" {
		// Joining another team's offering is that team's call: a draft.
		pl.req.Signing.Draft = true
		note += ": the claim is written as a draft, and a person among " + strings.Join(others, ", ") + " members accepts it with " + pl.req.AcceptCLI("", "<tag>")
	}
	pl.plan.Notes = append(pl.plan.Notes, note)
	return slug, nil
}

// offeringProviders counts the declared providers of offering in the fleet
// and their owner teams, sorted; why is set when the fleet was not fully
// read, so no provider found is unknown.
func (pl *declarePlanner) offeringProviders(ctx context.Context, offering string) (providers int, owners []string, why string) {
	snap := pl.req.Fleet
	if snap == nil {
		var err error
		if snap, err = pl.s.Fleet(ctx, FleetOptions{Discover: []string{pl.req.Registry}}); err != nil {
			return 0, nil, err.Error()
		}
	}
	for _, s := range snap.Services {
		if s.Offering != offering || s.Inferred() {
			continue
		}
		providers++
		if t := s.Owner.Team; t != "" && !slices.Contains(owners, t) {
			owners = append(owners, t)
		}
	}
	slices.Sort(owners)
	if snap.Completeness != fleet.CompletenessComplete && providers == 0 {
		why = "the offerings under " + pl.req.Registry + " were not all read"
	}
	return providers, owners, why
}

// offeringNote is the plan's line about the offering a claim names.
func offeringNote(offering string, providers int, owners []string, why string) string {
	switch {
	case why != "":
		return "intends offering " + offering + " (its providers are unknown: " + why + ")"
	case providers == 0:
		return "proposes offering " + offering + " (no provider yet)"
	}
	who := "no verified owner"
	if len(owners) == 1 {
		who = "owner " + owners[0]
	} else if len(owners) > 1 {
		who = "owners " + strings.Join(owners, ", ")
	}
	word := "providers"
	if providers == 1 {
		word = "provider"
	}
	return fmt.Sprintf("intends offering %s (%d %s, %s)", offering, providers, word, who)
}
