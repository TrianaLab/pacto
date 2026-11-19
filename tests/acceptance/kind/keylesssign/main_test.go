package main

import (
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// The signature the kind scenario attaches is one the promoter's watch
// accepts under the rule derived from ghcr.io/acme, read back through the
// trusted root the command wrote, as the promoter reads it.
func TestRunSignsWhatTheDerivedRuleAccepts(t *testing.T) {
	srv := httptest.NewServer(registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	reg := publish.NewClient(authn.DefaultKeychain, host)
	img, _ := random.Image(32, 1)
	d, err := reg.Write(t.Context(), host+"/apps/payments", img)
	if err != nil {
		t.Fatal(err)
	}
	const repo = "https://github.com/acme/payments"
	rootFile := filepath.Join(t.TempDir(), "trusted_root.json")
	if err := run(t.Context(), testutil.New(t), rootFile, host, repo+"/.github/workflows/release.yml@refs/heads/main",
		"https://token.actions.githubusercontent.com", repo, "refs/heads/main", []string{"apps/payments@" + d}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(rootFile)
	tr, err := root.NewTrustedRootFromJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	rule, _ := delivery.DeriveRule("ghcr.io/acme", "main")
	v := publish.CheckPublisher(t.Context(), reg, tr, false, host+"/apps/payments", d, delivery.ServiceRef{Name: "payments"}, repo,
		[]delivery.PublisherRule{rule}, delivery.PublisherSourceRule, nil)
	if v.Verdict != delivery.VerdictAccepted || v.Source != delivery.PublisherSourceRule {
		t.Fatalf("verdict %+v", v)
	}
	if err := run(t.Context(), testutil.New(t), rootFile, host, "s", "i", repo, "r", []string{"no-digest"}); err == nil {
		t.Error("a ref with no digest signed")
	}
	if err := run(t.Context(), testutil.New(t), "", host, "s", "i", repo, "r", nil); err == nil {
		t.Error("no root file and no refs ran")
	}
}
