package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/openapi"
)

// capSpec exercises every toolDesc branch and the write gate.
const capSpec = `{
  "openapi": "3.1.0",
  "paths": {
    "/ping": {"get": {"operationId": "ping", "summary": "Ping the service"}},
    "/desc": {"get": {"operationId": "descOnly", "description": "described but unsummarized"}},
    "/bare": {"get": {"operationId": "bare"}},
    "/refunds": {"post": {"operationId": "createRefund", "requestBody": {"required": true,
      "content": {"application/json": {"schema": {"type": "object"}}}}}}
  }
}`

func capBundle(fsys fstest.MapFS, ifaces ...contract.Interface) *contract.Bundle {
	return &contract.Bundle{Contract: &contract.Contract{Interfaces: ifaces}, FS: fsys}
}

func httpIface(name, path string) contract.Interface {
	return contract.Interface{Name: name, Type: contract.InterfaceTypeOpenAPI, Ref: path}
}

// connectCaps registers capabilities on a fresh server and returns a client session.
func connectCaps(t *testing.T, bundle *contract.Bundle, opts CapabilityOptions, stderr io.Writer) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := NewServer(nil, "test")
	if err := RegisterCapabilities(server, bundle, opts, stderr); err != nil {
		t.Fatalf("RegisterCapabilities: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "c", Version: "1"}, nil)
	t1, t2 := mcpsdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func toolNames(t *testing.T, session *mcpsdk.ClientSession) map[string]bool {
	t.Helper()
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := map[string]bool{}
	for _, tl := range res.Tools {
		names[tl.Name] = true
	}
	return names
}

func TestRegisterCapabilities_ReadOnlyListAndInvoke(t *testing.T) {
	var served string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = r.URL.Path
		_, _ = io.WriteString(w, `{"pong":true}`)
	}))
	defer srv.Close()

	fsys := fstest.MapFS{
		"interfaces/openapi.json": {Data: []byte(capSpec)},
		"skills/refund.md":        {Data: []byte("# Refund flow")},
	}
	bundle := capBundle(fsys, httpIface("http", "interfaces/openapi.json"))
	var stderr bytes.Buffer
	session := connectCaps(t, bundle, CapabilityOptions{BaseURL: srv.URL, HTTPClient: srv.Client()}, &stderr)

	names := toolNames(t, session)
	for _, want := range []string{"ping", "descOnly", "bare", "pacto_skill"} {
		if !names[want] {
			t.Errorf("missing tool %q (have %v)", want, names)
		}
	}
	if names["createRefund"] {
		t.Error("mutating createRefund must be gated without --allow-writes")
	}
	if !bytes.Contains(stderr.Bytes(), []byte("skipped 1 mutating")) {
		t.Errorf("expected skipped-mutating notice, got %q", stderr.String())
	}

	// invoke ping → live HTTP
	res, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "ping"})
	if err != nil {
		t.Fatalf("CallTool ping: %v", err)
	}
	text := resultText(t, res)
	if !bytes.Contains([]byte(text), []byte("pong")) || !bytes.Contains([]byte(text), []byte("200")) {
		t.Errorf("ping result = %q", text)
	}
	if served != "/ping" {
		t.Errorf("server saw path %q", served)
	}

	// skills list + content
	listRes, _ := session.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "pacto_skill"})
	if !bytes.Contains([]byte(resultText(t, listRes)), []byte("refund.md")) {
		t.Errorf("skill list = %q", resultText(t, listRes))
	}
	contentRes, _ := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "pacto_skill", Arguments: map[string]any{"name": "refund.md"}})
	if resultText(t, contentRes) != "# Refund flow" {
		t.Errorf("skill content = %q", resultText(t, contentRes))
	}
	missRes, _ := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "pacto_skill", Arguments: map[string]any{"name": "nope.md"}})
	if !missRes.IsError {
		t.Error("expected error result for missing skill")
	}
}

func TestRegisterCapabilities_AllowWrites(t *testing.T) {
	fsys := fstest.MapFS{"o.json": {Data: []byte(capSpec)}}
	bundle := capBundle(fsys, httpIface("http", "o.json"))
	session := connectCaps(t, bundle, CapabilityOptions{BaseURL: "http://x", AllowWrites: true}, io.Discard)
	if !toolNames(t, session)["createRefund"] {
		t.Error("createRefund must be exposed with AllowWrites")
	}
}

func TestRegisterCapabilities_MultiInterfacePrefix(t *testing.T) {
	fsys := fstest.MapFS{
		"a.json": {Data: []byte(`{"paths":{"/ping":{"get":{"operationId":"ping"}}}}`)},
		"b.json": {Data: []byte(`{"paths":{"/ping":{"get":{"operationId":"ping"}}}}`)},
	}
	bundle := capBundle(fsys, httpIface("alpha", "a.json"), httpIface("beta", "b.json"))
	session := connectCaps(t, bundle, CapabilityOptions{BaseURL: "http://x"}, io.Discard)
	names := toolNames(t, session)
	if !names["alpha_ping"] || !names["beta_ping"] {
		t.Errorf("expected interface-prefixed names, got %v", names)
	}
}

func TestRegisterCapabilities_ServersFromSpec(t *testing.T) {
	spec := `{"servers":[{"url":"http://from-spec"}],"paths":{"/ping":{"get":{"operationId":"ping"}}}}`
	bundle := capBundle(fstest.MapFS{"o.json": {Data: []byte(spec)}}, httpIface("http", "o.json"))
	// no BaseURL → falls back to servers[0]
	session := connectCaps(t, bundle, CapabilityOptions{}, io.Discard)
	if !toolNames(t, session)["ping"] {
		t.Error("expected ping registered using servers[0]")
	}
}

