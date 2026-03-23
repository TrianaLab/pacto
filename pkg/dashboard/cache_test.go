package dashboard

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestMemoryCache_SetAndGet(t *testing.T) {
	c := NewMemoryCache()
	c.Set("key", "value", time.Minute)

	v, ok := c.Get("key")
	if !ok {
		t.Fatal("expected key to exist")
	}
	if v != "value" {
		t.Fatalf("expected 'value', got %v", v)
	}
}

func TestMemoryCache_Miss(t *testing.T) {
	c := NewMemoryCache()
	_, ok := c.Get("missing")
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestMemoryCache_Expiry(t *testing.T) {
	c := NewMemoryCache()
	c.Set("key", "value", time.Nanosecond)

	// Wait for expiry
	time.Sleep(time.Millisecond)

	_, ok := c.Get("key")
	if ok {
		t.Fatal("expected cache entry to expire")
	}
}

func TestCachedDataSource_ListServices(t *testing.T) {
	inner := &stubSource{
		services: []Service{{Name: "svc", Version: "1.0.0", Source: "local"}},
	}
	cache := NewMemoryCache()
	cached := NewCachedDataSource(inner, cache, time.Minute, "test:")

	ctx := context.Background()

	// First call: cache miss, hits inner source
	services, err := cached.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}

	// Second call: should come from cache
	services, err = cached.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service from cache, got %d", len(services))
	}
}

func TestCachedDataSource_GetService(t *testing.T) {
	inner := &stubSource{
		details: map[string]*ServiceDetails{
			"svc": {Service: Service{Name: "svc", Version: "1.0.0"}},
		},
	}
	cache := NewMemoryCache()
	cached := NewCachedDataSource(inner, cache, time.Minute, "test:")

	ctx := context.Background()

	details, err := cached.GetService(ctx, "svc")
	if err != nil {
		t.Fatal(err)
	}
	if details.Name != "svc" {
		t.Errorf("expected name 'svc', got %q", details.Name)
	}

	// Second call hits cache
	details, err = cached.GetService(ctx, "svc")
	if err != nil {
		t.Fatal(err)
	}
	if details.Name != "svc" {
		t.Errorf("expected name 'svc' from cache, got %q", details.Name)
	}
}

func TestCachedDataSource_GetVersions(t *testing.T) {
	inner := &stubSource{
		versions: map[string][]Version{
			"svc": {{Version: "1.0.0"}, {Version: "2.0.0"}},
		},
	}
	cache := NewMemoryCache()
	cached := NewCachedDataSource(inner, cache, time.Minute, "test:")

	ctx := context.Background()

	versions, err := cached.GetVersions(ctx, "svc")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}

	// Second call hits cache
	versions, err = cached.GetVersions(ctx, "svc")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions from cache, got %d", len(versions))
	}
}

func TestCachedDataSource_GetDiff(t *testing.T) {
	inner := &stubSource{}
	cache := NewMemoryCache()
	cached := NewCachedDataSource(inner, cache, time.Minute, "test:")

	ctx := context.Background()
	a := Ref{Name: "svc", Version: "1.0.0"}
	b := Ref{Name: "svc", Version: "2.0.0"}

	result, err := cached.GetDiff(ctx, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification != "NON_BREAKING" {
		t.Errorf("expected NON_BREAKING, got %q", result.Classification)
	}

	// Second call hits cache
	result, err = cached.GetDiff(ctx, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification != "NON_BREAKING" {
		t.Errorf("expected NON_BREAKING from cache, got %q", result.Classification)
	}
}

// errorStubSource is a stub that returns errors for all operations.
type errorStubSource struct{}

func (e *errorStubSource) ListServices(_ context.Context) ([]Service, error) {
	return nil, fmt.Errorf("list error")
}
func (e *errorStubSource) GetService(_ context.Context, _ string) (*ServiceDetails, error) {
	return nil, fmt.Errorf("get error")
}
func (e *errorStubSource) GetVersions(_ context.Context, _ string) ([]Version, error) {
	return nil, fmt.Errorf("versions error")
}
func (e *errorStubSource) GetDiff(_ context.Context, _, _ Ref) (*DiffResult, error) {
	return nil, fmt.Errorf("diff error")
}

func TestCachedDataSource_ListServices_Error(t *testing.T) {
	cached := NewCachedDataSource(&errorStubSource{}, NewMemoryCache(), time.Minute, "err:")
	_, err := cached.ListServices(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCachedDataSource_GetService_Error(t *testing.T) {
	cached := NewCachedDataSource(&errorStubSource{}, NewMemoryCache(), time.Minute, "err:")
	_, err := cached.GetService(context.Background(), "svc")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCachedDataSource_GetVersions_Error(t *testing.T) {
	cached := NewCachedDataSource(&errorStubSource{}, NewMemoryCache(), time.Minute, "err:")
	_, err := cached.GetVersions(context.Background(), "svc")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCachedDataSource_GetDiff_Error(t *testing.T) {
	cached := NewCachedDataSource(&errorStubSource{}, NewMemoryCache(), time.Minute, "err:")
	_, err := cached.GetDiff(context.Background(), Ref{Name: "a"}, Ref{Name: "b"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCachedDataSource_PrefixIsolation(t *testing.T) {
	source1 := &stubSource{
		services: []Service{{Name: "from-source1", Version: "1.0.0"}},
	}
	source2 := &stubSource{
		services: []Service{{Name: "from-source2", Version: "2.0.0"}},
	}

	shared := NewMemoryCache()
	cached1 := NewCachedDataSource(source1, shared, time.Minute, "s1:")
	cached2 := NewCachedDataSource(source2, shared, time.Minute, "s2:")

	ctx := context.Background()

	// Populate cache for both
	svcs1, _ := cached1.ListServices(ctx)
	svcs2, _ := cached2.ListServices(ctx)

	if svcs1[0].Name != "from-source1" {
		t.Errorf("expected 'from-source1', got %q", svcs1[0].Name)
	}
	if svcs2[0].Name != "from-source2" {
		t.Errorf("expected 'from-source2', got %q", svcs2[0].Name)
	}

	// Re-read from cache to verify no collision
	svcs1, _ = cached1.ListServices(ctx)
	svcs2, _ = cached2.ListServices(ctx)

	if svcs1[0].Name != "from-source1" {
		t.Errorf("cache collision: expected 'from-source1', got %q", svcs1[0].Name)
	}
	if svcs2[0].Name != "from-source2" {
		t.Errorf("cache collision: expected 'from-source2', got %q", svcs2[0].Name)
	}
}
