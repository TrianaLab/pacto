//go:build e2e
// +build e2e

/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package e2e holds the Pacto 2.0 END-TO-END ACCEPTANCE MATRIX (spec section 12). Every case drives the
// REAL operator pipeline against an envtest control plane: a Pacto CR plus its Kubernetes fixtures are
// created, the controller's Reconcile runs (collector -> evidence -> validation.Evaluate -> Findings +
// Coverage -> CR status), and the resulting status is asserted and then fed through the real dashboard
// mapping (pkg/dashboard source_k8s -> ComputeCompliance). Active health/metrics probes are genuine HTTP
// GETs against an httptest.Server; only the TCP dial target of the in-cluster Service authority is
// redirected to the test server (exactly what kube DNS/kube-proxy would do in a real cluster), so the
// prober is never stubbed away.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
	"github.com/trianalab/pacto-operator/internal/controller"
	"github.com/trianalab/pacto-operator/internal/loader"
	"github.com/trianalab/pacto/v2/pkg/dashboard"
)

var (
	testEnv      *envtest.Environment
	cfg          *rest.Config
	k8sClient    client.Client
	sharedLoader *loader.Loader

	// probeRedirects maps an in-cluster Service authority ("svc.ns.svc:port") to the real
	// host:port of a test server, so the collector's genuine prober reaches it. Populated per case.
	probeRedirects sync.Map

	testCtx = context.Background()
)

// stabWindow is the operator stabilization window used by every case. It is long enough that a fresh
// negative is always "within window" (-> Unknown); beyond-window cases backdate the status window.
const stabWindow = 30 * time.Minute

func TestMain(m *testing.M) {
	// Redirect ONLY the registered in-cluster Service authorities to their test servers. Every other dial
	// (the apiserver client builds its own transport, so this is not even in its path) passes through
	// untouched. Keep-alives off so a reused authority across cases never hits a stale pooled connection.
	tr := http.DefaultTransport.(*http.Transport)
	tr.DisableKeepAlives = true
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if v, ok := probeRedirects.Load(addr); ok {
			addr = v.(string)
		}
		return dialer.DialContext(ctx, network, addr)
	}

	if err := pactov1alpha1.AddToScheme(scheme.Scheme); err != nil {
		fmt.Fprintf(os.Stderr, "add scheme: %v\n", err)
		os.Exit(1)
	}

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		if dir := latestEnvTestBinaryDir(); dir != "" {
			testEnv.BinaryAssetsDirectory = dir
		}
	}

	var err error
	cfg, err = testEnv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "start envtest: %v\n", err)
		os.Exit(1)
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "new client: %v\n", err)
		_ = testEnv.Stop()
		os.Exit(1)
	}
	sharedLoader = loader.New()

	code := m.Run()
	_ = testEnv.Stop()
	os.Exit(code)
}

// latestEnvTestBinaryDir mirrors the controller suite fallback: newest bin/k8s/<ver> holding a
// kube-apiserver binary, used only when KUBEBUILDER_ASSETS is unset.
func latestEnvTestBinaryDir() string {
	base := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(base, e.Name(), "kube-apiserver")); err == nil {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return filepath.Join(base, names[len(names)-1])
}

// --- reconcile harness ------------------------------------------------------

// faultClient wraps a client.Client and injects an error for Deployment, Service or ConfigMap GETs of a
// chosen name, to drive COLLECTION_FAILED cases (a non-NotFound API error) through the full real pipeline
// without a real cluster outage. A failed Service GET exercises the dependency/interface Failed path; a
// failed ConfigMap GET exercises the configuration Failed path. All other calls pass through to the envtest
// apiserver.
type faultClient struct {
	client.Client
	failDeploymentName string
	failServiceName    string
	failConfigMapName  string
}

func (f faultClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*appsv1.Deployment); ok && key.Name == f.failDeploymentName {
		return apierrors.NewInternalError(fmt.Errorf("synthetic apiserver failure"))
	}
	if _, ok := obj.(*corev1.Service); ok && f.failServiceName != "" && key.Name == f.failServiceName {
		return apierrors.NewInternalError(fmt.Errorf("synthetic apiserver failure"))
	}
	if _, ok := obj.(*corev1.ConfigMap); ok && f.failConfigMapName != "" && key.Name == f.failConfigMapName {
		return apierrors.NewInternalError(fmt.Errorf("synthetic apiserver failure"))
	}
	return f.Client.Get(ctx, key, obj, opts...)
}

// reconcileResult carries the reconciled CR plus the events the recorder captured (for the no-leak scan).
type reconcileResult struct {
	pacto  *pactov1alpha1.Pacto
	events []string
}

