/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"testing"

	"gopkg.in/yaml.v3"
)

// namespacedKinds are the kinds a hardened render may hold besides the CRDs. It is an allowlist, so a
// cluster-scoped kind a later template adds (a ClusterRole, a Namespace, an admission policy, a webhook
// configuration) fails here without anyone remembering to list it.
var namespacedKinds = map[string]bool{
	"ConfigMap": true, "Deployment": true, "HTTPRoute": true, "Ingress": true, "NetworkPolicy": true,
	"PodDisruptionBudget": true, "Role": true, "RoleBinding": true, "Secret": true, "Service": true,
	"ServiceAccount": true, "ServiceMonitor": true,
}

// TestHardenedRendersOnlyNamespacedObjects renders the chart with profile hardened, through Helm itself,
// and fails on any document whose kind is not namespaced: hardened renders no cluster-scoped object
// besides the CRDs the chart installs.
func TestHardenedRendersOnlyNamespacedObjects(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		// CI's chart legs install Helm; a workstation without it still runs every other test here.
		t.Skip("helm not on PATH")
	}
	base := []string{"--set", "profile=hardened", "--set", "controller.watchNamespaces={payments,ledger}"}
	for name, extra := range map[string][]string{
		"hardened":               nil,
		"hardened with registry": {"--set", "registry=ghcr.io/acme"},
		"hardened and idp":       {"--set", "profile={hardened,idp}"},
		"hardened with evidence": {
			"--set", "evidence.enabled=true", "--set", "evidence.trust.existingSecret=my-keys",
			"--set", "evidence.registry.subjects={oci://ghcr.io/acme/orders@sha256:" +
				"1111111111111111111111111111111111111111111111111111111111111111}",
		},
	} {
		t.Run(name, func(t *testing.T) {
			args := append([]string{"template", "pacto-operator", "../../charts/pacto-operator",
				"-n", "pacto-operator-system",
				"--api-versions", "admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy",
				"--api-versions", "gateway.networking.k8s.io/v1/HTTPRoute"}, base...)
			out, err := exec.Command(helm, append(args, extra...)...).Output()
			if err != nil {
				t.Fatalf("helm template: %v", err)
			}
			for _, kind := range renderedKinds(t, out) {
				if kind != "CustomResourceDefinition" && !namespacedKinds[kind] {
					t.Errorf("profile hardened renders a %s, which is not a namespaced kind", kind)
				}
			}
		})
	}
}

// renderedKinds returns the kind of every non-empty document in a multi-document render.
func renderedKinds(t *testing.T, out []byte) []string {
	t.Helper()
	var kinds []string
	dec := yaml.NewDecoder(bytes.NewReader(out))
	for {
		var doc struct {
			Kind string `yaml:"kind"`
		}
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return kinds
		}
		if err != nil {
			t.Fatalf("parse the render: %v", err)
		}
		if doc.Kind != "" {
			kinds = append(kinds, doc.Kind)
		}
	}
}
