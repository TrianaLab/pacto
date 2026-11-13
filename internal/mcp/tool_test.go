package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"testing/fstest"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// listTools returns every tool a server lists, by name.
func listTools(t *testing.T, server *mcpsdk.Server) map[string]*mcpsdk.Tool {
	t.Helper()
	res, err := catalogSession(t, server).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	tools := map[string]*mcpsdk.Tool{}
	for _, tl := range res.Tools {
		tools[tl.Name] = tl
	}
	return tools
}

// readOnlyPactoTools are the Pacto tools that change nothing.
var readOnlyPactoTools = map[string]bool{
	"pacto_schema": true, "pacto_check": true, "pacto_skill": true, "pacto_impact": true,
	"pacto_catalog_revision": true, "pacto_publish_plan": true, "pacto_capabilities": true,
}

func wantReadOnly(name string) bool {
	if ro, ok := readOnlyPactoTools[name]; ok {
		return ro
	}
	if strings.HasPrefix(name, "pacto_fleet_") {
		return true
	}
	return strings.HasPrefix(name, "pacto_delivery_") && name != "pacto_delivery_operation_propose" && name != "pacto_delivery_cancel"
}

func TestEveryToolAnnotated(t *testing.T) {
	variants := serverVariants(t)
	variants["capability"] = func() *mcpsdk.Server {
		s, err := New("test", Authoring{}, Modes{Bundle: &BundleMode{Bundle: shadowBundle(), Opts: CapabilityOptions{BaseURL: "http://x", AllowWrites: true}, Stderr: io.Discard}})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return s
	}
	variants["composed"] = func() *mcpsdk.Server { return mustNew("test", Authoring{}, composedModes(t, true)) }
	for name, build := range variants {
		t.Run(name, func(t *testing.T) {
			for tn, tl := range listTools(t, build()) {
				if tl.Annotations == nil {
					t.Errorf("tool %s has no annotations", tn)
					continue
				}
				if wantReadOnly(tn) && !tl.Annotations.ReadOnlyHint {
					t.Errorf("tool %s changes nothing but readOnlyHint is false", tn)
				}
			}
		})
	}
}

