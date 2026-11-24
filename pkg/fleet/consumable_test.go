package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const csmDomain = "ghcr.io/acme"

// csmRev is name at version, a workload provider of offering published at
// csmDomain, with prov as its signature check.
func csmRev(name, version, offering string, prov *BundleProvenance) RawRevision {
	c := svc24(name, version, offering)
	c.Workload = contract.WorkloadService
	r := ociRev(csmDomain, c, nil)
	r.Provenance = prov
	return r
}

// csmProv is a signature check of status by source, signed by the CI
// workflow of the provider.
func csmProv(status, source string) *BundleProvenance {
	return &BundleProvenance{Signature: BundleSignature{Status: status, Source: source,
		Signer: "https://github.com/acme/pg/.github/workflows/publish.yml@refs/heads/main", Issuer: "https://token.actions.githubusercontent.com"}, Source: "oci"}
}

func csmRule() *BundleProvenance { return csmProv(SignatureVerified, "rule") }

// csmFleet builds an OCI source of revs with the signer policy given.
func csmFleet(t *testing.T, policy *DeclarationSignerPolicy, revs ...RawRevision) *Query {
	t.Helper()
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, DeclarationSigners: policy},
		NewMemorySource("oci", "oci", &Collection{Revisions: revs}))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

// consumableList is q's consumable offering list.
func consumableList(t *testing.T, q *Query) *EntityList {
	t.Helper()
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindOffering}, Consumable: true})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// csmLimitations is l's limitations of code.
func csmLimitations(l *EntityList, code string) []Limitation {
	return slices.DeleteFunc(slices.Clone(l.Meta.Limitations), func(x Limitation) bool { return x.Code != code })
}

// wantExcluded asserts l lists nothing, one limitation of code names
// offering and its message holds want, and the completeness.
func wantExcluded(t *testing.T, l *EntityList, code, offering, want string, completeness Completeness) {
	t.Helper()
	lims := csmLimitations(l, code)
	if len(l.Entities) != 0 || len(lims) != 1 || !strings.Contains(lims[0].Message, offering) || !strings.Contains(lims[0].Message, want) {
		t.Errorf("entities %+v, %s limitations %+v, want %q", l.Entities, code, lims, want)
	}
	if l.Meta.Completeness != completeness {
		t.Errorf("completeness = %s, want %s", l.Meta.Completeness, completeness)
	}
}

// wantProvider asserts l lists one offering with one provider accepted as
// how by publisher.
func wantProvider(t *testing.T, l *EntityList, how, publisher string) ConsumableProvider {
	t.Helper()
	if len(l.Entities) != 1 || l.Entities[0].Consumable == nil || len(l.Entities[0].Consumable.Providers) != 1 {
		t.Fatalf("entities = %+v", l.Entities)
	}
	p := l.Entities[0].Consumable.Providers[0]
	if p.Acceptance != how || p.Publisher != publisher {
		t.Errorf("provider = %+v, want %s by %s", p, how, publisher)
	}
	return p
}

// csmDeclRev is a declaration of postgresql by asker, provenance prov.
func csmDeclRev(t *testing.T, asker contract.InferredSigner, prov *BundleProvenance) RawRevision {
	t.Helper()
	c := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "pg", Version: "17.0.0", Offering: "postgresql", Owner: contract.Owner{Team: "t"}},
		Workload: contract.WorkloadService}
	fsys := fstest.MapFS{"deploy/app.yaml": {Data: []byte(recordManifest)}, contract.InferredRecordFile: {Data: declaredRecord(t, contract.DeclarationDeclared, asker)}}
	r := ociRev(csmDomain, c, fsys)
	r.Provenance = prov
	return r
}

func TestConsumableDeclarationAccepted(t *testing.T) {
	p := wantProvider(t, consumableList(t, csmFleet(t, anaPolicy(), csmDeclRev(t, ana, anaSigned()))), AcceptanceDeclaration, anaSubject)
	if p.Version != "17.0.0" || p.Digest != testDigest(csmDomain, "pg", "17.0.0") || p.Lifecycle != LifecycleSupported || p.Snippet == nil ||
		p.Service.Key != string(NewServiceKeyDomain(csmDomain, "pg")) || p.Revision.Kind != KindRevision {
		t.Errorf("provider = %+v", p)
	}
}

