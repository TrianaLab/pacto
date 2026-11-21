package main

import (
	"context"
	_ "embed"
	"maps"
	"time"

	"github.com/trianalab/pacto/v4/examples/story"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
)

// This file builds the in-memory operational graph (fleet) and impact provider
// the WASM demo wires into the dashboard, entirely from the embedded bundles plus
// deterministic operational targets — so the Operational Graph and Impact pages
// are useful the moment the demo opens, with no server and no OCI registry.
// It builds on the host too, so `go test` checks the demo fleet; the impact
// provider and the wiring stay in main_wasm.go.

// demoRegistry is the synthetic registry the demo's revision refs live under. The
// refs are immutable digest refs so they satisfy the same identity rules the real
// product uses; the impact provider resolves them from memory, never over a
// network.
const demoRegistry = "oci://demo.pacto.local/"

// partnerRegistry and partnerDomain are the second domain's registry and logical
// scope. A domain comes from the SOURCE, never from the contract, which is what
// keeps two services that call themselves "platform-app-config" apart.
const (
	partnerRegistry = "oci://partners.demo.pacto.local/"
	partnerDomain   = "partners"
)

// demoClock is when the demo loaded. The fleet snapshot is taken then and every
// fixture time is an offset from it, so the stale-target split is the same on every
// load while "Data as of" reads off the same wall clock the simulated delivery runs
// on. A fixed date here had the fleet pages say "Jul 29, 2026" beside delivery pages
// saying "just now".
var demoClock = time.Now().UTC().Truncate(time.Second)

// demoDetected is what the demo cluster's operator detected: the registry,
// cloud, sign-in, exposure, metrics and node kernels the Data sources page
// lists as inferred rows. The Compose demo carries a byte-identical copy.
//
//go:embed detected.json
var demoDetected []byte

// demoNow is the clock the fleet snapshot is built against.
func demoNow() time.Time { return demoClock }

