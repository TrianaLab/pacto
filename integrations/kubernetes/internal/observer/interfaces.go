/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/runtimefacts"
)

// observeInterfacesDim observes all declared interfaces. Spec section 7.3 / B1 + B4.
func (o *Observer) observeInterfacesDim(ctx context.Context, input CollectInput, prov evidence.Provenance, now time.Time) ([]evidence.Observation, []ObservationWindowUpdate) {
	var observations []evidence.Observation
	var windowUpdates []ObservationWindowUpdate
	routes := &httpRoutes{}

	for _, iface := range input.Contract.Interfaces {
		subj := evidence.SubjectRef{Kind: "interface", Name: iface.Name}

		// Find the binding for this interface.
		binding := findInterfaceBinding(input.InterfaceBindings, iface.Name)
		if binding == nil {
			// Optional name-match discovery (spec section 7.3 / B4): resolve an unbound interface's Service
			// port by matching a port named after the interface — positive availability assist ONLY (INV-4),
			// never INTERFACE_ABSENT.
			if input.EnableInterfaceNameMatchDiscovery && o.interfaceSatisfiedByNameMatch(ctx, input, iface.Name) {
				observations = append(observations, evidence.NewInterfaceObserved(subj, iface.Type, true, prov))
				continue
			}
			// No binding -> OBSERVATION_UNSUPPORTED (spec section 7.3).
			obs, _ := evidence.NewUnobserved(evidence.InterfaceObserved, subj, evidence.Unsupported, prov)
			observations = append(observations, obs)
			continue
		}

		// Read the Service + EndpointSlices to determine ready endpoints.
		readyCount, svc, svcPort, ready, err := o.readyEndpoints(ctx, input.Namespace, input.ServiceName, binding.ServicePort)

		if err != nil {
			// API error (non-NotFound) -> COLLECTION_FAILED.
			o.fail("interfaces", err)
			obs, _ := evidence.NewUnobserved(evidence.InterfaceObserved, subj, evidence.Failed, prov)
			observations = append(observations, obs)
			continue
		}

		if readyCount == endpointsServiceNotFound {
			// Bound interface whose Service is NotFound -> emit NO observation so the engine infers
			// EVIDENCE_MISSING (spec section 7.3, mirroring the workload dimension's NotFound handling).
			continue
		}

		if readyCount == endpointsUnmappable {
			// Selector-less / ExternalName / port-unmappable -> conclusive EVIDENCE_INSUFFICIENT (spec section 7.3).
			obs, _ := evidence.NewUnobserved(evidence.InterfaceObserved, subj, evidence.Insufficient, prov)
			observations = append(observations, obs)
			continue
		}

		// We have a mappable port and a ready-endpoint count.
		obs, update := o.interfaceAvailability(ctx, input, iface, ready, svc, svcPort, routes, prov, now)
		observations = append(observations, obs)
		windowUpdates = append(windowUpdates, update)
	}

	return observations, windowUpdates
}

