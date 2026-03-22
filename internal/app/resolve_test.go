package app

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/override"
)

func TestDefaultPath_Empty(t *testing.T) {
	if got := defaultPath(""); got != "." {
		t.Errorf("expected %q, got %q", ".", got)
	}
}

func TestDefaultPath_NonEmpty(t *testing.T) {
	if got := defaultPath("custom"); got != "custom" {
		t.Errorf("expected custom, got %q", got)
	}
}

func TestIsOCIRef_True(t *testing.T) {
	if !isOCIRef("oci://ghcr.io/acme/svc:1.0.0") {
		t.Error("expected true for oci:// prefix")
	}
}

func TestIsOCIRef_False(t *testing.T) {
	if isOCIRef("my-service") {
		t.Error("expected false for local path")
	}
}

func TestResolveLocalPath_Success(t *testing.T) {
	dir := writeTestBundle(t)
	filePath, bundleDir, err := resolveLocalPath(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filePath != filepath.Join(dir, "pacto.yaml") {
		t.Errorf("expected filePath=%s, got %s", filepath.Join(dir, "pacto.yaml"), filePath)
	}
	if bundleDir != dir {
		t.Errorf("expected bundleDir=%s, got %s", dir, bundleDir)
	}
}

func TestResolveLocalPath_NotADirectory(t *testing.T) {
	dir := writeTestBundle(t)
	filePath := filepath.Join(dir, "pacto.yaml")
	_, _, err := resolveLocalPath(filePath)
	if err == nil {
		t.Error("expected error for file path instead of directory")
	}
}

func TestResolveLocalPath_MissingPactoYAML(t *testing.T) {
	dir := t.TempDir()
	_, _, err := resolveLocalPath(dir)
	if err == nil {
		t.Error("expected error for directory without pacto.yaml")
	}
}

func TestResolveLocalPath_NonexistentDir(t *testing.T) {
	_, _, err := resolveLocalPath("/nonexistent/dir")
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestResolveBundle_LocalPath(t *testing.T) {
	dir := writeTestBundle(t)
	svc := NewService(nil, nil)
	bundle, err := svc.resolveBundle(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bundle.Contract.Service.Name != "test-svc" {
		t.Errorf("expected test-svc, got %s", bundle.Contract.Service.Name)
	}
	if bundle.RawYAML == nil {
		t.Error("expected RawYAML to be set for local path")
	}
}

func TestResolveBundle_LocalPath_NotFound(t *testing.T) {
	svc := NewService(nil, nil)
	_, err := svc.resolveBundle(context.Background(), "/nonexistent/dir")
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestResolveBundle_LocalPath_UnreadableFile(t *testing.T) {
	dir := writeTestBundle(t)
	pactoPath := filepath.Join(dir, "pacto.yaml")
	if err := os.Chmod(pactoPath, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(pactoPath, 0644) })

	svc := NewService(nil, nil)
	_, err := svc.resolveBundle(context.Background(), dir)
	if err == nil {
		t.Error("expected error when pacto.yaml is unreadable")
	}
}

func TestResolveBundle_LocalPath_InvalidYAML(t *testing.T) {
	dir := writeUnparseableBundle(t)
	svc := NewService(nil, nil)
	_, err := svc.resolveBundle(context.Background(), dir)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestResolveBundle_OCI_Success(t *testing.T) {
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	bundle, err := svc.resolveBundle(context.Background(), "oci://ghcr.io/acme/svc:1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bundle.Contract.Service.Name != "test-svc" {
		t.Errorf("expected test-svc, got %s", bundle.Contract.Service.Name)
	}
}

func TestResolveBundle_OCI_NoTag_ResolvesLatest(t *testing.T) {
	store := &mockBundleStore{
		ListTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return []string{"1.0.0", "2.0.0", "3.0.0"}, nil
		},
	}
	svc := NewService(store, nil)
	bundle, err := svc.resolveBundle(context.Background(), "oci://ghcr.io/acme/svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bundle.Contract.Service.Name != "test-svc" {
		t.Errorf("expected test-svc, got %s", bundle.Contract.Service.Name)
	}
}

func TestResolveBundle_OCI_NoTag_ListTagsError(t *testing.T) {
	store := errBundleStore("list failed")
	svc := NewService(store, nil)
	_, err := svc.resolveBundle(context.Background(), "oci://ghcr.io/acme/svc")
	if err == nil {
		t.Error("expected error when ListTags fails")
	}
}

func TestResolveBundle_OCI_NilStore(t *testing.T) {
	svc := NewService(nil, nil)
	_, err := svc.resolveBundle(context.Background(), "oci://ghcr.io/acme/svc:1.0.0")
	if err == nil {
		t.Error("expected error for nil store")
	}
}

func TestResolveBundle_OCI_StoreError(t *testing.T) {
	store := errBundleStore("pull failed")
	svc := NewService(store, nil)
	_, err := svc.resolveBundle(context.Background(), "oci://ghcr.io/acme/svc:1.0.0")
	if err == nil {
		t.Error("expected error from store")
	}
}

func TestExtractBundleFS(t *testing.T) {
	fsys := fstest.MapFS{
		"pacto.yaml":   &fstest.MapFile{Data: []byte("test")},
		"sub/file.txt": &fstest.MapFile{Data: []byte("nested")},
	}
	dir := t.TempDir()
	if err := extractBundleFS(fsys, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	if err != nil {
		t.Fatalf("expected pacto.yaml: %v", err)
	}
	if string(data) != "test" {
		t.Errorf("expected 'test', got %q", string(data))
	}

	data, err = os.ReadFile(filepath.Join(dir, "sub", "file.txt"))
	if err != nil {
		t.Fatalf("expected sub/file.txt: %v", err)
	}
	if string(data) != "nested" {
		t.Errorf("expected 'nested', got %q", string(data))
	}
}

func TestExtractBundleFS_WithDirectory(t *testing.T) {
	fsys := fstest.MapFS{
		"dir":          &fstest.MapFile{Mode: os.ModeDir},
		"dir/file.txt": &fstest.MapFile{Data: []byte("content")},
	}
	dir := t.TempDir()
	if err := extractBundleFS(fsys, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "dir"))
	if err != nil {
		t.Fatalf("expected dir to exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected dir to be a directory")
	}
}

func TestPrepareBundleDir_LocalPath(t *testing.T) {
	dir := writeTestBundle(t)
	bundleDir, cleanup, err := prepareBundleDir(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cleanup != nil {
		t.Error("expected no cleanup for local path")
	}
	if bundleDir != dir {
		t.Errorf("expected %s, got %s", dir, bundleDir)
	}
}

func TestPrepareBundleDir_OCI(t *testing.T) {
	fsys := fstest.MapFS{
		"pacto.yaml": &fstest.MapFile{Data: []byte("test")},
	}
	dir, cleanup, err := prepareBundleDir("oci://ghcr.io/acme/svc:1.0.0", fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cleanup == nil {
		t.Fatal("expected cleanup for OCI ref")
	}
	defer cleanup()

	if _, err := os.Stat(filepath.Join(dir, "pacto.yaml")); err != nil {
		t.Fatalf("expected pacto.yaml in temp dir: %v", err)
	}
}

func TestExtractBundleFS_ReadFileError(t *testing.T) {
	fsys := readFailFS{fstest.MapFS{
		"test.txt": &fstest.MapFile{Data: []byte("content")},
	}}
	dir := t.TempDir()
	err := extractBundleFS(fsys, dir)
	if err == nil {
		t.Error("expected error when ReadFile fails")
	}
}

func TestExtractBundleFS_MkdirAllError(t *testing.T) {
	fsys := fstest.MapFS{
		"file.txt": &fstest.MapFile{Data: []byte("content")},
	}
	// /dev/null is not a directory, so MkdirAll for child paths fails
	err := extractBundleFS(fsys, "/dev/null/target")
	if err == nil {
		t.Error("expected error when MkdirAll for file parent fails")
	}
}

func TestExtractBundleFS_WalkError(t *testing.T) {
	dir := t.TempDir()
	err := extractBundleFS(&errFS{}, dir)
	if err == nil {
		t.Error("expected error from errFS")
	}
}

func TestPrepareBundleDir_OCIExtractError(t *testing.T) {
	_, _, err := prepareBundleDir("oci://ghcr.io/acme/svc:1.0.0", &errFS{})
	if err == nil {
		t.Error("expected error when extracting bundle FS fails")
	}
}

func TestApplyOverrides_Empty(t *testing.T) {
	bundle := testBundle()
	result, err := applyOverrides(bundle, override.Overrides{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != bundle {
		t.Error("expected same bundle returned for empty overrides")
	}
}

func TestApplyOverrides_WithSet(t *testing.T) {
	bundle := testBundle()
	result, err := applyOverrides(bundle, override.Overrides{
		SetValues: []string{"service.version=9.9.9"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Contract.Service.Version != "9.9.9" {
		t.Errorf("expected version 9.9.9, got %s", result.Contract.Service.Version)
	}
}

func TestApplyOverrides_NilRawYAML_FSReadError(t *testing.T) {
	bundle := &contract.Bundle{
		Contract: testBundle().Contract,
		RawYAML:  nil,
		FS:       &errFS{},
	}
	_, err := applyOverrides(bundle, override.Overrides{
		SetValues: []string{"service.version=2.0.0"},
	})
	if err == nil {
		t.Error("expected error when FS read fails")
	}
}

func TestApplyOverrides_NilRawYAML_WithFS(t *testing.T) {
	bundle := testBundle()
	bundle.RawYAML = nil // Force read from FS
	result, err := applyOverrides(bundle, override.Overrides{
		SetValues: []string{"service.version=2.0.0"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Contract.Service.Version != "2.0.0" {
		t.Errorf("expected version 2.0.0, got %s", result.Contract.Service.Version)
	}
}

func TestApplyOverrides_NilRawYAML_NilFS(t *testing.T) {
	bundle := &contract.Bundle{
		Contract: testBundle().Contract,
		RawYAML:  nil,
		FS:       nil,
	}
	_, err := applyOverrides(bundle, override.Overrides{
		SetValues: []string{"service.version=2.0.0"},
	})
	if err == nil {
		t.Error("expected error when both RawYAML and FS are nil")
	}
}

func TestApplyOverrides_InvalidOverride(t *testing.T) {
	bundle := testBundle()
	_, err := applyOverrides(bundle, override.Overrides{
		SetValues: []string{"no-equals"},
	})
	if err == nil {
		t.Error("expected error for invalid --set format")
	}
}

func TestApplyOverrides_InvalidOverrideResult(t *testing.T) {
	// Override service to a scalar — this will produce YAML that fails
	// structural validation (JSON Schema).
	bundle := testBundle()
	_, err := applyOverrides(bundle, override.Overrides{
		SetValues: []string{"service=not-a-map"},
	})
	if err == nil {
		t.Error("expected error when merged YAML produces invalid contract")
	}
}

func TestApplyOverrides_SchemaViolation(t *testing.T) {
	// Override an enum field to an invalid value — this should fail structural
	// validation even though Go struct unmarshalling would accept it.
	bundle := testBundle()
	_, err := applyOverrides(bundle, override.Overrides{
		SetValues: []string{"runtime.state.type=invalid-enum"},
	})
	if err == nil {
		t.Error("expected error for invalid enum override")
	}
}

func TestLoadAndValidateLocal_Success(t *testing.T) {
	dir := writeTestBundle(t)
	c, rawYAML, bundleFS, err := loadAndValidateLocal(dir, override.Overrides{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Service.Name != "test-svc" {
		t.Errorf("expected test-svc, got %s", c.Service.Name)
	}
	if rawYAML == nil {
		t.Error("expected rawYAML to be set")
	}
	if bundleFS == nil {
		t.Error("expected bundleFS to be set")
	}
}

func TestLoadAndValidateLocal_FileNotFound(t *testing.T) {
	_, _, _, err := loadAndValidateLocal("/nonexistent/dir", override.Overrides{})
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestLoadAndValidateLocal_UnreadableFile(t *testing.T) {
	dir := writeTestBundle(t)
	pactoPath := filepath.Join(dir, "pacto.yaml")
	if err := os.Chmod(pactoPath, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(pactoPath, 0644) })

	_, _, _, err := loadAndValidateLocal(dir, override.Overrides{})
	if err == nil {
		t.Error("expected error when pacto.yaml is unreadable")
	}
}

func TestLoadAndValidateLocal_InvalidContract(t *testing.T) {
	dir := writeUnparseableBundle(t)
	_, _, _, err := loadAndValidateLocal(dir, override.Overrides{})
	if err == nil {
		t.Error("expected error for invalid contract")
	}
}

func TestLoadAndValidateLocal_OverrideError(t *testing.T) {
	dir := writeTestBundle(t)
	_, _, _, err := loadAndValidateLocal(dir, override.Overrides{
		SetValues: []string{"no-equals"},
	})
	if err == nil {
		t.Error("expected error for invalid override")
	}
}

func TestLoadAndValidateLocal_ValidationFails(t *testing.T) {
	dir := writeInvalidBundle(t)
	_, _, _, err := loadAndValidateLocal(dir, override.Overrides{})
	if err == nil {
		t.Error("expected error for invalid bundle")
	}
}

func TestCopyFSWithReplace_ErrorFS(t *testing.T) {
	_, err := copyFSWithReplace(errFS{}, "pacto.yaml", []byte("data"))
	if err == nil {
		t.Error("expected error for errFS")
	}
}

func TestCopyFSWithReplace_ReadFileError(t *testing.T) {
	base := readFailFS{fstest.MapFS{
		"other.yaml": &fstest.MapFile{Data: []byte("data")},
	}}
	_, err := copyFSWithReplace(base, "pacto.yaml", []byte("data"))
	if err == nil {
		t.Error("expected error when ReadFile fails")
	}
}

func TestApplyOverrides_CopyFSError(t *testing.T) {
	b := testBundle()
	b.FS = errFS{}
	_, err := applyOverrides(b, override.Overrides{SetValues: []string{"service.version=9.9.9"}})
	if err == nil {
		t.Error("expected error when FS walk fails")
	}
}

func TestCopyFSWithReplace(t *testing.T) {
	base := fstest.MapFS{
		"pacto.yaml":   &fstest.MapFile{Data: []byte("original")},
		"openapi.yaml": &fstest.MapFile{Data: []byte("openapi content")},
		"docs":         &fstest.MapFile{Mode: fs.ModeDir | 0755},
		"docs/README":  &fstest.MapFile{Data: []byte("readme")},
	}
	replaced := []byte("overridden content")
	result, err := copyFSWithReplace(base, "pacto.yaml", replaced)
	if err != nil {
		t.Fatal(err)
	}

	data, err := fs.ReadFile(result, "pacto.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "overridden content" {
		t.Errorf("expected replaced content, got %q", string(data))
	}

	data, err = fs.ReadFile(result, "openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "openapi content" {
		t.Errorf("expected original content, got %q", string(data))
	}

	data, err = fs.ReadFile(result, "docs/README")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "readme" {
		t.Errorf("expected readme, got %q", string(data))
	}
}