func TestConsumablePublisherRuleAccepted(t *testing.T) {
	for _, source := range []string{"rule", "pin", "policy", "release"} {
		t.Run(source, func(t *testing.T) {
			l := consumableList(t, csmFleet(t, nil, csmRev("pg", "17.0.0", "postgresql", csmProv(SignatureVerified, source))))
			wantProvider(t, l, AcceptancePublisher, csmRule().Signature.Signer)
			if l.Meta.Completeness != CompletenessComplete || len(csmLimitations(l, LimitationOfferingNotConsumable)) != 0 {
				t.Errorf("meta = %+v", l.Meta)
			}
		})
	}
}

func TestConsumableOperatorSignatureNeverAccepts(t *testing.T) {
	l := consumableList(t, csmFleet(t, nil, csmRev("pg", "17.0.0", "postgresql", csmProv(SignatureUnverified, SignatureSourceOperator))))
	wantExcluded(t, l, LimitationOfferingNotConsumable, "postgresql", "operator", CompletenessComplete)
}

func TestConsumableInferredRuleIsUnknownAndPartial(t *testing.T) {
	l := consumableList(t, csmFleet(t, nil, csmRev("pg", "17.0.0", "postgresql", csmProv(SignatureUnknown, "inferred"))))
	wantExcluded(t, l, LimitationOfferingAcceptanceUnknown, "postgresql", "no installation publisher rule was read", CompletenessPartial)
}

func TestConsumableUnverifiedSignatureNotAccepted(t *testing.T) {
	l := consumableList(t, csmFleet(t, nil, csmRev("pg", "17.0.0", "postgresql", csmProv(SignatureUnverified, "rule"))))
	wantExcluded(t, l, LimitationOfferingNotConsumable, "postgresql", "its signature is unverified", CompletenessComplete)
	// A verified signer no rule, pin, policy or release covers.
	l = consumableList(t, csmFleet(t, nil, csmRev("pg", "17.0.0", "postgresql", csmProv(SignatureVerified, "none"))))
	wantExcluded(t, l, LimitationOfferingNotConsumable, "postgresql", "covers its signer", CompletenessComplete)
}

func TestConsumableUnsignedNotAccepted(t *testing.T) {
	l := consumableList(t, csmFleet(t, nil, csmRev("pg", "17.0.0", "postgresql", csmProv(SignatureUnsigned, "none"))))
	wantExcluded(t, l, LimitationOfferingNotConsumable, "postgresql", "not accepted: 1 provider of an offering you may see (postgresql: its signature is unsigned)", CompletenessComplete)
}

func TestConsumableDeclarationRefusedNotAccepted(t *testing.T) {
	pol := &DeclarationSignerPolicy{Signers: []PolicySigner{{Issuer: anaIssuer, Subjects: `[^@]+@example\.com`}}}
	// The dropped cluster facts make the snapshot partial; the list keeps it.
	q := csmFleet(t, pol, csmDeclRev(t, ana, anaSigned()))
	wantExcluded(t, consumableList(t, q), LimitationOfferingNotConsumable, "postgresql", "matches none of the subjects", q.snap.Completeness)
}

func TestConsumableDeclarationUnmatchedNotAccepted(t *testing.T) {
	prov := anaSigned()
	prov.Signers[0].Subject = "bob@acme.com"
	q := csmFleet(t, anaPolicy(), csmDeclRev(t, ana, prov))
	wantExcluded(t, consumableList(t, q), LimitationOfferingNotConsumable, "postgresql", "no verified person signer matches its asker", q.snap.Completeness)
}

func TestConsumableUncheckedIsUnknownAndPartial(t *testing.T) {
	for name, rev := range map[string]RawRevision{
		"declaration, no provenance": csmDeclRev(t, ana, nil),
		"declaration, status unknown": csmDeclRev(t, ana, func() *BundleProvenance {
			p := anaSigned()
			p.Signature.Status = SignatureUnknown
			return p
		}()),
		"plain, no provenance":  csmRev("pg", "17.0.0", "postgresql", nil),
		"plain, status unknown": csmRev("pg", "17.0.0", "postgresql", csmProv(SignatureUnknown, "rule")),
	} {
		t.Run(name, func(t *testing.T) {
			l := consumableList(t, csmFleet(t, anaPolicy(), rev))
			wantExcluded(t, l, LimitationOfferingAcceptanceUnknown, "postgresql", "registry source that checks signatures", CompletenessPartial)
		})
	}
}

