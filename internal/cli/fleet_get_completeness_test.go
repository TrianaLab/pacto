package cli_test

import (
	"encoding/json"
	"testing"
)

func TestFleetGetJSONContractCompleteness(t *testing.T) {
	root, ev := writeFleetFixture(t)
	out, _, err := execFleet(t, "fleet", "get", "orders", "--local", root, "--target-state", ev, "--output-format", "json")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var view struct {
		ContractCompleteness *struct {
			Percent  *int `json:"percentComplete"`
			Sections []struct {
				Section, State string
			} `json:"sections"`
			Completeness string `json:"completeness"`
		} `json:"contractCompleteness"`
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	c := view.ContractCompleteness
	if c == nil || c.Percent == nil || len(c.Sections) != 8 || c.Completeness == "" {
		t.Fatalf("contractCompleteness = %+v\n%s", c, out)
	}
}
