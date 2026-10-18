/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
)

// observeMetricsDim observes the metrics capability (spec section 7.5 / Refinement D). Returns nil if no metrics
// capability is declared, otherwise exactly one observation. No reliable operator-side negative this release.
// ponytail: discovery precedence (ServiceMonitor > annotations > named-port > contract), then active probe.
//
//nolint:unparam // updates slice always nil — matches observe function signature for consistency
func (o *Observer) observeMetricsDim(ctx context.Context, input CollectInput, prov evidence.Provenance, _ time.Time) (*evidence.Observation, []ObservationWindowUpdate) {
	// Find the metrics capability.
	var metricsCap *contract.Capability
	for i := range input.Contract.Capabilities {
		if input.Contract.Capabilities[i].Type == contract.CapabilityMetrics {
			metricsCap = &input.Contract.Capabilities[i]
			break
		}
	}

	if metricsCap == nil {
		return nil, nil
	}

	subj := evidence.SubjectRef{Kind: kindCapability, Name: metricsCap.AssertionKey()} // "metrics"

	// When metrics observation is DISABLED, return Unsupported HONESTLY (no discovery).
	if !input.EnableMetricsObservation {
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return &obs, nil
	}

	// No binding -> Unsupported.
	if metricsCap.Binding == nil {
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return &obs, nil
	}

	// Non-HTTP binding -> Unsupported (grpc not implemented).
	if metricsCap.Binding.Type != contract.CapabilityBindingHTTP {
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return &obs, nil
	}

	// Resolve the owning interface to its Service port (B4 reuse).
	binding := findInterfaceBinding(input.InterfaceBindings, metricsCap.Binding.Interface)
	if binding == nil {
		// Owning interface has no binding -> Unsupported (cannot resolve probe target).
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return &obs, nil
	}

	// Get the Service.
	svc := &corev1.Service{}
	err := o.client.Get(ctx, types.NamespacedName{Namespace: input.Namespace, Name: input.ServiceName}, svc)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			// Non-NotFound API error -> Failed.
			o.fail("metrics", err)
			obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Failed, prov)
			return &obs, nil
		}
		// Service NotFound -> Unsupported (cannot probe).
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return &obs, nil
	}

	// Resolve the interface-bound port number.
	boundPort, found := findServicePort(svc, binding.ServicePort)
	if !found {
		// Bound port not found in Service -> Unsupported.
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return &obs, nil
	}

	// Discovery precedence (spec section 7.5): first that yields a path+port wins.
	target, method := o.discoverMetricsTarget(ctx, input.Namespace, svc, o.getPodSpec(ctx, input), metricsCap.Binding.Path, boundPort.Port)
	if target == nil {
		// No discovery path succeeded -> Unsupported (Unknown).
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Unsupported, prov)
		return &obs, nil
	}

	// Build the probe URL (SSRF-safe via probe.URL — INV-6).
	u := probeURL(input.ServiceName, input.Namespace, target.port, target.path)

	// Direct in-cluster probe (metrics has no tier-B fallback).
	result := o.prober.Probe(ctx, u)

	if !result.Reachable {
		// Transport error -> Failed.
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Failed, prov)
		return &obs, nil
	}

	// Reachable but not 200 -> Insufficient (no reliable negative for metrics).
	if result.StatusCode != 200 {
		// 404/410/5xx/401-403 -> Insufficient (Unknown), not a violation.
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Insufficient, prov)
		return &obs, nil
	}

	// 200 but body does not parse as Prometheus -> Insufficient.
	if !result.PrometheusParsed {
		obs, _ := evidence.NewUnobserved(evidence.CapabilityObserved, subj, evidence.Insufficient, prov)
		return &obs, nil
	}

	// 200 + real Prometheus content -> satisfied. Update provenance with discovery method.
	prov.Collector = fmt.Sprintf("k8s-observer/metrics:%s", method)
	obs := evidence.NewCapabilityObserved(subj, true, prov)
	return &obs, nil
}

