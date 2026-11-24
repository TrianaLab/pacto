/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package detection

import (
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/trianalab/pacto/v4/pkg/detect"
)

// owned is a running pod in ns owned by a controller of kind and name.
func owned(ns, name, kind, owner, image string, env ...corev1.EnvVar) *corev1.Pod {
	p := pod(ns, name, "", image)
	yes := true
	p.OwnerReferences = []metav1.OwnerReference{{Kind: kind, Name: owner, UID: types.UID(ns + "/" + owner), Controller: &yes}}
	p.Spec.Containers[0].Env = env
	return p
}

func service(ns, name string, labels map[string]string, ports ...corev1.ServicePort) *corev1.Service {
	return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels},
		Spec: corev1.ServiceSpec{Selector: map[string]string{"app": name}, Ports: ports}}
}

func traceObjects() []client.Object {
	return []client.Object{
		operatorDeployment(),
		owned("obs", "otel-1", "ReplicaSet", "otel-collector-5d9f", "otel/opentelemetry-collector-contrib:0.98.0"),
		owned("obs", "tempo-0", "StatefulSet", "tempo", "grafana/tempo:2.6.0"),
		owned("tracing", "jaeger-1", "ReplicaSet", "jaeger-6c7b", "jaegertracing/all-in-one:1.60"),
		owned("datadog", "agent-x", "DaemonSet", "datadog-agent", "gcr.io/datadoghq/agent:7.50",
			corev1.EnvVar{Name: "DD_SITE", Value: "datadoghq.eu"},
			corev1.EnvVar{Name: "DD_API_KEY", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "datadog-secret"}, Key: "api-key"}}}),
		service("obs", "otel-collector", nil, corev1.ServicePort{Name: "otlp-http", Port: 4318}),
		service("monitoring", "prometheus-operated", map[string]string{"operated-prometheus": "true", "team": "x"}, corev1.ServicePort{Name: "web", Port: 9090}),
		service("tracing", "jaeger-query", nil, corev1.ServicePort{Name: "query", Port: 16686}, corev1.ServicePort{Name: "agent", Port: 6831, Protocol: corev1.ProtocolUDP}),
	}
}

func TestDetectorFindsTraceBackends(t *testing.T) {
	c := newClient(routesInterceptor(), traceObjects()...)
	if err := newDetector(c, servedAll).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, res := readResult(t, c)
	prom := detect.Object{Kind: "Service", Namespace: "monitoring", Name: "prometheus-operated"}
	if e := entry(t, res, "otel-collector"); e.Endpoint != "http://prometheus-operated.monitoring.svc:9090" ||
		!slices.Contains(e.Objects, detect.Object{Kind: "Service", Namespace: "obs", Name: "otel-collector"}) || !slices.Contains(e.Objects, prom) {
		t.Errorf("otel-collector = %+v", e)
	}
	if e := entry(t, res, "tempo"); e.Endpoint != "http://prometheus-operated.monitoring.svc:9090" || e.Completeness != detect.Complete {
		t.Errorf("tempo = %+v", e)
	}
	if e := entry(t, res, "jaeger"); e.Endpoint != "http://jaeger-query.tracing.svc:16686" {
		t.Errorf("jaeger = %+v", e)
	}
	e := entry(t, res, "datadog")
	if e.Evidence != "Agent found; DD_SITE datadoghq.eu" || e.Completeness != detect.Partial || !reflect.DeepEqual(e.Objects, []detect.Object{
		{Kind: "DaemonSet", Namespace: "datadog", Name: "datadog-agent"}, {Kind: "Secret", Namespace: "datadog", Name: "datadog-secret"},
	}) || !strings.Contains(e.Hint, "http://pacto-dashboard.pacto-system:4318") {
		t.Errorf("datadog = %+v", e)
	}
}

func TestDetectorReadsOnlyAllowlistedEnv(t *testing.T) {
	p := owned("datadog", "agent-x", "DaemonSet", "datadog-agent", "datadog/agent:7",
		corev1.EnvVar{Name: "DB_PASSWORD", Value: "x-secret-value"}, corev1.EnvVar{Name: "DD_API_KEY", Value: "literal"})
	p.Spec.Containers[0].EnvFrom = []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "from-envfrom"}}}}
	c := newClient(routesInterceptor(), operatorDeployment(), p)
	_ = newDetector(c, servedAll).Sync(context.Background())
	cm, _ := readResult(t, c)
	for _, s := range []string{"x-secret-value", "literal", "DB_PASSWORD", "from-envfrom"} {
		if strings.Contains(cm.Data[detect.ConfigMapKey], s) {
			t.Errorf("the ConfigMap holds %q: %s", s, cm.Data[detect.ConfigMapKey])
		}
	}
	if got := envFacts(p.Spec.Containers[0].Env); got != nil {
		t.Errorf("env facts = %+v", got)
	}
}