// buildDemoFleet builds a fleet query over every embedded revision plus the
// demo's operational targets, and returns a ref→bundle index so the impact
// provider can resolve the two revisions to compare from memory.
//
// It deliberately uses several sources rather than one, because the source list, the
// per-target provenance and the completeness semantics are product surfaces in their
// own right and a single source leaves all three blank: the bundle registry and the
// cluster collector that observes it are not the same thing (section 0b, Data source
// vs Collector), telemetry comes from a third, and an environment nobody could reach
// is what makes the snapshot honestly partial.
func buildDemoFleet(src, partnersSrc *EmbedSource) (*fleet.FleetSnapshot, map[string]*contract.Bundle, error) {
	col := &fleet.Collection{}
	byRef := map[string]*contract.Bundle{}
	revisions := func(s *EmbedSource, registry string) []fleet.RawRevision {
		var out []fleet.RawRevision
		for _, name := range s.names {
			for ver, entry := range s.byName[name] {
				ref := registry + name + "@sha256:" + entry.hash
				out = append(out, fleet.RawRevision{
					Bundle:       entry.bundle,
					RequestedRef: requestedRef(registry, name, ver),
					ResolvedRef:  ref,
					Digest:       "sha256:" + entry.hash,
					Document:     entry.doc,
				})
				byRef[ref] = entry.bundle
			}
		}
		return out
	}
	col.Revisions = revisions(src, demoRegistry)
	withProvenance(col.Revisions, "local")
	// newsletter-service stands in for a CI publish, so its owner reads attested.
	attestDemoRevision(col)
	// invoices is a person's declaration of a running workload: it replaces
	// the inferred invoices Deployment the cluster below reports.
	prodCluster := &fleet.Cluster{ID: "5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11", Name: "production", NameSource: fleet.ClusterNameFromContext}
	handover, handoverBundle, handedOver := demoHandover(demoNow(), prodCluster.ID)
	col.Revisions = append(col.Revisions, handover)
	byRef[handover.ResolvedRef] = handoverBundle
	col.Providers = []fleet.Provider{demoRegistryRow(demoNativeRegistry, col.Revisions)}

	// One more registry, the payments story's own domain: the contracts the
	// simulated delivery's Stages run (source_story_fleet.go), so each Stage row
	// links to its revision and a story service page lists its Stages.
	storyRevs, storyRefs, err := storyRevisions()
	if err != nil {
		return nil, nil, err
	}
	maps.Copy(byRef, storyRefs)
	storySource := fleet.NewMemorySource("story-registry", "oci", &fleet.Collection{Revisions: storyRevs})

	// A second registry, in a second DOMAIN. It publishes its own platform-app-config
	// — the same NAME as the default domain's, and not the same service — plus the
	// settlement-service that reads it.
	//
	// This is the demo's adversarial identity case, and every product surface has to
	// survive it: settlement-service's configuration reference must resolve to the
	// PARTNER platform-app-config (a reference resolves within the referring revision's
	// own domain), its policy reference must stay honestly unresolved (the partner
	// domain publishes no http policy and the default domain's is not a candidate),
	// search must show two distinguishable platform-app-configs, each config's
	// "Referenced by" must list only its own domain's consumers, and each service's
	// documentation must be its own. Anything that identifies a service by bare name
	// reads the wrong contract here.
	//
	// The colliding name is deliberately one that carries NO observed traffic. An
	// observed edge names a bare service name, so a collision there resolves to
	// ObservedAmbiguous and the edge is dropped with a limitation rather than
	// misattributed — correct, but it would silently empty the demo's Observed layer.
	partnerCol := &fleet.Collection{Revisions: revisions(partnersSrc, partnerRegistry)}
	for i := range partnerCol.Revisions {
		partnerCol.Revisions[i].Domain = partnerDomain
	}
	partnerCol.Providers = []fleet.Provider{demoRegistryRow(demoTagsRegistry, partnerCol.Revisions)}
	partnerRegistrySource := fleet.NewMemorySource("partner-registry", "oci", partnerCol)

	// The cluster collector reports what is running; the registry above reports what
	// was declared. Keeping them apart is what lets a target say which source saw it.
	// One cluster, named from its kubeconfig context: every key keeps its 3-part
	// form and the target page shows where the target runs.
	takeover, takenOver := demoTakeover(demoNow())
	targets, inferred := append(demoTargets(src), takeover), append(demoInferredServices(), handedOver)
	// Each cluster source says what it detected: the Data sources page lists
	// the Pacto CRD and the workloads the cluster runs. The taken-over
	// workload is the takeover target, so it counts once.
	crdRow, workloadsRow := demoProviders(targets, inferred)
	cluster := fleet.NewMemorySource("k8s-production", fleet.SourceKindKubernetes, &fleet.Collection{
		Cluster:      prodCluster,
		Providers:    []fleet.Provider{crdRow},
		Targets:      targets,
		Provisioning: demoProvisioning(src),
		Usage:        demoUsage(src),
		ExternalUses: demoExternalUses(demoNow()),
		// The kernel observer names a caller with no contract by its workload,
		// so this edge joins the inferred legacy-billing below: attention shows
		// an uncontracted caller of payments-service that links to it.
		Observed: append([]fleet.ObservedEdge{{From: "k8s:shop/Deployment/legacy-billing", To: "payments-service", Count: 9}}, demoInferredEdges(demoNow())...),
	})
	// The cluster's workload reader: workloads no contract describes show as
	// inferred services, each value with the object path Pacto read it from.
	workloads := fleet.NewMemorySource("k8s-production-workloads", fleet.SourceKindKubernetesWorkloads, &fleet.Collection{
		Cluster:          prodCluster,
		InferredServices: append(inferred, takenOver),
		Providers:        append([]fleet.Provider{workloadsRow}, demoGroupingProviders(inferred)...),
	})
	// Telemetry is a second collector over the SAME estate: it corroborates the two
	// targets it can see (agreeing on every identity-bearing field, so they merge
	// instead of quarantining) and contributes the observed call edges. That is how a
	// target ends up with two sources, which is the only way the provenance block on
	// the target page shows anything worth reading.
	// It observes the same estate, so it reports the same cluster: a clusterless
	// contribution merged into a clustered target would make that target partial.
	telemetry := fleet.NewMemorySource("otel-traces", "observation", &fleet.Collection{
		Cluster:  prodCluster,
		Targets:  demoCorroboratedTargets(),
		Observed: demoObservedEdges(demoNow()),
	})
	// A registry mirror that answered, but not with everything it holds: partial is a
	// third source state, distinct from available and unavailable, and the source
	// health strip is meant to distinguish all three.
	mirror := fleet.NewMemorySource("registry-mirror", "oci", &fleet.Collection{
		State:       &fleet.SourceState{Status: fleet.SourcePartial},
		Limitations: []fleet.Limitation{{Code: fleet.LimitationSourcePartial, Source: "registry-mirror", Message: "tag listing truncated by the mirror"}},
	})
	// The edge cluster's telemetry collector, not a Kubernetes source: an
	// unavailable Kubernetes source would leave every target-less service's
	// placement unknown, and the fleet then keeps the http-service golden path an
	// ordinary service, so the Golden paths tab would stay empty.
	unavailable := fleet.NewMemorySource("edge-cluster", "observation", &fleet.Collection{
		State: &fleet.SourceState{
			Status: fleet.SourceUnavailable,
			Error:  &fleet.SourceError{Code: fleet.LimitationSourceUnavailable, Message: "edge cluster unreachable"},
		},
	})
	// What the cluster's operator detected: an advisory source whose rows are
	// hints, dated now so they read current.
	detected, err := detect.Decode(demoDetected)
	if err != nil {
		return nil, nil, err
	}
	if inst := detected.Installation; inst != nil && inst.LastPush != nil {
		// The last push keeps its age against the detection.
		inst.LastPush.At = demoNow().Add(inst.LastPush.At.Sub(detected.AsOf))
	}
	detected.AsOf, detected.Cluster = demoNow(), prodCluster.ID
	detection := fleet.NewMemorySource("cluster-detection", fleet.SourceKindDetection, &fleet.Collection{
		Advisory: true, Providers: detect.ProviderRows(detected, "cluster-detection", demoNow()),
	})
	cat, err := demoCatalog()
	if err != nil {
		return nil, nil, err
	}
	snap, err := fleet.Build(context.Background(),
		fleet.BuildOptions{Now: demoNow, FreshnessWindow: 24 * time.Hour, ExternalCatalog: cat, DeclarationSigners: demoHandoverSigners()},
		fleet.NewMemorySource("local", "local", col), partnerRegistrySource, storySource, cluster, workloads, telemetry, mirror, unavailable, detection)
	if err != nil {
		return nil, nil, err
	}
	return snap, byRef, nil
}