// interfaceAvailability merges the Kubernetes ready-endpoint verdict with the kernel's listener verdict and
// runs the result through the interface's stabilization window (spec section 4). Only a Kubernetes positive
// consults the kernel: zero ready endpoints leaves no ready backing pod for the kernel to speak for.
func (o *Observer) interfaceAvailability(ctx context.Context, input CollectInput, iface contract.Interface, ready []discoveryv1.Endpoint,
	svc *corev1.Service, svcPort corev1.ServicePort, routes *httpRoutes, prov evidence.Provenance, now time.Time) (evidence.Observation, ObservationWindowUpdate) {
	subj := evidence.SubjectRef{Kind: "interface", Name: iface.Name}
	update := ObservationWindowUpdate{Kind: "interface", Subject: iface.Name}

	isNegative := len(ready) == 0
	if !isNegative {
		verdict := o.kernelInterfaceVerdict(ctx, input, svcPort, ready)
		if verdict != runtimefacts.Silent {
			prov.Collector = collectorKernelMerged
		}
		switch verdict {
		case runtimefacts.Negative:
			// Endpoints say ready but no fully observed ready pod listens on the target port.
			isNegative = true
		case runtimefacts.Partial, runtimefacts.Failed, runtimefacts.Stale:
			// The kernel cannot confirm the listener and cannot rule it out: never a positive, never a
			// negative, and the window restarts from the next conclusive reading.
			obs, _ := evidence.NewUnobserved(evidence.InterfaceObserved, subj, verdict.Outcome(), prov)
			return obs, update
		}
	}

	outcome, updatedWindow := stabilize(input.ObservationWindows["interface/"+iface.Name], isNegative, now, input.StabilizationWindow)
	update.FirstObservedNegativeAt = updatedWindow
	if outcome != evidence.Observed {
		// Insufficient (within window or first negative) -> emit Insufficient.
		obs, _ := evidence.NewUnobserved(evidence.InterfaceObserved, subj, outcome, prov)
		return obs, update
	}

	// Beyond window (or positive) -> emit Observed with the appropriate Present flag. The facts that only
	// mean something for a served port (protocol, exposure) are read for a present interface only.
	payload := evidence.InterfaceObservation{Type: iface.Type, Present: !isNegative}
	if payload.Present {
		payload.Exposure = o.interfaceExposure(ctx, routes, svc, svcPort)
		if input.EnableProbing {
			// SSRF-safe: the prober dials only the trusted Service DNS name and port (INV-6).
			payload.Protocol = o.prober.ProbeProtocol(ctx, serviceAddr(input.ServiceName, input.Namespace, svcPort.Port))
		}
	}
	return evidence.NewInterfaceObservation(subj, payload, prov), update
}

// interfaceSatisfiedByNameMatch attempts name-match discovery (spec section 7.3 / B4) for an unbound
// interface: it resolves the Service port whose Name equals the interface name and reports whether at
// least one Ready endpoint covers it. Positive-availability assist ONLY (INV-4) — it can only report
// satisfied, never absent, so a NotFound / unmappable / zero-ready outcome yields false.
func (o *Observer) interfaceSatisfiedByNameMatch(ctx context.Context, input CollectInput, ifaceName string) bool {
	readyCount, err := o.countReadyEndpoints(ctx, input.Namespace, input.ServiceName, intstr.FromString(ifaceName))
	if err != nil {
		o.fail("interfaces", err)
	}
	return err == nil && readyCount >= 1
}

// findInterfaceBinding locates the binding for a given interface name.
func findInterfaceBinding(bindings []InterfaceBinding, ifaceName string) *InterfaceBinding {
	for i := range bindings {
		if bindings[i].Interface == ifaceName {
			return &bindings[i]
		}
	}
	return nil
}

// Sentinels for countReadyEndpoints. They distinguish the two non-count outcomes so the interfaces
// producer can route them to the correct emission (spec section 7.3): a NotFound Service means the engine
// should infer EVIDENCE_MISSING, whereas an unmappable Service is a conclusive EVIDENCE_INSUFFICIENT.
const (
	endpointsServiceNotFound = -1 // the Service does not exist
	endpointsUnmappable      = -2 // selector-less / ExternalName / port not present in the Service
)

// countReadyEndpoints reads the Service and EndpointSlices for the given service and port, then returns the
// count of Ready endpoints covering that port. Returns endpointsServiceNotFound if the Service is NotFound,
// endpointsUnmappable if the Service is selector-less / ExternalName / the port is absent, or a non-NotFound
// API error as error.
func (o *Observer) countReadyEndpoints(ctx context.Context, namespace, serviceName string, servicePort intstr.IntOrString) (int, error) {
	ready, _, _, _, err := o.readyEndpoints(ctx, namespace, serviceName, servicePort)
	return ready, err
}