// reconcileOpts configures a single reconcile pass.
type reconcileOpts struct {
	cl             client.Client             // defaults to k8sClient
	loader         controller.ContractLoader // defaults to sharedLoader
	metricsEnabled bool
	// probingDisabled turns OFF active Tier-A health probing so the passive Tier-B readiness-probe path is
	// exercised (M11). The zero value keeps probing ON, preserving every existing case.
	probingDisabled bool
}

// reconcile builds a real PactoReconciler (direct, strongly-consistent client; no background manager) and
// runs one Reconcile pass, returning the fresh CR and any recorded events. Calling it twice replays the
// real windowed-stabilization flow, because ObservationWindows survive in status between passes.
func reconcile(t *testing.T, name, ns string, opts reconcileOpts) reconcileResult {
	t.Helper()
	cl := opts.cl
	if cl == nil {
		cl = k8sClient
	}
	ldr := opts.loader
	if ldr == nil {
		ldr = sharedLoader
	}
	rec := record.NewFakeRecorder(500)
	r := &controller.PactoReconciler{
		Client:                   cl,
		Scheme:                   scheme.Scheme,
		Recorder:                 rec,
		Loader:                   ldr,
		StabilizationWindow:      stabWindow,
		EnableMetricsObservation: opts.metricsEnabled,
		// Active Tier-A health probing is opt-in (M4a). Existing e2e health assertions rely on it; the
		// passive Tier-B path is driven by opts.probingDisabled (M11).
		EnableProbing: !opts.probingDisabled,
	}
	req := ctrl.Request{NamespacedName: client.ObjectKey{Namespace: ns, Name: name}}
	if _, err := r.Reconcile(testCtx, req); err != nil {
		t.Fatalf("reconcile %s/%s: %v", ns, name, err)
	}
	var events []string
	for {
		select {
		case e := <-rec.Events:
			events = append(events, e)
		default:
			return reconcileResult{pacto: getPacto(t, name, ns), events: events}
		}
	}
}

func getPacto(t *testing.T, name, ns string) *pactov1alpha1.Pacto {
	t.Helper()
	p := &pactov1alpha1.Pacto{}
	if err := k8sClient.Get(testCtx, client.ObjectKey{Namespace: ns, Name: name}, p); err != nil {
		t.Fatalf("get pacto %s/%s: %v", ns, name, err)
	}
	return p
}

// backdateWindows moves every ObservationWindow's firstObservedNegativeAt an hour into the past, so the
// next reconcile treats a sustained negative as beyond the stabilization window (drives the confirmed
// violation). This exercises the operator's real time-based window logic via the persisted status field.
func backdateWindows(t *testing.T, name, ns string) {
	t.Helper()
	p := getPacto(t, name, ns)
	if len(p.Status.ObservationWindows) == 0 {
		t.Fatalf("expected at least one observation window to backdate on %s/%s", ns, name)
	}
	old := metav1.NewTime(time.Now().Add(-time.Hour))
	for i := range p.Status.ObservationWindows {
		p.Status.ObservationWindows[i].FirstObservedNegativeAt = old
	}
	if err := k8sClient.Status().Update(testCtx, p); err != nil {
		t.Fatalf("backdate windows %s/%s: %v", ns, name, err)
	}
}

// --- fixtures ---------------------------------------------------------------

var nsCounter int

func newNamespace(t *testing.T) string {
	t.Helper()
	nsCounter++
	name := fmt.Sprintf("e2e-%d-%d", time.Now().UnixNano()%1_000_000, nsCounter)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := k8sClient.Create(testCtx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	return name
}

func createPacto(t *testing.T, name, ns string, spec pactov1alpha1.PactoSpec) {
	t.Helper()
	p := &pactov1alpha1.Pacto{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Spec: spec}
	if err := k8sClient.Create(testCtx, p); err != nil {
		t.Fatalf("create pacto: %v", err)
	}
}

func createService(t *testing.T, ns, name string, port int32) {
	t.Helper()
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{"app": name}},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": name},
			Ports:    []corev1.ServicePort{{Name: "http", Port: port, TargetPort: intstr.FromInt32(port)}},
		},
	}
	if err := k8sClient.Create(testCtx, svc); err != nil {
		t.Fatalf("create service: %v", err)
	}
}

// createServiceFull creates a Service with explicit ports and annotations, for the metrics-discovery
// fixtures (prometheus.io annotations, named "metrics" port). Selector-backed like createService.
func createServiceFull(t *testing.T, ns, name string, ports []corev1.ServicePort, annotations map[string]string) {
	t.Helper()
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: ns,
			Labels:      map[string]string{"app": name},
			Annotations: annotations,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": name},
			Ports:    ports,
		},
	}
	if err := k8sClient.Create(testCtx, svc); err != nil {
		t.Fatalf("create service: %v", err)
	}
}

