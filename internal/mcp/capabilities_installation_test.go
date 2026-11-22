package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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
	if p := inst["profile"]; p["value"] != nil || !strings.HasPrefix(fmt.Sprint(p["source"]), "the fleet snapshot in your view holds no Installation row") {
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

// assertInstallationParts: every installation part names its source,
// provenance and completeness.
func assertInstallationParts(t *testing.T, got map[string]any) {
	t.Helper()
	inst, _ := got["installation"].(map[string]any)
	for _, part := range []string{"profile", "registry", "delivery", "provisioning", "clusters", "mcp"} {
		if p, _ := inst[part].(map[string]any); p["source"] == nil || p["provenance"] == nil || p["completeness"] == nil {
			t.Errorf("installation.%s = %#v", part, inst[part])
		}
	}
}

// delivery.asOf is when the cached probe last asked the cluster.
func TestCapabilitiesInstallationDeliveryAsOfFromProbe(t *testing.T) {
	m := composedModes(t, false)
	m.Delivery.Installed = func(context.Context) (bool, error) { return false, nil }
	m.Delivery.InstalledAt = func() time.Time { return time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC) }
	if d := installationOf(t, m)["delivery"]; d["asOf"] != "2026-11-20T09:00:00Z" {
		t.Errorf("installation.delivery = %#v", d)
	}
}
