package publish

import (
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// TestLayerTypesMatchDelivery: the inspector reads the layers publish writes.
func TestLayerTypesMatchDelivery(t *testing.T) {
	if string(FluxLayerType) != delivery.FluxContentMediaType || string(HelmLayerType) != delivery.HelmChartMediaType {
		t.Fatalf("%s %s", FluxLayerType, HelmLayerType)
	}
}
