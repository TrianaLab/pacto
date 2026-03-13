package sbom

import (
	"fmt"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestParseFromFS_SPDX(t *testing.T) {
	fsys := fstest.MapFS{
		"sbom/sbom.spdx.json": &fstest.MapFile{Data: []byte(`{
			"spdxVersion": "SPDX-2.3",
			"packages": [
				{
					"name": "example-lib",
					"versionInfo": "1.0.0",
					"supplier": "Organization: Acme Inc.",
					"licenseConcluded": "MIT"
				}
			]
		}`)},
	}

	doc, err := ParseFromFS(fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc == nil {
		t.Fatal("expected document, got nil")
	}
	if doc.Format != "spdx" {
		t.Errorf("expected format=spdx, got %q", doc.Format)
	}
	if len(doc.Packages) != 1 {
		t.Fatalf("expected 1 package, got %d", len(doc.Packages))
	}
	p := doc.Packages[0]
	if p.Name != "example-lib" {
		t.Errorf("expected name=example-lib, got %q", p.Name)
	}
	if p.Version != "1.0.0" {
		t.Errorf("expected version=1.0.0, got %q", p.Version)
	}
	if p.Supplier != "Acme Inc." {
		t.Errorf("expected supplier=Acme Inc., got %q", p.Supplier)
	}
	if p.License != "MIT" {
		t.Errorf("expected license=MIT, got %q", p.License)
	}
}

func TestParseFromFS_CycloneDX(t *testing.T) {
	fsys := fstest.MapFS{
		"sbom/sbom.cdx.json": &fstest.MapFile{Data: []byte(`{
			"bomFormat": "CycloneDX",
			"specVersion": "1.5",
			"components": [
				{
					"name": "my-lib",
					"version": "2.0.0",
					"supplier": {"name": "My Org"},
					"licenses": [
						{"license": {"id": "Apache-2.0"}},
						{"license": {"name": "Custom License"}}
					]
				}
			]
		}`)},
	}

	doc, err := ParseFromFS(fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc == nil {
		t.Fatal("expected document, got nil")
	}
	if doc.Format != "cyclonedx" {
		t.Errorf("expected format=cyclonedx, got %q", doc.Format)
	}
	if len(doc.Packages) != 1 {
		t.Fatalf("expected 1 package, got %d", len(doc.Packages))
	}
	p := doc.Packages[0]
	if p.Name != "my-lib" {
		t.Errorf("expected name=my-lib, got %q", p.Name)
	}
	if p.Version != "2.0.0" {
		t.Errorf("expected version=2.0.0, got %q", p.Version)
	}
	if p.Supplier != "My Org" {
		t.Errorf("expected supplier=My Org, got %q", p.Supplier)
	}
	if p.License != "Apache-2.0 AND Custom License" {
		t.Errorf("expected license=Apache-2.0 AND Custom License, got %q", p.License)
	}
}

func TestParseFromFS_CycloneDX_ExpressionLicense(t *testing.T) {
	fsys := fstest.MapFS{
		"sbom/bom.cdx.json": &fstest.MapFile{Data: []byte(`{
			"bomFormat": "CycloneDX",
			"components": [
				{
					"name": "expr-lib",
					"version": "1.0.0",
					"licenses": [{"expression": "MIT OR Apache-2.0"}]
				}
			]
		}`)},
	}

	doc, err := ParseFromFS(fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc.Packages[0].License != "MIT OR Apache-2.0" {
		t.Errorf("expected expression license, got %q", doc.Packages[0].License)
	}
}

func TestParseFromFS_CycloneDX_NoSupplier(t *testing.T) {
	fsys := fstest.MapFS{
		"sbom/bom.cdx.json": &fstest.MapFile{Data: []byte(`{
			"bomFormat": "CycloneDX",
			"components": [{"name": "bare-lib", "version": "1.0.0"}]
		}`)},
	}

	doc, err := ParseFromFS(fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc.Packages[0].Supplier != "" {
		t.Errorf("expected empty supplier, got %q", doc.Packages[0].Supplier)
	}
}

func TestParseFromFS_NilFS(t *testing.T) {
	doc, err := ParseFromFS(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc != nil {
		t.Error("expected nil document for nil FS")
	}
}

func TestParseFromFS_NoSBOMDir(t *testing.T) {
	fsys := fstest.MapFS{
		"pacto.yaml": &fstest.MapFile{Data: []byte("test")},
	}
	doc, err := ParseFromFS(fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc != nil {
		t.Error("expected nil document when no sbom/ dir")
	}
}

func TestParseFromFS_EmptySBOMDir(t *testing.T) {
	fsys := fstest.MapFS{
		"sbom/readme.txt": &fstest.MapFile{Data: []byte("not an sbom")},
	}
	doc, err := ParseFromFS(fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc != nil {
		t.Error("expected nil document when no recognized SBOM files")
	}
}

func TestParseFromFS_InvalidSPDXJSON(t *testing.T) {
	fsys := fstest.MapFS{
		"sbom/bad.spdx.json": &fstest.MapFile{Data: []byte(`{invalid}`)},
	}
	_, err := ParseFromFS(fsys)
	if err == nil {
		t.Error("expected error for invalid SPDX JSON")
	}
}

func TestParseFromFS_InvalidCDXJSON(t *testing.T) {
	fsys := fstest.MapFS{
		"sbom/bad.cdx.json": &fstest.MapFile{Data: []byte(`not json`)},
	}
	_, err := ParseFromFS(fsys)
	if err == nil {
		t.Error("expected error for invalid CycloneDX JSON")
	}
}

func TestParseFromFS_SkipsDirectories(t *testing.T) {
	fsys := fstest.MapFS{
		"sbom/subdir/file.spdx.json": &fstest.MapFile{Data: []byte(`{"packages":[]}`)},
	}
	doc, err := ParseFromFS(fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc != nil {
		t.Error("expected nil when SBOM file is in subdirectory")
	}
}

func TestHasSBOM_True_SPDX(t *testing.T) {
	fsys := fstest.MapFS{
		"sbom/sbom.spdx.json": &fstest.MapFile{Data: []byte("{}")},
	}
	if !HasSBOM(fsys) {
		t.Error("expected HasSBOM=true for SPDX file")
	}
}

func TestHasSBOM_True_CycloneDX(t *testing.T) {
	fsys := fstest.MapFS{
		"sbom/bom.cdx.json": &fstest.MapFile{Data: []byte("{}")},
	}
	if !HasSBOM(fsys) {
		t.Error("expected HasSBOM=true for CycloneDX file")
	}
}

func TestHasSBOM_False_NilFS(t *testing.T) {
	if HasSBOM(nil) {
		t.Error("expected HasSBOM=false for nil FS")
	}
}

func TestHasSBOM_False_NoDir(t *testing.T) {
	fsys := fstest.MapFS{}
	if HasSBOM(fsys) {
		t.Error("expected HasSBOM=false when no sbom/ dir")
	}
}

func TestHasSBOM_False_UnrecognizedFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"sbom/readme.txt": &fstest.MapFile{Data: []byte("not an sbom")},
	}
	if HasSBOM(fsys) {
		t.Error("expected HasSBOM=false for unrecognized files")
	}
}

func TestParseSPDX_ReadError(t *testing.T) {
	// parseSPDX is called when a .spdx.json file exists in the directory listing,
	// but reading it fails. We test this via the exported ParseFromFS.
	fsys := errReadFS{
		readDirResult: []fakeDirEntry{{name: "sbom.spdx.json"}},
	}
	_, err := ParseFromFS(fsys)
	if err == nil {
		t.Error("expected error when SPDX file cannot be read")
	}
}

func TestParseCycloneDX_ReadError(t *testing.T) {
	fsys := errReadFS{
		readDirResult: []fakeDirEntry{{name: "bom.cdx.json"}},
	}
	_, err := ParseFromFS(fsys)
	if err == nil {
		t.Error("expected error when CycloneDX file cannot be read")
	}
}

// errReadFS is an fs.FS that returns entries from ReadDir but errors on ReadFile.
type errReadFS struct {
	readDirResult []fakeDirEntry
}

func (f errReadFS) Open(name string) (fs.File, error) {
	return nil, fmt.Errorf("read error: %s", name)
}

func (f errReadFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == DefaultDir {
		entries := make([]fs.DirEntry, len(f.readDirResult))
		for i := range f.readDirResult {
			entries[i] = &f.readDirResult[i]
		}
		return entries, nil
	}
	return nil, fmt.Errorf("not found: %s", name)
}

type fakeDirEntry struct {
	name string
}

func (f *fakeDirEntry) Name() string               { return f.name }
func (f *fakeDirEntry) IsDir() bool                 { return false }
func (f *fakeDirEntry) Type() fs.FileMode           { return 0 }
func (f *fakeDirEntry) Info() (fs.FileInfo, error)  { return nil, nil }

func TestNormalizeSPDXSupplier(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"Organization: Acme Inc.", "Acme Inc."},
		{"Person: Jane Doe", "Jane Doe"},
		{"NoPrefix", "NoPrefix"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := normalizeSPDXSupplier(tt.input); got != tt.want {
			t.Errorf("normalizeSPDXSupplier(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFlattenCDXLicenses(t *testing.T) {
	tests := []struct {
		name     string
		licenses []cdxLicenseEntry
		want     string
	}{
		{
			name:     "empty",
			licenses: nil,
			want:     "",
		},
		{
			name: "single ID",
			licenses: []cdxLicenseEntry{
				{License: &struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				}{ID: "MIT"}},
			},
			want: "MIT",
		},
		{
			name: "single name",
			licenses: []cdxLicenseEntry{
				{License: &struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				}{Name: "Custom"}},
			},
			want: "Custom",
		},
		{
			name: "expression",
			licenses: []cdxLicenseEntry{
				{Expression: "MIT OR Apache-2.0"},
			},
			want: "MIT OR Apache-2.0",
		},
		{
			name: "multiple",
			licenses: []cdxLicenseEntry{
				{License: &struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				}{ID: "MIT"}},
				{License: &struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				}{ID: "Apache-2.0"}},
			},
			want: "MIT AND Apache-2.0",
		},
		{
			name: "nil license",
			licenses: []cdxLicenseEntry{
				{License: nil},
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := flattenCDXLicenses(tt.licenses); got != tt.want {
				t.Errorf("flattenCDXLicenses() = %q, want %q", got, tt.want)
			}
		})
	}
}
