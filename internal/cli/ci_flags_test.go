package cli

import (
	"slices"
	"testing"

	"github.com/spf13/cobra"
)

// nonConsumerFlags are the shared fleet flags that name no consumer source:
// the repository itself, scoping and freshness knobs, the external catalog,
// the advisory detection rows and the workload-manager fingerprints, which
// never clear a gate, the trust config target snapshots are verified with and
// the organization index, which alone is never a consumer source: without a
// target source it is CONSUMER_INDEX_UNCHECKED (509-S7 Decision 16).
var nonConsumerFlags = []string{"local", "namespace", "freshness", "external-catalog", "detected", "fingerprints", "delivery-trust", "index"}

func TestConsumerSourcesClassifiesEveryFleetFlag(t *testing.T) {
	value := map[string]string{"cache": "true", "k8s": "true", "freshness": "1m"}
	for _, name := range fleetFlagNames() {
		cmd := &cobra.Command{Use: "x"}
		addFleetSourceFlags(cmd.Flags())
		v, ok := value[name]
		if !ok {
			v = "x"
		}
		if err := cmd.Flags().Set(name, v); err != nil {
			t.Fatalf("--%s: %v", name, err)
		}
		got := fleetOptions(cmd).ConsumerSources()
		if slices.Contains(nonConsumerFlags, name) == (len(got) > 0) {
			t.Errorf("--%s: consumer sources %q; add a case to FleetOptions.ConsumerSources or to the non-source list", name, got)
		}
	}
}
