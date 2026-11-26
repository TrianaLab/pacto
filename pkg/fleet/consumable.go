package fleet

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/schemax"
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
	// Classes is the enum of the class property of a required configuration,
	// in schema order; empty when no schema declares one.
	Classes             []string              `json:"classes" doc:"The classes the offering provisions: the enum of the class property of a required configuration, at most 50"`
	ClassesTotal        int                   `json:"classesTotal" doc:"How many classes the class property allows"`
	ClassesTruncated    bool                  `json:"classesTruncated" doc:"The class property allows more classes than classes lists"`
	Parameters          []OfferingParameter   `json:"parameters" doc:"The fields of the required configurations' closed schemas, at most 50, by configuration then path"`
	ParametersTotal     int                   `json:"parametersTotal"`
	ParametersTruncated bool                  `json:"parametersTruncated"`
	SchemaPaths         []string              `json:"schemaPaths" doc:"The schema files the parameters come from, in the bundle that declares each"`
	Guarantees          []ConsumableGuarantee `json:"guarantees" doc:"The interfaces of the revision that declare guarantees; empty when it declares none"`
}

// OfferingParameter is one field a consumer sets in a required configuration
// of a consumable provider. A sensitive field carries no default and no enum.
type OfferingParameter struct {
	Configuration    string   `json:"configuration" doc:"The provider's configuration the field belongs to"`
	Path             string   `json:"path" doc:"The field's path in dot notation"`
	Type             string   `json:"type,omitempty"`
	Tier             string   `json:"tier" enum:"contract,tunable" doc:"contract: set once, the same in every environment; tunable: may change per environment within the bounds"`
	Required         bool     `json:"required"`
	Enum             []any    `json:"enum,omitempty" doc:"The allowed values, at most 50"`
	EnumTotal        int      `json:"enumTotal,omitempty" doc:"How many values the schema allows"`
	Minimum          *float64 `json:"minimum,omitempty"`
	Maximum          *float64 `json:"maximum,omitempty"`
	ExclusiveMinimum *float64 `json:"exclusiveMinimum,omitempty"`
	ExclusiveMaximum *float64 `json:"exclusiveMaximum,omitempty"`
	MinLength        *int     `json:"minLength,omitempty"`
	MaxLength        *int     `json:"maxLength,omitempty"`
	Pattern          string   `json:"pattern,omitempty"`
	Default          any      `json:"default,omitempty"`
	Sensitive        bool     `json:"sensitive" doc:"The field is writeOnly, holds a writeOnly field or sits under a writeOnly object"`
}

// ConsumableGuarantee is what one interface of a consumable provider
// promises, and the text a consumer's dependencies[].requires names.
type ConsumableGuarantee struct {
	Interface  string              `json:"interface"`
	Guarantees contract.Guarantees `json:"guarantees"`
	Text       string              `json:"text"`
}

// Why a provider of an offering is left out of a consumable list, in the
// order [Query.consumableProvider] checks them: the first reason wins.
const (
	ConsumableExcludedObservedOnly      = "observed-only"
	ConsumableExcludedSunset            = "sunset"
	ConsumableExcludedNotPublished      = "not-published"
	ConsumableExcludedNothingToConsume  = "nothing-to-consume"
	ConsumableExcludedNotAccepted       = "not-accepted"
	ConsumableExcludedAcceptanceUnknown = "acceptance-unknown"
	ConsumableExcludedSchemaUnreadable  = "schema-unreadable"
	ConsumableExcludedSchemaOpen        = "schema-open"
	ConsumableExcludedSchemaComposed    = "schema-composed"
)

// Bounds of a consumable list.
const (
	MaxConsumableProviders  = 10 // ConsumableOffering.Providers
	MaxConsumableParameters = 50 // ConsumableProvider.Parameters
	MaxParameterEnum        = 50 // OfferingParameter.Enum
)

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

// exclusion is why a provider is left out of a consumable list and, for
// some reasons, more words and the fix of an acceptance-unknown cause.
type exclusion struct{ reason, why, fix string }