// demoTargets are deterministic operational targets covering the range of states
// the dashboard renders: three compliance verdicts (Compliant, NonCompliant and
// Unknown), exact and inferred revision matches alongside targets with nothing to
// match against, fresh and stale evidence, a service running in more than one scope,
// and the runtime detail a target page is FOR (labels and observed runtime values).
// A target page with no labels and no observed runtime is a page of four counters,
// which is what the demo used to show.
//
// src is threaded in so one target can pin the real content digest of an embedded
// revision. Nothing else in the demo can produce an EXACT revision match: an exact
// link means the collector reported the same immutable digest the registry holds,
// and inventing a digest that resolves to nothing would be the opposite of exact.
func demoTargets(src *EmbedSource) []fleet.RawTarget {
	fresh := demoNow().Add(-1 * time.Hour)
	older := demoNow().Add(-6 * time.Hour)
	stale := demoNow().Add(-72 * time.Hour) // older than the 24h freshness window
	cov := func(e, r int) *fleet.Coverage { return &fleet.Coverage{Evaluated: e, Required: r} }
	at := func(t time.Time) *time.Time { return &t }
	prod := func(extra map[string]string) map[string]string {
		l := map[string]string{"env": "production", "region": "eu-west-1", "managed-by": "platform"}
		for k, v := range extra {
			l[k] = v
		}
		return l
	}
	return []fleet.RawTarget{
		// EXACT, twice over and for two different reasons: the collector reported the
		// content digest of the embedded payments-service 2.1.0 revision (so the runs
		// edge is certain rather than deduced from a mutable tag) AND it resolved a
		// canonical digest reference (so the content is retrievable). Every other
		// target below separates the two -- a tag ref is a confident-enough match with
		// mutable content, and no ref at all is a match with nothing to fetch.
		{Scope: "prod", Kind: "Deployment", Name: "payments-service", Service: "payments-service",
			Digest:      demoDigest(src, "payments-service", "2.1.0"),
			ResolvedRef: demoRegistry + "payments-service@" + demoDigest(src, "payments-service", "2.1.0"),
			Compliance:  fleet.StatusCompliant,
			Coverage:    cov(5, 5), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels:          prod(map[string]string{"app.kubernetes.io/name": "payments-service", "tier": "critical"}),
			ObservedRuntime: map[string]any{"replicas": 6, "image": demoRegistry + "payments-service@" + demoDigest(src, "payments-service", "2.1.0"), "cpuRequest": "500m", "strategy": "RollingUpdate"}},
		// The SAME service in a second scope, matched only by a version tag: INFERRED.
		// One service, two targets, two certainties -- which is why the product keeps
		// Service and Operational Target apart in the first place.
		{Scope: "staging", Kind: "Deployment", Name: "payments-service", Service: "payments-service",
			ResolvedRef: demoRegistry + "payments-service:2.1.0", Compliance: fleet.StatusCompliant,
			Coverage: cov(5, 5), EvidenceAt: at(older), ReconciledAt: at(older),
			Labels:          map[string]string{"env": "staging", "region": "eu-west-1", "app.kubernetes.io/name": "payments-service"},
			ObservedRuntime: map[string]any{"replicas": 1, "image": demoRegistry + "payments-service:2.1.0"}},
		{Scope: "prod", Kind: "Deployment", Name: "orders-service", Service: "orders-service",
			ResolvedRef: demoRegistry + "orders-service:1.2.0", Compliance: fleet.StatusNonCompliant,
			Coverage: cov(4, 5), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels:          prod(map[string]string{"app.kubernetes.io/name": "orders-service", "tier": "critical"}),
			ObservedRuntime: map[string]any{"replicas": 4, "image": demoRegistry + "orders-service:1.2.0"},
			// Three severities on one target: the severity distribution is a real
			// distribution, and the findings table has something to sort.
			Findings: []finding.Finding{
				{Code: finding.CodeInterfaceProtocolMismatch, Severity: finding.SeverityError, Category: finding.CategoryRuntimeDrift, Subject: finding.SubjectRef{Kind: "interface", Name: "http"}, Message: "the http interface answers with a protocol other than the declared OpenAPI one"},
				{Code: finding.CodeDependencyUndeclared, Severity: finding.SeverityWarning, Category: finding.CategoryRuntimeDrift, Subject: finding.SubjectRef{Kind: "dependency", Name: "fraud-service"}, Message: "observed calling fraud-service, which the contract does not declare"},
				{Code: finding.CodeEvidenceMissing, Severity: finding.SeverityUnknown, Category: finding.CategoryInconclusive, Message: "1 of 5 required checks could not be evaluated from the available evidence"},
			}},
		// Recorded kernel-backed finding: the node agent saw nothing listen on the
		// declared port, and the Kubernetes API agreed, so the evidence ref names
		// both collectors. The dashboard shows it as "seen by: Kubernetes + kernel".
		{Scope: "staging", Kind: "Deployment", Name: "orders-service", Service: "orders-service",
			ResolvedRef: demoRegistry + "orders-service:1.2.0", Compliance: fleet.StatusNonCompliant,
			Coverage: cov(5, 5), EvidenceAt: at(older), ReconciledAt: at(older),
			Labels: map[string]string{"env": "staging", "region": "eu-west-1", "app.kubernetes.io/name": "orders-service"},
			Findings: []finding.Finding{
				{Code: finding.CodeInterfaceAbsent, Severity: finding.SeverityError, Category: finding.CategoryRuntimeDrift, Subject: finding.SubjectRef{Kind: "interface", Name: "http"}, Message: "nothing listens on the declared http interface",
					EvidenceRefs: []finding.EvidenceRef{{Source: "k8s", ObservedAt: older.Format(time.RFC3339), Collector: "k8s-observer+ebpf", DetectedAt: older.Format(time.RFC3339)}}},
			}},
		// A staging copy still runs 1.1.0 (team/commerce) while production runs
		// 1.2.0 (commerce-team): one service disputed by what runs, the
		// OWNER_CONFLICT the demo keeps, where payments-service and api-gateway
		// only handed over to a new owner.
		{Scope: "staging-eu", Kind: "Deployment", Name: "orders-service", Service: "orders-service",
			ResolvedRef: demoRegistry + "orders-service:1.1.0", Compliance: fleet.StatusUnknown,
			Coverage: cov(2, 5), EvidenceAt: at(older), ReconciledAt: at(older),
			Labels: map[string]string{"env": "staging", "region": "eu-west-1", "app.kubernetes.io/name": "orders-service"}},
		{Scope: "prod", Kind: "Deployment", Name: "auth-service", Service: "auth-service", Compliance: fleet.StatusUnknown,
			Coverage: cov(1, 5), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels:          prod(map[string]string{"app.kubernetes.io/name": "auth-service"}),
			ObservedRuntime: map[string]any{"replicas": 3},
			Findings: []finding.Finding{
				{Code: finding.CodeEvidenceInsufficient, Severity: finding.SeverityUnknown, Category: finding.CategoryInconclusive, Message: "the collector could not read the running interface, so compliance is unknown rather than clean"},
			}},
		{Scope: "staging", Kind: "Deployment", Name: "fraud-service", Service: "fraud-service", Compliance: fleet.StatusCompliant,
			Coverage: cov(5, 5), EvidenceAt: at(stale), ReconciledAt: at(stale),
			Labels: map[string]string{"env": "staging", "region": "eu-west-1", "app.kubernetes.io/name": "fraud-service"}},
		{Scope: "prod", Kind: "Deployment", Name: "frontend", Service: "frontend", Compliance: fleet.StatusCompliant,
			ResolvedRef: demoRegistry + "frontend:1.0.0", Coverage: cov(3, 3), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels:          prod(map[string]string{"app.kubernetes.io/name": "frontend"}),
			ObservedRuntime: map[string]any{"replicas": 2, "ingress": "https://shop.example.com"}},
		{Scope: "prod", Kind: "Deployment", Name: "api-gateway", Service: "api-gateway", Compliance: fleet.StatusCompliant,
			ResolvedRef: demoRegistry + "api-gateway:1.2.0", Coverage: cov(4, 4), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels:          prod(map[string]string{"app.kubernetes.io/name": "api-gateway", "tier": "edge"}),
			ObservedRuntime: map[string]any{"replicas": 3, "ingress": "https://api.example.com"}},
		// Never observed: the cluster inventory knows this workload exists, but no
		// evaluation evidence was ever collected for it. That is a third state, not a
		// stale one -- absence of evidence is not evidence of absence, and the freshness
		// bar is only a partition once "never looked" has its own bucket.
		{Scope: "prod", Kind: "Deployment", Name: "notification-worker", Service: "notification-worker",
			Compliance: fleet.StatusUnknown,
			Labels:     prod(map[string]string{"app.kubernetes.io/name": "notification-worker"})},
		// notification-worker also runs in staging, an environment none of its
		// delivery Stages covers (its one Stage is in prod).
		{Scope: "staging", Kind: "Deployment", Name: "notification-worker", Service: "notification-worker",
			Compliance: fleet.StatusUnknown,
			Labels:     map[string]string{"env": "staging", "region": "eu-west-1", "app.kubernetes.io/name": "notification-worker", fleet.TargetEnvironmentLabel: "staging"}},
		{Scope: "prod", Kind: "CronJob", Name: "audit-log", Service: "audit-log", Compliance: fleet.StatusCompliant,
			Coverage: cov(2, 2), EvidenceAt: at(older), ReconciledAt: at(older),
			Labels:          prod(map[string]string{"app.kubernetes.io/name": "audit-log", fleet.TargetEnvironmentLabel: story.Prod}),
			ObservedRuntime: map[string]any{"schedule": "*/15 * * * *", "lastSuccessful": older.Format(time.RFC3339)}},
		// checkout 1.9.0 in the story's test environment, the one story workload the
		// cluster reports: its target page offers what its delivery Stage allows.
		{Scope: "test", Kind: "Deployment", Name: "checkout", Service: "checkout", Domain: story.Domain, Compliance: fleet.StatusCompliant,
			ResolvedRef: "oci://" + story.Domain + "/checkout:1.9.0", Coverage: cov(3, 3), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels: map[string]string{"app.kubernetes.io/name": "checkout", fleet.TargetEnvironmentLabel: story.Test}},
		// payments in the story's prod, back on 2.3.0 after the CHG-2041 incident: the
		// delivery tour plans an undo from its target page under that ticket.
		{Scope: story.Prod, Kind: "Deployment", Name: "payments", Service: "payments", Domain: story.Domain, Compliance: fleet.StatusCompliant,
			ResolvedRef: "oci://" + story.Domain + "/payments:2.3.0", Coverage: cov(3, 3), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels: map[string]string{"app.kubernetes.io/name": "payments", fleet.TargetEnvironmentLabel: story.Prod}},
		// The synthetic catalog and logistics domains. fulfillment-service running in
		// prod is what makes inventory-service 3.0.0, which stops promising the dedupe
		// window fulfillment-service requires, a blocked change rather than an
		// unverified one in Change analysis.
		{Scope: "prod", Kind: "StatefulSet", Name: "inventory-service", Service: "inventory-service", Compliance: fleet.StatusCompliant,
			ResolvedRef: demoRegistry + "inventory-service:2.1.0", Coverage: cov(5, 5), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels:          prod(map[string]string{"app.kubernetes.io/name": "inventory-service", "tier": "critical"}),
			ObservedRuntime: map[string]any{"replicas": 3, "image": demoRegistry + "inventory-service:2.1.0"}},
		{Scope: "prod", Kind: "Deployment", Name: "fulfillment-service", Service: "fulfillment-service", Compliance: fleet.StatusCompliant,
			ResolvedRef: demoRegistry + "fulfillment-service:1.1.0", Coverage: cov(4, 4), EvidenceAt: at(older), ReconciledAt: at(older),
			Labels: prod(map[string]string{"app.kubernetes.io/name": "fulfillment-service"})},
		{Scope: "prod", Kind: "Deployment", Name: "catalog-service", Service: "catalog-service", Compliance: fleet.StatusCompliant,
			ResolvedRef: demoRegistry + "catalog-service:1.1.0", Coverage: cov(3, 3), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels: prod(map[string]string{"app.kubernetes.io/name": "catalog-service"})},
		// One of the targets whose Pacto carries the environment label, so its
		// page names the environment it runs in and checks it against the revision's
		// declared production values. audit-log in prod (the environment of its
		// delivery Stage, matched by exact name) and notification-worker in staging
		// carry it too; every other target reads Unknown.
		{Scope: "prod", Kind: "Deployment", Name: "storefront-web", Service: "storefront-web", Compliance: fleet.StatusCompliant,
			ResolvedRef: demoRegistry + "storefront-web:1.3.0", Coverage: cov(4, 4), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels:          prod(map[string]string{"app.kubernetes.io/name": "storefront-web", "tier": "edge", fleet.TargetEnvironmentLabel: "production"}),
			ObservedRuntime: map[string]any{"replicas": 6, "ingress": "https://shop.example.com"}},
		// The same revision in canary, an environment storefront-web 1.3.0 does not
		// declare: the service page lists it as observed, not declared.
		{Scope: "prod", Kind: "Deployment", Name: "storefront-web-canary", Service: "storefront-web", Compliance: fleet.StatusCompliant,
			ResolvedRef: demoRegistry + "storefront-web:1.3.0", Coverage: cov(4, 4), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels:          prod(map[string]string{"app.kubernetes.io/name": "storefront-web", "tier": "edge", fleet.TargetEnvironmentLabel: "canary"}),
			ObservedRuntime: map[string]any{"replicas": 1, "ingress": "https://canary.shop.example.com"}},
		{Scope: "prod", Kind: "Deployment", Name: "pricing-service", Service: "pricing-service", Compliance: fleet.StatusUnknown,
			ResolvedRef: demoRegistry + "pricing-service:1.1.0", Coverage: cov(2, 4), EvidenceAt: at(stale), ReconciledAt: at(stale),
			Labels: prod(map[string]string{"app.kubernetes.io/name": "pricing-service"})},
		{Scope: "staging", Kind: "Deployment", Name: "search-indexer", Service: "search-indexer", Compliance: fleet.StatusUnknown,
			Labels: map[string]string{"env": "staging", "region": "eu-west-1", "app.kubernetes.io/name": "search-indexer"}},
		// Fresh evidence and a health check that answered and reports it is
		// failing: an observed operational problem, kept apart from the targets
		// Pacto cannot see. Compliance stays Unknown (HEALTH_FAILING is Family 2).
		{Scope: "prod", Kind: "Deployment", Name: "gift-cards", Service: "gift-cards", Compliance: fleet.StatusUnknown,
			ResolvedRef: demoRegistry + "gift-cards:0.1.0", Coverage: cov(2, 3), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels: prod(map[string]string{"app.kubernetes.io/name": "gift-cards"}),
			Findings: []finding.Finding{
				{Code: finding.CodeHealthFailing, Severity: finding.SeverityUnknown, Category: finding.CategoryInconclusive,
					Subject: finding.SubjectRef{Kind: "capability", Name: "health"}, Message: `capability "health" answered but reports it is not healthy`},
			}},
		// The postgresql offering's two providers, the same facts
		// fleet-targets.yaml carries. postgresql serves production with fresh
		// evidence (available) and staging only with stale evidence (unknown);
		// postgresql-ha runs in production only. ticketing-service 1.2.0 in
		// staging reports it cannot reach postgresql-ha, so postgresql-ha reads
		// unavailable there (inferred) while the offering reads unknown: an
		// unknown provider may be available, so it outranks a confirmed outage.
		{Scope: "prod", Kind: "StatefulSet", Name: "postgresql", Service: "postgresql", Compliance: fleet.StatusCompliant,
			ResolvedRef: demoRegistry + "postgresql:16.1.0", Coverage: cov(2, 2), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels: prod(map[string]string{"app.kubernetes.io/name": "postgresql", fleet.TargetEnvironmentLabel: "production"})},
		{Scope: "staging", Kind: "StatefulSet", Name: "postgresql", Service: "postgresql", Compliance: fleet.StatusCompliant,
			ResolvedRef: demoRegistry + "postgresql:16.1.0", Coverage: cov(2, 2), EvidenceAt: at(stale), ReconciledAt: at(stale),
			Labels: map[string]string{"env": "staging", "region": "eu-west-1", "app.kubernetes.io/name": "postgresql", fleet.TargetEnvironmentLabel: "staging"}},
		{Scope: "prod", Kind: "StatefulSet", Name: "postgresql-ha", Service: "postgresql-ha", Compliance: fleet.StatusCompliant,
			ResolvedRef: demoRegistry + "postgresql-ha:1.0.0", Coverage: cov(2, 2), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels: prod(map[string]string{"app.kubernetes.io/name": "postgresql-ha", fleet.TargetEnvironmentLabel: "production"})},
		{Scope: "staging", Kind: "Deployment", Name: "ticketing-service", Service: "ticketing-service", Compliance: fleet.StatusNonCompliant,
			ResolvedRef: demoRegistry + "ticketing-service:1.2.0", Coverage: cov(4, 4), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels: map[string]string{"env": "staging", "region": "eu-west-1", "app.kubernetes.io/name": "ticketing-service", fleet.TargetEnvironmentLabel: "staging"},
			Findings: []finding.Finding{
				{Code: finding.CodeDependencyUnreachable, Severity: finding.SeverityError, Category: finding.CategoryRuntimeDrift, Subject: finding.SubjectRef{Kind: "dependency", Name: "postgresql-ha"}, Message: `dependency "postgresql-ha" is unreachable`},
			}},
	}
}

