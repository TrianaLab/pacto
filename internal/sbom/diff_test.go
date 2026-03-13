package sbom

import (
	"testing"
)

func TestDiff_BothNil(t *testing.T) {
	result := Diff(nil, nil)
	if result != nil {
		t.Error("expected nil result when both documents are nil")
	}
}

func TestDiff_OldNil_NewHasPackages(t *testing.T) {
	new := &Document{
		Format: "spdx",
		Packages: []Package{
			{Name: "lib-a", Version: "1.0.0"},
			{Name: "lib-b", Version: "2.0.0"},
		},
	}
	result := Diff(nil, new)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.NewFormat != "spdx" {
		t.Errorf("expected newFormat=spdx, got %q", result.NewFormat)
	}
	if len(result.Changes) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(result.Changes))
	}
	for _, c := range result.Changes {
		if c.Type != PackageAdded {
			t.Errorf("expected all changes to be added, got %s", c.Type)
		}
	}
}

func TestDiff_NewNil_OldHasPackages(t *testing.T) {
	old := &Document{
		Format: "cyclonedx",
		Packages: []Package{
			{Name: "lib-a", Version: "1.0.0"},
		},
	}
	result := Diff(old, nil)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.OldFormat != "cyclonedx" {
		t.Errorf("expected oldFormat=cyclonedx, got %q", result.OldFormat)
	}
	if len(result.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(result.Changes))
	}
	if result.Changes[0].Type != PackageRemoved {
		t.Errorf("expected removed, got %s", result.Changes[0].Type)
	}
}

func TestDiff_NoChanges(t *testing.T) {
	doc := &Document{
		Format: "spdx",
		Packages: []Package{
			{Name: "lib-a", Version: "1.0.0", License: "MIT"},
		},
	}
	result := Diff(doc, doc)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(result.Changes))
	}
}

func TestDiff_PackageAdded(t *testing.T) {
	old := &Document{Format: "spdx", Packages: []Package{
		{Name: "lib-a", Version: "1.0.0"},
	}}
	new := &Document{Format: "spdx", Packages: []Package{
		{Name: "lib-a", Version: "1.0.0"},
		{Name: "lib-b", Version: "2.0.0"},
	}}

	result := Diff(old, new)
	found := false
	for _, c := range result.Changes {
		if c.Package == "lib-b" && c.Type == PackageAdded {
			found = true
			if c.NewValue != "2.0.0" {
				t.Errorf("expected newValue=2.0.0, got %q", c.NewValue)
			}
		}
	}
	if !found {
		t.Error("expected lib-b added change")
	}
}

func TestDiff_PackageRemoved(t *testing.T) {
	old := &Document{Format: "spdx", Packages: []Package{
		{Name: "lib-a", Version: "1.0.0"},
		{Name: "lib-b", Version: "2.0.0"},
	}}
	new := &Document{Format: "spdx", Packages: []Package{
		{Name: "lib-a", Version: "1.0.0"},
	}}

	result := Diff(old, new)
	found := false
	for _, c := range result.Changes {
		if c.Package == "lib-b" && c.Type == PackageRemoved {
			found = true
			if c.OldValue != "2.0.0" {
				t.Errorf("expected oldValue=2.0.0, got %q", c.OldValue)
			}
		}
	}
	if !found {
		t.Error("expected lib-b removed change")
	}
}

func TestDiff_VersionModified(t *testing.T) {
	old := &Document{Format: "spdx", Packages: []Package{
		{Name: "lib-a", Version: "1.0.0"},
	}}
	new := &Document{Format: "spdx", Packages: []Package{
		{Name: "lib-a", Version: "2.0.0"},
	}}

	result := Diff(old, new)
	found := false
	for _, c := range result.Changes {
		if c.Package == "lib-a" && c.Type == PackageModified && c.Field == "version" {
			found = true
			if c.OldValue != "1.0.0" || c.NewValue != "2.0.0" {
				t.Errorf("expected 1.0.0->2.0.0, got %s->%s", c.OldValue, c.NewValue)
			}
		}
	}
	if !found {
		t.Error("expected lib-a version modified change")
	}
}

func TestDiff_LicenseModified(t *testing.T) {
	old := &Document{Format: "spdx", Packages: []Package{
		{Name: "lib-a", Version: "1.0.0", License: "MIT"},
	}}
	new := &Document{Format: "spdx", Packages: []Package{
		{Name: "lib-a", Version: "1.0.0", License: "Apache-2.0"},
	}}

	result := Diff(old, new)
	found := false
	for _, c := range result.Changes {
		if c.Package == "lib-a" && c.Type == PackageModified && c.Field == "license" {
			found = true
			if c.OldValue != "MIT" || c.NewValue != "Apache-2.0" {
				t.Errorf("expected MIT->Apache-2.0, got %s->%s", c.OldValue, c.NewValue)
			}
		}
	}
	if !found {
		t.Error("expected lib-a license modified change")
	}
}

func TestDiff_SupplierModified(t *testing.T) {
	old := &Document{Format: "spdx", Packages: []Package{
		{Name: "lib-a", Version: "1.0.0", Supplier: "Acme Inc."},
	}}
	new := &Document{Format: "spdx", Packages: []Package{
		{Name: "lib-a", Version: "1.0.0", Supplier: "New Corp."},
	}}

	result := Diff(old, new)
	found := false
	for _, c := range result.Changes {
		if c.Package == "lib-a" && c.Type == PackageModified && c.Field == "supplier" {
			found = true
			if c.OldValue != "Acme Inc." || c.NewValue != "New Corp." {
				t.Errorf("expected Acme Inc.->New Corp., got %s->%s", c.OldValue, c.NewValue)
			}
		}
	}
	if !found {
		t.Error("expected lib-a supplier modified change")
	}
}

func TestDiff_MultipleChanges(t *testing.T) {
	old := &Document{Format: "spdx", Packages: []Package{
		{Name: "lib-a", Version: "1.0.0", License: "MIT"},
		{Name: "lib-b", Version: "1.0.0"},
	}}
	new := &Document{Format: "cyclonedx", Packages: []Package{
		{Name: "lib-a", Version: "2.0.0", License: "Apache-2.0"},
		{Name: "lib-c", Version: "3.0.0"},
	}}

	result := Diff(old, new)
	if result.OldFormat != "spdx" || result.NewFormat != "cyclonedx" {
		t.Errorf("unexpected formats: old=%q new=%q", result.OldFormat, result.NewFormat)
	}
	// lib-a: version + license modified
	// lib-b: removed
	// lib-c: added
	if len(result.Changes) != 4 {
		t.Errorf("expected 4 changes, got %d: %v", len(result.Changes), result.Changes)
	}
}

func TestDiff_EmptyDocuments(t *testing.T) {
	old := &Document{Format: "spdx"}
	new := &Document{Format: "spdx"}
	result := Diff(old, new)
	if len(result.Changes) != 0 {
		t.Errorf("expected 0 changes for empty documents, got %d", len(result.Changes))
	}
}