// createExternalNameService creates a type=ExternalName Service (no selector, no endpoints), so the
// dependency observer classifies it as OBSERVATION_UNSUPPORTED (spec section 7.6).
func createExternalNameService(t *testing.T, ns, name, externalName string) {
	t.Helper()
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeExternalName, ExternalName: externalName},
	}
	if err := k8sClient.Create(testCtx, svc); err != nil {
		t.Fatalf("create externalname service: %v", err)
	}
}

// createEndpointSlice creates an EndpointSlice for a Service with `ready` replicas covering `port`.
func createEndpointSlice(t *testing.T, ns, svcName string, port int32, ready int) {
	t.Helper()
	p := port
	eps := make([]discoveryv1.Endpoint, 0, ready)
	for i := 0; i < ready; i++ {
		yes := true
		eps = append(eps, discoveryv1.Endpoint{
			Addresses:  []string{fmt.Sprintf("10.244.0.%d", i+1)},
			Conditions: discoveryv1.EndpointConditions{Ready: &yes},
		})
	}
	if ready == 0 {
		// A slice that exists but has zero ready endpoints (the windowed-negative signal).
		no := false
		eps = append(eps, discoveryv1.Endpoint{
			Addresses:  []string{"10.244.0.250"},
			Conditions: discoveryv1.EndpointConditions{Ready: &no},
		})
	}
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName + "-slice",
			Namespace: ns,
			Labels:    map[string]string{discoveryv1.LabelServiceName: svcName},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports:       []discoveryv1.EndpointPort{{Port: &p}},
		Endpoints:   eps,
	}
	if err := k8sClient.Create(testCtx, slice); err != nil {
		t.Fatalf("create endpointslice: %v", err)
	}
}

// setEndpointSliceReady flips a Service's EndpointSlice to a single ready endpoint (recovery path).
func setEndpointSliceReady(t *testing.T, ns, svcName string, port int32) {
	t.Helper()
	slice := &discoveryv1.EndpointSlice{}
	if err := k8sClient.Get(testCtx, client.ObjectKey{Namespace: ns, Name: svcName + "-slice"}, slice); err != nil {
		t.Fatalf("get endpointslice: %v", err)
	}
	yes := true
	p := port
	slice.Ports = []discoveryv1.EndpointPort{{Port: &p}}
	slice.Endpoints = []discoveryv1.Endpoint{{
		Addresses:  []string{"10.244.0.1"},
		Conditions: discoveryv1.EndpointConditions{Ready: &yes},
	}}
	if err := k8sClient.Update(testCtx, slice); err != nil {
		t.Fatalf("update endpointslice: %v", err)
	}
}

// deployOpts controls the pod template of a created workload.
type deployOpts struct {
	volumes []corev1.Volume // raw volumes for persistence classification
	// readinessProbePort, when > 0, gives the main container that ContainerPort plus an httpGet READINESS
	// probe on it, so the passive Tier-B health fallback has usable evidence (M11).
	readinessProbePort int32
}

func createDeployment(t *testing.T, ns, name string, opts deployOpts) {
	t.Helper()
	replicas := int32(1)
	main := corev1.Container{Name: "main", Image: "app:1"}
	if opts.readinessProbePort > 0 {
		main.Ports = []corev1.ContainerPort{{ContainerPort: opts.readinessProbePort}}
		main.ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt32(opts.readinessProbePort)},
			},
		}
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec: corev1.PodSpec{
					Volumes:    opts.volumes,
					Containers: []corev1.Container{main},
				},
			},
		},
	}
	if err := k8sClient.Create(testCtx, dep); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
}

func createJob(t *testing.T, ns, name string) {
	t.Helper()
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers:    []corev1.Container{{Name: "main", Image: "app:1"}},
				},
			},
		},
	}
	if err := k8sClient.Create(testCtx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
}

func createConfigMap(t *testing.T, ns, name string, data map[string]string) {
	t.Helper()
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Data: data}
	if err := k8sClient.Create(testCtx, cm); err != nil {
		t.Fatalf("create configmap: %v", err)
	}
}

func createSecret(t *testing.T, ns, name string, data map[string][]byte) {
	t.Helper()
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Data: data}
	if err := k8sClient.Create(testCtx, sec); err != nil {
		t.Fatalf("create secret: %v", err)
	}
}