func TestDetectorOwnerFromReplicaSet(t *testing.T) {
	p := owned("a", "web-1", "ReplicaSet", "web-7d4b9c", "x")
	if o := ownerObject(p, metav1.GetControllerOf(p)); o != (detect.Object{Kind: "ReplicaSet", Namespace: "a", Name: "web-7d4b9c"}) {
		t.Errorf("no hash label = %+v", o)
	}
	p.Labels = map[string]string{appsv1.DefaultDeploymentUniqueLabelKey: "7d4b9c"}
	if o := ownerObject(p, metav1.GetControllerOf(p)); o != (detect.Object{Kind: "Deployment", Namespace: "a", Name: "web"}) {
		t.Errorf("owner = %+v", o)
	}
	p.Labels[appsv1.DefaultDeploymentUniqueLabelKey] = "other"
	if o := ownerObject(p, metav1.GetControllerOf(p)); o.Kind != "ReplicaSet" {
		t.Errorf("a hash that is not the suffix = %+v", o)
	}
}

func TestDetectorTraceReadFailureIsOneUnknownEntry(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, "", errors.New("no"))
	c := newClient(interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		if _, ok := l.(*corev1.ServiceList); ok && len(opts) == 1 {
			return forbidden
		}
		return c.List(ctx, l, opts...)
	}}, traceObjects()...)
	_ = newDetector(c, servedAll).Sync(context.Background())
	_, res := readResult(t, c)
	n := 0
	for _, e := range res.Entries {
		switch e.Kind {
		case "traces":
			n++
			if e.Completeness != detect.Unknown || !strings.Contains(e.Reason, "Forbidden") {
				t.Errorf("traces = %+v", e)
			}
		case "otel-collector", "tempo", "jaeger", "datadog":
			t.Errorf("a backend entry beside the unknown one: %+v", e)
		}
	}
	if n != 1 {
		t.Errorf("%d traces entries in %+v", n, res.Entries)
	}
}

// TestDetectorEntriesFitMaxEntries: every rule emitting its maximum stays
// within detect.MaxEntries.
func TestDetectorEntriesFitMaxEntries(t *testing.T) {
	objs := append(traceObjects(), clusterObjects()[1:]...)
	objs = append(objs, pulling("b", "b9", "ghcr.io/acme/web:1", "ghcr-pull"))
	objs = append(objs, backstageObjects()...)
	c := newClient(routesInterceptor(route("a", map[string]any{"name": "public", "namespace": "infra"})), objs...)
	d := newDetector(c, servedAll)
	d.Installation, d.LastPush = &detect.Installation{Registry: "ghcr.io/acme"}, func() *detect.PushResult { return nil }
	d.Pieces = func() []detect.Entry {
		return []detect.Entry{detect.EvidencePiece("off", "no registry value", nil, detect.Complete, ""), detect.RecorderPiece("on")}
	}
	res, _ := d.detect(context.Background())
	want := []string{"registry", "pull-secret", "cloud", "nodes", "metrics", "istio-egress", "cilium-fqdn", "sign-in", "exposure",
		"crossplane", "evidence", "recorder", "otel-collector", "tempo", "jaeger", "datadog", "backstage"}
	got := make([]string, 0, len(res.Entries))
	for _, e := range res.Entries {
		got = append(got, e.Kind)
	}
	if !slices.Equal(got, want) || len(res.Entries) > detect.MaxEntries {
		t.Errorf("entries %v, want %v within %d", got, want, detect.MaxEntries)
	}
	if _, err := detect.Encode(res); err != nil {
		t.Error(err)
	}
}

// backstageObjects are a Backstage Deployment's pod and its Service on 7007.
func backstageObjects() []client.Object {
	return []client.Object{
		owned("idp", "backstage-1", "ReplicaSet", "backstage-7f9c", "ghcr.io/acme/backstage-backend:1.30"),
		service("idp", "backstage", nil, corev1.ServicePort{Name: "http", Port: 7007}),
	}
}

func TestDetectorAddsBackstage(t *testing.T) {
	c := newClient(routesInterceptor(), append([]client.Object{operatorDeployment()}, backstageObjects()...)...)
	if err := newDetector(c, servedAll).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, res := readResult(t, c)
	if e := entry(t, res, "backstage"); e.Endpoint != "http://backstage.idp.svc:7007" || e.Completeness != detect.Complete ||
		!slices.Contains(e.Objects, detect.Object{Kind: "Service", Namespace: "idp", Name: "backstage"}) {
		t.Errorf("backstage = %+v", e)
	}
}

