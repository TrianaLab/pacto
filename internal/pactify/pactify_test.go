package pactify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// writeTree writes files (slash paths relative to dir) and returns dir.
func writeTree(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for p, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// fraudDir copies testdata/fraud, a copy of examples/story/plain/fraud (the
// plain service the story pactifies; keep the two in step), into a folder
// named fraud.
func fraudDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "fraud")
	files := map[string]string{}
	err := filepath.WalkDir("testdata/fraud", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := os.ReadFile(p)
			rel, _ := filepath.Rel("testdata/fraud", p)
			files[filepath.ToSlash(rel)] = string(data)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return writeTree(t, dir, files)
}

func file(t *testing.T, r *Result, path string) File {
	t.Helper()
	for _, f := range r.Files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("no %s in the plan: %+v", path, r.Files)
	return File{}
}

func hasLine(lines []string, sub string) bool {
	return slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, sub) })
}

// TestPactifyFraud is the story's pactify step: plain/fraud, a Dockerfile and
// two manifests, becomes the contract the story hand-wrote for it
// (contracts/fraud/1.0.0) plus the cluster pieces. The plan writes nothing,
// the apply writes it all, and a second run adds nothing.
func TestPactifyFraud(t *testing.T) {
	dir := fraudDir(t)
	opts := Options{Dir: dir, Owner: "risk", Delivery: true, Plan: true}
	r, err := Init(context.Background(), opts, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if r.Applied || r.Mode != modePactify || r.Service != "fraud" {
		t.Fatalf("plan: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "pacto.yaml")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("--plan wrote pacto.yaml")
	}

	if text := file(t, r, "pacto.yaml").Content; text != fraudContract {
		t.Fatalf("pacto.yaml:\n%s", text)
	}
	wantFound := []string{"Deployment fraud in k8s/deployment.yaml", "version 1.0.0, from the image tag in k8s/deployment.yaml",
		"registry registry.example/story, from the image registry.example/story/images/fraud:1.0.0", "Dockerfile builds the image registry.example/story/images/fraud"}
	wantUnknown := []string{"interfaces: fraud listens on port 8080, but no OpenAPI file describes it",
		"who uses fraud: no other services' contracts were given (--fleet), so none were looked at"}
	if !slices.Equal(r.Found, wantFound) || !slices.Equal(r.Unknown, wantUnknown) || len(r.Dependencies) != 0 {
		t.Fatalf("findings: %+v", r)
	}
	if ig := file(t, r, ".pactoignore"); ig.Content != "cosign.key\ndelivery/\n" {
		t.Fatalf(".pactoignore: %+v", ig)
	}
	if res := file(t, r, "delivery/pacto.yaml").Content; !strings.Contains(res, "oci: oci://registry.example/story/fraud:@PACTO_VERSION@") ||
		!strings.Contains(res, "  target:\n    serviceName: fraud\n    workloadRef:\n") {
		t.Fatalf("the Pacto resource does not name the contract and fraud's Service:\n%s", res)
	}
	checkStages(t, file(t, r, "delivery/test.yaml").Content, file(t, r, "delivery/prod.yaml").Content)

	opts.Plan = false
	if r, err = Init(context.Background(), opts, os.WriteFile); err != nil || !r.Applied {
		t.Fatalf("apply: %+v %v", r, err)
	}
	c, _ := contract.Parse(strings.NewReader(fraudContract))
	runManifestsBuild(t, dir, c.Publish.Artifacts[1].Build.Command)
	checkSecondRunAddsNothing(t, opts)
}

// checkSecondRunAddsNothing runs init again: every file is kept, and the
// contract it wrote already ships the Pacto resource.
func checkSecondRunAddsNothing(t *testing.T, opts Options) {
	t.Helper()
	again, err := Init(context.Background(), opts, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range again.Files {
		if f.Action != Keep {
			t.Errorf("second run would %s %s", f.Action, f.Path)
		}
	}
	if hasLine(again.Next, "ship delivery/pacto.yaml") {
		t.Error("the contract already ships delivery/pacto.yaml")
	}
}

// fraudContract is what init writes for fraud: the contract the story wrote
// by hand (contracts/fraud/1.0.0), with the Pacto resource in the manifests
// build because of --delivery.
const fraudContract = `# Written by pacto init from Dockerfile, k8s/deployment.yaml, k8s/service.yaml.
# Check it, then see what publishing would do: pacto publish --plan
pactoVersion: "2.1"
service:
  name: fraud
  version: 1.0.0
  owner:
    team: risk
workload: service
publish:
  bundle: oci://registry.example/story/fraud
  deploy: manifests
  artifacts:
    - name: api
      type: image
      ref: oci://registry.example/story/images/fraud
      build:
        dockerfile:
          context: .
    - name: manifests
      type: manifests
      ref: oci://registry.example/story/deploy/fraud
      build:
        command: [sh, -c, 'cp k8s/deployment.yaml k8s/service.yaml "$PACTO_OUT"/ && sed "s/@PACTO_VERSION@/$PACTO_VERSION/" delivery/pacto.yaml > "$PACTO_OUT"/pacto-resource.yaml']
  sign:
    key: cosign.key
`

// checkStages decodes every Stage strictly into the real type, and checks the
// promotion order: test takes new versions from its candidate OCIRepository,
// prod takes what test verified.
func checkStages(t *testing.T, test, prod string) {
	t.Helper()
	stage := func(text string) (delivery.StageSpec, map[string]bool) { return decodeStage(t, text) }
	ts, tk := stage(test)
	ps, pk := stage(prod)
	if ts.Candidates.OCIRepositoryRef == nil || ts.Candidates.OCIRepositoryRef.Name != "fraud-candidates" || ts.Environment != "test" || !ts.Auto || ts.ApplyRef.Name != "fraud" {
		t.Fatalf("test Stage: %+v", ts)
	}
	if ps.Candidates.UpstreamStage == nil || *ps.Candidates.UpstreamStage != (delivery.StageRef{Name: "fraud", Namespace: "test"}) {
		t.Fatalf("prod Stage: %+v", ps)
	}
	if !tk["OCIRepository"] || !tk["Kustomization"] || !pk["Kustomization"] || !strings.Contains(test, "delivery.pacto.trianalab.io/apply-ref: fraud") {
		t.Fatalf("pieces: test %v prod %v", tk, pk)
	}
}

// decodeStage returns the Stage spec in text, decoded strictly, and the kinds
// of every object there.
func decodeStage(t *testing.T, text string) (delivery.StageSpec, map[string]bool) {
	t.Helper()
	var spec delivery.StageSpec
	kinds := map[string]bool{}
	dec := yaml.NewDecoder(strings.NewReader(text))
	for {
		var doc map[string]any
		if err := dec.Decode(&doc); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		kinds[doc["kind"].(string)] = true
		if doc["kind"] != "Stage" {
			continue
		}
		raw, _ := json.Marshal(doc["spec"])
		jd := json.NewDecoder(bytes.NewReader(raw))
		jd.DisallowUnknownFields()
		if err := jd.Decode(&spec); err != nil {
			t.Fatalf("Stage spec: %v\n%s", err, raw)
		}
	}
	return spec, kinds
}

// runManifestsBuild runs the build command pacto publish would, and checks it
// ships the manifests and the Pacto resource for the version published.
func runManifestsBuild(t *testing.T, dir string, command []string) {
	t.Helper()
	out := t.TempDir()
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir, cmd.Env = dir, append(os.Environ(), "PACTO_OUT="+out, "PACTO_VERSION=1.0.0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("manifests build: %v\n%s", err, b)
	}
	res, err := os.ReadFile(filepath.Join(out, "pacto-resource.yaml"))
	if err != nil || !strings.Contains(string(res), "oci: oci://registry.example/story/fraud:1.0.0") {
		t.Fatalf("pacto-resource.yaml: %v\n%s", err, res)
	}
	for _, f := range []string{"deployment.yaml", "service.yaml"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Error(err)
		}
	}
}

// TestDependenciesFromEnvironment: a workload's environment names the
// services it calls. Its own Services, itself, localhost and anything outside
// the cluster are not dependencies; each one found is listed once, left
// commented out in the contract and its accepted versions are Unknown.
func TestDependenciesFromEnvironment(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"Dockerfile":         "FROM scratch\n",
		"deploy/secret.yaml": "apiVersion: v1\nkind: Secret\nmetadata: {name: s}\nstringData: {password: hunter2}\n",
		"deploy/app.yaml": `apiVersion: apps/v1
kind: Deployment
metadata: {name: checkout, labels: {team: shop}}
spec:
  template:
    spec:
      containers:
        - name: sidecar
          image: ghcr.io/acme/sidecar:2
        - name: checkout
          image: ghcr.io/acme/checkout:v1.9.0
          ports: [{containerPort: 8080}, {containerPort: 9090}]
          env:
            - {name: PAYMENTS_URL, value: "http://payments:8080/v1"}
            - {name: PAYMENTS_AGAIN, value: "http://payments:8080"}
            - {name: LEDGER, value: "ledger.billing.svc.cluster.local:9000"}
            - {name: STRIPE, value: "https://api.stripe.com"}
            - {name: LOG_LEVEL, value: info}
            - {name: SELF, value: "http://checkout:8080"}
            - {name: LOCAL, value: "localhost:9000"}
            - {name: DB, value: "postgres://u:p@db:5432/x"}
            - {name: OTHER, value: "orders.shop:80"}
            - {name: SECRET, valueFrom: {secretKeyRef: {name: s, key: k}}}
---
apiVersion: v1
kind: Service
metadata: {name: db}
`,
	})
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	want := []Finding{{Name: "payments", Where: "deploy/app.yaml, env PAYMENTS_URL"}, {Name: "ledger", Where: "deploy/app.yaml, env LEDGER"}}
	if !slices.Equal(r.Dependencies, want) {
		t.Fatalf("dependencies: %+v", r.Dependencies)
	}
	text := file(t, r, "pacto.yaml").Content
	for _, s := range []string{"#   - name: payments\n#     ref: oci://ghcr.io/acme/contracts/payments", "version: 1.9.0", "team: shop", "ref: oci://ghcr.io/acme/checkout\n",
		"bundle: oci://ghcr.io/acme/contracts/checkout\n",
		`command: [sh, -c, 'cp deploy/app.yaml "$PACTO_OUT"/']`, "from Dockerfile, deploy/app.yaml.\n"} {
		if !strings.Contains(text, s) {
			t.Errorf("pacto.yaml lacks %q:\n%s", s, text)
		}
	}
	if !hasLine(r.Next, "deploy/secret.yaml holds a Secret's values") ||
		!hasLine(r.Unknown, "the versions of payments that checkout accepts") || !hasLine(r.Found, "owner team shop") || !hasLine(r.Unknown, "port 8080, 9090") {
		t.Fatalf("findings: %+v", r)
	}
}

