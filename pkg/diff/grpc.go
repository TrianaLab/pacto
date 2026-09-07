package diff

import (
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// diffGRPC compares two proto3 files and returns changes for services, rpcs,
// messages and message fields.
//
// This is a deterministic text scan, not a protobuf compiler: the
// contract-compatibility surface of a .proto is those four things, and a full
// parser is a large dependency for them. It does not resolve `import`s and it
// does not descend into nested messages or oneof bodies. An unparseable file is
// not reported as an error; it simply yields no changes.
func diffGRPC(oldPath, newPath string, oldFS, newFS fs.FS) []Change {
	if oldFS == nil || newFS == nil || oldPath == "" || newPath == "" {
		return nil
	}

	oldData, oldErr := fs.ReadFile(oldFS, oldPath)
	newData, newErr := fs.ReadFile(newFS, newPath)
	if oldErr != nil || newErr != nil {
		return nil
	}

	old := extractProto(string(oldData))
	new := extractProto(string(newData))

	changes := diffProtoSection(protoServices, old.services, new.services)
	return append(changes, diffProtoSection(protoMessages, old.messages, new.messages)...)
}

// protoAPI is the extracted contract surface of a .proto file: each service with
// its rpc signatures, and each message with its "<type> = <number>" fields.
type protoAPI struct {
	services map[string]map[string]string
	messages map[string]map[string]string
}

// protoSection describes how one kind of owner (service or message) and its
// members (rpcs or fields) are named in change paths and reasons.
type protoSection struct {
	ownerRule, ownerNoun, ownerPathFmt    string
	memberRule, memberNoun, memberPathFmt string
}

var (
	protoServices = protoSection{
		ownerRule: "grpc.services", ownerNoun: "service", ownerPathFmt: "grpc.services[%s]",
		memberRule: "grpc.rpcs", memberNoun: "rpc", memberPathFmt: "grpc.rpcs[%s.%s]",
	}
	protoMessages = protoSection{
		ownerRule: "grpc.messages", ownerNoun: "message", ownerPathFmt: "grpc.messages[%s]",
		memberRule: "grpc.messages.fields", memberNoun: "field", memberPathFmt: "grpc.messages[%s].fields[%s]",
	}
)

// diffProtoSection diffs owners and, for owners present on both sides, their
// members. Every map is walked in sorted key order so output is deterministic.
func diffProtoSection(s protoSection, old, new map[string]map[string]string) []Change {
	var changes []Change

	for _, owner := range slices.Sorted(maps.Keys(old)) {
		newMembers, exists := new[owner]
		if !exists {
			changes = append(changes, Change{
				Path:           fmt.Sprintf(s.ownerPathFmt, owner),
				Type:           Removed,
				OldValue:       owner,
				Classification: classify(s.ownerRule, Removed),
				Reason:         fmt.Sprintf("%s %s removed", s.ownerNoun, owner),
			})
			continue
		}
		changes = append(changes, diffProtoMembers(s, owner, old[owner], newMembers)...)
	}

	for _, owner := range slices.Sorted(maps.Keys(new)) {
		if _, exists := old[owner]; !exists {
			changes = append(changes, Change{
				Path:           fmt.Sprintf(s.ownerPathFmt, owner),
				Type:           Added,
				NewValue:       owner,
				Classification: classify(s.ownerRule, Added),
				Reason:         fmt.Sprintf("%s %s added", s.ownerNoun, owner),
			})
		}
	}

	return changes
}

// diffProtoMembers diffs one owner's members: a service's rpcs keyed by method
// name, or a message's fields keyed by field name. The map value is the rpc
// signature or the field's "<type> = <number>", so a unary-to-streaming rpc, a
// retyped field and a renumbered field all show up as Modified.
func diffProtoMembers(s protoSection, owner string, old, new map[string]string) []Change {
	var changes []Change

	for _, member := range slices.Sorted(maps.Keys(old)) {
		path := fmt.Sprintf(s.memberPathFmt, owner, member)
		label := fmt.Sprintf("%s %s.%s", s.memberNoun, owner, member)
		newSig, exists := new[member]
		if !exists {
			changes = append(changes, Change{
				Path:           path,
				Type:           Removed,
				OldValue:       old[member],
				Classification: classify(s.memberRule, Removed),
				Reason:         label + " removed",
			})
			continue
		}
		if old[member] != newSig {
			changes = append(changes, Change{
				Path:           path,
				Type:           Modified,
				OldValue:       old[member],
				NewValue:       newSig,
				Classification: classify(s.memberRule, Modified),
				Reason:         label + " modified",
			})
		}
	}

	for _, member := range slices.Sorted(maps.Keys(new)) {
		if _, exists := old[member]; !exists {
			changes = append(changes, Change{
				Path:           fmt.Sprintf(s.memberPathFmt, owner, member),
				Type:           Added,
				NewValue:       new[member],
				Classification: classify(s.memberRule, Added),
				Reason:         fmt.Sprintf("%s %s.%s added", s.memberNoun, owner, member),
			})
		}
	}

	return changes
}

var (
	protoBlockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
	protoLineCommentRe  = regexp.MustCompile(`//[^\n]*`)
	protoBlockRe        = regexp.MustCompile(`(?m)^\s*(service|message)\s+(\w+)\s*\{`)
	protoRPCRe          = regexp.MustCompile(`(?s)\brpc\s+(\w+)\s*\(([^)]*)\)\s*returns\s*\(([^)]*)\)`)
	protoFieldRe        = regexp.MustCompile(`^(?:(repeated|optional)\s+)?(map\s*<[^>]*>|[.\w]+)\s+(\w+)\s*=\s*(\d+)$`)
)

// protoStatementKeywords lead a statement that is never a field declaration.
var protoStatementKeywords = map[string]bool{
	"option": true, "reserved": true, "extensions": true,
	"import": true, "package": true, "syntax": true,
}

// extractProto scans a proto3 source file for its top-level services and
// messages. Comments are stripped first, then blocks are walked in source order,
// jumping past each block body so nested messages are never mistaken for
// top-level ones.
func extractProto(src string) protoAPI {
	src = stripProtoComments(src)
	api := protoAPI{
		services: make(map[string]map[string]string),
		messages: make(map[string]map[string]string),
	}

	for i := 0; i < len(src); {
		loc := protoBlockRe.FindStringSubmatchIndex(src[i:])
		if loc == nil {
			break
		}
		kind := src[i+loc[2] : i+loc[3]]
		name := src[i+loc[4] : i+loc[5]]
		open := i + loc[1] - 1 // index of the block's opening brace
		end := matchBrace(src, open)
		if end < 0 {
			break // unbalanced braces: stop rather than guess
		}
		body := src[open+1 : end-1]
		if kind == "service" {
			api.services[name] = extractRPCs(body)
		} else {
			api.messages[name] = extractFields(body)
		}
		i = end
	}

	return api
}

// stripProtoComments removes block and line comments.
//
// Comment markers embedded in string literals are not handled: an
// `option x = "http://example.com";` line loses its tail. That is harmless here
// because option statements are not part of the extracted surface, and it keeps
// this a three-line strip instead of a lexer.
func stripProtoComments(src string) string {
	src = protoBlockCommentRe.ReplaceAllString(src, "")
	return protoLineCommentRe.ReplaceAllString(src, "")
}

// matchBrace returns the index just past the brace matching the one at open, or
// -1 if the braces never balance.
func matchBrace(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// extractRPCs pulls every rpc out of a service body, keyed by method name with
// the whitespace-collapsed signature as the value.
func extractRPCs(body string) map[string]string {
	out := make(map[string]string)
	for _, m := range protoRPCRe.FindAllStringSubmatch(body, -1) {
		out[m[1]] = fmt.Sprintf("(%s) returns (%s)", collapseSpace(m[2]), collapseSpace(m[3]))
	}
	return out
}

// extractFields pulls the direct fields out of a message body, keyed by field
// name with "<type> = <number>" as the value. Nested message, enum and oneof
// bodies are skipped whole rather than mis-parsed as fields.
func extractFields(body string) map[string]string {
	out := make(map[string]string)
	var stmt strings.Builder
	depth := 0

	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '{':
			depth++
			stmt.Reset() // discard the nested block's header
		case '}':
			depth--
			stmt.Reset()
		case ';':
			if depth == 0 {
				addProtoField(out, stmt.String())
			}
			stmt.Reset()
		default:
			if depth == 0 {
				stmt.WriteByte(body[i])
			}
		}
	}

	return out
}

// addProtoField records one field statement (without its terminating semicolon)
// if it really is a field declaration.
func addProtoField(out map[string]string, stmt string) {
	stmt = collapseSpace(stmt)
	if protoStatementKeywords[strings.SplitN(stmt, " ", 2)[0]] {
		return
	}
	m := protoFieldRe.FindStringSubmatch(stmt)
	if m == nil {
		return
	}
	typ := m[2]
	if m[1] != "" {
		typ = m[1] + " " + typ
	}
	out[m[3]] = typ + " = " + m[4]
}

// collapseSpace normalises all runs of whitespace to single spaces so formatting
// changes alone never register as a signature change.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
