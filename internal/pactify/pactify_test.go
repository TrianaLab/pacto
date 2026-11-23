package pactify

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
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
func fraudDir(t *testing.T) string { return fixtureDir(t, "fraud") }

// fixtureDir copies testdata/<name> into a folder named name.
func fixtureDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	files := map[string]string{}
	err := filepath.WalkDir("testdata/"+name, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := os.ReadFile(p)
			rel, _ := filepath.Rel("testdata/"+name, p)
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
	wantFound := []string{"Deployment fraud in k8s/deployment.yaml",
		"first publish: 1.0.0 is the image tag in k8s/deployment.yaml; declare service.version to publish it, or pacto ci --publish derives 1.0.0",
		"registry registry.example/story, from the image registry.example/story/images/fraud:1.0.0"}
	wantUnknown := []string{"interfaces: fraud listens on port 8080, but no OpenAPI file describes it",
		"who uses fraud: no other services' contracts were given (--fleet), so none were looked at"}
	if !slices.Equal(r.Found, wantFound) || !slices.Equal(r.Unknown, wantUnknown) || len(r.Dependencies) != 0 {
		t.Fatalf("findings: %+v", r)
	}
	checkFraudDelivery(t, r)

	opts.Plan = false
	if r, err = Init(context.Background(), opts, os.WriteFile); err != nil || !r.Applied {
		t.Fatalf("apply: %+v %v", r, err)
	}
	checkSecondRunAddsNothing(t, opts)
}

// checkFraudDelivery checks the files --delivery plans for fraud and how
// init says prod earns its autonomy.
func checkFraudDelivery(t *testing.T, r *Result) {
	t.Helper()
	if ig := file(t, r, ".pactoignore"); ig.Content != "delivery/\n" {
		t.Fatalf(".pactoignore: %+v", ig)
	}
	if res := file(t, r, "delivery/pacto.yaml").Content; !strings.Contains(res, "oci: oci://registry.example/story/fraud:@PACTO_VERSION@") ||
		!strings.Contains(res, "  target:\n    serviceName: fraud\n    workloadRef:\n") {
		t.Fatalf("the Pacto resource does not name the contract and fraud's Service:\n%s", res)
	}
	checkNoClusterPieces(t, r)
	if !hasLine(r.Next, "Pacto creates each environment's Stage once delivery is on") {
		t.Fatalf("init does not say the promoter creates the Stages: %+v", r.Next)
	}
}

// checkNoClusterPieces: --delivery plans the Pacto resource and nothing
// else under delivery/, and no environment values file.
func checkNoClusterPieces(t *testing.T, r *Result) {
	t.Helper()
	for _, f := range r.Files {
		if (strings.HasPrefix(f.Path, "delivery/") && f.Path != "delivery/pacto.yaml") || strings.HasPrefix(f.Path, "environments/") {
			t.Fatalf("init plans %s: the promoter creates environments and Stages", f.Path)
		}
	}
}

// checkSecondRunAddsNothing runs init again: every file is kept, and the
// contract it wrote, with no publish section, needs no hint to ship the
// Pacto resource: pacto ci --publish ships it.
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

// fraudContract is what init writes for fraud: who owns it, its workload and
// no environments, with no service.version and no publish section, which
// pacto ci --publish derives.
const fraudContract = `# Written by pacto init from Dockerfile, k8s/deployment.yaml, k8s/service.yaml.
# Check it, then see what publishing would do: pacto ci
pactoVersion: "2.6"
service:
  name: fraud
  owner:
    team: risk
workload: service
`

// TestDependenciesFromEnvironment: a workload's environment names the
// services it calls. Its own Services, itself, localhost and anything outside
// the cluster are not dependencies; each one found is declared once, with
// compatibility unknown and a comment saying no range is proposed, and its
// accepted versions are Unknown.
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
	for _, s := range []string{"  - name: payments\n    ref: oci://ghcr.io/acme/contracts/payments\n    required: true\n    compatibility: unknown   # no range proposed: no contract or running version of payments was read\n",
		"  # from deploy/app.yaml, env PAYMENTS_URL\n  # no contract for payments was read", "team: shop", "from Dockerfile, deploy/app.yaml.\n"} {
		if !strings.Contains(text, s) {
			t.Errorf("pacto.yaml lacks %q:\n%s", s, text)
		}
	}
	if strings.Contains(text, "version:") || strings.Contains(text, "publish:") || !hasLine(r.Found, "first publish: 1.9.0 is the image tag in deploy/app.yaml") ||
		!hasLine(r.Found, "contract bundles under ghcr.io/acme/contracts, as the image ghcr.io/acme/checkout:v1.9.0 already holds ghcr.io/acme/checkout") {
		t.Fatalf("pacto.yaml:\n%s\nfound: %v", text, r.Found)
	}
	if !hasLine(r.Next, "deploy/secret.yaml holds a Secret's values") ||
		!hasLine(r.Unknown, "the versions of payments that checkout accepts") || !hasLine(r.Found, "owner team shop") || !hasLine(r.Unknown, "port 8080, 9090") {
		t.Fatalf("findings: %+v", r)
	}
}

