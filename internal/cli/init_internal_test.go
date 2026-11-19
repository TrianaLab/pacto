package cli

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/pactify"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func runInit(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCommand(app.NewService(nil, nil), VersionInfo{Version: "test"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"init"}, args...))
	err := root.Execute()
	return out.String(), err
}

func serviceFolder(t *testing.T, name, image string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: " + name + "}\nspec:\n  template:\n    spec:\n      containers: [{name: " + name + ", image: " + image + "}]\n"
	if err := os.WriteFile(filepath.Join(dir, "deploy.yaml"), []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestInitSeveralFolders: each folder is planned, then a summary; a folder
// that fails is named and fails the command after the rest.
func TestInitSeveralFolders(t *testing.T) {
	a, b := serviceFolder(t, "checkout", "reg.io/acme/checkout:1.0.0"), serviceFolder(t, "payments", "reg.io/acme/payments:2.0.0")
	out, err := runInit(t, "--plan", a, b)
	if err != nil || !strings.Contains(out, "SERVICE") || !strings.Contains(out, "checkout") || !strings.Contains(out, "payments") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = runInit(t, "--plan", a, filepath.Join(b, "deploy.yaml"))
	if err == nil || !strings.Contains(err.Error(), "is a file, not a folder") || !strings.Contains(out, "checkout") {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = runInit(t, "--plan", "--output-format", "json", a, b)
	var bulk pactify.Bulk
	if err != nil || json.Unmarshal([]byte(out), &bulk) != nil || len(bulk.Results) != 2 {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := runInit(t, "--name", "x", a, b); err == nil {
		t.Fatal("one name for two folders")
	}
}

// TestInitCluster: --cluster drafts every workload no Pacto describes, and
// the flags that only go with it, or without folders, are refused.
func TestInitCluster(t *testing.T) {
	for _, args := range [][]string{{"--cluster", "x"}, {"--out", "x"}, {"--namespace", "shop"}} {
		if _, err := runInit(t, args...); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	defer func(old func() (dynamic.Interface, error)) { initDynamic = old }(initDynamic)
	initDynamic = func() (dynamic.Interface, error) { return nil, errors.New("no kubeconfig") }
	if _, err := runInit(t, "--cluster", "--out", t.TempDir()); err == nil || err.Error() != "no kubeconfig" {
		t.Fatalf("client: %v", err)
	}
	gvr := func(g, v, r string) schema.GroupVersionResource {
		return schema.GroupVersionResource{Group: g, Version: v, Resource: r}
	}
	lists := map[schema.GroupVersionResource]string{
		gvr("apps", "v1", "deployments"): "DeploymentList", gvr("apps", "v1", "statefulsets"): "StatefulSetList", gvr("apps", "v1", "daemonsets"): "DaemonSetList",
		gvr("batch", "v1", "cronjobs"): "CronJobList", gvr("", "v1", "services"): "ServiceList", gvr("", "v1", "pods"): "PodList",
		gvr("apps", "v1", "replicasets"): "ReplicaSetList", gvr("batch", "v1", "jobs"): "JobList", gvr("discovery.k8s.io", "v1", "endpointslices"): "EndpointSliceList",
		gvr("pacto.trianalab.io", "v1alpha1", "pactos"): "PactoList", gvr("pacto.trianalab.io", "v1alpha1", "noderuntimeobservations"): "NodeRuntimeObservationList",
	}
	deploy := func(name string) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": name, "namespace": "shop"},
			"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": name, "image": "reg.io/acme/" + name + ":1.0.0"}}}}}}}
	}
	pacto := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "pacto.trianalab.io/v1alpha1", "kind": "Pacto", "metadata": map[string]any{"name": "payments", "namespace": "shop"},
		"spec": map[string]any{"target": map[string]any{"workloadRef": map[string]any{"name": "payments", "kind": "Deployment"}}}}}
	initDynamic = func() (dynamic.Interface, error) {
		return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, deploy("checkout"), deploy("payments"), pacto), nil
	}
	drafts := t.TempDir()
	out, err := runInit(t, "--cluster", "--namespace", "shop", "--out", drafts)
	if err != nil || !strings.Contains(out, "shop/Deployment/payments") || !strings.Contains(out, "skipped: a Pacto describes it") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(drafts, "shop", "checkout", "pacto.yaml")); err != nil {
		t.Fatal(err)
	}
}

