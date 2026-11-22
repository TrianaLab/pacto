/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package linker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/observer"
	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const (
	opNS     = "pacto-system"
	opDeploy = "pacto-operator"
	cmName   = opDeploy + "-detected"
	annRef   = "oci://ghcr.io/acme/contracts/payments:2.3.0"
)

var t0 = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = pactov1alpha1.AddToScheme(s)
	return s
}

func controlledBy(kind, name string) []metav1.OwnerReference {
	yes := true
	return []metav1.OwnerReference{{Kind: kind, Name: name, UID: types.UID(kind + "/" + name), Controller: &yes}}
}

// deployment is Deployment shop/name running image@digest through its
// ReplicaSet, carrying annotation when set.
func deployment(name, annotation, image, digest string) []client.Object {
	labels := map[string]string{"app": name}
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: name},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}}}}
	if annotation != "" {
		d.Annotations = map[string]string{AnnotationContract: annotation}
	}
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: name + "-rs", UID: types.UID("ReplicaSet/" + name + "-rs"),
		OwnerReferences: controlledBy("Deployment", name)}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: name + "-0", Labels: labels, OwnerReferences: controlledBy("ReplicaSet", name+"-rs")},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: image}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{
			{Name: "app", ImageID: strings.Split(image, ":")[0] + "@" + digest}}}}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: name}, Spec: corev1.ServiceSpec{Selector: labels}}
	return []client.Object{d, rs, pod, svc}
}

func cluster(objs ...client.Object) []client.Object {
	return append(objs, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: opNS, Name: opDeploy, UID: "op-uid"}})
}

func newClient(funcs interceptor.Funcs, objs ...client.Object) client.WithWatch {
	return fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objs...).WithInterceptorFuncs(funcs).WithReturnManagedFields().Build()
}

// evaluator is a fake Evaluate: it records each transient Pacto and judges it
// as service at version, adding one observation window per call.
type evaluator struct {
	service string
	seen    []*pactov1alpha1.Pacto
	anon    []*pactov1alpha1.Pacto
	fail    bool
}

func (e *evaluator) judge(_ context.Context, p *pactov1alpha1.Pacto) {
	e.seen = append(e.seen, p.DeepCopy())
	if e.fail {
		p.Status.ContractStatus = pactov1alpha1.ContractStatusUnknown
		return
	}
	p.Status.ContractStatus = pactov1alpha1.ContractStatusCompliant
	p.Status.Contract = &pactov1alpha1.ContractInfo{ServiceName: e.service, Version: "2.3.0", ResolvedRef: p.Spec.ContractRef.OCI}
	p.Status.ObservationWindows = append(p.Status.ObservationWindows, pactov1alpha1.ObservationWindow{Kind: "dependency", Subject: "ledger"})
}

func (e *evaluator) judgeAnonymously(ctx context.Context, p *pactov1alpha1.Pacto) {
	e.anon = append(e.anon, p.DeepCopy())
	e.judge(ctx, p)
}

func newLinker(c client.Client, e *evaluator) *Linker {
	l, _ := ForOperator(Config{DeploymentName: opDeploy, Namespace: opNS, Cluster: "cluster-uid", Reader: c, Writer: c})
	l.Evaluate, l.EvaluateAnonymous = e.judge, e.judgeAnonymously
	l.Now = func() time.Time { return t0 }
	return l
}

func written(t *testing.T, c client.Client) (*corev1.ConfigMap, core.LinkedTargets) {
	t.Helper()
	cm := &corev1.ConfigMap{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: opNS, Name: cmName}, cm); err != nil {
		t.Fatal(err)
	}
	l, err := core.DecodeLinked([]byte(cm.Data[core.LinkedConfigMapKey]))
	if err != nil {
		t.Fatal(err)
	}
	return cm, l
}

