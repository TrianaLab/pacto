package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

// ordersSpec is an OpenAPI file whose GET /orders response requires total.
const ordersSpec = `openapi: 3.0.0
info: {title: orders, version: "1"}
paths:
  /orders:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                required: [id, total]
                properties:
                  id: {type: string}
                  total: {type: number}
`

// advisoryDirs writes orders 1.0.0 and 1.0.1 with the spec only inferred;
// the new one drops the required response field total.
func advisoryDirs(t *testing.T) (string, string) {
	t.Helper()
	write := func(version, spec string) string {
		dir := filepath.Join(t.TempDir(), "orders")
		mustWriteFile(t, filepath.Join(dir, "pacto.yaml"), "pactoVersion: \"2.5\"\nservice:\n  name: orders\n  version: \""+version+"\"\nworkload: service\n"+parityState)
		mustWriteFile(t, filepath.Join(dir, "openapi.yaml"), spec)
		return dir
	}
	dropped := strings.Replace(strings.Replace(ordersSpec, "[id, total]", "[id]", 1), "                  total: {type: number}\n", "", 1)
	return write("1.0.0", ordersSpec), write("1.0.1", dropped)
}

func TestDiffPrintsInferredAdvisory(t *testing.T) {
	statsOff(t)
	oldDir, newDir := advisoryDirs(t)
	text, _, err := runRoot(t, defaultDeps(), nil, "diff", oldDir, newDir)
	if err != nil || !strings.Contains(text, "Classification: NON_BREAKING") || !strings.Contains(text, "Possible breaks (inferred, advisory):") ||
		!strings.Contains(text, "  possible break (inferred) api openapi.yaml:1: ") || !strings.Contains(text, "    interfaces:\n      - name: api\n") {
		t.Fatalf("text (%v):\n%s", err, text)
	}
	md, _, err := runRoot(t, defaultDeps(), nil, "diff", oldDir, newDir, "--output-format", "markdown")
	if err != nil || !strings.Contains(md, "### Possible breaks (inferred, advisory)\n\n- possible break (inferred) `api openapi.yaml:1: ") || !strings.Contains(md, "```yaml\ninterfaces:\n") {
		t.Fatalf("markdown (%v):\n%s", err, md)
	}
	js, _, err := runRoot(t, defaultDeps(), nil, "diff", oldDir, newDir, "--output-format", "json")
	var res struct {
		Classification string
		Advisories     []struct{ Interface string }
	}
	if err != nil || json.Unmarshal([]byte(js), &res) != nil || res.Classification != "NON_BREAKING" || len(res.Advisories) != 1 || res.Advisories[0].Interface != "api" {
		t.Fatalf("json (%v):\n%s", err, js)
	}
}

func TestImpactRecordInferredAdvisoryCode(t *testing.T) {
	statsOff(t)
	oldDir, newDir := advisoryDirs(t)
	file := filepath.Join(t.TempDir(), "run.json")
	out, _, err := runRoot(t, defaultDeps(), nil, "impact", oldDir, newDir, "--local", t.TempDir(), "--stats-file", file)
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 2 || !strings.Contains(out, "Release gate: unverified\npossible break (inferred) orders api openapi.yaml:1: ") {
		t.Fatalf("impact (%v):\n%s", err, out)
	}
	data, _ := os.ReadFile(file)
	rec, err := runrecord.Parse(data)
	if err != nil || rec.Gate == nil || rec.Gate.Verdict != "unverified" || rec.Gate.Reason != "INFERRED_POSSIBLE_BREAK" || rec.Outcome != runrecord.OutcomeUnverified {
		t.Fatalf("record (%v): %s", err, data)
	}
}

// TestInferredAdvisoryParity: pacto impact, pacto_impact and GET
// /api/fleet/impact over one local fleet carry the same advisories and gate.
func TestInferredAdvisoryParity(t *testing.T) {
	t.Setenv("PACTO_NO_UPDATE_CHECK", "1")
	oldDir, newDir := advisoryDirs(t)
	root := t.TempDir()
	for _, f := range []string{"pacto.yaml", "openapi.yaml"} {
		data, err := os.ReadFile(filepath.Join(oldDir, f))
		expect(t, err == nil, "read: %v", err)
		mustWriteFile(t, filepath.Join(root, "orders", f), string(data))
	}
	mustWriteFile(t, filepath.Join(root, "web", "pacto.yaml"), "pactoVersion: \"2.5\"\nservice:\n  name: web\n  version: \"1.0.0\"\nworkload: service\n"+parityState+
		"dependencies:\n  - name: orders\n    ref: "+oldDir+"\n    compatibility: \"^1.0.0\"\n")
	flags := []string{"--local", root}
	cmd := newRootCommand(app.NewService(nil, nil), VersionInfo{Version: "test"}, defaultDeps())
	cmd.SetArgs(append([]string{"impact", oldDir, newDir, "--output-format", "json"}, flags...))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	var exit *ExitError
	expect(t, errors.As(cmd.Execute(), &exit) && exit.Code == 2, "pacto impact exits 2")
	plain, _, _ := paritySurfaces(t, flags)
	m, isErr := callText(t, plain, "pacto_impact", map[string]any{"old_ref": oldDir, "new_ref": newDir})
	expect(t, !isErr, "pacto_impact: %s", m)
	dash := dashboard.NewServer(dashboard.Options{Fleet: fixtureSource(t, flags), Impact: impactProviderForFleet(app.NewService(nil, nil), true, nil)}).APIHandler()
	d := routeBody(t, dash, context.Background(), "/api/fleet/impact?old="+url.QueryEscape(oldDir)+"&new="+url.QueryEscape(newDir))
	read := func(what, body string) any {
		var res struct {
			Gate       string `json:"gate"`
			Advisories any    `json:"advisories"`
		}
		expect(t, json.Unmarshal([]byte(body), &res) == nil, "%s: %s", what, body)
		expect(t, res.Gate == "unverified" && res.Advisories != nil, "%s: gate %s advisories %v", what, res.Gate, res.Advisories)
		return res.Advisories
	}
	cli, mcp, route := read("cli", out.String()), read("mcp", m), read("dashboard", d)
	expect(t, reflect.DeepEqual(cli, mcp) && reflect.DeepEqual(cli, route), "cli %v\nmcp %v\ndashboard %v", cli, mcp, route)
	expect(t, strings.Contains(out.String(), `"service": "web"`) || strings.Contains(out.String(), `"service":"web"`), "the advisory names web: %s", out.String())
}
