package diff

import (
	"testing"
	"testing/fstest"
)

func TestDiffOpenAPI_BothFSNil(t *testing.T) {
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffOpenAPI_EmptyPath(t *testing.T) {
	oldFS := fstest.MapFS{}
	newFS := fstest.MapFS{}
	changes := diffOpenAPI("", "", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffOpenAPI_OldFSNil(t *testing.T) {
	newFS := fstest.MapFS{
		"openapi.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
paths:
  /health:
    get:
      summary: Health
`)},
	}
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", nil, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffOpenAPI_BothReadError(t *testing.T) {
	oldFS := fstest.MapFS{}
	newFS := fstest.MapFS{}
	changes := diffOpenAPI("missing.yaml", "missing.yaml", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffOpenAPI_OldReadError(t *testing.T) {
	oldFS := fstest.MapFS{}
	newFS := fstest.MapFS{
		"openapi.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
paths:
  /health:
    get:
      summary: Health
`)},
	}
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffOpenAPI_NewReadError(t *testing.T) {
	oldFS := fstest.MapFS{
		"openapi.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
paths:
  /health:
    get:
      summary: Health
`)},
	}
	newFS := fstest.MapFS{}
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestReadOpenAPIPaths_InvalidYAML(t *testing.T) {
	fs := fstest.MapFS{
		"openapi.yaml": &fstest.MapFile{Data: []byte(`{invalid`)},
	}
	_, err := readOpenAPIPaths(fs, "openapi.yaml")
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}