func sync(t *testing.T, l *Linker) {
	t.Helper()
	if err := l.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func hasLimitation(l core.LinkedTargets, code string) bool {
	for _, lim := range l.Limitations {
		if lim.Code == code {
			return true
		}
	}
	return false
}

func TestLinkerWritesOnlyItsKey(t *testing.T) {
	c := newClient(interceptor.Funcs{}, cluster(deployment("payments", annRef, "ghcr.io/acme/payments:2.3.0", dA)...)...)
	if err := c.Apply(context.Background(), corev1ac.ConfigMap(cmName, opNS).WithData(map[string]string{"detected.json": "{}"}),
		client.FieldOwner(detect.FieldManager), client.ForceOwnership); err != nil {
		t.Fatal(err)
	}
	l := newLinker(c, &evaluator{service: "payments"})
	sync(t, l)
	cm, got := written(t, c)
	if cm.Data["detected.json"] != "{}" {
		t.Fatalf("the detection key did not survive: %v", cm.Data)
	}
	if len(cm.OwnerReferences) != 1 || cm.OwnerReferences[0].UID != "op-uid" {
		t.Fatalf("owner: %+v", cm.OwnerReferences)
	}
	if got.Cluster != "cluster-uid" || got.AsOf != "2026-11-22T10:00:00Z" || got.Completeness != fleet.CompletenessComplete || len(got.Targets) != 1 {
		t.Fatalf("linked: %+v", got)
	}
	if !hasLimitation(got, core.LimitationLinkDigestOff) {
		t.Fatalf("no registry must say the digest signal is off: %+v", got.Limitations)
	}
	owners := map[string]string{}
	for _, f := range cm.ManagedFields {
		owners[f.Manager] = f.FieldsV1.GetRawString()
	}
	if !strings.Contains(owners[core.LinkerFieldManager], `"f:linked.json"`) || strings.Contains(owners[core.LinkerFieldManager], "detected.json") ||
		!strings.Contains(owners[detect.FieldManager], `"f:detected.json"`) {
		t.Fatalf("field managers: %v", owners)
	}
}

func TestLinkerSkipsTargetedWorkload(t *testing.T) {
	cr := &pactov1alpha1.Pacto{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "payments"},
		Spec: pactov1alpha1.PactoSpec{Target: pactov1alpha1.TargetRef{ServiceName: "payments"}}}
	e := &evaluator{service: "payments"}
	c := newClient(interceptor.Funcs{}, cluster(append(deployment("payments", annRef, "ghcr.io/acme/payments:2.3.0", dA), cr)...)...)
	l := newLinker(c, e)
	sync(t, l)
	if _, got := written(t, c); len(got.Targets) != 0 || len(e.seen) != 0 {
		t.Fatalf("a workload a Pacto targets was linked: %+v", got.Targets)
	}
}

func TestLinkerEvaluatesLinkedWorkload(t *testing.T) {
	e := &evaluator{service: "payments"}
	c := newClient(interceptor.Funcs{}, cluster(deployment("payments", annRef, "ghcr.io/acme/payments:2.3.0", dA)...)...)
	l := newLinker(c, e)
	sync(t, l)
	if len(e.seen) != 1 {
		t.Fatalf("evaluations: %d", len(e.seen))
	}
	p := e.seen[0]
	if p.Spec.ContractRef.OCI != annRef || p.Spec.Target.ServiceName != "payments" ||
		p.Spec.Target.WorkloadRef == nil || p.Spec.Target.WorkloadRef.Kind != "Deployment" || p.Spec.Target.WorkloadRef.Name != "payments" ||
		len(p.Spec.Target.InterfaceBindings) != 0 {
		t.Fatalf("transient spec: %+v", p.Spec)
	}
	v := l.Current().Targets[0]
	if v.Metadata.Name != "Deployment/payments" || v.Metadata.Labels[core.LinkedByLabel] != fleet.LinkSignalAnnotation ||
		v.Metadata.Labels[core.LinkedValueLabel] != annRef || v.Status.ContractStatus != "Compliant" {
		t.Fatalf("view: %+v", v)
	}
	if s := l.Siblings(); len(s) != 1 || s[0] != (observerSibling("shop", "payments", annRef)) {
		t.Fatalf("siblings: %+v", s)
	}
}

func TestLinkerKeepsSinceWhileServiceUnchanged(t *testing.T) {
	e := &evaluator{service: "payments"}
	c := newClient(interceptor.Funcs{}, cluster(deployment("payments", annRef, "ghcr.io/acme/payments:2.3.0", dA)...)...)
	l := newLinker(c, e)
	sync(t, l)
	l.Now = func() time.Time { return t0.Add(time.Hour) }
	sync(t, l)
	if got := l.Current().Targets[0].Metadata.Labels[core.LinkedSinceLabel]; got != "2026-11-22T10:00:00Z" {
		t.Fatalf("since moved while the service stayed: %s", got)
	}
	e.service = "billing"
	sync(t, l)
	if got := l.Current().Targets[0].Metadata.Labels[core.LinkedSinceLabel]; got != "2026-11-22T11:00:00Z" {
		t.Fatalf("a new service is a new link: %s", got)
	}
}

