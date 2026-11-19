package cli

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/pactify"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// stubCluster makes pacto init --cluster return b and remembers the options
// it was given.
func stubCluster(t *testing.T, b *pactify.Bulk) *pactify.ClusterOptions {
	t.Helper()
	oldDyn, oldCluster := initDynamic, initCluster
	t.Cleanup(func() { initDynamic, initCluster = oldDyn, oldCluster })
	initDynamic = func() (dynamic.Interface, error) {
		return dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), nil
	}
	got := &pactify.ClusterOptions{}
	initCluster = func(_ context.Context, _ dynamic.Interface, _ pactify.Options, c pactify.ClusterOptions,
		_ func(string, []byte, os.FileMode) error) (*pactify.Bulk, error) {
		*got = c
		return b, nil
	}
	return got
}

// TestInitClusterFlags: the bound, the window and the token reach the read,
// and each is refused without --cluster.
func TestInitClusterFlags(t *testing.T) {
	got := stubCluster(t, &pactify.Bulk{})
	if out, err := runInit(t, "--cluster", "--out", "drafts", "--max-items", "7", "--call-window", "2h", "--continue", "tok"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.MaxItems != 7 || got.CallWindow != 2*time.Hour || got.Continue != "tok" || got.Out != "drafts" {
		t.Fatalf("options %+v", *got)
	}
	stubCluster(t, &pactify.Bulk{Continue: "next"})
	out, err := runInit(t, "--cluster", "--out", "drafts", "--continue", "tok")
	if err != nil || !strings.Contains(out, "Continue: pacto init --cluster --out drafts --continue next\n") {
		t.Fatalf("the resume command keeps the old token: %v\n%s", err, out)
	}
	for _, args := range [][]string{{"--max-items", "7"}, {"--call-window", "2h"}, {"--continue", "tok"}} {
		if _, err := runInit(t, args...); err == nil || !strings.Contains(err.Error(), "go with --cluster") {
			t.Errorf("%v: %v", args, err)
		}
	}
}

// TestPrintBulkCoverageHiddenContinue: after the table, the sources not read
// whole, the counts of what may not be read and the command that resumes.
func TestPrintBulkCoverageHiddenContinue(t *testing.T) {
	stubCluster(t, &pactify.Bulk{
		Coverage: []contract.InferredCoverage{
			{Source: "workloads", State: contract.CoveragePartial, Reason: "stopped at the item bound (1)"},
			{Source: "pods", State: contract.CoverageComplete},
			{Source: "endpointslices", State: "failed", Reason: "forbidden"},
		},
		Results: []*pactify.Result{
			{Service: "a", Inferred: &contract.InferredRecord{Hidden: &contract.InferredHidden{Services: 1, Callers: 2}}},
			{Service: "b", Inferred: &contract.InferredRecord{Hidden: &contract.InferredHidden{Callers: 1}}},
			{Service: "c", Inferred: &contract.InferredRecord{}},
		},
		Continue: "eyJ0b2tlbiI6MX0",
	})
	out, err := runInit(t, "--cluster", "--namespace", "shop", "--out", "my drafts", "--max-items", "1", "--plan=false")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"\nCoverage: workloads partial (stopped at the item bound (1)), endpointslices failed (forbidden)\n",
		"\nHidden: 3 callers and 1 services you may not read\n",
		"\nContinue: pacto init --cluster --max-items 1 --namespace shop --out 'my drafts' --plan=false --continue eyJ0b2tlbiI6MX0\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// TestPrintBulkCompleteCoverageSilent: a read that saw everything, hid
// nothing and finished prints no coverage, hidden or continue line.
func TestPrintBulkCompleteCoverageSilent(t *testing.T) {
	stubCluster(t, &pactify.Bulk{Coverage: []contract.InferredCoverage{{Source: "workloads", State: contract.CoverageComplete}}})
	out, err := runInit(t, "--cluster", "--out", "drafts")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"Coverage:", "Hidden:", "Continue:"} {
		if strings.Contains(out, line) {
			t.Errorf("output holds %q:\n%s", line, out)
		}
	}
}

// TestInitResultPrintsClusterFieldFacts: a cluster field says when it was
// read, whether it is partial and the range proposed for it.
func TestInitResultPrintsClusterFieldFacts(t *testing.T) {
	var buf strings.Builder
	printDiscovered(&buf, &app.InitResult{Service: "invoices", Inferred: &contract.InferredRecord{Fields: []contract.Inferred{
		{Field: "dependencies[ledger].compatibility", Value: "compatibility unknown", Source: "cluster:fleet reported versions 2.3.1",
			AsOf: "2026-11-14T09:58:12Z", Completeness: contract.CoveragePartial, Proposed: "^2.3.0"},
		{Field: "image.digest[app]", Value: "sha256:ab", Source: "cluster:runtime pods of shop/Deployment/invoices",
			AsOf: "2026-11-14T10:02:00Z", Completeness: contract.CoverageComplete},
	}}})
	for _, want := range []string{
		"compatibility unknown from cluster:fleet reported versions 2.3.1 (read from the cluster 2026-11-14T09:58:12Z, partial, proposed ^2.3.0)\n",
		"sha256:ab from cluster:runtime pods of shop/Deployment/invoices (read from the cluster 2026-11-14T10:02:00Z)\n",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, buf.String())
		}
	}
}

// TestShellWord: a plain word stays bare; anything else is single-quoted.
func TestShellWord(t *testing.T) {
	for in, want := range map[string]string{"shop": "shop", "": "''", "a b": "'a b'", "it's": `'it'\''s'`} {
		if got := shellWord(in); got != want {
			t.Errorf("shellWord(%q) = %q, want %q", in, got, want)
		}
	}
}
