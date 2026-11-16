package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/logging"
)

// auditLines returns every "mcp tool call" line logged to buf, decoded.
func auditLines(t *testing.T, buf *strings.Builder) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) == nil && m["msg"] == "mcp tool call" {
			lines = append(lines, m)
		}
	}
	return lines
}

func jsonLogger(buf *strings.Builder) context.Context {
	return logging.WithLogger(context.Background(), slog.New(slog.NewJSONHandler(buf, nil)))
}

// loggedSession connects a client to server with ctx (carrying the logger) as
// the server's context, so the handlers log through it.
func loggedSession(t *testing.T, ctx context.Context, server *mcpsdk.Server) *mcpsdk.ClientSession {
	t.Helper()
	st, ct := mcpsdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	session, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "audit", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestAuditLineFields(t *testing.T) {
	buf := &strings.Builder{}
	ctx := jsonLogger(buf)
	q := buildFleetQuery(t)
	server := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(q), Opts: FleetOptions{}}})
	if _, err := loggedSession(t, ctx, server).CallTool(ctx, &mcpsdk.CallToolParams{Name: "pacto_fleet_status", Arguments: map[string]any{}}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	lines := auditLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("want one audit line, got %d:\n%s", len(lines), buf)
	}
	l := lines[0]
	for _, k := range []string{"tool", "caller", "callerSource", "readOnly", "outcome", "durationMs", "snapshotId", "argsDigest"} {
		if _, ok := l[k]; !ok {
			t.Errorf("audit line lacks %q: %v", k, l)
		}
	}
	if l["tool"] != "pacto_fleet_status" || l["caller"] != "local" || l["callerSource"] != "local" || l["outcome"] != "ok" ||
		l["readOnly"] != true || l["snapshotId"] != q.SnapshotID() {
		t.Errorf("audit line: %v", l)
	}
}

func TestAuditLineHasNoArguments(t *testing.T) {
	buf := &strings.Builder{}
	ctx := jsonLogger(buf)
	server := mustNew("test", Authoring{}, Modes{})
	_, _ = loggedSession(t, ctx, server).CallTool(ctx, &mcpsdk.CallToolParams{Name: "pacto_schema", Arguments: map[string]any{"text": "s3cr3t"}})
	if strings.Contains(buf.String(), "s3cr3t") {
		t.Fatalf("the audit line carries an argument:\n%s", buf)
	}
	lines := auditLines(t, buf)
	if len(lines) != 1 || !strings.HasPrefix(lines[0]["argsDigest"].(string), "hmac-sha256:") {
		t.Fatalf("want one line with argsDigest:\n%s", buf)
	}
	if _, ok := lines[0]["snapshotId"]; ok {
		t.Errorf("a tool that read no snapshot logs no snapshotId: %v", lines[0])
	}
}

func rawReq(args string) *mcpsdk.CallToolRequest {
	return &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{Arguments: json.RawMessage(args)}}
}

func TestAuditArgsDigestIsKeyedAndCanonical(t *testing.T) {
	a, b := argsDigest(rawReq(`{"a":1,"b":2}`)), argsDigest(rawReq(`{ "b": 2, "a": 1 }`))
	if a != b {
		t.Fatalf("same arguments in another order give another digest: %s %s", a, b)
	}
	for _, plain := range []string{`{"a":1,"b":2}`, `{ "b": 2, "a": 1 }`} {
		sum := sha256.Sum256([]byte(plain))
		if strings.HasSuffix(a, hex.EncodeToString(sum[:])) {
			t.Fatalf("the digest is the plain sha256 of %s", plain)
		}
	}
	if argsDigest(nil) == "" || argsDigest(rawReq(`not json`)) == argsDigest(rawReq(`{}`)) {
		t.Error("a missing or undecodable argument set still gets its own digest")
	}
}

// call runs one audited handler and returns its audit line.
func call(t *testing.T, ctx context.Context, buf *strings.Builder, req *mcpsdk.CallToolRequest, h mcpsdk.ToolHandlerFor[map[string]any, any]) map[string]any {
	t.Helper()
	tool := &mcpsdk.Tool{Name: "pacto_x", Annotations: writes(false, false, true)}
	_, _, _ = audited(tool, h)(ctx, req, nil)
	lines := auditLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("want one audit line:\n%s", buf)
	}
	return lines[0]
}

func okHandler(context.Context, *mcpsdk.CallToolRequest, map[string]any) (*mcpsdk.CallToolResult, any, error) {
	return &mcpsdk.CallToolResult{}, nil, nil
}

func TestAuditOutcomeError(t *testing.T) {
	buf := &strings.Builder{}
	l := call(t, jsonLogger(buf), buf, rawReq(`{}`), func(context.Context, *mcpsdk.CallToolRequest, map[string]any) (*mcpsdk.CallToolResult, any, error) {
		return errorResult(errors.New("no")), nil, nil
	})
	if l["outcome"] != "error" || l["readOnly"] != false {
		t.Errorf("tool error: %v", l)
	}
	buf.Reset()
	l = call(t, jsonLogger(buf), buf, rawReq(`{}`), func(context.Context, *mcpsdk.CallToolRequest, map[string]any) (*mcpsdk.CallToolResult, any, error) {
		return nil, nil, errors.New("broken")
	})
	if l["outcome"] != "failed" {
		t.Errorf("Go error: %v", l)
	}
}

func TestAuditCallerFromContext(t *testing.T) {
	buf := &strings.Builder{}
	req := rawReq(`{}`)
	req.Extra = &mcpsdk.RequestExtra{TokenInfo: &auth.TokenInfo{UserID: "opaque-sub"}}
	l := call(t, WithCaller(jsonLogger(buf), "dave"), buf, req, okHandler)
	if l["caller"] != "dave" || l["callerSource"] != "reviewed" {
		t.Errorf("reviewed caller: %v", l)
	}
}

func TestAuditCallerFromToken(t *testing.T) {
	buf := &strings.Builder{}
	req := rawReq(`{}`)
	req.Extra = &mcpsdk.RequestExtra{TokenInfo: &auth.TokenInfo{UserID: "https://idp#agent"}}
	l := call(t, jsonLogger(buf), buf, req, okHandler)
	if l["caller"] != "https://idp#agent" || l["callerSource"] != "token" {
		t.Errorf("token caller: %v", l)
	}
}
