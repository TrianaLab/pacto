package discover

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const composeDir = "testdata/compose/payments"

func TestComposeServiceByName(t *testing.T) {
	r := readFixture(t, composeDir)
	if f := field(t, r.Fields, "image"); f.Value != "ghcr.io/acme/payments:1.4.0" || f.Source != "compose.yaml:9" {
		t.Errorf("image = %+v", f)
	}
	// Nothing from the redis service.
	if slices.ContainsFunc(r.entries, func(e entry) bool {
		return strings.Contains(e.Value, "redis") || e.Field == "ports[6379]" || e.Field == "configuration.REDIS_PASSWORD"
	}) {
		t.Errorf("another service was read: %+v", r.entries)
	}
}

func TestComposeSingleService(t *testing.T) {
	r := readFixture(t, writeFiles(t, map[string]string{"docker-compose.yml": "services:\n  web:\n    image: web:1\n"}))
	if f := field(t, r.Fields, "image"); f.Value != "web:1" || f.Source != "docker-compose.yml:3" || f.Confidence != contract.ConfidenceLikely {
		t.Errorf("image = %+v", f)
	}
	// A Compose file below the folder root is not read.
	r = readFixture(t, writeFiles(t, map[string]string{"dev/compose.yaml": "services:\n  web:\n    image: web:1\n"}))
	if len(r.entries) != 0 {
		t.Errorf("entries = %+v", r.entries)
	}
}

func TestComposeAmbiguous(t *testing.T) {
	r := readFixture(t, writeFiles(t, map[string]string{"compose.yml": "services:\n  a:\n    image: a:1\n  b:\n    image: b:1\n"}))
	got := findingsOf(r, CodeAmbiguous)
	if len(r.entries) != 0 || len(got) != 1 || got[0].Source != "compose.yml:1" || !strings.Contains(got[0].Message, "a, b") {
		t.Errorf("entries = %+v, ambiguous = %+v", r.entries, got)
	}
}

func TestComposePortForms(t *testing.T) {
	r := readFixture(t, composeDir)
	for name, want := range map[string]string{
		"ports[8080]": "8080/tcp @ compose.yaml:11",
		"ports[80]":   "80/tcp @ compose.yaml:12",
		"ports[53]":   "53/udp @ compose.yaml:13",
		"ports[7000]": "7000/tcp @ compose.yaml:14",
	} {
		if f := field(t, r.Fields, name); f.Value+" @ "+f.Source != want || f.Declare != "" {
			t.Errorf("%s = %+v, want %s", name, f, want)
		}
	}
	// The host side and a range are never a container port.
	if slices.ContainsFunc(r.entries, func(e entry) bool {
		return slices.Contains([]string{"ports[9000]", "ports[5353]", "ports[17000]", "ports[8000]"}, e.Field)
	}) {
		t.Errorf("a host port was read: %+v", r.entries)
	}
	// A plain number and a long form without a protocol.
	r = readFixture(t, writeFiles(t, map[string]string{"compose.yaml": "services:\n  w:\n    ports:\n      - 8081\n      - target: 9091\n      - {published: 1}\n"}))
	if got := entriesOf(r, "ports[8081]"); !slices.Equal(got, []string{"8081/tcp @ compose.yaml:4"}) {
		t.Errorf("ports[8081] = %v", got)
	}
	if got := entriesOf(r, "ports[9091]"); !slices.Equal(got, []string{"9091/tcp @ compose.yaml:5"}) {
		t.Errorf("ports[9091] = %v", got)
	}
}

func TestPortRankManifestOverCompose(t *testing.T) {
	r := readFixture(t, writeFiles(t, map[string]string{
		"compose.yaml": "services:\n  w:\n    image: w:compose\n    ports: [\"8080\"]\n",
		"deploy.yaml":  "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: w}\nspec: {template: {spec: {containers: [{name: w, image: w:manifest, ports: [{containerPort: 8080}]}]}}}\n",
	}))
	if f := field(t, r.Fields, "ports[8080]"); f.Source != "deploy.yaml:4" || !slices.Equal(f.Outranks, []string{"compose.yaml:4"}) {
		t.Errorf("ports[8080] = %+v", f)
	}
	if f := field(t, r.Fields, "image"); f.Value != "w:compose" || !slices.Equal(f.Outranks, []string{"deploy.yaml:4"}) {
		t.Errorf("image = %+v", f)
	}
}

