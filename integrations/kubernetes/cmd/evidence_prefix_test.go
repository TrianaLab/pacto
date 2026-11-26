/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package main

import (
	"flag"
	"slices"
	"testing"

	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/credentials"
)

// The Evidence Server writes its ledger with its own credential, so a subject
// prefix must never widen the operator's installation push prefixes, which
// feed its keychains and the Installation row's Pushes.
func TestEvidenceSubjectPrefixLeavesInstallationPrefixes(t *testing.T) {
	parse := func(args ...string) *options {
		t.Helper()
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		o := &options{}
		bindFlags(fs, o)
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		return o
	}
	reg, push := "--registry=zot.acme.internal/acme", "--registry-push-prefix=zot.acme.internal/acme/streams"
	without := parse(reg, push)
	with := parse(reg, push, "--evidence-subject-prefix=oci://zot.acme.internal/acme/")

	if !slices.Equal(with.evidenceSubjectPrefixes, []string{"oci://zot.acme.internal/acme/"}) {
		t.Fatalf("evidenceSubjectPrefixes = %v", with.evidenceSubjectPrefixes)
	}
	if !slices.Equal(with.registryPushPrefixes, without.registryPushPrefixes) {
		t.Errorf("registryPushPrefixes = %v, want %v", with.registryPushPrefixes, without.registryPushPrefixes)
	}
	got := credentials.InstallationPrefixes(with.registry, with.registryPushPrefixes)
	want := credentials.InstallationPrefixes(without.registry, without.registryPushPrefixes)
	if !slices.Equal(got, want) {
		t.Errorf("InstallationPrefixes = %v, want %v", got, want)
	}
	for _, p := range got {
		if p == "zot.acme.internal/acme/pacto-evidence" {
			t.Errorf("the ledger leaked into the installation prefixes: %v", got)
		}
	}
}
