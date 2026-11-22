package mcp

import (
	"reflect"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestFleetStatusToolContractCompleteness(t *testing.T) {
	q := buildFleetQuery(t)
	res := callHandler(t, fleetStatusTool(), fleetStatusHandler(q), map[string]any{})
	var out fleet.StatusResult
	decode(t, res, &out)
	want := q.Overview().Summary.Knowledge.ContractCompleteness
	if out.ContractCompleteness.Judged+out.ContractCompleteness.Unknown == 0 || !reflect.DeepEqual(out.ContractCompleteness, want) {
		t.Fatalf("contractCompleteness = %+v, overview %+v", out.ContractCompleteness, want)
	}
}