// readyEndpoints is countReadyEndpoints that also returns the Service, the resolved Service port and the
// ready endpoints covering it, which are only meaningful for a non-negative count.
func (o *Observer) readyEndpoints(ctx context.Context, namespace, serviceName string, servicePort intstr.IntOrString) (int, *corev1.Service, corev1.ServicePort, []discoveryv1.Endpoint, error) {
	// Read the Service.
	svc := &corev1.Service{}
	err := o.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: serviceName}, svc)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return 0, nil, corev1.ServicePort{}, nil, fmt.Errorf("failed to get Service %s: %w", serviceName, err)
		}
		// Service NotFound -> distinct from unmappable so the caller can infer EVIDENCE_MISSING.
		return endpointsServiceNotFound, nil, corev1.ServicePort{}, nil, nil
	}

	// Check if the Service is selector-backed (not ExternalName).
	if svc.Spec.Type == corev1.ServiceTypeExternalName || len(svc.Spec.Selector) == 0 {
		// Selector-less / ExternalName -> unmappable (EVIDENCE_INSUFFICIENT).
		return endpointsUnmappable, nil, corev1.ServicePort{}, nil, nil
	}

	// Resolve the Service port. EndpointSlices carry the TARGET (container) port, never the Service port.
	svcPort, found := findServicePort(svc, servicePort)
	if !found {
		// Port not found in Service -> unmappable (EVIDENCE_INSUFFICIENT).
		return endpointsUnmappable, nil, corev1.ServicePort{}, nil, nil
	}

	// List EndpointSlices for this Service.
	slices := &discoveryv1.EndpointSliceList{}
	err = o.client.List(ctx, slices, client.InNamespace(namespace), client.MatchingLabels{
		discoveryv1.LabelServiceName: serviceName,
	})
	if err != nil {
		return 0, nil, corev1.ServicePort{}, nil, fmt.Errorf("failed to list EndpointSlices for Service %s: %w", serviceName, err)
	}

	// Count Ready endpoints covering the target port.
	var ready []discoveryv1.Endpoint
	for _, slice := range slices.Items {
		if !sliceCoversPort(slice, svcPort) {
			continue
		}

		// Count ready endpoints in this slice.
		for _, endpoint := range slice.Endpoints {
			if endpoint.Conditions.Ready != nil && *endpoint.Conditions.Ready {
				ready = append(ready, endpoint)
			}
		}
	}

	// An empty slice list (freshly-created Service) is zero ready, which the caller windows.
	return len(ready), svc, svcPort, ready, nil
}

// findServicePort returns the Service port the selector names.
func findServicePort(svc *corev1.Service, selector intstr.IntOrString) (corev1.ServicePort, bool) {
	for _, p := range svc.Spec.Ports {
		if matchesServicePort(p, selector) {
			return p, true
		}
	}
	return corev1.ServicePort{}, false
}

// sliceCoversPort reports whether an EndpointSlice serves the Service port. The slice lists the resolved
// target port under the Service port's name: an int targetPort (unset means equal to the port) is
// compared by number, a named targetPort can only be matched by that name, since its number differs per
// pod.
func sliceCoversPort(slice discoveryv1.EndpointSlice, p corev1.ServicePort) bool {
	for _, sp := range slice.Ports {
		if p.TargetPort.Type == intstr.String {
			if sp.Name != nil && *sp.Name == p.Name {
				return true
			}
			continue
		}
		if sp.Port != nil && *sp.Port == intTargetPort(p) {
			return true
		}
	}
	return false
}

// targetPort is the one resolver of a Service port's targetPort to a container port. A named targetPort is
// looked up among the containers (its number may differ per pod); a numeric one, unset included, needs no
// pod at all.
func targetPort(p corev1.ServicePort, containers []corev1.Container) (int32, bool) {
	if p.TargetPort.Type == intstr.String {
		return containerPortNamed(containers, p.TargetPort.StrVal)
	}
	return intTargetPort(p), true
}

// containerPortNamed is the number of the container port called name.
func containerPortNamed(containers []corev1.Container, name string) (int32, bool) {
	for _, c := range containers {
		for _, cp := range c.Ports {
			if cp.Name == name {
				return cp.ContainerPort, true
			}
		}
	}
	return 0, false
}

// intTargetPort is the numeric targetPort of a Service port; the API server defaults an unset one to the
// port itself.
func intTargetPort(p corev1.ServicePort) int32 {
	if p.TargetPort.IntVal == 0 {
		return p.Port
	}
	return p.TargetPort.IntVal
}

// matchesServicePort reports whether a Service port matches the given IntOrString selector.
func matchesServicePort(p corev1.ServicePort, selector intstr.IntOrString) bool {
	switch selector.Type {
	case intstr.Int:
		return p.Port == selector.IntVal
	case intstr.String:
		return p.Name == selector.StrVal
	default:
		return false
	}
}