// demoProvisioning is what provider adapters report about the resources six
// consumers asked for. examples/demo/fleet-targets.yaml carries five of the six
// reports: catalog-service 1.3.0's database is Ready in production, the
// adapter could not observe orders-service's postgresql, so that use reads
// Unknown, the mail adapter failed to provision notification-worker 1.0.0's
// email-provider, a provider that declares no offering, so the resource reads
// Failed and raises PROVISIONING_FAILED, the platform adapter could not
// provision ticketing-service 1.2.0's postgresql-ha database in staging, so
// that request reads Failed, and the security team's scanner adapter reports
// warehouse-scanner 1.1.0's security scan Ready. The sixth, the scanner's
// report on returns-service 1.2.0, is the only signed one, so it cannot live
// in the unsigned file; the dashboard says which reports are signed. They are
// dated off the demo clock, so they stay current; the file's fixed date reads
// stale instead.
func demoProvisioning(src *EmbedSource) []fleet.RawProvisioning {
	at := demoNow().Add(-30 * time.Minute)
	return []fleet.RawProvisioning{
		{Service: "catalog-service", Digest: demoDigest(src, "catalog-service", "1.3.0"), Kind: "configuration", Name: "database",
			Environment: "production", Provider: "platform-db", ExternalRef: "databases/catalog-production", Phase: string(evidence.PhaseReady),
			Outcome: string(evidence.Observed), Producer: "platform-adapter", ObservedAt: at},
		{Service: "orders-service", Digest: demoDigest(src, "orders-service", "1.2.1"), Kind: "dependency", Name: "postgresql",
			Outcome: string(evidence.Insufficient), Producer: "platform-adapter", ObservedAt: at},
		{Service: "ticketing-service", Digest: demoDigest(src, "ticketing-service", "1.2.0"), Kind: "dependency", Name: "postgresql-ha",
			Environment: "staging", Provider: "platform-db", ExternalRef: "databases/ticketing-staging", Phase: string(evidence.PhaseFailed),
			Outcome: string(evidence.Observed), Producer: "platform-adapter", ObservedAt: at},
		{Service: "notification-worker", Digest: demoDigest(src, "notification-worker", "1.0.0"), Kind: "dependency", Name: "email-provider",
			Provider: "mail-adapter", ExternalRef: "senders/notifications", Phase: string(evidence.PhaseFailed),
			Outcome: string(evidence.Observed), Producer: "mail-adapter", ObservedAt: at},
		{Service: "returns-service", Digest: demoDigest(src, "returns-service", "1.2.0"), Kind: "configuration", Name: "security",
			Environment: "production", Provider: "scanner", ExternalRef: "scans/returns-service", Phase: string(evidence.PhaseReady),
			Outcome: string(evidence.Observed), Producer: "security-adapter", ProducerKeyID: "security-adapter-2026", ObservedAt: at, Verified: true},
		{Service: "warehouse-scanner", Digest: demoDigest(src, "warehouse-scanner", "1.1.0"), Kind: "configuration", Name: "security",
			Environment: "production", Provider: "scanner", ExternalRef: "scans/warehouse-scanner", Phase: string(evidence.PhaseReady),
			Outcome: string(evidence.Observed), Producer: "security-adapter", ObservedAt: at},
	}
}

