/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/evidence"
)

// Test extractPort with int64 port
func TestExtractPort_Int64(t *testing.T) {
	epMap := map[string]any{
		"port": int64(9090),
	}
	port := extractPort(epMap)
	if port != 9090 {
		t.Errorf("expected port 9090, got %d", port)
	}
}

// Test extractPort with string port (named port)
func TestExtractPort_String(t *testing.T) {
	epMap := map[string]any{
		"port": "metrics",
	}
	port := extractPort(epMap)
	if port != 0 {
		t.Errorf("expected port 0 for named port, got %d", port)
	}
}

// Test extractPort with missing port
func TestExtractPort_Missing(t *testing.T) {
	epMap := map[string]any{}
	port := extractPort(epMap)
	if port != 0 {
		t.Errorf("expected port 0 for missing port, got %d", port)
	}
}

// Test extractPort with invalid port type
func TestExtractPort_InvalidType(t *testing.T) {
	epMap := map[string]any{
		"port": 3.14,
	}
	port := extractPort(epMap)
	if port != 0 {
		t.Errorf("expected port 0 for invalid type, got %d", port)
	}
}

// Test labelsMatch with matching labels
func TestLabelsMatch_Match(t *testing.T) {
	target := map[string]string{
		"app":  "test",
		"tier": "backend",
	}
	selector := map[string]any{
		"app": "test",
	}
	if !labelsMatch(target, selector) {
		t.Error("expected labels to match")
	}
}

// Test labelsMatch with no match
func TestLabelsMatch_NoMatch(t *testing.T) {
	target := map[string]string{
		"app": "test",
	}
	selector := map[string]any{
		"app": "other",
	}
	if labelsMatch(target, selector) {
		t.Error("expected labels not to match")
	}
}

// Test labelsMatch with missing label in target
func TestLabelsMatch_MissingLabel(t *testing.T) {
	target := map[string]string{
		"app": "test",
	}
	selector := map[string]any{
		"tier": "backend",
	}
	if labelsMatch(target, selector) {
		t.Error("expected labels not to match when label missing")
	}
}

// Test labelsMatch with empty selector
func TestLabelsMatch_EmptySelector(t *testing.T) {
	target := map[string]string{
		"app": "test",
	}
	selector := map[string]any{}
	if !labelsMatch(target, selector) {
		t.Error("expected empty selector to match")
	}
}

// Test labelsMatch with non-string value in selector
func TestLabelsMatch_NonStringValue(t *testing.T) {
	target := map[string]string{
		"app": "test",
	}
	selector := map[string]any{
		"app": 123,
	}
	if labelsMatch(target, selector) {
		t.Error("expected non-string selector value not to match")
	}
}

// Test discoverFromAnnotations with scrape=true, path, and port
func TestDiscoverFromAnnotations_FullConfig(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"prometheus.io/scrape": "true",
				"prometheus.io/path":   "/custom/metrics",
				"prometheus.io/port":   "9090",
			},
		},
	}
	target := discoverFromAnnotations(svc)
	if target == nil {
		t.Fatal("expected target, got nil")
	}
	if target.path != "/custom/metrics" {
		t.Errorf("expected path /custom/metrics, got %s", target.path)
	}
	if target.port != 9090 {
		t.Errorf("expected port 9090, got %d", target.port)
	}
}

// Test discoverFromAnnotations with default path
func TestDiscoverFromAnnotations_DefaultPath(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"prometheus.io/scrape": "true",
				"prometheus.io/port":   "8080",
			},
		},
	}
	target := discoverFromAnnotations(svc)
	if target == nil {
		t.Fatal("expected target, got nil")
	}
	if target.path != "/metrics" {
		t.Errorf("expected default path /metrics, got %s", target.path)
	}
	if target.port != 8080 {
		t.Errorf("expected port 8080, got %d", target.port)
	}
}

// Test discoverFromAnnotations with scrape=false
func TestDiscoverFromAnnotations_ScrapeFalse(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"prometheus.io/scrape": "false",
				"prometheus.io/port":   "8080",
			},
		},
	}
	target := discoverFromAnnotations(svc)
	if target != nil {
		t.Errorf("expected nil target for scrape=false, got %+v", target)
	}
}

