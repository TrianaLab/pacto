/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	unversioned "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

// dependencyReachability merges the Kubernetes reachability verdict with the kernel's connection verdict,
// applies stabilization and emits the observation and window update (spec section 4). A recent kernel
// success makes the dependency reachable, a recent failure newer than any success makes it a windowed
// negative and kernel silence leaves the Kubernetes verdict standing. A kernel success never overrides a
// Kubernetes negative (no ready endpoint): the two contradict each other, so the result is Insufficient
// while the Kubernetes window keeps running. A stale or failed node only replaces a Kubernetes result that
// is not conclusive anyway.
func dependencyReachability(input CollectInput, subj evidence.SubjectRef, version string, isNegative bool,
	peers []runtimefacts.Address, prov evidence.Provenance, now time.Time) (evidence.Observation, ObservationWindowUpdate) {
	verdict := input.kernel.dependencyVerdict(peers)
	conflict := verdict == runtimefacts.Positive && isNegative
	switch {
	case conflict:
		prov.Collector = collectorKernelMerged
	case verdict == runtimefacts.Positive, verdict == runtimefacts.Negative:
		isNegative = verdict == runtimefacts.Negative
		prov.Collector = collectorKernelMerged
	}

	outcome, updatedWindow := stabilize(input.ObservationWindows["dependency/"+subj.Name], isNegative, now, input.StabilizationWindow)
	update := ObservationWindowUpdate{Kind: kindDependency, Subject: subj.Name, FirstObservedNegativeAt: updatedWindow}
	if conflict {
		outcome = evidence.Insufficient
	}

	if outcome == evidence.Observed {
		return evidence.NewDependencyObservation(subj, evidence.DependencyObservation{Reachable: !isNegative, Version: version}, prov), update
	}
	if verdict == runtimefacts.Stale || verdict == runtimefacts.Failed {
		outcome = verdict.Outcome()
		prov.Collector = collectorKernelMerged
	}
	obs, _ := evidence.NewUnobserved(evidence.DependencyReachable, subj, outcome, prov)
	return obs, update
}

