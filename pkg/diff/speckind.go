package diff

import (
	"io/fs"
	"strings"

	"gopkg.in/yaml.v3"
)

// SpecKind reports which interface spec the file p in fsys is, read by the
// same reader pacto diff compares it with, so a spec it names is always one
// pacto diff can read: "asyncapi" for a document with a top-level asyncapi
// key, "openapi" for one with openapi or swagger, and "grpc" for a .proto
// file that declares a service. ok is false for anything else.
func SpecKind(fsys fs.FS, p string) (kind string, ok bool) {
	data, err := fs.ReadFile(fsys, p)
	if err != nil {
		return "", false
	}
	if strings.HasSuffix(p, ".proto") {
		return "grpc", len(extractProto(string(data)).services) > 0
	}
	var top map[string]any
	if yaml.Unmarshal(data, &top) != nil {
		return "", false
	}
	switch {
	case top["asyncapi"] != nil:
		_, err = readAsyncAPIDoc(fsys, p)
		return "asyncapi", err == nil
	case top["openapi"] != nil || top["swagger"] != nil:
		_, err = readOpenAPISpec(fsys, p)
		return "openapi", err == nil
	}
	return "", false
}