// Test discoverFromAnnotations with missing scrape annotation
func TestDiscoverFromAnnotations_NoScrape(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"prometheus.io/port": "8080",
			},
		},
	}
	target := discoverFromAnnotations(svc)
	if target != nil {
		t.Errorf("expected nil target when scrape not set, got %+v", target)
	}
}

// Test discoverFromAnnotations with missing port
func TestDiscoverFromAnnotations_NoPort(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"prometheus.io/scrape": "true",
			},
		},
	}
	target := discoverFromAnnotations(svc)
	if target != nil {
		t.Errorf("expected nil target when port missing, got %+v", target)
	}
}

// Test discoverFromAnnotations with invalid port
func TestDiscoverFromAnnotations_InvalidPort(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"prometheus.io/scrape": "true",
				"prometheus.io/port":   "invalid",
			},
		},
	}
	target := discoverFromAnnotations(svc)
	if target != nil {
		t.Errorf("expected nil target for invalid port, got %+v", target)
	}
}

// Test discoverFromAnnotations with nil annotations
func TestDiscoverFromAnnotations_NilAnnotations(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: nil,
		},
	}
	target := discoverFromAnnotations(svc)
	if target != nil {
		t.Errorf("expected nil target when annotations nil, got %+v", target)
	}
}

// Test discoverFromNamedPort with metrics port
func TestDiscoverFromNamedPort_MetricsPort(t *testing.T) {
	svc := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080},
				{Name: "metrics", Port: 9090},
			},
		},
	}
	target := discoverFromNamedPort(svc, "/custom")
	if target == nil {
		t.Fatal("expected target, got nil")
	}
	if target.path != "/custom" {
		t.Errorf("expected path /custom, got %s", target.path)
	}
	if target.port != 9090 {
		t.Errorf("expected port 9090, got %d", target.port)
	}
}

// Test discoverFromNamedPort with http-metrics port
func TestDiscoverFromNamedPort_HTTPMetricsPort(t *testing.T) {
	svc := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080},
				{Name: "http-metrics", Port: 9091},
			},
		},
	}
	target := discoverFromNamedPort(svc, "/custom")
	if target == nil {
		t.Fatal("expected target, got nil")
	}
	if target.port != 9091 {
		t.Errorf("expected port 9091, got %d", target.port)
	}
}

// Test discoverFromNamedPort with default path
func TestDiscoverFromNamedPort_DefaultPath(t *testing.T) {
	svc := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "metrics", Port: 9090},
			},
		},
	}
	target := discoverFromNamedPort(svc, "")
	if target == nil {
		t.Fatal("expected target, got nil")
	}
	if target.path != "/metrics" {
		t.Errorf("expected default path /metrics, got %s", target.path)
	}
}

// Test discoverFromNamedPort with no matching port
func TestDiscoverFromNamedPort_NoMatch(t *testing.T) {
	svc := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080},
			},
		},
	}
	target := discoverFromNamedPort(svc, "/metrics")
	if target != nil {
		t.Errorf("expected nil target when no metrics port, got %+v", target)
	}
}

// Test discoverFromServiceMonitor with matching monitor
func TestDiscoverFromServiceMonitor_Match(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
			Labels:    map[string]string{"app": "test"},
		},
	}
	sm := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "ServiceMonitor",
			"metadata": map[string]any{
				"name":      "test-sm",
				"namespace": "default",
			},
			"spec": map[string]any{
				"selector": map[string]any{
					"matchLabels": map[string]any{
						"app": "test",
					},
				},
				"endpoints": []any{
					map[string]any{
						"path": "/custom/metrics",
						"port": int64(9090),
					},
				},
			},
		},
	}
	sm.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "ServiceMonitor",
	})

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(sm).Build(),
	}

	target := o.discoverFromServiceMonitor(context.Background(), "default", "test-svc", svc)
	if target == nil {
		t.Fatal("expected target, got nil")
	}
	if target.path != "/custom/metrics" {
		t.Errorf("expected path /custom/metrics, got %s", target.path)
	}
	if target.port != 9090 {
		t.Errorf("expected port 9090, got %d", target.port)
	}
}

