package impact

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// serviceEventCases are service-subject event answers over snapshots built at
// evidenceNow, so their JSON is stable.
func serviceEventCases(t *testing.T) map[string]*Result {
	obs := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("auth-service"), rev("shadow"), rev("a", dep("auth-service", false)), rev("b", authDep)},
		Observed: []fleet.ObservedEdge{{From: "shadow", To: "auth-service", Count: 2, LastSeen: *ago(time.Hour)}, {From: "k8s:pay/Deployment/legacy", To: "auth-service", Count: 1}}})
	chain := func() *fleet.FleetSnapshot {
		return buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("auth-service"), rev("api-gateway", authDep), rev("frontend", dep("api-gateway", false))},
			Targets: []fleet.RawTarget{target("api-gateway", ago(time.Hour)), target("auth-service", ago(30*time.Hour))}})
	}
	return map[string]*Result{
		"chain-unavailable":    event(chain(), EventUnavailable, "auth-service"),
		"chain-removed":        event(chain(), EventRemoved, "auth-service"),
		"chain-regressed":      regressed(chain(), &Regression{Evidence: []RegressionEvidence{{Kind: RegressionFailed, Namespace: "n", Stage: "s"}}}),
		"observed-unavailable": event(obs, EventUnavailable, "auth-service"),
		"policy-removed": event(buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{{
			Bundle: &contract.Bundle{Contract: policyProvider("1.0.0"), FS: policySchema(openPolicy)}, Digest: "sha256:policy", RequestedRef: policyRef + ":1.0.0",
		}, policyReferrer("orders", true, dep("policy", true))}}), EventRemoved, "policy"),
		"pg-removed": event(pgProvisioned(t, "production", "staging"), EventRemoved, "postgresql"),
	}
}