// Fixes of the acceptance-unknown causes.
const (
	fixSignatureSource = "read the offerings from a registry source that checks signatures"
	fixSignatureAgain  = "read the offerings again once the registry source can check the signature"
	fixPublisherRule   = "add an installation publisher rule for this repository"
	fixValidRevision   = "publish a valid revision"
	fixWholeView       = "ask with a view that may read every revision of the provider"
)

// acceptance is how rev is accepted and by whom, or, with reason
// not-accepted or acceptance-unknown, why it is not. A declaration revision
// is judged by its declarer in the order of [declarationRejected]; any other
// revision by its verified signer. Unknown is never positive: a revision no
// source checked is acceptance-unknown, never accepted.
func acceptance(rev *ContractRevision) (how, publisher string, ex exclusion) {
	if rev.Record != nil && rev.Record.Declaration != nil {
		return declarationAcceptance(rev)
	}
	p := rev.Provenance
	switch {
	case p == nil:
		return "", "", exclusion{ConsumableExcludedAcceptanceUnknown, noSignatureCheck, fixSignatureSource}
	case p.Signature.Status == SignatureUnknown && p.Signature.Source == "inferred":
		return "", "", exclusion{ConsumableExcludedAcceptanceUnknown, "no installation publisher rule was read", fixPublisherRule}
	case p.Signature.Status == SignatureUnknown:
		return "", "", exclusion{ConsumableExcludedAcceptanceUnknown, "its signature could not be checked", fixSignatureAgain}
	case p.Signature.Status == SignatureVerified && slices.Contains(publisherSources, p.Signature.Source):
		return AcceptancePublisher, p.Signature.Signer, exclusion{}
	case p.Signature.Source == SignatureSourceOperator:
		return "", "", exclusion{reason: ConsumableExcludedNotAccepted, why: "only this installation's operator signed it"}
	case p.Signature.Status == SignatureVerified:
		return "", "", exclusion{reason: ConsumableExcludedNotAccepted, why: "no publisher rule, pin, policy or release signer covers its signer"}
	}
	return "", "", exclusion{reason: ConsumableExcludedNotAccepted, why: "its signature is " + p.Signature.Status}
}

// noSignatureCheck is why a revision no source checked signatures of is
// unknown, the text [declarationRejected] gives.
const noSignatureCheck = "this source checks no signatures (a --local fleet or the Kubernetes source)"

// declarationAcceptance is [acceptance] of a declaration revision: an
// absent or unread signer policy is unknown, a refused declarer is not
// accepted.
func declarationAcceptance(rev *ContractRevision) (how, publisher string, ex exclusion) {
	why, fix := declarationRejected(rev)
	switch {
	case rev.Provenance == nil:
		return "", "", exclusion{ConsumableExcludedAcceptanceUnknown, why, fixSignatureSource}
	case rev.Provenance.Signature.Status == SignatureUnknown:
		return "", "", exclusion{ConsumableExcludedAcceptanceUnknown, "its signatures could not be checked", fixSignatureAgain}
	case why == "":
		return AcceptanceDeclaration, rev.Record.Declaration.Asker.Subject, exclusion{}
	case rev.declarationPolicyUnknown:
		return "", "", exclusion{ConsumableExcludedAcceptanceUnknown, why, fix}
	}
	return "", "", exclusion{reason: ConsumableExcludedNotAccepted, why: why}
}

