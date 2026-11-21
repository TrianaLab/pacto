package cli

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// handoverGolden is the P4 fixture as pkg/fleet pins it: ana's declaration of
// invoices from the inferred invoices Deployment.
type handoverGolden struct {
	Service struct {
		Fields        *fleet.FieldsPreview
		Replaces      []fleet.Replacement
		ReplacesTotal int
	}
	Explain struct {
		Fields  *fleet.FieldsPreview
		Reasons []fleet.Reason
	}
}

func readHandoverGolden(t *testing.T) handoverGolden {
	t.Helper()
	b, err := os.ReadFile("../../pkg/fleet/testdata/handover.golden.json")
	expect(t, err == nil, "golden: %v", err)
	var g handoverGolden
	expect(t, json.Unmarshal(b, &g) == nil, "golden json")
	return g
}

// inOrder fails unless every want is a line of out, in that order.
func inOrder(t *testing.T, out string, want ...string) {
	t.Helper()
	lines := strings.Split(out, "\n")
	i := 0
	for _, l := range lines {
		if i < len(want) && l == want[i] {
			i++
		}
	}
	expect(t, i == len(want), "output lacks %q in order:\n%s", want[min(i, len(want)-1)], out)
}

func TestPrintFleetServiceHandover(t *testing.T) {
	g := readHandoverGolden(t)
	view := &fleet.ServiceView{Service: &fleet.ServiceRecord{Name: "invoices", Owner: contract.Owner{Team: "billing"}, Status: "unknown", Sources: []string{"oci"}},
		Replaces: g.Service.Replaces, ReplacesTotal: g.Service.ReplacesTotal, Fields: g.Service.Fields, ReplacesHidden: 2}
	out := printed(t, func(c *cobra.Command) error { return printFleetService(c, view, "text") })
	inOrder(t, out,
		"Service: invoices",
		"Declared by ana@acme.com from inferred k8s:billing/Deployment/invoices (revision invoices@sha256:invoices-0.1.0)",
		"Owner: billing",
		"Status: unknown",
		"Sources: [oci]",
		"Fields:",
		"  owner         billing    declared",
		"  ledger range  ^2.3.0     accepted by ana@acme.com",
		"  audit range   unknown    proposed ^1.4.0 (fleet, partial)",
		"Replaces hidden: 2",
	)
}

func TestPrintFleetExplainFields(t *testing.T) {
	g := readHandoverGolden(t)
	res := &fleet.ExplainResult{Kind: "service", Subject: "invoices", Status: "unknown", Reasons: g.Explain.Reasons, Fields: g.Explain.Fields}
	out := printed(t, func(c *cobra.Command) error { return printFleetExplain(c, res, "text") })
	inOrder(t, out,
		"service invoices: unknown",
		"  [REPLACES_INFERRED] Declared by ana@acme.com from inferred k8s:billing/Deployment/invoices (revision invoices@sha256:invoices-0.1.0)",
		"Fields:",
		"  owner         billing    declared",
		"  workload      service    inferred from deploy/app.yaml:1",
	)
	plain := printed(t, func(c *cobra.Command) error {
		return printFleetExplain(c, &fleet.ExplainResult{Kind: "service", Subject: "web"}, "text")
	})
	expect(t, !strings.Contains(plain, "Fields:"), "a service with no field provenance prints a Fields block:\n%s", plain)
}

// An inferred-from replacement carries no time: the line never reads one.
func TestPrintFleetServiceReplacesWithoutTime(t *testing.T) {
	r := fleet.Replacement{From: fleet.NewServiceKey("k8s:billing/Deployment/invoices"), To: "invoices", Link: "inferred-from",
		Revision: "invoices@sha256:a", By: "ana@acme.com"}
	view := &fleet.ServiceView{Service: &fleet.ServiceRecord{Name: "invoices"}, ReplacedFrom: &r, Replaces: []fleet.Replacement{r}, ReplacesTotal: 1}
	out := printed(t, func(c *cobra.Command) error { return printFleetService(c, view, "text") })
	line := "Declared by ana@acme.com from inferred k8s:billing/Deployment/invoices (revision invoices@sha256:a)"
	inOrder(t, out, line, "Service: invoices", line)
	expect(t, !strings.Contains(out, " ago"), "an untimed replacement prints an age:\n%s", out)
}

// The CLI's JSON, pacto_fleet_get and pacto_fleet_entity carry the same fields
// and replaces for a handed-over service.
func TestHandoverSameProvenanceOnEverySurface(t *testing.T) {
	q := testutil.HandoverQuery(t)
	view, err := q.GetService("invoices")
	expect(t, err == nil && view.Fields != nil && len(view.Replaces) == 1, "get: %v %+v", err, view)
	type prov struct {
		Fields   *fleet.FieldsPreview `json:"fields"`
		Replaces []fleet.Replacement  `json:"replaces"`
	}
	var cliOut, getOut prov
	var entityOut struct{ Service prov }
	expect(t, json.Unmarshal([]byte(printed(t, func(c *cobra.Command) error { return printFleetService(c, view, "json") })), &cliOut) == nil, "cli json")
	server, err := pactomcp.New("v", pactomcp.Authoring{Remote: true}, pactomcp.Modes{Fleet: &pactomcp.FleetMode{
		Src: func(context.Context) (*fleet.Query, error) { return q, nil }}})
	expect(t, err == nil, "server: %v", err)
	session := sessionOn(t, server)
	get, _ := callText(t, session, "pacto_fleet_get", map[string]any{"service": "invoices"})
	entity, _ := callText(t, session, "pacto_fleet_entity", map[string]any{"kind": "service", "key": "invoices"})
	expect(t, json.Unmarshal([]byte(get), &getOut) == nil, "get json:\n%s", get)
	expect(t, json.Unmarshal([]byte(entity), &entityOut) == nil, "entity json:\n%s", entity)
	expect(t, cliOut.Fields != nil && len(cliOut.Replaces) == 1, "cli %+v", cliOut)
	expect(t, reflect.DeepEqual(cliOut, getOut), "pacto_fleet_get differs:\n%+v\n%+v", cliOut, getOut)
	expect(t, reflect.DeepEqual(cliOut, entityOut.Service), "pacto_fleet_entity differs:\n%+v\n%+v", cliOut, entityOut.Service)
}