// TestPrintBulk: nothing to print returns the error; a JSON write that fails
// is reported.
func TestPrintBulk(t *testing.T) {
	cmd, _ := testCmd()
	boom := errors.New("boom")
	if err := printBulk(cmd, nil, boom, "text"); err != boom {
		t.Fatal(err)
	}
	cmd.SetOut(errWriter{})
	if err := printBulk(cmd, &pactify.Bulk{}, nil, "json"); err == nil {
		t.Fatal("the failed write was not reported")
	}
	cmd, buf := testCmd()
	if err := printBulk(cmd, &pactify.Bulk{Results: []*pactify.Result{nil}}, boom, "json"); err != boom || !strings.Contains(buf.String(), "results") {
		t.Fatal(err)
	}
}

// TestPrintBulkFailedFolder: a folder that planned nothing leaves no row.
func TestPrintBulkFailedFolder(t *testing.T) {
	cmd, buf := testCmd()
	r := &pactify.Result{Service: "a", Dir: "a", Files: []pactify.File{{Path: "pacto.yaml", Action: pactify.Keep}}, Dependencies: []pactify.Finding{{Name: "b"}, {Name: "c"}}}
	if err := printBulk(cmd, &pactify.Bulk{Results: []*pactify.Result{nil, r}}, nil, "text"); err != nil || !strings.Contains(buf.String(), "b,c") {
		t.Fatalf("%v\n%s", err, buf)
	}
}

// TestInitAcceptsTheCommandDeliveryPrints: a hold tells the person to run
// delivery.InitClusterCommand; pacto init must parse exactly those flags.
func TestInitAcceptsTheCommandDeliveryPrints(t *testing.T) {
	defer func(old func() (dynamic.Interface, error)) { initDynamic = old }(initDynamic)
	initDynamic = func() (dynamic.Interface, error) { return nil, errors.New("no kubeconfig") }
	args := strings.Fields(strings.Replace(delivery.InitClusterCommand("prod"), "<dir>", t.TempDir(), 1))
	if args[0] != "pacto" || args[1] != "init" {
		t.Fatalf("not a pacto init command: %v", args)
	}
	if _, err := runInit(t, args[2:]...); err == nil || err.Error() != "no kubeconfig" {
		t.Fatalf("pacto init refused %v: %v", args, err)
	}
}

// TestInitDomainFlag: --domain and --owner reach a blank contract, which
// declares no publish section, so the registry goes nowhere in it.
func TestInitDomainFlag(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "blank")
	if out, err := runInit(t, dir, "--owner", "billing", "--domain", "payments", "--registry", "ghcr.io/acme"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`pactoVersion: "2.5"`, "team: billing", "domain: payments"} {
		if !strings.Contains(string(data), s) {
			t.Errorf("pacto.yaml lacks %q:\n%s", s, data)
		}
	}
	if strings.Contains(string(data), "publish:") || strings.Contains(string(data), "version: 0.1.0") {
		t.Errorf("pacto.yaml writes what pacto ci --publish derives:\n%s", data)
	}
}

