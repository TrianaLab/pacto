package app

import (
	"errors"
	"testing"

	"github.com/trianalab/pacto/v4/internal/evidenceoci"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

// The store the Evidence Server opens writes referrers on its subjects and,
// with no subject prefix, never a tag: its push scope is referrer-only. With a
// prefix it may tag exactly <prefix>pacto-evidence.
func TestServeEvidenceStoreScope(t *testing.T) {
	opts := evidenceStoreOptions(nil)
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
	t.Run("prefix", prefixScope)
}

// prefixScope is TestServeEvidenceStoreScope with a subject prefix.
func prefixScope(t *testing.T) {
	host, _ := referrersRegistry(t)
	prefixes, err := evidenceoci.ParsePrefixes([]string{"oci://" + host + "/acme/"})
	if err != nil {
		t.Fatal(err)
	}
	opts := evidenceStoreOptions(prefixes)
	if got := opts.Scope.Prefixes(); len(got) != 1 || got[0] != host+"/acme/pacto-evidence" {
		t.Fatalf("evidence store scope = %v, want exactly the ledger", got)
	}
	ctx := t.Context()
	anchor := ocireferrer.Build("application/x-anchor", "application/x-layer", []byte("a"), nil, nil)
	ledger, _ := ocireferrer.Open(host+"/acme/pacto-evidence", opts)
	if err := ocireferrer.PushWriteOnce(ctx, ledger, anchor, "evidence-x"); err != nil {
		t.Fatalf("tag in the ledger: %v", err)
	}
	svc, _ := ocireferrer.Open(host+"/acme/invoices", opts)
	plain, _ := ocireferrer.Open(host+"/acme/invoices", ocireferrer.Options{})
	if err := ocireferrer.Push(ctx, plain, anchor); err != nil {
		t.Fatal(err)
	}
	if err := svc.Tag(ctx, anchor.ManifestDesc, "v1"); !errors.Is(err, oci.ErrOutsidePushScope) {
		t.Fatalf("tag on a service repository: err = %v, want ErrOutsidePushScope", err)
	}
}