// TestDetectionAddsNoGrant: the Services and pods detection reads are the
// ones the ClusterRole already grants.
func TestDetectionAddsNoGrant(t *testing.T) {
	b, err := os.ReadFile("../../charts/pacto-operator/templates/clusterrole.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"services", "pods"} {
		if !strings.Contains(string(b), "resources: ["+r+"]\n    verbs: [get, list, watch]") {
			t.Errorf("the ClusterRole grant on %s changed", r)
		}
	}
}

// A trace rule judged on part of the cluster (the watched namespaces or a
// sample of the pods) proves no single backend: its entries are partial.
func TestDetectorTracesOnPartOfTheClusterArePartial(t *testing.T) {
	c := newClient(routesInterceptor(), traceObjects()...)
	if err := newDetector(c, servedAll, "obs", "tracing").Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, res := readResult(t, c)
	for _, kind := range []string{"otel-collector", "tempo", "jaeger"} {
		if e := entry(t, res, kind); e.Completeness != detect.Partial || !strings.Contains(e.Reason, scopedReason) {
			t.Errorf("scoped %s = %+v", kind, e)
		}
	}

	pages := 0
	big := func(l client.ObjectList, i int) {
		if l, ok := l.(*corev1.PodList); ok {
			l.Items = make([]corev1.Pod, detect.MaxPods/2+1)
			for j := range l.Items {
				l.Items[j] = *pod("tracing", "p", "", "jaegertracing/all-in-one:1.60")
				l.Items[j].UID = types.UID(strings.Repeat("u", i) + string(rune(j)))
			}
		}
	}
	c = newClient(pagedList(&pages, 3, big), operatorDeployment(), service("tracing", "jaeger-query", nil, corev1.ServicePort{Port: 16686}))
	_ = newDetector(c, servedAll).Sync(context.Background())
	_, res = readResult(t, c)
	if e := entry(t, res, "jaeger"); e.Completeness != detect.Partial || !strings.Contains(e.Reason, "sampled the first 20000 running pods") {
		t.Errorf("sampled jaeger = %+v", e)
	}
}

// TestDetectorSampledBackstageHasNoEndpoint: a Backstage judged on a sample of
// the pods keeps its row but gives the dashboard no address to read, so a
// look-alike in a namespace that sorts first never wins the pick.
func TestDetectorSampledBackstageHasNoEndpoint(t *testing.T) {
	pages := 0
	big := func(l client.ObjectList, i int) {
		if l, ok := l.(*corev1.PodList); ok {
			l.Items = make([]corev1.Pod, detect.MaxPods/2+1)
			for j := range l.Items {
				l.Items[j] = *pod("aaa", "p", "", "registry.example/x/backstage:1")
				l.Items[j].UID = types.UID(strings.Repeat("u", i) + string(rune(j)))
			}
		}
	}
	c := newClient(pagedList(&pages, 3, big), operatorDeployment(), service("aaa", "backstage", nil, corev1.ServicePort{Port: 7007}))
	_ = newDetector(c, servedAll).Sync(context.Background())
	_, res := readResult(t, c)
	if e := entry(t, res, "backstage"); e.Endpoint != "" || e.Completeness != detect.Partial || !strings.Contains(e.Reason, "sampled the first 20000 running pods") {
		t.Errorf("sampled backstage = %+v", e)
	}
}

// TestDetectorReportsBackstageNotDetected: a cluster with no Backstage gets a
// partial "not detected" row; a sampled read keeps the sampled reason too.
func TestDetectorReportsBackstageNotDetected(t *testing.T) {
	c := newClient(routesInterceptor(), operatorDeployment())
	if err := newDetector(c, servedAll).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, res := readResult(t, c)
	if e := entry(t, res, "backstage"); e.Value != detect.NotDetected || e.Completeness != detect.Partial || e.Endpoint != "" ||
		!strings.Contains(e.Reason, "dashboard.backstage.url") {
		t.Errorf("backstage = %+v", e)
	}

	pages := 0
	big := func(l client.ObjectList, i int) {
		if l, ok := l.(*corev1.PodList); ok {
			l.Items = make([]corev1.Pod, detect.MaxPods/2+1)
			for j := range l.Items {
				l.Items[j] = *pod("aaa", "p", "", "registry.example/x/web:1")
				l.Items[j].UID = types.UID(strings.Repeat("u", i) + string(rune(j)))
			}
		}
	}
	c = newClient(pagedList(&pages, 3, big), operatorDeployment())
	_ = newDetector(c, servedAll).Sync(context.Background())
	_, res = readResult(t, c)
	if e := entry(t, res, "backstage"); e.Value != detect.NotDetected || e.Completeness != detect.Partial ||
		!strings.Contains(e.Reason, "dashboard.backstage.url") || !strings.Contains(e.Reason, "sampled the first 20000 running pods") {
		t.Errorf("sampled backstage = %+v", e)
	}
}
