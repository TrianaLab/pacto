package discover

import (
	"context"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/diff"
)

const (
	specKindsDir = "testdata/specs/kinds"
	notSpecsDir  = "testdata/specs/notspecs"
)

// interfaceFields are the interfaces[...] fields of fields.
func interfaceFields(fields []contract.Inferred) []contract.Inferred {
	var out []contract.Inferred
	for _, f := range fields {
		if strings.HasPrefix(f.Field, "interfaces[") {
			out = append(out, f)
		}
	}
	return out
}

func TestSpecAnyPath(t *testing.T) {
	f := field(t, readFixture(t, specKindsDir).Fields, "interfaces[api]")
	if f.Value != "openapi api/openapi.v1.yaml" || f.Source != "api/openapi.v1.yaml:2" || f.Confidence != contract.ConfidenceLikely ||
		f.Declare != "interfaces:\n  - name: api\n    type: openapi\n    ref: api/openapi.v1.yaml\n" {
		t.Errorf("api = %+v", f)
	}
}

func TestSpecPerKindNames(t *testing.T) {
	got := map[string]string{}
	for _, f := range interfaceFields(readFixture(t, specKindsDir).Fields) {
		got[f.Field] = f.Value + " @ " + f.Source
	}
	want := map[string]string{
		"interfaces[api]":    "openapi api/openapi.v1.yaml @ api/openapi.v1.yaml:2",
		"interfaces[events]": "asyncapi events/asyncapi.yaml @ events/asyncapi.yaml:1",
		"interfaces[grpc]":   "grpc proto/payments.proto @ proto/payments.proto:5",
	}
	if !maps.Equal(got, want) {
		t.Errorf("interfaces = %v, want %v", got, want)
	}
}

func TestSpecLineOfServiceAfterABlock(t *testing.T) {
	// A service on the line another block closes on is still a service, at
	// no line of its own.
	if got := specLine("grpc", []byte("message A {} service B { rpc C (A) returns (A); }\n")); got != 1 {
		t.Errorf("line = %d", got)
	}
	if got := specLine("openapi", []byte("- a\n")); got != 1 {
		t.Errorf("a list = %d", got)
	}
}

func TestAmbiguousSpecNoEntry(t *testing.T) {
	r := Read(Input{Folder: fstest.MapFS{
		"api/openapi.v1.yaml": {Data: []byte("openapi: 3.1.0\n")},
		"api/openapi.v2.yaml": {Data: []byte("openapi: 3.1.0\n")},
		"docs/petstore.yaml":  {Data: []byte("swagger: '2.0'\n")},
		"events.yaml":         {Data: []byte("asyncapi: 3.0.0\n")},
	}, FolderPath: "."})
	if got := interfaceFields(r.Fields); len(got) != 1 || got[0].Field != "interfaces[events]" {
		t.Errorf("interfaces = %+v", got)
	}
	want := []contract.InferredFinding{{Code: CodeAmbiguous, Source: "api/openapi.v1.yaml",
		Message: "3 OpenAPI files: api/openapi.v1.yaml, api/openapi.v2.yaml, docs/petstore.yaml; declare the one this service serves"}}
	if !slices.Equal(r.Findings, want) {
		t.Errorf("findings = %+v", r.Findings)
	}
}

func TestGraphQLUnsupported(t *testing.T) {
	r := readFixture(t, specKindsDir)
	want := []contract.InferredFinding{{Code: CodeUnsupportedFile, Source: "schema.graphql", Message: "schema.graphql is not read: GraphQL is not a Pacto interface type"}}
	if !slices.Equal(r.Findings, want) {
		t.Errorf("findings = %+v", r.Findings)
	}
	for _, name := range []string{"a.gql", "b.graphqls"} {
		if r := Read(Input{Folder: fstest.MapFS{name: {}}, FolderPath: "."}); len(r.Findings) != 1 || r.Findings[0].Code != CodeUnsupportedFile {
			t.Errorf("%s: %+v", name, r.Findings)
		}
	}
}

func TestSpecIgnoredByPactoignore(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"api/openapi.yaml":  "openapi: 3.1.0\n",
		"docs/openapi.yaml": "openapi: 3.1.0\n",
		".pactoignore":      "docs/\n",
	})
	r := readFixture(t, dir)
	if got := interfaceFields(r.Fields); len(got) != 1 || got[0].Value != "openapi api/openapi.yaml" || len(r.Findings) != 0 {
		t.Errorf("interfaces = %+v, findings = %+v", got, r.Findings)
	}
}

func TestSpecOverSizeUnreadable(t *testing.T) {
	big := "openapi: 3.1.0\nx: '" + strings.Repeat("a", maxFileSize) + "'\n"
	r := readFixture(t, writeFiles(t, map[string]string{"openapi.yaml": big, "api.json": big}))
	if len(interfaceFields(r.Fields)) != 0 {
		t.Errorf("an oversize spec was read: %+v", r.Fields)
	}
	want := []contract.InferredFinding{
		{Code: CodeUnreadable, Source: "api.json", Message: "api.json is over 1 MiB, so it is not read"},
		{Code: CodeUnreadable, Source: "openapi.yaml", Message: "openapi.yaml is over 1 MiB, so it is not read"},
	}
	if !slices.Equal(r.Findings, want) {
		t.Errorf("findings = %+v", r.Findings)
	}
}