// observeDependenciesDim observes all declared dependencies. Spec section 7.6 / B5.
// ponytail: sibling-CR matching + windowed reachability negatives.
func (o *Observer) observeDependenciesDim(ctx context.Context, input CollectInput, prov evidence.Provenance, now time.Time) ([]evidence.Observation, []ObservationWindowUpdate) {
	var observations []evidence.Observation
	var windowUpdates []ObservationWindowUpdate

	for _, dep := range input.Contract.Dependencies {
		subj := evidence.SubjectRef{Kind: kindDependency, Name: dep.Name}

		// Resolve the dependency to an in-cluster sibling Pacto CR (spec section 7.6).
		target, ambiguous, err := o.resolveDependencyToSibling(ctx, input.Namespace, dep)

		if err != nil {
			// Controller-runtime client error (not a "no match" case) -> COLLECTION_FAILED.
			o.fail("dependencies", err)
			obs, _ := evidence.NewUnobserved(evidence.DependencyReachable, subj, evidence.Failed, prov)
			observations = append(observations, obs)
			continue
		}

		if ambiguous {
			// More than one distinct sibling target -> arbitrary pick would be unsafe -> Insufficient (Unknown).
			obs, _ := evidence.NewUnobserved(evidence.DependencyReachable, subj, evidence.Insufficient, prov)
			observations = append(observations, obs)
			continue
		}

		if target == nil {
			// No reliable sibling match (external / not pacto-managed) -> Unsupported (Unknown), never NonCompliant.
			obs, _ := evidence.NewUnobserved(evidence.DependencyReachable, subj, evidence.Unsupported, prov)
			observations = append(observations, obs)
			continue
		}

		if target.serviceName == "" {
			// Sibling matched but reference-only (empty spec.target.serviceName) -> Insufficient (Unknown).
			obs, _ := evidence.NewUnobserved(evidence.DependencyReachable, subj, evidence.Insufficient, prov)
			observations = append(observations, obs)
			continue
		}

		// Check if the resolved Service is ExternalName -> Unsupported.
		svc := &corev1.Service{}
		err = o.client.Get(ctx, types.NamespacedName{Namespace: target.namespace, Name: target.serviceName}, svc)
		if err != nil {
			if client.IgnoreNotFound(err) != nil {
				// Non-NotFound API error -> COLLECTION_FAILED.
				o.fail("dependencies", fmt.Errorf("failed to get Service %s/%s: %w", target.namespace, target.serviceName, err))
				obs, _ := evidence.NewUnobserved(evidence.DependencyReachable, subj, evidence.Failed, prov)
				observations = append(observations, obs)
				continue
			}
			// Service NotFound -> a negative, apply stabilization. No address is left for the kernel to match.
			obs, update := dependencyReachability(input, subj, target.version, true, nil, prov, now)
			observations = append(observations, obs)
			windowUpdates = append(windowUpdates, update)
			continue
		}

		// Service exists -> check if it's ExternalName.
		if svc.Spec.Type == corev1.ServiceTypeExternalName {
			obs, _ := evidence.NewUnobserved(evidence.DependencyReachable, subj, evidence.Unsupported, prov)
			observations = append(observations, obs)
			continue
		}

		// Count ready endpoints for the dependency Service (protocol-agnostic, using any port).
		readyCount, epSlices, err := o.countReadyEndpointsForService(ctx, target.namespace, target.serviceName)
		if err != nil {
			// Error listing EndpointSlices -> COLLECTION_FAILED.
			o.fail("dependencies", err)
			obs, _ := evidence.NewUnobserved(evidence.DependencyReachable, subj, evidence.Failed, prov)
			observations = append(observations, obs)
			continue
		}

		// We have a reliable ready-endpoint count.
		obs, update := dependencyReachability(input, subj, target.version, readyCount == 0, servicePeers(svc, epSlices), prov, now)
		observations = append(observations, obs)
		windowUpdates = append(windowUpdates, update)
	}

	return observations, windowUpdates
}

// dependencyTarget is the resolved in-cluster target for a dependency. version is the sibling's deployed
// contract version; the seen-set key leaves it empty, so it never splits one target into two.
type dependencyTarget struct {
	namespace   string
	serviceName string
	version     string
}

// resolveDependencyToSibling resolves a contract dependency to an in-cluster Pacto CR (spec section 7.6).
// Returns (nil, false, nil) when no reliable match exists (external / not pacto-managed); (target, false,
// nil) for a single match; (nil, true, nil) when the dependency resolves to more than one DISTINCT
// {namespace, serviceName} target (blue-green with equal resolvedRef) and not exactly one of them
// satisfies dep.Compatibility — an arbitrary pick would risk a wrong verdict, so the caller must treat it
// as Insufficient. A target in the consumer's own namespace wins over the same service in another one: a
// service in test calls the provider in test, never the one in prod.
// Returns an error only for controller-runtime client failures.
func (o *Observer) resolveDependencyToSibling(ctx context.Context, namespace string, dep contract.Dependency) (*dependencyTarget, bool, error) {
	// List all Pacto CRs. For single-namespace manager cache, this only sees CRs in the watched namespace(s).
	var pactoList unversioned.PactoList
	if err := o.client.List(ctx, &pactoList); err != nil {
		return nil, false, fmt.Errorf("failed to list Pacto CRs: %w", err)
	}

	// Match dep.Ref to the sibling's status.contract.resolvedRef, then read the sibling's
	// spec.target.serviceName + namespace for Service coordinates. Compatibility only breaks a tie between
	// several targets; a single target is reported whatever its version (DependencyObservation.Version) and
	// the engine judges it. Distinct matches are counted so an ambiguous resolution is never silently
	// collapsed to a first-match-wins pick.
	var matches []dependencyTarget
	seen := make(map[dependencyTarget]bool)
	for _, sibling := range pactoList.Items {
		if sibling.Status.Contract == nil {
			continue
		}
		if !matchesRef(dep.Ref, sibling.Status.Contract.ResolvedRef) {
			continue
		}
		t := dependencyTarget{namespace: sibling.Namespace, serviceName: sibling.Spec.Target.ServiceName}
		if !seen[t] {
			seen[t] = true
			t.version = sibling.Status.Contract.Version
			matches = append(matches, t)
		}
	}

	if local := slices.DeleteFunc(slices.Clone(matches), func(m dependencyTarget) bool { return m.namespace != namespace }); len(local) > 0 {
		matches = local
	}
	switch len(matches) {
	case 0:
		// No matching sibling CR -> external / not pacto-managed.
		return nil, false, nil
	case 1:
		// The only target, compatible or not: Evaluate judges the reported version against the range.
		return &matches[0], false, nil
	}
	// Several targets (a version rollout side by side): the ones inside the declared range are the ones
	// this dependency can mean. Exactly one of them is unambiguous; none or several is not.
	var compatible []dependencyTarget
	if rng, err := contract.ParseRange(dep.Compatibility); err == nil {
		for _, m := range matches {
			if rng.Contains(m.version) {
				compatible = append(compatible, m)
			}
		}
	}
	if len(compatible) == 1 {
		return &compatible[0], false, nil
	}
	// Ambiguous multi-match -> caller emits EVIDENCE_INSUFFICIENT (spec section 7.6).
	return nil, true, nil
}