func TestConsumableNoSignerPolicyIsUnknownAndPartial(t *testing.T) {
	l := consumableList(t, csmFleet(t, nil, csmDeclRev(t, ana, anaSigned())))
	wantExcluded(t, l, LimitationOfferingAcceptanceUnknown, "postgresql", noSignerPolicy, CompletenessPartial)
}

func TestConsumableUnreadSignerPolicyIsUnknownAndPartial(t *testing.T) {
	l := consumableList(t, csmFleet(t, &DeclarationSignerPolicy{Unread: "forbidden"}, csmDeclRev(t, ana, anaSigned())))
	wantExcluded(t, l, LimitationOfferingAcceptanceUnknown, "postgresql", "could not be read (forbidden)", CompletenessPartial)
}

// csmUnreadable is pg at version, a revision that does not validate.
func csmUnreadable(t *testing.T, version string) RawRevision {
	t.Helper()
	y := strings.NewReplacer("name: bad-svc", "name: pg\n  offering: postgresql", "version: 1.0.0", "version: "+version).Replace(invalidYAML)
	c := mustParse(t, y)
	return RawRevision{Bundle: &contract.Bundle{Contract: c, RawYAML: []byte(y), FS: fstest.MapFS{}}, Domain: csmDomain,
		RequestedRef: "oci://" + csmDomain + "/pg:" + version, Digest: testDigest(csmDomain, "pg", version), Provenance: csmRule()}
}

func TestConsumableNewestUnreadableIsUnknown(t *testing.T) {
	l := consumableList(t, csmFleet(t, nil, csmRev("pg", "16.1.0", "postgresql", csmRule()), csmUnreadable(t, "17.0.0")))
	wantExcluded(t, l, LimitationOfferingAcceptanceUnknown, "postgresql", "newest revision could not be read", CompletenessPartial)
	// No readable revision at all: a target reports the offering.
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{csmUnreadable(t, "17.0.0")}}),
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{
			{Scope: "db", Kind: SourceKindKubernetes, Name: "pg-0", Service: "pg", Domain: csmDomain, Offering: "postgresql"}}}))
	if err != nil {
		t.Fatal(err)
	}
	l = consumableList(t, NewQuery(snap))
	if len(csmLimitations(l, LimitationOfferingNotConsumable)) != 0 {
		t.Errorf("an unreadable provider was a known exclusion: %+v", l.Meta.Limitations)
	}
	wantExcluded(t, l, LimitationOfferingAcceptanceUnknown, "postgresql", "newest revision could not be read", CompletenessPartial)
}

func TestConsumableEmptyCompleteStaysComplete(t *testing.T) {
	l := consumableList(t, csmFleet(t, nil, csmRev("web", "1.0.0", "", csmRule())))
	if len(l.Entities) != 0 || l.Meta.Completeness != CompletenessComplete || len(l.Meta.Limitations) != 0 {
		t.Errorf("list = %+v", l)
	}
}

func TestConsumableExcludesInferredService(t *testing.T) {
	q := csmFleet(t, nil, csmRev("pg", "17.0.0", "postgresql", csmRule()))
	s := q.snap.Services[NewServiceKeyDomain(csmDomain, "pg")]
	s.Declaration = DeclarationInferred
	l := consumableList(t, q)
	if len(l.Entities) != 0 || len(l.Meta.Limitations) != 0 {
		t.Errorf("an inferred provider reached the rule: %+v", l)
	}
}

