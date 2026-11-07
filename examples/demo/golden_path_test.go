package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/lock"
)

// TestDemoGoldenPathIdentity holds gift-cards 0.1.0 to the bundles it names: its
// declared origin digest is the content identity of the http-service golden
// path (an edit to the path that skips gift-cards would unlink it in the fleet),
// and its lock pins postgresql 16.1.0's content.
func TestDemoGoldenPathIdentity(t *testing.T) {
	c, _ := readBundle(t, filepath.Join("bundles", "gift-cards", "v0.1.0"))
	if c.Service.Origin == nil {
		t.Fatal("gift-cards declares no service.origin")
	}
	if want := hashBundle(t, filepath.Join("bundles", "http-service", "v1.0.0")); c.Service.Origin.Digest != want {
		t.Errorf("origin digest %s, want %s (lock.HashFS of http-service 1.0.0): update gift-cards", c.Service.Origin.Digest, want)
	}
	l, err := lock.ReadFS(os.DirFS(filepath.Join("bundles", "gift-cards", "v0.1.0")))
	if err != nil || l == nil {
		t.Fatalf("read gift-cards 0.1.0 pacto.lock: %v", err)
	}
	want := hashBundle(t, filepath.Join("bundles", "postgresql", "v16.1.0"))
	for _, r := range l.References {
		if r.Kind == "config" && r.Name == "database" && r.ContentHash != want {
			t.Errorf("contentHash %s, want %s (lock.HashFS of postgresql 16.1.0): update the lock", r.ContentHash, want)
		}
	}
}

// TestDemoGoldenPathInTheFleet builds the fleet `pacto fleet --local
// examples/demo/bundles` builds: http-service is a golden path, gift-cards links
// back to it and postgresql's offering consumers leave the path out.
func TestDemoGoldenPathInTheFleet(t *testing.T) {
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleetsrc.NewLocalSource("local", "bundles"))
	if err != nil {
		t.Fatal(err)
	}
	path := fleet.NewServiceKey("http-service")
	if s := snap.Services[path]; s == nil || !s.Template {
		t.Fatalf("http-service is not a golden path: %+v", s)
	}
	gc := snap.Services[fleet.NewServiceKey("gift-cards")]
	if gc == nil || gc.Origin == nil || gc.Origin.GoldenPath != path {
		t.Fatalf("gift-cards origin does not resolve to http-service: %+v", gc)
	}
	d, err := fleet.NewQuery(snap).EntityDetail(fleet.KindOffering, "postgresql")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range d.Offering.Consumers.Items {
		if u.Consumer.Key == string(path) {
			t.Errorf("http-service is an offering consumer of postgresql: %+v", u)
		}
	}
}
