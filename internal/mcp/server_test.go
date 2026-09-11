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
