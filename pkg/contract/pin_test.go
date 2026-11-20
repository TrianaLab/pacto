package contract_test

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// pinned pins raw and asserts the result parses with a root pactoVersion
// declared as Current().
func pinned(t *testing.T, raw string) string {
	t.Helper()
	out, err := contract.PinVersion([]byte(raw))
	if err != nil {
		t.Fatalf("%q: %v", raw, err)
	}
	c, err := contract.Parse(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("%q pinned to %q: %v", raw, out, err)
	}
	if err := contract.RequireDeclared(&contract.Contract{Service: contract.Service{Name: "a", Version: "1"}}, out); err != nil {
		t.Fatalf("%q pinned to %q: %v", raw, out, err)
	}
	if c.PactoVersion != contract.Current() {
		t.Fatalf("%q pinned to %q: pactoVersion %q", raw, out, c.PactoVersion)
	}
	return string(out)
}

var pinLine = `pactoVersion: "` + contract.Current() + `"`

func TestPinVersionAddsLine(t *testing.T) {
	got := pinned(t, "service:\n  name: a\n")
	if want := pinLine + "\nservice:\n  name: a\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := pinned(t, "  service:\n    name: a\n"); got != "  "+pinLine+"\n  service:\n    name: a\n" {
		t.Errorf("indented root: got %q", got)
	}
}

func TestPinVersionAfterDocumentMarker(t *testing.T) {
	for raw, want := range map[string]string{
		"---\nservice:\n  name: a\n":               "---\n" + pinLine + "\nservice:\n  name: a\n",
		"%YAML 1.1\n---\nservice:\n  name: a\n":    "%YAML 1.1\n---\n" + pinLine + "\nservice:\n  name: a\n",
		"# head\n--- # doc\nservice:\n  name: a\n": "# head\n--- # doc\n" + pinLine + "\nservice:\n  name: a\n",
		"---": "---\n" + pinLine + "\n",
		"# top\nservice:\n  name: a\n---\nx: 1\n": pinLine + "\n# top\nservice:\n  name: a\n---\nx: 1\n",
	} {
		if raw == "# top\nservice:\n  name: a\n---\nx: 1\n" {
			out, _ := contract.PinVersion([]byte(raw)) // a second document: Parse reads the first
			if string(out) != want {
				t.Errorf("%q: got %q", raw, out)
			}
			continue
		}
		if got := pinned(t, raw); got != want {
			t.Errorf("%q: got %q, want %q", raw, got, want)
		}
	}
}

// A root tag or anchor must stay on the contract's mapping: the pinned text is
// one document that still declares the service.
func TestPinVersionTaggedOrAnchoredRoot(t *testing.T) {
	for _, raw := range []string{
		"--- !!map\nservice:\n  name: a\n  version: 1.0.0\n",
		"--- &root\nservice:\n  name: a\n  version: 1.0.0\n",
		"---\n!!map\nservice:\n  name: a\n  version: 1.0.0\n",
	} {
		c, err := contract.Parse(strings.NewReader(pinned(t, raw)))
		if err != nil || c.Service.Name != "a" || c.PactoVersion != contract.Current() {
			t.Errorf("%q: got %+v, %v", raw, c, err)
		}
	}
}

func TestPinVersionKeepsDeclared(t *testing.T) {
	for _, raw := range []string{"pactoVersion: \"2.4\"\nservice:\n  name: a\n", `{"pactoVersion": "2.4"}`} {
		out, err := contract.PinVersion([]byte(raw))
		if err != nil || string(out) != raw {
			t.Errorf("%q: got %q, %v", raw, out, err)
		}
	}
}

func TestPinVersionEmpty(t *testing.T) {
	for raw, want := range map[string]string{
		"":            pinLine + "\n",
		"\n":          pinLine + "\n\n",
		"# comment\n": pinLine + "\n# comment\n",
		"null\n":      pinLine + "\n",
		"# c\n~\n":    "# c\n" + pinLine + "\n",
	} {
		if got := pinned(t, raw); got != want {
			t.Errorf("%q: got %q, want %q", raw, got, want)
		}
	}
}

