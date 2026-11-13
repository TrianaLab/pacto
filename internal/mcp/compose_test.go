package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// composedModes is fleet, delivery and the shadow bundle in one server.
func composedModes(t *testing.T, writes bool) Modes {
	t.Helper()
	return Modes{
		Fleet:    &FleetMode{Src: staticFleet(buildFleetQuery(t)), Opts: FleetOptions{Impact: stubImpact(nil, nil, nil)}},
		Delivery: &DeliveryMode{API: &stubDelivery{}, Writes: writes},
		Bundle:   &BundleMode{Bundle: shadowBundle(), Opts: CapabilityOptions{BaseURL: "http://x"}, Stderr: io.Discard},
	}
}

func TestComposedFleetDeliveryBundle(t *testing.T) {
	res, err := catalogSession(t, mustNew("test", Authoring{}, composedModes(t, true))).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, tl := range res.Tools {
		seen[tl.Name]++
	}
	for _, want := range []string{"pacto_schema", "pacto_fleet_get", "pacto_impact", "pacto_delivery_get",
		"pacto_delivery_operation_propose", "lookup", "pacto_skill", "pacto_capabilities"} {
		if seen[want] != 1 {
			t.Errorf("%s registered %d times, want once", want, seen[want])
		}
	}
	for name, n := range seen {
		if n != 1 {
			t.Errorf("%s listed %d times", name, n)
		}
	}
}

func TestComposedBundleCannotShadow(t *testing.T) {
	var stderr strings.Builder
	m := composedModes(t, false)
	m.Bundle.Stderr = &stderr
	server := mustNew("test", Authoring{}, m)
	tools := listTools(t, server)
	if tools["pacto_future_tool"] != nil || tools["pacto_publish"] != nil {
		t.Error("a bundle operation took a pacto_ name")
	}
	if !strings.Contains(tools["pacto_delivery_get"].Description, "Read an operation") {
		t.Errorf("pacto_delivery_get is not the delivery tool: %q", tools["pacto_delivery_get"].Description)
	}
	if !strings.Contains(stderr.String(), `skipped operation "pacto_delivery_get"`) {
		t.Errorf("no skip line: %q", stderr.String())
	}
	// The delivery tool answers, never the bundle's endpoint.
	var op struct {
		Metadata struct{ Name string } `json:"metadata"`
	}
	decode(t, callToolOn(t, server, "pacto_delivery_get", map[string]any{"namespace": "prod", "name": "op-1"}), &op)
	if op.Metadata.Name != "op-1" {
		t.Errorf("pacto_delivery_get answered %+v", op)
	}
}

// instructionCombos are every server checkMCPMode allows, over both
// transports, plus the catalog and publish servers.
func instructionCombos(t *testing.T) map[string]*mcpsdk.Server {
	t.Helper()
	out := map[string]*mcpsdk.Server{}
	cat, _ := platformCatalog(t)
	out["catalog"] = NewCatalogServer("test", cat)
	for _, remote := range []bool{false, true} {
		a := Authoring{Remote: remote}
		tr := map[bool]string{false: "stdio", true: "http"}[remote]
		out["publish/"+tr] = NewPublishServer("test", nil, a)
		for mask := range 16 {
			full := composedModes(t, mask&8 != 0)
			m := Modes{}
			name := tr
			if mask&1 != 0 {
				m.Fleet, name = full.Fleet, name+"+fleet"
			}
			if mask&2 != 0 {
				m.Delivery, name = full.Delivery, name+"+delivery"
			}
			if mask&4 != 0 {
				m.Bundle, name = full.Bundle, name+"+bundle"
			}
			if mask&8 != 0 {
				if m.Delivery == nil {
					continue
				}
				name += "-writes"
			}
			out[name] = mustNew("test", a, m)
		}
	}
	return out
}

var pactoName = regexp.MustCompile(`pacto_[a-z_]+`)

// sentenceEnd splits instructions into sentences.
var sentenceEnd = regexp.MustCompile(`[.!?](\s+|$)|\n+`)

func TestInstructionsNameOnlyRegisteredTools(t *testing.T) {
	for name, server := range instructionCombos(t) {
		session := catalogSession(t, server)
		tools := toolNames(t, session)
		for _, sentence := range sentenceEnd.Split(session.InitializeResult().Instructions, -1) {
			for _, tool := range pactoName.FindAllString(sentence, -1) {
				tool = strings.TrimRight(tool, "_")
				if !tools[tool] && !strings.Contains(sentence, "pacto mcp --") {
					t.Errorf("%s: instructions name %s, which this server does not register, without saying which server does: %q", name, tool, sentence)
				}
			}
		}
	}
}

// capabilitiesOf calls pacto_capabilities through the server.
func capabilitiesOf(t *testing.T, a Authoring, m Modes) map[string]any {
	t.Helper()
	var got map[string]any
	decode(t, callToolOn(t, mustNew("test", a, m), "pacto_capabilities", map[string]any{}), &got)
	return got
}

