package mcp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// statsCall is what a stub stats provider was called with.
type statsCall struct {
	caller     *auth.TokenInfo
	full, view *fleet.Query
	req        StatsRequest
}

// stubStats answers st or err and records each call.
func stubStats(st *stats.Stats, err error) (StatsProvider, *[]statsCall) {
	var calls []statsCall
	return func(_ context.Context, caller *auth.TokenInfo, full, view *fleet.Query, req StatsRequest) (*stats.Stats, error) {
		calls = append(calls, statsCall{caller, full, view, req})
		return st, err
	}, &calls
}

func statsModes(t *testing.T, p StatsProvider, view FleetView) Modes {
	t.Helper()
	return Modes{Fleet: &FleetMode{Src: staticFleet(buildFleetQuery(t)), Opts: FleetOptions{Stats: p, View: view}}}
}

func TestStatsToolAnnotatedReadOnly(t *testing.T) {
	p, _ := stubStats(&stats.Stats{}, nil)
	tool := listTools(t, mustNew("test", Authoring{}, statsModes(t, p, nil)))["pacto_stats"]
	if tool == nil {
		t.Fatal("pacto_stats not listed")
	}
	if a := tool.Annotations; a == nil || !a.ReadOnlyHint || a.OpenWorldHint == nil || !*a.OpenWorldHint {
		t.Fatalf("annotations = %+v", tool.Annotations)
	}
	kinds := tool.InputSchema.(map[string]any)["properties"].(map[string]any)["kinds"].(map[string]any)
	if _, ok := kinds["items"]; ok || !reflect.DeepEqual(kinds["type"], []any{"string", "null"}) {
		t.Fatalf("kinds = %+v", kinds)
	}
}

func TestStatsToolRegisteredOnlyWithProvider(t *testing.T) {
	p, _ := stubStats(&stats.Stats{}, nil)
	if listTools(t, mustNew("test", Authoring{}, statsModes(t, nil, nil)))["pacto_stats"] != nil {
		t.Error("pacto_stats registered without a provider")
	}
	if listTools(t, mustNew("test", Authoring{}, statsModes(t, p, nil)))["pacto_stats"] == nil {
		t.Error("pacto_stats missing with a provider")
	}
}

func TestStatsInstructionsOnlyWhenRegistered(t *testing.T) {
	p, _ := stubStats(&stats.Stats{}, nil)
	if !strings.Contains(instructionsFor(Authoring{}, statsModes(t, p, nil)), statsInstructions) {
		t.Error("no stats instructions with a provider")
	}
	if strings.Contains(instructionsFor(Authoring{}, statsModes(t, nil, nil)), "pacto_stats") {
		t.Error("stats instructions without a provider")
	}
}

func TestStatsToolPassesCallerFullAndViewedQuery(t *testing.T) {
	full := buildFleetQuery(t)
	narrowed := fleet.NewQuery(&fleet.FleetSnapshot{})
	view := func(context.Context, *auth.TokenInfo, *fleet.Query) (*fleet.Query, error) { return narrowed, nil }
	p, calls := stubStats(&stats.Stats{Provenance: stats.Provenance}, nil)
	m := Modes{Fleet: &FleetMode{Src: staticFleet(full), Opts: FleetOptions{Stats: p, View: view}}}
	var got stats.Stats
	decode(t, callToolOn(t, mustNew("test", Authoring{}, m), "pacto_stats", map[string]any{"by": "owner", "since": "7d", "service": "orders"}), &got)
	if got.Provenance != stats.Provenance || len(*calls) != 1 {
		t.Fatalf("answer %+v, calls %d", got, len(*calls))
	}
	c := (*calls)[0]
	if c.full != full || c.view != narrowed || c.req != (StatsRequest{By: "owner", Since: "7d", Service: "orders"}) {
		t.Fatalf("call = %+v", c)
	}
	// Without a view the provider gets no viewed query.
	p, calls = stubStats(&stats.Stats{}, nil)
	callToolOn(t, mustNew("test", Authoring{}, statsModes(t, p, nil)), "pacto_stats", map[string]any{})
	if (*calls)[0].view != nil {
		t.Fatal("an unrestricted server passed a view")
	}
}

func TestStatsToolAcceptsCommaSeparatedKinds(t *testing.T) {
	p, calls := stubStats(&stats.Stats{}, nil)
	res := callToolOn(t, mustNew("test", Authoring{}, statsModes(t, p, nil)), "pacto_stats", map[string]any{"kinds": "diff,impact", "registry": "r.example/s"})
	if res.IsError || len(*calls) != 1 || (*calls)[0].req.Kinds != "diff,impact" || (*calls)[0].req.Registry != "r.example/s" {
		t.Fatalf("res = %s calls = %+v", resultText(t, res), *calls)
	}
}

func TestStatsToolErrorIsToolError(t *testing.T) {
	p, _ := stubStats(nil, errors.New("no stats registry"))
	res := callToolOn(t, mustNew("test", Authoring{}, statsModes(t, p, nil)), "pacto_stats", map[string]any{})
	if !res.IsError || resultText(t, res) != "no stats registry" {
		t.Fatalf("res = %+v", res)
	}
	view := func(context.Context, *auth.TokenInfo, *fleet.Query) (*fleet.Query, error) {
		return nil, errors.New("review failed")
	}
	p, calls := stubStats(&stats.Stats{}, nil)
	res = callToolOn(t, mustNew("test", Authoring{}, statsModes(t, p, view)), "pacto_stats", map[string]any{})
	if !res.IsError || resultText(t, res) != "review failed" || len(*calls) != 0 {
		t.Fatalf("a failed view answered: %+v", res)
	}
}

func TestStatsToolSourceErrorIsToolError(t *testing.T) {
	p, calls := stubStats(&stats.Stats{}, nil)
	src := func(context.Context) (*fleet.Query, error) { return nil, errors.New("no snapshot") }
	res := callToolOn(t, mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: src, Opts: FleetOptions{Stats: p}}}), "pacto_stats", map[string]any{})
	if !res.IsError || resultText(t, res) != "no snapshot" || len(*calls) != 0 {
		t.Fatalf("res = %+v", res)
	}
}

// A signed-in caller's token reaches both the view and the provider.
func TestStatsToolPassesSignedInCaller(t *testing.T) {
	var viewedBy *auth.TokenInfo
	view := func(_ context.Context, c *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) { viewedBy = c; return q, nil }
	p, calls := stubStats(&stats.Stats{}, nil)
	alice := &auth.TokenInfo{UserID: "alice"}
	req := &mcpsdk.CallToolRequest{Extra: &mcpsdk.RequestExtra{TokenInfo: alice}}
	res, _, err := statsHandler(staticFleet(buildFleetQuery(t)), view, p)(context.Background(), req, statsArgs{})
	if err != nil || res.IsError || viewedBy != alice || len(*calls) != 1 || (*calls)[0].caller != alice {
		t.Fatalf("res %+v err %v viewed by %+v calls %+v", res, err, viewedBy, *calls)
	}
}
