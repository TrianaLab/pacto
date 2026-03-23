package dashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiskCache_NewCreatesSubdirs(t *testing.T) {
	root := t.TempDir()
	_, err := NewDiskCache(root)
	if err != nil {
		t.Fatalf("NewDiskCache: %v", err)
	}
	for _, sub := range []string{"oci", "index", "metadata"} {
		info, err := os.Stat(filepath.Join(root, sub))
		if err != nil {
			t.Fatalf("expected %s dir to exist: %v", sub, err)
		}
		if !info.IsDir() {
			t.Fatalf("expected %s to be a directory", sub)
		}
	}
}

func TestDiskCache_SetAndGet(t *testing.T) {
	root := t.TempDir()
	c, err := NewDiskCache(root)
	if err != nil {
		t.Fatal(err)
	}

	c.Set("mykey", map[string]string{"hello": "world"}, time.Minute)

	v, ok := c.Get("mykey")
	if !ok {
		t.Fatal("expected cache hit")
	}

	raw, ok := v.(json.RawMessage)
	if !ok {
		t.Fatalf("expected json.RawMessage, got %T", v)
	}

	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["hello"] != "world" {
		t.Fatalf("expected 'world', got %q", m["hello"])
	}
}

func TestDiskCache_Miss(t *testing.T) {
	root := t.TempDir()
	c, err := NewDiskCache(root)
	if err != nil {
		t.Fatal(err)
	}

	_, ok := c.Get("nonexistent")
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestDiskCache_Expiry(t *testing.T) {
	root := t.TempDir()
	c, err := NewDiskCache(root)
	if err != nil {
		t.Fatal(err)
	}

	c.Set("ephemeral", "data", time.Nanosecond)
	time.Sleep(time.Millisecond)

	_, ok := c.Get("ephemeral")
	if ok {
		t.Fatal("expected cache entry to expire")
	}
}

func TestDiskCache_setImmutableNoExpiry(t *testing.T) {
	root := t.TempDir()
	c, err := NewDiskCache(root)
	if err != nil {
		t.Fatal(err)
	}

	c.setImmutable("permanent", "data")

	v, ok := c.Get("permanent")
	if !ok {
		t.Fatal("expected cache hit for immutable entry")
	}

	raw, ok := v.(json.RawMessage)
	if !ok {
		t.Fatalf("expected json.RawMessage, got %T", v)
	}

	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s != "data" {
		t.Fatalf("expected 'data', got %q", s)
	}
}

func TestDiskCache_OCIBundle(t *testing.T) {
	root := t.TempDir()
	c, err := NewDiskCache(root)
	if err != nil {
		t.Fatal(err)
	}

	digest := "sha256_abc123def456"
	payload := []byte(`{"name":"order-service","version":"1.0.0"}`)

	c.setOCIBundle(digest, payload)

	got, ok := c.getOCIBundle(digest)
	if !ok {
		t.Fatal("expected OCI bundle cache hit")
	}
	if string(got) != string(payload) {
		t.Fatalf("expected %s, got %s", payload, got)
	}
}

func TestDiskCache_OCIBundleMiss(t *testing.T) {
	root := t.TempDir()
	c, err := NewDiskCache(root)
	if err != nil {
		t.Fatal(err)
	}

	_, ok := c.getOCIBundle("sha256_nonexistent")
	if ok {
		t.Fatal("expected OCI bundle cache miss")
	}
}

func TestDiskCache_Metadata(t *testing.T) {
	root := t.TempDir()
	c, err := NewDiskCache(root)
	if err != nil {
		t.Fatal(err)
	}

	type tagMapping struct {
		Tag    string `json:"tag"`
		Digest string `json:"digest"`
	}

	c.setMetadata("order-service-tags", tagMapping{Tag: "v1.0.0", Digest: "sha256:abc"})

	var got tagMapping
	if !c.getMetadata("order-service-tags", &got) {
		t.Fatal("expected metadata hit")
	}
	if got.Tag != "v1.0.0" || got.Digest != "sha256:abc" {
		t.Fatalf("unexpected metadata: %+v", got)
	}
}

func TestDiskCache_MetadataMiss(t *testing.T) {
	root := t.TempDir()
	c, err := NewDiskCache(root)
	if err != nil {
		t.Fatal(err)
	}

	var dest map[string]string
	if c.getMetadata("nonexistent", &dest) {
		t.Fatal("expected metadata miss")
	}
}

func TestDiskCache_GetCorruptedJSON(t *testing.T) {
	root := t.TempDir()
	c, err := NewDiskCache(root)
	if err != nil {
		t.Fatal(err)
	}

	// Write a valid entry first to find the path, then corrupt it.
	c.Set("corrupt", "value", time.Minute)
	path := c.keyPath("corrupt")
	if err := os.WriteFile(path, []byte("not valid json{{{"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, ok := c.Get("corrupt")
	if ok {
		t.Fatal("expected cache miss for corrupted entry")
	}
}

func TestDiskCache_SetUnmarshalableValue(t *testing.T) {
	root := t.TempDir()
	c, err := NewDiskCache(root)
	if err != nil {
		t.Fatal(err)
	}

	// func values cannot be marshaled to JSON.
	c.Set("bad", func() {}, time.Minute)

	_, ok := c.Get("bad")
	if ok {
		t.Fatal("expected cache miss for unmarshalable value")
	}
}

func TestDiskCache_setMetadata_UnmarshalableValue(t *testing.T) {
	root := t.TempDir()
	c, err := NewDiskCache(root)
	if err != nil {
		t.Fatal(err)
	}

	// func values cannot be marshaled to JSON -- setMetadata should silently fail.
	c.setMetadata("bad", func() {})

	var dest any
	if c.getMetadata("bad", &dest) {
		t.Fatal("expected metadata miss for unmarshalable value")
	}
}

func TestNewDiskCache_MkdirFails(t *testing.T) {
	// Use a path under a file (not a directory) so MkdirAll fails.
	root := t.TempDir()
	blockingFile := filepath.Join(root, "blocker")
	if err := os.WriteFile(blockingFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := NewDiskCache(filepath.Join(blockingFile, "subdir"))
	if err == nil {
		t.Fatal("expected error when MkdirAll fails")
	}
}

func TestDiskCache_ImplementsCacheInterface(t *testing.T) {
	root := t.TempDir()
	c, err := NewDiskCache(root)
	if err != nil {
		t.Fatal(err)
	}

	// Verify DiskCache satisfies the Cache interface.
	var _ Cache = c
}

func TestDiskCache_InvalidateAll(t *testing.T) {
	root := t.TempDir()
	c, err := NewDiskCache(root)
	if err != nil {
		t.Fatal(err)
	}

	c.Set("key1", "value1", time.Hour)
	c.Set("key2", "value2", time.Hour)

	if _, ok := c.Get("key1"); !ok {
		t.Fatal("expected key1 to exist before invalidation")
	}

	c.InvalidateAll()

	if _, ok := c.Get("key1"); ok {
		t.Error("expected key1 to be gone after InvalidateAll")
	}
	if _, ok := c.Get("key2"); ok {
		t.Error("expected key2 to be gone after InvalidateAll")
	}
}

func TestDefaultCacheDir(t *testing.T) {
	dir, err := DefaultCacheDir()
	if err != nil {
		t.Fatalf("DefaultCacheDir: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	expected := filepath.Join(home, ".cache", "pacto")
	if dir != expected {
		t.Errorf("expected %q, got %q", expected, dir)
	}
}

func TestDefaultCacheDir_HomeDirError(t *testing.T) {
	orig := userHomeDir
	userHomeDir = func() (string, error) {
		return "", fmt.Errorf("no home directory")
	}
	t.Cleanup(func() { userHomeDir = orig })

	_, err := DefaultCacheDir()
	if err == nil {
		t.Fatal("expected error when userHomeDir fails")
	}
	if !strings.Contains(err.Error(), "determining home directory") {
		t.Errorf("unexpected error: %v", err)
	}
}
