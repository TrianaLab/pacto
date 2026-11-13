package mcp

import (
	"net/http"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/capability"
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
	mcpsdk.AddTool(s, t, h)
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
