/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"cmp"
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	unversioned "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

// The egress gateways read: Istio's, in detect.GatewayNamespace, labelled istio=egressgateway. A pod with
// the label anywhere else is never read: a tenant can label its own pod.
const (
	gatewayLabel     = "istio=egressgateway"
	gatewayContainer = "istio-proxy"
)

// Bounds of one gateway read: the last 2000 lines and at most 1 MiB per pod, once a minute.
const (
	gatewayTailLines  = 2000
	gatewayLimitBytes = 1 << 20
	gatewayTTL        = time.Minute
)

// GatewayLogs reads the access log tail of the Istio egress gateways and keeps the parsed lines for a
// minute, so every reconciler's Observer shares one read. Build it once (the operator builds it only when a
// person set controller.egressGatewayLogs=on) and hand it to each Observer with WithGatewayLogs.
// ponytail: one global cache read at the first caller's window; schedule per gateway if gateways multiply.
type GatewayLogs struct {
	client corev1client.CoreV1Interface
	now    func() time.Time

	mu     sync.Mutex
	readAt time.Time
	last   gatewayRead
}

// gatewayRead is what one read of every gateway found: the access lines it parsed and the coverage
// reasons of what it could not read.
type gatewayRead struct {
	lines   []accessLine
	reasons []string
}

// NewGatewayLogs is the gateway log reader over c.
func NewGatewayLogs(c corev1client.CoreV1Interface) *GatewayLogs {
	return &GatewayLogs{client: c, now: time.Now}
}

// WithGatewayLogs names the calls the target's pods made through an Istio egress gateway from g's access
// lines; nil reads no gateway.
func (o *Observer) WithGatewayLogs(g *GatewayLogs) *Observer {
	o.gatewayLogs = g
	return o
}

// read is the last read of every gateway, made again once it is older than gatewayTTL, each pod's log read
// back window.
func (g *GatewayLogs) read(ctx context.Context, window time.Duration) gatewayRead {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if g.readAt.IsZero() || now.Sub(g.readAt) >= gatewayTTL {
		g.readAt, g.last = now, g.fetch(ctx, window)
	}
	return g.last
}

// fetch reads the access log tail of every trusted gateway pod. A list or log it cannot read gives
// gateway-log-unreadable, a pod with no line in the window gateway-access-log-off (Istio writes no access log
// by default) and a pod whose lines are mostly unparseable gateway-log-format.
func (g *GatewayLogs) fetch(ctx context.Context, window time.Duration) gatewayRead {
	pods, err := g.client.Pods(detect.GatewayNamespace).List(ctx, metav1.ListOptions{LabelSelector: gatewayLabel})
	if err != nil {
		return gatewayRead{reasons: []string{"gateway-log-unreadable"}}
	}
	var out gatewayRead
	since := max(int64(window.Seconds()), 1)
	tail, limit := int64(gatewayTailLines), int64(gatewayLimitBytes)
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Namespace != detect.GatewayNamespace || p.Status.Phase != corev1.PodRunning || !deploymentOwned(p) {
			continue
		}
		raw, err := g.client.Pods(detect.GatewayNamespace).GetLogs(p.Name, &corev1.PodLogOptions{
			Container: gatewayContainer, TailLines: &tail, LimitBytes: &limit, SinceSeconds: &since,
		}).DoRaw(ctx)
		if err != nil {
			out.reasons = append(out.reasons, "gateway-log-unreadable")
			continue
		}
		total, bad := 0, 0
		for line := range strings.Lines(string(raw)) {
			if strings.TrimSpace(line) == "" {
				continue
			}
			total++
			if l, ok := parseAccessLine(line); ok {
				out.lines = append(out.lines, l)
			} else {
				bad++
			}
		}
		switch {
		case total == 0:
			out.reasons = append(out.reasons, "gateway-access-log-off")
		case bad*2 > total:
			out.reasons = append(out.reasons, "gateway-log-format")
		}
	}
	return out
}

// deploymentOwned reports whether p's controller is a ReplicaSet of a Deployment, named
// <deployment>-<pod-template-hash>.
func deploymentOwned(p *corev1.Pod) bool {
	o := metav1.GetControllerOf(p)
	hash := p.Labels[appsv1.DefaultDeploymentUniqueLabelKey]
	return o != nil && o.Kind == "ReplicaSet" && hash != "" && strings.HasSuffix(o.Name, "-"+hash)
}

// accessLine is one gateway access log line: when the call started, the address it came from and the host
// and port it left for.
type accessLine struct {
	start      time.Time
	downstream netip.Addr
	host       string
	port       int32
}

