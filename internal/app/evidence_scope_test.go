package app

import (
	"errors"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

// The store the Evidence Server opens writes referrers on its subjects and
// never a tag: its push scope is referrer-only.
func TestServeEvidenceStoreHasReferrerOnlyScope(t *testing.T) {
	opts := evidenceStoreOptions()
	if opts.Scope == nil || len(opts.Scope.Prefixes()) != 0 {
		t.Fatalf("evidence store scope = %+v, want referrer-only", opts.Scope)
	}
	host, _ := referrersRegistry(t)
	repo, err := ocireferrer.Open(host+"/acme/svc", opts)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := ocireferrer.Open(host+"/acme/svc", ocireferrer.Options{})
	ctx := t.Context()
	anchor := ocireferrer.Build("application/x-anchor", "application/x-layer", []byte("a"), nil, nil)
	if err := ocireferrer.Push(ctx, plain, anchor); err != nil {
		t.Fatal(err)
	}
	if err := repo.Tag(ctx, anchor.ManifestDesc, "v1"); !errors.Is(err, oci.ErrOutsidePushScope) {
		t.Fatalf("tag through the evidence store: err = %v, want ErrOutsidePushScope", err)
	}
	rec := ocireferrer.Build("application/x-evidence", "application/x-layer", []byte("e"), &anchor.ManifestDesc, nil)
	if err := ocireferrer.Push(ctx, repo, rec); err != nil {
		t.Fatalf("referrer through the evidence store: %v", err)
	}
}
