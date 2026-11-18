package discover

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const dockerDir = "testdata/docker"

// readFixture reads dir as its own repository.
func readFixture(t *testing.T, dir string) Reading {
	t.Helper()
	r, err := readDir(dir, dir, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// field is the field named name, failing the test when there is none.
func field(t *testing.T, fields []contract.Inferred, name string) contract.Inferred {
	t.Helper()
	i := slices.IndexFunc(fields, func(f contract.Inferred) bool { return f.Field == name })
	if i < 0 {
		t.Fatalf("no field %s in %+v", name, fields)
	}
	return fields[i]
}

// writeFiles writes files, by slash path, under a new folder.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDockerExposePorts(t *testing.T) {
	r := readFixture(t, dockerDir)
	for name, want := range map[string]string{"ports[8080]": "8080/tcp", "ports[9090]": "9090/udp"} {
		f := field(t, r.Fields, name)
		if f.Value != want || f.Source != "Dockerfile:12" || f.Confidence != contract.ConfidenceLikely || f.Declare != "" {
			t.Errorf("%s = %+v", name, f)
		}
	}
	// The root Dockerfile outranks the deeper one on the same port; a port
	// only the deeper one exposes is its own.
	if f := field(t, r.Fields, "ports[8080]"); !slices.Equal(f.Outranks, []string{"tools/Dockerfile.dev:3"}) {
		t.Errorf("ports[8080] outranks %q", f.Outranks)
	}
	if f := field(t, r.Fields, "ports[2345]"); f.Source != "tools/Dockerfile.dev:3" {
		t.Errorf("ports[2345] = %+v", f)
	}
	if len(r.Findings) != 0 {
		t.Errorf("findings = %+v", r.Findings)
	}
}

func TestDockerExposeUnresolved(t *testing.T) {
	dir := writeFiles(t, map[string]string{"Dockerfile": "FROM x\nEXPOSE ${PORT} 8000-8010 70000 80/sctp 81/UDP\n"})
	r := readFixture(t, dir)
	if len(r.Findings) != 1 || r.Findings[0].Code != CodeUnresolved || r.Findings[0].Source != "Dockerfile:2" ||
		r.Findings[0].Message != "EXPOSE at Dockerfile:2 names a port through a variable or a range, so it is not read" {
		t.Errorf("findings = %+v", r.Findings)
	}
	if f := field(t, r.Fields, "ports[81]"); f.Value != "81/udp" {
		t.Errorf("ports[81] = %+v", f)
	}
}

func TestDockerHealthcheckHTTP(t *testing.T) {
	r := readFixture(t, dockerDir)
	f := field(t, r.Fields, "capabilities[health]")
	if f.Value != "http /healthz on 8080" || f.Source != "Dockerfile:11" || f.Declare != HealthYAML("/healthz", false) {
		t.Errorf("capabilities[health] = %+v", f)
	}
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Fields: r.Fields}
	if strings.Contains(string(rec.Marshal()), "tok3n") {
		t.Errorf("the record holds the health URL's query:\n%s", rec.Marshal())
	}
}

func TestDockerHealthcheckForms(t *testing.T) {
	for _, tc := range []struct{ line, value, path string }{
		{`HEALTHCHECK CMD ["curl", "-f", "http://127.0.0.1/ready"]`, "http /ready on 80", "/ready"},
		{`HEALTHCHECK CMD wget -qO- https://localhost`, "http / on 443", "/"},
		{`HEALTHCHECK CMD curl http://user:pw@localhost:9000//evil`, "http //evil on 9000", ""},
		{`HEALTHCHECK CMD curl http://localhost:9000/%zz`, "command", ""},
		{`HEALTHCHECK CMD /bin/check`, "command", ""},
	} {
		dir := writeFiles(t, map[string]string{"Dockerfile": "FROM x\n" + tc.line + "\n"})
		f := field(t, readFixture(t, dir).Fields, "capabilities[health]")
		if f.Value != tc.value || f.Declare != HealthYAML(tc.path, false) || strings.Contains(f.Value, "pw") {
			t.Errorf("%s: %+v", tc.line, f)
		}
	}
	dir := writeFiles(t, map[string]string{"Dockerfile": "FROM x\nHEALTHCHECK NONE\nHEALTHCHECK\n"})
	if r := readFixture(t, dir); len(r.Fields) != 0 {
		t.Errorf("HEALTHCHECK NONE gave %+v", r.Fields)
	}
}