// A restart keeps Since from linked.json; only the stabilization windows
// start over.
func TestLinkerKeepsSinceAcrossRestart(t *testing.T) {
	c := newClient(interceptor.Funcs{}, cluster(deployment("payments", annRef, "ghcr.io/acme/payments:2.3.0", dA)...)...)
	first := newLinker(c, &evaluator{service: "payments"})
	first.Now = func() time.Time { return t0.Add(-2 * time.Hour) }
	sync(t, first)

	again := newLinker(c, &evaluator{service: "payments"})
	sync(t, again)
	if got := again.Current().Targets[0].Metadata.Labels[core.LinkedSinceLabel]; got != "2026-11-22T08:00:00Z" {
		t.Fatalf("since after a restart: %s", got)
	}
}

func TestLinkerRestartRestartsWindows(t *testing.T) {
	c := newClient(interceptor.Funcs{}, cluster(deployment("payments", annRef, "ghcr.io/acme/payments:2.3.0", dA)...)...)
	e := &evaluator{service: "payments"}
	l := newLinker(c, e)
	sync(t, l)
	sync(t, l)
	if n := len(e.seen[1].Status.ObservationWindows); n != 1 {
		t.Fatalf("the second cycle got %d windows, want the first cycle's one", n)
	}
	e2 := &evaluator{service: "payments"}
	sync(t, newLinker(c, e2))
	if n := len(e2.seen[0].Status.ObservationWindows); n != 0 {
		t.Fatalf("a restarted linker carried %d windows", n)
	}
}

// digestLinker links by digest through f: payments 2.3.0 published at
// reg.io/c/payments with an images record naming reg.io/payments@dA.
func digestLinker(t *testing.T, c client.Client, e *evaluator, f *fakeIndex) *Linker {
	t.Helper()
	l := newLinker(c, e)
	tag := f.publishVersion(t, "payments", "reg.io/c/payments", "2.3.0", "reg.io/payments", dA)
	l.Tags = func(context.Context) ([]string, error) { return []string{tag}, nil }
	l.Registry = f
	return l
}

func TestLinkerLinksByDigest(t *testing.T) {
	e := &evaluator{service: "payments"}
	c := newClient(interceptor.Funcs{}, cluster(deployment("payments", "", "reg.io/payments:2.3.0", dA)...)...)
	l := digestLinker(t, c, e, newFake())
	sync(t, l)
	got := l.Current()
	if len(got.Targets) != 1 || got.Targets[0].Metadata.Labels[core.LinkedValueLabel] != dA || len(got.Limitations) != 0 ||
		!strings.HasPrefix(e.seen[0].Spec.ContractRef.OCI, "reg.io/c/payments@sha256:") {
		t.Fatalf("linked: %+v", got)
	}
}

func TestLinkerEntryServiceMismatchAmbiguous(t *testing.T) {
	e := &evaluator{service: "impostor"}
	c := newClient(interceptor.Funcs{}, cluster(deployment("payments", "", "reg.io/payments:2.3.0", dA)...)...)
	f := newFake()
	l := digestLinker(t, c, e, f)
	for tag, en := range f.entries {
		en.Name = "payments"
		f.entries[tag] = en
	}
	sync(t, l)
	got := l.Current()
	if len(got.Targets) != 0 || !hasLimitation(got, fleet.LimitationInferredReplacementAmbiguous) {
		t.Fatalf("a bundle disagreeing with its entry linked: %+v", got)
	}
}

func TestLinkerIndexBoundIsPartial(t *testing.T) {
	c := newClient(interceptor.Funcs{}, cluster(deployment("payments", "", "reg.io/payments:2.3.0", dA)...)...)
	f := newFake()
	l := digestLinker(t, c, &evaluator{service: "payments"}, f)
	tags := make([]string, 0, maxIndexReadsPerCycle+1)
	for i := range maxIndexReadsPerCycle + 1 {
		tags = append(tags, f.publishVersion(t, fmt.Sprintf("svc%d", i), "reg.io/c/x", "1.0.0", "reg.io/x", dB))
	}
	l.Tags = func(context.Context) ([]string, error) { return tags, nil }
	sync(t, l)
	if got := l.Current(); got.Completeness != fleet.CompletenessPartial || !hasLimitation(got, LimitationIndexPartial) {
		t.Fatalf("an index read past the bound is complete: %+v", got.Limitations)
	}
}

