package oci_test

import (
	"context"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/oci"
)

// draftTags is a bundle repository after `pacto declare --draft` ran twice:
// one published version and two write-once draft tags.
var draftTags = []string{"draft-2", "1.0.0", "draft-1"}

// A draft-<n> tag is a declaration waiting for a person, never a version: the
// service-tag reader resolves an untagged reference to the published 1.0.0.
func TestResolveLatestSkipsDraftTags(t *testing.T) {
	got, err := oci.ResolveRef(context.Background(), &mockTagLister{tags: draftTags}, "ghcr.io/acme/invoices", "")
	if err != nil || got != "ghcr.io/acme/invoices:1.0.0" {
		t.Fatalf("ResolveRef = %q, %v; want ghcr.io/acme/invoices:1.0.0", got, err)
	}
	if got, err := oci.BestTag(draftTags, ">=0.0.0"); err != nil || got != "1.0.0" {
		t.Fatalf("BestTag with a range = %q, %v; want 1.0.0", got, err)
	}
	if got := oci.FilterSemverTags(draftTags); len(got) != 1 || got[0] != "1.0.0" {
		t.Fatalf("FilterSemverTags = %v; want [1.0.0]", got)
	}
}
