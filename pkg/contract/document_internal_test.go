package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// oldDocument is the decode validation used before Document: DecodeYAML, key
// conversion, then a json.Marshal / json.Unmarshal round trip.
func oldDocument(raw []byte) (any, error) {
	var v any
	if err := DecodeYAML(raw, &v); err != nil {
		return nil, err
	}
	var conv func(any) any
	conv = func(v any) any {
		switch v := v.(type) {
		case map[string]any:
			out := map[string]any{}
			for k, x := range v {
				out[k] = conv(x)
			}
			return out
		case map[any]any:
			out := map[string]any{}
			for k, x := range v {
				out[fmt.Sprintf("%v", k)] = conv(x)
			}
			return out
		case []any:
			out := make([]any, len(v))
			for i, x := range v {
				out[i] = conv(x)
			}
			return out
		}
		return v
	}
	b, err := json.Marshal(conv(v))
	if err != nil {
		return nil, err
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return out, nil
}

// sameAsBefore checks ParseDocument against Parse and Document against the
// old decode, error text included.
func sameAsBefore(t *testing.T, name string, raw []byte) {
	t.Helper()
	wantC, wantErr := Parse(bytes.NewReader(raw))
	gotC, doc, gotErr := ParseDocument(raw)
	// Compared as YAML, not DeepEqual: a NaN in Metadata is never DeepEqual to itself.
	gotY, _ := yaml.Marshal(gotC)
	wantY, _ := yaml.Marshal(wantC)
	if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || !bytes.Equal(gotY, wantY) || (gotC == nil) != (wantC == nil) {
		t.Errorf("%s: ParseDocument = %+v, %v; Parse = %+v, %v", name, gotC, gotErr, wantC, wantErr)
	}
	if gotErr == nil && doc == nil {
		t.Fatalf("%s: no Document for a parsed contract", name)
	}
	wantV, wantVErr := oldDocument(raw)
	gotV, gotVErr := DecodeDocument(raw).JSON()
	if fmt.Sprint(gotVErr) != fmt.Sprint(wantVErr) || !reflect.DeepEqual(gotV, wantV) {
		t.Errorf("%s: DecodeDocument = %#v, %v; before = %#v, %v", name, gotV, gotVErr, wantV, wantVErr)
	}
	if doc != nil {
		if v, err := doc.JSON(); fmt.Sprint(err) != fmt.Sprint(wantVErr) || !reflect.DeepEqual(v, wantV) {
			t.Errorf("%s: ParseDocument's Document = %#v, %v; before = %#v", name, v, err, wantV)
		}
	}
}

func TestParseDocument_MatchesEveryContractInTheRepo(t *testing.T) {
	n := 0
	_ = filepath.WalkDir("../..", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git") {
			return filepath.SkipDir
		}
		if d.Name() != "pacto.yaml" && !(strings.Contains(p, "testdata") && strings.HasSuffix(p, ".yaml")) {
			return nil
		}
		raw, _ := os.ReadFile(p)
		sameAsBefore(t, p, raw)
		n++
		return nil
	})
	if n < 40 {
		t.Fatalf("checked only %d documents", n)
	}
}

const base = "pactoVersion: \"2.0\"\nservice:\n  name: svc\n  version: 1.0.0\n"

