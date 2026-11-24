package fleet

import (
	"slices"
	"testing"
)

// TestSortAttentionBreaksLabelTies: two services of one name in two domains
// tie on severity, category and label; the order still does not depend on the
// order they were collected in.
func TestSortAttentionBreaksLabelTies(t *testing.T) {
	a := AttentionItem{Severity: "warning", Category: "unknown", Label: "app-config", Entity: EntityRef{Key: "local/app-config"}, Code: "X"}
	b := AttentionItem{Severity: "warning", Category: "unknown", Label: "app-config", Entity: EntityRef{Key: "partners/app-config"}, Code: "X"}
	c := b
	c.Code = "Y"
	want := []AttentionItem{a, b, c}
	for _, in := range [][]AttentionItem{{c, b, a}, {b, a, c}, {a, c, b}} {
		sortAttention(in)
		if !slices.EqualFunc(in, want, func(x, y AttentionItem) bool { return x.Entity.Key == y.Entity.Key && x.Code == y.Code }) {
			t.Fatalf("got %+v", in)
		}
	}
}