// The service path answers what it answered before the per-subject body was
// extracted. testdata/service-events.json was generated before the
// extraction; the one difference, by design, is that an unavailable
// consumer now carries the resources it asked for.
func TestImpactServiceEventUnchanged(t *testing.T) {
	got, err := json.MarshalIndent(serviceEventCases(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/service-events.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("service event answers changed:\n%s", got)
	}
}

// offeringRev is a revision of name that provides offering.
func offeringRev(name, offering, team string, deps ...contract.Dependency) fleet.RawRevision {
	c := svcContract(name, "1.0.0", contract.Owner{Team: team}, deps...)
	c.Service.Offering = offering
	return fleet.RawRevision{Bundle: mkBundle(c)}
}

// kubeTarget is a fresh Kubernetes target of service in namespace ns.
func kubeTarget(service, ns string) fleet.RawTarget {
	return fleet.RawTarget{Scope: ns, Kind: fleet.SourceKindKubernetes, Name: service, Service: service, Compliance: fleet.StatusCompliant, EvidenceAt: ago(time.Hour)}
}

// offeringCollection: pg and pg-ha provide postgresql (pg-ha replicates from
// pg); orders needs pg, billing may use pg-ha, shared needs pg and may use
// pg-ha, shadow is seen calling pg and an uncontracted caller calls both.
func offeringCollection() *fleet.Collection {
	return &fleet.Collection{
		Revisions: []fleet.RawRevision{
			offeringRev("pg", "postgresql", "db"),
			offeringRev("pg-ha", "postgresql", "ha", dep("pg", true)),
			rev("orders", dep("pg", true)),
			rev("billing", dep("pg-ha", false)),
			rev("shared", dep("pg", true), dep("pg-ha", false)),
			rev("shadow"),
		},
		Targets: []fleet.RawTarget{kubeTarget("pg", "db"), kubeTarget("pg-ha", "ha"), kubeTarget("orders", "shop"),
			kubeTarget("billing", "shop"), kubeTarget("shared", "shop"), kubeTarget("shadow", "shop")},
		Observed: []fleet.ObservedEdge{{From: "shadow", To: "pg", Count: 3, LastSeen: *ago(time.Hour)},
			{From: "k8s:pay/Deployment/legacy", To: "pg", Count: 1}, {From: "k8s:pay/Deployment/legacy", To: "pg-ha", Count: 1}},
	}
}

func offeringEvent(snap *fleet.FleetSnapshot, kind, offering string) *Result {
	return Analyze(context.Background(), nil, nil, nil, nil, snap, Options{Event: &Event{Kind: kind, Offering: offering}})
}

func TestImpactOfferingUnionsProviders(t *testing.T) {
	res := offeringEvent(buildAt(t, offeringCollection()), EventUnavailable, "postgresql")
	got := effectsOf(res)
	want := map[string]string{"orders": EffectBreaks, "billing": EffectMayDegrade, "shared": EffectBreaks, "shadow": EffectUnknown, "k8s:pay/Deployment/legacy": EffectUnknown}
	if !reflect.DeepEqual(got, want) || res.Service != "postgresql" || !reflect.DeepEqual(res.Providers, []string{"pg", "pg-ha"}) {
		t.Errorf("effects %v, service %s, providers %v", got, res.Service, res.Providers)
	}
	if !strings.Contains(res.GateReason, "if postgresql becomes unavailable (providers pg, pg-ha), 2 consumers break") {
		t.Errorf("gate reason %q", res.GateReason)
	}
}

func TestImpactOfferingDedupesConsumer(t *testing.T) {
	res := offeringEvent(buildAt(t, offeringCollection()), EventUnavailable, "postgresql")
	c := consumerNamed(t, res, "shared")
	if c.Effect != EffectBreaks || !reflect.DeepEqual(c.Path, []string{"shared", "pg"}) || !c.Required || !reflect.DeepEqual(c.Via, []string{fleet.RelationshipDependency}) ||
		c.Note != "it declares the dependency as required" || c.policyVerdict != "" || c.rangeVerdict != "" {
		t.Errorf("shared = %+v", c)
	}
	n := 0
	for _, c := range res.Consumers {
		if c.Service == "k8s:pay/Deployment/legacy" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the uncontracted caller of both providers is listed %d times", n)
	}
}

// A worse row met later takes over, its path with it; a status both
// providers report is named once; policy verdicts stay as they were.
func TestMergeEventConsumer(t *testing.T) {
	status := fleet.ProvisioningStatus{Phase: "Unknown"}
	rows := mergeEventConsumer(nil, AffectedConsumer{Service: "x", Effect: EffectMayDegrade, Path: []string{"x", "a"}, Via: []string{"observed"},
		Resources: []fleet.ProvisioningStatus{status}, ResourcesTotal: 1, policyVerdict: CompatibilityCompatible})
	rows = mergeEventConsumer(rows, AffectedConsumer{Service: "x", Effect: EffectBreaks, Path: []string{"x", "b"}, Via: []string{"observed", "dependency"},
		Resources: []fleet.ProvisioningStatus{status, {Phase: "Ready"}}, ResourcesTotal: 2, Evidence: []Evidence{{Kind: EvidenceTarget, Source: "t"}}})
	if len(rows) != 1 || rows[0].Effect != EffectBreaks || !reflect.DeepEqual(rows[0].Path, []string{"x", "b"}) || !reflect.DeepEqual(rows[0].Via, []string{"observed", "dependency"}) ||
		rows[0].ResourcesTotal != 2 || len(rows[0].Resources) != 2 || rows[0].policyVerdict != CompatibilityCompatible || len(rows[0].Evidence) != 1 {
		t.Errorf("rows = %+v", rows)
	}
}

func TestImpactOfferingLimitationsNotDuplicated(t *testing.T) {
	col := offeringCollection()
	col.Targets = append(col.Targets, fleet.RawTarget{Scope: "shop", Kind: "deployment", Name: "orders-old", Service: "orders", EvidenceAt: ago(30 * time.Hour)})
	snap := buildAt(t, col)
	snap.Limitations = append(snap.Limitations, fleet.Limitation{Code: "SNAP", Message: "once"})
	res := offeringEvent(snap, EventUnavailable, "postgresql")
	seen := map[string]int{}
	for _, l := range res.Limitations {
		seen[l.Code+l.Message]++
		if seen[l.Code+l.Message] > 1 {
			t.Errorf("limitation said twice: %+v", l)
		}
	}
	if limitation(res, "SNAP") == nil || limitation(res, "CONSUMER_EVIDENCE_STALE") == nil {
		t.Errorf("limitations %+v", res.Limitations)
	}
}

func TestImpactOfferingProviderNotConsumer(t *testing.T) {
	res := offeringEvent(buildAt(t, offeringCollection()), EventRemoved, "postgresql")
	if _, ok := effectsOf(res)["pg-ha"]; ok {
		t.Errorf("a provider is its own consumer: %v", effectsOf(res))
	}
}

func TestImpactOfferingNotInFleet(t *testing.T) {
	res := offeringEvent(buildAt(t, offeringCollection()), EventUnavailable, "kafka")
	if res.Completeness != fleet.CompletenessPartial || res.Gate != GateUnverified || limitation(res, LimitationOfferingNotInFleet) == nil || len(res.Consumers) != 0 {
		t.Errorf("res = %+v", res)
	}
}

func TestImpactOfferingHiddenProviderCounted(t *testing.T) {
	full := buildAt(t, offeringCollection())
	if limitation(offeringEvent(full, EventUnavailable, "postgresql"), LimitationHiddenProviders) != nil {
		t.Error("an unrestricted answer counts hidden providers")
	}
	snap := fleet.NewQuery(full).Restrict(func(ns string) bool { return ns != "ha" }).SnapshotForSerialization()
	res := offeringEvent(snap, EventUnavailable, "postgresql")
	l := limitation(res, LimitationHiddenProviders)
	if l == nil || !strings.HasPrefix(l.Message, "1 provider of postgresql you may not see") || res.Completeness != fleet.CompletenessPartial {
		t.Errorf("limitations %+v, completeness %s", res.Limitations, res.Completeness)
	}
	if b, _ := json.Marshal(res); bytes.Contains(b, []byte("pg-ha")) || bytes.Contains(b, []byte("billing")) {
		t.Errorf("the hidden provider leaks: %s", b)
	}
	// Every provider hidden reads like a missing offering.
	snap = fleet.NewQuery(full).Restrict(func(ns string) bool { return ns == "shop" }).SnapshotForSerialization()
	if res := offeringEvent(snap, EventUnavailable, "postgresql"); limitation(res, LimitationOfferingNotInFleet) == nil || limitation(res, LimitationHiddenProviders) != nil {
		t.Errorf("limitations %+v", res.Limitations)
	}
}

func TestImpactOfferingRegressedRejected(t *testing.T) {
	res := offeringEvent(buildAt(t, offeringCollection()), EventRegressed, "postgresql")
	if limitation(res, LimitationEventNeedsService) == nil || res.Completeness != fleet.CompletenessPartial || len(res.Consumers) != 0 {
		t.Errorf("res = %+v", res)
	}
}

func TestImpactUnavailableJoinsUses(t *testing.T) {
	c := onlyConsumer(t, event(pgProvisioned(t, "production"), EventUnavailable, "postgresql"))
	if c.ResourcesTotal == 0 || !slices.ContainsFunc(c.Evidence, func(e Evidence) bool { return e.Kind == EvidenceLifecycle }) {
		t.Errorf("consumer = %+v", c)
	}
}

func TestImpactOfferingRemovedResources(t *testing.T) {
	res := offeringEvent(buildAt(t, offeringCollection()), EventRemoved, "postgresql")
	// shared asked both providers; the status neither reported is named once.
	if c := consumerNamed(t, res, "shared"); c.ResourcesTotal != 1 || len(c.Resources) != 1 || c.Resources[0].Phase != "Unknown" {
		t.Errorf("shared resources %+v (%d)", c.Resources, c.ResourcesTotal)
	}
}

func TestImpactOfferingObservedCallerUnknown(t *testing.T) {
	c := consumerNamed(t, offeringEvent(buildAt(t, offeringCollection()), EventUnavailable, "postgresql"), "shadow")
	if c.Effect != EffectUnknown || c.Provenance != fleet.ProvenanceObserved {
		t.Errorf("shadow = %+v", c)
	}
}

func TestImpactOfferingProduct(t *testing.T) {
	snap := buildAt(t, offeringCollection())
	res := offeringEvent(snap, EventRemoved, "postgresql")
	p := BuildProductOutcome(fleet.NewQuery(snap).ProductMeta(), snap, "", nil, nil, res, 0, 0, "")
	if p.Service.Kind != fleet.KindOffering || p.Service.Key != "postgresql" || p.Service.Label != "postgresql" || p.Offering == nil || *p.Offering != p.Service {
		t.Errorf("service %+v, offering %+v", p.Service, p.Offering)
	}
	var providers []string
	for _, r := range p.Providers {
		providers = append(providers, r.Key+"="+r.Owner)
	}
	if !reflect.DeepEqual(providers, []string{"pg=db", "pg-ha=ha"}) || p.ChangedOwner != nil || res.ChangedOwnerKey != "" {
		t.Errorf("providers %v, changed owner %+v", providers, p.ChangedOwner)
	}
	if b, _ := json.Marshal(p); bytes.Contains(b, []byte(`"changedOwner"`)) {
		t.Errorf("changedOwner on an offering answer: %s", b)
	}
}

func TestImpactOfferingGateNeverBlocked(t *testing.T) {
	snap := buildAt(t, offeringCollection())
	for _, kind := range []string{EventUnavailable, EventRemoved} {
		if res := offeringEvent(snap, kind, "postgresql"); res.Gate != GateUnverified || res.ReleaseBlocking() {
			t.Errorf("%s gate %s", kind, res.Gate)
		}
	}
}

// A removed offering lists the golden paths that reference any provider's
// policy once, and an unresolved reference to a provider makes it partial.
func TestImpactOfferingRemovedTemplatesAndPartial(t *testing.T) {
	provider := policyProvider("1.0.0")
	provider.Service.Offering = "policies"
	path := policyReferrer("http-path", false)
	path.Bundle.Contract.Service.Template = &contract.Template{}
	tagged := policyReferrer("orders", false)
	tagged.Lock = nil
	tagged.Bundle.Contract.Policies[0].Ref = policyRef + ":1"
	snap := buildAt(t, &fleet.Collection{Revisions: []fleet.RawRevision{{
		Bundle: &contract.Bundle{Contract: provider, FS: policySchema(openPolicy)}, Digest: "sha256:policy", RequestedRef: policyRef + ":1.0.0",
	}, path, tagged}})
	res := offeringEvent(snap, EventRemoved, "policies")
	if !slices.Equal(res.Templates, []string{"http-path"}) || res.Completeness != fleet.CompletenessPartial || limitation(res, "POLICY_REF_CONSUMERS_UNRESOLVED") == nil {
		t.Errorf("templates %v, completeness %s, limitations %+v", res.Templates, res.Completeness, res.Limitations)
	}
}