// TestPactifyWhatItCannotTell: a folder with only Dockerfiles says nothing
// about the registry, the owner, the workload or the API, so each is Unknown,
// and with no manifests there is nothing to apply.
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
	if !strings.Contains(text, "# owner:") || strings.Contains(text, "version:") || strings.Contains(text, "publish:") || strings.Contains(text, "workload:") {
		t.Errorf("pacto.yaml claims what it does not know:\n%s", text)
	}
	for _, s := range []string{"registry: no image", "owner: no team", "workload: no Kubernetes", "nothing describes what api serves", "the cluster pieces"} {
		if !hasLine(r.Unknown, s) {
			t.Errorf("Unknown lacks %q: %v", s, r.Unknown)
		}
	}
	if hasLine(r.Found, "first publish") {
		t.Errorf("no image tag is a version: %v", r.Found)
	}
	if slices.ContainsFunc(r.Files, func(f File) bool { return f.Path == ".pactoignore" }) {
		t.Fatalf("a .pactoignore with nothing to ignore: %+v", r.Files)
	}
}

// TestPactifyReadsWhatIsThere: an OpenAPI file becomes the interface, a
// CronJob a scheduled workload, a kustomization is named and a registry given
// wins over the image's. Tooling folders and the cluster pieces init writes
// are never read.
func TestPactifyReadsWhatIsThere(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"Dockerfile":                "FROM scratch\n",
		"Dockerfile.dev":            "FROM scratch\n",
		"api/openapi.yaml":          "openapi: 3.0.0\n",
		"docs/old/swagger.json":     "{}",
		"cosign.key":                "key",
		"node_modules/x/Dockerfile": "FROM scratch\n",
		".github/ci.yaml":           "apiVersion: v1\nkind: ConfigMap\n",
		"delivery/test.yaml":        "apiVersion: v1\nkind: Stage\n",
		"my k8s/cron.yaml":          "apiVersion: batch/v1\nkind: CronJob\nmetadata: {name: report}\nspec:\n  jobTemplate:\n    spec:\n      template:\n        spec:\n          containers: [{name: report, image: 'reg.io/r:v1.2.0'}]\n",
		"my k8s/kustomization.yaml": "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources: [cron.yaml]\n",
		"mixed/a.yaml":              "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a}\n",
		"mixed/b.yml":               "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: b}\n",
		"mixed/values.yaml":         "replicas: 1\n",
		"root.yaml":                 "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: r}\n",
		".pactoignore":              "*.key",
	})
	r, err := Init(context.Background(), Options{Dir: dir, Registry: "oci://ghcr.io/acme/", Delivery: true, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	text := file(t, r, "pacto.yaml").Content
	for _, s := range []string{"name: report", "workload: scheduled", "ref: api/openapi.yaml"} {
		if !strings.Contains(text, s) {
			t.Errorf("pacto.yaml lacks %q:\n%s", s, text)
		}
	}
	if !hasLine(r.Next, "my k8s/kustomization.yaml decides what Flux applies") || hasLine(r.Next, "signing key") {
		t.Fatalf("findings: %+v", r)
	}
	// No Service in the folder, so the target names none: workloadRef follows target directly.
	for _, want := range [][2]string{{"delivery/pacto.yaml", "  target:\n    workloadRef:\n      name: report\n      kind: CronJob\n"},
		{"delivery/pacto.yaml", "oci: oci://ghcr.io/acme/report:@PACTO_VERSION@"}} {
		if !strings.Contains(file(t, r, want[0]).Content, want[1]) {
			t.Fatalf("%s lacks %q", want[0], want[1])
		}
	}
	checkNoClusterPieces(t, r)
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

// TestDeliveryFromTheUsersContract: with a contract already there, the
// Pacto resource follows its publish section and still has to be added to
// its build; no Stage is planned, whatever the contract signs with.
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
	for name, sign := range map[string]string{"no sign": "", "key outside the folder": "  sign: {key: /keys/cosign.key}\n"} {
		t.Run(name, func(t *testing.T) {
			dir := writeTree(t, t.TempDir(), map[string]string{"pacto.yaml": contractWith(sign), "k8s/deploy.yaml": payments200})
			r, err := Init(context.Background(), Options{Dir: dir, Delivery: true, Plan: true}, os.WriteFile)
			if err != nil {
				t.Fatal(err)
			}
			if r.Mode != modePactify || r.Service != "payments" || file(t, r, "pacto.yaml").Action != Keep || !hasLine(r.Next, "ship delivery/pacto.yaml") ||
				!strings.Contains(file(t, r, "delivery/pacto.yaml").Content, "oci: oci://ghcr.io/acme/payments:@PACTO_VERSION@") ||
				file(t, r, ".pactoignore").Content != "delivery/\n" || hasLine(r.Unknown, "who publishes") || hasLine(r.Next, "cosign-pub") {
				t.Fatalf("plan: %+v", r)
			}
			checkNoClusterPieces(t, r)
		})
	}
}

// TestDeliveryChartWritesNoApplier: a contract that deploys a chart gets no
// applier object and no cluster pieces, only a Next line naming the Stage's
// spec.applyRef: the cluster's applier deploys the chart.
func TestDeliveryChartWritesNoApplier(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{"k8s/deploy.yaml": payments200, "pacto.yaml": `pactoVersion: "2.5"
service: {name: payments, version: 2.0.0}
publish:
  bundle: oci://ghcr.io/acme/payments
  deploy: chart
  artifacts:
    - {name: chart, type: chart, ref: oci://ghcr.io/acme/charts/payments}
`})
	r, err := Init(context.Background(), Options{Dir: dir, Delivery: true, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if !hasLine(r.Next, "publish.deploy names the chart chart, so Pacto wrote no applier object: deploy it with the applier your cluster runs and name that object in each Stage's spec.applyRef") ||
		hasLine(r.Unknown, "the cluster pieces") || slices.ContainsFunc(r.Files, func(f File) bool { return strings.HasPrefix(f.Path, "delivery/") }) {
		t.Fatalf("plan: %+v", r)
	}
}

const payments200 = "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: payments}\nspec:\n  template:\n    spec:\n      containers: [{name: payments, image: ghcr.io/acme/payments:2.0.0}]\n"

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
	// A 2.0 contract, the installed base: init says how to reach pacto ci
	// --publish instead of that there is nothing to add.
	dir := writeTree(t, t.TempDir(), map[string]string{"pacto.yaml": "pactoVersion: \"2.0\"\nservice: {name: p, version: 1.0.0}\n"})
	if r, _ := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile); !hasLine(r.Next, `pacto.yaml declares no publish section, and pacto ci --publish derives one only from pactoVersion 2.5: set pactoVersion: "2.6"`) {
		t.Fatalf("next: %v", r.Next)
	}
	dir = writeTree(t, t.TempDir(), map[string]string{"pacto.yaml": "pactoVersion: \"2.1\"\nservice: {name: p, version: 1.0.0}\n" +
		"publish: {bundle: oci://r/p, deploy: m, artifacts: [{name: m, type: manifests, ref: oci://r/d, build: {command: [make]}}]}\n"})
	if r, _ := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile); hasLine(r.Next, "no publish section") {
		t.Fatalf("a contract with publish: %v", r.Next)
	}
	dir = writeTree(t, t.TempDir(), map[string]string{"pacto.yaml": "not: [a contract"})
	if r, _ := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile); !hasLine(r.Unknown, "pacto.yaml could not be read") {
		t.Fatalf("unknown: %v", r.Unknown)
	}
}