// TestInitFromFlagChecks: what cannot go with a golden path, or needs one, is
// refused before anything is read.
func TestInitFromFlagChecks(t *testing.T) {
	path := t.TempDir()
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"values without from":           {[]string{"--set", "service.version=1.0.0"}, "go with --from"},
		"trust without from":            {[]string{"--trust", "trust.yaml"}, "go with --from"},
		"plugins without from":          {[]string{"--run-plugins", "dockerfile"}, "go with --from"},
		"from with fleet":               {[]string{t.TempDir(), "--from", path, "--owner", "checkout", "--fleet", path}, "drop --fleet"},
		"from, secret without delivery": {[]string{t.TempDir(), "--from", path, "--owner", "checkout", "--registry-secret", "creds"}, "needs delivery"},
		"from with folders":             {[]string{"a", "b", "--from", path}, "one service in one folder"},
		"from with cluster":             {[]string{"--cluster", "--from", path}, "one service in one folder"},
		"from without owner":            {[]string{t.TempDir(), "--from", path}, "--owner"},
		"from with a bad trust":         {[]string{t.TempDir(), "--from", path, "--owner", "checkout", "--trust", filepath.Join(path, "missing")}, "trust config"},
	} {
		if out, err := runInit(t, c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v\n%s", name, err, out)
		}
	}
}

// TestInitFromDirectory: --from creates the service and prints its Status.
func TestInitFromDirectory(t *testing.T) {
	t.Setenv("PACTO_DELIVERY_TRUST", "")
	path := t.TempDir()
	_ = os.WriteFile(filepath.Join(path, "pacto.yaml"), []byte("pactoVersion: \"2.4\"\nservice:\n  name: http-service\n  version: 1.0.0\n  template: {}\n"), 0o644)
	dir := filepath.Join(t.TempDir(), "gift-cards")
	out, err := runInit(t, dir, "--from", path, "--owner", "checkout", "--set", "service.version=0.2.0")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "Status\n  unknown") || !strings.Contains(out, "a directory carries no signature") {
		t.Errorf("no Status block:\n%s", out)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml")); !strings.Contains(string(data), "version: 0.2.0") {
		t.Errorf("--set did not reach the contract:\n%s", data)
	}
}

// TestInitFromWithDelivery: --delivery and its options go with --from; a path
// that publishes no manifests plans no cluster pieces and the Status says why.
func TestInitFromWithDelivery(t *testing.T) {
	t.Setenv("PACTO_DELIVERY_TRUST", "")
	path := t.TempDir()
	_ = os.WriteFile(filepath.Join(path, "pacto.yaml"), []byte("pactoVersion: \"2.4\"\nservice:\n  name: http-service\n  version: 1.0.0\n  template: {}\n"), 0o644)
	dir := filepath.Join(t.TempDir(), "gift-cards")
	out, err := runInit(t, dir, "--from", path, "--owner", "checkout", "--delivery", "--env", "qa,prod", "--registry-secret", "creds", "--insecure-registry", "--plan")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !regexp.MustCompile(`unknown +delivery +no cluster pieces`).MatchString(out) {
		t.Errorf("no delivery step:\n%s", out)
	}
}

// TestTemplateTrustBarePEM: a bare PEM given to --trust is a template key; the
// same PEM in $PACTO_DELIVERY_TRUST stays a record key.
func TestTemplateTrustBarePEM(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pem, _ := delivery.PublicKeyPEM(pub)
	file := filepath.Join(t.TempDir(), "cosign.pub")
	_ = os.WriteFile(file, []byte(pem), 0o644)
	got, err := templateTrust(file)
	if err != nil || len(got.Keys) != 1 || got.Keys[0].Role != delivery.RoleTemplate {
		t.Fatalf("--trust: %+v %v", got, err)
	}
	t.Setenv("PACTO_DELIVERY_TRUST", file)
	if got, err = templateTrust(""); err != nil || got.Keys[0].Role != "" {
		t.Errorf("$PACTO_DELIVERY_TRUST: %+v %v", got, err)
	}
	config := filepath.Join(t.TempDir(), "trust.yaml")
	_ = os.WriteFile(config, []byte("keys:\n  - {name: recorder, publicKey: "+strconv.Quote(pem)+"}\n"), 0o644)
	if got, err = templateTrust(config); err != nil || got.Keys[0].Role != "" {
		t.Errorf("a trust config file: %+v %v", got, err)
	}
	t.Setenv("PACTO_DELIVERY_TRUST", "")
	if got, err = templateTrust(""); err != nil || got != nil {
		t.Errorf("none: %+v %v", got, err)
	}
}