// consumableProvider judges s, a visible, declared provider of an offering,
// at its newest revision, and returns it filled when a caller may consume
// it, else the reason it is left out and, for some reasons, why.
//
// ponytail: the newest revision speaks; an older accepted version is not
// offered, since a snippet for it carries a caret range the unjudged newest
// satisfies. Judge each version and offer the newest accepted one if the
// provisioning gate needs it.
func (q *Query) consumableProvider(s *ServiceRecord) (ConsumableProvider, exclusion) {
	var newest *ContractRevision
	for _, r := range q.revisionsOf(s) {
		if newest == nil || LessRevisionChrono(newest, r) {
			newest = r
		}
	}
	// A provider with no revision at all is known: no contract declares the
	// offering, only targets report it.
	if newest == nil && s.OfferingProvenance != ProvenanceDeclared {
		return ConsumableProvider{}, exclusion{reason: ConsumableExcludedObservedOnly}
	}
	// A restricted view that dropped some revisions may have dropped the
	// newest: the newest this view holds is never the one judged.
	if q.snap.revisionsHidden[s.Key] {
		return ConsumableProvider{}, exclusion{ConsumableExcludedAcceptanceUnknown, "some of its revisions are outside your view", fixWholeView}
	}
	if ex := unreadableNewest(newest); ex.reason != "" {
		return ConsumableProvider{}, ex
	}
	if s.OfferingProvenance != ProvenanceDeclared {
		return ConsumableProvider{}, exclusion{reason: ConsumableExcludedObservedOnly}
	}
	lifecycle := revisionLifecycle(newest, q.snap.GeneratedAt)
	if lifecycle == LifecycleSunset {
		return ConsumableProvider{}, exclusion{reason: ConsumableExcludedSunset}
	}
	if consumeRepo(q.snap, s, newest) == "" {
		return ConsumableProvider{}, exclusion{reason: ConsumableExcludedNotPublished}
	}
	snippet := consumeSnippet(q.snap, s, newest)
	if snippet == nil {
		return ConsumableProvider{}, exclusion{reason: ConsumableExcludedNothingToConsume}
	}
	how, publisher, ex := acceptance(newest)
	if ex.reason != "" {
		return ConsumableProvider{}, ex
	}
	p := ConsumableProvider{
		Service: serviceEntityRef(s), Revision: revisionEntityRef(newest), Version: newest.Version, Digest: newest.Digest,
		Acceptance: how, Publisher: publisher, Lifecycle: lifecycle, Snippet: snippet, Guarantees: guaranteesOf(newest.Contract),
	}
	if ex := q.parameterSpace(&p, newest); ex.reason != "" {
		return ConsumableProvider{}, ex
	}
	return p, exclusion{}
}

// OfferingSchema is the parsed schema of configuration, a required
// configuration of the revision revisionKey, read from the bundle the
// snapshot keeps (a ref'd one from the revision its edge resolves to): the
// whole closed parameter space a value is checked against, which
// Parameters bounds and masks. It answers only for the revision a caller may
// consume, the newest of a consumable provider in this query's view; any
// other revision, hidden or absent, is the same NotFoundError.
func (q *Query) OfferingSchema(revisionKey, configuration string) (map[string]any, error) {
	missing := &NotFoundError{Kind: "consumable offering schema", ID: revisionKey + "#" + configuration}
	rev := q.snap.Revisions[RevisionKey(revisionKey)]
	if rev == nil || rev.Contract == nil {
		return nil, missing
	}
	s := q.snap.Services[rev.ServiceKey]
	if s == nil {
		return nil, missing
	}
	if p, ex := q.consumableProvider(s); ex.reason != "" || p.Revision.Key != revisionKey {
		return nil, missing
	}
	i := slices.IndexFunc(rev.Contract.Configurations, func(c contract.Configuration) bool { return c.Name == configuration && c.Required })
	if i < 0 {
		return nil, missing
	}
	from, path := rev, rev.Contract.Configurations[i].Schema
	if rev.Contract.Configurations[i].Ref != "" {
		from, path = refSchemaFile(q.snap, rev, configuration)
	}
	// ponytail: consumableProvider read and parsed this schema just now.
	doc, _ := schemaDoc(from, path)
	return doc, nil
}

// unreadableNewest is the acceptance-unknown exclusion of a newest revision
// that is missing, could not be read or failed validation.
func unreadableNewest(newest *ContractRevision) exclusion {
	switch {
	case newest == nil || newest.Contract == nil:
		return exclusion{ConsumableExcludedAcceptanceUnknown, "its newest revision could not be read", fixValidRevision}
	case !readableRevision(newest):
		return exclusion{ConsumableExcludedAcceptanceUnknown, "its newest revision is invalid", fixValidRevision}
	}
	return exclusion{}
}

