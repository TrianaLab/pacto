package discover

import (
	"fmt"
	"path"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// The codes of what discovery recognized but could not read. A finding
// describes what was read, never a contract defect: it changes no validation
// result and no gate. Every message is built from a fixed template and only
// the code, a relative path, a line and names, never from an error's text or
// the file's bytes, which could quote a value.
const (
	CodeUnsupportedFile         = "DISCOVERY_UNSUPPORTED_FILE"
	CodeAmbiguous               = "DISCOVERY_AMBIGUOUS"
	CodeChartDependencyUnlocked = "DISCOVERY_CHART_DEPENDENCY_UNLOCKED"
	CodeUnreadable              = "DISCOVERY_UNREADABLE"
	CodeUnresolved              = "DISCOVERY_UNRESOLVED"
	CodeTruncated               = contract.InferredFindingTruncated
)

// Why a file is unreadable, each the end of a fixed message.
const (
	unreadableNotRegular = "is not a regular file, so it is not read"
	unreadableTooLarge   = "is over 1 MiB, so it is not read"
	unreadableNotYAML    = "is not valid YAML, so it is not read"
	unreadableFailed     = "could not be read"
)

// unreadableMessage says why the file at source was not read; why is one of
// the unreadable* texts.
func unreadableMessage(source, why string) string { return source + " " + why }

// unresolvedPortMessage says an EXPOSE names a port Pacto cannot read as one
// number.
func unresolvedPortMessage(source string) string {
	return "EXPOSE at " + source + " names a port through a variable or a range, so it is not read"
}

// truncatedMessage says discovery stopped after read files with left not read.
func truncatedMessage(read, left int) string {
	return fmt.Sprintf("discovery stopped after %d files; %d files were not read, so results are partial", read, left)
}

// ambiguousSpecMessage says several files hold specs of one kind, label, so
// none is recorded.
func ambiguousSpecMessage(label string, paths []string) string {
	return fmt.Sprintf("%d %s files: %s; declare the one this service serves", len(paths), label, strings.Join(paths, ", "))
}

// unlockedDependencyMessage says the chart dependency name has no Chart.lock
// digest, so it is not read.
func unlockedDependencyMessage(name string) string {
	return "chart dependency '" + name + "' has no Chart.lock digest, not read"
}

// unsupportedFiles are the files Pacto recognizes but does not read, by base
// name pattern, each with why.
// ponytail: a fixed table of the formats seen so far; the next one is one row.
var unsupportedFiles = map[string]string{
	"*.graphql":  unsupportedGraphQL,
	"*.graphqls": unsupportedGraphQL,
	"*.gql":      unsupportedGraphQL,
}

const unsupportedGraphQL = "GraphQL is not a Pacto interface type"

// unsupported is a finding when the file p, named name, is one Pacto
// recognizes but does not read.
func (r *reader) unsupported(p, name string) {
	for pattern, why := range unsupportedFiles {
		if ok, _ := path.Match(pattern, name); ok {
			r.find(CodeUnsupportedFile, p, unsupportedMessage(p, why))
			return
		}
	}
}

// unsupportedMessage says the file at source is not read, and why.
func unsupportedMessage(source, why string) string { return source + " is not read: " + why }