func TestCapabilitiesReportsModes(t *testing.T) {
	m := composedModes(t, true)
	m.Transport = "stdio"
	m.Delivery.Caller = delivery.Caller{Identity: delivery.Identity{User: "system:serviceaccount:agents:bot"}, Class: delivery.ClassAgent}
	got := capabilitiesOf(t, Authoring{}, m)
	modes, _ := json.Marshal(got["modes"])
	if string(modes) != `["fleet","delivery","bundle"]` {
		t.Errorf("modes = %s", modes)
	}
	for key, want := range map[string]any{"fleet": true, "impact": true, "observed": buildFleetQuery(t).HasObserved(),
		"delivery": true, "writes": true, "transport": "stdio"} {
		if got[key] != want {
			t.Errorf("%s = %#v, want %#v", key, got[key], want)
		}
	}
	if _, ok := got["provenance"]; ok {
		t.Error("pacto_capabilities carries a blanket provenance field")
	}
	if s, _ := got["asOf"].(string); s == "" {
		t.Error("no asOf")
	}
	caller := got["caller"].(map[string]any)
	for key, want := range map[string]any{"user": "local", "callerSource": "local", "class": "agent", "policyError": "", "writes": true} {
		if caller[key] != want {
			t.Errorf("caller.%s = %#v, want %#v", key, caller[key], want)
		}
	}
	authoring, _ := json.Marshal(got["authoring"])
	if string(authoring) != `["pacto_create","pacto_edit","pacto_check","pacto_schema"]` {
		t.Errorf("stdio authoring = %s", authoring)
	}
	remote := capabilitiesOf(t, Authoring{Remote: true}, Modes{Transport: "http"})
	if authoring, _ := json.Marshal(remote["authoring"]); string(authoring) != `["pacto_schema"]` {
		t.Errorf("remote authoring = %s", authoring)
	}
	t.Run("unreadable policy", capabilitiesUnreadablePolicy)
	t.Run("remote callers", capabilitiesRemoteCallers)
}

// capabilitiesUnreadablePolicy: the class comes from the defaults and
// policyError says why.
func capabilitiesUnreadablePolicy(t *testing.T) {
	m := composedModes(t, false)
	m.Delivery.Caller = delivery.Caller{Identity: delivery.Identity{User: "alice"}, Class: delivery.DefaultClassPolicy().Classify(delivery.Identity{User: "alice"}),
		PolicyErr: "configmaps is forbidden"}
	caller := capabilitiesOf(t, Authoring{}, m)["caller"].(map[string]any)
	if caller["policyError"] != "configmaps is forbidden" || caller["class"] != string(delivery.DefaultClassPolicy().Classify(delivery.Identity{User: "alice"})) || caller["writes"] != false {
		t.Errorf("unreadable policy caller = %+v", caller)
	}
}

// capabilitiesRemoteCallers: a reviewed remote caller, and one only a token
// names.
func capabilitiesRemoteCallers(t *testing.T) {
	ctx := WithCaller(context.Background(), "dave")
	out, err := capabilities(ctx, nil, Authoring{Remote: true}, Modes{Transport: "http"})
	if err != nil || out.Caller.User != "dave" || out.Caller.CallerSource != "reviewed" {
		t.Errorf("reviewed caller = %+v %v", out.Caller, err)
	}
	req := &mcpsdk.CallToolRequest{Extra: &mcpsdk.RequestExtra{TokenInfo: &auth.TokenInfo{UserID: "opaque-sub"}}}
	out, err = capabilities(context.Background(), req, Authoring{Remote: true}, Modes{Transport: "http"})
	if err != nil || out.Caller.User != "unknown" || out.Caller.CallerSource != "token" {
		t.Errorf("unreviewed caller = %+v %v", out.Caller, err)
	}
}

func TestCapabilitiesWithoutDelivery(t *testing.T) {
	got := capabilitiesOf(t, Authoring{}, Modes{Transport: "stdio"})
	for key, want := range map[string]any{"fleet": false, "impact": false, "observed": false, "delivery": false, "writes": false} {
		if got[key] != want {
			t.Errorf("%s = %#v, want %#v", key, got[key], want)
		}
	}
	if modes, _ := json.Marshal(got["modes"]); string(modes) != `[]` {
		t.Errorf("modes = %s", modes)
	}
	caller := got["caller"].(map[string]any)
	if _, ok := caller["class"]; ok {
		t.Errorf("class without delivery: %+v", caller)
	}
	if _, ok := caller["policyError"]; ok {
		t.Errorf("policyError without delivery: %+v", caller)
	}
	// A bundle with --allow-writes exposes writes.
	bundle := &BundleMode{Bundle: shadowBundle(), Opts: CapabilityOptions{BaseURL: "http://x", AllowWrites: true}, Stderr: io.Discard}
	if got := capabilitiesOf(t, Authoring{}, Modes{Bundle: bundle}); got["writes"] != true {
		t.Errorf("bundle writes = %v", got["writes"])
	}
}

// The caller's view decides observed, and a view that refuses refuses the
// answer.
func TestCapabilitiesFollowTheView(t *testing.T) {
	refuse := func(context.Context, *auth.TokenInfo, *fleet.Query) (*fleet.Query, error) {
		return nil, errors.New("not reviewed")
	}
	m := Modes{Fleet: &FleetMode{Src: staticFleet(buildFleetQuery(t)), Opts: FleetOptions{View: refuse}}}
	res := callToolOn(t, mustNew("test", Authoring{}, m), "pacto_capabilities", map[string]any{})
	if !res.IsError || !strings.Contains(res.Content[0].(*mcpsdk.TextContent).Text, "not reviewed") {
		t.Errorf("a refused view must refuse: %+v", res)
	}
}