func TestConsumableExcludesObservedOnlyOffering(t *testing.T) {
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{csmRev("pg", "17.0.0", "", csmRule())}}),
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{
			{Scope: "db", Kind: SourceKindKubernetes, Name: "pg-0", Service: "pg", Domain: csmDomain, Offering: "postgresql"}}}))
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	if s := q.snap.Services[NewServiceKeyDomain(csmDomain, "pg")]; s.OfferingProvenance != ProvenanceObserved {
		t.Fatalf("service = %+v", s)
	}
	wantExcluded(t, consumableList(t, q), LimitationOfferingNotConsumable, "postgresql", "only targets report the offering", CompletenessComplete)
	// A provider with no revision at all is known observed-only, never unknown.
	snap, err = Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{
			{Scope: "db", Kind: SourceKindKubernetes, Name: "pg-0", Service: "pg", Domain: csmDomain, Offering: "postgresql"}}}))
	if err != nil {
		t.Fatal(err)
	}
	l := consumableList(t, NewQuery(snap))
	wantExcluded(t, l, LimitationOfferingNotConsumable, "postgresql", "only targets report the offering", snap.Completeness)
	if len(csmLimitations(l, LimitationOfferingAcceptanceUnknown)) != 0 {
		t.Errorf("a provider with no revision read unknown: %+v", l.Meta.Limitations)
	}
}

func TestConsumableSunsetExcludedDeprecatedListed(t *testing.T) {
	sunset := csmRev("pg", "17.0.0", "postgresql", csmRule())
	sunset.Bundle.Contract.Service.Lifecycle = &contract.InterfaceLifecycle{Deprecated: "2020-01-01", Sunset: "2021-01-01"}
	wantExcluded(t, consumableList(t, csmFleet(t, nil, sunset)), LimitationOfferingNotConsumable, "postgresql", "past the sunset date", CompletenessComplete)
	deprecated := csmRev("pg", "17.0.0", "postgresql", csmRule())
	deprecated.Bundle.Contract.Service.Lifecycle = &contract.InterfaceLifecycle{Deprecated: "2020-01-01"}
	if p := wantProvider(t, consumableList(t, csmFleet(t, nil, deprecated)), AcceptancePublisher, csmRule().Signature.Signer); p.Lifecycle != LifecycleDeprecated {
		t.Errorf("lifecycle = %s", p.Lifecycle)
	}
}

func TestConsumableNotPublishedExcluded(t *testing.T) {
	c := svc24("pg", "17.0.0", "postgresql")
	c.Workload = contract.WorkloadService
	local := RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: "sha256:local", Provenance: csmRule()}
	q := buildDomainSnap(t, "local", &Collection{Revisions: []RawRevision{local}})
	wantExcluded(t, consumableList(t, q), LimitationOfferingNotConsumable, "postgresql", "no registry repository", CompletenessComplete)
}

// TestContributedOfferingNotConsumable: a signed contribution declaring
// an offering is an inferred service, never consumable, and a consumer's
// dependency on it stays unmet.
func TestContributedOfferingNotConsumable(t *testing.T) {
	rev := ctbRev("pg", ctbYAML("pg"), ctbRepo, "acme-idp", ctbRecord(), fixedNow().Add(-time.Hour))
	rev.Bundle.Contract.Service.Offering, rev.Bundle.Contract.Workload = "postgresql", contract.WorkloadService
	rev.Provenance = csmRule()
	consumer := svc24("app", "1.0.0", "")
	consumer.Dependencies = []contract.Dependency{{Name: "pg", Ref: "oci://" + ctbRepo + "/pg", Required: true, Compatibility: "^0.1.0"}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: 24 * time.Hour},
		NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{ociRev(ctbRoot, consumer, nil)}}),
		NewMemorySource("evidence", SourceKindEvidenceHTTP, &Collection{Revisions: []RawRevision{rev}}))
	if err != nil {
		t.Fatal(err)
	}
	key := NewServiceKeyDomain(ctbRoot+"/pacto-inferred/acme-idp", "pg")
	if s := snap.Services[key]; s == nil || !s.Inferred() {
		t.Fatalf("contribution = %+v", s)
	}
	for _, r := range snap.Relationships {
		if r.ToService == key && r.Provenance == ProvenanceDeclared {
			t.Errorf("a declared dependency resolved to a contribution: %+v", r)
		}
	}
	if l := consumableList(t, NewQuery(snap)); len(l.Entities) != 0 {
		t.Errorf("a contributed offering is consumable: %+v", l.Entities)
	}
}