func TestDiscoveryRespectsDeclaredFilter(t *testing.T) {
	for _, iface := range []contract.Interface{
		{Name: "http", Type: "openapi", Ref: "api/openapi.v1.yaml"},
		{Name: "api", Type: "openapi", Ref: "other.yaml"},
	} {
		rec, err := CompleteIn(specKindsDir, specKindsDir, &contract.Contract{Interfaces: []contract.Interface{iface}}, contract.InferredInputs{})
		if err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(rec.Fields, func(f contract.Inferred) bool { return f.Field == "interfaces[api]" }) {
			t.Errorf("declared %+v, yet interfaces[api] was inferred", iface)
		}
		if !slices.ContainsFunc(rec.Fields, func(f contract.Inferred) bool { return f.Field == "interfaces[events]" }) {
			t.Errorf("declared %+v hid interfaces[events]", iface)
		}
	}
}

// removeOne removes the first path, channel or rpc of the spec data of kind,
// and names it; ok is false when the spec has none Pacto can find.
func removeOne(kind string, data []byte) (out []byte, removed string, ok bool) {
	if kind == "grpc" {
		rpc := regexp.MustCompile(`(?m)^\s*rpc\s+(\w+)[^\n]*\n`)
		m := rpc.FindSubmatchIndex(data)
		if m == nil {
			return nil, "", false
		}
		return append(slices.Clone(data[:m[0]]), data[m[1]:]...), string(data[m[2]:m[3]]), true
	}
	var doc map[string]any
	_ = yaml.Unmarshal(data, &doc)
	section, _ := doc[map[string]string{"openapi": "paths", "asyncapi": "channels"}[kind]].(map[string]any)
	if len(section) == 0 {
		return nil, "", false
	}
	removed = slices.Sorted(maps.Keys(section))[0]
	delete(section, removed)
	out, _ = yaml.Marshal(doc)
	return out, removed, true
}

// TestDiscoveredSpecsAreDiffable: every spec discovery names is one pacto diff
// reads, so removing a path, a channel or an rpc from it is a change. A file
// that only looks like a spec (notspecs) is never named.
func TestDiscoveredSpecsAreDiffable(t *testing.T) {
	for _, dir := range []string{specKindsDir, notSpecsDir} {
		for _, f := range interfaceFields(readFixture(t, dir).Fields) {
			kind, ref, _ := strings.Cut(f.Value, " ")
			data, err := os.ReadFile(dir + "/" + ref)
			if err != nil {
				t.Fatal(err)
			}
			changed, removed, ok := removeOne(kind, data)
			if !ok {
				t.Errorf("%s: discovery named %s, which pacto diff cannot read", dir, ref)
				continue
			}
			c := &contract.Contract{Interfaces: []contract.Interface{{Name: "x", Type: kind, Ref: ref}}}
			res := diff.Compare(context.Background(), c, c, fstest.MapFS{ref: {Data: data}}, fstest.MapFS{ref: {Data: changed}})
			if !slices.ContainsFunc(res.Changes, func(ch diff.Change) bool { return strings.Contains(ch.Path, removed) }) {
				t.Errorf("%s: removing %s from %s changed nothing: %+v", dir, removed, ref, res.Changes)
			}
		}
	}
}

// A contract that declares one of several OpenAPI files has chosen: the
// ambiguity is not reported. Declaring another file keeps it.
func TestAmbiguousSpecDeclaredRefChoosesOne(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"api/openapi.v1.yaml": "openapi: 3.1.0\n",
		"api/openapi.v2.yaml": "openapi: 3.1.0\n",
	})
	for ref, want := range map[string]int{"api/openapi.v2.yaml": 0, "other.yaml": 1} {
		d := &contract.Contract{Interfaces: []contract.Interface{{Name: "public", Type: "openapi", Ref: ref}}}
		rec, err := CompleteIn(dir, dir, d, contract.InferredInputs{})
		if err != nil {
			t.Fatal(err)
		}
		got := 0
		if rec != nil {
			got = len(rec.Findings)
		}
		if got != want {
			t.Errorf("ref %s: record %+v", ref, rec)
		}
	}
}

// A package lockfile is never a spec: over 1 MiB it is no finding.
func TestToolJSONNotRead(t *testing.T) {
	r := Read(Input{Folder: fstest.MapFS{"package-lock.json": {Data: make([]byte, maxFileSize+1)}, "big.json": {Data: make([]byte, maxFileSize+1)}}, FolderPath: "."})
	if len(r.Findings) != 1 || r.Findings[0].Source != "big.json" {
		t.Errorf("findings = %+v", r.Findings)
	}
}
