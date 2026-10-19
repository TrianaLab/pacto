/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/probe"
)

// observeHealthDim observes the health capability (spec section 7.4). Returns exactly one observation and any
// window updates for the assertion.
// ponytail: discriminated http binding, tier-A direct probe + tier-B READINESS-fallback, windowed 404.
func (o *Observer) observeHealthDim(ctx context.Context, input CollectInput, cap contract.Capability, prov evidence.Provenance, now time.Time) (evidence.Observation, []ObservationWindowUpdate) {
	subj := evidence.SubjectRef{Kind: kindCapability, Name: cap.AssertionKey()} // "health"

	// No binding -> Unsupported.
	if cap.Binding == nil {
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return obs, nil
	}

	// Non-HTTP binding -> Unsupported (grpc not implemented).
	if cap.Binding.Type != contract.CapabilityBindingHTTP {
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return obs, nil
	}

	// Resolve the owning interface to its Service port (B4 reuse).
	binding := findInterfaceBinding(input.InterfaceBindings, cap.Binding.Interface)
	if binding == nil {
		// Owning interface has no binding -> Unsupported (cannot resolve probe target).
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return obs, nil
	}

	// Resolve the Service port number.
	svc := &corev1.Service{}
	err := o.client.Get(ctx, types.NamespacedName{Namespace: input.Namespace, Name: input.ServiceName}, svc)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			// Non-NotFound API error -> Failed.
			o.fail("health", err)
			obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Failed, prov)
			return obs, nil
		}
		// Service NotFound -> Unsupported (cannot probe).
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return obs, nil
	}

	boundPort, found := findServicePort(svc, binding.ServicePort)
	if !found {
		// Bound port not found in Service -> Unsupported.
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return obs, nil
	}

	// Tier A: active in-cluster probe (opt-in via EnableProbing — SSRF surface). When disabled, leave the
	// zero-value (Reachable=false) result so handleHealthProbeResult routes straight to passive Tier-B.
	var result probe.Result
	if input.EnableProbing {
		// SSRF-safe URL via probe.URL — INV-6.
		result = o.prober.Probe(ctx, probeURL(input.ServiceName, input.Namespace, boundPort.Port, cap.Binding.Path))
	}

	// targetPort is the health capability's OWNING binding port; the Tier-B readiness fallback must check
	// that same binding, never an arbitrary InterfaceBindings[0].
	return o.handleHealthProbeResult(ctx, result, subj, cap.AssertionKey(), input, prov, now, boundPort.Port, cap.Binding.Path)
}

// handleHealthProbeResult interprets the prober result and returns the appropriate observation/window-update.
// The prober is injected (endpointProber), so this needs no HTTP to test.
// ponytail: tier-A probe result -> satisfied/404-windowed/insufficient, then tier-B fallback.
func (o *Observer) handleHealthProbeResult(
	ctx context.Context,
	result probe.Result,
	subj evidence.SubjectRef,
	assertionKey string,
	input CollectInput,
	prov evidence.Provenance,
	now time.Time,
	owningServicePort int32,
	declaredPath string,
) (evidence.Observation, []ObservationWindowUpdate) {
	// A satisfied/positive health observation must CLEAR any stale negative window (mirrors the interface and
	// dependency dimensions), so that after recovery a later single declared-path 404 starts a FRESH window and
	// is windowed as Unknown, never treated as beyond-window -> premature CAPABILITY_ABSENT (I7).
	clearWindow := resetWindow(kindCapability, assertionKey)

	if result.Reachable {
		// Probe succeeded.
		if result.StatusCode >= 200 && result.StatusCode < 400 {
			// 2xx/3xx -> satisfied (Tier A) -> clear any stale negative window.
			return evidence.NewCapabilityObserved(subj, true, prov), clearWindow
		}

		if result.StatusCode == 404 {
			// Declared-path 404 -> windowed negative (spec section 7.4): confirmed absent beyond the window.
			return windowed(input, kindCapability, assertionKey, evidence.NewCapabilityObserved(subj, false, prov), now)
		}

		// 5xx/501/405 -> endpoint exists but unhealthy/not-serving-GET -> Insufficient (not absent).
		observation, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Insufficient, prov)
		return observation, nil
	}

	// Active probe unreachable or disabled -> passive Tier B (READINESS-probe fallback).
	hasReadinessProbe, podReady := o.checkReadinessProbeFallbackFromInput(ctx, input, owningServicePort, declaredPath)
	switch {
	case hasReadinessProbe && podReady:
		// Tier B satisfied (lower confidence) -> clear any stale negative window.
		return evidence.NewCapabilityObserved(subj, true, prov), clearWindow
	case !hasReadinessProbe:
		// No usable Tier-B evidence (liveness-only / no-probe / tcpSocket / exec / unresolvable target)
		// -> EVIDENCE_INSUFFICIENT (spec section 7.4), never COLLECTION_FAILED from missing evidence.
		observation, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Insufficient, prov)
		return observation, nil
	default:
		// Readiness probe declared but Pod not Ready -> Failed.
		observation, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Failed, prov)
		return observation, nil
	}
}

