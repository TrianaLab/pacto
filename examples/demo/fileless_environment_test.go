package main

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// TestDemoFilelessEnvironment: the demo's pactoVersion 2.6 contract, kept
// outside the published bundles, declares pr-412 with no values file. It is
// valid, pr-412 adds no values of its own and so runs app on its defaults,
// and comparing it with production lists exactly the values production sets.
func TestDemoFilelessEnvironment(t *testing.T) {
	const dir = "testdata/contract-2.6"
	raw, err := os.ReadFile(dir + "/pacto.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := contract.Parse(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if c.PactoVersion != "2.6" || c.PactoVersion != contract.Current() {
		t.Fatalf("pactoVersion %q, newest %q", c.PactoVersion, contract.Current())
	}
	pr := c.Environments[1]
	if pr.Name != "pr-412" || pr.File != "" || pr.Lifetime != "temporary" {
		t.Fatalf("pr-412: %+v", pr)
	}
	fsys := os.DirFS(dir)
	if res := validation.Validate(c, raw, fsys); !res.IsValid() || len(res.Warnings) != 0 {
		t.Fatalf("errors %+v warnings %+v", res.Errors, res.Warnings)
	}
	if _, values, err := contract.ReadEnvironmentValues(fsys, pr); err != nil || len(values) != 0 {
		t.Fatalf("pr-412 values %v, %v", values, err)
	}
	diffs, err := validation.EnvironmentDifferences(c, fsys, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range diffs {
		got = append(got, fmt.Sprint(d.Configuration, ".", d.Path, " ", d.Values))
	}
	want := []string{"app.logLevel map[pr-412:info production:warn]", "app.replicas map[pr-412:1 production:4]"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}
