package discover

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// writeTree writes files under dir and returns dir.
func writeTree(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const appManifest = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
  labels: {team: payments}
spec:
  template:
    spec:
      containers:
        - name: sidecar
          image: envoy:1
          ports: [{containerPort: 9901}]
        - name: app
          image: ghcr.io/acme/app:1.2.0
          ports: [{containerPort: 8080}]
          readinessProbe: {httpGet: {path: /healthz}}
          envFrom:
            - {configMapRef: {name: cfg}, prefix: CFG_}
            - {configMapRef: {name: gone}}
            - secretRef: {name: creds}
            - {}
          env:
            - {name: ORDERS_URL, value: "http://orders:8080/x"}
            - {name: DB_HOST, value: "db.prod.svc.cluster.local:5432"}
            - {name: STRIPE, value: "https://api.stripe.com"}
            - {name: SELF, value: "http://app"}
            - {name: LOCAL, value: "localhost:1"}
            - {name: OWN, value: "cfg:80"}
            - {name: AGAIN, value: "http://orders:9090"}
            - {name: BARE, value: "orders"}
            - {name: ODD, value: "not a url!!"}
            - {name: DB_PASSWORD, value: hunter2}
            - name: LEVEL
              valueFrom: {configMapKeyRef: {name: cfg, key: level}}
            - name: MISSING
              valueFrom: {configMapKeyRef: {name: gone, key: k}}
            - name: TOKEN
              valueFrom: {secretKeyRef: {name: creds, key: stripe-token}}
            - {name: BARE, value: "8080"}
---
apiVersion: v1
kind: ConfigMap
metadata: {name: cfg}
data: {level: info, port: 8080, empty: null, API_KEY: s3cret}
---
apiVersion: v1
kind: Service
metadata: {name: app}
---
apiVersion: batch/v1
kind: CronJob
metadata: {name: nightly}
spec:
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - {name: nightly, image: nightly, livenessProbe: {tcpSocket: {port: 1}}}
---
apiVersion: batch/v1
kind: Job
metadata: {name: once}
`

// folder is a service folder that exercises every reader of Dir.
func folder(t *testing.T) string {
	return writeTree(t, filepath.Join(t.TempDir(), "app"), map[string]string{
		"Dockerfile":                 "FROM scratch\n",
		"build/Dockerfile.dev":       "FROM scratch\n",
		"build/api.Dockerfile":       "FROM scratch\n",
		"api/swagger.json":           "{}",
		"openapi.yaml":               "openapi: 3.1.0\n",
		"Chart.yaml":                 "name: app\n",
		"templates/x.yaml":           "apiVersion: v1\nkind: Service\nmetadata: {name: tmpl}\n",
		"chart/Chart.yaml":           "name: sub\n",
		"chart/values.yaml":          "hosts:\n  - &erp https://api.stripe.com\n  - *erp\nn: 3\n",
		"chart2/Chart.yaml":          "name: sub2\n",
		"chart2/values.yaml":         "url: https://api.stripe.com\n",
		"chart3/Chart.yaml":          "name: sub3\n",
		"chart4/Chart.yaml":          "name: sub4\n",
		"chart4/values.yaml":         ": [\n",
		".git/x.yaml":                "apiVersion: v1\nkind: Service\nmetadata: {name: git}\n",
		"node_modules/x.yaml":        "apiVersion: v1\nkind: Service\nmetadata: {name: nm}\n",
		"delivery/x.yaml":            "apiVersion: v1\nkind: Service\nmetadata: {name: dl}\n",
		"ignored/x.yaml":             "apiVersion: v1\nkind: Service\nmetadata: {name: ign}\n",
		"skip.yaml":                  "apiVersion: v1\nkind: Service\nmetadata: {name: skip}\n",
		".pactoignore":               "ignored/\nskip.yaml\nchart2/values.yaml\n",
		"notes.txt":                  "hello",
		"bad.yaml":                   ": [\n",
		"catalog-info.yaml":          "apiVersion: backstage.io/v1alpha1\nkind: Component\n",
		"plain.yml":                  "a: 1\n",
		"k8s/app.yaml":               appManifest,
		"k8s/kustomization.yaml":     "resources: [app.yaml]\n",
		"k8s/secret.yaml":            "apiVersion: v1\nkind: Secret\nmetadata: {name: creds}\ndata: {a: b}\n",
		"k8s/string.yaml":            "apiVersion: v1\nkind: Secret\nmetadata: {name: s2}\nstringData: {a: b}\n",
		"k8s/empty-secret.yaml":      "apiVersion: v1\nkind: Secret\nmetadata: {name: s3}\n",
		"k8s/gen/kustomization.yaml": "secretGenerator: [{name: x}]\n",
		"k8s/flux.yaml":              "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nmetadata: {name: flux}\n",
	})
}

func TestDirReadsTheFolder(t *testing.T) {
	sc, err := Dir(folder(t))
	if err != nil {
		t.Fatal(err)
	}
	if sc.Dockerfile != "Dockerfile" || !slices.Equal(sc.Dockerfiles, []string{"Dockerfile", "build/Dockerfile.dev", "build/api.Dockerfile"}) {
		t.Errorf("dockerfiles: %q %q", sc.Dockerfile, sc.Dockerfiles)
	}
	if sc.OpenAPI != "openapi.yaml" {
		t.Errorf("openapi: %q", sc.OpenAPI)
	}
	if !slices.Equal(sc.Charts, []string{".", "chart", "chart2", "chart3", "chart4"}) {
		t.Errorf("charts: %q", sc.Charts)
	}
}

func TestDirSortsTheFiles(t *testing.T) {
	sc, err := Dir(folder(t))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sc.Manifests, []string{"k8s/app.yaml", "k8s/empty-secret.yaml", "k8s/flux.yaml", "k8s/kustomization.yaml"}) {
		t.Errorf("manifests: %q", sc.Manifests)
	}
	if !slices.Equal(sc.Kustomize, []string{"k8s/flux.yaml", "k8s/kustomization.yaml"}) {
		t.Errorf("kustomize: %q", sc.Kustomize)
	}
	if !slices.Equal(sc.Secrets, []string{"k8s/gen/kustomization.yaml", "k8s/secret.yaml", "k8s/string.yaml"}) {
		t.Errorf("secrets: %q", sc.Secrets)
	}
}

func TestDirReadsTheWorkload(t *testing.T) {
	sc, err := Dir(folder(t))
	if err != nil {
		t.Fatal(err)
	}
	if !sc.Services["app"] || sc.Services["cfg"] {
		t.Errorf("services: %v", sc.Services)
	}
	w := sc.Workload
	if w == nil || w.Name != "app" || w.Kind != "Deployment" || w.Team != "payments" || w.Image != "ghcr.io/acme/app:1.2.0" ||
		!slices.Equal(w.Ports, []int{9901, 8080}) || *w.Probe != (Probe{Kind: "readiness", Path: "/healthz"}) {
		t.Errorf("workload: %+v", w)
	}
}

func TestDirReadsConfigMapsAndJobs(t *testing.T) {
	sc, err := Dir(folder(t))
	if err != nil {
		t.Fatal(err)
	}
	if sc.ConfigMaps["cfg"].data["empty"] != "" || sc.ConfigMaps["cfg"].data["port"] != "8080" {
		t.Errorf("configmap: %+v", sc.ConfigMaps["cfg"])
	}
	if len(sc.workloads) != 3 || sc.workloads[1].Probe.Path != "" || sc.workloads[2].Probe != nil {
		t.Errorf("workloads: %+v", sc.workloads)
	}
}

func TestDirResolvesTheEnvironment(t *testing.T) {
	sc, err := Dir(folder(t))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range sc.Env {
		names = append(names, e.Name)
	}
	want := []string{"CFG_API_KEY", "CFG_level", "CFG_port", "ORDERS_URL", "DB_HOST", "STRIPE", "SELF", "LOCAL", "OWN", "AGAIN", "BARE", "ODD", "DB_PASSWORD", "LEVEL", "MISSING", "TOKEN"}
	if !slices.Equal(names, want) {
		t.Errorf("env: %q", names)
	}
	for _, e := range sc.Env {
		if e.Name == "LEVEL" && (e.Value != "info" || e.File != "k8s/app.yaml") || e.Name == "BARE" && e.Value != "8080" {
			t.Errorf("%s: %+v", e.Name, e)
		}
	}
	if len(sc.EnvNotes) != 3 || !strings.Contains(sc.EnvNotes[0], "the ConfigMap gone") || !strings.Contains(sc.EnvNotes[1], "from the Secret creds") {
		t.Errorf("notes: %q", sc.EnvNotes)
	}
}

func TestDirDependencies(t *testing.T) {
	sc, err := Dir(folder(t))
	if err != nil {
		t.Fatal(err)
	}
	got := sc.Dependencies("app")
	want := []Finding{{Name: "orders", Where: "k8s/app.yaml, env ORDERS_URL"}, {Name: "db", Where: "k8s/app.yaml, env DB_HOST"}}
	if !slices.Equal(got, want) {
		t.Errorf("dependencies: %+v", got)
	}
	if d := EnvDependencies([]EnvVar{{Name: "U", Value: "http://pay:80"}, {Name: "V", Value: "http://own:80"}}, map[string]bool{"own": true}, "self"); !slices.Equal(d, []Finding{{Name: "pay", Where: ", env U"}}) {
		t.Errorf("env dependencies: %+v", d)
	}
}

func TestDirLiteralsAndProperties(t *testing.T) {
	sc, err := Dir(folder(t))
	if err != nil {
		t.Fatal(err)
	}
	lit := sc.Literals()
	if !slices.Equal(lit["k8s/app.yaml"], []string{"DB_PASSWORD", "CFG_API_KEY"}) && !slices.Equal(lit["k8s/app.yaml"], []string{"CFG_API_KEY", "DB_PASSWORD"}) {
		t.Errorf("literals: %v", lit)
	}
	sc.Workload.Env = append(sc.Workload.Env, EnvVar{File: "k8s/app.yaml", Name: "PW_PASSWORD", From: "configmap", Ref: "pw", Key: "x"})
	sc.ConfigMaps["pw"] = ConfigMap{file: "k8s/pw.yaml", data: map[string]string{"x": "hunter2"}}
	if lit := sc.Literals(); !slices.Equal(lit["k8s/pw.yaml"], []string{"PW_PASSWORD"}) {
		t.Errorf("configmap key literal: %v", lit)
	}
	for _, tc := range []struct {
		e    EnvVar
		want string
	}{
		{EnvVar{Name: "T", Secret: true, Ref: "creds"}, "Read from the Secret creds"},
		{EnvVar{Name: "DB_PASSWORD", Value: "x"}, "Sensitive"},
		{EnvVar{Name: "URL", Value: "postgres://a:b@db/x"}, "Sensitive"},
		{EnvVar{Name: "PORT", Value: "80"}, "integer"},
	} {
		p := EnvProperty(tc.e)
		if !strings.Contains(fmt.Sprint(p), tc.want) {
			t.Errorf("%+v: %v", tc.e, p)
		}
	}
}

func TestDirExternals(t *testing.T) {
	dir := folder(t)
	sc, err := Dir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var where []string
	for _, x := range sc.Externals(dir, externalcatalog.Default()) {
		where = append(where, x.Identity+" "+x.Where)
	}
	for _, w := range []string{"stripe k8s/app.yaml, env STRIPE", "stripe chart/values.yaml:2 hosts", "stripe k8s/app.yaml, secretKeyRef creds/stripe-token (key name only)"} {
		if !slices.Contains(where, w) {
			t.Errorf("externals miss %q: %q", w, where)
		}
	}
	if slices.ContainsFunc(where, func(s string) bool { return strings.Contains(s, "chart2") }) {
		t.Errorf("an ignored values.yaml was read: %q", where)
	}
	if !NamesHost(externalcatalog.NormalizeHost("https://api.stripe.com")) || NamesHost(externalcatalog.NormalizeHost("orders")) {
		t.Error("NamesHost")
	}
	if CommentSafe("a\nb c") != "a?b?c" || EnvWhere(EnvVar{File: "f", Name: "N\n"}) != "f, env N?" {
		t.Error("CommentSafe")
	}
	visited := 0
	(&Scan{}).ChartValues(dir, "chart", func(string, string, string) { visited++ })
	if visited != 1 {
		t.Errorf("chart values without a .pactoignore: %d", visited)
	}
}

func TestDirSecretRefExternal(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{"d.yaml": `apiVersion: apps/v1
kind: Deployment
metadata: {name: x}
spec: {template: {spec: {containers: [{name: x, envFrom: [{secretRef: {name: stripe-credentials}}]}]}}}
`})
	sc, err := Dir(dir)
	if err != nil {
		t.Fatal(err)
	}
	ext := sc.Externals(dir, externalcatalog.Default())
	if len(ext) != 1 || ext[0].Where != "d.yaml, secretRef stripe-credentials (Secret name only)" {
		t.Errorf("externals: %+v", ext)
	}
}

func TestDirPicksAndRefuses(t *testing.T) {
	sc, err := Dir(writeTree(t, t.TempDir(), map[string]string{"j.yaml": "apiVersion: batch/v1\nkind: Job\nmetadata: {name: first}\n---\napiVersion: batch/v1\nkind: Job\nmetadata: {name: second}\n"}))
	if err != nil || sc.Workload.Name != "first" {
		t.Errorf("first workload: %v %+v", err, sc)
	}
	sc, err = Dir(filepath.Join(t.TempDir(), "missing"))
	if err != nil || sc.Workload != nil || sc.Env != nil {
		t.Errorf("missing folder: %v %+v", err, sc)
	}
	if _, err := Dir(writeTree(t, t.TempDir(), map[string]string{".pactoignore/x": ""})); err == nil || !strings.Contains(err.Error(), ".pactoignore: ") {
		t.Errorf("unreadable .pactoignore: %v", err)
	}
	locked := writeTree(t, t.TempDir(), map[string]string{"sub/x": "x", "a.yaml": "apiVersion: v1\nkind: Service\nmetadata: {name: a}\n"})
	if err := os.Chmod(filepath.Join(locked, "a.yaml"), 0); err != nil {
		t.Fatal(err)
	}
	if sc, err := Dir(locked); err != nil || sc.Services["a"] {
		t.Errorf("unreadable file: %v", err)
	}
	if err := os.Chmod(filepath.Join(locked, "sub"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(locked, "sub"), 0o755) })
	if _, err := Dir(locked); err == nil || !strings.Contains(err.Error(), "reading") {
		t.Errorf("unreadable folder: %v", err)
	}
}