func TestRegisterCapabilities_SkipsNonHTTP(t *testing.T) {
	fsys := fstest.MapFS{"o.json": {Data: []byte(capSpec)}, "skills/x.md": {Data: []byte("x")}}
	bundle := capBundle(fsys,
		contract.Interface{Name: "ev", Type: contract.InterfaceTypeAsyncAPI, Ref: "events.json"},
		contract.Interface{Name: "noref", Type: contract.InterfaceTypeOpenAPI},
	)
	// only pacto_skill should be registered (no openapi-with-ref interfaces)
	session := connectCaps(t, bundle, CapabilityOptions{}, io.Discard)
	names := toolNames(t, session)
	if !names["pacto_skill"] || names["ping"] {
		t.Errorf("expected only pacto_skill, got %v", names)
	}
}

func TestRegisterCapabilities_ReadDocError(t *testing.T) {
	bundle := capBundle(fstest.MapFS{}, httpIface("http", "missing.json"))
	err := RegisterCapabilities(NewServer(nil, "t"), bundle, CapabilityOptions{BaseURL: "http://x"}, io.Discard)
	if err == nil {
		t.Fatal("expected error for missing openapi file")
	}
}

func TestRegisterCapabilities_NoBaseURL(t *testing.T) {
	bundle := capBundle(fstest.MapFS{"o.json": {Data: []byte(capSpec)}}, httpIface("http", "o.json"))
	err := RegisterCapabilities(NewServer(nil, "t"), bundle, CapabilityOptions{}, io.Discard)
	if err == nil {
		t.Fatal("expected error when no base URL and no servers")
	}
}

func TestNewCapabilityServer_InstructionsReachClient(t *testing.T) {
	fsys := fstest.MapFS{
		"o.json":      {Data: []byte(capSpec)},
		"skills/x.md": {Data: []byte("x")},
	}
	bundle := &contract.Bundle{
		Contract: &contract.Contract{
			Service:    contract.Service{Name: "demo-svc"},
			Interfaces: []contract.Interface{httpIface("http", "o.json")},
		},
		FS: fsys,
	}
	server, err := NewCapabilityServer(bundle, CapabilityOptions{BaseURL: "http://x"}, "test", io.Discard)
	if err != nil {
		t.Fatalf("NewCapabilityServer: %v", err)
	}

	ctx := context.Background()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "c", Version: "1"}, nil)
	t1, t2 := mcpsdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	instr := session.InitializeResult().Instructions
	for _, want := range []string{"pacto_create", "demo-svc", "executable tools", "read-only", "pacto_skill"} {
		if !strings.Contains(instr, want) {
			t.Errorf("instructions missing %q; got: %s", want, instr)
		}
	}
	// capability tools + skill tool are registered too
	if !toolNames(t, session)["ping"] {
		t.Error("expected ping tool registered")
	}
}

func TestCapabilityInstructions_AllowWrites(t *testing.T) {
	bundle := &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: "svc"}}, FS: fstest.MapFS{}}
	ro := capabilityInstructions(bundle, CapabilityOptions{})
	if !strings.Contains(ro, "Only read-only") {
		t.Errorf("read-only instructions = %q", ro)
	}
	rw := capabilityInstructions(bundle, CapabilityOptions{AllowWrites: true})
	if !strings.Contains(rw, "mutating") || strings.Contains(rw, "Only read-only") {
		t.Errorf("allow-writes instructions = %q", rw)
	}
}

func TestNewCapabilityServer_Error(t *testing.T) {
	bundle := capBundle(fstest.MapFS{"o.json": {Data: []byte(capSpec)}}, httpIface("http", "o.json"))
	// creds without base URL → RegisterCapabilities error propagates
	_, err := NewCapabilityServer(bundle, CapabilityOptions{Creds: map[string]string{"k": "v"}}, "t", io.Discard)
	if err == nil {
		t.Fatal("expected error from NewCapabilityServer")
	}
}

func TestRegisterCapabilities_CredsRequireBaseURL(t *testing.T) {
	bundle := capBundle(fstest.MapFS{"o.json": {Data: []byte(capSpec)}}, httpIface("http", "o.json"))
	err := RegisterCapabilities(NewServer(nil, "t"), bundle,
		CapabilityOptions{Creds: map[string]string{"apiKey": "secret"}}, io.Discard)
	if err == nil {
		t.Fatal("expected error: --auth without --base-url must be refused")
	}
}

func TestCapabilityHandler_InvalidArgsAndInvokeError(t *testing.T) {
	op := openapi.Operation{Method: "GET", Path: "/x"}
	h := capabilityHandler(op, &openapi.Doc{}, "http://127.0.0.1:0", CapabilityOptions{})

	// malformed arguments
	bad := &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{Arguments: json.RawMessage("not json")}}
	res, err := h(context.Background(), bad)
	if err != nil || !res.IsError {
		t.Fatalf("expected invalid-args error result, got res=%v err=%v", res, err)
	}

	// transport failure surfaces as an error result
	ok := &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{Arguments: json.RawMessage(`{}`)}}
	res, err = h(context.Background(), ok)
	if err != nil || !res.IsError {
		t.Fatalf("expected invoke error result, got res=%v err=%v", res, err)
	}
}
