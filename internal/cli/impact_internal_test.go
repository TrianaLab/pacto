package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// The text output names each guarantee a consumer requires that the new
// revision refuses, as the JSON `unmet` field and the TUI do.
func TestPrintImpactResult_Unmet(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	m := contract.Mismatch{Consumer: "web", Provider: "api", Interface: "events", Guarantee: "delivery", Required: "at-least-once"}
	res := &impact.Result{Service: "api", Classification: "BREAKING", Consumers: []impact.AffectedConsumer{
		{Service: "web", Direct: true, Unmet: []contract.Mismatch{m}},
	}}
	if err := printImpactResult(cmd, res, "text"); err != nil {
		t.Fatal(err)
	}
	if want := "    refused: " + m.String() + "\n"; !strings.Contains(buf.String(), want) {
		t.Errorf("missing %q in:\n%s", want, buf.String())
	}
}