// TestPactifyWhatItCannotTell: a folder with only Dockerfiles says nothing
// about the version, the registry, the owner, the workload or the API, so each
// is Unknown, and with no manifests there is nothing for Flux to apply.
func TestPactifyWhatItCannotTell(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"build/api.Dockerfile":    "FROM scratch\n",
		"build/deep/Dockerfile":   "FROM scratch\n",
		"build/Dockerfile.worker": "FROM scratch\n",
		"notes.yaml":              "just: data\n",
		"broken.yml":              "a: [\n",
	})
	r, err := Init(context.Background(), Options{Dir: dir, Name: "api", Delivery: true, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	text := file(t, r, "pacto.yaml").Content
	for _, s := range []string{"version: 0.1.0", "bundle: oci://registry.example/api", "ref: oci://registry.example/images/api",
		"context: build\n          file: build/Dockerfile.worker", "# owner:"} {
		if !strings.Contains(text, s) {
			t.Errorf("pacto.yaml lacks %q:\n%s", s, text)
		}
	}
	if strings.Contains(text, "deploy:") || strings.Contains(text, "workload:") {
		t.Errorf("pacto.yaml claims what it does not know:\n%s", text)
	}
	for _, s := range []string{"version: no image tag", "registry: no image", "owner: no team", "workload: no Kubernetes", "nothing describes what api serves",
		"several Dockerfiles (build/Dockerfile.worker, build/api.Dockerfile, build/deep/Dockerfile): pacto init used build/Dockerfile.worker", "the cluster pieces"} {
		if !hasLine(r.Unknown, s) {
			t.Errorf("Unknown lacks %q: %v", s, r.Unknown)
		}
	}
	if ig := file(t, r, ".pactoignore"); ig.Content != "cosign.key\n" {
		t.Fatalf(".pactoignore: %+v", ig)
	}
}

// TestPactifyReadsWhatIsThere: an OpenAPI file becomes the interface, a
// CronJob a scheduled workload, a chart and a kustomization are named, a
// registry given wins over the image's, and manifests beside a kustomization
// keep their folders. Tooling folders and the cluster pieces init writes are
// never read.
func TestPactifyReadsWhatIsThere(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"Dockerfile":                "FROM scratch\n",
		"Dockerfile.dev":            "FROM scratch\n",
		"api/openapi.yaml":          "openapi: 3.0.0\n",
		"docs/old/swagger.json":     "{}",
		"cosign.key":                "key",
		"chart/Chart.yaml":          "name: x\n",
		"chart/sub/Chart.yaml":      "name: y\n",
		"node_modules/x/Dockerfile": "FROM scratch\n",
		".github/ci.yaml":           "apiVersion: v1\nkind: ConfigMap\n",
		"delivery/test.yaml":        "apiVersion: v1\nkind: Stage\n",
		"my k8s/cron.yaml":          "apiVersion: batch/v1\nkind: CronJob\nmetadata: {name: report}\nspec:\n  jobTemplate:\n    spec:\n      template:\n        spec:\n          containers: [{name: report, image: 'reg.io/r:latest'}]\n",
		"my k8s/kustomization.yaml": "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources: [cron.yaml]\n",
		"mixed/a.yaml":              "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a}\n",
		"mixed/b.yml":               "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: b}\n",
		"mixed/values.yaml":         "replicas: 1\n",
		"root.yaml":                 "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: r}\n",
		".pactoignore":              "*.key",
	})
	r, err := Init(context.Background(), Options{Dir: dir, Registry: "oci://ghcr.io/acme/", Delivery: true, Environments: []string{"dev"}, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	text := file(t, r, "pacto.yaml").Content
	for _, s := range []string{"name: report", "workload: scheduled", "ref: api/openapi.yaml", "bundle: oci://ghcr.io/acme/report", "ref: oci://reg.io/r\n",
		`mkdir -p "$PACTO_OUT"/mixed/ && cp mixed/a.yaml mixed/b.yml "$PACTO_OUT"/mixed/ && mkdir -p "$PACTO_OUT"/''my k8s''/ && cp ''my k8s/cron.yaml'' ''my k8s/kustomization.yaml'' "$PACTO_OUT"/''my k8s''/ && cp root.yaml "$PACTO_OUT"/ &&`} {
		if !strings.Contains(text, s) {
			t.Errorf("pacto.yaml lacks %q:\n%s", s, text)
		}
	}
	if strings.Contains(text, " file:") {
		t.Errorf("a plain Dockerfile needs no file:\n%s", text)
	}
	if !hasLine(r.Unknown, "the Helm chart in chart is not read") || !hasLine(r.Next, "my k8s/kustomization.yaml decides what Flux applies") ||
		!hasLine(r.Unknown, "several Dockerfiles") || hasLine(r.Next, "signing key") {
		t.Fatalf("findings: %+v", r)
	}
	// No Service in the folder, so the target names none: workloadRef follows target directly.
	if !strings.Contains(file(t, r, "delivery/pacto.yaml").Content, "  target:\n    workloadRef:\n      name: report\n      kind: CronJob\n") || !strings.Contains(file(t, r, "delivery/dev.yaml").Content, "ociRepositoryRef") {
		t.Fatal("delivery pieces")
	}
	if ig := file(t, r, ".pactoignore"); ig.Action != Append || ig.Content != "delivery/\n" {
		t.Fatalf(".pactoignore: %+v", ig)
	}

	// Applying appends to the user's .pactoignore, ending its last line first.
	r.Files = []File{file(t, r, ".pactoignore")}
	if err := apply(r, os.WriteFile); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, ".pactoignore")); string(data) != "*.key\ndelivery/\n" {
		t.Fatalf(".pactoignore: %q", data)
	}
}