// parameterSpace fills p's classes, parameters and schema paths from the
// schemas of rev's required configurations (a ref'd one read from the
// revision its edge resolves to), or says why it cannot: schema-unreadable
// when one cannot be read or parsed, else schema-open naming up to three
// open paths of the first open one, or schema-composed naming the paths
// Parameters cannot list.
func (q *Query) parameterSpace(p *ConsumableProvider, rev *ContractRevision) exclusion {
	type schema struct {
		cfg, path string
		doc       map[string]any
	}
	var schemas []schema
	for _, cfg := range rev.Contract.Configurations {
		if !cfg.Required {
			continue
		}
		from, path := rev, cfg.Schema
		if cfg.Ref != "" {
			from, path = refSchemaFile(q.snap, rev, cfg.Name)
		}
		doc, why := schemaDoc(from, path)
		if why != "" {
			return exclusion{reason: ConsumableExcludedSchemaUnreadable, why: cfg.Name + ": " + why}
		}
		schemas = append(schemas, schema{cfg.Name, path, doc})
	}
	p.Classes, p.SchemaPaths = []string{}, []string{}
	var params []OfferingParameter
	for _, sc := range schemas {
		if open := schemax.OpenPaths(sc.doc); len(open) > 0 {
			return exclusion{reason: ConsumableExcludedSchemaOpen, why: sc.cfg + ": " + someNamesOf(open, ", ")}
		}
		// A closed space Parameters cannot list is never offered as a
		// shorter one.
		if composed := schemax.Composed(sc.doc); len(composed) > 0 {
			return exclusion{reason: ConsumableExcludedSchemaComposed, why: sc.cfg + ": " + someNamesOf(rootNamed(composed), ", ")}
		}
		p.SchemaPaths = append(p.SchemaPaths, sc.path)
		for _, sp := range schemax.Parameters(sc.doc) {
			params = append(params, offeringParameter(sc.cfg, sp))
			if sp.Path == schemax.ClassProperty && p.ClassesTotal == 0 {
				p.Classes, p.ClassesTotal, p.ClassesTruncated = classesOf(sp.Enum)
			}
		}
	}
	p.Parameters, p.ParametersTotal, p.ParametersTruncated = boundSlice(params, MaxConsumableParameters)
	return exclusion{}
}

// rootNamed is paths with the root's empty path named.
func rootNamed(paths []string) []string {
	out := slices.Clone(paths)
	for i, p := range out {
		if p == "" {
			out[i] = "(root)"
		}
	}
	return out
}

// classesOf is the class property's enum as text, bounded like any enum.
func classesOf(enum []any) ([]string, int, bool) {
	shown, total, truncated := boundSlice(enum, MaxParameterEnum)
	classes := make([]string, len(shown))
	for i, c := range shown {
		classes[i] = fmt.Sprint(c)
	}
	return classes, total, truncated
}

// schemaDoc is the parsed schema at path in from's bundle, or why it cannot
// be read; from is nil when a configuration reference does not resolve.
func schemaDoc(from *ContractRevision, path string) (map[string]any, string) {
	if from == nil {
		return nil, "its configuration reference does not resolve"
	}
	data, why := readBundleFile(from, path, maxSchemaBytes)
	if why != "" {
		return nil, why
	}
	doc, err := schemax.Parse(data, path)
	if err != nil {
		return nil, "the schema file could not be parsed: " + err.Error()
	}
	return doc, ""
}

// offeringParameter is sp, a field of configuration cfg, with its enum
// bounded.
func offeringParameter(cfg string, sp schemax.Parameter) OfferingParameter {
	enum, total, _ := boundSlice(sp.Enum, MaxParameterEnum)
	return OfferingParameter{
		Configuration: cfg, Path: sp.Path, Type: sp.Type, Tier: sp.Tier, Required: sp.Required,
		Enum: enum, EnumTotal: total, Minimum: sp.Minimum, Maximum: sp.Maximum,
		ExclusiveMinimum: sp.ExclusiveMinimum, ExclusiveMaximum: sp.ExclusiveMaximum,
		MinLength: sp.MinLength, MaxLength: sp.MaxLength, Pattern: sp.Pattern, Default: sp.Default, Sensitive: sp.Sensitive,
	}
}

// guaranteesOf is what each interface of c that declares guarantees
// promises, in declaration order; empty when none does.
func guaranteesOf(c *contract.Contract) []ConsumableGuarantee {
	out := []ConsumableGuarantee{}
	for _, i := range c.Interfaces {
		if i.Guarantees != nil {
			out = append(out, ConsumableGuarantee{Interface: i.Name, Guarantees: *i.Guarantees, Text: i.Guarantees.Text(false)})
		}
	}
	return out
}