func TestConsumableNewestRevisionSpeaks(t *testing.T) {
	l := consumableList(t, csmFleet(t, nil, csmRev("pg", "16.1.0", "postgresql", csmRule()), csmRev("pg", "17.0.0", "postgresql", nil)))
	if len(l.Entities) != 0 {
		t.Errorf("an older accepted version spoke for the newest: %+v", l.Entities)
	}
	l = consumableList(t, csmFleet(t, nil, csmRev("pg", "16.1.0", "postgresql", csmRule()), csmRev("pg", "17.0.0", "postgresql", csmProv(SignatureUnsigned, "none"))))
	wantExcluded(t, l, LimitationOfferingNotConsumable, "postgresql", "not accepted", CompletenessComplete)
}

func TestConsumableTwoProvidersOneRow(t *testing.T) {
	l := consumableList(t, csmFleet(t, nil,
		csmRev("pg", "17.0.0", "postgresql", csmRule()), csmRev("pg-ha", "2.0.0", "postgresql", csmProv(SignatureUnsigned, "none")),
		csmRev("redis", "7.0.0", "redis", csmProv(SignatureUnsigned, "none"))))
	if len(l.Entities) != 1 || l.Entities[0].Key != "postgresql" || l.Entities[0].Consumable.ProvidersTotal != 1 || l.Entities[0].Consumable.ProvidersTruncated {
		t.Fatalf("entities = %+v", l.Entities)
	}
	lims := csmLimitations(l, LimitationOfferingNotConsumable)
	if len(lims) != 1 || !strings.HasPrefix(lims[0].Message, "Not consumable, not accepted: 2 providers of offerings you may see (postgresql: its signature is unsigned; redis: ") {
		t.Errorf("limitations = %+v", lims)
	}
}

func TestConsumableRestrictedViewHidesProviders(t *testing.T) {
	digest := testDigest(csmDomain, "pg", "17.0.0")
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{csmRev("pg", "17.0.0", "postgresql", csmRule())}}),
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{
			{Scope: "secret", Kind: SourceKindKubernetes, Name: "pg-0", Service: "pg", Domain: csmDomain, Digest: digest, Compliance: StatusCompliant}}}))
	if err != nil {
		t.Fatal(err)
	}
	if l := consumableList(t, NewQuery(snap)); len(l.Entities) != 1 {
		t.Fatalf("unrestricted = %+v", l.Entities)
	}
	l := consumableList(t, NewQuery(snap).Restrict(anyCluster(func(ns string) bool { return ns != "secret" }), anyClusterKind(nil)))
	blob, _ := json.Marshal(l)
	if len(l.Entities) != 0 || strings.Contains(string(blob), `"pg`) || len(csmLimitations(l, LimitationOfferingNotConsumable))+len(csmLimitations(l, LimitationOfferingAcceptanceUnknown)) != 0 {
		t.Errorf("the hidden provider leaks: %s", blob)
	}
}

func TestConsumableProvidersBounded(t *testing.T) {
	var revs []RawRevision
	for i := range 12 {
		revs = append(revs, csmRev(fmt.Sprintf("pg-%02d", i), "17.0.0", "postgresql", csmRule()))
	}
	c := consumableList(t, csmFleet(t, nil, revs...)).Entities[0].Consumable
	if len(c.Providers) != MaxConsumableProviders || c.ProvidersTotal != 12 || !c.ProvidersTruncated || !strings.HasSuffix(c.Providers[0].Service.Key, "pg-00") {
		t.Errorf("consumable = %d providers, total %d, truncated %v", len(c.Providers), c.ProvidersTotal, c.ProvidersTruncated)
	}
	// A limitation names at most three offerings.
	if got := someNames([]string{"e", "d", "c", "b", "a"}); got != "a; b; c; 2 more" {
		t.Errorf("names = %q", got)
	}
	// An offering with several providers left out is named once.
	if got := someNames([]string{"b", "a", "b"}); got != "a; b" {
		t.Errorf("names = %q", got)
	}
	if got := providersOfOfferings(1); got != "1 provider of an offering you may see" {
		t.Errorf("one = %q", got)
	}
}

