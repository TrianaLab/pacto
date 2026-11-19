/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const snapCluster = "6f1e2c3d-uid"

type targetsEnv struct {
	p    *TargetPublisher
	reg  *testRegistry
	key  ed25519.PrivateKey
	mu   sync.Mutex
	logs []string
}

func (e *targetsEnv) logged() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return strings.Join(e.logs, "\n")
}

func podTemplate(c corev1.Container) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{c}}}
}

// snapshotObjects is a Pacto that targets StatefulSet pay-0 and one workload
// of each kind the snapshot reads.
func snapshotObjects() []client.Object {
	p := pactoObj("pay", "pay", "sha256:"+strings.Repeat("a", 64), "Compliant", 1)
	_ = unstructured.SetNestedMap(p.Object, map[string]any{"serviceName": "pay",
		"workloadRef": map[string]any{"kind": "StatefulSet", "name": "pay-0"}}, "spec", "target")
	meta := func(name string) metav1.ObjectMeta { return metav1.ObjectMeta{Namespace: ns, Name: name} }
	c := corev1.Container{Name: "app", Image: "reg.example/app:1", Ports: []corev1.ContainerPort{{ContainerPort: 8080}}}
	return []client.Object{p,
		&appsv1.Deployment{ObjectMeta: meta("web"), Spec: appsv1.DeploymentSpec{Template: podTemplate(c)}},
		&appsv1.StatefulSet{ObjectMeta: meta("pay-0"), Spec: appsv1.StatefulSetSpec{Template: podTemplate(c)}},
		&appsv1.DaemonSet{ObjectMeta: meta("agent"), Spec: appsv1.DaemonSetSpec{Template: podTemplate(c)}},
		&batchv1.CronJob{ObjectMeta: meta("nightly"), Spec: batchv1.CronJobSpec{JobTemplate: batchv1.JobTemplateSpec{
			Spec: batchv1.JobSpec{Template: podTemplate(c)}}}},
	}
}

func newTargetsEnv(t *testing.T, c client.Reader) *targetsEnv {
	t.Helper()
	reg := newTestRegistry(t)
	repo, err := ocireferrer.Open(reg.host+"/acme/pacto-targets", ocireferrer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	e := &targetsEnv{reg: reg, key: key}
	e.p = &TargetPublisher{Client: c, Repo: repo, Signer: edSigner(key), ClusterID: snapCluster,
		Publisher: ServiceAccountUser("pacto-system", "pacto-operator-delivery-recorder"),
		Mode:      func(context.Context) string { return "referrers=native listing=catalog" },
		Now:       func() time.Time { return time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC) }, Interval: time.Minute,
		Log: funcr.New(func(prefix, args string) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.logs = append(e.logs, prefix+" "+args)
		}, funcr.Options{Verbosity: 1})}
	return e
}

func fakeReader(objs ...client.Object) client.WithWatch {
	return fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objs...).Build()
}

// pushed reads the published snapshot back: the bundle verifies with the
// recorder's key and opens as a target-snapshot statement.
func (e *targetsEnv) pushed(t *testing.T) (core.TargetSnapshot, []byte) {
	t.Helper()
	bundle, _, err := deliveryrecord.FetchTargetSnapshot(context.Background(), e.p.Repo, "cluster-"+snapCluster)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (signing.Policy{Key: e.key.Public()}).Verify(bundle, nil); err != nil {
		t.Fatalf("signature: %v", err)
	}
	st, _, err := core.OpenTargetSnapshot(bundle, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return st.Predicate, bundle
}

func TestTargetPublisherPublishesSnapshot(t *testing.T) {
	e := newTargetsEnv(t, fakeReader(snapshotObjects()...))
	if err := e.p.publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, _ := e.pushed(t)
	if s.Cluster != snapCluster || s.AsOf != "2026-11-17T10:00:00Z" || s.Completeness != fleet.CompletenessComplete ||
		s.RegistryMode != "referrers=native listing=catalog" ||
		s.Publisher != "system:serviceaccount:pacto-system:pacto-operator-delivery-recorder" || len(s.Limitations) != 0 {
		t.Fatalf("snapshot: %+v", s)
	}
	if len(s.Pactos) != 1 || s.Pactos[0].Metadata.Name != "pay" || s.Pactos[0].Spec.Target.Workload().Name != "pay-0" {
		t.Fatalf("pactos: %+v", s.Pactos)
	}
	kinds := map[string]string{}
	for _, w := range s.Workloads {
		kinds[w.Kind] = w.Name
		if w.Image == nil || w.Image.Value != "reg.example/app:1" || len(w.Ports) != 1 {
			t.Errorf("workload shape: %+v", w)
		}
	}
	if kinds["Deployment"] != "web" || kinds["StatefulSet"] != "pay-0" || kinds["DaemonSet"] != "agent" || kinds["CronJob"] != "nightly" {
		t.Fatalf("workloads: %v", kinds)
	}
	if !strings.Contains(e.logged(), "target snapshot published") {
		t.Fatalf("logs: %s", e.logged())
	}
}

// The workload a Pacto targets stays in the payload as an inferred service:
// fleet.Build drops it on read, where the Pacto's spec.target is known.
func TestTargetPublisherWorkloadOfPactoTargetStaysInferredInPayload(t *testing.T) {
	e := newTargetsEnv(t, fakeReader(snapshotObjects()...))
	if err := e.p.publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, _ := e.pushed(t)
	for _, w := range s.Workloads {
		if w.Kind == "StatefulSet" && w.Name == "pay-0" {
			return
		}
	}
	t.Fatalf("the targeted workload left the payload: %+v", s.Workloads)
}

func TestTargetPublisherListFailureIsPartial(t *testing.T) {
	c := interceptor.NewClient(fakeReader(snapshotObjects()...), interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if k := kindOf(list); k == "Pacto" || k == "CronJob" {
				return errors.New("forbidden: secret detail")
			}
			return c.List(ctx, list, opts...)
		}})
	e := newTargetsEnv(t, c)
	if err := e.p.publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, bundle := e.pushed(t)
	if s.Completeness != fleet.CompletenessPartial || len(s.Limitations) != 2 || len(s.Pactos) != 0 || len(s.Workloads) != 3 {
		t.Fatalf("snapshot: %+v", s)
	}
	for _, l := range s.Limitations {
		if l.Code != fleet.LimitationSourcePartial {
			t.Errorf("limitation: %+v", l)
		}
	}
	if strings.Contains(string(bundle), "secret detail") {
		t.Fatal("an error text reached the payload")
	}
}

