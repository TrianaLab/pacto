package mcp

import (
	"encoding/json"
	"errors"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewServer(t *testing.T) {
	server := NewServer(nil, "1.0.0")
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

func TestParseInput(t *testing.T) {
	t.Run("valid string", func(t *testing.T) {
		req := makeRequest(t, map[string]any{"path": "./test"})
		got := parseInput(req, "path")
		if got != "./test" {
			t.Errorf("expected './test', got %q", got)
		}
	})

	t.Run("missing field", func(t *testing.T) {
		req := makeRequest(t, map[string]any{"other": "val"})
		got := parseInput(req, "path")
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("nil arguments", func(t *testing.T) {
		req := &mcpsdk.CallToolRequest{}
		got := parseInput(req, "path")
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("non-string value", func(t *testing.T) {
		req := makeRequest(t, map[string]any{"path": 42})
		got := parseInput(req, "path")
		if got != "" {
			t.Errorf("expected empty string for non-string, got %q", got)
		}
	})
}

func TestParseInputBool(t *testing.T) {
	t.Run("true value", func(t *testing.T) {
		req := makeRequest(t, map[string]any{"flag": true})
		got := parseInputBool(req, "flag")
		if !got {
			t.Error("expected true")
		}
	})

	t.Run("false value", func(t *testing.T) {
		req := makeRequest(t, map[string]any{"flag": false})
		got := parseInputBool(req, "flag")
		if got {
			t.Error("expected false")
		}
	})

	t.Run("missing field", func(t *testing.T) {
		req := makeRequest(t, map[string]any{})
		got := parseInputBool(req, "flag")
		if got {
			t.Error("expected false for missing field")
		}
	})

	t.Run("nil arguments", func(t *testing.T) {
		req := &mcpsdk.CallToolRequest{}
		got := parseInputBool(req, "flag")
		if got {
			t.Error("expected false for nil args")
		}
	})

	t.Run("non-bool value", func(t *testing.T) {
		req := makeRequest(t, map[string]any{"flag": "yes"})
		got := parseInputBool(req, "flag")
		if got {
			t.Error("expected false for non-bool")
		}
	})
}

func TestParseArgs(t *testing.T) {
	t.Run("valid args", func(t *testing.T) {
		req := makeRequest(t, map[string]any{"a": "b"})
		args := parseArgs(req)
		if args == nil {
			t.Fatal("expected non-nil args")
		}
		if _, ok := args["a"]; !ok {
			t.Error("expected key 'a' in args")
		}
	})

	t.Run("nil arguments", func(t *testing.T) {
		req := &mcpsdk.CallToolRequest{}
		args := parseArgs(req)
		if args != nil {
			t.Error("expected nil args")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := &mcpsdk.CallToolRequest{
			Params: &mcpsdk.CallToolParamsRaw{
				Arguments: json.RawMessage(`not json`),
			},
		}
		args := parseArgs(req)
		if args != nil {
			t.Error("expected nil args for invalid JSON")
		}
	})
}

// makeRequest builds a CallToolRequest from a map of arguments.
func makeRequest(t *testing.T, args map[string]any) *mcpsdk.CallToolRequest {
	t.Helper()
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("failed to marshal args: %v", err)
	}
	return &mcpsdk.CallToolRequest{
		Params: &mcpsdk.CallToolParamsRaw{
			Arguments: json.RawMessage(data),
		},
	}
}