func TestParseDocument_Edges(t *testing.T) {
	for name, raw := range map[string]string{
		"minimal":            base,
		"empty":              "",
		"invalid yaml":       "{invalid yaml",
		"unknown top":        base + "bogus: 1\n",
		"unknown nested":     base + "  bogus: 1\n",
		"unknown in list":    base + "interfaces:\n  - name: a\n    bogus: 1\n",
		"unknown in ptr":     base + "state:\n  bogus: 1\n",
		"type error":         base + "interfaces: 3\n",
		"list for a struct":  base + "state: [1]\n",
		"map for a list":     base + "interfaces:\n  a: 1\n",
		"missing version":    "pactoVersion: \"2.0\"\nservice:\n  name: svc\n",
		"duplicate key":      base + "workload: a\nworkload: b\n",
		"alias":              "x: &v svc\npactoVersion: \"2.0\"\nservice:\n  name: *v\n  version: 1.0.0\n",
		"merge":              "pactoVersion: \"2.0\"\nservice:\n  <<: {name: svc}\n  version: 1.0.0\n",
		"metadata merge":     base + "metadata:\n  <<: {a: 1}\n",
		"metadata alias":     base + "metadata:\n  a: &x {b: 1}\n  c: *x\n",
		"dates":              base + "metadata:\n  when: 2099-12-31\n  at: 2024-01-15T00:00:00Z\n",
		"numbers":            base + "metadata:\n  i: 42\n  big: 18446744073709551615\n  neg: -9223372036854775808\n  f: 1.5\n  z: -0.0\n",
		"nan":                base + "metadata:\n  v: .nan\n",
		"inf in list":        base + "metadata:\n  v: [.inf]\n",
		"nan in a map":       base + "metadata:\n  v: {w: .nan}\n",
		"nan non-string key": base + "metadata:\n  v: {1: .nan}\n",
		"non-string keys":    base + "metadata:\n  42: a\n  true: b\n",
		"binary":             base + "metadata:\n  b: !!binary /w==\n",
		"two documents":      base + "---\nnot: [valid\n",
		"scalar document":    "42",
	} {
		sameAsBefore(t, name, []byte(raw))
	}
}

type selfDecoding struct{ A string }

func (s *selfDecoding) UnmarshalYAML(n *yaml.Node) error { return nil }

type tagged struct {
	Plain   string
	Skipped string `yaml:"-"`
	private string //nolint:unused // proves private fields are not keys
}

type Embedded struct{}

type embedding struct {
	Embedded // anonymous: yaml.v3 rules not mirrored
}

type inline struct {
	M map[string]any `yaml:",inline"`
}

type holder struct {
	Self  selfDecoding         `yaml:"self"`
	Items []map[string]tagged  `yaml:"items"`
	Arr   [1]struct{ N int }   `yaml:"arr"`
	Any   any                  `yaml:"any"`
	Ptrs  map[string]*struct{} `yaml:"ptrs"`
}

func TestKnownFields(t *testing.T) {
	node := func(s string) *yaml.Node {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(s), &n); err != nil {
			t.Fatal(err)
		}
		return &n
	}
	ht := reflect.TypeOf(holder{})
	for s, want := range map[string]bool{
		"items: [{a: {}}]":                 true,
		"arr: [{n: 1}]":                    true,
		"arr: [{x: 1}]":                    false,
		"any: {whatever: 1}":               true,
		"ptrs: {a: {}}":                    true,
		"ptrs: {a: {x: 1}}":                false,
		"ptrs: {<<: {}}":                   false,
		"self: {anything: 1}":              false,
		"items: 3":                         true,
		"items: [{a: {plain: x}}]":         true,
		"items: [{a: {skipped: x}}]":       false,
		"items: [{a: {private: x}}]":       false,
		"items: [{a: {bare: x}}]":          false,
		"items: [{a: {embedded: x}}]":      false,
		"items: [{a: {plain: x}}, {b: 1}]": true,
	} {
		if got := knownFields(node(s), ht); got != want {
			t.Errorf("knownFields(%q) = %v, want %v", s, got, want)
		}
	}
	if knownFields(node("embedded: {}"), reflect.TypeOf(embedding{})) {
		t.Error("an anonymous field must make the check unsure")
	}
	if knownFields(node("m: 1"), reflect.TypeOf(inline{})) {
		t.Error("an inline field must make the check unsure")
	}
	doc := &yaml.Node{Kind: yaml.DocumentNode}
	if knownFields(doc, ht) {
		t.Error("a document with no content is not vouched for")
	}
}

type obsolete struct{}

func (*obsolete) UnmarshalYAML(func(any) error) error { return nil }

func TestKnownFields_ObsoleteUnmarshaler(t *testing.T) {
	var n yaml.Node
	_ = yaml.Unmarshal([]byte("a: 1"), &n)
	if knownFields(&n, reflect.TypeOf(obsolete{})) {
		t.Error("a type with the old UnmarshalYAML decodes itself")
	}
}

func TestToJSON_Numbers(t *testing.T) {
	for v, want := range map[any]float64{int(-3): -3, int64(-4): -4, uint64(5): 5} {
		if got, err := toJSON(v); err != nil || got != want {
			t.Errorf("toJSON(%T %v) = %v, %v", v, v, got, err)
		}
	}
}