// Test discoverFromServiceMonitor with default path
func TestDiscoverFromServiceMonitor_DefaultPath(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
			Labels:    map[string]string{"app": "test"},
		},
	}
	sm := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "ServiceMonitor",
			"metadata": map[string]any{
				"name":      "test-sm",
				"namespace": "default",
			},
			"spec": map[string]any{
				"selector": map[string]any{
					"matchLabels": map[string]any{
						"app": "test",
					},
				},
				"endpoints": []any{
					map[string]any{
						"port": int64(9090),
					},
				},
			},
		},
	}
	sm.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "ServiceMonitor",
	})

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(sm).Build(),
	}

	target := o.discoverFromServiceMonitor(context.Background(), "default", "test-svc", svc)
	if target == nil {
		t.Fatal("expected target, got nil")
	}
	if target.path != "/metrics" {
		t.Errorf("expected default path /metrics, got %s", target.path)
	}
}

// Test discoverFromServiceMonitor with no match
func TestDiscoverFromServiceMonitor_NoMatch(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
			Labels:    map[string]string{"app": "test"},
		},
	}
	sm := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "ServiceMonitor",
			"metadata": map[string]any{
				"name":      "test-sm",
				"namespace": "default",
			},
			"spec": map[string]any{
				"selector": map[string]any{
					"matchLabels": map[string]any{
						"app": "other",
					},
				},
				"endpoints": []any{
					map[string]any{
						"path": "/metrics",
						"port": int64(9090),
					},
				},
			},
		},
	}
	sm.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "ServiceMonitor",
	})

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(sm).Build(),
	}

	target := o.discoverFromServiceMonitor(context.Background(), "default", "test-svc", svc)
	if target != nil {
		t.Errorf("expected nil target when labels don't match, got %+v", target)
	}
}

// Test discoverFromServiceMonitor with CRD not installed
func TestDiscoverFromServiceMonitor_CRDNotInstalled(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
			Labels:    map[string]string{"app": "test"},
		},
	}

	errClient := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			return fmt.Errorf("no kind is registered for the type v1.ServiceMonitorList")
		},
	}).Build()

	o := &Observer{client: errClient}

	target := o.discoverFromServiceMonitor(context.Background(), "default", "test-svc", svc)
	if target != nil {
		t.Errorf("expected nil target when CRD not installed, got %+v", target)
	}
}

// Test discoverFromServiceMonitor with auth fields present (INV-5)
func TestDiscoverFromServiceMonitor_WithAuthFields(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
			Labels:    map[string]string{"app": "test"},
		},
	}
	sm := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "ServiceMonitor",
			"metadata": map[string]any{
				"name":      "test-sm",
				"namespace": "default",
			},
			"spec": map[string]any{
				"selector": map[string]any{
					"matchLabels": map[string]any{
						"app": "test",
					},
				},
				"endpoints": []any{
					map[string]any{
						"path": "/metrics",
						"port": int64(9090),
						"bearerTokenSecret": map[string]any{
							"name": "token-secret",
						},
						"authorization": map[string]any{
							"type": "Bearer",
						},
						"basicAuth": map[string]any{
							"username": "user",
						},
					},
				},
			},
		},
	}
	sm.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "ServiceMonitor",
	})

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(sm).Build(),
	}

	target := o.discoverFromServiceMonitor(context.Background(), "default", "test-svc", svc)
	if target == nil {
		t.Fatal("expected target, got nil")
	}
	if target.path != "/metrics" {
		t.Errorf("expected path /metrics, got %s", target.path)
	}
	if target.port != 9090 {
		t.Errorf("expected port 9090, got %d", target.port)
	}
}

// Test discoverFromPodMonitor with matching monitor
func TestDiscoverFromPodMonitor_Match(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
		},
	}
	pm := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "PodMonitor",
			"metadata": map[string]any{
				"name":      "test-pm",
				"namespace": "default",
			},
			"spec": map[string]any{
				"selector": map[string]any{
					"matchLabels": map[string]any{
						"app": "test",
					},
				},
				"podMetricsEndpoints": []any{
					map[string]any{
						"path": "/pod/metrics",
						"port": int64(9091),
					},
				},
			},
		},
	}
	pm.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "PodMonitor",
	})

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(pm).Build(),
	}

	target := o.discoverFromPodMonitor(context.Background(), "default", svc)
	if target == nil {
		t.Fatal("expected target, got nil")
	}
	if target.path != "/pod/metrics" {
		t.Errorf("expected path /pod/metrics, got %s", target.path)
	}
	if target.port != 9091 {
		t.Errorf("expected port 9091, got %d", target.port)
	}
}