func TestHealthcheckCommandNotRecorded(t *testing.T) {
	dir := writeFiles(t, map[string]string{"Dockerfile": "FROM x\nHEALTHCHECK CMD curl -H 'Authorization: Bearer x' http://api.internal/ready\n"})
	rec, err := CompleteIn(dir, dir, nil, contract.InferredInputs{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f := field(t, rec.Fields, "capabilities[health]"); f.Value != "command" {
		t.Errorf("capabilities[health] = %+v", f)
	}
	if out := string(rec.Marshal()); strings.Contains(out, "Bearer") || strings.Contains(out, "Authorization") || strings.Contains(out, "api.internal") {
		t.Errorf("the record holds the command:\n%s", out)
	}
}

func TestHealthProbeOutranksHealthcheck(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"Dockerfile": "FROM x\nHEALTHCHECK CMD curl -f http://localhost:8080/live\n",
		"deploy/app.yaml": `apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
spec:
  template:
    spec:
      containers:
        - name: app
          image: app:1
          readinessProbe:
            httpGet:
              path: /healthz
`,
	})
	rec, err := CompleteIn(dir, dir, nil, contract.InferredInputs{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := field(t, rec.Fields, "capabilities[health]")
	if f.Source != "deploy/app.yaml:11" || !slices.Equal(f.Outranks, []string{"Dockerfile:2"}) {
		t.Errorf("capabilities[health] = %+v", f)
	}
	var cited []string
	for _, s := range rec.Inputs.Sources {
		cited = append(cited, s.Path)
	}
	if !slices.Equal(cited, []string{"Dockerfile", "deploy/app.yaml"}) {
		t.Errorf("sources = %q", cited)
	}
	// A declared health capability leaves both out.
	rec, err = CompleteIn(dir, dir, &contract.Contract{Capabilities: []contract.Capability{{Type: "health"}}}, contract.InferredInputs{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(rec.Fields, func(f contract.Inferred) bool { return f.Field == "capabilities[health]" }) {
		t.Errorf("a declared health capability was inferred: %+v", rec.Fields)
	}
}

func TestDockerDescriptionLabel(t *testing.T) {
	r := readFixture(t, dockerDir)
	f := field(t, r.Fields, "service.summary")
	if f.Value != "Takes and tracks orders" || f.Source != "Dockerfile:8" || f.Declare != "service:\n  summary: 'Takes and tracks orders'\n" ||
		!slices.Equal(f.Outranks, []string{"tools/Dockerfile.dev:2"}) {
		t.Errorf("service.summary = %+v", f)
	}
	long := strings.Repeat("a", 201)
	dir := writeFiles(t, map[string]string{"Dockerfile": "FROM x\nLABEL org.opencontainers.image.description=" + long + " other=x org.opencontainers.image.description=\n"})
	if f := field(t, readFixture(t, dir).Fields, "service.summary"); f.Value != long || f.Declare != "" {
		t.Errorf("a summary over 200 characters declares %q", f.Declare)
	}
	// A declared summary leaves it out.
	rec, err := CompleteIn(dockerDir, dockerDir, &contract.Contract{Service: contract.Service{Summary: "Orders"}}, contract.InferredInputs{}, nil)
	if err != nil || slices.ContainsFunc(rec.Fields, func(f contract.Inferred) bool { return f.Field == "service.summary" }) {
		t.Errorf("a declared summary was inferred: %v %+v", err, rec)
	}
}

func TestDockerContinuationLines(t *testing.T) {
	dir := writeFiles(t, map[string]string{"build.Dockerfile": "FROM x\n# a comment\nEXPOSE 80 \\\n  443\n\\\n"})
	r := readFixture(t, dir)
	for _, name := range []string{"ports[80]", "ports[443]"} {
		if f := field(t, r.Fields, name); f.Source != "build.Dockerfile:3" {
			t.Errorf("%s = %+v", name, f)
		}
	}
}

func TestPortNeverWrittenToInterfaces(t *testing.T) {
	rec, err := CompleteIn(dockerDir, dockerDir, nil, contract.InferredInputs{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rec.Fields {
		if strings.HasPrefix(f.Field, "interfaces[") && strings.HasSuffix(f.Field, ".port") || strings.Contains(f.Declare, "port:") {
			t.Errorf("a port reaches an interface: %+v", f)
		}
		if strings.HasPrefix(f.Field, "ports[") && f.Declare != "" {
			t.Errorf("%s declares %q", f.Field, f.Declare)
		}
	}
	// A declared interface on the port leaves it out.
	rec, err = CompleteIn(dockerDir, dockerDir, &contract.Contract{Interfaces: []contract.Interface{{Name: "api", Port: 8080}, {Name: "x"}}}, contract.InferredInputs{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(rec.Fields, func(f contract.Inferred) bool { return f.Field == "ports[8080]" }) {
		t.Errorf("a declared port was inferred: %+v", rec.Fields)
	}
}

// EXPOSE with two variables, and a flow list of remote resources, name each
// line once.
func TestRepeatedFindingNamedOnce(t *testing.T) {
	r := readFixture(t, writeFiles(t, map[string]string{
		"Dockerfile":           "FROM scratch\nEXPOSE ${PORT} ${METRICS_PORT}\n",
		"k/kustomization.yaml": "resources: [https://a.example/x.yaml, https://a.example/x.yaml]\n",
	}))
	for _, code := range []string{CodeUnresolved, CodeUnsupportedFile} {
		if got := findingsOf(r, code); len(got) != 1 {
			t.Errorf("%s = %+v", code, got)
		}
	}
}