func TestConsumableRefusesOtherKind(t *testing.T) {
	for name, f := range map[string]EntityFilter{
		"no kinds":          {Consumable: true},
		"service":           {Kinds: []EntityKind{KindService}, Consumable: true},
		"offering, service": {Kinds: []EntityKind{KindOffering, KindService}, Consumable: true},
		"inferred":          {Kinds: []EntityKind{KindOffering}, Declaration: DeclarationInferred, Consumable: true},
	} {
		t.Run(name, func(t *testing.T) {
			var iq *InvalidQueryError
			if _, err := csmFleet(t, nil).Entities(f); !errors.As(err, &iq) || iq.Field != "consumable" {
				t.Errorf("err = %v", err)
			}
		})
	}
}

// entitiesGolden is offeringDetailFleet's offering list before
// EntityFilter.Consumable existed.
const entitiesGolden = `{"meta":{"schemaVersion":"pacto.dev/fleet-product/v1","snapshotId":"sha256:717f88b839313c06d3168e2f86d8dbee35fd7d0ff77b5850c5dac647dda2af1d","asOf":"2026-07-29T12:00:00Z","completeness":"complete","sources":[{"id":"src","kind":"oci","status":"available","lastSuccessfulSync":"2026-07-29T12:00:00Z","observedAt":"2026-07-29T12:00:00Z","revisionCount":5,"targetCount":1}],"sourceCounts":{"total":1,"available":1,"partial":0,"stale":0,"unavailable":0,"availableByKind":[{"kind":"oci","count":1}]},"externalCatalogs":[{"origin":"default","repository":"ghcr.io/trianalab/pacto/external-catalog","digest":"sha256:62a3fd52949e99b18eb330766ec3269d47ba7a821f59b77ab69dea81ad76a063","copy":"embedded","signature":"unknown","reason":"the embedded copy has not been checked against the registry"}]},"total":1,"count":1,"limit":100,"offset":0,"truncated":false,"entities":[{"kind":"offering","key":"shared-config","label":"shared-config","offering":"shared-config","offeringProvenance":"declared","owner":"platform","targets":1,"services":2,"lifecycle":"supported","availability":"unknown","availabilityProvenance":"unknown","consumers":2}],"aggregate":{"matched":1,"services":0,"inferredServices":0,"revisions":0,"targets":0,"owners":0,"sources":0,"domains":0,"serviceCompliance":{"compliant":0,"nonCompliant":0,"unknown":0,"warning":0,"invalid":0,"reference":0,"notEvaluated":0,"other":0,"staleCompliant":0},"targetCompliance":{"compliant":0,"nonCompliant":0,"unknown":0,"warning":0,"invalid":0,"reference":0,"notEvaluated":0,"other":0,"staleCompliant":0},"targetHealth":{"available":0,"degraded":0,"unavailable":0,"unknown":0,"notObserved":0},"ownership":{"consistent":0,"conflicting":0,"unowned":0,"unknown":0},"readiness":{"passing":0,"belowThreshold":0,"expired":0,"notDeclared":0},"knowledge":{"services":0,"described":0,"withGaps":0,"unknown":0,"gaps":{"noSummary":0,"noDomain":0,"noInterfaceDocs":0,"noReadiness":0,"notDeployed":0,"unverifiedCapability":0},"contractCompleteness":{"judged":0,"unknown":0,"fullyDescribed":0,"mostMissing":[]}},"offeringCount":1,"offeringPhases":{"requested":0,"provisioning":0,"ready":0,"failed":0,"deleting":0,"unknown":2,"readyUnsigned":0},"offeringPhaseConsumers":{"requested":0,"provisioning":0,"ready":0,"failed":0,"deleting":0,"unknown":2,"readyUnsigned":0},"origins":{"beyond":{"services":0,"nonCompliant":0},"unresolved":{"services":0,"nonCompliant":0},"none":{"services":0,"nonCompliant":0}}}}`

func TestEntitiesWithoutConsumableUnchanged(t *testing.T) {
	for _, f := range []EntityFilter{{Kinds: []EntityKind{KindOffering}}, {Kinds: []EntityKind{KindOffering}, Consumable: false}} {
		l, err := offeringDetailFleet(t).Entities(f)
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := json.Marshal(l); string(b) != entitiesGolden {
			t.Errorf("offering list changed:\n%s", b)
		}
	}
}
