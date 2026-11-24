package fleet

import (
	"fmt"
	"slices"
	"strings"
)

// ConsumableOffering is what an offering row of a consumable list carries:
// the providers a caller may consume, newest judged revision first.
type ConsumableOffering struct {
	Providers          []ConsumableProvider `json:"providers" doc:"The providers of this offering a caller may consume, at most 10, by service key"`
	ProvidersTotal     int                  `json:"providersTotal" doc:"How many providers of this offering a caller may consume"`
	ProvidersTruncated bool                 `json:"providersTruncated" doc:"More providers may be consumed than providers lists"`
}

// ConsumableProvider is one provider a caller may consume, judged at its
// newest revision: who accepted it and how to reference it.
type ConsumableProvider struct {
	Service    EntityRef `json:"service"`
	Revision   EntityRef `json:"revision" doc:"The newest revision of the provider, the one acceptance judged and the snippet pins"`
	Version    string    `json:"version"`
	Digest     string    `json:"digest" doc:"The bundle digest acceptance covers; run pacto lock to pin it"`
	Acceptance string    `json:"acceptance" enum:"declaration,publisher" doc:"How the revision is accepted: a person's verified declaration the installation signer policy allows, or a verified signer an installation publisher rule, pin, policy or the release signer covers"`
	Publisher  string    `json:"publisher" doc:"Who accepted it: the declaring person or the verified signer"`
	Lifecycle  string    `json:"lifecycle" enum:"supported,deprecated"`
	// Snippet is the provider's consume snippet (see [ConsumeSnippet]).
	Snippet *ConsumeSnippet `json:"snippet"`
}

// Why a provider of an offering is left out of a consumable list, in the
// order [Query.consumableProvider] checks them: the first reason wins.
const (
	ConsumableExcludedObservedOnly      = "observed-only"
	ConsumableExcludedSunset            = "sunset"
	ConsumableExcludedNotPublished      = "not-published"
	ConsumableExcludedNotAccepted       = "not-accepted"
	ConsumableExcludedAcceptanceUnknown = "acceptance-unknown"
)

// MaxConsumableProviders bounds ConsumableOffering.Providers.
const MaxConsumableProviders = 10

// Acceptance values (see [ConsumableProvider.Acceptance]).
const (
	AcceptanceDeclaration = "declaration"
	AcceptancePublisher   = "publisher"
)

// publisherSources are the signature sources whose verified signer makes a
// publisher: an installation rule, a person's pin, the signed policy and the
// Pacto release signer. An inferred rule never verifies, and the operator
// signs only inferred contracts.
var publisherSources = []string{"rule", "pin", "policy", "release"}

// acceptance is how rev is accepted and by whom, or, with reason
// not-accepted or acceptance-unknown, why it is not. A declaration revision
// is judged by its declarer in the order of [declarationRejected]; any other
// revision by its verified signer. Unknown is never positive: a revision no
// source checked is acceptance-unknown, never accepted.
func acceptance(rev *ContractRevision) (how, publisher, reason, why string) {
	if rev.Record != nil && rev.Record.Declaration != nil {
		return declarationAcceptance(rev)
	}
	p := rev.Provenance
	switch {
	case p == nil:
		return "", "", ConsumableExcludedAcceptanceUnknown, noSignatureCheck
	case p.Signature.Status == SignatureUnknown && p.Signature.Source == "inferred":
		return "", "", ConsumableExcludedAcceptanceUnknown, "no installation publisher rule was read"
	case p.Signature.Status == SignatureUnknown:
		return "", "", ConsumableExcludedAcceptanceUnknown, "its signature could not be checked"
	case p.Signature.Status == SignatureVerified && slices.Contains(publisherSources, p.Signature.Source):
		return AcceptancePublisher, p.Signature.Signer, "", ""
	case p.Signature.Source == SignatureSourceOperator:
		return "", "", ConsumableExcludedNotAccepted, "only this installation's operator signed it"
	case p.Signature.Status == SignatureVerified:
		return "", "", ConsumableExcludedNotAccepted, "no publisher rule, pin, policy or release signer covers its signer"
	}
	return "", "", ConsumableExcludedNotAccepted, "its signature is " + p.Signature.Status
}