// Test discoverFromPodMonitor with default path
func TestDiscoverFromPodMonitor_DefaultPath(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
		},
	}
	pm := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "PodMonitor",
			"metadata": map[string]any{
				"name":      "test-pm",
				"namespace": "default",
			},
			"spec": map[string]any{
				"selector": map[string]any{
					"matchLabels": map[string]any{
						"app": "test",
					},
				},
				"podMetricsEndpoints": []any{
					map[string]any{
						"port": int64(9091),
					},
				},
			},
		},
	}
	pm.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "PodMonitor",
	})

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(pm).Build(),
	}

	target := o.discoverFromPodMonitor(context.Background(), "default", svc)
	if target == nil {
		t.Fatal("expected target, got nil")
	}
	if target.path != "/metrics" {
		t.Errorf("expected default path /metrics, got %s", target.path)
	}
}

// Test discoverFromPodMonitor with no match
func TestDiscoverFromPodMonitor_NoMatch(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
		},
	}
	pm := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "PodMonitor",
			"metadata": map[string]any{
				"name":      "test-pm",
				"namespace": "default",
			},
			"spec": map[string]any{
				"selector": map[string]any{
					"matchLabels": map[string]any{
						"app": "other",
					},
				},
				"podMetricsEndpoints": []any{
					map[string]any{
						"path": "/metrics",
						"port": int64(9091),
					},
				},
			},
		},
	}
	pm.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "PodMonitor",
	})

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(pm).Build(),
	}

	target := o.discoverFromPodMonitor(context.Background(), "default", svc)
	if target != nil {
		t.Errorf("expected nil target when labels don't match, got %+v", target)
	}
}

// Test discoverFromPodMonitor with CRD not installed
func TestDiscoverFromPodMonitor_CRDNotInstalled(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
		},
	}

	errClient := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			return fmt.Errorf("no kind is registered for the type v1.PodMonitorList")
		},
	}).Build()

	o := &Observer{client: errClient}

	target := o.discoverFromPodMonitor(context.Background(), "default", svc)
	if target != nil {
		t.Errorf("expected nil target when CRD not installed, got %+v", target)
	}
}

// Test discoverFromPodMonitor with nil service selector
func TestDiscoverFromPodMonitor_NilServiceSelector(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Selector: nil,
		},
	}
	pm := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "PodMonitor",
			"metadata": map[string]any{
				"name":      "test-pm",
				"namespace": "default",
			},
			"spec": map[string]any{
				"selector": map[string]any{
					"matchLabels": map[string]any{
						"app": "test",
					},
				},
				"podMetricsEndpoints": []any{
					map[string]any{
						"port": int64(9091),
					},
				},
			},
		},
	}
	pm.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "PodMonitor",
	})

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(pm).Build(),
	}

	target := o.discoverFromPodMonitor(context.Background(), "default", svc)
	if target != nil {
		t.Errorf("expected nil target when service selector is nil, got %+v", target)
	}
}

// Test discoverFromPodMonitor with auth fields (INV-5)
func TestDiscoverFromPodMonitor_WithAuthFields(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
		},
	}
	pm := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "PodMonitor",
			"metadata": map[string]any{
				"name":      "test-pm",
				"namespace": "default",
			},
			"spec": map[string]any{
				"selector": map[string]any{
					"matchLabels": map[string]any{
						"app": "test",
					},
				},
				"podMetricsEndpoints": []any{
					map[string]any{
						"path": "/metrics",
						"port": int64(9091),
						"bearerTokenSecret": map[string]any{
							"name": "token-secret",
						},
					},
				},
			},
		},
	}
	pm.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "PodMonitor",
	})

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(pm).Build(),
	}

	target := o.discoverFromPodMonitor(context.Background(), "default", svc)
	if target == nil {
		t.Fatal("expected target, got nil")
	}
	if target.path != "/metrics" {
		t.Errorf("expected path /metrics, got %s", target.path)
	}
	if target.port != 9091 {
		t.Errorf("expected port 9091, got %d", target.port)
	}
}

