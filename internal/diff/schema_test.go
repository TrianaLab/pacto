package diff

import (
	"testing"
	"testing/fstest"
)

func TestDiffSchema_BothFSNil(t *testing.T) {
	changes := diffSchema("schema.json", "schema.json", nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffSchema_EmptyPath(t *testing.T) {
	oldFS := fstest.MapFS{}
	newFS := fstest.MapFS{}
	changes := diffSchema("", "", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffSchema_BothReadError(t *testing.T) {
	oldFS := fstest.MapFS{}
	newFS := fstest.MapFS{}
	changes := diffSchema("missing.json", "missing.json", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffSchema_OldReadError(t *testing.T) {
	oldFS := fstest.MapFS{}
	newFS := fstest.MapFS{
		"schema.json": &fstest.MapFile{Data: []byte(`{"type":"object","properties":{}}`)},
	}
	changes := diffSchema("schema.json", "schema.json", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffSchema_NewReadError(t *testing.T) {
	oldFS := fstest.MapFS{
		"schema.json": &fstest.MapFile{Data: []byte(`{"type":"object","properties":{}}`)},
	}
	newFS := fstest.MapFS{}
	changes := diffSchema("schema.json", "schema.json", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestReadSchemaProperties_InvalidJSON(t *testing.T) {
	fs := fstest.MapFS{
		"schema.json": &fstest.MapFile{Data: []byte(`{invalid}`)},
	}
	_, err := readSchemaProperties(fs, "schema.json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