// demoUsage is what the platform adapter reports about the usage and cost of
// catalog-service 1.3.0's database in production. It is marked Verified because
// the WASM demo stands in for the Evidence Server, which accepts only signed
// usage reports from a key granted ResourceUsage, so the dashboard shows the
// figures as reported. examples/demo/fleet-targets.yaml carries the same report
// unsigned, so the CLI demo reads the same resource's usage unknown. The mail
// adapter's last usage report on notification-worker's email-provider is
// signed but two days old, older than the demo's freshness window, so its
// figures read unknown and stay apart as reported, never as current.
func demoUsage(src *EmbedSource) []fleet.RawUsage {
	at := demoNow().Add(-30 * time.Minute)
	old := demoNow().Add(-48 * time.Hour)
	return []fleet.RawUsage{
		{Service: "notification-worker", Digest: demoDigest(src, "notification-worker", "1.0.0"), Kind: "dependency", Name: "email-provider",
			Provider: "mail-adapter", ExternalRef: "senders/notifications",
			Metrics: []evidence.UsageMetric{{Name: "emails-sent", Value: 120000}},
			Cost:    &evidence.UsageCost{Amount: "48.00", Currency: "USD", Period: "P30D"},
			Outcome: string(evidence.Observed), Producer: "mail-adapter", ProducerKeyID: "demo-mail-adapter", ObservedAt: old, Verified: true},
		{Service: "catalog-service", Digest: demoDigest(src, "catalog-service", "1.3.0"), Kind: "configuration", Name: "database",
			Environment: "production", Provider: "platform-db", ExternalRef: "databases/catalog-production",
			Metrics: []evidence.UsageMetric{{Name: "storage-used", Value: 31, Unit: "Gi"}},
			Cost:    &evidence.UsageCost{Amount: "212.40", Currency: "USD", Period: "P30D"},
			Outcome: string(evidence.Observed), Producer: "platform-adapter", ProducerKeyID: "demo-platform-adapter", ObservedAt: at, Verified: true},
	}
}