// noSignatureCheck is why a revision no source checked signatures of is
// unknown, the text [declarationRejected] gives.
const noSignatureCheck = "this source checks no signatures (a --local fleet or the Kubernetes source)"

// declarationAcceptance is [acceptance] of a declaration revision: an
// absent or unread signer policy is unknown, a refused declarer is not
// accepted.
func declarationAcceptance(rev *ContractRevision) (how, publisher, reason, why string) {
	why, _ = declarationRejected(rev)
	switch {
	case rev.Provenance == nil:
		return "", "", ConsumableExcludedAcceptanceUnknown, why
	case rev.Provenance.Signature.Status == SignatureUnknown:
		return "", "", ConsumableExcludedAcceptanceUnknown, "its signatures could not be checked"
	case why == "":
		return AcceptanceDeclaration, rev.Record.Declaration.Asker.Subject, "", ""
	case rev.declarationPolicyUnknown:
		return "", "", ConsumableExcludedAcceptanceUnknown, why
	}
	return "", "", ConsumableExcludedNotAccepted, why
}

// consumableProvider judges s, a visible, declared provider of an offering,
// at its newest revision, and returns it filled when a caller may consume
// it, else the reason it is left out and, for some reasons, why.
//
// ponytail: the newest revision speaks; an older accepted version is not
// offered, since a snippet for it carries a caret range the unjudged newest
// satisfies. Judge each version and offer the newest accepted one if the
// provisioning gate needs it.
func (q *Query) consumableProvider(s *ServiceRecord) (ConsumableProvider, string, string) {
	var newest *ContractRevision
	for _, r := range q.revisionsOf(s) {
		if newest == nil || LessRevisionChrono(newest, r) {
			newest = r
		}
	}
	if newest == nil || !readableRevision(newest) {
		return ConsumableProvider{}, ConsumableExcludedAcceptanceUnknown, "its newest revision could not be read"
	}
	if s.OfferingProvenance != ProvenanceDeclared {
		return ConsumableProvider{}, ConsumableExcludedObservedOnly, ""
	}
	lifecycle := revisionLifecycle(newest, q.snap.GeneratedAt)
	if lifecycle == LifecycleSunset {
		return ConsumableProvider{}, ConsumableExcludedSunset, ""
	}
	snippet := consumeSnippet(q.snap, s, newest)
	if snippet == nil {
		return ConsumableProvider{}, ConsumableExcludedNotPublished, ""
	}
	how, publisher, reason, why := acceptance(newest)
	if reason != "" {
		return ConsumableProvider{}, reason, why
	}
	return ConsumableProvider{
		Service: serviceEntityRef(s), Revision: revisionEntityRef(newest), Version: newest.Version, Digest: newest.Digest,
		Acceptance: how, Publisher: publisher, Lifecycle: lifecycle, Snippet: snippet,
	}, "", ""
}

// consumableRows keeps the offering rows with a provider a caller may
// consume, each carrying its providers, and returns the limitations that
// count every provider left out by reason. Hidden providers never reach it:
// the restricted view removed them, and the row counts them.
func (q *Query) consumableRows(rows []EntityRef, facts map[string]*offeringFacts) ([]EntityRef, []Limitation) {
	kept := rows[:0:0]
	byReason := map[string][]string{}
	for _, r := range rows {
		var providers []ConsumableProvider
		for _, s := range facts[r.Key].providers {
			p, reason, why := q.consumableProvider(s)
			if reason != "" {
				byReason[reason] = append(byReason[reason], exclusionEntry(r.Key, why))
				continue
			}
			providers = append(providers, p)
		}
		if len(providers) == 0 {
			continue
		}
		slices.SortFunc(providers, func(a, b ConsumableProvider) int { return strings.Compare(a.Service.Key, b.Service.Key) })
		shown, total, truncated := boundSlice(providers, MaxConsumableProviders)
		r.Consumable = &ConsumableOffering{Providers: shown, ProvidersTotal: total, ProvidersTruncated: truncated}
		kept = append(kept, r)
	}
	return kept, consumableLimitations(byReason)
}