// An empty pacto.yaml declares no name: the plan keeps the folder's, and its
// implicit version is current, so pacto ci --publish derives its publish
// section and init asks for nothing.
func TestExistingEmptyContractKeepsTheFolderName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orders")
	writeTree(t, dir, map[string]string{"pacto.yaml": ""})
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
	if err != nil || r.Service != "orders" {
		t.Fatalf("service = %q, err = %v", r.Service, err)
	}
	if hasLine(r.Next, "pactoVersion") || hasLine(r.Next, "no publish section") {
		t.Fatalf("next: %v", r.Next)
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

// TestOfflineSigningAndPlainHTTP: InsecureRegistry names the registry in a
// next step, so the Pacto resource reads its contract over plain HTTP, and is
// refused without delivery; NoTransparencyLog is refused without a golden
// path, the only contract init writes a publish section for.
func TestOfflineSigningAndPlainHTTP(t *testing.T) {
	r, err := Init(context.Background(), Options{Dir: fraudDir(t), Owner: "risk", Delivery: true, InsecureRegistry: true, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if !hasLine(r.Next, "the registry registry.example speaks plain HTTP: list it in the chart value insecureRegistries") {
		t.Fatalf("next: %v", r.Next)
	}
	checkNoClusterPieces(t, r)
	for _, dir := range []string{fraudDir(t), t.TempDir()} {
		if _, err := Init(context.Background(), Options{Dir: dir, Name: "p", NoTransparencyLog: true, Plan: true}, os.WriteFile); err == nil ||
			!strings.Contains(err.Error(), "transparencyLog goes under a golden path's publish.sign") {
			t.Fatalf("no transparency log without a golden path: %v", err)
		}
	}
	if _, err := Init(context.Background(), Options{Dir: fraudDir(t), InsecureRegistry: true, Plan: true}, os.WriteFile); err == nil || !strings.Contains(err.Error(), "needs delivery") {
		t.Fatalf("insecure without delivery: %v", err)
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
		{"unreadable .pactoignore", Options{Dir: writeTree(t, t.TempDir(), map[string]string{".pactoignore/x": ""}), Name: "x"}, ".pactoignore: "},
		{"bad name", Options{Dir: t.TempDir(), Name: "Bad_Name"}, "not a valid service name"},
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

	// A file where a folder must go is refused before anything is written.
	dir := writeTree(t, fraudDir(t), map[string]string{"delivery": "a file"})
	if _, err := Init(context.Background(), Options{Dir: dir, Delivery: true}, os.WriteFile); err == nil || !strings.Contains(err.Error(), "delivery is a file in") {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pacto.yaml")); err == nil {
		t.Fatal("a refused plan wrote pacto.yaml")
	}
	r := &Result{Dir: dir, Files: []File{{Path: "delivery/x.yaml", Action: Add}}}
	if err := apply(r, os.WriteFile); err == nil || !strings.Contains(err.Error(), "failed to create directory") {
		t.Fatalf("apply mkdir: %v", err)
	}

	// The file to append to went away between plan and apply.
	r = &Result{Dir: t.TempDir(), Files: []File{{Path: ".pactoignore", Action: Append, Content: "x\n"}}}
	if err := apply(r, os.WriteFile); err == nil {
		t.Fatal("appending to a missing file was not an error")
	}
}

func TestHelpers(t *testing.T) {
	if refRepo("oci://r.io/a/b:1.0.0") != "r.io/a/b" || refRepo("oci://r.io:5000/a@sha256:x") != "r.io:5000/a" {
		t.Error("refRepo")
	}
}

// TestManifestsOnly: an image name that is not a valid repository says
// nothing about the registry.
func TestManifestsOnly(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"k8s/app.yaml": "apiVersion: apps/v1\nkind: StatefulSet\nmetadata: {name: store}\nspec: {template: {spec: {containers: [{name: store, image: 'Reg.IO/Store:1.0.0'}]}}}\n",
	})
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	text := file(t, r, "pacto.yaml").Content
	if strings.Contains(text, "publish:") || !strings.HasPrefix(text, "# Written by pacto init from k8s/app.yaml.\n") || !hasLine(r.Unknown, "registry: no image") {
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

// TestPrivateRegistry: RegistrySecret names the registry login on the Pacto
// resource (spec.contractRef.pullSecretRef), and Next says to create it and
// to give delivery's promoter one; without delivery, or with a bad name, it
// is refused.
func TestPrivateRegistry(t *testing.T) {
	dir := fraudDir(t)
	r, err := Init(context.Background(), Options{Dir: dir, Owner: "risk", Delivery: true, RegistrySecret: "registry-creds", Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	checkNoClusterPieces(t, r)
	var res struct {
		Spec struct {
			ContractRef struct {
				PullSecretRef string `yaml:"pullSecretRef"`
			} `yaml:"contractRef"`
		}
	}
	if err := yaml.Unmarshal([]byte(file(t, r, "delivery/pacto.yaml").Content), &res); err != nil || res.Spec.ContractRef.PullSecretRef != "registry-creds" {
		t.Fatalf("the Pacto resource does not log in with registry-creds: %v\n%s", err, file(t, r, "delivery/pacto.yaml").Content)
	}
	if !hasLine(r.Next, "kubectl -n <namespace> create secret docker-registry registry-creds") || !hasLine(r.Next, "promoter.registryCredentials.secretName") {
		t.Fatalf("next: %v", r.Next)
	}
	for _, opts := range []Options{{Dir: dir, RegistrySecret: "registry-creds"}, {Dir: dir, Delivery: true, RegistrySecret: "Bad_Name"}} {
		opts.Plan = true
		if _, err := Init(context.Background(), opts, os.WriteFile); err == nil {
			t.Errorf("%+v was accepted", opts)
		}
	}
}

// TestHelmChartIsNotRead: a Helm chart's templates are never manifests (#432).
// Neither the header nor the workload come from a chart,
// whether its templates parse as YAML or not, and Unknown names the chart once.
// A plain manifest beside the chart is still read.
func TestHelmChartIsNotRead(t *testing.T) {
	chart := map[string]string{
		"chart/Chart.yaml":               "apiVersion: v2\nname: checkout\nversion: 0.1.0\n",
		"chart/values.yaml":              "image: ghcr.io/acme/checkout:1.0.0\nreplicas: 2\n",
		"chart/templates/configmap.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: checkout-config\ndata:\n  LOG_LEVEL: info\n",
		"chart/templates/deployment.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: \"{{ .Release.Name }}\"\nspec:\n" +
			"  replicas: \"{{ .Values.replicas }}\"\n  template:\n    spec:\n      containers:\n        - name: checkout\n          image: \"{{ .Values.image }}\"\n",
		"chart/templates/service.yaml": "apiVersion: v1\nkind: Service\nmetadata:\n  name: {{ include \"checkout.fullname\" . }}\nspec:\n  ports:\n    - port: 80\n",
	}
	with := func(extra map[string]string) map[string]string {
		m := maps.Clone(chart)
		maps.Copy(m, extra)
		return m
	}
	const docker = "FROM alpine:3.20\n"
	deploy := "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: checkout}\nspec: {template: {spec: {containers: [{name: checkout, image: 'ghcr.io/acme/checkout:1.2.0'}]}}}\n"
	for _, tc := range []struct {
		name, mode, header string
		chartDirs          []string
		files              map[string]string
		want               []string // in pacto.yaml
	}{
		{name: "beside a Dockerfile", mode: modePactify, header: "Dockerfile", chartDirs: []string{"chart"},
			files: with(map[string]string{"Dockerfile": docker})},
		{name: "chart only", mode: modeBlank, chartDirs: []string{"chart"}, files: chart},
		{name: "two charts are both named", mode: modePactify, header: "Dockerfile", chartDirs: []string{"chart", "worker"},
			files: with(map[string]string{"Dockerfile": docker, "worker/Chart.yaml": chart["chart/Chart.yaml"],
				"worker/templates/configmap.yaml": chart["chart/templates/configmap.yaml"]})},
		{name: "beside a plain manifest", mode: modePactify, header: "Dockerfile, k8s/deploy.yaml", chartDirs: []string{"chart"},
			files: with(map[string]string{"Dockerfile": docker, "k8s/deploy.yaml": deploy}),
			want:  []string{"workload: service"}},
		{name: "the folder is the chart", mode: modePactify, header: "Dockerfile", chartDirs: []string{"."},
			files: map[string]string{"Dockerfile": docker, "Chart.yaml": chart["chart/Chart.yaml"],
				"templates/configmap.yaml": chart["chart/templates/configmap.yaml"], "charts/db/templates/cm.yaml": chart["chart/templates/configmap.yaml"],
				"crds/crd.yaml": "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: widgets.acme.io\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeTree(t, filepath.Join(t.TempDir(), "checkout"), tc.files)
			r, err := Init(context.Background(), Options{Dir: dir, Registry: "ghcr.io/acme/checkout", Plan: true}, os.WriteFile)
			if err != nil {
				t.Fatal(err)
			}
			if r.Service != "checkout" || r.Mode != tc.mode {
				t.Fatalf("service %q, mode %q", r.Service, r.Mode)
			}
			for _, c := range tc.chartDirs {
				assertChartNamedOnce(t, r, c)
			}
			text := file(t, r, "pacto.yaml").Content
			assertNoChartRead(t, text, r.Found)
			if tc.header != "" && !strings.HasPrefix(text, "# Written by pacto init from "+tc.header+".\n") {
				t.Fatalf("header:\n%s", text)
			}
			for _, s := range tc.want {
				if !strings.Contains(text, s) {
					t.Errorf("pacto.yaml lacks %q:\n%s", s, text)
				}
			}
		})
	}
}

// assertNoChartRead fails when pacto.yaml or Found carries a chart template
// or CRD.
func assertNoChartRead(t *testing.T, text string, found []string) {
	t.Helper()
	if strings.Contains(text, "templates/") || strings.Contains(text, "Release.Name") || strings.Contains(text, "crds/") || hasLine(found, "templates/") {
		t.Fatalf("a chart template was read:\n%s\n%+v", text, found)
	}
}

// assertChartNamedOnce fails unless Found says exactly once that the chart in
// dir was read statically, and Unknown never asks to render it.
func assertChartNamedOnce(t *testing.T, r *Result, dir string) {
	t.Helper()
	n := 0
	for _, l := range r.Found {
		if strings.HasPrefix(l, "Helm chart "+dir+" read statically") {
			n++
		}
	}
	if n != 1 || hasLine(r.Unknown, "render it") {
		t.Fatalf("want chart %s read once in Found and never rendered, got %d: %q %q", dir, n, r.Found, r.Unknown)
	}
}

// TestOnlyClusterFilesAreManifests: tool configs written like Kubernetes
// objects (a Backstage catalog-info.yaml, a skaffold.yaml) are not manifests,
// and a file .pactoignore lists is not read (#433).
func TestOnlyClusterFilesAreManifests(t *testing.T) {
	const (
		catalog  = "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: checkout}\nspec: {type: service, owner: payments}\n"
		skaffold = "apiVersion: skaffold/v4beta11\nkind: Config\nmetadata: {name: checkout}\nmanifests: {rawYaml: [k8s/deploy.yaml]}\n"
		deploy   = "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: checkout}\nspec: {template: {spec: {containers: [{name: checkout, image: 'ghcr.io/acme/checkout:1.0.0'}]}}}\n"
		extra    = "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: extra}\n"
	)
	for _, tc := range []struct {
		name   string
		files  map[string]string
		header string
	}{
		{name: "tool configs beside a manifest", header: "Dockerfile, k8s/deploy.yaml",
			files: map[string]string{"Dockerfile": "FROM scratch\n", "catalog-info.yaml": catalog, "skaffold.yaml": skaffold, "k8s/deploy.yaml": deploy}},
		{name: "a manifest .pactoignore lists", header: "Dockerfile, k8s/deploy.yaml",
			files: map[string]string{"Dockerfile": "FROM scratch\n", "k8s/deploy.yaml": deploy, "k8s/extra.yaml": extra, "local/dev.yaml": extra,
				".pactoignore": "k8s/extra.yaml\nlocal/\n"}},
		{name: "an ignored Dockerfile", header: "k8s/deploy.yaml",
			files: map[string]string{"Dockerfile": "FROM scratch\n", "k8s/deploy.yaml": deploy, ".pactoignore": "Dockerfile\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeTree(t, filepath.Join(t.TempDir(), "checkout"), tc.files)
			r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
			if err != nil {
				t.Fatal(err)
			}
			text := file(t, r, "pacto.yaml").Content
			if !strings.HasPrefix(text, "# Written by pacto init from "+tc.header+".\n") {
				t.Fatalf("pacto.yaml:\n%s", text)
			}
			for _, s := range []string{"catalog-info.yaml", "skaffold.yaml", "extra.yaml", "local/"} {
				if strings.Contains(text, s) {
					t.Errorf("pacto.yaml names %s:\n%s", s, text)
				}
			}
		})
	}
}

// planBlank plans a blank contract in a new folder named svc.
func planBlank(t *testing.T, opts Options) (*Result, *contract.Contract) {
	t.Helper()
	opts.Dir, opts.Plan = filepath.Join(t.TempDir(), "svc"), true
	r, err := Init(context.Background(), opts, os.WriteFile)
	if err != nil || r.Mode != modeBlank {
		t.Fatalf("plan: %+v %v", r, err)
	}
	c, err := contract.Parse(strings.NewReader(file(t, r, "pacto.yaml").Content))
	if err != nil {
		t.Fatal(err)
	}
	return r, c
}

// TestBlankHonoursOwnerDomainRegistry: a blank contract carries the owner
// and domain it was given, at the current pactoVersion, with no publish
// section and so nothing to keep out of the bundle; the registry is pacto ci
// --publish's to resolve.
func TestBlankHonoursOwnerDomainRegistry(t *testing.T) {
	r, c := planBlank(t, Options{Owner: "billing", Domain: "payments", Registry: "oci://ghcr.io/acme/"})
	if c.PactoVersion != "2.6" || c.Service.Owner.Team != "billing" || c.Service.Domain != "payments" || c.Publish != nil {
		t.Fatalf("contract: %+v", c)
	}
	if slices.ContainsFunc(r.Files, func(f File) bool { return f.Path == ".pactoignore" }) {
		t.Fatalf("files: %+v", r.Files)
	}
	if hasLine(r.Unknown, "owner") || hasLine(r.Unknown, "registry") {
		t.Fatalf("unknown: %v", r.Unknown)
	}
	text := file(t, r, "pacto.yaml").Content
	for _, s := range []string{"dri:", "contacts:", "readiness", "metadata"} {
		if strings.Contains(text, s) {
			t.Errorf("pacto.yaml holds %q:\n%s", s, text)
		}
	}
}

// TestBlankWithoutOwnerIsUnknown: with no owner the contract says so in a
// comment, named in Unknown.
func TestBlankWithoutOwnerIsUnknown(t *testing.T) {
	r, c := planBlank(t, Options{})
	if c.PactoVersion != "2.6" || c.Service.Owner.Team != "" || c.Service.Domain != "" || c.Publish != nil {
		t.Fatalf("contract: %+v", c)
	}
	if !strings.Contains(file(t, r, "pacto.yaml").Content, "  # owner:\n") || !hasLine(r.Unknown, "owner: no team was given; pass --owner") {
		t.Fatalf("unknown: %v", r.Unknown)
	}
}

// TestBlankIsValid: every option combination writes a contract that
// validates.
func TestBlankIsValid(t *testing.T) {
	for _, owner := range []string{"", "billing"} {
		for _, domain := range []string{"", "payments"} {
			for _, describe := range []bool{false, true} {
				r, _ := planBlank(t, Options{Owner: owner, Domain: domain, DescribeOnly: describe})
				files := map[string]string{}
				for _, f := range r.Files {
					files[f.Path] = f.Content
				}
				if err := validate(files["pacto.yaml"], files); err != nil {
					t.Errorf("%q %q %v: %v", owner, domain, describe, err)
				}
			}
		}
	}
}

// TestBlankInvalidDomainFails: a domain the schema refuses fails before
// anything is planned.
func TestBlankInvalidDomainFails(t *testing.T) {
	_, err := Init(context.Background(), Options{Dir: filepath.Join(t.TempDir(), "svc"), Domain: "Not A Domain", Plan: true}, os.WriteFile)
	if err == nil || !strings.Contains(err.Error(), "is not valid") {
		t.Fatalf("err: %v", err)
	}
}

// TestPactifiedWritesDomain: a contract read from the folder carries the
// domain it was given.
func TestPactifiedWritesDomain(t *testing.T) {
	dir := writeTree(t, filepath.Join(t.TempDir(), "checkout"), map[string]string{"Dockerfile": "FROM alpine:3.20\n"})
	r, err := Init(context.Background(), Options{Dir: dir, Domain: "payments", Plan: true}, os.WriteFile)
	if err != nil || r.Mode != modePactify {
		t.Fatalf("plan: %+v %v", r, err)
	}
	c, err := contract.Parse(strings.NewReader(file(t, r, "pacto.yaml").Content))
	if err != nil || c.PactoVersion != "2.6" || c.Service.Domain != "payments" {
		t.Fatalf("contract: %+v %v", c, err)
	}
}

// TestInitDeliveryWritesNoEnvironments: the contract --delivery writes,
// blank or read from the folder, declares no environments and plans no
// values file: the promoter creates environments from the installation
// ladder. The contract still validates.
func TestInitDeliveryWritesNoEnvironments(t *testing.T) {
	blank, _ := planBlank(t, Options{Delivery: true, Domain: "payments"})
	fraud, err := Init(context.Background(), Options{Dir: fraudDir(t), Owner: "risk", Delivery: true, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	invoices, err := Init(context.Background(), Options{Dir: fixtureDir(t, "invoices"), Owner: "billing", Domain: "billing", Delivery: true, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []*Result{blank, fraud, invoices} {
		text := file(t, r, "pacto.yaml").Content
		if strings.Contains(text, "environments:") {
			t.Fatalf("pacto.yaml declares environments:\n%s", text)
		}
		checkNoClusterPieces(t, r)
	}
}

// TestInitDeliveryWritesNoStage: --delivery plans delivery/pacto.yaml and no
// delivery/<env>.yaml, and a contract already there gets no environment hint.
func TestInitDeliveryWritesNoStage(t *testing.T) {
	r, err := Init(context.Background(), Options{Dir: fraudDir(t), Owner: "risk", Delivery: true, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if file(t, r, "delivery/pacto.yaml").Action != Add {
		t.Fatalf("no Pacto resource: %+v", r.Files)
	}
	checkNoClusterPieces(t, r)
	text := "pactoVersion: \"2.3\"\nservice: {name: payments, version: 2.0.0}\n"
	dir := writeTree(t, t.TempDir(), map[string]string{"pacto.yaml": text})
	if r, err = Init(context.Background(), Options{Dir: dir, Delivery: true, Plan: true}, os.WriteFile); err != nil || hasLine(r.Next, "declares no environment") {
		t.Fatalf("plan: %+v %v", r, err)
	}
}

// TestPactifyFindsSpecAtAnyPath: the OpenAPI file a new contract names is the
// one pacto diff reads, at any path and under any name (Northwind's
// api/openapi.v1.yaml), never a file that only carries the name.
func TestPactifyFindsSpecAtAnyPath(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"Dockerfile":          "FROM scratch\n",
		"api/openapi.v1.yaml": "openapi: 3.1.0\npaths: {}\n",
		"openapi.yaml":        "title: not a spec\n",
	})
	r, err := Init(context.Background(), Options{Dir: dir, Name: "payments", Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if text := file(t, r, "pacto.yaml").Content; !strings.Contains(text, "    ref: api/openapi.v1.yaml\n") || !hasLine(r.Found, "API description api/openapi.v1.yaml") {
		t.Fatalf("pacto.yaml:\n%s\nfound: %q", text, r.Found)
	}
}

// TestPactifyPortUnknownFromDiscovery: the ports pacto init names when no
// OpenAPI file describes them are the ones discovery found, from a
// Dockerfile and the manifests alike, in numeric order.
func TestPactifyPortUnknownFromDiscovery(t *testing.T) {
	dir := writeTree(t, filepath.Join(t.TempDir(), "svc"), map[string]string{
		"Dockerfile": "FROM alpine:3\nEXPOSE 10000\n",
		"deploy.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: svc}\n" +
			"spec: {template: {spec: {containers: [{name: svc, image: ghcr.io/acme/svc:1.0.0, ports: [{containerPort: 9090}, {containerPort: 10000}]}]}}}\n",
	})
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(r.Unknown, "interfaces: svc listens on port 9090, 10000, but no OpenAPI file describes it") {
		t.Fatalf("unknown: %q", r.Unknown)
	}
}

// TestPactifiedKeepsTeamLabelOwner: a new contract's owner is the workload's
// team label, whatever CODEOWNERS or catalog-info.yaml say; they reach only
// the inferred record.
func TestPactifiedKeepsTeamLabelOwner(t *testing.T) {
	repo := writeTree(t, filepath.Join(t.TempDir(), "repo"), map[string]string{
		".git/HEAD":                       "ref: refs/heads/main\n",
		".github/CODEOWNERS":              "* @acme/owners\n",
		"payments/catalog-info.yaml":      "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: payments}\nspec: {owner: group:catalog-team}\n",
		"payments/Dockerfile":             "FROM alpine:3.20\n",
		"payments/deploy/deployment.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: payments\n  labels: {team: shop}\nspec: {template: {spec: {containers: [{name: payments, image: payments:1}]}}}\n",
	})
	r, err := Init(context.Background(), Options{Dir: filepath.Join(repo, "payments"), Plan: true}, os.WriteFile)
	if err != nil || r.Mode != modePactify {
		t.Fatalf("plan: %+v %v", r, err)
	}
	c, err := contract.Parse(strings.NewReader(file(t, r, "pacto.yaml").Content))
	if err != nil || c.Service.Owner.Team != "shop" || c.Service.Owner.DRI != "" {
		t.Fatalf("owner: %+v %v", c.Service.Owner, err)
	}
}
