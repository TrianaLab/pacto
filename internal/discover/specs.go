package discover

import (
	"bytes"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/diff"
)

// spec is one interface spec file and the line of its kind.
type spec struct {
	path string
	line int
}

// specNames name the interface each spec kind becomes, as pacto init names
// the OpenAPI one.
var specNames = map[string]string{"openapi": "api", "asyncapi": "events", "grpc": "grpc"}

// specLabels name each spec kind in a finding.
var specLabels = map[string]string{"openapi": "OpenAPI", "asyncapi": "AsyncAPI", "grpc": "gRPC proto"}

// spec keeps the file p, whose bytes are data, when pacto diff reads it as
// an interface spec.
func (r *reader) spec(p string, data []byte) {
	kind, ok := diff.SpecKind(r.in.Folder, p)
	if !ok {
		return
	}
	r.specs[kind] = append(r.specs[kind], spec{path: p, line: specLine(kind, data)})
}

// interfaces is one interface per spec kind found once. A kind found in
// several files is ambiguous: which one the service serves is a person's
// call, so none is recorded.
func (r *reader) interfaces() {
	for _, kind := range slices.Sorted(maps.Keys(r.specs)) {
		found := r.specs[kind]
		slices.SortFunc(found, func(a, b spec) int { return strings.Compare(a.path, b.path) })
		if len(found) > 1 {
			var paths []string
			for _, s := range found {
				paths = append(paths, s.path)
			}
			r.find(CodeAmbiguous, found[0].path, ambiguousSpecMessage(specLabels[kind], paths))
			continue
		}
		s, name := found[0], specNames[kind]
		r.add(entry{Inferred: contract.Inferred{Field: "interfaces[" + name + "]", Value: kind + " " + s.path, Source: fmt.Sprintf("%s:%d", s.path, s.line),
			Confidence: contract.ConfidenceLikely, Declare: InterfaceYAML(name, kind, s.path)}, file: s.path})
	}
}

// protoServiceRE finds a service declaration in a .proto file.
var protoServiceRE = regexp.MustCompile(`(?m)^[ \t]*service\s+\w+\s*\{`)

// specLine is the line of what makes data a spec of kind: its top-level
// openapi, swagger or asyncapi key, or its first service. 1 when not found.
func specLine(kind string, data []byte) int {
	if kind == "grpc" {
		if loc := protoServiceRE.FindIndex(data); loc != nil {
			return bytes.Count(data[:loc[0]], []byte("\n")) + 1
		}
		return 1
	}
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) == nil && len(doc.Content) == 1 {
		m := doc.Content[0]
		for i := 0; i+1 < len(m.Content); i += 2 {
			if k := m.Content[i].Value; k == kind || (kind == "openapi" && k == "swagger") {
				return m.Content[i].Line
			}
		}
	}
	return 1
}

// OpenAPIRef is the OpenAPI file fields name as the api interface, or "".
func OpenAPIRef(fields []contract.Inferred) string {
	for _, f := range fields {
		if ref, ok := strings.CutPrefix(f.Value, "openapi "); ok && f.Field == "interfaces[api]" {
			return ref
		}
	}
	return ""
}
