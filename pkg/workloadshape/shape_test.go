package workloadshape

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// LeakStrings are the strings of internal/testutil/testdata/leak-deployment.json (the
// Decision 9 object) that must never be read.
var leakStrings = []string{"DB_PASSWORD", "hunter2", "API_KEY", "billing-creds", "token", "billing-config", "s3cr3t-hdr", "s3cr3t-query", "s3cr3t-exec", "s3cr3t-arg", "last-applied"}

func container(name, image string, extra map[string]any) map[string]any {
	c := map[string]any{"name": name, "image": image}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

func podSpec(containers ...any) map[string]any {
	return map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": containers}}}}
}

func TestReadWorkloadShapeDeployment(t *testing.T) {
	obj := podSpec(
		container("sidecar", "proxy:1", map[string]any{"ports": []any{map[string]any{"containerPort": int64(15001)}}}),
		container("cart", "cart:2", map[string]any{
			"ports":          []any{map[string]any{"containerPort": 8080, "name": "http", "protocol": "TCP"}, map[string]any{"name": "no-number"}},
			"readinessProbe": map[string]any{"httpGet": map[string]any{"path": "/ready", "port": "http"}},
			"livenessProbe":  map[string]any{"httpGet": map[string]any{"path": "/live", "port": 8081}},
		}),
	)
	got := ReadWorkloadShape("Deployment", "cart", obj)
	at := "spec.template.spec.containers[name="
	want := WorkloadShape{
		Image: "cart:2", ImageFrom: at + "cart].image",
		Ports: []Port{
			{Number: 15001, From: at + "sidecar].ports[0].containerPort"},
			{Number: 8080, Name: "http", Protocol: "TCP", From: at + "cart].ports[0].containerPort"},
		},
		Probes: []Probe{
			{Kind: "readiness", Path: "/ready", Port: "http", From: at + "cart].readinessProbe"},
			{Kind: "liveness", Path: "/live", Port: "8081", From: at + "cart].livenessProbe"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("shape = %+v\nwant    %+v", got, want)
	}
}

func TestReadWorkloadShapeCronJob(t *testing.T) {
	obj := map[string]any{"spec": map[string]any{"jobTemplate": podSpec(
		container("nightly", "batch:1", map[string]any{"startupProbe": map[string]any{"httpGet": map[string]any{"path": "/up"}}}),
	)}}
	got := ReadWorkloadShape("CronJob", "nightly", obj)
	if got.Image != "batch:1" || got.ImageFrom != "spec.jobTemplate.spec.template.spec.containers[name=nightly].image" {
		t.Errorf("image = %q from %q", got.Image, got.ImageFrom)
	}
	if len(got.Probes) != 1 || got.Probes[0].Kind != "startup" || got.Probes[0].Path != "/up" || got.Probes[0].Port != "" {
		t.Errorf("probes = %+v", got.Probes)
	}
}

func TestReadWorkloadShapeFirstContainerFallback(t *testing.T) {
	got := ReadWorkloadShape("StatefulSet", "db", podSpec(container("postgres", "postgres:16", nil), container("exporter", "exp:1", nil)))
	if got.Image != "postgres:16" {
		t.Errorf("image = %q, want the first container's", got.Image)
	}
	if empty := ReadWorkloadShape("DaemonSet", "x", map[string]any{}); !reflect.DeepEqual(empty, WorkloadShape{}) {
		t.Errorf("no pod template = %+v, want empty", empty)
	}
	noImage := ReadWorkloadShape("Deployment", "x", podSpec(map[string]any{"name": "x"}))
	if noImage.Image != "" || noImage.ImageFrom != "" {
		t.Errorf("no image = %+v", noImage)
	}
}

func TestReadWorkloadShapeJSONNumbers(t *testing.T) {
	var obj map[string]any
	data := `{"spec":{"template":{"spec":{"containers":[{"name":"api","image":"api:1","ports":[{"containerPort":9090}],"readinessProbe":{"httpGet":{"path":"/r","port":9090}},"livenessProbe":{"httpGet":{"port":true}}}]}}}}`
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		t.Fatal(err)
	}
	got := ReadWorkloadShape("Deployment", "api", obj)
	if len(got.Ports) != 1 || got.Ports[0].Number != 9090 {
		t.Errorf("ports = %+v", got.Ports)
	}
	if len(got.Probes) != 2 || got.Probes[0].Port != "9090" || got.Probes[1].Port != "" {
		t.Errorf("probes = %+v", got.Probes)
	}
}

// LeakObject is the Decision 9 object.
func leakObject(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile("../../internal/testutil/testdata/leak-deployment.json")
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatal(err)
	}
	return obj
}

func TestReadWorkloadShapeNeverReadsEnv(t *testing.T) {
	shape := ReadWorkloadShape("Deployment", "billing", leakObject(t))
	if shape.Image != "registry.example/billing:1.4.2" || len(shape.Ports) != 1 || len(shape.Probes) != 2 {
		t.Fatalf("shape = %+v, want the image, one port and two probes", shape)
	}
	dump := fmt.Sprintf("%#v", shape)
	for _, s := range leakStrings {
		if strings.Contains(dump, s) {
			t.Errorf("shape holds %q: %s", s, dump)
		}
	}
}

func TestReadWorkloadShapeStripsProbeQuery(t *testing.T) {
	obj := podSpec(container("a", "a:1", map[string]any{"readinessProbe": map[string]any{"httpGet": map[string]any{"path": "/healthz?token=s3cr3t-query#x"}}}))
	if got := ReadWorkloadShape("Deployment", "a", obj).Probes[0].Path; got != "/healthz" {
		t.Errorf("path = %q, want /healthz", got)
	}
	obj = podSpec(container("a", "a:1", map[string]any{"readinessProbe": map[string]any{"httpGet": map[string]any{"path": "/h#frag"}}}))
	if got := ReadWorkloadShape("Deployment", "a", obj).Probes[0].Path; got != "/h" {
		t.Errorf("path = %q, want /h", got)
	}
}