// Test discoverMetricsTarget precedence: ServiceMonitor wins
func TestDiscoverMetricsTarget_ServiceMonitorPrecedence(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
			Labels:    map[string]string{"app": "test"},
			Annotations: map[string]string{
				"prometheus.io/scrape": "true",
				"prometheus.io/port":   "8888",
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
			Ports: []corev1.ServicePort{
				{Name: "metrics", Port: 7777},
			},
		},
	}
	sm := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "ServiceMonitor",
			"metadata": map[string]any{
				"name":      "test-sm",
				"namespace": "default",
			},
			"spec": map[string]any{
				"selector": map[string]any{
					"matchLabels": map[string]any{
						"app": "test",
					},
				},
				"endpoints": []any{
					map[string]any{
						"path": "/sm/metrics",
						"port": int64(9999),
					},
				},
			},
		},
	}
	sm.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "ServiceMonitor",
	})

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(sm).Build(),
	}

	target, method := o.discoverMetricsTarget(context.Background(), "default", "test-svc", svc, "/contract", 8080)
	if target == nil {
		t.Fatal("expected target, got nil")
	}
	if method != "servicemonitor" {
		t.Errorf("expected method servicemonitor, got %s", method)
	}
	if target.port != 9999 {
		t.Errorf("expected port 9999 from ServiceMonitor, got %d", target.port)
	}
	if target.path != "/sm/metrics" {
		t.Errorf("expected path /sm/metrics from ServiceMonitor, got %s", target.path)
	}
}

// Test discoverMetricsTarget precedence: PodMonitor when no ServiceMonitor
func TestDiscoverMetricsTarget_PodMonitorPrecedence(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
			Annotations: map[string]string{
				"prometheus.io/scrape": "true",
				"prometheus.io/port":   "8888",
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
			Ports: []corev1.ServicePort{
				{Name: "metrics", Port: 7777},
			},
		},
	}
	pm := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "PodMonitor",
			"metadata": map[string]any{
				"name":      "test-pm",
				"namespace": "default",
			},
			"spec": map[string]any{
				"selector": map[string]any{
					"matchLabels": map[string]any{
						"app": "test",
					},
				},
				"podMetricsEndpoints": []any{
					map[string]any{
						"path": "/pm/metrics",
						"port": int64(9998),
					},
				},
			},
		},
	}
	pm.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "PodMonitor",
	})

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(pm).Build(),
	}

	target, method := o.discoverMetricsTarget(context.Background(), "default", "test-svc", svc, "/contract", 8080)
	if target == nil {
		t.Fatal("expected target, got nil")
	}
	if method != "podmonitor" {
		t.Errorf("expected method podmonitor, got %s", method)
	}
	if target.port != 9998 {
		t.Errorf("expected port 9998 from PodMonitor, got %d", target.port)
	}
}

// Test discoverMetricsTarget precedence: annotations when no monitors
func TestDiscoverMetricsTarget_AnnotationPrecedence(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
			Annotations: map[string]string{
				"prometheus.io/scrape": "true",
				"prometheus.io/port":   "8888",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "metrics", Port: 7777},
			},
		},
	}

	o := &Observer{
		client: fake.NewClientBuilder().Build(),
	}

	target, method := o.discoverMetricsTarget(context.Background(), "default", "test-svc", svc, "/contract", 8080)
	if target == nil {
		t.Fatal("expected target, got nil")
	}
	if method != "annotation" {
		t.Errorf("expected method annotation, got %s", method)
	}
	if target.port != 8888 {
		t.Errorf("expected port 8888 from annotation, got %d", target.port)
	}
}

// Test discoverMetricsTarget precedence: named-port when no monitors or annotations
func TestDiscoverMetricsTarget_NamedPortPrecedence(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080},
				{Name: "metrics", Port: 7777},
			},
		},
	}

	o := &Observer{
		client: fake.NewClientBuilder().Build(),
	}

	target, method := o.discoverMetricsTarget(context.Background(), "default", "test-svc", svc, "/contract", 8080)
	if target == nil {
		t.Fatal("expected target, got nil")
	}
	if method != "named-port" {
		t.Errorf("expected method named-port, got %s", method)
	}
	if target.port != 7777 {
		t.Errorf("expected port 7777 from named port, got %d", target.port)
	}
}