// metricsTarget is a discovered scrape target (path+port).
type metricsTarget struct {
	path string
	port int32
}

// discoverMetricsTarget implements the 4-step precedence (spec section 7.5): ServiceMonitor/PodMonitor >
// annotations > named-port > contract binding.path. Returns (target, method) or (nil, "") if no path succeeds.
// podSpec is the workload's pod template (nil when unknown), which a PodMonitor's named port resolves against.
// ponytail: precedence ladder, first valid path+port wins.
func (o *Observer) discoverMetricsTarget(ctx context.Context, namespace string, service *corev1.Service, podSpec *corev1.PodSpec, contractPath string, boundPort int32) (*metricsTarget, string) {
	// 1. ServiceMonitor/PodMonitor (unstructured read, extract path+port).
	if t := o.discoverFromServiceMonitor(ctx, namespace, service); t != nil {
		return t, "servicemonitor"
	}
	if t := o.discoverFromPodMonitor(ctx, namespace, service, podSpec); t != nil {
		return t, "podmonitor"
	}

	// 2. prometheus.io annotations (scrape=true + path + port).
	if t := discoverFromAnnotations(service); t != nil {
		return t, "annotation"
	}

	// 3. Named metrics port ("metrics" / "http-metrics").
	if t := discoverFromNamedPort(service, contractPath); t != nil {
		return t, "named-port"
	}

	// 4. Contract binding.path on the bound port (if path is specified).
	if contractPath == "" {
		return nil, ""
	}
	return &metricsTarget{path: contractPath, port: boundPort}, "probe"
}

// monitorList memoises one Prometheus monitor kind's list in one namespace for one Collect: an unstructured
// read bypasses the manager's cache, so without it every reconcile would LIST once per call site.
type monitorList struct {
	items []unstructured.Unstructured
}

// monitors lists the monitor kind in the namespace, at most once per Collect. A list error (CRD not
// installed, or the additive metrics-observation RBAC not granted) yields none: monitor discovery is the
// first rung of a fallback ladder, so an unreadable monitor falls through to the next rung, never Failed.
func (o *Observer) monitors(ctx context.Context, kind, namespace string) []unstructured.Unstructured {
	key := kind + "/" + namespace
	if o.run != nil {
		if m, ok := o.run.monitors[key]; ok {
			return m.items
		}
	}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{Group: promGroup, Version: "v1", Kind: kind})
	m := &monitorList{}
	if err := o.client.List(ctx, list, client.InNamespace(namespace)); err == nil {
		m.items = list.Items
	}
	if o.run != nil {
		o.run.monitors[key] = m
	}
	return m.items
}

// discoverFromServiceMonitor reads ServiceMonitor CRDs via unstructured (no Go dep on prometheus-operator).
// Returns path+port from the first matching endpoint, or nil if CRD absent / no match / list error. An
// endpoint's port is a Service port NAME in the real CRD; a number is still accepted as the Service port.
// ponytail: unstructured List + extract endpoint path/port, skip auth fields (INV-5).
func (o *Observer) discoverFromServiceMonitor(ctx context.Context, namespace string, service *corev1.Service) *metricsTarget {
	byName := func(name string) (int32, bool) {
		for _, p := range service.Spec.Ports {
			if p.Name == name {
				return p.Port, true
			}
		}
		return 0, false
	}
	for _, item := range o.monitors(ctx, promMonitorServiceMonitor, namespace) {
		// Check if this ServiceMonitor targets the Service via matchLabels.
		if !selectsLabels(item, service.Labels) {
			continue
		}
		if t := firstEndpoint(item, "endpoints", byName); t != nil {
			return t
		}
	}
	return nil
}