// createSiblingPacto creates a dependency's in-cluster Pacto CR whose status.contract.resolvedRef +
// spec.target.serviceName let the collector's real sibling resolution find its Service coordinates.
func createSiblingPacto(t *testing.T, ns, name, serviceName, resolvedRef, version string) {
	t.Helper()
	sib := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       pactov1alpha1.PactoSpec{Target: pactov1alpha1.TargetRef{ServiceName: serviceName}},
	}
	if err := k8sClient.Create(testCtx, sib); err != nil {
		t.Fatalf("create sibling pacto: %v", err)
	}
	sib.Status.Contract = &pactov1alpha1.ContractInfo{ServiceName: serviceName, Version: version, ResolvedRef: resolvedRef}
	sib.Status.ContractStatus = pactov1alpha1.ContractStatusCompliant
	if err := k8sClient.Status().Update(testCtx, sib); err != nil {
		t.Fatalf("set sibling status: %v", err)
	}
}

// --- probe servers ----------------------------------------------------------

// startProbeServer stands up a real httptest.Server and redirects the in-cluster authority
// "svc.ns.svc:port" to it, so the collector's genuine prober hits it. Registered redirect is removed on
// test cleanup.
func startProbeServer(t *testing.T, ns, svc string, port int32, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	authority := fmt.Sprintf("%s.%s.svc:%d", svc, ns, port)
	probeRedirects.Store(authority, srv.Listener.Addr().String())
	t.Cleanup(func() { probeRedirects.Delete(authority) })
}

// pointAtClosedPort redirects the in-cluster authority to a closed port, so the genuine prober gets an
// immediate connection-refused (a real transport error) rather than a slow DNS failure.
func pointAtClosedPort(t *testing.T, ns, svc string, port int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	authority := fmt.Sprintf("%s.%s.svc:%d", svc, ns, port)
	probeRedirects.Store(authority, addr)
	t.Cleanup(func() { probeRedirects.Delete(authority) })
}

func okHealth(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

// healthBodyCanary is a distinctive response body the prober reads for the 2xx check but must NEVER persist
// to status/findings (INV-5 no-body canary).
const healthBodyCanary = "HEALTH-BODY-LEAK-CANARY-2f9a"

func okHealthWithBody(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(healthBodyCanary))
}

func status404(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }

func status503(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }

// promMetrics serves a genuine Prometheus text-exposition body that the real expfmt parser accepts.
func promMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("# HELP http_requests_total Total requests.\n# TYPE http_requests_total counter\nhttp_requests_total 42\n"))
}

// substringButNotPrometheus serves 200 with a body that CONTAINS "# HELP" as a substring but does NOT
// parse as Prometheus (proves parse != substring, Refinement D).
func substringButNotPrometheus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("<html><body># HELP not really metrics </body></html>\n"))
}

// --- assertion helpers ------------------------------------------------------

func findingCodes(p *pactov1alpha1.Pacto) []string {
	out := make([]string, 0, len(p.Status.Findings))
	for _, f := range p.Status.Findings {
		out = append(out, f.Code)
	}
	return out
}

func findFinding(p *pactov1alpha1.Pacto, code string) *pactov1alpha1.FindingStatus {
	for i := range p.Status.Findings {
		if p.Status.Findings[i].Code == code {
			return &p.Status.Findings[i]
		}
	}
	return nil
}

func validationCodes(p *pactov1alpha1.Pacto) []string {
	var out []string
	if p.Status.Validation == nil {
		return out
	}
	for _, e := range p.Status.Validation.Errors {
		out = append(out, e.Code)
	}
	return out
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

func requireStatus(t *testing.T, p *pactov1alpha1.Pacto, want string) {
	t.Helper()
	if p.Status.ContractStatus != want {
		t.Fatalf("contractStatus = %q, want %q; findings=%v validation=%v",
			p.Status.ContractStatus, want, findingCodes(p), validationCodes(p))
	}
}

func requireFinding(t *testing.T, p *pactov1alpha1.Pacto, code string) {
	t.Helper()
	if !contains(findingCodes(p), code) {
		t.Fatalf("expected finding %q, got %v", code, findingCodes(p))
	}
}

func requireNoFinding(t *testing.T, p *pactov1alpha1.Pacto, code string) {
	t.Helper()
	if contains(findingCodes(p), code) {
		t.Fatalf("did not expect finding %q, got %v", code, findingCodes(p))
	}
}

// ipv4Pattern matches any dotted-quad IPv4 address (four octets). Legitimate status content — semver
// "1.0.0" (three parts), RFC3339 timestamps (dashes/colons), OCI refs — never forms a four-octet quad, so
// a match is a real EndpointSlice/ClusterIP leak.
var ipv4Pattern = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)

