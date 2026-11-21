package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// installationOf is pacto_capabilities' installation for m.
func installationOf(t *testing.T, m Modes) map[string]map[string]any {
	t.Helper()
	got := capabilitiesOf(t, Authoring{}, m)
	parts := map[string]map[string]any{}
	for k, v := range got["installation"].(map[string]any) {
		parts[k] = v.(map[string]any)
	}
	parts["top"] = got
	return parts
}

func TestCapabilitiesInstallationServedOverMCP(t *testing.T) {
	m := composedModes(t, false)
	m.Transport = "http"
	m.Fleet.Opts.InstallationConfig = func(context.Context) (*fleet.InstallationDeclared, fleet.SourceState) {
		return &fleet.InstallationDeclared{Profile: []string{"idp"}, ClusterID: "elsewhere"}, fleet.SourceState{Status: fleet.SourceAvailable}
	}
	inst := installationOf(t, m)
	// The fleet holds no Installation row of that cluster: the caller's view
	// does not include it.
	if p := inst["profile"]; p["value"] != nil || p["source"] != "your view does not include the cluster this installation runs in" {
		t.Errorf("profile = %#v", p)
	}
	if mcp := inst["mcp"]; mcp["served"] != true || mcp["provenance"] != "host" || mcp["source"] != "this MCP server, over http" {
		t.Errorf("mcp = %#v", mcp)
	}
	if c := inst["clusters"]; c["joined"] == nil {
		t.Errorf("clusters = %#v", c)
	}
}

func TestCapabilitiesInstallationDeliveryProbeError(t *testing.T) {
	m := composedModes(t, false)
	m.Delivery.Installed = func(context.Context) (bool, error) { return false, errors.New("apiserver down") }
	inst := installationOf(t, m)
	if d := inst["delivery"]; d["enabled"] != nil || d["source"] != "the delivery probe failed: apiserver down" {
		t.Errorf("installation.delivery = %#v", d)
	}
	if inst["top"]["delivery"] != true {
		t.Error("a failed probe hides delivery")
	}
}

func TestCapabilitiesInstallationDeliveryNotProbed(t *testing.T) {
	inst := installationOf(t, composedModes(t, false))
	if d := inst["delivery"]; d["enabled"] != nil || !strings.Contains(d["source"].(string), "not read here") {
		t.Errorf("installation.delivery = %#v", d)
	}
	if inst["top"]["delivery"] != true {
		t.Error("an unprobed delivery mode hides delivery")
	}
}

func TestCapabilitiesInstallationWithoutFleet(t *testing.T) {
	inst := installationOf(t, Modes{Transport: "stdio"})
	if p := inst["profile"]; p["value"] != nil || !strings.HasPrefix(p["source"].(string), "no --detected") {
		t.Errorf("profile = %#v", p)
	}
	if inst["mcp"]["served"] != true || inst["clusters"]["joined"] != nil {
		t.Errorf("mcp %#v clusters %#v", inst["mcp"], inst["clusters"])
	}
}