// consumableRows keeps the offering rows with a provider a caller may
// consume, each carrying its providers, and returns the limitations that
// count every provider left out by reason. Hidden providers never reach it:
// the restricted view removed them, and the row counts them.
func (q *Query) consumableRows(rows []EntityRef, facts map[string]*offeringFacts) ([]EntityRef, []Limitation) {
	kept := rows[:0:0]
	byReason, unknownByFix := map[string][]string{}, map[string][]string{}
	for _, r := range rows {
		var providers []ConsumableProvider
		for _, s := range facts[r.Key].providers {
			p, ex := q.consumableProvider(s)
			switch {
			case ex.reason == ConsumableExcludedAcceptanceUnknown:
				unknownByFix[ex.fix] = append(unknownByFix[ex.fix], exclusionEntry(r.Key, ex.why))
				continue
			case ex.reason != "":
				byReason[ex.reason] = append(byReason[ex.reason], exclusionEntry(r.Key, ex.why))
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
	return kept, consumableLimitations(byReason, unknownByFix)
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
	{ConsumableExcludedNothingToConsume, "its newest revision declares nothing a consumer references (no workload, interface, required configuration or policy)",
		"declare a workload, an interface, a required configuration or a policy"},
	{ConsumableExcludedNotAccepted, "not accepted", "a person declares the offering with a verified signature the installation signer policy allows, or an installation publisher rule covers its signer"},
	{ConsumableExcludedSchemaOpen, "an open parameter schema", "a person closes it and accepts again"},
	{ConsumableExcludedSchemaComposed, "a parameter schema composed with allOf, anyOf or oneOf",
		"list its fields under properties, and each oneOf or anyOf choice as a const or an enum"},
}

// maxConsumableNames bounds the offerings a consumable limitation names.
const maxConsumableNames = 3

// consumableLimitations is one OFFERING_NOT_CONSUMABLE limitation per reason
// a known exclusion has and one OFFERING_ACCEPTANCE_UNKNOWN per fix of the
// providers Pacto could not judge, each counting the providers and naming
// up to three offerings.
func consumableLimitations(byReason, unknownByFix map[string][]string) []Limitation {
	var lims []Limitation
	for _, e := range consumableExclusions {
		if names := byReason[e.reason]; len(names) > 0 {
			lims = append(lims, Limitation{Code: LimitationOfferingNotConsumable,
				Message: fmt.Sprintf("Not consumable, %s: %s (%s); %s", e.why, providersOfOfferings(len(names)), someNames(names), e.fix)})
		}
	}
	for _, fix := range slices.Sorted(maps.Keys(unknownByFix)) {
		names := unknownByFix[fix]
		lims = append(lims, Limitation{Code: LimitationOfferingAcceptanceUnknown,
			Message: fmt.Sprintf("Acceptance unknown: %s (%s), so a missing offering means unknown; %s", providersOfOfferings(len(names)), someNames(names), fix)})
	}
	if names := byReason[ConsumableExcludedSchemaUnreadable]; len(names) > 0 {
		lims = append(lims, Limitation{Code: LimitationOfferingAcceptanceUnknown,
			Message: fmt.Sprintf("Parameter schema unreadable: %s (%s), so a missing offering means unknown; publish the provider again with its schema files",
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

// someNames is up to maxConsumableNames of the distinct names, sorted, and
// how many more; an offering with several providers left out for the same
// reason is named once.
func someNames(names []string) string {
	return someNamesOf(slices.Compact(slices.Sorted(slices.Values(names))), "; ")
}

// someNamesOf is up to maxConsumableNames of names, in order, joined by sep,
// and how many more.
func someNamesOf(names []string, sep string) string {
	shown, total, _ := boundSlice(names, maxConsumableNames)
	s := strings.Join(shown, sep)
	if total > len(shown) {
		s += fmt.Sprintf("%s%d more", sep, total-len(shown))
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
		return &InvalidQueryError{Field: "consumable", Value: "true", Reason: "consumable lists accepted offerings and declaration inferred lists inferred ones; ask for one"}
	}
	return nil
}
