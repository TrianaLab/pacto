package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
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
	others := slices.DeleteFunc(owners, func(o string) bool { return o == owner })
	switch {
	case len(others) == 0 && why == "":
	case pl.req.Dir == "":
		// Joining another team's offering is that team's call: a draft. An
		// owner set not fully read may hold another team, so it is one too.
		pl.req.Signing.Draft = true
		note += ": the claim is written as a draft, and a person among " + offeringAccepters(others, why) + " accepts it with " + pl.req.AcceptCLI("", "<tag>")
	default:
		// Accepting the draft: the person must be one of those teams.
		policy, from := pl.ownerPolicy(ctx)
		pl.joins = &offeringJoin{offering: slug, teams: others, unknown: why, policy: policy, from: from}
		note += ": only a person among " + offeringAccepters(others, why) + " may accept it"
	}
	pl.plan.Notes = append(pl.plan.Notes, note)
	return slug, nil
}

// offeringAccepters names who accepts joining an offering: the members of
// the teams that provide it, or of its owners once they are all read.
func offeringAccepters(others []string, why string) string {
	if why != "" {
		return "the offering's owners (once they are read)"
	}
	return strings.Join(others, ", ") + " members"
}

// offeringJoin is the offering an accepted claim draft joins and the teams
// that provide it: the person who accepts must be a member of one of them
// under the owner policy (nil, and why, when none was read). unknown says
// why the teams were not all read, so nobody is known to be one.
type offeringJoin struct {
	offering string
	teams    []string
	unknown  string
	policy   *delivery.OwnerPolicy
	from     string
}

// refusal refuses signer accepting j unless they are a member of one of
// j's teams: a default approver is no member, and the owner rule judges
// the claim's own owner apart.
func (j *offeringJoin) refusal(service string, signer delivery.PublisherIdentity, now time.Time) error {
	if j == nil {
		return nil
	}
	if j.unknown != "" {
		return &DeclareError{Code: delivery.OwnerSignerNotAllowed, Message: "who may accept joining offering " + j.offering + " is unknown: " + j.unknown}
	}
	for _, t := range j.teams {
		member := delivery.OwnerClaim{Owner: contract.Owner{Team: t}, Provenance: fleet.OwnerProvenanceDeclared}
		if delivery.DecideOwnerRight(service, signer, member, delivery.OwnerClaim{}, j.policy, j.from, now).Grant == fleet.OwnerProvenanceDeclared+"-owner" {
			return nil
		}
	}
	msg := signer.Subject + " is no member of " + strings.Join(j.teams, ", ") + ", which provide offering " + j.offering + ": a person among them accepts this draft"
	if j.policy == nil {
		return &DeclareError{Code: delivery.OwnerPolicyUnverified, Message: msg + " (no verified owner policy: " + j.from + ")"}
	}
	return &DeclareError{Code: delivery.OwnerSignerNotAllowed, Message: msg}
}

// offeringProviders counts the declared providers of offering in the fleet
// and their owner teams, sorted; why is set when the fleet was not fully
// read, so more providers and owners may exist than were found.
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
	if snap.Completeness != fleet.CompletenessComplete {
		why = "the offerings under " + pl.req.Registry + " were not all read"
	}
	return providers, owners, why
}

// offeringNote is the plan's line about the offering a claim names.
func offeringNote(offering string, providers int, owners []string, why string) string {
	switch {
	case why != "" && providers == 0:
		return "intends offering " + offering + " (its providers are unknown: " + why + ")"
	case why != "":
		return fmt.Sprintf("intends offering %s (at least %d %s, %s; %s)", offering, providers, providerWord(providers), ownersText(owners), why)
	case providers == 0:
		return "proposes offering " + offering + " (no provider yet)"
	}
	return fmt.Sprintf("intends offering %s (%d %s, %s)", offering, providers, providerWord(providers), ownersText(owners))
}

// providerWord is provider or providers for n.
func providerWord(n int) string {
	if n == 1 {
		return "provider"
	}
	return "providers"
}

// ownersText names the owner teams an offering note lists.
func ownersText(owners []string) string {
	switch len(owners) {
	case 0:
		return "no verified owner"
	case 1:
		return "owner " + owners[0]
	}
	return "owners " + strings.Join(owners, ", ")
}
