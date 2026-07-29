package fleet

import (
	"context"
	"errors"
	"testing"
)

func TestMemorySourceIDKind(t *testing.T) {
	s := NewMemorySource("oci", "cache", &Collection{})
	if s.ID() != "oci" || s.Kind() != "cache" {
		t.Errorf("ID/Kind = %q/%q", s.ID(), s.Kind())
	}
}

func TestMemorySourceCollect_ReturnsCollection(t *testing.T) {
	col := &Collection{Revisions: []RawRevision{{}}}
	s := NewMemorySource("m", "mem", col)
	got, err := s.Collect(context.Background())
	if err != nil || got != col {
		t.Fatalf("Collect = %v, %v", got, err)
	}
}

func TestMemorySourceCollect_NilCollection(t *testing.T) {
	s := NewMemorySource("m", "mem", nil)
	got, err := s.Collect(context.Background())
	if err != nil || got == nil {
		t.Fatalf("Collect nil col = %v, %v", got, err)
	}
	if len(got.Revisions) != 0 || len(got.Targets) != 0 {
		t.Errorf("expected empty collection, got %+v", got)
	}
}

func TestMemorySourceCollect_Error(t *testing.T) {
	sentinel := errors.New("boom")
	s := NewFailingSource("bad", "oci", sentinel)
	got, err := s.Collect(context.Background())
	if !errors.Is(err, sentinel) || got != nil {
		t.Fatalf("Collect = %v, %v", got, err)
	}
}

func TestMemorySourceCollect_ContextCancelled(t *testing.T) {
	s := NewMemorySource("m", "mem", &Collection{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := s.Collect(ctx)
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("Collect cancelled = %v, %v", got, err)
	}
}

func TestMemorySourceCollect_WithCollectFunc(t *testing.T) {
	want := &Collection{Targets: []RawTarget{{Name: "x"}}}
	s := NewMemorySource("m", "mem", nil).WithCollectFunc(
		func(ctx context.Context) (*Collection, error) { return want, nil },
	)
	got, err := s.Collect(context.Background())
	if err != nil || got != want {
		t.Fatalf("Collect = %v, %v", got, err)
	}
}
