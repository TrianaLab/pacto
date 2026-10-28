/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runtimewatch

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestDefaultBoundsWithinCRDMaxItems: the NodeRuntimeObservation schema's
// maxItems must admit every list DefaultBounds lets the agent publish, or the
// apiserver rejects the status write.
func TestDefaultBoundsWithinCRDMaxItems(t *testing.T) {
	raw, err := os.ReadFile("../../integrations/kubernetes/config/crd/bases/pacto.trianalab.io_noderuntimeobservations.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var crd struct {
		Spec struct {
			Versions []struct {
				Schema struct {
					OpenAPIV3Schema schema `yaml:"openAPIV3Schema"`
				}
			}
		}
	}
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}
	b := DefaultBounds()
	for _, v := range crd.Spec.Versions {
		pods := v.Schema.OpenAPIV3Schema.Properties["status"].Properties["pods"]
		pod := pods.Items.Properties
		for name, c := range map[string]struct{ maxItems, bound int }{
			"status.pods":               {pods.MaxItems, b.MaxWorkloads},
			"status.pods[].listeners":   {pod["listeners"].MaxItems, b.MaxListeners},
			"status.pods[].connections": {pod["connections"].MaxItems, b.MaxConnections},
		} {
			if c.maxItems < c.bound {
				t.Errorf("%s maxItems %d is below the agent's bound %d", name, c.maxItems, c.bound)
			}
		}
	}
	if len(crd.Spec.Versions) == 0 {
		t.Fatal("no versions in the CRD")
	}
}

type schema struct {
	MaxItems   int               `yaml:"maxItems"`
	Items      *schema           `yaml:"items"`
	Properties map[string]schema `yaml:"properties"`
}