// TestPactifyKustomizeLayout: a base and an overlay keep their folders, so
// neither kustomization overwrites the other, the one with a secretGenerator
// is left out like a Secret, and every file is named, so a Secret added later
// is not shipped by a glob.
func TestPactifyKustomizeLayout(t *testing.T) {
	deploy := "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: api}\nspec:\n  template:\n    spec:\n      containers: [{name: api, image: 'reg.io/api:1'}]\n"
	dir := writeTree(t, t.TempDir(), map[string]string{
		"deploy/base/deployment.yaml":             deploy,
		"deploy/base/kustomization.yaml":          "resources: [deployment.yaml]\n",
		"deploy/overlays/prod/deployment.yaml":    deploy,
		"deploy/overlays/prod/kustomization.yaml": "resources: [../../base]\npatches: [{path: deployment.yaml}]\n",
		"deploy/overlays/dev/kustomization.yaml":  "resources: [../../base]\nsecretGenerator: [{name: db, literals: [password=hunter2]}]\n",
		"deploy/overlays/dev/deployment.yaml":     deploy,
	})
	r, err := Init(context.Background(), Options{Dir: dir, Registry: "oci://ghcr.io/acme/", Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Publish struct {
			Artifacts []struct{ Build struct{ Command []string } }
		}
	}
	if err := yaml.Unmarshal([]byte(file(t, r, "pacto.yaml").Content), &c); err != nil {
		t.Fatal(err)
	}
	cmd := c.Publish.Artifacts[len(c.Publish.Artifacts)-1].Build.Command[2]
	if strings.Contains(cmd, "*") || strings.Contains(cmd, "overlays/dev/kustomization.yaml") {
		t.Fatalf("command: %s", cmd)
	}
	if !hasLine(r.Next, "deploy/overlays/dev/kustomization.yaml holds a Secret's values") || !hasLine(r.Unknown, "the folder Flux builds") {
		t.Fatalf("findings: %+v", r)
	}
	_ = os.WriteFile(filepath.Join(dir, "deploy/base/secret.yaml"), []byte("apiVersion: v1\nkind: Secret\ndata: {a: b}\n"), 0o644)
	out := t.TempDir()
	build := exec.Command("sh", "-c", cmd)
	build.Dir, build.Env = dir, append(os.Environ(), "PACTO_OUT="+out)
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	for _, f := range []string{"deploy/base/kustomization.yaml", "deploy/overlays/prod/kustomization.yaml", "deploy/overlays/prod/deployment.yaml"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Error(err)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "deploy/base/secret.yaml")); err == nil {
		t.Error("a Secret added after init was shipped")
	}
}