// checkReadinessProbeFallbackFromInput resolves the Service and runs the Tier-B readiness fallback against
// the health capability's OWNING binding port (already resolved by observeHealthDim), never an arbitrary
// InterfaceBindings[0]. Using the wrong binding's port would check the wrong container and could report
// SATISFIED when only a sidecar is Ready.
func (o *Observer) checkReadinessProbeFallbackFromInput(ctx context.Context, input CollectInput, servicePort int32, declaredPath string) (bool, bool) {
	svc := &corev1.Service{}
	if err := o.client.Get(ctx, types.NamespacedName{Namespace: input.Namespace, Name: input.ServiceName}, svc); err != nil {
		if client.IgnoreNotFound(err) != nil {
			o.fail("health", err)
		}
		return false, false
	}
	return o.checkReadinessProbeFallback(ctx, input, svc, servicePort, declaredPath)
}

// checkReadinessProbeFallback checks if the workload has an httpGet READINESS probe (not liveness) configured
// on the container backing the Service target port, on the declared health path when the contract declares
// one, and if the Pod is Ready. A probe on another path says nothing about the declared endpoint, so it
// counts as no usable probe. Returns (hasReadinessProbe, podReady).
// ponytail: sidecar-safe container selection via Service target port -> pod container port match.
func (o *Observer) checkReadinessProbeFallback(ctx context.Context, input CollectInput, svc *corev1.Service, servicePort int32, declaredPath string) (bool, bool) {
	podSpec := o.getPodSpec(ctx, input)
	if podSpec == nil {
		return false, false
	}

	targetPort, ok := o.resolveTargetPort(svc, servicePort, podSpec)
	if !ok {
		return false, false
	}

	if !o.hasHTTPGetReadinessProbe(podSpec, targetPort, declaredPath) {
		return false, false
	}

	readyCount, err := o.countReadyEndpoints(ctx, input.Namespace, input.ServiceName, intstr.FromInt32(servicePort))
	if err != nil {
		o.fail("health", err)
	}
	if err != nil || readyCount <= 0 {
		return true, false
	}

	return true, true
}

// resolveTargetPort resolves the Service port numbered servicePort to the container port it targets in podSpec.
func (o *Observer) resolveTargetPort(svc *corev1.Service, servicePort int32, podSpec *corev1.PodSpec) (int32, bool) {
	p, found := findServicePort(svc, intstr.FromInt32(servicePort))
	if !found {
		return 0, false
	}
	return targetPort(p, podSpec.Containers)
}

func (o *Observer) hasHTTPGetReadinessProbe(podSpec *corev1.PodSpec, targetPort int32, declaredPath string) bool {
	for _, c := range podSpec.Containers {
		exposesPort := false
		for _, cp := range c.Ports {
			if cp.ContainerPort == targetPort {
				exposesPort = true
				break
			}
		}
		if exposesPort && c.ReadinessProbe != nil && c.ReadinessProbe.HTTPGet != nil &&
			(declaredPath == "" || c.ReadinessProbe.HTTPGet.Path == declaredPath) {
			return true
		}
	}
	return false
}
