package mcp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/trianalab/pacto/v4/internal/pactify"
)

// stubPactifyCluster makes the cluster read return an empty Bulk, counts the
// reads and remembers the options of the last one.
func stubPactifyCluster(t *testing.T) (*pactify.ClusterOptions, *int) {
	t.Helper()
	oldDyn, oldCluster := newDynamic, pactifyCluster
	t.Cleanup(func() { newDynamic, pactifyCluster = oldDyn, oldCluster })
	newDynamic = func() (dynamic.Interface, error) { return dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), nil }
	got, reads := &pactify.ClusterOptions{}, new(int)
	pactifyCluster = func(_ context.Context, _ dynamic.Interface, _ pactify.Options, c pactify.ClusterOptions,
		_ func(string, []byte, os.FileMode) error) (*pactify.Bulk, error) {
		*got = c
		*reads++
		return &pactify.Bulk{Continue: "next"}, nil
	}
	return got, reads
}

// TestPactifyClusterBoundWindowContinue: max_items, call_window and continue
// reach the read, and the token it returns comes back as continue.
func TestPactifyClusterBoundWindowContinue(t *testing.T) {
	got, _ := stubPactifyCluster(t)
	res := callTool(t, "pacto_create", map[string]any{"name": "d", "path": "drafts", "pactify": true, "cluster": true,
		"max_items": 7, "call_window": "2h", "continue": "tok"})
	if res.IsError || !strings.Contains(resultText(t, res), `"continue": "next"`) {
		t.Fatalf("%s", resultText(t, res))
	}
	if got.MaxItems != 7 || got.CallWindow != 2*time.Hour || got.Continue != "tok" || got.Out != "drafts" {
		t.Fatalf("options %+v", *got)
	}
}

// TestPactifyClusterBadCallWindow: a call window that is not a duration is
// refused before any read.
func TestPactifyClusterBadCallWindow(t *testing.T) {
	_, reads := stubPactifyCluster(t)
	res := callTool(t, "pacto_create", map[string]any{"name": "d", "pactify": true, "cluster": true, "call_window": "a day"})
	if !res.IsError || !strings.Contains(resultText(t, res), `call_window "a day" is not a duration`) || *reads != 0 {
		t.Fatalf("reads=%d %s", *reads, resultText(t, res))
	}
}

// TestPactifyClusterArgsNeedCluster: each of the three without cluster is
// refused before anything is read.
func TestPactifyClusterArgsNeedCluster(t *testing.T) {
	_, reads := stubPactifyCluster(t)
	for name, v := range map[string]any{"max_items": 7, "call_window": "2h", "continue": "tok"} {
		res := callTool(t, "pacto_create", map[string]any{"name": "d", "path": t.TempDir(), "pactify": true, name: v})
		if !res.IsError || resultText(t, res) != "namespaces, max_items, call_window and continue go with cluster" || *reads != 0 {
			t.Errorf("%s: reads=%d %s", name, *reads, resultText(t, res))
		}
	}
}

// TestFromConflictsClusterArgs: each of the three sent with from is named.
func TestFromConflictsClusterArgs(t *testing.T) {
	f := &fakeInit{r: &pactify.Result{}}
	for name, v := range map[string]any{"max_items": 7, "call_window": "2h", "continue": "tok"} {
		text, isErr := callFrom(t, f.init, map[string]any{"from": "./path", "name": "a", "owner": "o", name: v})
		if !isErr || !strings.Contains(text, "does not take "+name) {
			t.Errorf("%s: isErr=%v %q", name, isErr, text)
		}
	}
}

// TestPactifyClusterNamespacesNeedCluster: namespaces without cluster is
// refused like the CLI's --namespace, before anything is read.
func TestPactifyClusterNamespacesNeedCluster(t *testing.T) {
	_, reads := stubPactifyCluster(t)
	res := callTool(t, "pacto_create", map[string]any{"name": "d", "path": t.TempDir(), "pactify": true, "namespaces": `["shop"]`})
	if !res.IsError || resultText(t, res) != "namespaces, max_items, call_window and continue go with cluster" || *reads != 0 {
		t.Fatalf("reads=%d %s", *reads, resultText(t, res))
	}
}

// TestClusterArgsNeedPactify: each cluster argument without pactify is
// refused instead of running an intent create that drops it.
func TestClusterArgsNeedPactify(t *testing.T) {
	_, reads := stubPactifyCluster(t)
	for name, v := range map[string]any{"cluster": true, "namespaces": `["shop"]`, "max_items": 7, "call_window": "2h", "continue": "tok"} {
		dir := t.TempDir()
		res := callTool(t, "pacto_create", map[string]any{"name": "d", "path": dir, name: v})
		if !res.IsError || resultText(t, res) != "cluster, namespaces, max_items, call_window and continue go with pactify" || *reads != 0 {
			t.Errorf("%s: reads=%d %s", name, *reads, resultText(t, res))
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("%s: wrote %v", name, entries)
		}
	}
}

// TestPactifyClusterArgLimits: a negative bound or a window under a minute
// is refused in the tool's own argument names, before any read.
func TestPactifyClusterArgLimits(t *testing.T) {
	_, reads := stubPactifyCluster(t)
	for want, args := range map[string]map[string]any{
		"max_items must not be negative":                   {"max_items": -1},
		`call_window "30s" is shorter than the 1m minimum`: {"call_window": "30s"},
	} {
		args["name"], args["pactify"], args["cluster"] = "d", true, true
		res := callTool(t, "pacto_create", args)
		if !res.IsError || resultText(t, res) != want || *reads != 0 || strings.Contains(resultText(t, res), "--") {
			t.Errorf("reads=%d %s", *reads, resultText(t, res))
		}
	}
}