// ipv6Pattern matches obvious IPv6 forms: a "::" compression marker followed by a hex group, or four-plus
// colon-separated hex groups. RFC3339 times ("18:44:00", two colons) never reach four groups, so this does
// not false-positive on the timestamps status legitimately carries.
var ipv6Pattern = regexp.MustCompile(`::[0-9a-fA-F]{1,4}|(?:[0-9a-fA-F]{1,4}:){4,}[0-9a-fA-F]{1,4}`)

// requireNoIP asserts the serialized status/findings carry NO IPv4 or IPv6 address (INV-5). Fixtures use
// the 10.244.0.x range for EndpointSlice addresses; a ClusterIP or pod IP would also be a dotted quad. Any
// such address surfacing in findings or status is a leak.
func requireNoIP(t *testing.T, p *pactov1alpha1.Pacto) {
	t.Helper()
	b, err := json.Marshal(p.Status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	s := string(b)
	if m := ipv4Pattern.FindString(s); m != "" {
		t.Fatalf("INV-5 LEAK: IPv4 address %q found in serialized status: %s", m, s)
	}
	if m := ipv6Pattern.FindString(s); m != "" {
		t.Fatalf("INV-5 LEAK: IPv6 address %q found in serialized status: %s", m, s)
	}
}

// requireStatusExcludes fails if any needle appears in the serialized status (a no-leak canary for known
// secret/config values or response bodies that INV-5 must discard).
func requireStatusExcludes(t *testing.T, p *pactov1alpha1.Pacto, needles ...string) {
	t.Helper()
	b, err := json.Marshal(p.Status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	for _, n := range needles {
		if strings.Contains(string(b), n) {
			t.Fatalf("no-leak canary: %q found in serialized status: %s", n, string(b))
		}
	}
}

// serviceMonitorCRDInstalled reports whether the monitoring.coreos.com ServiceMonitor CRD is registered in
// the running control plane. envtest loads only config/crd/bases, so this is expected to be false; the
// metrics ServiceMonitor-discovery row is guarded on it (see TestMetrics).
func serviceMonitorCRDInstalled(ctx context.Context) bool {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "monitoring.coreos.com", Version: "v1", Kind: "ServiceMonitorList",
	})
	err := k8sClient.List(ctx, list)
	// No-match => CRD absent. Any other outcome (nil, or a non-NoMatch error) treats the kind as known.
	return !meta.IsNoMatchError(err)
}

func requireCoverage(t *testing.T, p *pactov1alpha1.Pacto, evaluated, required int32) {
	t.Helper()
	c := p.Status.EvaluationCoverage
	if c == nil {
		t.Fatalf("expected evaluationCoverage {%d,%d}, got nil", evaluated, required)
	}
	if c.Evaluated != evaluated || c.Required != required {
		t.Fatalf("evaluationCoverage = {%d,%d}, want {%d,%d}", c.Evaluated, c.Required, evaluated, required)
	}
}

// --- dashboard feed ---------------------------------------------------------

// dashClient is a dashboard.K8sClient that serves a fixed set of Pacto CRs, so the REAL source_k8s
// mapping (serviceFromK8sStatus -> NormalizeContractStatus -> ComputeCompliance) runs over operator output.
type dashClient struct {
	listJSON []byte
	byName   map[string][]byte
}

func newDashClient(t *testing.T, pactos ...*pactov1alpha1.Pacto) dashClient {
	t.Helper()
	items := make([]json.RawMessage, 0, len(pactos))
	byName := make(map[string][]byte, len(pactos))
	for _, p := range pactos {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal pacto: %v", err)
		}
		items = append(items, b)
		byName[p.Name] = b
	}
	list, err := json.Marshal(struct {
		Items []json.RawMessage `json:"items"`
	}{Items: items})
	if err != nil {
		t.Fatalf("marshal list: %v", err)
	}
	return dashClient{listJSON: list, byName: byName}
}

func (d dashClient) Probe(context.Context) error { return nil }
func (d dashClient) DiscoverCRD(context.Context) (*dashboard.CRDDiscovery, error) {
	return &dashboard.CRDDiscovery{Found: true, Group: "pacto.trianalab.io", Version: "v1alpha1", ResourceName: "pactos"}, nil
}
func (d dashClient) ListJSON(context.Context, string, string) ([]byte, error) { return d.listJSON, nil }
func (d dashClient) GetJSON(_ context.Context, _, _, name string) ([]byte, error) {
	if b, ok := d.byName[name]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("not found: %s", name)
}
func (d dashClient) CountResources(context.Context, string, string) (int, error) {
	return len(d.byName), nil
}
