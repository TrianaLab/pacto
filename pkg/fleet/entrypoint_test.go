package fleet

import (
	"strings"
	"testing"
)

// attentionServices walks every page of the unfiltered attention list and
// returns the distinct population services it names.
func attentionServices(t *testing.T, q *Query) (map[ServiceKey]bool, int) {
	t.Helper()
	seen := map[ServiceKey]bool{}
	total := 0
	for off := 0; ; {
		al, err := q.Attention(AttentionFilter{Limit: MaxAttentionLimit, Offset: off})
		if err != nil {
			t.Fatal(err)
		}
		total = al.Total
		for _, it := range al.Items {
			if q.goldenPath(ServiceKey(it.Service)) {
				t.Errorf("attention lists golden path %s", it.Service)
			}
			if q.populationService(ServiceKey(it.Service)) {
				seen[ServiceKey(it.Service)] = true
			}
		}
		if al.NextOffset == nil {
			return seen, total
		}
		off = *al.NextOffset
	}
}

func leadTile(t *testing.T, ov *Overview) EntryPoint {
	t.Helper()
	for _, ep := range ov.EntryPoints {
		if ep.Label == "Services needing attention" {
			return ep
		}
	}
	t.Fatal("no lead tile")
	return EntryPoint{}
}

func TestNeedingAttentionTileCountsDistinctServicesInItsList(t *testing.T) {
	for name, q := range map[string]*Query{
		"product": productFleet(t), // stale, unknown, non-compliant, invalid, unresolved, undeclared
		"domain":  domainFleet(t),  // unowned and knowledge-only services
		"golden":  goldenFleet(t),  // golden paths never count
	} {
		t.Run(name, func(t *testing.T) {
			seen, _ := attentionServices(t, q)
			if got := q.Overview().Summary.ServicesNeedingAttention; got != len(seen) {
				t.Errorf("tile = %d, distinct services in its list = %d (%v)", got, len(seen), seen)
			}
		})
	}
}

func TestLeadTileItemCount(t *testing.T) {
	q := domainFleet(t)
	_, total := attentionServices(t, q)
	ov := q.Overview()
	if lead := leadTile(t, ov); lead.ItemCount != total || lead.ItemCount != ov.Attention.Total || lead.Count == 0 {
		t.Errorf("lead tile = %+v, attention total %d", lead, total)
	}
	for _, ep := range ov.EntryPoints {
		if ep.Label != "Services needing attention" && ep.ItemCount != 0 {
			t.Errorf("only the lead tile carries itemCount, got %+v", ep)
		}
	}
}

func TestSourceHealthIncompleteFilter(t *testing.T) {
	q := productFleet(t) // local available; stale-oci, partial-oci and down incomplete
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindSource}, SourceHealth: SourceHealthIncomplete})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range l.Entities {
		got[r.Key] = r.Status
	}
	if len(got) != 3 || got["stale-oci"] != "stale" || got["partial-oci"] != "partial" || got["down"] != "unavailable" {
		t.Errorf("incomplete sources = %v", got)
	}
	// A value outside the five is still rejected, and incomplete matches no other kind.
	if _, err := q.Entities(EntityFilter{SourceHealth: "broken"}); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("bogus source health: %v", err)
	}
	if svc, _ := q.Entities(EntityFilter{Kinds: []EntityKind{KindSource, KindService}, SourceHealth: SourceHealthIncomplete}); svc != nil && svc.Total != 3 {
		t.Errorf("incomplete matched non-sources: %d", svc.Total)
	}
}

func TestIncompleteSourcesTileMatchesSourceList(t *testing.T) {
	q := productFleet(t)
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindSource}, SourceHealth: SourceHealthIncomplete})
	if err != nil {
		t.Fatal(err)
	}
	var tile *EntryPoint
	for _, ep := range q.Overview().EntryPoints {
		if ep.Label == "Incomplete sources" {
			tile = &ep
		}
	}
	if tile == nil || tile.Count != l.Total || tile.View != EntryPointSources || tile.Category != SourceHealthIncomplete {
		t.Fatalf("tile = %+v, list total %d", tile, l.Total)
	}
}
