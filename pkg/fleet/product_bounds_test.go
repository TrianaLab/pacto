package fleet

import (
	"errors"
	"fmt"
	"testing"
)

// Every product answer must be genuinely bounded: no answer may carry an
// unbounded copy of snapshot state, every page reports typed page metadata, and
// negative inputs are rejected rather than silently converted to a default
// (requirement: bounded product responses).

func TestBoundSources_CapsAndPrioritizesUnhealthy(t *testing.T) {
	// Under the cap: unchanged, not truncated.
	few := []SourceState{{ID: "a", Status: SourceAvailable}, {ID: "b", Status: SourceStale}}
	got, trunc := boundSources(few)
	if trunc || len(got) != 2 || got[0].ID != "a" {
		t.Errorf("under-cap sources must be returned verbatim: got=%v trunc=%v", got, trunc)
	}

	// Over the cap: capped to MaxMetaSources, truncated, unhealthy first. Cycle
	// through every status so the health ranking is fully exercised.
	statuses := []SourceStatus{SourceAvailable, SourceStale, SourcePartial, SourceUnavailable}
	var many []SourceState
	for i := 0; i < MaxMetaSources+10; i++ {
		many = append(many, SourceState{ID: fmt.Sprintf("s%03d", i), Status: statuses[i%len(statuses)]})
	}
	capped, trunc := boundSources(many)
	if !trunc {
		t.Fatal("over-cap sources must report truncation")
	}
	if len(capped) != MaxMetaSources {
		t.Fatalf("sources not capped: %d", len(capped))
	}
	if capped[0].Status != SourceUnavailable {
		t.Errorf("truncation must keep the unhealthy source first, got %q", capped[0].Status)
	}
}

func TestBoundLimitations_Caps(t *testing.T) {
	few := []Limitation{{Code: "X"}}
	got, trunc := boundLimitations(few)
	if trunc || len(got) != 1 {
		t.Errorf("under-cap limitations verbatim: %v trunc=%v", got, trunc)
	}
	var many []Limitation
	for i := 0; i < MaxMetaLimitations+5; i++ {
		many = append(many, Limitation{Code: "L"})
	}
	capped, trunc := boundLimitations(many)
	if !trunc || len(capped) != MaxMetaLimitations {
		t.Errorf("limitations not capped: len=%d trunc=%v", len(capped), trunc)
	}
}

func TestProductMeta_UnderCapNotTruncated(t *testing.T) {
	q := productFleet(t)
	m := q.ProductMeta()
	if m.SourcesTruncated || m.LimitationsTruncated {
		t.Errorf("a small fleet must not report truncation: %+v", m)
	}
	if len(m.Sources) == 0 {
		t.Error("expected the fixture's sources in the meta")
	}
}

func TestEntities_PageMetadata(t *testing.T) {
	q := productFleet(t)
	full, err := q.Entities(EntityFilter{})
	if err != nil {
		t.Fatal(err)
	}
	total := full.Total
	if total < 4 {
		t.Fatalf("fixture too small to page: %d", total)
	}

	// First page of two.
	p1, err := q.Entities(EntityFilter{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if p1.Limit != 2 || p1.Offset != 0 || p1.Count != 2 || p1.Total != total {
		t.Errorf("page-1 metadata wrong: %+v", *p1)
	}
	if !p1.Truncated || p1.NextOffset == nil || *p1.NextOffset != 2 {
		t.Errorf("page-1 must report truncation and nextOffset=2: %+v", *p1)
	}

	// Walk every page and prove pagination stability (no dupes, no gaps, same
	// order as the unpaged list).
	var walked []string
	for off := 0; ; off += 2 {
		p, err := q.Entities(EntityFilter{Limit: 2, Offset: off})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range p.Entities {
			walked = append(walked, string(e.Kind)+"/"+e.Key)
		}
		if p.NextOffset == nil {
			if p.Truncated {
				t.Error("last page must not be truncated")
			}
			break
		}
	}
	if len(walked) != total {
		t.Fatalf("paged walk saw %d entities, want %d", len(walked), total)
	}
	for i, e := range full.Entities {
		if walked[i] != string(e.Kind)+"/"+e.Key {
			t.Fatalf("pagination unstable at %d: %q vs %q", i, walked[i], string(e.Kind)+"/"+e.Key)
		}
	}
}

func TestAttention_RejectsNegativeLimit(t *testing.T) {
	q := productFleet(t)
	if _, err := q.Attention(AttentionFilter{Limit: -1}); err == nil {
		t.Error("a negative attention limit must be rejected, not silently defaulted")
	}
	var iqe *InvalidQueryError
	if _, err := q.Attention(AttentionFilter{Kind: "bogus"}); !errors.As(err, &iqe) {
		t.Errorf("an invalid attention kind must be a typed InvalidQueryError, got %v", err)
	}
	if _, err := q.Attention(AttentionFilter{Status: "bogus"}); !errors.As(err, &iqe) {
		t.Errorf("an invalid attention status must be a typed InvalidQueryError, got %v", err)
	}
}

func TestAttention_CapsAndReportsLimit(t *testing.T) {
	q := productFleet(t)
	list, err := q.Attention(AttentionFilter{Limit: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	if list.Limit != MaxAttentionLimit {
		t.Errorf("an excessive attention limit must be capped to %d, got %d", MaxAttentionLimit, list.Limit)
	}
	// A default (unset) limit is reported too.
	def, err := q.Attention(AttentionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if def.Limit != DefaultAttentionLimit {
		t.Errorf("default attention limit not reported: %d", def.Limit)
	}
}
