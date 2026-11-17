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

// unresolvedTemplateMessage says the template line at source sets a value
// through an expression Pacto does not resolve.
func unresolvedTemplateMessage(source string) string {
	return source + " sets a value through a template expression Pacto does not resolve without rendering the chart, so what it sets there is not read"
}

// emailOwnersMessage says the CODEOWNERS rule at source names its owners by
// email only.
func emailOwnersMessage(source string) string {
	return source + " names owners by email only, which Pacto cannot read as a team or a person, so no owner is read"
}

// unresolvedSystemMessage says the catalog line at source names a system
// that does not make a domain.
func unresolvedSystemMessage(source string) string {
	return source + " names a system that is not a valid domain, so it is not read"
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
	"*.graphql":     unsupportedGraphQL,
	"*.graphqls":    unsupportedGraphQL,
	"*.gql":         unsupportedGraphQL,
	"helmfile.yaml": unsupportedHelmfile,
	"helmfile.yml":  unsupportedHelmfile,
	"Tiltfile":      unsupportedTiltfile,
	"*.jsonnet":     unsupportedJsonnet,
	"*.libsonnet":   unsupportedJsonnet,
	"*.cue":         unsupportedCUE,
}

// Why a file, or a part of one, is not read.
const (
	unsupportedGraphQL        = "GraphQL is not a Pacto interface type"
	unsupportedHelmCharts     = "Kustomize helmCharts are rendered by Helm, which Pacto does not run"
	unsupportedComposeInclude = "Compose include names files Pacto does not follow"
	unsupportedComposeExtends = "Compose extends from another file, which Pacto does not follow"
	unsupportedComposeEnvFile = "Compose env_file holds values, which Pacto never reads"
	unsupportedHelmfile       = "Helmfile releases are rendered by Helmfile, which Pacto does not run"
	unsupportedTiltfile       = "a Tiltfile is a program, which Pacto does not run"
	unsupportedJsonnet        = "Jsonnet is a program, which Pacto does not evaluate"
	unsupportedCUE            = "CUE is evaluated by the CUE tool, which Pacto does not run"
	unsupportedApplicationSet = "an Argo CD ApplicationSet generates Applications, which Pacto does not expand"
)

// remoteResourceMessage says the kustomization line at source names the
// remote resource ref, without its credentials.
func remoteResourceMessage(source, ref string) string {
	return source + " names the remote Kustomize resource " + ref + "; Pacto reads only files in the folder, so it is not read"
}

// unresolvedResourceMessage says the kustomization line at source names a
// resource Pacto cannot read in the folder.
func unresolvedResourceMessage(source string) string {
	return source + " names a Kustomize resource outside the folder or missing from it, so it is not read"
}

// envNameMessage says source names an environment the contract does not
// accept as a name.
func envNameMessage(source string) string {
	return source + " names an environment that is not a valid environment name (lowercase letters, digits and hyphens, at most 63), so it is not read"
}

// gitopsPathMessage says the GitOps object line at source names a path
// outside the folder.
func gitopsPathMessage(source string) string {
	return source + " names a path outside the folder, so what it deploys there is not read"
}

// ambiguousNamesMessage says source lists several what, names, and none is
// named like the service, so none is read.
func ambiguousNamesMessage(source, what string, names []string) string {
	return fmt.Sprintf("%s lists %d %s: %s; none is named like this service, so none is read", source, len(names), what, strings.Join(names, ", "))
}

// unsupported reports whether the file p, named name, is one Pacto
// recognizes but does not read, and then it is a finding.
func (r *reader) unsupported(p, name string) bool {
	for pattern, why := range unsupportedFiles {
		if ok, _ := path.Match(pattern, name); ok {
			r.find(CodeUnsupportedFile, p, unsupportedMessage(p, why))
			return true
		}
	}
	return false
}

// unsupportedMessage says the file at source is not read, and why.
func unsupportedMessage(source, why string) string { return source + " is not read: " + why }
