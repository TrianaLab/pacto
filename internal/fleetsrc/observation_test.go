package fleetsrc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const observationTrace = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"web"}}]},
  "scopeSpans":[{"spans":[
    {"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"payments"}}]},
    {"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"payments"}}]}
  ]}]}]}`

// secretTrace is a perfectly parseable trace that lives OUTSIDE any declared
// root. It names an endpoint nothing else does, so if a rooted read ever consumed
// it, the resulting edge would say so.
const secretTrace = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"stolen"}}]},
  "scopeSpans":[{"spans":[
    {"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"exfiltrated"}}]}
  ]}]}]}`

func TestObservationSource_IDAndKind(t *testing.T) {
	if got := NewObservationSource("", "", "/x").ID(); got != "observation" {
		t.Errorf("default id = %q, want observation", got)
	}
	if got := NewObservationSource("otel-eu", "", "/x").ID(); got != "otel-eu" {
		t.Errorf("custom id = %q, want otel-eu", got)
	}
	if got := NewObservationSource("", "", "/x").Kind(); got != "observation" {
		t.Errorf("kind = %q, want observation", got)
	}
}

func TestObservationSource_Collect(t *testing.T) {
	path := writeFixture(t, "traces.json", observationTrace)
	col, err := NewObservationSource("otel", "", path).Collect(context.Background())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	// Two spans to the same peer collapse to one edge with count 2.
	if len(col.Observed) != 1 {
		t.Fatalf("observed edges = %+v, want 1", col.Observed)
	}
	e := col.Observed[0]
	if e.From != "web" || e.To != "payments" || e.Count != 2 {
		t.Errorf("edge = %+v, want web->payments count 2", e)
	}
}

func TestObservationSource_MissingFile(t *testing.T) {
	if _, err := NewObservationSource("", "", filepath.Join(t.TempDir(), "missing.json")).Collect(context.Background()); err == nil {
		t.Fatal("expected an error for a missing trace file")
	}
}

func TestObservationSource_BadJSON(t *testing.T) {
	path := writeFixture(t, "bad.json", "{not json")
	if _, err := NewObservationSource("", "", path).Collect(context.Background()); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestObservationSource_ContextCancelled(t *testing.T) {
	path := writeFixture(t, "traces.json", observationTrace)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewObservationSource("", "", path).Collect(ctx); err == nil {
		t.Fatal("expected a context-cancelled error")
	}
}

// rootedLayout builds a real filesystem shaped like an operator-managed
// observation mount that someone has tampered with, and returns the declared root
// and the directory outside it.
//
// Real symlinks, not a fake filesystem: the whole question is what the kernel does
// when a path is resolved, so anything that models that instead of performing it
// would be testing the model.
func rootedLayout(t *testing.T) (root, outside string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "mount")
	outside = filepath.Join(base, "outside")
	for _, dir := range []string{root, outside, filepath.Join(root, "nested")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, name string) {
		if err := os.Symlink(target, name); err != nil {
			t.Fatal(err)
		}
	}

	write(filepath.Join(root, "traces.json"), observationTrace)
	write(filepath.Join(root, "nested", "traces.json"), observationTrace)
	write(filepath.Join(outside, "secret.json"), secretTrace)

	// The counterexample: the declared file is a symlink out of the mount, exactly
	// as a tampered-with export volume could contain.
	link(filepath.Join(outside, "secret.json"), filepath.Join(root, "escape.json"))
	// The same escape one level up: an intermediate DIRECTORY leaves the mount, so
	// a lexically innocent relative path resolves outside it.
	link(outside, filepath.Join(root, "elsewhere"))
	// A projected Kubernetes ConfigMap volume: the visible file is a symlink into a
	// versioned "..data" directory, itself a symlink to a timestamped one. Legal,
	// entirely inside the mount, and must keep working.
	const versioned = "..2026_08_12_09_00_00.1234"
	if err := os.MkdirAll(filepath.Join(root, versioned), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(root, versioned, "projected.json"), observationTrace)
	link(versioned, filepath.Join(root, "..data"))
	link("..data/projected.json", filepath.Join(root, "projected.json"))
	return root, outside
}

// TestObservationSource_RootedReadStaysInsideItsMount is the path-safety
// acceptance. A source declaring a root reads inside it and nowhere else, however
// the storage it was handed is arranged. Lexical validation upstream cannot
// establish this: every escaping case here is a perfectly ordinary relative path.
func TestObservationSource_RootedReadStaysInsideItsMount(t *testing.T) {
	root, _ := rootedLayout(t)

	for _, tc := range []struct {
		name string
		file string
		ok   bool
	}{
		{"ordinary file at the root", "traces.json", true},
		{"nested ordinary file", "nested/traces.json", true},
		{"symlink within the root", "projected.json", true},
		{"file symlink leaving the root", "escape.json", false},
		{"intermediate directory symlink leaving the root", "elsewhere/secret.json", false},
		{"relative traversal out of the root", "../outside/secret.json", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			col, err := NewObservationSource("orders", root, tc.file).Collect(context.Background())
			if tc.ok {
				if err != nil {
					t.Fatalf("a legitimate read inside the mount failed: %v", err)
				}
				if len(col.Observed) != 1 || col.Observed[0].To != "payments" {
					t.Fatalf("observed = %+v, want the mounted export's edge", col.Observed)
				}
				return
			}
			if err == nil {
				t.Fatalf("the read left the declared root and returned %+v", col.Observed)
			}
			// A rejection, not a parse failure: nothing outside the root was read.
			if !strings.Contains(err.Error(), "escapes") {
				t.Errorf("error = %v, want a root-escape rejection", err)
			}
		})
	}
}

// TestObservationSource_UnrootedReadFollowsTheGivenPath keeps the ad-hoc command
// line working: `pacto dashboard --traces PATH` names whatever the path names,
// symlink or not, because there is no declared root for it to escape from.
func TestObservationSource_UnrootedReadFollowsTheGivenPath(t *testing.T) {
	root, outside := rootedLayout(t)
	// The same symlink the rooted source refuses.
	col, err := NewObservationSource("", "", filepath.Join(root, "escape.json")).Collect(context.Background())
	if err != nil {
		t.Fatalf("an ad-hoc path that happens to be a symlink must still work: %v", err)
	}
	if len(col.Observed) != 1 || col.Observed[0].From != "stolen" {
		t.Fatalf("observed = %+v, want the linked file's edge", col.Observed)
	}
	if _, err := os.Stat(filepath.Join(outside, "secret.json")); err != nil {
		t.Fatalf("fixture: %v", err)
	}
}

// TestObservationSource_UnopenableRootIsASourceError proves a root that cannot be
// opened at all — the mount never materialized — fails the source rather than
// falling back to an unrooted read.
func TestObservationSource_UnopenableRootIsASourceError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never-mounted")
	if _, err := NewObservationSource("orders", missing, "traces.json").Collect(context.Background()); err == nil {
		t.Fatal("expected an error for a root that does not exist")
	}
}