// parseAccessLine reads one line of Istio's default access log format, as text or as its JSON encoding.
// A line with no start time, no downstream address or no host is unparseable.
func parseAccessLine(line string) (accessLine, bool) {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "{") {
		var j struct {
			Start      string `json:"start_time"`
			Downstream string `json:"downstream_remote_address"`
			Authority  string `json:"authority"`
			SNI        string `json:"requested_server_name"`
			Upstream   string `json:"upstream_host"`
		}
		if json.Unmarshal([]byte(line), &j) != nil {
			return accessLine{}, false
		}
		return newAccessLine(j.Start, j.Downstream, j.Authority, j.SNI, j.Upstream)
	}
	// [START_TIME] "METHOD PATH PROTOCOL" ... "AUTHORITY" "UPSTREAM_HOST" UPSTREAM_CLUSTER UPSTREAM_LOCAL
	// DOWNSTREAM_LOCAL DOWNSTREAM_REMOTE REQUESTED_SERVER_NAME ROUTE_NAME: read from the end, so a release
	// that adds a field in the middle still parses.
	f := accessFields(line)
	if !strings.HasPrefix(line, "[") || len(f) < 21 {
		return accessLine{}, false
	}
	n := len(f)
	return newAccessLine(f[0], f[n-3], f[n-8], f[n-2], f[n-7])
}

// accessFields splits a text access line into its fields: a [bracketed] or "quoted" field is one field.
func accessFields(line string) []string {
	var out []string
	for line = strings.TrimLeft(line, " "); line != ""; line = strings.TrimLeft(line, " ") {
		field, ok := "", true
		switch line[0] {
		case '[':
			field, line, ok = strings.Cut(line[1:], "]")
		case '"':
			field, line, ok = strings.Cut(line[1:], `"`)
		default:
			field, line, _ = strings.Cut(line, " ")
		}
		if !ok {
			return nil
		}
		out = append(out, field)
	}
	return out
}

// newAccessLine builds a line from its fields: the host is the TLS server name when there is one, else the
// authority without its port; the port is the upstream host's, else the authority's.
func newAccessLine(start, downstream, authority, sni, upstream string) (accessLine, bool) {
	t, err := time.Parse(time.RFC3339Nano, start)
	if err != nil {
		return accessLine{}, false
	}
	ds, err := netip.ParseAddrPort(downstream)
	if err != nil {
		return accessLine{}, false
	}
	authHost, authPort := authority, ""
	if h, p, err := net.SplitHostPort(authority); err == nil {
		authHost, authPort = h, p
	}
	host := strings.ToLower(cmp.Or(dash(sni), dash(authHost)))
	if host == "" {
		return accessLine{}, false
	}
	l := accessLine{start: t, downstream: ds.Addr().Unmap(), host: host}
	if up, err := netip.ParseAddrPort(upstream); err == nil {
		l.port = int32(up.Port())
	} else if p, err := netip.ParseAddrPort("0.0.0.0:" + authPort); err == nil {
		l.port = int32(p.Port())
	}
	return l, true
}

// dash is s, empty when the access log wrote "-" for no value.
func dash(s string) string {
	if s == "-" {
		return ""
	}
	return s
}

// gatewayEgress are the externals the target's pods called through a gateway: lines from a pod's current
// address that started after the pod did (a pod address is reused; a line from its previous owner is
// dropped). A pod with no start time vouches for no line. unclassified is classifyEgress's.
func gatewayEgress(lines []accessLine, pods []corev1.Pod) ([]unversioned.EgressStatus, bool) {
	started := map[netip.Addr]time.Time{}
	for _, p := range pods {
		if p.Status.StartTime == nil {
			continue
		}
		for _, ip := range append([]corev1.PodIP{{IP: p.Status.PodIP}}, p.Status.PodIPs...) {
			if a, err := netip.ParseAddr(ip.IP); err == nil {
				started[a.Unmap()] = p.Status.StartTime.Time
			}
		}
	}
	var named []runtimefacts.Egress
	for _, l := range lines {
		if s, ok := started[l.downstream]; ok && l.start.After(s) {
			named = append(named, runtimefacts.Egress{Host: l.host, Port: l.port, LastSuccessAt: l.start})
		}
	}
	slices.SortFunc(named, func(a, b runtimefacts.Egress) int {
		return cmp.Or(cmp.Compare(a.Host, b.Host), cmp.Compare(a.Port, b.Port))
	})
	return classifyEgress(named, unversioned.EgressSourceGatewayLog)
}
