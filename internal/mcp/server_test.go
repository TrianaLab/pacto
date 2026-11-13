package mcp

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// mustNew is New for a test whose modes cannot fail to register.
func mustNew(version string, a Authoring, m Modes) *mcpsdk.Server {
	s, err := New(version, a, m)
	if err != nil {
		panic(err)
	}
	return s
}

func TestNew(t *testing.T) {
	server := mustNew("1.0.0", Authoring{}, Modes{})
	if server == nil {
		t.Fatal("expected non-nil server")
	}
}

func TestJsonResult_Error(t *testing.T) {
	// json.MarshalIndent fails for channels
	_, err := jsonResult(make(chan int))
	if err == nil {
		t.Error("expected error for unmarshallable type")
	}
}

func TestJsonResult(t *testing.T) {
	data := map[string]string{"key": "value"}
	result, err := jsonResult(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result.Content))
	}
	tc, ok := result.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatal("expected TextContent")
	}
	if tc.Text == "" {
		t.Error("expected non-empty text")
	}

	var parsed map[string]string
	if err := json.Unmarshal([]byte(tc.Text), &parsed); err != nil {
		t.Fatalf("expected valid JSON, got error: %v", err)
	}
	if parsed["key"] != "value" {
		t.Errorf("expected key=value, got %v", parsed["key"])
	}
}

func TestTextResult(t *testing.T) {
	result := textResult("hello")
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result.Content))
	}
	tc := result.Content[0].(*mcpsdk.TextContent)
	if tc.Text != "hello" {
		t.Errorf("expected 'hello', got %q", tc.Text)
	}
	if result.IsError {
		t.Error("expected IsError=false for textResult")
	}
}

func TestErrorResult(t *testing.T) {
	result := errorResult(errors.New("something failed"))
	if !result.IsError {
		t.Error("expected IsError=true")
	}
	tc := result.Content[0].(*mcpsdk.TextContent)
	if tc.Text != "something failed" {
		t.Errorf("expected error message, got %q", tc.Text)
	}
}

// authoringVariants builds every server that registers the contract tools with a.
func authoringVariants(t *testing.T, a Authoring) map[string]*mcpsdk.Server {
	t.Helper()
	capability := mustNew("test", a, Modes{Bundle: &BundleMode{Bundle: shadowBundle(), Opts: CapabilityOptions{BaseURL: "http://x"}, Stderr: io.Discard}})
	return map[string]*mcpsdk.Server{
		"authoring":  mustNew("test", a, Modes{}),
		"fleet":      mustNew("test", a, Modes{Fleet: &FleetMode{Src: staticFleet(buildFleetQuery(t)), Opts: FleetOptions{Impact: stubImpact(nil, nil, nil)}}}),
		"delivery":   mustNew("test", a, Modes{Delivery: &DeliveryMode{API: &stubDelivery{}, Writes: true}}),
		"publish":    NewPublishServer("test", nil, a),
		"capability": capability,
	}
}

var fileTools = []string{"pacto_create", "pacto_edit", "pacto_check"}

func TestRemoteAuthoringRegistersOnlySchema(t *testing.T) {
	for name, server := range authoringVariants(t, Authoring{Remote: true}) {
		tools := listTools(t, server)
		for _, ft := range fileTools {
			if tools[ft] != nil {
				t.Errorf("%s: remote server registers %s", name, ft)
			}
		}
		if tools["pacto_schema"] == nil {
			t.Errorf("%s: remote server lacks pacto_schema", name)
		}
	}
	for name, server := range authoringVariants(t, Authoring{}) {
		tools := listTools(t, server)
		for _, ft := range append(fileTools, "pacto_schema") {
			if tools[ft] == nil {
				t.Errorf("%s: stdio server lacks %s", name, ft)
			}
		}
	}
}

func TestRemoteInstructionsNameNoFileTool(t *testing.T) {
	for name, server := range authoringVariants(t, Authoring{Remote: true}) {
		got := catalogSession(t, server).InitializeResult().Instructions
		if !strings.HasPrefix(got, remoteInstructions) {
			t.Errorf("%s: instructions do not open with the remote text: %q", name, got)
		}
	}
	rest := strings.Replace(remoteInstructions, remoteAuthoringSentence, "", 1)
	for _, ft := range fileTools {
		if strings.Contains(rest, ft) {
			t.Errorf("remote instructions name %s outside the stdio sentence", ft)
		}
	}
	if !strings.Contains(remoteInstructions, "pacto_create, pacto_edit and pacto_check run beside the agent") {
		t.Error("remote instructions do not say where the file tools run")
	}
	if !strings.HasPrefix(baseFor(Authoring{}), stdioInstructions) {
		t.Error("a stdio server lost its authoring instructions")
	}
}

func TestRemoteSchemaNamesNoFileTool(t *testing.T) {
	text := func(a Authoring) string {
		res := callToolOn(t, mustNew("test", a, Modes{}), "pacto_schema", map[string]any{})
		var got schemaResult
		if err := json.Unmarshal([]byte(res.Content[0].(*mcpsdk.TextContent).Text), &got); err != nil {
			t.Fatal(err)
		}
		return got.Description
	}
	if got := text(Authoring{Remote: true}); strings.Contains(got, "Use pacto_create") || !strings.Contains(got, remoteAuthoringSentence) {
		t.Errorf("remote pacto_schema description: %q", got)
	}
	if got := text(Authoring{}); !strings.Contains(got, "Use pacto_create") {
		t.Errorf("stdio pacto_schema description: %q", got)
	}
}