// matchesRef reports whether a dependency ref matches a sibling's resolvedRef.
// ponytail: base-ref match ignoring scheme and tag/digest suffix.
func matchesRef(depRef, siblingRef string) bool {
	// Compare the base refs (no oci:// scheme, no tag/digest). A contract writes OCI dependencies as
	// oci://registry/service-pacto[:tag], while the loader stores resolvedRef without the scheme
	// (registry/service-pacto:1.2.3 or registry/service-pacto@sha256:...).
	depBase := stripRefSuffix(depRef)
	siblingBase := stripRefSuffix(siblingRef)
	return depBase == siblingBase
}

// stripRefSuffix removes a trailing :tag or @digest from an OCI ref, ignoring the host:port colon in a
// registry authority, after dropping an oci:// scheme. A '@' digest separator is unambiguous anywhere; a ':' is a tag separator only when
// it appears AFTER the last '/' (so "oci://registry:5000/payments" keeps its full repo path).
func stripRefSuffix(ref string) string {
	ref = strings.TrimPrefix(ref, "oci://")
	if idx := strings.LastIndexByte(ref, '@'); idx >= 0 {
		return ref[:idx]
	}
	if idx := strings.LastIndexByte(ref, ':'); idx > strings.LastIndexByte(ref, '/') {
		return ref[:idx]
	}
	return ref
}

// countReadyEndpointsForService counts ready endpoints for a Service (protocol-agnostic, any port) and
// returns the slices it read, so the kernel merge can match peer addresses without a second list.
// ponytail: reuse EndpointSlice listing logic, count all ready endpoints regardless of port.
func (o *Observer) countReadyEndpointsForService(ctx context.Context, namespace, serviceName string) (int, []discoveryv1.EndpointSlice, error) {
	list := &discoveryv1.EndpointSliceList{}
	err := o.client.List(ctx, list, client.InNamespace(namespace), client.MatchingLabels{
		discoveryv1.LabelServiceName: serviceName,
	})
	if err != nil {
		return 0, nil, fmt.Errorf("failed to list EndpointSlices for Service %s/%s: %w", namespace, serviceName, err)
	}

	// Count all ready endpoints across all slices (protocol-agnostic).
	readyCount := 0
	for _, slice := range list.Items {
		for _, endpoint := range slice.Endpoints {
			if endpoint.Conditions.Ready != nil && *endpoint.Conditions.Ready {
				readyCount++
			}
		}
	}

	// An empty slice list (freshly-created Service) is zero ready, which the caller windows.
	return readyCount, list.Items, nil
}
