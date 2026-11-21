package fleet

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
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
	s := withCollect(NewMemorySource("m", "mem", nil),
		func(ctx context.Context) (*Collection, error) { return want, nil },
	)
	got, err := s.Collect(context.Background())
	if err != nil || got != want {
		t.Fatalf("Collect = %v, %v", got, err)
	}
}

// withCollect overrides Collect with a custom function, e.g. to honor context
// cancellation in tests.
func withCollect(s *MemorySource, fn func(ctx context.Context) (*Collection, error)) *MemorySource {
	s.collect = fn
	return s
}

func TestStaleStateSanitizes(t *testing.T) {
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	st := StaleState(at, errors.New(`Get "https://10.0.0.1:6443/apis?token=s3cr3t": dial tcp: connection refused`))
	if st.Status != SourceStale || st.LastSuccessfulSync == nil || !st.LastSuccessfulSync.Equal(at) {
		t.Fatalf("state = %+v", st)
	}
	if st.Error == nil || st.Error.Code != "UNAVAILABLE" || strings.Contains(st.Error.Message, "10.0.0.1") || strings.Contains(st.Error.Message, "s3cr3t") {
		t.Fatalf("error = %+v, want the sanitized category only", st.Error)
	}
}