// TestDeliveryFromTheUsersContract: with a contract already there, the
// cluster pieces follow its publish section: a keyless identity becomes
// Flux's matchOIDCIdentity, a keyless sign with no identity or no sign at all
// is Unknown, and the Pacto resource still has to be added to its build.
func TestDeliveryFromTheUsersContract(t *testing.T) {
	contractWith := func(sign string) string {
		return `pactoVersion: "2.1"
service: {name: payments, version: 2.0.0}
publish:
  bundle: oci://ghcr.io/acme/payments
  deploy: manifests
  artifacts:
    - {name: manifests, type: manifests, ref: oci://ghcr.io/acme/deploy/payments, build: {command: [make, manifests]}}
` + sign
	}
	cases := []struct {
		name, sign string
		check      func(r *Result) bool
	}{
		{"keyless", "  sign: {keyless: true, identity: 'https://github.com/acme/payments/.github/workflows/release.yml@refs/heads/main', issuer: 'https://token.actions.githubusercontent.com'}\n",
			func(r *Result) bool {
				return strings.Contains(file(t, r, "delivery/test.yaml").Content, `- issuer: '^https://token\.actions\.githubusercontent\.com$'`) && !hasLine(r.Unknown, "who publishes")
			}},
		{"keyless without identity", "  sign: {keyless: true}\n", func(r *Result) bool { return hasLine(r.Unknown, "who publishes payments") }},
		{"no sign", "", func(r *Result) bool { return hasLine(r.Unknown, "who publishes payments") }},
		{"key outside the folder", "  sign: {key: /keys/cosign.key}\n", func(r *Result) bool {
			return hasLine(r.Next, "create the Secret cosign-pub") && file(t, r, ".pactoignore").Content == "delivery/\n"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeTree(t, t.TempDir(), map[string]string{"pacto.yaml": contractWith(tc.sign)})
			r, err := Init(context.Background(), Options{Dir: dir, Delivery: true, Plan: true}, os.WriteFile)
			if err != nil {
				t.Fatal(err)
			}
			if r.Mode != modeBlank || r.Service != "payments" || file(t, r, "pacto.yaml").Action != Keep || !hasLine(r.Next, "ship delivery/pacto.yaml") ||
				!strings.Contains(file(t, r, "delivery/test.yaml").Content, "url: oci://ghcr.io/acme/deploy/payments") || !tc.check(r) {
				t.Fatalf("plan: %+v", r)
			}
		})
	}

}