// TestPrintInitSteps: one line per step, its plugin name after the step.
func TestPrintInitSteps(t *testing.T) {
	var b bytes.Buffer
	printCreationSteps(&b, "\nStatus", nil)
	if b.Len() != 0 {
		t.Errorf("no steps printed %q", b.String())
	}
	printCreationSteps(&b, "\nStatus", []fleet.CreationStep{
		{Step: fleet.StepSignature, Status: fleet.StepStatusUnknown, Detail: "a directory carries no signature"},
		{Step: fleet.StepPlugin, Name: "dockerfile", Status: fleet.StepStatusSkipped, Detail: "not run"},
	})
	want := "\nStatus\n  unknown  signature          a directory carries no signature\n  skipped  plugin dockerfile  not run\n"
	if b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}

// TestInitDeliveryDeclaresEnvironments: --delivery --env declares each
// environment in pacto.yaml with an empty values file.
func TestInitDeliveryDeclaresEnvironments(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "blank")
	if out, err := runInit(t, dir, "--owner", "billing", "--delivery", "--env", "test,pr-1"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`pactoVersion: "2.5"`, "  - name: test\n    file: environments/test.yaml\n", "  - name: pr-1\n    file: environments/pr-1.yaml\n"} {
		if !strings.Contains(string(data), s) {
			t.Errorf("pacto.yaml lacks %q:\n%s", s, data)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "environments", "pr-1.yaml")); err != nil || !strings.HasSuffix(string(raw), "{}\n") {
		t.Errorf("environments/pr-1.yaml: %q %v", raw, err)
	}
}

// leakFiles is a chart folder whose Deployment sets sensitive literals and reads
// one from a ConfigMap in the folder.
var leakFiles = map[string]string{
	"Chart.yaml":  "apiVersion: v2\nname: payments\nversion: 1.0.0\n",
	"values.yaml": "db:\n  password: v4lues-s3cret\n",
	"deploy/app.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: payments}\nspec:\n  template:\n    spec:\n      containers:\n" +
		"        - name: payments\n          image: reg.io/payments:1.0.0\n          envFrom: [{configMapRef: {name: cm}}]\n          env:\n" +
		"            - {name: DB_PASSWORD, value: hunter2}\n            - {name: DATABASE_URL, value: \"postgres://app:u4l-s3cret@db:5432/x\"}\n",
	"deploy/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: cm}\ndata: {CLIENT_SECRET: c0nfigmap-s3cret}\n",
}

// leakedLiterals are the folder and cluster fixtures' secret bytes.
var leakedLiterals = []string{"hunter2", "u4l-s3cret", "v4lues-s3cret", "c0nfigmap-s3cret", "r3dis-pw", "t0ken-123"}

// leakFolder writes a fresh copy of leakFiles.
func leakFolder(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "payments")
	for name, body := range leakFiles {
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

// leakCluster is a fake cluster whose checkout Deployment sets sensitive
// literals in a container and an initContainer.
func leakCluster() dynamic.Interface {
	gvr := func(g, v, r string) schema.GroupVersionResource {
		return schema.GroupVersionResource{Group: g, Version: v, Resource: r}
	}
	lists := map[schema.GroupVersionResource]string{
		gvr("apps", "v1", "deployments"): "DeploymentList", gvr("apps", "v1", "statefulsets"): "StatefulSetList", gvr("apps", "v1", "daemonsets"): "DaemonSetList",
		gvr("batch", "v1", "cronjobs"): "CronJobList", gvr("", "v1", "services"): "ServiceList", gvr("", "v1", "pods"): "PodList",
		gvr("apps", "v1", "replicasets"): "ReplicaSetList", gvr("batch", "v1", "jobs"): "JobList", gvr("discovery.k8s.io", "v1", "endpointslices"): "EndpointSliceList",
		gvr("pacto.trianalab.io", "v1alpha1", "pactos"): "PactoList", gvr("pacto.trianalab.io", "v1alpha1", "noderuntimeobservations"): "NodeRuntimeObservationList",
	}
	lit := func(name, value string) map[string]any { return map[string]any{"name": name, "value": value} }
	deploy := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": "checkout", "namespace": "shop"},
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
			"containers":     []any{map[string]any{"name": "app", "image": "reg.io/acme/checkout:1.0.0", "env": []any{lit("DB_PASSWORD", "hunter2"), lit("REDIS_URL", "redis://:r3dis-pw@cache:6379")}}},
			"initContainers": []any{map[string]any{"name": "migrate", "image": "reg.io/acme/migrate:1.0.0", "env": []any{lit("MIGRATE_TOKEN", "t0ken-123")}}},
		}}}}}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, deploy)
}

