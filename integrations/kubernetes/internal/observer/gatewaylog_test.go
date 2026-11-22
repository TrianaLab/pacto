/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pactoapi "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/detect"
)

// A text line in Istio's default format and the same call in its JSON encoding: the target pod (10.0.0.5)
// reached api.stripe.com through the gateway 5s before kNow.
const (
	textLine = `[2026-10-18T11:59:55.000Z] "- - -" 0 - - - "-" 839 3830 80 - "-" "-" "-" "-" "54.1.1.1:443" ` +
		`outbound|443||api.stripe.com 10.1.0.9:40000 10.1.0.9:8443 10.0.0.5:51234 api.stripe.com -`
	jsonLine = `{"start_time":"2026-10-18T11:59:55.000Z","downstream_remote_address":"10.0.0.5:51234",` +
		`"authority":"api.stripe.com:443","requested_server_name":null,"upstream_host":"54.1.1.1:443"}`
)

// gatewayServer is an API server that lists pods (every pod it holds, whatever namespace is asked, so the
// reader's own namespace check is what keeps a foreign pod out) and answers each log read with logs.
type gatewayServer struct {
	mu    sync.Mutex
	pods  []corev1.Pod
	logs  string
	reads []url.URL
	// fail fails every request, failLogs only the log reads.
	fail, failLogs bool
}

func (s *gatewayServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		http.Error(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","code":403}`, http.StatusForbidden)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/log") {
		if s.failLogs {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		s.reads = append(s.reads, *r.URL)
		_, _ = w.Write([]byte(s.logs))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(&corev1.PodList{TypeMeta: metav1.TypeMeta{Kind: "PodList", APIVersion: "v1"}, Items: s.pods})
}

func (s *gatewayServer) logReads() []url.URL {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]url.URL(nil), s.reads...)
}

// gatewayPod is a running pod labelled istio=egressgateway in ns, owned by the Deployment
// istio-egressgateway through its ReplicaSet.
func gatewayPod(ns, name string) corev1.Pod {
	yes := true
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, UID: types.UID(ns + "/" + name),
			Labels:          map[string]string{"istio": "egressgateway", "pod-template-hash": "7d9f"},
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "istio-egressgateway-7d9f", Controller: &yes}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