// exclusionEntry is how a limitation names an offering whose provider is
// left out, with why when there is more to say than the reason.
func exclusionEntry(offering, why string) string {
	if why == "" {
		return offering
	}
	return offering + ": " + why
}

// consumableExclusions says, per reason a provider is left out, why in
// words and what a person does about it, in the order the limitations list
// them.
var consumableExclusions = []struct{ reason, why, fix string }{
	{ConsumableExcludedObservedOnly, "only targets report the offering", "the provider declares service.offering in its contract and publishes it"},
	{ConsumableExcludedSunset, "past the sunset date", "consume the successor"},
	{ConsumableExcludedNotPublished, "no registry repository to reference", "publish the provider to a registry"},
	{ConsumableExcludedNotAccepted, "not accepted", "a person declares the offering with a verified signature the installation signer policy allows, or an installation publisher rule covers its signer"},
}

// maxConsumableNames bounds the offerings a consumable limitation names.
const maxConsumableNames = 3

// consumableLimitations is one OFFERING_NOT_CONSUMABLE limitation per reason
// a known exclusion has and one OFFERING_ACCEPTANCE_UNKNOWN for the
// providers Pacto could not judge, each counting the providers and naming
// up to three offerings.
func consumableLimitations(byReason map[string][]string) []Limitation {
	var lims []Limitation
	for _, e := range consumableExclusions {
		if names := byReason[e.reason]; len(names) > 0 {
			lims = append(lims, Limitation{Code: LimitationOfferingNotConsumable,
				Message: fmt.Sprintf("Not consumable, %s: %s (%s); %s", e.why, providersOfOfferings(len(names)), someNames(names), e.fix)})
		}
	}
	if names := byReason[ConsumableExcludedAcceptanceUnknown]; len(names) > 0 {
		lims = append(lims, Limitation{Code: LimitationOfferingAcceptanceUnknown,
			Message: fmt.Sprintf("Acceptance unknown: %s (%s), so a missing offering means unknown; read the offerings from a registry source that checks signatures",
				providersOfOfferings(len(names)), someNames(names))})
	}
	return lims
}

// providersOfOfferings counts n providers of offerings a caller may see.
func providersOfOfferings(n int) string {
	if n == 1 {
		return "1 provider of an offering you may see"
	}
	return fmt.Sprintf("%d providers of offerings you may see", n)
}

// someNames is up to maxConsumableNames of names, sorted, and how many more.
func someNames(names []string) string {
	names = slices.Sorted(slices.Values(names))
	shown, total, _ := boundSlice(names, maxConsumableNames)
	s := strings.Join(shown, "; ")
	if total > len(shown) {
		s += fmt.Sprintf("; %d more", total-len(shown))
	}
	return s
}

// validateConsumable refuses a consumable filter that asks for anything but
// declared offerings: it keeps offering rows only, so any other kind set,
// the empty one included, would answer rows it never judged.
func validateConsumable(f EntityFilter) error {
	switch {
	case !f.Consumable:
		return nil
	case len(f.Kinds) != 1 || f.Kinds[0] != KindOffering:
		return &InvalidQueryError{Field: "consumable", Value: "true", Reason: "consumable keeps offerings; ask kind offering"}
	case f.Declaration == DeclarationInferred:
		return &InvalidQueryError{Field: "consumable", Value: "true", Reason: "--consumable lists accepted offerings and --inferred lists inferred ones; ask for one"}
	}
	return nil
}