// TestDeliveryEnvironmentAdded: adding an environment in front of kept ones
// leaves the old first Stage reading the registry, and dropping the first
// leaves the new first reading a Stage that is gone; both are Next.
func TestDeliveryEnvironmentAdded(t *testing.T) {
	dir := fraudDir(t)
	if _, err := Init(context.Background(), Options{Dir: dir, Owner: "risk", Delivery: true}, os.WriteFile); err != nil {
		t.Fatal(err)
	}
	r, err := Init(context.Background(), Options{Dir: dir, Delivery: true, Plan: true, Environments: []string{"dev", "test", "prod"}}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if file(t, r, "delivery/dev.yaml").Action != Add || !hasLine(r.Next, "delivery/test.yaml was left as it is, but its Stage takes candidates from the registry, not the Stage in dev") ||
		hasLine(r.Next, "delivery/prod.yaml was left") {
		t.Fatalf("plan: %+v", r)
	}
	r, err = Init(context.Background(), Options{Dir: dir, Delivery: true, Plan: true, Environments: []string{"prod"}}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if !hasLine(r.Next, "delivery/prod.yaml was left as it is, but its Stage takes candidates from the Stage in test, not the registry") {
		t.Fatalf("plan: %+v", r)
	}
	writeTree(t, dir, map[string]string{"delivery/prod.yaml": "kind: [\n"})
	if r, _ = Init(context.Background(), Options{Dir: dir, Delivery: true, Plan: true, Environments: []string{"prod"}}, os.WriteFile); hasLine(r.Next, "was left as it is") {
		t.Fatalf("an unreadable file says nothing: %+v", r)
	}
}

// TestDeliveryWithoutManifests: a contract with no publish section, or one
// that cannot be read, plans no cluster pieces; an unreadable contract is
// Unknown, not an error.
func TestDeliveryWithoutManifests(t *testing.T) {
	for _, c := range []string{"pactoVersion: \"2.1\"\nservice: {name: p, version: 1.0.0}\n", "not: [a contract"} {
		dir := writeTree(t, t.TempDir(), map[string]string{"pacto.yaml": c})
		r, err := Init(context.Background(), Options{Dir: dir, Delivery: true, Plan: true}, os.WriteFile)
		if err != nil || !hasLine(r.Unknown, "the cluster pieces") || len(r.Files) != 1 {
			t.Fatalf("plan: %+v %v", r, err)
		}
	}
	dir := writeTree(t, t.TempDir(), map[string]string{"pacto.yaml": "not: [a contract"})
	if r, _ := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile); !hasLine(r.Unknown, "pacto.yaml could not be read") {
		t.Fatalf("unknown: %v", r.Unknown)
	}
}

// TestConsumersFromFleet: the contracts in --fleet that declare this service,
// by name or by its bundle, are its consumers, each once; a root that cannot
// be read makes the answer partial, and a call nobody declared stays Unknown.
func TestConsumersFromFleet(t *testing.T) {
	fleetDir := writeTree(t, t.TempDir(), map[string]string{
		"checkout/pacto.yaml": "pactoVersion: \"2.0\"\nservice: {name: checkout, version: 1.0.0}\ndependencies:\n  - {name: fraud, ref: 'oci://registry.example/story/fraud:1.0.0', required: true, compatibility: '^1.0.0'}\n",
		"orders/pacto.yaml":   "pactoVersion: \"2.0\"\nservice: {name: orders, version: 1.0.0}\ndependencies:\n  - {name: fraud, ref: 'oci://elsewhere.example/fraud', required: true}\n  - {name: pay, ref: 'oci://registry.example/story/payments', required: true}\n",
		"fraud/pacto.yaml":    "pactoVersion: \"2.0\"\nservice: {name: fraud, version: 1.0.0}\ndependencies:\n  - {name: fraud, ref: 'oci://registry.example/story/fraud', required: true}\n",
	})
	r, err := Init(context.Background(), Options{Dir: fraudDir(t), FleetRoots: []string{fleetDir, filepath.Join(fleetDir, "missing")}, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Consumers) == 0 || r.Consumers[0] != (Finding{Name: "checkout", Where: "declares fraud ^1.0.0"}) {
		t.Fatalf("consumers: %+v", r.Consumers)
	}
	if !hasLine(r.Unknown, "read only in part") || !hasLine(r.Unknown, "who calls fraud without declaring it") {
		t.Fatalf("unknown: %v", r.Unknown)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Init(ctx, Options{Dir: fraudDir(t), FleetRoots: []string{fleetDir}, Plan: true}, os.WriteFile); err == nil {
		t.Fatal("a cancelled read of the fleet was not an error")
	}
}

// TestRefusals: what init refuses before planning anything.
func TestRefusals(t *testing.T) {
	notDir := writeTree(t, t.TempDir(), map[string]string{"f": "x"})
	locked := writeTree(t, t.TempDir(), map[string]string{"sub/x": "x"})
	if err := os.Chmod(filepath.Join(locked, "sub"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(locked, "sub"), 0o755) })
	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{
		{"a file", Options{Dir: filepath.Join(notDir, "f")}, "is a file"},
		{"unreadable", Options{Dir: locked, Name: "x"}, "reading"},
		{"bad name", Options{Dir: t.TempDir(), Name: "Bad_Name"}, "not a valid service name"},
		{"bad environment", Options{Dir: t.TempDir(), Name: "x", Environments: []string{"Prod"}}, "not a valid namespace name"},
		{"bad owner", Options{Dir: fraudDir(t), Owner: "the risk team"}, "is not valid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Init(context.Background(), tc.opts, os.WriteFile); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

// TestApplyFailures: a write that fails stops the apply and says which file.
func TestApplyFailures(t *testing.T) {
	failing := func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	if _, err := Init(context.Background(), Options{Dir: filepath.Join(t.TempDir(), "svc")}, failing); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("write: %v", err)
	}

	// A file where a folder must go.
	dir := writeTree(t, fraudDir(t), map[string]string{"delivery": "a file"})
	if _, err := Init(context.Background(), Options{Dir: dir, Delivery: true}, os.WriteFile); err == nil || !strings.Contains(err.Error(), "failed to create directory") {
		t.Fatalf("mkdir: %v", err)
	}

	// The file to append to went away between plan and apply.
	r := &Result{Dir: t.TempDir(), Files: []File{{Path: ".pactoignore", Action: Append, Content: "x\n"}}}
	if err := apply(r, os.WriteFile); err == nil {
		t.Fatal("appending to a missing file was not an error")
	}
}

func TestHelpers(t *testing.T) {
	for in, want := range map[string]string{"python:3.13": "docker.io/library/python", "acme/app": "docker.io/acme/app", "localhost:5000/app:1": "localhost:5000/app",
		"reg.io/a/b@sha256:abc": "reg.io/a/b"} {
		if got, _ := imageRepo(in); got != want {
			t.Errorf("imageRepo(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"risk": "risk", "1.0.0": "1.0.0", "1.0": "'1.0'", "yes": "'yes'", "it's": "'it''s'"} {
		if got := yq(in); got != want {
			t.Errorf("yq(%q) = %q, want %q", in, got, want)
		}
	}
	if refRepo("oci://r.io/a/b:1.0.0") != "r.io/a/b" || refRepo("oci://r.io:5000/a@sha256:x") != "r.io:5000/a" {
		t.Error("refRepo")
	}
}

// TestManifestsOnly: manifests with no Dockerfile publish no image, and an
// image name that is not a valid repository says nothing about the registry.
func TestManifestsOnly(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"k8s/app.yaml": "apiVersion: apps/v1\nkind: StatefulSet\nmetadata: {name: store}\nspec: {template: {spec: {containers: [{name: store, image: 'Reg.IO/Store:1.0.0'}]}}}\n",
	})
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	text := file(t, r, "pacto.yaml").Content
	if strings.Contains(text, "type: image") || !strings.HasPrefix(text, "# Written by pacto init from k8s/app.yaml.\n") || !hasLine(r.Unknown, "registry: no image") {
		t.Fatalf("plan: %+v\n%s", r, text)
	}
}

// TestDefaultFolder: with no folder, init uses the name as the folder, and
// with neither the current folder.
func TestDefaultFolder(t *testing.T) {
	t.Chdir(t.TempDir())
	r, err := Init(context.Background(), Options{Name: "svc", Plan: true}, os.WriteFile)
	if err != nil || r.Dir != "svc" || r.Mode != modeBlank {
		t.Fatalf("plan: %+v %v", r, err)
	}
	dir := filepath.Join(t.TempDir(), "here")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if r, err = Init(context.Background(), Options{Plan: true}, os.WriteFile); err != nil || r.Dir != "." || r.Service != "here" {
		t.Fatalf("plan: %+v %v", r, err)
	}
}