func TestTargetPublisherRefusesOversize(t *testing.T) {
	e := newTargetsEnv(t, fakeReader(snapshotObjects()...))
	e.p.maxBytes = 100
	err := e.p.publish(context.Background())
	if err == nil || !strings.Contains(err.Error(), "over the 100 byte bound") {
		t.Fatalf("published an oversize snapshot: %v", err)
	}
	if _, _, err := deliveryrecord.FetchTargetSnapshot(context.Background(), e.p.Repo, "cluster-"+snapCluster); err == nil {
		t.Fatal("something was pushed")
	}
	// Start logs the refusal.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = e.p.Start(ctx)
	if !strings.Contains(e.logged(), "target snapshot not published") || !strings.Contains(e.logged(), "byte bound") {
		t.Fatalf("logs: %s", e.logged())
	}
}

func TestTargetPublisherSignAndPushFailures(t *testing.T) {
	e := newTargetsEnv(t, fakeReader())
	e.p.Signer = failingSigner{}
	if err := e.p.publish(context.Background()); err == nil {
		t.Fatal("published with a failing signer")
	}
	e.p.Signer = edSigner(e.key)
	e.p.ClusterID = "not a tag"
	if err := e.p.publish(context.Background()); err == nil {
		t.Fatal("pushed an untaggable cluster")
	}
}

func TestTargetPublisherNeedsLeaderElection(t *testing.T) {
	if !(&TargetPublisher{}).NeedLeaderElection() {
		t.Fatal("two recorders would write one cluster tag")
	}
}

func TestTargetPublisherPushesOnlyToTargets(t *testing.T) {
	e := newTargetsEnv(t, fakeReader())
	streams, _ := ocireferrer.Open(e.reg.host+"/delivery/streams", ocireferrer.Options{})
	e.p.Repo = streams
	if err := e.p.publish(context.Background()); err == nil || !strings.Contains(err.Error(), "pacto-targets") {
		t.Fatalf("pushed outside pacto-targets: %v", err)
	}
}

func TestTargetSnapshotCarriesNoEnv(t *testing.T) {
	c := corev1.Container{Name: "app", Image: "reg.example/app:1",
		Env: []corev1.EnvVar{{Name: "DB_PASSWORD", Value: "hunter2"},
			{Name: "API_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "creds"}, Key: "apikey"}}}},
		ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
			Path: "/healthz?token=secret", Port: intstr.FromInt32(8080),
			HTTPHeaders: []corev1.HTTPHeader{{Name: "X-Auth-Header", Value: "bearer-xyz"}}}}}}
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "leaky"},
		Spec: appsv1.DeploymentSpec{Template: podTemplate(c)}}
	e := newTargetsEnv(t, fakeReader(d))
	if err := e.p.publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, bundle := e.pushed(t)
	if len(s.Workloads) != 1 || s.Workloads[0].Name != "leaky" {
		t.Fatalf("workloads: %+v", s.Workloads)
	}
	payload := string(bundle)
	for _, leak := range []string{"DB_PASSWORD", "hunter2", "API_TOKEN", "creds", "apikey", "token", "healthz",
		"X-Auth-Header", "bearer-xyz", "probes"} {
		if strings.Contains(payload, leak) {
			t.Errorf("payload carries %q", leak)
		}
	}
}

func TestTargetPublisherPublishesOnTickAndStopsOnCancel(t *testing.T) {
	e := newTargetsEnv(t, fakeReader())
	ticks := make(chan time.Time)
	var asked time.Duration
	e.p.Tick = func(d time.Duration) <-chan time.Time { asked = d; return ticks }
	published := 0
	e.p.Mode = func(context.Context) string { published++; return "" }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- e.p.Start(ctx) }()
	ticks <- time.Now() // received only after the first publish
	ticks <- time.Now() // received only after the second
	cancel()
	if err := <-done; err != nil || published != 3 || asked != time.Minute {
		t.Fatalf("Start: %v, %d publishes, interval %s", err, published, asked)
	}
}

func TestTargetPublisherDefaultTicker(t *testing.T) {
	e := newTargetsEnv(t, fakeReader())
	e.p.Log = logr.Discard()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.p.Start(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestStoreModeRecordsProbeMode(t *testing.T) {
	s, _, _ := testStore(t)
	if got := s.Mode(context.Background()); got != "referrers=native listing=catalog" {
		t.Fatalf("mode %q", got)
	}
	if got := (&RecordStore{Streams: "not a host/x"}).Mode(context.Background()); got != "" {
		t.Fatalf("a failed probe recorded mode %q", got)
	}
}