func TestPinVersionFlowMapping(t *testing.T) {
	got := pinned(t, "{service: {name: a, version: 1.0.0}}")
	if want := "{" + pinLine + ", service: {name: a, version: 1.0.0}}\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPinVersionJSONRoot(t *testing.T) {
	got := pinned(t, `{"service": {"name": "a", "version": "1.0.0"}}`)
	if !strings.HasPrefix(got, "{"+pinLine+", ") {
		t.Errorf("got %q", got)
	}
}

func TestPinVersionCommentedKeyIsNotDeclared(t *testing.T) {
	for _, raw := range []string{
		"# pactoVersion: \"2.0\"\nservice:\n  name: a\n",
		"service:\n  name: a\n  pactoVersion: \"2.0\"\n",
	} {
		out, err := contract.PinVersion([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		var root yaml.Node
		if err := yaml.Unmarshal(out, &root); err != nil {
			t.Fatal(err)
		}
		if k := root.Content[0].Content[0]; k.Value != "pactoVersion" || root.Content[0].Content[1].Value != contract.Current() {
			t.Errorf("%q: got %q", raw, out)
		}
	}
}

func TestPinVersionCRLFAndBOM(t *testing.T) {
	got := pinned(t, "\ufeffservice:\r\n  name: a\r\n")
	if want := "\ufeff" + pinLine + "\r\nservice:\r\n  name: a\r\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	got = pinned(t, "\ufeff{service: {name: a}}\r\n")
	if !strings.HasPrefix(got, "\ufeff{") || !strings.HasSuffix(got, "}\r\n") {
		t.Errorf("flow: got %q", got)
	}
}

func TestPinVersionRefusesNonMapping(t *testing.T) {
	for _, raw := range []string{"[a]", "42", "{invalid"} {
		if _, err := contract.PinVersion([]byte(raw)); err == nil {
			t.Errorf("%q: expected an error", raw)
		}
	}
}

// TestPinServiceVersion: the derived version is declared under service, as a
// line where the text allows it and re-encoded otherwise; a declared version
// is left as written.
func TestPinServiceVersion(t *testing.T) {
	for raw, want := range map[string]string{
		"":                                        "service:\n  version: 1.4.3\n",
		"pactoVersion: \"2.5\"\n":                 "service:\n  version: 1.4.3\npactoVersion: \"2.5\"\n",
		"service:\n  name: x\n":                   "service:\n  version: 1.4.3\n  name: x\n",
		"service:\r\n    name: x\r\n":             "service:\r\n    version: 1.4.3\r\n    name: x\r\n",
		"\ufeffservice:\n  name: x\n":             "\ufeffservice:\n  version: 1.4.3\n  name: x\n",
		"service: {name: x}\n":                    "service: {name: x, version: 1.4.3}\n",
		"{service: {name: x}}":                    "{service: {name: x, version: 1.4.3}}\n",
		"{pactoVersion: \"2.5\"}":                 "{pactoVersion: \"2.5\", service: {version: 1.4.3}}\n",
		"service:\n":                              "service:\n  version: 1.4.3\n",
		"service:\n  name: x\n  version: 1.0.0\n": "service:\n  name: x\n  version: 1.0.0\n",
	} {
		out, err := contract.PinServiceVersion([]byte(raw), "1.4.3")
		if err != nil {
			t.Errorf("%q: %v", raw, err)
			continue
		}
		var doc struct {
			Service struct{ Version string } `yaml:"service"`
		}
		if err := yaml.Unmarshal(bytes.TrimPrefix(out, []byte("\ufeff")), &doc); err != nil {
			t.Errorf("%q pinned to %q: %v", raw, out, err)
		}
		if !strings.Contains(raw, "1.0.0") && doc.Service.Version != "1.4.3" {
			t.Errorf("%q pinned to %q: version %q", raw, out, doc.Service.Version)
		}
		if string(out) != want {
			t.Errorf("%q: got %q, want %q", raw, out, want)
		}
	}
	for _, raw := range []string{"- a\n", "service: [a]\n", "service: x\n", "a: [\n"} {
		if _, err := contract.PinServiceVersion([]byte(raw), "1.4.3"); err == nil {
			t.Errorf("%q: pinned", raw)
		}
	}
}

func TestPinDependencyField(t *testing.T) {
	cases := map[string]struct{ raw, want string }{
		"block": {"service: {name: reports}\ndependencies:\n  - name: ledger\n    required: true\n",
			"service: {name: reports}\ndependencies:\n  - name: ledger\n    compatibility: \">=2.3.0 <3\"\n    required: true\n"},
		"crlf":         {"dependencies:\r\n  - name: ledger\r\n", "dependencies:\r\n  - name: ledger\r\n    compatibility: \">=2.3.0 <3\"\r\n"},
		"declared":     {"dependencies:\n  - name: ledger\n    compatibility: \"^1.0.0\"\n", "dependencies:\n  - name: ledger\n    compatibility: \"^1.0.0\"\n"},
		"unknown":      {"dependencies:\n  - name: ledger\n    compatibility: unknown # pactified\n", "dependencies:\n  - name: ledger\n    compatibility: \">=2.3.0 <3\" # pactified\n"},
		"multibyte":    {"dependencies:\n  - {note: \"é\", name: ledger,   compatibility:   unknown}\n", "dependencies:\n  - {note: \"é\", name: ledger, compatibility: \">=2.3.0 <3\"}\n"},
		"quoted":       {"dependencies:\n  - name: ledger\n    compatibility: \"unknown\"\n", "dependencies:\n  - name: ledger\n    compatibility: \">=2.3.0 <3\"\n"},
		"flow unknown": {"dependencies:\n  - {name: ledger, compatibility: 'unknown'}\n", "dependencies:\n  - {name: ledger, compatibility: \">=2.3.0 <3\"}\n"},
		"flow":         {"dependencies:\n  - {name: ledger, required: true}\n", "dependencies:\n  - {name: ledger, required: true, compatibility: \">=2.3.0 <3\"}\n"},
		"no newline":   {"dependencies:\n  - name: ledger", "dependencies:\n  - name: ledger\n    compatibility: \">=2.3.0 <3\"\n"},
		"name later":   {"dependencies:\n  - required: true\n    name: ledger\n", "dependencies:\n  - required: true\n    compatibility: \">=2.3.0 <3\"\n    name: ledger\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := contract.PinDependencyField([]byte(tc.raw), "ledger", "compatibility", ">=2.3.0 <3")
			if err != nil || string(out) != tc.want {
				t.Fatalf("%v\n%q\nwant %q", err, out, tc.want)
			}
			var doc map[string]any
			if err := yaml.Unmarshal(out, &doc); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPinDependencyFieldMissingDependency(t *testing.T) {
	for _, raw := range []string{"service: {name: reports}\n", "dependencies:\n  - name: billing\n", "dependencies: {}\n", "- a\n", ""} {
		if _, err := contract.PinDependencyField([]byte(raw), "ledger", "ref", "oci://x"); err == nil || !strings.Contains(err.Error(), "no dependency ledger") {
			t.Fatalf("%q: %v", raw, err)
		}
	}
	if _, err := contract.PinDependencyField([]byte("a: [\n"), "ledger", "ref", "oci://x"); err == nil {
		t.Fatal("bad YAML pinned")
	}
}

// TestPinUnknownOverUnknownKeepsText: pinning unknown where unknown is
// declared changes nothing.
func TestPinUnknownOverUnknownKeepsText(t *testing.T) {
	raw := "dependencies:\n  - name: ledger\n    compatibility: unknown\n"
	if out, err := contract.PinDependencyField([]byte(raw), "ledger", "compatibility", contract.CompatibilityUnknown); err != nil || string(out) != raw {
		t.Fatalf("%v %q", err, out)
	}
}