func TestGeneratedToolHintsFromMethod(t *testing.T) {
	spec := `{"openapi":"3.1.0","paths":{"/r":{
	  "get":{"operationId":"g"},"head":{"operationId":"h"},"post":{"operationId":"po"},
	  "put":{"operationId":"pu"},"patch":{"operationId":"pa"},"delete":{"operationId":"d"}}}}`
	bundle := capBundle(fstest.MapFS{"o.json": {Data: []byte(spec)}}, httpIface("http", "o.json"))
	server, err := New("test", Authoring{}, Modes{Bundle: &BundleMode{Bundle: bundle, Opts: CapabilityOptions{BaseURL: "http://x", AllowWrites: true}, Stderr: io.Discard}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tools := listTools(t, server)
	cases := []struct {
		name                            string
		readOnly, idempotent, noDestroy bool
		destructive                     *bool
	}{
		{name: "g", readOnly: true, noDestroy: true},
		{name: "h", readOnly: true, noDestroy: true},
		{name: "po", noDestroy: true},
		{name: "pu", idempotent: true, noDestroy: true},
		{name: "pa", noDestroy: true},
		{name: "d", destructive: ptr(true)},
	}
	for _, c := range cases {
		a := tools[c.name].Annotations
		if a == nil {
			t.Fatalf("%s: no annotations", c.name)
		}
		if a.ReadOnlyHint != c.readOnly || a.IdempotentHint != c.idempotent {
			t.Errorf("%s: readOnly=%v idempotent=%v, want %v %v", c.name, a.ReadOnlyHint, a.IdempotentHint, c.readOnly, c.idempotent)
		}
		if a.OpenWorldHint == nil || !*a.OpenWorldHint {
			t.Errorf("%s: every generated tool reaches a service: openWorldHint must be true", c.name)
		}
		raw, _ := json.Marshal(a)
		if has := strings.Contains(string(raw), "destructiveHint"); has == c.noDestroy {
			t.Errorf("%s: destructiveHint present=%v in %s", c.name, has, raw)
		}
		if c.destructive != nil && (a.DestructiveHint == nil || *a.DestructiveHint != *c.destructive) {
			t.Errorf("%s: destructiveHint %v, want %v", c.name, a.DestructiveHint, *c.destructive)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestAuthoringToolHints(t *testing.T) {
	tools := listTools(t, mustNew("test", Authoring{}, Modes{}))
	edit, create := tools["pacto_edit"].Annotations, tools["pacto_create"].Annotations
	if edit.DestructiveHint == nil || !*edit.DestructiveHint {
		t.Errorf("pacto_edit removes entries in place: destructiveHint must be true, got %v", edit.DestructiveHint)
	}
	if create.DestructiveHint == nil || *create.DestructiveHint {
		t.Errorf("pacto_create only adds files: destructiveHint must be false, got %v", create.DestructiveHint)
	}
	for name, a := range map[string]*mcpsdk.ToolAnnotations{"pacto_edit": edit, "pacto_create": create, "pacto_check": tools["pacto_check"].Annotations} {
		if a.OpenWorldHint == nil || !*a.OpenWorldHint {
			t.Errorf("%s follows references: openWorldHint must be true", name)
		}
	}
	if s := tools["pacto_schema"].Annotations; s.OpenWorldHint == nil || *s.OpenWorldHint {
		t.Errorf("pacto_schema reads nothing outside Pacto: openWorldHint must be false")
	}
}

func TestAddToolPanicsWithoutAnnotations(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "tool bare has no annotations") {
			t.Errorf("addTool must refuse a tool without annotations, recovered %v", r)
		}
	}()
	addTool(newBareServer("test", ""), &mcpsdk.Tool{Name: "bare", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcpsdk.CallToolRequest, map[string]any) (*mcpsdk.CallToolResult, any, error) {
			return nil, nil, nil
		})
}

func TestUnavailableStandInIsReadOnly(t *testing.T) {
	spec := `{"openapi":"3.1.0","paths":{"/broken":{"post":{"operationId":"broken","parameters":[
	  {"name":"q","in":"query","schema":{"$ref":"#/components/schemas/Nope"}}]}}}}`
	bundle := capBundle(fstest.MapFS{"o.json": {Data: []byte(spec)}}, httpIface("http", "o.json"))
	server, err := New("test", Authoring{}, Modes{Bundle: &BundleMode{Bundle: bundle, Opts: CapabilityOptions{BaseURL: "http://x", AllowWrites: true}, Stderr: io.Discard}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	broken := listTools(t, server)["broken"]
	if !strings.Contains(broken.Description, "UNAVAILABLE") {
		t.Fatalf("fixture must produce the stand-in, got %q", broken.Description)
	}
	if a := broken.Annotations; a == nil || !a.ReadOnlyHint {
		t.Errorf("the stand-in never reaches the service: readOnlyHint must be true, got %+v", a)
	}
}

func TestDeliveryWriteToolsAreNotReadOnly(t *testing.T) {
	tools := listTools(t, mustNew("test", Authoring{}, Modes{Delivery: &DeliveryMode{API: &stubDelivery{}, Writes: true}}))
	for _, name := range []string{"pacto_delivery_operation_propose", "pacto_delivery_cancel"} {
		a := tools[name].Annotations
		if a == nil || a.ReadOnlyHint {
			t.Errorf("%s asks for a change: readOnlyHint must be false, got %+v", name, a)
			continue
		}
		if a.DestructiveHint == nil || *a.DestructiveHint {
			t.Errorf("%s only asks; people and Pacto decide: destructiveHint must be false", name)
		}
	}
}
