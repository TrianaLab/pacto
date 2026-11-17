package discover

import (
	"bytes"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const (
	helmLockedDir   = "testdata/helm/locked"
	helmUnlockedDir = "testdata/helm/unlocked"
	helmValuesDir   = "testdata/helm/values"
	helmCredsDir    = "testdata/helm/creds"
	helmLockDigest  = "sha256:3f1b0c2d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f9"
)

// findingsOf are the findings of r with code.
func findingsOf(r Reading, code string) []contract.InferredFinding {
	var out []contract.InferredFinding
	for _, f := range r.Findings {
		if f.Code == code {
			out = append(out, f)
		}
	}
	return out
}

func TestChartLockedDependencyInputs(t *testing.T) {
	r := readFixture(t, helmLockedDir)
	want := []contract.InferredChartDependency{{Chart: "chart", Name: "redis", Version: "19.0.2", Repository: "https://charts.bitnami.com/bitnami", Digest: helmLockDigest}}
	if !slices.Equal(r.ChartDependencies, want) {
		t.Errorf("chart dependencies = %+v, want %+v", r.ChartDependencies, want)
	}
	if got := findingsOf(r, CodeChartDependencyUnlocked); len(got) != 0 {
		t.Errorf("findings = %+v", got)
	}
	rec, err := CompleteIn(helmLockedDir, helmLockedDir, nil, contract.InferredInputs{Pacto: "4.6.0"})
	if err != nil || rec == nil || !slices.Equal(rec.Inputs.ChartDependencies, want) {
		t.Fatalf("record = %+v, %v", rec, err)
	}
}

func TestUnlockedChartDependencyFinding(t *testing.T) {
	r := readFixture(t, helmUnlockedDir)
	want := []contract.InferredFinding{
		{Code: CodeChartDependencyUnlocked, Source: "chart/Chart.yaml:5", Message: "chart dependency 'postgresql' has no Chart.lock digest, not read"},
		{Code: CodeChartDependencyUnlocked, Source: "chart/Chart.yaml:8", Message: "chart dependency 'redis' has no Chart.lock digest, not read"},
	}
	if got := findingsOf(r, CodeChartDependencyUnlocked); !slices.Equal(got, want) {
		t.Errorf("findings = %+v, want %+v", got, want)
	}
	if len(r.ChartDependencies) != 0 {
		t.Errorf("chart dependencies = %+v", r.ChartDependencies)
	}
}

func TestChartLockMissingEntry(t *testing.T) {
	chart := "name: payments\ndependencies:\n  - name: postgresql\n    version: 15.x\n  - name: redis\n    version: 19.x\n"
	for name, lock := range map[string]string{
		"entry missing":  "dependencies:\n- name: postgresql\n  version: 15.1.0\ndigest: " + helmLockDigest + "\n",
		"digest missing": "dependencies:\n- name: postgresql\n  version: 15.1.0\n- name: redis\n  version: 19.0.2\n",
	} {
		r := readFixture(t, writeFiles(t, map[string]string{"Chart.yaml": chart, "Chart.lock": lock}))
		got := findingsOf(r, CodeChartDependencyUnlocked)
		switch name {
		case "entry missing":
			if len(got) != 1 || got[0].Source != "Chart.yaml:5" || !strings.Contains(got[0].Message, "'redis'") ||
				len(r.ChartDependencies) != 1 || r.ChartDependencies[0].Chart != "." || r.ChartDependencies[0].Version != "15.1.0" {
				t.Errorf("%s: findings %+v, dependencies %+v", name, got, r.ChartDependencies)
			}
		default:
			if len(got) != 2 || len(r.ChartDependencies) != 0 {
				t.Errorf("%s: findings %+v, dependencies %+v", name, got, r.ChartDependencies)
			}
		}
	}
}

func TestChartLockUnreadable(t *testing.T) {
	r := readFixture(t, writeFiles(t, map[string]string{"Chart.yaml": "name: x\ndependencies:\n  - name: redis\n", "Chart.lock": ": [\n"}))
	if got := findingsOf(r, CodeUnreadable); len(got) != 1 || got[0].Source != "Chart.lock" {
		t.Errorf("unreadable = %+v", got)
	}
	if got := findingsOf(r, CodeChartDependencyUnlocked); len(got) != 1 {
		t.Errorf("unlocked = %+v", got)
	}
}

// openLog is a folder that records every path opened in it.
type openLog struct {
	fs.FS
	opened *[]string
}

func (o openLog) Open(name string) (fs.File, error) {
	*o.opened = append(*o.opened, name)
	return o.FS.Open(name)
}

func TestChartDependencyNeverFetched(t *testing.T) {
	var opened []string
	r := Read(Input{Folder: openLog{FS: os.DirFS(helmLockedDir), opened: &opened}, FolderPath: ".", Name: "payments"})
	if slices.ContainsFunc(opened, func(p string) bool { return strings.HasSuffix(p, ".tgz") }) {
		t.Errorf("a vendored chart was opened: %q", opened)
	}
	if len(r.Findings) != 0 || len(r.ChartDependencies) != 1 {
		t.Errorf("findings %+v, dependencies %+v", r.Findings, r.ChartDependencies)
	}
}

func TestValuesImageAndPort(t *testing.T) {
	r := readFixture(t, helmValuesDir)
	img := field(t, r.Fields, "image")
	if img.Value != "ghcr.io/acme/payments:2.0.1" || img.Source != "chart/values.yaml:2" || img.Confidence != contract.ConfidenceLikely || img.Declare != "" {
		t.Errorf("image = %+v", img)
	}
	for name, want := range map[string]string{"ports[80]": "80/tcp @ chart/values.yaml:5", "ports[8080]": "8080/tcp @ chart/values.yaml:7", "ports[9090]": "9090/udp @ chart/values.yaml:9"} {
		if f := field(t, r.Fields, name); f.Value+" @ "+f.Source != want || f.Declare != "" {
			t.Errorf("%s = %+v, want %s", name, f, want)
		}
	}
	if slices.ContainsFunc(r.Fields, func(f contract.Inferred) bool { return strings.Contains(f.Value, "http") }) {
		t.Errorf("a named targetPort was read as a port: %+v", r.Fields)
	}
	for _, values := range []string{
		"image: ghcr.io/acme/plain:1\nservice: {port: 0}\ncontainerPort: '8080'\n",
		"image: {repository: ghcr.io/acme/plain}\nports: [{containerPort: 53, protocol: SCTP}]\n",
	} {
		got := readFixture(t, writeFiles(t, map[string]string{"Chart.yaml": "name: x\n", "values.yaml": values}))
		if f := field(t, got.Fields, "image"); !strings.HasPrefix(f.Value, "ghcr.io/acme/plain") || f.Source != "values.yaml:1" {
			t.Errorf("image = %+v", f)
		}
		if slices.ContainsFunc(got.Fields, func(f contract.Inferred) bool { return strings.HasPrefix(f.Field, "ports[") }) {
			t.Errorf("%q: ports read: %+v", values, got.Fields)
		}
	}
}

func TestValuesEnvKeysOnly(t *testing.T) {
	r := readFixture(t, helmValuesDir)
	got := map[string]string{}
	for _, f := range r.Fields {
		if strings.HasPrefix(f.Field, "configuration.") {
			got[f.Field] = f.Source + " " + strings.Join(f.Outranks, ",")
		}
	}
	want := map[string]string{
		"configuration.API_TOKEN":   "chart/values.yaml:15 ",
		"configuration.DB_PASSWORD": "chart/values.yaml:12 ",
		"configuration.LOG_LEVEL":   "chart/values.yaml:13 chart/values-prod.yaml:2",
		"configuration.REGION":      "chart/values-prod.yaml:3 ",
	}
	if len(got) != len(want) {
		t.Fatalf("configuration = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if f := field(t, r.Fields, "configuration.LOG_LEVEL"); f.Value != "string" || f.Declare != `"LOG_LEVEL": {"type":"string"}` {
		t.Errorf("LOG_LEVEL = %+v", f)
	}
}

func TestValuesSensitiveKeyWriteOnly(t *testing.T) {
	r := readFixture(t, helmValuesDir)
	for _, key := range []string{"DB_PASSWORD", "API_TOKEN"} {
		if f := field(t, r.Fields, "configuration."+key); f.Value != "string, writeOnly" || !strings.Contains(f.Declare, `"writeOnly":true`) {
			t.Errorf("%s = %+v", key, f)
		}
	}
}

func TestDiscoveryNeverRecordsEnvValues(t *testing.T) {
	rec, err := CompleteIn(helmValuesDir, helmValuesDir, nil, contract.InferredInputs{Pacto: "4.6.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(Reading{Findings: rec.Findings}, CodeUnreadable); len(got) != 1 || got[0].Source != "chart/values-broken.yaml" {
		t.Errorf("unreadable = %+v", got)
	}
	out := rec.Marshal()
	for _, leak := range []string{"hunter2", "s3cr3t-values", "eu-west-1", "warn"} {
		if bytes.Contains(out, []byte(leak)) {
			t.Errorf("the record holds %q:\n%s", leak, out)
		}
	}
	if _, err := contract.ParseInferredRecord(out); err != nil {
		t.Error(err)
	}
}

func TestChartDeeperThanTwoIgnored(t *testing.T) {
	r := readFixture(t, helmValuesDir)
	if slices.ContainsFunc(r.Fields, func(f contract.Inferred) bool { return strings.HasPrefix(f.Source, "a/") }) {
		t.Errorf("a chart three folders deep was read: %+v", r.Fields)
	}
	r = readFixture(t, writeFiles(t, map[string]string{"a/b/Chart.yaml": "name: x\n", "a/b/values.yaml": "image: ghcr.io/acme/two:1\n", "values.yaml": "image: ghcr.io/acme/nochart:1\n"}))
	if f := field(t, r.Fields, "image"); f.Value != "ghcr.io/acme/two:1" || len(f.Outranks) != 0 {
		t.Errorf("image = %+v", f)
	}
}

func TestValuesKeysRespectDeclaredFilter(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(helmValuesDir)); err != nil {
		t.Fatal(err)
	}
	writeTree(t, dir, map[string]string{"schema.json": `{"properties": {"LOG_LEVEL": {"type": "string"}}}`})
	for name, cfg := range map[string]contract.Configuration{
		"listed":   {Name: "env", Schema: "schema.json", Values: map[string]any{"API_TOKEN": "x"}},
		"unlisted": {Name: "base", Ref: "oci://reg/platform/base-config"},
	} {
		rec, err := CompleteIn(dir, dir, &contract.Contract{Configurations: []contract.Configuration{cfg}}, contract.InferredInputs{Pacto: "4.6.0"})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, f := range rec.Fields {
			if strings.HasPrefix(f.Field, "configuration.") {
				got = append(got, f.Field)
			}
		}
		want := map[string][]string{"listed": {"configuration.DB_PASSWORD", "configuration.REGION"}}[name]
		if !slices.Equal(got, want) {
			t.Errorf("%s: configuration = %q, want %q", name, got, want)
		}
	}
}