func TestComposeEnvKeysOnly(t *testing.T) {
	r := readFixture(t, composeDir)
	for name, want := range map[string]string{"configuration.DB_PASSWORD": "string, writeOnly @ compose.yaml:19", "configuration.LOG_LEVEL": "string @ compose.yaml:20"} {
		if f := field(t, r.Fields, name); f.Value+" @ "+f.Source != want {
			t.Errorf("%s = %+v, want %s", name, f, want)
		}
	}
	dir := writeFiles(t, map[string]string{"compose.yaml": "services:\n  w:\n    environment:\n      API_TOKEN: s3cr3t-values\n      PLAIN: \"\"\n"})
	r = readFixture(t, dir)
	if got := entriesOf(r, "configuration.API_TOKEN"); !slices.Equal(got, []string{"string, writeOnly @ compose.yaml:4"}) {
		t.Errorf("API_TOKEN = %v", got)
	}
	for _, d := range []string{composeDir, dir} {
		rec, err := CompleteIn(d, d, nil, contract.InferredInputs{})
		if err != nil || rec == nil {
			t.Fatalf("record = %v, %v", rec, err)
		}
		out := rec.Marshal()
		for _, leak := range []string{"hunter2", "s3cr3t-values"} {
			if bytes.Contains(out, []byte(leak)) {
				t.Errorf("the record holds %q:\n%s", leak, out)
			}
		}
	}
}

func TestComposeEnvFileUnsupported(t *testing.T) {
	r := readFixture(t, composeDir)
	got := findingsOf(r, CodeUnsupportedFile)
	if len(got) != 1 || got[0].Source != "compose.yaml:21" || !strings.Contains(got[0].Message, "env_file") {
		t.Errorf("unsupported = %+v", got)
	}
	// include and extends with a file name what Pacto does not follow.
	r = readFixture(t, writeFiles(t, map[string]string{"compose.yaml": "include:\n  - other.yaml\nservices:\n  w:\n    extends:\n      file: base.yaml\n      service: w\n"}))
	var sources []string
	for _, f := range findingsOf(r, CodeUnsupportedFile) {
		sources = append(sources, f.Source)
	}
	if !slices.Equal(sources, []string{"compose.yaml:1", "compose.yaml:6"}) {
		t.Errorf("unsupported = %v", r.Findings)
	}
}

func TestComposeShapes(t *testing.T) {
	for name, doc := range map[string]string{
		"empty":        "",
		"no services":  "name: x\n",
		"a list":       "- a\n",
		"no ports":     "services:\n  w:\n    ports: \"8080\"\n    environment: \"x\"\n",
		"extends here": "services:\n  w:\n    extends:\n      service: x\n",
	} {
		r := readFixture(t, writeFiles(t, map[string]string{"compose.yaml": doc}))
		if len(r.entries) != 0 || len(r.Findings) != 0 {
			t.Errorf("%s: entries = %+v, findings = %+v", name, r.entries, r.Findings)
		}
	}
}

// The service name the contract declares picks the Compose service and the
// catalog Component, not the folder's name.
func TestReadersPickByDeclaredName(t *testing.T) {
	root := writeFiles(t, map[string]string{
		"web/compose.yaml": "services:\n  web:\n    image: nginx:1.27\n    ports: [\"80:80\"]\n  storefront:\n    image: ghcr.io/acme/storefront:2.0\n    ports: [\"8080:8080\"]\n",
		"web/catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: storefront}\nspec: {owner: group:shop}\n---\n" +
			"apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: storefront-worker}\nspec: {owner: group:other}\n",
	})
	dir := root + "/web"
	rec, err := CompleteIn(dir, dir, &contract.Contract{Service: contract.Service{Name: "storefront"}}, contract.InferredInputs{})
	if err != nil || rec == nil {
		t.Fatalf("record = %v, %v", rec, err)
	}
	if f := field(t, rec.Fields, "image"); f.Value != "ghcr.io/acme/storefront:2.0" {
		t.Errorf("image = %+v", f)
	}
	if f := field(t, rec.Fields, "service.owner"); !strings.Contains(f.Value, "shop") {
		t.Errorf("owner = %+v", f)
	}
	if slices.ContainsFunc(rec.Fields, func(f contract.Inferred) bool { return f.Field == "ports[80]" }) {
		t.Errorf("the web service's port was read: %+v", rec.Fields)
	}
}
