package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/lock"
)

// writeGoldenPathFixture writes a golden path (http-service) that depends on
// postgresql and names one plugin, a service created from it (gift-cards) and
// an ordinary service (billing), all read from local directories.
func writeGoldenPathFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	state := "state:\n  type: stateless\n  persistence:\n    scope: local\n    durability: ephemeral\n  dataCriticality: low\n"
	mustWrite(t, filepath.Join(root, "pg", "pacto.yaml"), "pactoVersion: \"2.4\"\nservice:\n  name: postgresql\n  version: \"16.1.0\"\n"+
		"  offering: postgresql\n  owner:\n    team: infra-data\nworkload: service\n"+state)
	path := filepath.Join(root, "http-service")
	mustWrite(t, filepath.Join(path, "pacto.yaml"), "pactoVersion: \"2.4\"\nservice:\n  name: http-service\n  version: \"1.0.0\"\n"+
		"  owner:\n    team: platform\n  summary: An HTTP API with a PostgreSQL database\n"+
		"  template:\n    plugins:\n      - name: dockerfile\n        description: A Dockerfile for a Go HTTP service\n"+
		"dependencies:\n  - name: postgresql\n    ref: oci://ghcr.io/acme/postgresql\n    required: true\n    compatibility: ^16.0.0\n"+
		"workload: service\n"+state)
	digest, err := lock.HashFS(os.DirFS(path))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "gift-cards", "pacto.yaml"), "pactoVersion: \"2.4\"\nservice:\n  name: gift-cards\n  version: \"0.1.0\"\n"+
		"  owner:\n    team: checkout\n  origin:\n    ref: oci://ghcr.io/acme/http-service:1.0.0\n    digest: \""+digest+"\"\n"+
		"workload: service\n"+state)
	mustWrite(t, filepath.Join(root, "billing", "pacto.yaml"), "pactoVersion: \"2.4\"\nservice:\n  name: billing\n  version: \"1.0.0\"\n"+
		"workload: service\n"+state)
	return root
}

func TestFleetEntitiesTemplateFlag(t *testing.T) {
	root := writeGoldenPathFixture(t)
	for _, tc := range []struct {
		flags      []string
		want, deny []string
	}{
		{[]string{"--template"}, []string{"1 of 1 entit(ies):", "  golden path ", " http-service "}, nil},
		{[]string{"--template", "--output-format", "json"}, []string{`"asOf"`, `"template": true`, `"created": 1`}, nil},
		{[]string{"--template=exclude", "--kind", "service"}, []string{" gift-cards ", " billing "}, []string{"http-service"}},
		{[]string{"--created-from", "http-service"}, []string{"1 of 1 entit(ies):", " gift-cards "}, nil},
	} {
		out, _, err := execFleet(t, append([]string{"fleet", "entities", "--local", root}, tc.flags...)...)
		if err != nil {
			t.Errorf("entities %v: %v", tc.flags, err)
		}
		for _, w := range tc.want {
			if !strings.Contains(out, w) {
				t.Errorf("entities %v lacks %q:\n%s", tc.flags, w, out)
			}
		}
		for _, d := range tc.deny {
			if strings.Contains(out, d) {
				t.Errorf("entities %v names %q:\n%s", tc.flags, d, out)
			}
		}
	}
	if _, _, err := execFleet(t, "fleet", "entities", "--local", root, "--template=bogus"); err == nil {
		t.Error("an unknown --template value is an error")
	}
}

func TestPrintFleetEntity_GoldenPath(t *testing.T) {
	root := writeGoldenPathFixture(t)
	out, _, err := execFleet(t, "fleet", "entity", "service", "http-service", "--local", root)
	if err != nil {
		t.Fatalf("entity: %v", err)
	}
	for _, want := range []string{
		"Golden path: An HTTP API with a PostgreSQL database\n",
		"Signature: unknown: no source that read this revision checks signatures\n  The signer is the one",
		"Plugins:\n  dockerfile: A Dockerfile for a Go HTTP service\n",
		"Requirements:\n  http-service -> postgresql (postgresql) via dependency, range satisfied\n",
		"Created services:\n  gift-cards\n",
		"Create a service: pacto init <name> --from " + filepath.Join(root, "http-service") + " --owner <team>\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("golden path text lacks %q:\n%s", want, out)
		}
	}
}

func TestPrintFleetEntity_Origin(t *testing.T) {
	root := writeGoldenPathFixture(t)
	out, _, err := execFleet(t, "fleet", "entity", "service", "gift-cards", "--local", root)
	if err != nil {
		t.Fatalf("entity: %v", err)
	}
	for _, want := range []string{
		"Created from: oci://ghcr.io/acme/http-service:1.0.0 (sha256:",
		"), declared\n  golden path: http-service 1.0.0\n",
		"Creation:\n",
		"  unknown         signature          no source that read this revision checks signatures\n",
		"  done            contract           revision 0.1.0 read from local\n",
		"  unknown         plugin dockerfile  Pacto does not observe what a plugin wrote\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("origin text lacks %q:\n%s", want, out)
		}
	}
}

func TestFleetSearchTemplateFlag(t *testing.T) {
	root := writeGoldenPathFixture(t)
	out, _, err := execFleet(t, "fleet", "search", "--local", root, "--template")
	if err != nil || !strings.Contains(out, "1 of 1 service(s):") || !strings.Contains(out, "http-service") {
		t.Errorf("search --template = %v:\n%s", err, out)
	}
	out, _, err = execFleet(t, "fleet", "search", "--local", root, "--template=exclude")
	if err != nil || strings.Contains(out, "http-service") || !strings.Contains(out, "gift-cards") {
		t.Errorf("search --template=exclude = %v:\n%s", err, out)
	}
}

func TestPrintFleetSearch_MarksGoldenPath(t *testing.T) {
	root := writeGoldenPathFixture(t)
	out, _, err := execFleet(t, "fleet", "search", "--local", root)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for line := range strings.Lines(out) {
		if strings.Contains(line, "http-service") != strings.Contains(line, "golden path") {
			t.Errorf("only the golden path row is marked: %q\n%s", line, out)
		}
	}
}