// demoDigest returns the content digest of an embedded revision, so a target can
// claim an exact match against something that actually exists.
func demoDigest(src *EmbedSource, name, version string) string {
	if e := src.byName[name][version]; e != nil {
		return "sha256:" + e.hash
	}
	return ""
}

// demoCorroboratedTargets is the telemetry collector's view of two targets the
// cluster collector also reported. Every identity-bearing field either agrees or is
// left empty, so Build MERGES them and the targets carry two sources -- corroborated
// observation, not a conflict. (A disagreement here would quarantine the target,
// which is the correct behaviour and a state the demo deliberately does not enter:
// a fixture that ships a self-contradicting fleet teaches the wrong lesson.)
//
// Its observations are deliberately OLDER than the cluster collector's, because the
// fresher contribution owns the evaluation fields. Telemetry does not evaluate
// compliance -- it watches traffic -- so a fresher telemetry contribution would
// replace a real verdict with an empty one and the demo would show every target as
// unknown. Corroboration here means provenance, not a second opinion on compliance.
func demoCorroboratedTargets() []fleet.RawTarget {
	seen := demoNow().Add(-3 * time.Hour)
	at := func(t time.Time) *time.Time { return &t }
	return []fleet.RawTarget{
		{Scope: "prod", Kind: "Deployment", Name: "payments-service", Service: "payments-service", EvidenceAt: at(seen)},
		{Scope: "prod", Kind: "Deployment", Name: "orders-service", Service: "orders-service", EvidenceAt: at(seen)},
	}
}

