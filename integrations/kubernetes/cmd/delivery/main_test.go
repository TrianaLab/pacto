/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package main

import (
	"testing"

	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

func TestTargetsRepositoryFromTopLevelRegistry(t *testing.T) {
	for registry, want := range map[string]string{
		"ghcr.io/acme":           "ghcr.io/acme/pacto-targets",
		"zot:5000":               "zot:5000/pacto-targets",
		"registry.example/pacto": "registry.example/pacto/pacto-targets",
	} {
		if got := targetsRepository(registry); got != want {
			t.Errorf("%s: %s, want %s", registry, got, want)
		}
	}
	for _, bad := range []string{"pacto-targets", "ghcr.io/acme/streams", "/pacto-targets"} {
		if err := addTargets(nil, bad, ocireferrer.Options{}, &delivery.TargetPublisher{}); err == nil {
			t.Errorf("%s: accepted", bad)
		}
	}
}

// With no --targets-repository (no top-level registry value) the recorder
// registers no publisher: addTargets never touches the manager.
func TestRecorderWithoutTargetsRepositoryPublishesNoSnapshot(t *testing.T) {
	if err := addTargets(nil, "", ocireferrer.Options{}, &delivery.TargetPublisher{}); err != nil {
		t.Fatal(err)
	}
}
