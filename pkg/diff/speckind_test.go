package diff

import (
	"testing"
	"testing/fstest"
)

func specKind(t *testing.T, name, data string) (string, bool) {
	t.Helper()
	return SpecKind(fstest.MapFS{name: {Data: []byte(data)}}, name)
}

func TestSpecKindOpenAPI(t *testing.T) {
	if kind, ok := specKind(t, "api/openapi.v1.yaml", "openapi: 3.1.0\npaths:\n  /a: {get: {}}\n"); kind != "openapi" || !ok {
		t.Errorf("got %q %v", kind, ok)
	}
	// JSON is read too.
	if kind, ok := specKind(t, "api.json", `{"openapi": "3.0.0", "paths": {}}`); kind != "openapi" || !ok {
		t.Errorf("json: got %q %v", kind, ok)
	}
	// An openapi key whose paths the reader cannot read is no spec.
	if _, ok := specKind(t, "bad.yaml", "openapi: 3.1.0\npaths: [a, b]\n"); ok {
		t.Error("unreadable paths read as a spec")
	}
}

func TestSpecKindSwagger(t *testing.T) {
	if kind, ok := specKind(t, "swagger.json", `{"swagger": "2.0", "paths": {}}`); kind != "openapi" || !ok {
		t.Errorf("got %q %v", kind, ok)
	}
}

func TestSpecKindAsyncAPI(t *testing.T) {
	if kind, ok := specKind(t, "events.yaml", "asyncapi: 3.0.0\nchannels:\n  orders: {}\n"); kind != "asyncapi" || !ok {
		t.Errorf("got %q %v", kind, ok)
	}
	if _, ok := specKind(t, "bad.yaml", "asyncapi: 3.0.0\nchannels: [a]\n"); ok {
		t.Error("unreadable channels read as a spec")
	}
}

func TestSpecKindProto(t *testing.T) {
	if kind, ok := specKind(t, "api.proto", "syntax = \"proto3\";\nservice Pay {\n  rpc Charge (A) returns (B);\n}\n"); kind != "grpc" || !ok {
		t.Errorf("got %q %v", kind, ok)
	}
	if _, ok := specKind(t, "types.proto", "syntax = \"proto3\";\nmessage A {}\n"); ok {
		t.Error("a proto without a service read as a spec")
	}
}

func TestSpecKindPlainYAMLNotSpec(t *testing.T) {
	for name, data := range map[string]string{
		"deploy.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: x}\n",
		"list.yaml":   "- a\n- b\n",
		"broken.yaml": "a: [",
	} {
		if kind, ok := specKind(t, name, data); ok {
			t.Errorf("%s read as %q", name, kind)
		}
	}
	if _, ok := SpecKind(fstest.MapFS{}, "missing.yaml"); ok {
		t.Error("a missing file read as a spec")
	}
}
