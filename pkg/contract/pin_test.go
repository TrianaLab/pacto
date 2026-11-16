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
