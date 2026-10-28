package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// TestViewedFleetTools: with a view, every fleet tool answers from the
// caller's view of the snapshot, impact included; a failed view is the tool's
// error.
func TestViewedFleetTools(t *testing.T) {
	q := buildFleetQuery(t)
	empty := fleet.NewQuery(&fleet.FleetSnapshot{Services: map[fleet.ServiceKey]*fleet.ServiceRecord{}})
	var caller *auth.TokenInfo
	view := func(_ context.Context, c *auth.TokenInfo, _ *fleet.Query) (*fleet.Query, error) {
		caller = c
		if c != nil && c.UserID == "boom" {
			return nil, errors.New("checking permissions failed")
		}
		return empty, nil
	}
	req := func(user string) *mcpsdk.CallToolRequest {
		return &mcpsdk.CallToolRequest{Extra: &mcpsdk.RequestExtra{TokenInfo: &auth.TokenInfo{UserID: user}}}
	}
	res, _, err := viewed(view, q, fleetSearchHandler)(context.Background(), req("alice"), fleetSearchArgs{})
	var out fleet.SearchResult
	decode(t, res, &out)
	if err != nil || out.Total != 0 || caller == nil || caller.UserID != "alice" {
		t.Errorf("search through the view: %d services for %+v, %v", out.Total, caller, err)
	}
	if res, _, _ := viewed(view, q, fleetSearchHandler)(context.Background(), req("boom"), fleetSearchArgs{}); !res.IsError {
		t.Error("a failed view must be the tool's error")
	}
	if _, _, _ = viewed(view, q, fleetSearchHandler)(context.Background(), nil, fleetSearchArgs{}); caller != nil {
		t.Error("no request extra is no caller")
	}

	var got *fleet.FleetSnapshot
	provide := func(_ context.Context, snap *fleet.FleetSnapshot, _, _ string, _ bool, _ string) (*impact.Result, error) {
		got = snap
		return &impact.Result{}, nil
	}
	server := NewFleetServer("test", q, provide, nil, view)
	callToolOn(t, server, "pacto_impact", map[string]any{"old_ref": "a", "new_ref": "b"})
	if got != empty.SnapshotForSerialization() {
		t.Error("impact must analyse over the caller's view")
	}
	callToolOn(t, NewFleetServer("test", q, provide, nil, nil), "pacto_impact", map[string]any{"old_ref": "a", "new_ref": "b"})
	if got != nil {
		t.Error("without a view impact builds a fresh snapshot (nil)")
	}
}