func TestLinkerRegistryErrorIsPartial(t *testing.T) {
	c := newClient(interceptor.Funcs{}, cluster(deployment("payments", "", "reg.io/payments:2.3.0", dA)...)...)
	l := digestLinker(t, c, &evaluator{service: "payments"}, newFake())
	l.Tags = func(context.Context) ([]string, error) { return nil, errors.New("unreachable") }
	sync(t, l)
	if got := l.Current(); got.Completeness != fleet.CompletenessPartial || !hasLimitation(got, LimitationIndexPartial) || len(got.Targets) != 0 {
		t.Fatalf("a registry error: %+v", got)
	}
}

func TestLinkerNoRegistryAnnotationOnly(t *testing.T) {
	e := &evaluator{service: "payments"}
	c := newClient(interceptor.Funcs{}, cluster(append(deployment("payments", annRef, "ghcr.io/acme/payments:2.3.0", dA),
		deployment("billing", "", "reg.io/payments:2.3.0", dA)...)...)...)
	l := newLinker(c, e)
	sync(t, l)
	got := l.Current()
	if len(got.Targets) != 1 || got.Targets[0].Metadata.Name != "Deployment/payments" || got.Completeness != fleet.CompletenessComplete ||
		!hasLimitation(got, core.LimitationLinkDigestOff) {
		t.Fatalf("no registry: %+v", got)
	}
}

func TestLinkerCappedIsPartial(t *testing.T) {
	objs := make([]client.Object, 0, 12)
	for _, n := range []string{"a", "b", "c"} {
		objs = append(objs, deployment(n, annRef, "ghcr.io/acme/payments:2.3.0", dA)...)
	}
	c := newClient(interceptor.Funcs{}, cluster(objs...)...)
	l := newLinker(c, &evaluator{service: "payments"})
	sync(t, l)
	_, full := written(t, c)
	b, _ := core.EncodeLinked(full)
	l.maxBytes = len(b) - 10
	sync(t, l)
	_, got := written(t, c)
	if len(got.Targets) != 2 || got.Targets[1].Metadata.Name != "Deployment/b" || got.Completeness != fleet.CompletenessPartial ||
		!hasLimitation(got, LimitationTargetsCapped) {
		t.Fatalf("capped: %d targets %+v", len(got.Targets), got.Limitations)
	}
}

// A forbidden list and a failed write never stop the operator: Sync returns
// nil and what it wrote is partial.
func TestLinkerFirstSyncListErrorDoesNotStopManager(t *testing.T) {
	conflict := true
	c := newClient(interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*batchv1.CronJobList); ok && (&client.ListOptions{}).ApplyOptions(opts).Namespace == "shop" {
				return apierrors.NewForbidden(schema.GroupResource{Resource: "cronjobs"}, "", errors.New("no"))
			}
			return c.List(ctx, list, opts...)
		},
		Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
			if conflict {
				conflict = false
				return apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, cmName, errors.New("conflict"))
			}
			return c.Apply(ctx, obj, opts...)
		},
	}, cluster(deployment("payments", annRef, "ghcr.io/acme/payments:2.3.0", dA)...)...)
	l := newLinker(c, &evaluator{service: "payments"})
	l.Watched = []string{"shop", "other"}
	sync(t, l)
	sync(t, l)
	if _, got := written(t, c); got.Completeness != fleet.CompletenessPartial || !hasLimitation(got, fleet.LimitationSourcePartial) {
		t.Fatalf("a forbidden namespace: %+v", got)
	}
}

func TestLinkerPodListErrorIsPartial(t *testing.T) {
	c := newClient(interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*corev1.PodList); ok {
				return errors.New("down")
			}
			return c.List(ctx, list, opts...)
		},
	}, cluster(deployment("payments", annRef, "ghcr.io/acme/payments:2.3.0", dA)...)...)
	l := newLinker(c, &evaluator{service: "payments"})
	sync(t, l)
	if got := l.Current(); got.Completeness != fleet.CompletenessPartial || len(got.Targets) != 0 {
		t.Fatalf("pods unread: %+v", got)
	}
}

func TestLinkerAnnotationOutsideIndexPullsAnonymously(t *testing.T) {
	e := &evaluator{service: "payments"}
	c := newClient(interceptor.Funcs{}, cluster(append(deployment("payments", annRef, "ghcr.io/acme/payments:2.3.0", dA),
		deployment("billing", "oci://reg.io/c/payments:2.3.0", "ghcr.io/acme/billing:1", dB)...)...)...)
	l := digestLinker(t, c, e, newFake())
	sync(t, l)
	if len(e.anon) != 1 || e.anon[0].Spec.ContractRef.OCI != annRef || len(e.seen) != 2 {
		t.Fatalf("anonymous %d (%v), all %d: an annotation outside the index uses no credentials; an indexed one does",
			len(e.anon), e.anon, len(e.seen))
	}
}

