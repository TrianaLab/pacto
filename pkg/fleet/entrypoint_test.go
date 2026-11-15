package fleet

import "testing"

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
