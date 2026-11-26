package impact

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// storageOffering is the postgresql offering at version whose provisioning
// schema caps storageGB at maximum.
func storageOffering(version string, maximum int) (*contract.Contract, fstest.MapFS) {
	c := pgContract(version, "provisioning", false)
	c.Service.Offering = "postgresql"
	return c, fstest.MapFS{"schema.json": {Data: []byte(`{"type":"object","additionalProperties":false,` +
		`"properties":{"storageGB":{"type":"integer","maximum":` + strconv.Itoa(maximum) + `}}}`)}}
}

// A narrowed offering schema is breaking, and impact names the consumer whose
// current values it rejects while a consumer within the new bound stays
// compatible.
func TestImpactOfferingNarrowedRejectsConsumerValues(t *testing.T) {
	old, oldFS := storageOffering("16.1.0", 500)
	newC, newFS := storageOffering("16.2.0", 100)
	col := &fleet.Collection{Revisions: []fleet.RawRevision{
		{Bundle: &contract.Bundle{Contract: old, FS: oldFS}, Digest: "sha256:pg", RequestedRef: "oci://x/postgresql:16.1.0"},
		refConsumer("orders", "", map[string]any{"storageGB": 400}, true),
		refConsumer("billing", "", map[string]any{"storageGB": 50}, true),
	}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("test", "memory", col))
	if err != nil {
		t.Fatal(err)
	}
	res := Analyze(context.Background(), old, newC, oldFS, newFS, snap, Options{})
	if res.Classification != "BREAKING" {
		t.Fatalf("classification %s", res.Classification)
	}
	verdicts := map[string]string{}
	for _, c := range res.Consumers {
		verdicts[c.Service] = c.CompatibilityVerdict
		if c.Service == "orders" && !strings.Contains(c.Note, "/storageGB") {
			t.Errorf("orders note %q does not name the rejected value", c.Note)
		}
	}
	if verdicts["orders"] != CompatibilityIncompatible || verdicts["billing"] != CompatibilityCompatible {
		t.Errorf("verdicts = %v, consumers %+v", verdicts, res.Consumers)
	}
}
