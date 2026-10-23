package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/v4/internal/pactify"
)

// TestPactifyPlanIsCurrent: the plan the browser demo shows in its pactify
// step is what pacto init plans today on the story's plain fraud service.
// PACTO_UPDATE_PACTIFY_PLAN=1 (make pactify-plan) rewrites it.
func TestPactifyPlanIsCurrent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fraud")
	if err := os.CopyFS(dir, os.DirFS("../story/plain/fraud")); err != nil {
		t.Fatal(err)
	}
	r, err := pactify.Init(t.Context(), pactify.Options{Dir: dir, Owner: "risk", Plan: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Dir, r.Path = "fraud", filepath.Join("fraud", filepath.Base(r.Path))
	got, _ := json.MarshalIndent(r, "", "  ")
	got = append(got, '\n')
	if os.Getenv("PACTO_UPDATE_PACTIFY_PLAN") == "1" {
		if err := os.WriteFile("pactify-fraud.json", got, 0o644); err != nil {
			t.Fatal(err)
		}
		return // the binary embeds the old copy until it is built again
	}
	if string(got) != pactifyPlan {
		t.Fatalf("pactify-fraud.json is stale; run make -C examples/demo pactify-plan. pacto init plans:\n%s", got)
	}
}