// TestInitNeverPrintsEnvValues: no secret literal reaches pacto init output on
// any path, and the help says the schema holds keys and types.
func TestInitNeverPrintsEnvValues(t *testing.T) {
	defer func(old func() (dynamic.Interface, error)) { initDynamic = old }(initDynamic)
	initDynamic = func() (dynamic.Interface, error) { return leakCluster(), nil }
	runs := map[string]func() []string{
		"plan":    func() []string { return []string{"--plan", leakFolder(t)} },
		"json":    func() []string { return []string{"--plan", "--output-format", "json", leakFolder(t)} },
		"apply":   func() []string { return []string{"--describe-only", leakFolder(t)} },
		"deliver": func() []string { return []string{leakFolder(t)} },
		"several": func() []string { return []string{"--plan", leakFolder(t), leakFolder(t)} },
		"cluster": func() []string { return []string{"--cluster", "--namespace", "shop", "--out", t.TempDir()} },
		"clusterJ": func() []string {
			return []string{"--cluster", "--namespace", "shop", "--output-format", "json", "--out", t.TempDir()}
		},
	}
	for name, args := range runs {
		t.Run(name, func(t *testing.T) {
			out, err := runInit(t, args()...)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			for _, v := range leakedLiterals {
				if strings.Contains(out, v) {
					t.Errorf("the output holds %q:\n%s", v, out)
				}
			}
		})
	}
	if out, _ := runInit(t, "--help"); !strings.Contains(out, "keys and types") {
		t.Fatalf("help:\n%s", out)
	}
}

// TestInitCompletionNeverPrintsValues: completing a folder that holds
// pacto.yaml lists what it adds in text and JSON, and no secret literal
// reaches either, planned, applied or with several folders.
func TestInitCompletionNeverPrintsValues(t *testing.T) {
	completed := func() string {
		dir := leakFolder(t)
		if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("service: {name: payments, version: 1.0.0}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	runs := map[string]func() []string{
		"plan":    func() []string { return []string{"--plan", completed()} },
		"json":    func() []string { return []string{"--plan", "--output-format", "json", completed()} },
		"apply":   func() []string { return []string{completed()} },
		"several": func() []string { return []string{"--plan", completed(), completed()} },
	}
	for name, args := range runs {
		t.Run(name, func(t *testing.T) {
			out, err := runInit(t, args()...)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			for _, v := range leakedLiterals {
				if strings.Contains(out, v) {
					t.Errorf("the output holds %q:\n%s", v, out)
				}
			}
			if name == "json" {
				var r struct {
					Mode     string `json:"mode"`
					Inferred struct {
						Inputs struct{ Pacto string }   `json:"inputs"`
						Fields []struct{ Field string } `json:"fields"`
					} `json:"inferred"`
				}
				if err := json.Unmarshal([]byte(out), &r); err != nil || r.Mode != "complete" || len(r.Inferred.Fields) == 0 || r.Inferred.Inputs.Pacto != "dev" {
					t.Fatalf("json: %v\n%s", err, out)
				}
				return
			}
			if !strings.Contains(out, "\npayments inferred:\n") || !regexp.MustCompile(`\n  configuration\.DB_PASSWORD +string, writeOnly from deploy/app\.yaml:\d+\n`).MatchString(out) {
				t.Fatalf("text:\n%s", out)
			}
		})
	}
}