// demoInferredServices are four workloads the demo cluster runs with no
// contract: a Deployment pinned by digest and a second Deployment of the same
// Argo CD Application, a CronJob a plain Helm release installs and a
// DaemonSet a Flux HelmRelease installs in another namespace. The groups and
// repositories are what a live read with Argo CD, Flux and Helm would give.
func demoInferredServices() []fleet.RawInferredService {
	at := demoNow().Add(-15 * time.Minute)
	const pod = "spec.template.spec.containers[name="
	const job = "spec.jobTemplate.spec.template.spec.containers[name="
	shopApp := &fleet.InferredGroup{Rule: workloadmanager.ArgoCD, Namespace: "shop", Name: "shop", From: "annotation argocd.argoproj.io/tracking-id"}
	shopRepo := []fleet.InferredRepository{{URL: "https://github.com/example/shop", Path: "deploy/legacy",
		From: "Application shop/shop spec.source.repoURL", Namespace: "shop", Kind: "Application"}}
	return []fleet.RawInferredService{
		{
			Namespace: "shop", Kind: "Deployment", Name: "legacy-billing", ObservedAt: &at,
			Image: &fleet.InferredValue{
				Value: "registry.example.com/shop/legacy-billing@sha256:4f1c2a9e8b7d6c5f4e3d2c1b0a9f8e7d6c5b4a39281706f5e4d3c2b1a0f9e8d7",
				From:  pod + "billing].image",
			},
			Ports:        []fleet.InferredPort{{Port: 8080, Name: "http", Protocol: "TCP", From: pod + "billing].ports[0].containerPort"}},
			Probes:       []fleet.InferredProbe{{Kind: "readiness", Path: "/healthz", Port: "http", From: pod + "billing].readinessProbe"}},
			Group:        shopApp,
			Repositories: shopRepo,
		},
		{
			Namespace: "shop", Kind: "Deployment", Name: "billing-worker", ObservedAt: &at,
			Image:        &fleet.InferredValue{Value: "registry.example.com/shop/billing-worker:1.9.0", From: pod + "worker].image"},
			Group:        shopApp,
			Repositories: shopRepo,
		},
		{
			Namespace: "shop", Kind: "CronJob", Name: "nightly-export", ObservedAt: &at,
			Image: &fleet.InferredValue{Value: "registry.example.com/shop/exporter:2.3.1", From: job + "exporter].image"},
			Group: &fleet.InferredGroup{Rule: workloadmanager.Helm, Namespace: "shop", Name: "exporter",
				From: "annotations meta.helm.sh/release-name and release-namespace"},
		},
		{
			Namespace: "observability", Kind: "DaemonSet", Name: "log-shipper", ObservedAt: &at,
			Image:  &fleet.InferredValue{Value: "fluent/fluent-bit:3.1.4", From: pod + "fluent-bit].image"},
			Ports:  []fleet.InferredPort{{Port: 2020, Name: "metrics", Protocol: "TCP", From: pod + "fluent-bit].ports[0].containerPort"}},
			Probes: []fleet.InferredProbe{{Kind: "liveness", Path: "/api/v1/health", Port: "2020", From: pod + "fluent-bit].livenessProbe"}},
			Group: &fleet.InferredGroup{Rule: workloadmanager.FluxHelmRelease, Namespace: "observability", Name: "log-shipper",
				From: "labels helm.toolkit.fluxcd.io/name and namespace"},
			Repositories: []fleet.InferredRepository{{URL: "https://github.com/example/platform",
				From: "GitRepository flux-system/platform spec.url", Namespace: "flux-system", Kind: "GitRepository"}},
		},
	}
}

// demoCatalog is the default catalog with the demo organization's extension:
// what the demo fleet classifies with and newsletter-service's record was
// completed with. The extension is embedded, so the demo reports it unsigned
// rather than inventing a verification it never ran.
func demoCatalog() (*externalcatalog.Catalog, error) {
	return externalcatalog.Assemble(externalcatalog.DefaultRef(), demoExternalCatalog, externalcatalog.Ref{
		Origin: externalcatalog.OriginOrganization, Repository: "demo (embedded)", Digest: externalcatalog.Digest(demoExternalCatalog),
		Copy: externalcatalog.CopyEmbedded, Signature: externalcatalog.SignatureUnknown,
		Reason: "the demo catalog is embedded in the demo; it is not signed",
	})
}