// discoverFromPodMonitor reads PodMonitor CRDs via unstructured. Returns path+port or nil. A
// podMetricsEndpoint's port is a container port NAME: it resolves to the Service port whose targetPort
// reaches that container port, since the probe dials the Service. A number is still accepted as the
// Service port.
// ponytail: unstructured List + extract podMetricsEndpoints path/port, skip auth fields.
func (o *Observer) discoverFromPodMonitor(ctx context.Context, namespace string, service *corev1.Service, podSpec *corev1.PodSpec) *metricsTarget {
	if service.Spec.Selector == nil {
		return nil
	}
	byName := func(name string) (int32, bool) {
		if podSpec == nil {
			return 0, false
		}
		containerPort, ok := containerPortNamed(podSpec.Containers, name)
		if !ok {
			return 0, false
		}
		for _, p := range service.Spec.Ports {
			if tp, ok := targetPort(p, podSpec.Containers); ok && tp == containerPort {
				return p.Port, true
			}
		}
		return 0, false
	}
	for _, item := range o.monitors(ctx, promMonitorPodMonitor, namespace) {
		// Check selector.matchLabels against Service selector (PodMonitor targets pods).
		if !selectsLabels(item, service.Spec.Selector) {
			continue
		}
		if t := firstEndpoint(item, "podMetricsEndpoints", byName); t != nil {
			return t
		}
	}
	return nil
}

// selectsLabels reports whether the monitor's spec.selector.matchLabels selects the labels. A monitor without
// matchLabels, or with a non-string value in them, selects nothing.
func selectsLabels(monitor unstructured.Unstructured, target map[string]string) bool {
	selector, found, err := unstructured.NestedStringMap(monitor.Object, "spec", "selector", "matchLabels")
	return found && err == nil && labels.SelectorFromSet(selector).Matches(labels.Set(target))
}

// firstEndpoint returns path+port of the first endpoint under spec.<field> whose port resolves (INV-5: auth
// fields are never read).
func firstEndpoint(monitor unstructured.Unstructured, field string, byName func(string) (int32, bool)) *metricsTarget {
	endpoints, _, _ := unstructured.NestedSlice(monitor.Object, "spec", field)
	for _, ep := range endpoints {
		epMap, ok := ep.(map[string]any)
		if !ok {
			continue
		}
		path, _ := epMap["path"].(string)
		if path == "" {
			path = promDefaultPath
		}
		if port := extractPort(epMap, byName); port > 0 {
			return &metricsTarget{path: path, port: port}
		}
	}
	return nil
}

// extractPort reads an endpoint's port: a name resolves through byName, a number in the port range is used
// as is. Returns 0 if absent, unresolved or invalid.
func extractPort(epMap map[string]any, byName func(string) (int32, bool)) int32 {
	switch v := epMap["port"].(type) {
	case string:
		if port, ok := byName(v); ok {
			return port
		}
	case int64:
		if v > 0 && v <= 65535 {
			return int32(v)
		}
	}
	return 0
}

// discoverFromAnnotations extracts prometheus.io annotations from the Service or Pod. Returns nil if not set.
// ponytail: scrape=true + path + port from annotations.
func discoverFromAnnotations(svc *corev1.Service) *metricsTarget {
	if svc.Annotations == nil {
		return nil
	}
	if svc.Annotations["prometheus.io/scrape"] != promScrapeAnnotation {
		return nil
	}
	path := svc.Annotations["prometheus.io/path"]
	if path == "" {
		path = promDefaultPath
	}
	portStr := svc.Annotations["prometheus.io/port"]
	if portStr == "" {
		return nil
	}
	port, err := strconv.ParseInt(portStr, 10, 32)
	if err != nil || port <= 0 {
		return nil
	}
	return &metricsTarget{path: path, port: int32(port)}
}

// discoverFromNamedPort finds a Service port named "metrics" or "http-metrics". Returns path+port or nil.
// ponytail: scan ports for metrics/http-metrics name.
func discoverFromNamedPort(svc *corev1.Service, fallbackPath string) *metricsTarget {
	for _, p := range svc.Spec.Ports {
		if p.Name == metricsPort || p.Name == "http-metrics" {
			path := fallbackPath
			if path == "" {
				path = promDefaultPath
			}
			return &metricsTarget{path: path, port: p.Port}
		}
	}
	return nil
}
