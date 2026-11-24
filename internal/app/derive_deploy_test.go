package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// deployEnv is what publish hands the deploy build: the version and the
// image this publish built.
var deployEnv = []string{"PACTO_VERSION=1.2.3", "PACTO_REF_IMAGE=r.io/acme/payments", "PACTO_DIGEST_IMAGE=" + digest64}

const pinnedRef = "r.io/acme/payments@" + digest64

// deployOf derives the deploy artifact of a folder holding files, the image
// supplied when supplied is set.
func deployOf(t *testing.T, files map[string]string, supplied string) (*derived, error) {
	t.Helper()
	dir := t.TempDir()
	writeFiles(t, dir, files)
	return deployIn(t, dir, supplied)
}

func deployIn(t *testing.T, dir, supplied string) (*derived, error) {
	t.Helper()
	sc, err := discover.Dir(dir)
	if err != nil {
		t.Fatal(err)
	}
	bp, findings := discover.Build(dir, sc)
	d := &derived{pub: &contract.Publish{}, deploy: "r.io/acme/deploy/payments"}
	return d, d.addDeploy(dir, sc, bp, findings, supplied)
}

// buildDeploy runs the deploy builtin into a fresh directory.
func buildDeploy(t *testing.T, d *derived) (string, error) {
	t.Helper()
	out := t.TempDir()
	return out, d.builtins[d.pub.Deploy](t.Context(), out, deployEnv)
}

// deployed derives and builds the deploy artifact of files and returns every
// file it wrote, a chart's .tgz unpacked under its name.
func deployed(t *testing.T, files map[string]string, supplied string) (*derived, map[string]string) {
	t.Helper()
	d, err := deployOf(t, files, supplied)
	if err != nil {
		t.Fatal(err)
	}
	out, err := buildDeploy(t, d)
	if err != nil {
		t.Fatal(err)
	}
	return d, tree(t, out)
}

func tree(t *testing.T, out string) map[string]string {
	t.Helper()
	got := map[string]string{}
	_ = fs.WalkDir(os.DirFS(out), ".", func(p string, e fs.DirEntry, _ error) error {
		if e.IsDir() || strings.HasPrefix(p, "chart/") {
			return nil
		}
		raw, _ := fs.ReadFile(os.DirFS(out), p)
		if strings.HasSuffix(p, ".tgz") {
			for k, v := range untar(t, raw) {
				got[k] = v
			}
			return nil
		}
		got[p] = string(raw)
		return nil
	})
	return got
}

func untar(t *testing.T, tgz []byte) map[string]string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return got
		}
		if h.Typeflag == tar.TypeReg {
			raw, _ := io.ReadAll(tr)
			got[h.Name] = string(raw)
		}
	}
}

// manifestsFolder is a Dockerfile and manifests that deploy the image three
// ways, beside another image.
var manifestsFolder = map[string]string{
	"Dockerfile": "FROM scratch\n",
	"k8s/deploy.yaml": deployment("r.io/acme/payments:1.0") +
		"        - {name: side, image: r.io/acme/payments:1.0}\n        - name: proxy\n          image: \"r.io/acme/proxy:2\"\n",
	"k8s/job.yaml":     "apiVersion: batch/v1\nkind: Job\nmetadata: {name: migrate}\nspec:\n  template:\n    spec:\n      containers:\n        - name: m\n          image: \"r.io/acme/payments:1.0\"\n",
	"k8s/service.yaml": "apiVersion: v1\nkind: Service\nmetadata: {name: payments}\n",
}

func TestDeployManifestsPinsTheImage(t *testing.T) {
	t.Parallel()
	d, got := deployed(t, manifestsFolder, "")
	if d.pub.Deploy != contract.ArtifactManifests || d.pub.Artifacts[0].Ref != "oci://r.io/acme/deploy/payments" {
		t.Fatalf("%+v", d.pub)
	}
	dep, job := got["k8s/deploy.yaml"], got["k8s/job.yaml"]
	if strings.Count(dep, pinnedRef) != 2 || !strings.Contains(dep, `"r.io/acme/proxy:2"`) || !strings.Contains(job, `"`+pinnedRef+`"`) {
		t.Fatalf("deploy:\n%s\njob:\n%s", dep, job)
	}
	if got["k8s/service.yaml"] != manifestsFolder["k8s/service.yaml"] || len(got) != 3 {
		t.Fatalf("a file with no image is copied as written: %v", got)
	}
	_, got = deployed(t, manifestsFolder, "other.example/img@"+digest64)
	if !strings.Contains(got["k8s/deploy.yaml"], "image: other.example/img@"+digest64) {
		t.Fatalf("--image is pinned: %s", got["k8s/deploy.yaml"])
	}
}

