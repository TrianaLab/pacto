package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoCapabilitiesInstallation: the demo answers installation as
// main_wasm.go wires it, over its own fleet and in-process delivery.
func TestDemoCapabilitiesInstallation(t *testing.T) {
	d, err := newDemoDelivery(time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(demoFleet(t))
	opts := dashboard.Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }}
	d.wire(&opts)
	if opts.InstallationConfig, err = demoInstallationConfig(); err != nil {
		t.Fatal(err)
	}
	var caps struct {
		Installation fleet.Installation `json:"installation"`
	}
	if code := call(t, demoHandler(opts), "GET", "/api/capabilities", "", &caps); code != 200 {
		t.Fatalf("capabilities: %d", code)
	}
	inst := caps.Installation
	t.Logf("installation: %+v", inst)
	if !slices.Equal(inst.Profile.Value, []string{"default"}) || inst.Registry.Value == nil || *inst.Registry.Value != "ghcr.io/acme" {
		t.Errorf("profile %+v registry %+v", inst.Profile, inst.Registry)
	}
	if e := inst.Delivery.Enabled; e == nil || !*e || inst.Delivery.Source != "this server serves delivery itself" {
		t.Errorf("delivery %+v", inst.Delivery)
	}
	for name, src := range map[string]string{"profile": inst.Profile.Source, "registry": inst.Registry.Source, "delivery": inst.Delivery.Source,
		"provisioning": inst.Provisioning.Source, "clusters": inst.Clusters.Source, "mcp": inst.MCP.Source} {
		if src == "" {
			t.Errorf("installation.%s has no source", name)
		}
	}
}