// Test discoverMetricsTarget fallback to contract binding.path
func TestDiscoverMetricsTarget_ContractFallback(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080},
			},
		},
	}

	o := &Observer{
		client: fake.NewClientBuilder().Build(),
	}

	target, method := o.discoverMetricsTarget(context.Background(), "default", "test-svc", svc, "/contract/metrics", 9999)
	if target == nil {
		t.Fatal("expected target, got nil")
	}
	if method != "probe" {
		t.Errorf("expected method probe, got %s", method)
	}
	if target.port != 9999 {
		t.Errorf("expected port 9999 from bound port, got %d", target.port)
	}
	if target.path != "/contract/metrics" {
		t.Errorf("expected path /contract/metrics from contract, got %s", target.path)
	}
}

// Test discoverFromServiceMonitor with invalid endpoint type
func TestDiscoverFromServiceMonitor_InvalidEndpointType(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
			Labels:    map[string]string{"app": "test"},
		},
	}
	sm := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "ServiceMonitor",
			"metadata": map[string]any{
				"name":      "test-sm",
				"namespace": "default",
			},
			"spec": map[string]any{
				"selector": map[string]any{
					"matchLabels": map[string]any{
						"app": "test",
					},
				},
				"endpoints": []any{
					"invalid-not-a-map",
					map[string]any{
						"path": "/metrics",
						"port": int64(9090),
					},
				},
			},
		},
	}
	sm.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "ServiceMonitor",
	})

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(sm).Build(),
	}

	target := o.discoverFromServiceMonitor(context.Background(), "default", "test-svc", svc)
	if target == nil {
		t.Fatal("expected target from second endpoint, got nil")
	}
	if target.path != "/metrics" {
		t.Errorf("expected path /metrics, got %s", target.path)
	}
}

// Test discoverFromPodMonitor with invalid endpoint type
func TestDiscoverFromPodMonitor_InvalidEndpointType(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
		},
	}
	pm := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "monitoring.coreos.com/v1",
			"kind":       "PodMonitor",
			"metadata": map[string]any{
				"name":      "test-pm",
				"namespace": "default",
			},
			"spec": map[string]any{
				"selector": map[string]any{
					"matchLabels": map[string]any{
						"app": "test",
					},
				},
				"podMetricsEndpoints": []any{
					"invalid-not-a-map",
					map[string]any{
						"path": "/pod/metrics",
						"port": int64(9091),
					},
				},
			},
		},
	}
	pm.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "PodMonitor",
	})

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(pm).Build(),
	}

	target := o.discoverFromPodMonitor(context.Background(), "default", svc)
	if target == nil {
		t.Fatal("expected target from second endpoint, got nil")
	}
	if target.path != "/pod/metrics" {
		t.Errorf("expected path /pod/metrics, got %s", target.path)
	}
}

// Test discoverMetricsTarget returns nil when no discovery path succeeds and contract path is empty
func TestDiscoverMetricsTarget_NoPathReturnsNil(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080},
			},
		},
	}

	o := &Observer{
		client: fake.NewClientBuilder().Build(),
	}

	target, method := o.discoverMetricsTarget(context.Background(), "default", "test-svc", svc, "", 8080)
	if target != nil {
		t.Errorf("expected nil target when no discovery succeeds and contractPath is empty, got %+v", target)
	}
	if method != "" {
		t.Errorf("expected empty method when no discovery succeeds, got %s", method)
	}
}

// Test observeMetricsDim with no discovery path (empty contract path)
func TestObserveMetricsDim_NoDiscoveryPathEmptyContractPath(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080},
			},
		},
	}
	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(svc).Build(),
	}
	cap := contract.Capability{
		Type: contract.CapabilityMetrics,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "",
		},
	}
	input := CollectInput{
		Namespace:   "default",
		ServiceName: "test-svc",
		Contract: &contract.Contract{
			Service:      contract.Service{Name: "test"},
			Capabilities: []contract.Capability{cap},
		},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow:      2 * time.Minute,
		EnableMetricsObservation: true,
		ObservationWindows:       make(map[string]*metav1.Time),
		Now:                      time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeMetricsDim(context.Background(), input, prov, input.Now)

	if obs == nil {
		t.Fatal("expected observation, got nil")
	}
	if obs.Outcome != evidence.Unsupported {
		t.Errorf("expected Unsupported when no discovery path succeeds and contract path empty, got %s", obs.Outcome)
	}
}