func TestDeployManifestsUnpinnedFails(t *testing.T) {
	t.Parallel()
	// The image is written through an alias, which no rewrite can pin.
	aliased := "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: payments, annotations: {img: &img r.io/acme/payments:1.0}}\n" +
		"spec:\n  template:\n    spec:\n      containers:\n        - name: api\n          image: *img\n"
	d, err := deployOf(t, map[string]string{"Dockerfile": "FROM scratch\n", "k8s/deploy.yaml": aliased}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buildDeploy(t, d); err == nil || err.Error() != "deploy: no image: r.io/acme/payments line to pin in k8s/deploy.yaml" {
		t.Fatal(err)
	}
}

func TestDeployManifestsDeterministic(t *testing.T) {
	t.Parallel()
	var tgzs [][]byte
	for range 2 {
		d, err := deployOf(t, manifestsFolder, "")
		if err != nil {
			t.Fatal(err)
		}
		out, err := buildDeploy(t, d)
		if err != nil {
			t.Fatal(err)
		}
		tgz, err := oci.BundleToTarGz(os.DirFS(out))
		if err != nil {
			t.Fatal(err)
		}
		tgzs = append(tgzs, tgz)
	}
	if !bytes.Equal(tgzs[0], tgzs[1]) {
		t.Fatal("two builds of the same folder differ")
	}
}

// deployLeaks are the secret values the fixtures hold; none may ship.
var deployLeaks = []string{"hunter2", "u4l-s3cret", "v4lues-s3cret", "c0nfigmap-s3cret", "ign0red"}

func noLeak(t *testing.T, got map[string]string) {
	t.Helper()
	for p, v := range got {
		for _, l := range deployLeaks {
			if strings.Contains(v, l) {
				t.Fatalf("%s ships %s:\n%s", p, l, v)
			}
		}
	}
}

func TestDeployLeavesSecretsOut(t *testing.T) {
	t.Parallel()
	d, got := deployed(t, map[string]string{
		"Dockerfile":             "FROM scratch\n",
		"kustomization.yaml":     "resources: [deploy.yaml, secret.yaml, empty-secret.yaml, gen]\n",
		"deploy.yaml":            deployment("r.io/acme/payments:1.0"),
		"secret.yaml":            "apiVersion: v1\nkind: Secret\nmetadata: {name: db}\nstringData: {password: hunter2}\n",
		"empty-secret.yaml":      "apiVersion: v1\nkind: Secret\nmetadata: {name: tls}\n",
		"gen/kustomization.yaml": "secretGenerator:\n  - name: api\n    literals: [token=u4l-s3cret]\n    envs: [.env]\n",
		"gen/.env":               "PASSWORD=c0nfigmap-s3cret\n",
		"notes.yaml":             "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: n}\ndata: {note: ign0red}\n",
		".pactoignore":           "notes.yaml\n",
	}, "")
	noLeak(t, got)
	if _, ok := got["deploy.yaml"]; !ok || len(got) != 2 {
		t.Fatalf("only the kustomization and the deployment ship: %v", got)
	}
	for _, f := range []string{"secret.yaml", "empty-secret.yaml", "gen/kustomization.yaml"} {
		if !slices.ContainsFunc(d.notes, func(n string) bool { return strings.HasPrefix(n, f+" holds a Secret's values") }) {
			t.Fatalf("no note for %s: %v", f, d.notes)
		}
	}
	d, got = deployed(t, map[string]string{
		"Dockerfile":                   "FROM scratch\n",
		"chart/Chart.yaml":             "apiVersion: v2\nname: payments\nversion: 0.1.0\n",
		"chart/values.yaml":            "image:\n  repository: r.io/acme/payments\n  tag: \"1.0\"\n",
		"chart/values-prod.yaml":       "password: v4lues-s3cret\n",
		"chart/templates/deploy.yaml":  "kind: Deployment\n",
		"chart/templates/secret.yaml":  "apiVersion: v1\nkind: Secret\nmetadata: {name: db}\nstringData: {password: hunter2}\n",
		"chart/templates/wrapped.yaml": "{{- if .Values.secret }}\napiVersion: v1\nkind: Secret\nstringData:\n  password: hunter2\n{{- end }}\n",
		"chart/templates/debug.yaml":   "note: ign0red\n",
		"chart/secret-notes.txt":       "hunter2\n",
		"chart/ci/values.yaml":         "x: ign0red\n",
		"chart/.helmignore":            "# Helm\nsecret-notes.txt\nci/\n",
		".pactoignore":                 "chart/templates/debug.yaml\n",
	}, "")
	noLeak(t, got)
	if _, ok := got["payments/templates/deploy.yaml"]; !ok || len(got) != 4 || !slices.Contains(d.notes, "chart/values-prod.yaml holds per-environment values, so it is left out of the chart") {
		t.Fatalf("%v %v", got, d.notes)
	}
	for _, f := range []string{"chart/templates/secret.yaml", "chart/templates/wrapped.yaml"} {
		if !slices.ContainsFunc(d.notes, func(n string) bool { return strings.HasPrefix(n, f+" holds a Secret's values") }) {
			t.Fatalf("no note for %s: %v", f, d.notes)
		}
	}
}

func TestDeployRefusesSymlinks(t *testing.T) {
	t.Parallel()
	outside := filepath.Join(t.TempDir(), "secret.yaml")
	writeFiles(t, filepath.Dir(outside), map[string]string{"secret.yaml": deployment("r.io/acme/payments:1.0")})
	for name, files := range map[string]map[string]string{
		"k8s/link.yaml":          {"Dockerfile": "FROM scratch\n", "k8s/deploy.yaml": deployment("r.io/acme/payments:1.0")},
		"chart/.helmignore":      {"chart/Chart.yaml": "name: payments\n"},
		"chart/templates/x.yaml": {"chart/Chart.yaml": "name: payments\n"},
		"delivery/pacto.yaml":    {"k8s/deploy.yaml": deployment("r.io/acme/payments@" + digest64)},
	} {
		dir := t.TempDir()
		writeFiles(t, dir, files)
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		if err := os.Symlink(outside, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
		if _, err := deployIn(t, dir, ""); err == nil || err.Error() != "deploy: "+name+" is a symlink; Pacto copies regular files only" {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestDeployOutputSecretScanFails(t *testing.T) {
	t.Parallel()
	for file, files := range map[string]map[string]string{
		"k8s/deploy.yaml": {"k8s/deploy.yaml": deployment("r.io/acme/payments@"+digest64) + "          env:\n            - {name: DB_PASSWORD, value: hunter2}\n"},
		"k8s/cm.yaml": {"k8s/deploy.yaml": deployment("r.io/acme/payments@" + digest64),
			"k8s/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: c}\ndata: {LEVEL: info, API_KEY: abc123}\n"},
		"k8s/url.yaml": {"k8s/deploy.yaml": deployment("r.io/acme/payments@" + digest64),
			"k8s/url.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: u}\nbinaryData: {}\nother: {db: \"postgres://u:pw@db/x\"}\n"},
		"app.env": {"kustomization.yaml": "resources: [deploy.yaml]\nconfigMapGenerator:\n  - name: c\n    envs: [app.env]\n",
			"deploy.yaml": deployment("r.io/acme/payments@" + digest64), "app.env": "# a comment\nLEVEL=info\nplain\nTOKEN=\"abc\"\n"},
		"chart/values.yaml": {"chart/Chart.yaml": "name: payments\n", "chart/values.yaml": "auth:\n  password: v4lues-s3cret\n"},
	} {
		if _, err := deployOf(t, files, ""); err == nil || !strings.HasPrefix(err.Error(), "deploy: "+file+" sets a sensitive literal value") {
			t.Fatalf("%s: %v", file, err)
		}
	}
	host, reg := publishRegistry(t)
	dir := sectionless(t, "1.0.0", "", map[string]string{"k8s/deploy.yaml": deployment(host+"/acme/payments@"+digest64) + "          env:\n            - {name: DB_PASSWORD, value: hunter2}\n"})
	if _, err := publishService(&fakeRun{}, reg).Publish(t.Context(), signedOpts(t, dir, host)); err == nil || !strings.Contains(err.Error(), "k8s/deploy.yaml") {
		t.Fatal(err)
	}
	if tags, _ := reg.Tags(t.Context(), host+"/acme/payments"); len(tags) != 0 {
		t.Fatalf("published %v", tags)
	}
	_, got := deployed(t, map[string]string{"k8s/deploy.yaml": deployment("r.io/acme/payments@" + digest64),
		"k8s/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: c}\n"}, "")
	if len(got) != 2 {
		t.Fatalf("a ConfigMap with no data ships: %v", got)
	}
}

// kustomizeFolder is a kustomization that names files every way it can, and
// a file it does not name.
var kustomizeFolder = map[string]string{
	"Dockerfile": "FROM scratch\n",
	"kustomization.yaml": "resources: [deploy.yaml, base, base, \"github.com/acme/x//y?ref=v1\"]\ncomponents: [comp]\n" +
		"patches:\n  - path: patch.yaml\n  - patch: |-\n      kind: X\npatchesStrategicMerge:\n  - psm.yaml\n  - |-\n    kind: Y\n    a: b\n" +
		"configMapGenerator:\n  - name: c\n    files: [cfg=app.properties, other.txt]\n    envs: [app.env]\n    env: one.env\n" +
		"images:\n  - name: r.io/acme/payments\n    newTag: \"1.0\"\n  - name: r.io/acme/proxy\n    newTag: \"2\"\n",
	"deploy.yaml":             deployment("r.io/acme/payments:1.0"),
	"base/kustomization.yaml": "resources: [svc.yaml]\nimages:\n",
	"base/svc.yaml":           "apiVersion: v1\nkind: Service\nmetadata: {name: payments}\n",
	"comp/kustomization.yaml": "",
	"patch.yaml":              "kind: Deployment\n",
	"psm.yaml":                "kind: Deployment\n",
	"app.properties":          "a=b\n",
	"other.txt":               "x\n",
	"app.env":                 "LEVEL=info\n",
	"one.env":                 "MODE=x\n",
	"extra.yaml":              "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: extra}\n",
}

func TestDeployKustomizeOnlyReferencedFiles(t *testing.T) {
	t.Parallel()
	_, got := deployed(t, kustomizeFolder, "")
	want := []string{"app.env", "app.properties", "base/kustomization.yaml", "base/svc.yaml", "comp/kustomization.yaml",
		"deploy.yaml", "kustomization.yaml", "one.env", "other.txt", "patch.yaml", "psm.yaml"}
	var have []string
	for p := range got {
		have = append(have, p)
	}
	slices.Sort(have)
	if !slices.Equal(have, want) || got["deploy.yaml"] != kustomizeFolder["deploy.yaml"] {
		t.Fatalf("got %v\nwant %v", have, want)
	}
	for name, files := range map[string]map[string]string{
		"deploy: missing.yaml is missing from the folder or left out by .pactoignore: file does not exist": {"kustomization.yaml": "resources: [deploy.yaml]\npatches:\n  - path: missing.yaml\n"},
		"deploy: kustomization.yaml names ../up.yaml, outside the folder; Pacto deploys only the folder's files: declare publish": {
			"kustomization.yaml": "resources: [deploy.yaml]\npatches:\n  - path: ../up.yaml\n"},
		"deploy: nok/kustomization.yaml is missing from the folder or left out by .pactoignore: file does not exist": {"kustomization.yaml": "resources: [deploy.yaml, nok]\n", "nok/x.yaml": "a: b\n"},
	} {
		files["deploy.yaml"] = deployment("r.io/acme/payments@" + digest64)
		if _, err := deployOf(t, files, ""); err == nil || err.Error() != name {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestDeployKustomizeImagesEntry(t *testing.T) {
	t.Parallel()
	_, got := deployed(t, kustomizeFolder, "")
	root, base := got["kustomization.yaml"], got["base/kustomization.yaml"]
	entry := "  - name: r.io/acme/payments\n    digest: " + digest64 + "\n"
	if !strings.Contains(root, entry+"  - name: r.io/acme/proxy\n") || strings.Contains(root, `newTag: "1.0"`) || !strings.Contains(base, "images:\n"+entry) {
		t.Fatalf("root:\n%s\nbase:\n%s", root, base)
	}
	if got["comp/kustomization.yaml"] != "" {
		t.Fatalf("an empty kustomization stays empty: %q", got["comp/kustomization.yaml"])
	}
	_, got = deployed(t, map[string]string{"Dockerfile": "FROM scratch\n", "kustomization.yaml": "resources: [deploy.yaml]\n", "deploy.yaml": deployment("r.io/acme/payments:1.0")}, "other.example/img@"+digest64)
	if !strings.Contains(got["kustomization.yaml"], "images:\n  - name: r.io/acme/payments\n    newName: other.example/img\n    digest: "+digest64) {
		t.Fatalf("%s", got["kustomization.yaml"])
	}
	// kustomize matches the name as written: a Docker Hub short name is not
	// docker.io/..., or the deploy would keep running the old tag.
	_, got = deployed(t, map[string]string{"Dockerfile": "FROM scratch\n", "kustomization.yaml": "resources: [deploy.yaml]\n", "deploy.yaml": deployment("acme/payments:1.0")}, "docker.io/acme/payments@"+digest64)
	if !strings.Contains(got["kustomization.yaml"], "images:\n  - name: acme/payments\n    newName: docker.io/acme/payments\n    digest: "+digest64) {
		t.Fatalf("%s", got["kustomization.yaml"])
	}
}

// chartFolder is a Dockerfile and a chart.
var chartFolder = map[string]string{
	"Dockerfile":                  "FROM scratch\n",
	"chart/Chart.yaml":            "apiVersion: v2\nname: payments\nversion: 0.1.0\n",
	"chart/values.yaml":           "image:\n  repository: r.io/acme/old\n  tag: \"1.0\"\n",
	"chart/templates/deploy.yaml": "kind: Deployment\nimage: {{ .Values.image.repository }}:{{ .Values.image.tag }}\n",
}

func TestChartPackageDeterministic(t *testing.T) {
	t.Parallel()
	var tgzs []string
	for range 2 {
		d, err := deployOf(t, chartFolder, "")
		if err != nil {
			t.Fatal(err)
		}
		out, err := buildDeploy(t, d)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(out, "payments-1.2.3.tgz"))
		if err != nil {
			t.Fatal(err)
		}
		tgzs = append(tgzs, string(raw))
	}
	if tgzs[0] != tgzs[1] {
		t.Fatal("two packages of the same chart differ")
	}
}

func TestChartVersionIsTheServiceVersion(t *testing.T) {
	t.Parallel()
	_, got := deployed(t, chartFolder, "")
	if c := got["payments/Chart.yaml"]; c != "apiVersion: v2\nname: payments\nversion: 1.2.3\nappVersion: 1.2.3\n" {
		t.Fatalf("%q", c)
	}
}

func TestChartValuesDigestOrTag(t *testing.T) {
	t.Parallel()
	_, got := deployed(t, chartFolder, "")
	if v := got["payments/values.yaml"]; v != "image:\n  repository: r.io/acme/payments\n  tag: 1.2.3@"+digest64+"\n" {
		t.Fatalf("%q", v)
	}
	files := map[string]string{"Dockerfile": "FROM scratch\n", "chart/Chart.yaml": "name: payments\n", "chart/values.yaml": "image:\n  digest: \"\"\n"}
	if _, got = deployed(t, files, ""); got["payments/values.yaml"] != "image:\n  digest: "+digest64+"\n" {
		t.Fatalf("%q", got["payments/values.yaml"])
	}
	files["chart/values.yaml"] = "replicas: 1\n"
	if _, err := deployOf(t, files, ""); err == nil || err.Error() != "deploy: chart/values.yaml has no image: to pin the image this publish deploys in: declare publish" {
		t.Fatal(err)
	}
	files = map[string]string{"chart/Chart.yaml": "name: payments\n", "chart/templates/x.yaml": "kind: X\n"}
	if _, got = deployed(t, files, ""); len(got) != 2 || got["payments/Chart.yaml"] != "name: payments\nversion: 1.2.3\nappVersion: 1.2.3\n" {
		t.Fatalf("a chart with no image to pin keeps its values: %v", got)
	}
	files["chart/Chart.yaml"] = "version: 1\n"
	if _, err := deployOf(t, files, ""); err == nil || err.Error() != "deploy: chart/Chart.yaml names no chart" {
		t.Fatal(err)
	}
}

// TestChartIsTheDeployArtifact: a chart folder publishes its chart as the
// deploy artifact, annotated with the contract, and no applier object.
func TestChartIsTheDeployArtifact(t *testing.T) {
	t.Parallel()
	host, reg := publishRegistry(t)
	dir := sectionless(t, "1.0.0", "", chartFolder)
	res, err := publishService(&fakeRun{}, reg).withRun(&buildRun{}).Publish(t.Context(), signedOpts(t, dir, host))
	if err != nil || !strings.HasPrefix(res.Deploy, host+"/acme/deploy/payments@") || res.Plan.Items[1].Type != contract.ArtifactChart {
		t.Fatalf("%v %+v", err, res)
	}
	remote, err := reg.Head(t.Context(), res.Deploy)
	if err != nil || remote.Annotations[delivery.ContractAnnotation] != "oci://"+host+"/acme/payments@"+res.Digests["bundle"] {
		t.Fatalf("%v %+v", err, remote)
	}
	_, got := deployed(t, chartFolder, "")
	for p, v := range got {
		for _, applier := range []string{"HelmRelease", "OCIRepository", "kind: Kustomization"} {
			if strings.Contains(v, applier) {
				t.Fatalf("%s holds %s", p, applier)
			}
		}
	}
}

func TestDeployShipsPactoResource(t *testing.T) {
	t.Parallel()
	resource := map[string]string{"delivery/pacto.yaml": "kind: Pacto\nversion: @PACTO_VERSION@\n"}
	with := func(files map[string]string) map[string]string {
		out := map[string]string{}
		for _, m := range []map[string]string{files, resource} {
			for k, v := range m {
				out[k] = v
			}
		}
		return out
	}
	_, got := deployed(t, with(manifestsFolder), "")
	if got["pacto-resource.yaml"] != "kind: Pacto\nversion: 1.2.3\n" {
		t.Fatalf("%v", got)
	}
	_, got = deployed(t, with(map[string]string{"Dockerfile": "FROM scratch\n", "k8s/kustomization.yaml": "resources: [deploy.yaml]\n", "k8s/deploy.yaml": deployment("r.io/acme/payments:1.0")}), "")
	if got["k8s/pacto-resource.yaml"] != "kind: Pacto\nversion: 1.2.3\n" || !strings.HasPrefix(got["k8s/kustomization.yaml"], "resources: [deploy.yaml, pacto-resource.yaml]\n") {
		t.Fatalf("%v", got)
	}
	_, got = deployed(t, with(chartFolder), "")
	if got["payments/templates/pacto-resource.yaml"] != "kind: Pacto\nversion: 1.2.3\n" {
		t.Fatalf("%v", got)
	}
}

// TestDeployFindingsRefuse: a folder that shows several deploy sources
// derives none, and says why; a build-tool finding is not the deploy's.
func TestDeployFindingsRefuse(t *testing.T) {
	t.Parallel()
	_, err := deployOf(t, map[string]string{"Dockerfile": "FROM scratch\n", "chart/Chart.yaml": "name: payments\n", "k8s/deploy.yaml": deployment("r.io/acme/payments:1.0")}, "")
	if err == nil || err.Error() != "the deploy artifact cannot be assembled: finding: both chart (chart) and manifests (k8s/deploy.yaml) found; declare publish" {
		t.Fatal(err)
	}
	d := &derived{pub: &contract.Publish{}}
	if err := d.addDeploy(t.TempDir(), &discover.Scan{}, discover.BuildPlan{}, []contract.InferredFinding{{Message: "x; pass --image or declare publish"}}, ""); err != nil || d.pub.Deploy != "" {
		t.Fatal(err)
	}
}

func TestDeployFolderErrors(t *testing.T) {
	t.Parallel()
	if _, err := readDeploy(filepath.Join(t.TempDir(), "missing"), &discover.Scan{}, discover.DeploySource{}); err == nil {
		t.Fatal("a missing folder")
	}
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, ".pactoignore"), 0o755)
	if _, err := openFolder(dir); err == nil {
		t.Fatal("an unreadable .pactoignore")
	}
	dir = t.TempDir()
	writeFiles(t, dir, map[string]string{"chart/Chart.yaml": "name: payments\n", "chart/locked/x.yaml": "a: b\n"})
	_ = os.Chmod(filepath.Join(dir, "chart/locked"), 0o000)
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "chart/locked"), 0o755) })
	if _, err := readDeploy(dir, &discover.Scan{}, discover.DeploySource{Kind: discover.DeployChart, Paths: []string{"chart"}}); err == nil {
		t.Fatal("an unreadable chart folder")
	}
	file := filepath.Join(t.TempDir(), "file")
	writeFiles(t, filepath.Dir(file), map[string]string{"file": ""})
	if err := writeDeployTree(file, map[string][]byte{"a": nil}); err == nil {
		t.Fatal("writing under a file")
	}
	if err := writeChart(file, "c", map[string][]byte{}, false, "", "1.0.0"); err == nil {
		t.Fatal("packaging under a file")
	}
}
