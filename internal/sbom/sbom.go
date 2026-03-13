// Package sbom provides SBOM parsing and diffing for Pacto bundles.
// It supports SPDX 2.3 and CycloneDX 1.5 JSON formats.
package sbom

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// DefaultDir is the directory inside a bundle where SBOM files are stored.
const DefaultDir = "sbom"

// Package represents a normalized software package extracted from an SBOM.
type Package struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	License  string `json:"license,omitempty"`
	Supplier string `json:"supplier,omitempty"`
}

// Document represents a parsed SBOM document, independent of format.
type Document struct {
	Format   string    `json:"format"` // "spdx" or "cyclonedx"
	Packages []Package `json:"packages"`
}

// ParseFromFS detects and parses an SBOM file from the bundle filesystem.
// It looks for files matching *.spdx.json or *.cdx.json inside the sbom/ directory.
// Returns nil, nil if no SBOM is found.
func ParseFromFS(fsys fs.FS) (*Document, error) {
	if fsys == nil {
		return nil, nil
	}

	entries, err := fs.ReadDir(fsys, DefaultDir)
	if err != nil {
		return nil, nil // sbom/ directory doesn't exist
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		filePath := path.Join(DefaultDir, name)

		if strings.HasSuffix(name, ".spdx.json") {
			return parseSPDX(fsys, filePath)
		}
		if strings.HasSuffix(name, ".cdx.json") {
			return parseCycloneDX(fsys, filePath)
		}
	}

	return nil, nil
}

// HasSBOM reports whether the bundle filesystem contains an SBOM directory
// with at least one recognized SBOM file.
func HasSBOM(fsys fs.FS) bool {
	if fsys == nil {
		return false
	}
	entries, err := fs.ReadDir(fsys, DefaultDir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".spdx.json") || strings.HasSuffix(name, ".cdx.json") {
			return true
		}
	}
	return false
}

// parseSPDX parses an SPDX 2.3 JSON file into a Document.
func parseSPDX(fsys fs.FS, path string) (*Document, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("reading SPDX file: %w", err)
	}

	var spdx struct {
		Packages []struct {
			Name             string `json:"name"`
			VersionInfo      string `json:"versionInfo"`
			Supplier         string `json:"supplier"`
			LicenseConcluded string `json:"licenseConcluded"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &spdx); err != nil {
		return nil, fmt.Errorf("parsing SPDX JSON: %w", err)
	}

	packages := make([]Package, 0, len(spdx.Packages))
	for _, p := range spdx.Packages {
		packages = append(packages, Package{
			Name:     p.Name,
			Version:  p.VersionInfo,
			License:  p.LicenseConcluded,
			Supplier: normalizeSPDXSupplier(p.Supplier),
		})
	}

	return &Document{Format: "spdx", Packages: packages}, nil
}

// normalizeSPDXSupplier strips the "Organization: " or "Person: " prefix
// from SPDX supplier strings.
func normalizeSPDXSupplier(s string) string {
	if idx := strings.Index(s, ": "); idx >= 0 {
		return s[idx+2:]
	}
	return s
}

// parseCycloneDX parses a CycloneDX 1.5 JSON file into a Document.
func parseCycloneDX(fsys fs.FS, path string) (*Document, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("reading CycloneDX file: %w", err)
	}

	var cdx struct {
		Components []cdxComponent `json:"components"`
	}
	if err := json.Unmarshal(data, &cdx); err != nil {
		return nil, fmt.Errorf("parsing CycloneDX JSON: %w", err)
	}

	packages := make([]Package, 0, len(cdx.Components))
	for _, c := range cdx.Components {
		pkg := Package{
			Name:    c.Name,
			Version: c.Version,
		}
		if c.Supplier != nil {
			pkg.Supplier = c.Supplier.Name
		}
		pkg.License = flattenCDXLicenses(c.Licenses)
		packages = append(packages, pkg)
	}

	return &Document{Format: "cyclonedx", Packages: packages}, nil
}

type cdxComponent struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Supplier *struct {
		Name string `json:"name"`
	} `json:"supplier"`
	Licenses []cdxLicenseEntry `json:"licenses"`
}

type cdxLicenseEntry struct {
	License *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"license"`
	Expression string `json:"expression"`
}

func flattenCDXLicenses(licenses []cdxLicenseEntry) string {
	var parts []string
	for _, lc := range licenses {
		if lc.Expression != "" {
			parts = append(parts, lc.Expression)
		} else if lc.License != nil {
			if lc.License.ID != "" {
				parts = append(parts, lc.License.ID)
			} else if lc.License.Name != "" {
				parts = append(parts, lc.License.Name)
			}
		}
	}
	return strings.Join(parts, " AND ")
}
