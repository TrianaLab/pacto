package mcp

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/capability"
	"github.com/trianalab/pacto/v4/pkg/logging"
)

// addTool registers every tool Pacto serves. It goes through the generic
// mcpsdk.AddTool, the form that validates a call against the declared schema
// before the handler runs, and refuses a tool without annotations: an agent's
// client reads them to decide what to confirm, so a missing one is a
// programming error every server test catches.
func addTool[A any](s *mcpsdk.Server, t *mcpsdk.Tool, h mcpsdk.ToolHandlerFor[A, any]) {
	if t.Annotations == nil {
		panic("tool " + t.Name + " has no annotations")
	}
	mcpsdk.AddTool(s, t, audited(t, h))
}

// audited logs one line per call of tool t: who called, whether the tool
// changes anything, how it ended and how long it took. The arguments appear
// only as a keyed digest, so the log never holds a value an agent passed.
func audited[A any](t *mcpsdk.Tool, h mcpsdk.ToolHandlerFor[A, any]) mcpsdk.ToolHandlerFor[A, any] {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest, a A) (*mcpsdk.CallToolResult, any, error) {
		start := time.Now()
		note := &callNote{}
		res, out, err := h(context.WithValue(ctx, callNoteKey{}, note), req, a)
		outcome := "ok"
		switch {
		case err != nil:
			outcome = "failed"
		case res != nil && res.IsError:
			outcome = "error"
		}
		caller, source := callerOf(ctx, req)
		attrs := []any{"tool", t.Name, "caller", caller, "callerSource", source,
			"readOnly", t.Annotations.ReadOnlyHint, "outcome", outcome,
			"durationMs", time.Since(start).Milliseconds(), "argsDigest", argsDigest(req)}
		if note.snapshotID != "" {
			attrs = append(attrs, "snapshotId", note.snapshotID)
		}
		logging.LoggerFromContext(ctx).Info("mcp tool call", attrs...)
		return res, out, err
	}
}

// callNote collects what a handler learns about the call for its audit line.
type callNote struct{ snapshotID string }

type callNoteKey struct{}

// noteFrom returns the audit note of the call ctx belongs to, nil outside one.
func noteFrom(ctx context.Context) *callNote {
	n, _ := ctx.Value(callNoteKey{}).(*callNote)
	return n
}

type callerKey struct{}

// WithCaller returns ctx naming the caller the apiserver reviewed, so the audit
// line records the same name admission records.
func WithCaller(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, callerKey{}, user)
}

// CallerFrom returns the reviewed caller WithCaller stored, empty when none.
func CallerFrom(ctx context.Context) string {
	user, _ := ctx.Value(callerKey{}).(string)
	return user
}

// callerOf names the caller and how Pacto knows: reviewed by the apiserver,
// only a verified token subject, or the local user.
func callerOf(ctx context.Context, req *mcpsdk.CallToolRequest) (user, source string) {
	if u := CallerFrom(ctx); u != "" {
		return u, "reviewed"
	}
	if req != nil && req.Extra != nil && req.Extra.TokenInfo != nil && req.Extra.TokenInfo.UserID != "" {
		return req.Extra.TokenInfo.UserID, "token"
	}
	return "local", "local"
}

// auditKey keys the argument digest. It is made once per process and never
// logged, so whoever reads the logs cannot confirm a guessed short value.
var auditKey = func() []byte {
	k := make([]byte, 32)
	_, _ = rand.Read(k) // crypto/rand.Read never returns an error
	return k
}()

// argsDigest is HMAC-SHA256 over the canonical JSON of the call's arguments
// (decoded and encoded again, which sorts object keys), so the same arguments
// in another key order give the same digest.
func argsDigest(req *mcpsdk.CallToolRequest) string {
	var raw json.RawMessage
	if req != nil && req.Params != nil {
		raw = req.Params.Arguments
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		raw, _ = json.Marshal(v) // v came from JSON, so it encodes
	}
	m := hmac.New(sha256.New, auditKey)
	m.Write(raw)
	return "hmac-sha256:" + hex.EncodeToString(m.Sum(nil))
}

// readOnly annotates a tool that changes nothing. openWorld says it reads
// systems outside Pacto (a registry, a cluster or a service).
func readOnly(openWorld bool) *mcpsdk.ToolAnnotations {
	return &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld}
}

// writes annotates a tool that changes something. Hints never claim more
// safety than Pacto knows; --allow-writes and the stdio-only rules stay the
// enforcement.
func writes(destructive, idempotent, openWorld bool) *mcpsdk.ToolAnnotations {
	return &mcpsdk.ToolAnnotations{DestructiveHint: &destructive, IdempotentHint: idempotent, OpenWorldHint: &openWorld}
}

// generatedHints derives a generated tool's hints from its HTTP method. Pacto
// knows nothing about the service behind it, so POST, PATCH and PUT leave
// destructiveHint unset (the MCP default when unset is destructive) and only
// DELETE says it outright.
func generatedHints(t capability.Tool) *mcpsdk.ToolAnnotations {
	open := true
	h := &mcpsdk.ToolAnnotations{OpenWorldHint: &open}
	switch strings.ToUpper(t.Method) {
	case http.MethodGet, http.MethodHead:
		h.ReadOnlyHint = true
	case http.MethodPut:
		h.IdempotentHint = true
	case http.MethodDelete:
		destructive := true
		h.DestructiveHint = &destructive
	}
	return h
}