func TestLinkerUnreadableContractIsUnknown(t *testing.T) {
	c := newClient(interceptor.Funcs{}, cluster(deployment("payments", annRef, "ghcr.io/acme/payments:2.3.0", dA)...)...)
	l := newLinker(c, &evaluator{fail: true})
	sync(t, l)
	if got := l.Current(); len(got.Targets) != 0 || got.Completeness != fleet.CompletenessPartial || !hasLimitation(got, LimitationAnnotationUnreadable) {
		t.Fatalf("annotation: %+v", got)
	}
	c = newClient(interceptor.Funcs{}, cluster(deployment("payments", "", "reg.io/payments:2.3.0", dA)...)...)
	l = digestLinker(t, c, &evaluator{fail: true}, newFake())
	sync(t, l)
	if got := l.Current(); len(got.Targets) != 0 || !hasLimitation(got, fleet.LimitationSourcePartial) {
		t.Fatalf("digest: %+v", got)
	}
}

func TestLinkerMatchLimitationAndJobs(t *testing.T) {
	cron := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "nightly"}}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "nightly-1", UID: "Job/nightly-1", OwnerReferences: controlledBy("CronJob", "nightly")}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "nightly-1-x", OwnerReferences: controlledBy("Job", "nightly-1")},
		Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "reg.io/payments:2.3.0"}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Name: "app", ImageID: "reg.io/payments@" + dA}}}}
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "db", Annotations: map[string]string{AnnotationContract: "free text"}}}
	orphan := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "lone"}}
	loneJob := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "once"}}
	pending := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "p"}, Status: corev1.PodStatus{Phase: corev1.PodPending}}
	c := newClient(interceptor.Funcs{}, cluster(cron, job, pod, sts, orphan, loneJob, pending)...)
	l := digestLinker(t, c, &evaluator{service: "payments"}, newFake())
	sync(t, l)
	got := l.Current()
	if len(got.Targets) != 1 || got.Targets[0].Metadata.Name != "CronJob/nightly" || !hasLimitation(got, LimitationAnnotationInvalid) {
		t.Fatalf("linked: %+v", got)
	}
}

func TestLinkerWaitsForItsOwner(t *testing.T) {
	c := newClient(interceptor.Funcs{}, deployment("payments", annRef, "ghcr.io/acme/payments:2.3.0", dA)...)
	l := newLinker(c, &evaluator{service: "payments"})
	sync(t, l)
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: opNS, Name: cmName}, &corev1.ConfigMap{}); !apierrors.IsNotFound(err) {
		t.Fatalf("written with no owner: %v", err)
	}
	if l.Current().SchemaVersion != "" || l.Siblings() != nil {
		t.Fatal("a linker that never ran has no result")
	}
}

func TestLinkerMisconfigured(t *testing.T) {
	if err := (&Linker{}).Sync(context.Background()); err == nil {
		t.Fatal("a linker with nothing configured must stop the manager")
	}
}

func TestLinkerSeedIgnoresUnreadablePayload(t *testing.T) {
	bad := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: opNS, Name: cmName}, Data: map[string]string{core.LinkedConfigMapKey: "{"}}
	c := newClient(interceptor.Funcs{}, cluster(append(deployment("payments", annRef, "ghcr.io/acme/payments:2.3.0", dA), bad)...)...)
	l := newLinker(c, &evaluator{service: "payments"})
	sync(t, l)
	if got := l.Current().Targets[0].Metadata.Labels[core.LinkedSinceLabel]; got != "2026-11-22T10:00:00Z" {
		t.Fatalf("since: %s", got)
	}
}

func TestLinkerStartStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := newClient(interceptor.Funcs{}, cluster()...)
	if err := newLinker(c, &evaluator{}).Start(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRefRepository(t *testing.T) {
	for in, want := range map[string]string{
		"oci://reg.io/c/payments:2.3.0":           "reg.io/c/payments",
		"reg.io/c/payments@" + dA:                 "reg.io/c/payments",
		"oci://localhost:5000/c/payments":         "localhost:5000/c/payments",
		"oci://localhost:5000/c/payments:1@" + dA: "localhost:5000/c/payments",
	} {
		if got := refRepository(in); got != want {
			t.Errorf("refRepository(%q) = %q, want %q", in, got, want)
		}
	}
}

func observerSibling(ns, service, ref string) observer.LinkedSibling {
	return observer.LinkedSibling{Namespace: ns, ServiceName: service, ResolvedRef: ref, Version: "2.3.0"}
}
