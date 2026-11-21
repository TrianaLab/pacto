package catalog

import "testing"

// TestCatalogByNameIsPartial: a dependency by name (no ref, not external) is
// never fetched and leaves the closure partial with DEPENDENCY_BY_NAME.
func TestCatalogByNameIsPartial(t *testing.T) {
	f := newFake().ok("reg/root:1", rev(at("root", "bn"), ct("root", "1.0.0", dep("ledger", ""))))

	c := build(t, f, Bounds{}, "reg/root:1")

	m := c.Meta()
	if f.count() != 1 || m.Completeness != CompletenessPartial || len(m.Limitations) != 1 ||
		m.Limitations[0].Code != LimitationDependencyByName || m.Limitations[0].Message != "root depends on ledger by name; the closure cannot follow it without a ref" {
		t.Fatalf("resolver calls = %d, meta = %+v", f.count(), m)
	}
}
