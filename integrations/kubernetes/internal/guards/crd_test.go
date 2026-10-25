package guards

import (
	"os"
	"path/filepath"
	"testing"

	"sigs.k8s.io/yaml"
)

// Kargo also serves a kind named Stage, so `kubectl get stage` is ambiguous on
// a cluster that runs both. The pacto-delivery category gives every delivery
// kind one unambiguous handle: `kubectl get pacto-delivery -A`.
func TestDeliveryCRDsShareTheCategory(t *testing.T) {
	for _, dir := range []string{"../../config/crd/bases", "../../charts/pacto-delivery/crds"} {
		for _, kind := range []string{"stages", "operations", "approvals"} {
			path := filepath.Join(dir, "delivery.pacto.trianalab.io_"+kind+".yaml")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var crd struct {
				Spec struct {
					Names struct {
						Categories []string `json:"categories"`
					} `json:"names"`
				} `json:"spec"`
			}
			if err := yaml.Unmarshal(raw, &crd); err != nil {
				t.Fatal(err)
			}
			if len(crd.Spec.Names.Categories) != 1 || crd.Spec.Names.Categories[0] != "pacto-delivery" {
				t.Errorf("%s: categories = %v, want [pacto-delivery]", path, crd.Spec.Names.Categories)
			}
		}
	}
}