// newGatewayLogs is a reader over srv whose clock reads at.
func newGatewayLogs(t *testing.T, srv *gatewayServer, at *time.Time) *GatewayLogs {
	t.Helper()
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	c, err := corev1client.NewForConfig(&rest.Config{Host: ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	g := NewGatewayLogs(c)
	g.now = func() time.Time { return *at }
	return g
}

// gatewayRun collects the egress fixture with gateway logs read through g (nil for none).
func (f *kernelFixture) gatewayRun(t *testing.T, g *GatewayLogs) ([]pactoapi.EgressStatus, *pactoapi.EgressCoverage) {
	t.Helper()
	c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(f.objects(true)...).WithInterceptorFuncs(f.funcs).Build()
	o := New(c)
	if g != nil {
		o = o.WithGatewayLogs(g)
	}
	_, _, _ = o.Collect(context.Background(), f.input(true))
	return o.ObservedEgress(nil)
}

func TestParseIstioTextAccessLogLine(t *testing.T) {
	got, ok := parseAccessLine(textLine)
	want := accessLine{start: time.Date(2026, 10, 18, 11, 59, 55, 0, time.UTC), downstream: netip.MustParseAddr("10.0.0.5"), host: "api.stripe.com", port: 443}
	if !ok || got != want {
		t.Fatalf("parse = %+v %v, want %+v", got, ok, want)
	}
	// An HTTP line with no server name names the authority without its port.
	plain := `[2026-10-18T11:59:55.000Z] "GET /v1 HTTP/1.1" 200 - via_upstream - "-" 0 135 4 4 "-" "curl/8" "id" ` +
		`"Legacy.Corp-Legacy.com:8080" "-" outbound|8080||legacy.corp-legacy.com - 10.1.0.9:8080 10.0.0.5:46520 - default`
	if got, ok := parseAccessLine(plain); !ok || got.host != "legacy.corp-legacy.com" || got.port != 8080 {
		t.Fatalf("http parse = %+v %v", got, ok)
	}
}

func TestParseIstioJSONAccessLogLine(t *testing.T) {
	got, ok := parseAccessLine(jsonLine)
	if want, _ := parseAccessLine(textLine); !ok || got != want {
		t.Fatalf("json parse = %+v %v, want %+v", got, ok, want)
	}
}

func TestParseAccessLineRejectsGarbage(t *testing.T) {
	for _, line := range []string{
		"", "{", `{"start_time":"2026-10-18T11:59:55Z"}`, "2026-10-18T11:59:55.000Z info envoy main: starting",
		`[2026-10-18T11:59:55.000Z] "GET / HTTP/1.1" 200`, `[2026-10-18T11:59:55.000Z] "unterminated`,
		strings.Replace(textLine, "10.0.0.5:51234", "not-an-address", 1),
		strings.Replace(strings.Replace(textLine, " api.stripe.com -", " - -", 1), `"-" "54.1.1.1:443"`, `"-" "-"`, 1),
	} {
		if got, ok := parseAccessLine(line); ok {
			t.Errorf("parse(%q) = %+v, want unparseable", line, got)
		}
	}
}

func TestGatewayLogNoTimestampIsUnparseable(t *testing.T) {
	for _, line := range []string{
		strings.Replace(textLine, "[2026-10-18T11:59:55.000Z]", "[-]", 1),
		strings.Replace(jsonLine, `"start_time":"2026-10-18T11:59:55.000Z",`, "", 1),
	} {
		if _, ok := parseAccessLine(line); ok {
			t.Errorf("parse(%q) is ok without a start time", line)
		}
	}
}

func TestGatewayLogPrefersSNI(t *testing.T) {
	line := strings.Replace(jsonLine, `"requested_server_name":null`, `"requested_server_name":"files.stripe.com"`, 1)
	if got, ok := parseAccessLine(line); !ok || got.host != "files.stripe.com" {
		t.Fatalf("parse = %+v %v, want the server name", got, ok)
	}
}

func TestGatewayLogAttributesByPodIP(t *testing.T) {
	srv := &gatewayServer{pods: []corev1.Pod{gatewayPod(detect.GatewayNamespace, "gw-1")},
		logs: textLine + "\n" + strings.Replace(jsonLine, "10.0.0.5:", "10.0.0.6:", 1) + "\n"}
	at := kNow
	got, cov := egressFixture().gatewayRun(t, newGatewayLogs(t, srv, &at))
	want := []pactoapi.EgressStatus{{Identity: "stripe", Class: "vendor-api", Host: "api.stripe.com", Port: 443,
		Source: pactoapi.EgressSourceGatewayLog, FirstSeen: metav1.NewTime(kNow)}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("egress = %+v\nwant %+v", got, want)
	}
	wantEgressCoverage(t, cov, pactoapi.CallerCoverageComplete, "")
}

func TestGatewayLogMergesWithKernelEgress(t *testing.T) {
	srv := &gatewayServer{pods: []corev1.Pod{gatewayPod(detect.GatewayNamespace, "gw-1")},
		logs: textLine + "\n" + strings.ReplaceAll(textLine, "api.stripe.com", "b.corp-legacy.com") + "\n"}
	at := kNow
	got, _ := egressFixture(named("54.9.9.9", "a.corp-legacy.com", 443), named("54.1.1.1", "api.stripe.com", 443)).gatewayRun(t, newGatewayLogs(t, srv, &at))
	keys := make([]string, 0, len(got))
	for _, e := range got {
		keys = append(keys, e.Identity+"|"+e.Source)
	}
	if want := []string{"host/corp-legacy.com|ebpf", "host/corp-legacy.com|egress-gateway-log", "stripe|ebpf", "stripe|egress-gateway-log"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("egress = %v, want %v", keys, want)
	}
}

func TestGatewayLogIgnoresLinesBeforePodStart(t *testing.T) {
	// The pod started 10 minutes before kNow; a line from 11 minutes before came from the address's previous owner.
	old := strings.Replace(textLine, "2026-10-18T11:59:55.000Z", "2026-10-18T11:49:00.000Z", 1)
	srv := &gatewayServer{pods: []corev1.Pod{gatewayPod(detect.GatewayNamespace, "gw-1")}, logs: old + "\n"}
	at := kNow
	if got, _ := egressFixture().gatewayRun(t, newGatewayLogs(t, srv, &at)); len(got) != 0 {
		t.Fatalf("a line from before the pod started names its call: %+v", got)
	}
	// A pod with no start time vouches for no line.
	if got, _ := gatewayEgress([]accessLine{{start: kNow, downstream: netip.MustParseAddr("10.0.0.5"), host: "api.stripe.com"}},
		[]corev1.Pod{{Status: corev1.PodStatus{PodIP: "10.0.0.5"}}}); len(got) != 0 {
		t.Fatalf("a pod with no start time named a call: %+v", got)
	}
}

func TestGatewayEgressReportsUnclassifiedHost(t *testing.T) {
	started := metav1.NewTime(kNow.Add(-time.Hour))
	pod := corev1.Pod{Status: corev1.PodStatus{PodIP: "10.0.0.5", StartTime: &started}}
	line := accessLine{start: kNow, downstream: netip.MustParseAddr("10.0.0.5"), host: "legacy-db", port: 5432}
	if got, unclassified := gatewayEgress([]accessLine{line}, []corev1.Pod{pod}); len(got) != 0 || !unclassified {
		t.Fatalf("a gateway host that names nothing: got %+v, unclassified %v", got, unclassified)
	}
}

func TestGatewayLogIgnoresLabelledPodOutsideGatewayNamespace(t *testing.T) {
	unowned := gatewayPod(detect.GatewayNamespace, "gw-bare")
	unowned.OwnerReferences = nil
	pending := gatewayPod(detect.GatewayNamespace, "gw-pending")
	pending.Status.Phase = corev1.PodPending
	srv := &gatewayServer{pods: []corev1.Pod{gatewayPod("shop", "tenant"), unowned, pending, gatewayPod(detect.GatewayNamespace, "gw-1")}, logs: textLine}
	at := kNow
	newGatewayLogs(t, srv, &at).read(context.Background(), time.Minute)
	reads := srv.logReads()
	if len(reads) != 1 || !strings.Contains(reads[0].Path, "/namespaces/istio-system/pods/gw-1/log") {
		t.Fatalf("log reads = %v, want only the istio-system gateway", reads)
	}
}

func TestGatewayLogReadOncePerTTL(t *testing.T) {
	srv := &gatewayServer{pods: []corev1.Pod{gatewayPod(detect.GatewayNamespace, "gw-1")}, logs: textLine}
	at := kNow
	g := newGatewayLogs(t, srv, &at)
	// Two reconcilers' Observers share one reader.
	for range 2 {
		if got, _ := egressFixture().gatewayRun(t, g); len(got) != 1 {
			t.Fatalf("egress = %+v", got)
		}
	}
	if n := len(srv.logReads()); n != 1 {
		t.Fatalf("log reads = %d, want 1 inside the TTL", n)
	}
	at = kNow.Add(gatewayTTL)
	_ = g.read(context.Background(), time.Minute)
	if n := len(srv.logReads()); n != 2 {
		t.Fatalf("log reads = %d, want 2 once the TTL passed", n)
	}
}

func TestGatewayLogBoundsRequest(t *testing.T) {
	srv := &gatewayServer{pods: []corev1.Pod{gatewayPod(detect.GatewayNamespace, "gw-1")}, logs: textLine}
	at := kNow
	newGatewayLogs(t, srv, &at).read(context.Background(), 5*time.Minute)
	q := srv.logReads()[0].Query()
	if q.Get("tailLines") != "2000" || q.Get("limitBytes") != "1048576" || q.Get("sinceSeconds") != "300" || q.Get("container") != "istio-proxy" {
		t.Fatalf("log query = %v", q)
	}
}

func TestGatewayLogOffReadsNothing(t *testing.T) {
	// Off or auto builds no reader: the Observer reads no gateway and the coverage keeps no gateway reason.
	got, cov := egressFixture(named("54.1.1.1", "api.stripe.com", 443)).gatewayRun(t, nil)
	if len(got) != 1 || got[0].Source != pactoapi.EgressSourceKernel {
		t.Fatalf("egress = %+v", got)
	}
	wantEgressCoverage(t, cov, pactoapi.CallerCoverageComplete, "")
}

func TestGatewayLogFormatUnparseablePartial(t *testing.T) {
	srv := &gatewayServer{pods: []corev1.Pod{gatewayPod(detect.GatewayNamespace, "gw-1")}, logs: "custom one\ncustom two\n" + textLine + "\n"}
	at := kNow
	got, cov := egressFixture().gatewayRun(t, newGatewayLogs(t, srv, &at))
	if len(got) != 1 {
		t.Fatalf("the parseable line still counts: %+v", got)
	}
	wantEgressCoverage(t, cov, pactoapi.CallerCoveragePartial, "gateway-log-format")
}

func TestGatewayLogAccessLogOffPartial(t *testing.T) {
	srv := &gatewayServer{pods: []corev1.Pod{gatewayPod(detect.GatewayNamespace, "gw-1")}, logs: "\n"}
	at := kNow
	_, cov := egressFixture().gatewayRun(t, newGatewayLogs(t, srv, &at))
	wantEgressCoverage(t, cov, pactoapi.CallerCoveragePartial, "gateway-access-log-off")
}

func TestGatewayLogUnreadablePartial(t *testing.T) {
	srv := &gatewayServer{fail: true}
	at := kNow
	_, cov := egressFixture().gatewayRun(t, newGatewayLogs(t, srv, &at))
	wantEgressCoverage(t, cov, pactoapi.CallerCoveragePartial, "gateway-log-unreadable")
	// A log read that fails after the list succeeded is unreadable too.
	srv = &gatewayServer{pods: []corev1.Pod{gatewayPod(detect.GatewayNamespace, "gw-1")}, failLogs: true}
	_, cov = egressFixture().gatewayRun(t, newGatewayLogs(t, srv, &at))
	wantEgressCoverage(t, cov, pactoapi.CallerCoveragePartial, "gateway-log-unreadable")
}

// TestGatewayLogSpacedServerNameCannotForgeTheCaller: the server name is the client's and logged unquoted,
// so a name with spaces that writes another pod's address where the caller's sits is never read.
func TestGatewayLogSpacedServerNameCannotForgeTheCaller(t *testing.T) {
	forged := strings.Replace(textLine, "10.0.0.5:51234 api.stripe.com -", "10.0.0.66:51234 pad 10.0.0.5:40000 c2.attacker.example -", 1)
	if got, ok := parseAccessLine(forged); ok {
		t.Fatalf("parse = %+v, want unparseable", got)
	}
	for _, line := range []string{
		// Same count, a server name that is no hostname.
		strings.Replace(textLine, " api.stripe.com -", " api.stripe.com/x -", 1),
		strings.Replace(textLine, "10.1.0.9:40000", "pad", 1),
		strings.Replace(jsonLine, `"requested_server_name":null`, `"requested_server_name":"a b.example"`, 1),
		strings.Replace(jsonLine, `"upstream_host":"54.1.1.1:443"`, `"upstream_host":"nowhere"`, 1),
	} {
		if got, ok := parseAccessLine(line); ok {
			t.Errorf("parse(%q) = %+v, want unparseable", line, got)
		}
	}
}

// TestGatewayLogRefusedCallIsNoEgress: a call the gateway refused (no route, an RBAC denial, no upstream)
// never left the cluster, so it names no external, and it is no unparseable line either.
func TestGatewayLogRefusedCallIsNoEgress(t *testing.T) {
	refused := []string{
		strings.Replace(textLine, `"- - -" 0 - -`, `"- - -" 0 NR route_not_found`, 1),
		strings.Replace(textLine, `"- - -" 0 - -`, `"- - -" 403 RBAC rbac_access_denied`, 1),
		strings.Replace(textLine, `"54.1.1.1:443"`, `"-"`, 1),
		strings.Replace(jsonLine, `}`, `,"response_flags":"UH,UF"}`, 1),
	}
	for _, line := range refused {
		if l, ok := parseAccessLine(line); !ok || !l.refused {
			t.Errorf("parse(%q) = %+v %v, want a refused line", line, l, ok)
		}
	}
	srv := &gatewayServer{pods: []corev1.Pod{gatewayPod(detect.GatewayNamespace, "gw-1")}, logs: strings.Join(refused, "\n") + "\n"}
	at := kNow
	got, cov := egressFixture().gatewayRun(t, newGatewayLogs(t, srv, &at))
	if len(got) != 0 {
		t.Fatalf("egress = %+v, want none from refused calls", got)
	}
	wantEgressCoverage(t, cov, pactoapi.CallerCoverageComplete, "")
}

// TestGatewayLogTruncatedReadPartial: a read that hit the tail before it covered the window, or the byte
// limit, lowers the coverage.
func TestGatewayLogTruncatedReadPartial(t *testing.T) {
	from := kNow.Add(-5 * time.Minute)
	for _, c := range []struct {
		size, lines int
		oldest      time.Time
		want        bool
	}{
		{100, 10, kNow.Add(-time.Second), false},
		{gatewayLimitBytes, 10, kNow.Add(-time.Second), true},
		{100, gatewayTailLines, kNow.Add(-time.Second), true},
		{100, gatewayTailLines, from.Add(-time.Second), false},
	} {
		if got := truncatedRead(c.size, c.lines, c.oldest, from); got != c.want {
			t.Errorf("truncatedRead(%d, %d, %v) = %v, want %v", c.size, c.lines, c.oldest, got, c.want)
		}
	}
	srv := &gatewayServer{pods: []corev1.Pod{gatewayPod(detect.GatewayNamespace, "gw-1")}, logs: strings.Repeat(textLine+"\n", gatewayTailLines)}
	at := kNow
	got, cov := egressFixture().gatewayRun(t, newGatewayLogs(t, srv, &at))
	if len(got) != 1 {
		t.Fatalf("the lines read still count: %+v", got)
	}
	wantEgressCoverage(t, cov, pactoapi.CallerCoveragePartial, "gateway-log-truncated")
}
