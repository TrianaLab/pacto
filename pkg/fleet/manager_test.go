package fleet

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

func memSource(id string) Source {
	c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "leaf-svc", Version: "1.0.0"}}
	b := &contract.Bundle{Contract: c, FS: fstest.MapFS{}}
	return NewMemorySource(id, "mem", &Collection{Revisions: []RawRevision{{Bundle: b, Digest: "sha256:" + id}}})
}

func okBuilder(id string) Builder {
	return func(ctx context.Context) (*FleetSnapshot, error) {
		return Build(ctx, BuildOptions{Now: fixedNow}, memSource(id))
	}
}

func TestManager_NoSnapshotBeforeRefresh(t *testing.T) {
	m := NewManager(okBuilder("a"), ManagerOptions{Now: fixedNow})
	if _, err := m.Current(); !errors.Is(err, ErrNoSnapshot) {
		t.Errorf("Current before refresh should be ErrNoSnapshot, got %v", err)
	}
	if _, err := m.Query(); !errors.Is(err, ErrNoSnapshot) {
		t.Errorf("Query before refresh should be ErrNoSnapshot, got %v", err)
	}
	st := m.Status()
	if st.HasSnapshot || st.Degraded || st.Refreshes != 0 {
		t.Errorf("initial status wrong: %+v", st)
	}
}

func TestManager_RefreshPublishesAtomically(t *testing.T) {
	m := NewManager(okBuilder("a"), ManagerOptions{Now: fixedNow})
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	snap, err := m.Current()
	if err != nil || snap == nil {
		t.Fatalf("expected a snapshot, got %v", err)
	}
	q, err := m.Query()
	if err != nil || q.SnapshotID() != snap.SnapshotID {
		t.Fatalf("query snapshot id mismatch: %v", err)
	}
	st := m.Status()
	if !st.HasSnapshot || st.Degraded || st.Refreshes != 1 || st.Failures != 0 || st.SnapshotID != snap.SnapshotID {
		t.Errorf("status after success wrong: %+v", st)
	}
}

func TestManager_FailedRefreshRetainsLastGood(t *testing.T) {
	var fail atomic.Bool
	build := func(ctx context.Context) (*FleetSnapshot, error) {
		if fail.Load() {
			return nil, errors.New("registry 401 unauthorized token=SECRET")
		}
		return okBuilder("a")(ctx)
	}
	m := NewManager(build, ManagerOptions{Now: fixedNow})
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	good, _ := m.Current()

	fail.Store(true)
	if err := m.Refresh(context.Background()); err == nil {
		t.Fatal("expected refresh error")
	}
	// Last good snapshot is still served.
	cur, err := m.Current()
	if err != nil || cur.SnapshotID != good.SnapshotID {
		t.Errorf("failed refresh should retain last good snapshot")
	}
	st := m.Status()
	if !st.Degraded || st.Failures != 1 || st.Error == nil {
		t.Errorf("status should be degraded with a sanitized error: %+v", st)
	}
	if st.Error.Message == "" || contains(st.Error.Message, "SECRET") {
		t.Errorf("refresh error must be sanitized, got %q", st.Error.Message)
	}
}

func TestManager_RefreshCoalesces(t *testing.T) {
	var builds atomic.Int64
	release := make(chan struct{})
	build := func(ctx context.Context) (*FleetSnapshot, error) {
		builds.Add(1)
		<-release // block until released so concurrent callers pile up
		return okBuilder("a")(ctx)
	}
	m := NewManager(build, ManagerOptions{Now: fixedNow})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = m.Refresh(context.Background()) }()
	}
	// Give goroutines time to coalesce onto one in-flight build.
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	if got := builds.Load(); got != 1 {
		t.Errorf("concurrent refreshes should coalesce to 1 build, got %d", got)
	}
}

func TestManager_WaiterHonorsContextCancellation(t *testing.T) {
	release := make(chan struct{})
	build := func(ctx context.Context) (*FleetSnapshot, error) {
		<-release
		return okBuilder("a")(ctx)
	}
	m := NewManager(build, ManagerOptions{Now: fixedNow})
	go func() { _ = m.Refresh(context.Background()) }() // holds the in-flight build
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Refresh(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("a waiter with a cancelled context should return the ctx error, got %v", err)
	}
	close(release)
}

func TestManager_StartPeriodicAndSingle(t *testing.T) {
	m := NewManager(okBuilder("a"), ManagerOptions{})
	// interval <= 0 → single initial refresh then return.
	m.Start(context.Background(), 0)
	if m.Status().Refreshes != 1 {
		t.Errorf("Start(0) should refresh once, got %d", m.Status().Refreshes)
	}

	m2 := NewManager(okBuilder("b"), ManagerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m2.Start(ctx, time.Millisecond); close(done) }()
	// Wait for more than one refresh, then stop.
	deadline := time.After(2 * time.Second)
	for m2.Status().Refreshes < 2 {
		select {
		case <-deadline:
			cancel()
			t.Fatal("periodic refresh did not run")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	<-done
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
