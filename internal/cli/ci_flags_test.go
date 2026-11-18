package cli

import (
	"slices"
	"testing"

	"github.com/spf13/cobra"
)

// nonConsumerFlags are the shared fleet flags that name no consumer source:
// the repository itself, scoping and freshness knobs, the external catalog
// and the advisory detection rows, which never clear a gate.
var nonConsumerFlags = []string{"local", "namespace", "freshness", "external-catalog", "detected"}

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
