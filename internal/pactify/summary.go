package pactify

import (
	"fmt"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// maxSummaryLabels is how many externals the plan line names before ", N more".
const maxSummaryLabels = 10

// ExternalSummary is the plan line for the external dependencies the record
// fields hold, "External dependencies: N (<label>, ...), inferred", then one
// line per dependency with every place that names it. unread counts the
// external inputs Pacto did not read, so a folder that names none says so
// only of what was read.
func ExternalSummary(fields []contract.Inferred, unread int) []string {
	var labels, names, froms []string
	width := 0
	for _, f := range fields {
		if f.External == nil {
			continue
		}
		labels = append(labels, externalLabel(f))
		names, froms = append(names, f.Field), append(froms, externalFrom(f.External))
		width = max(width, len(f.Field))
	}
	var head string
	switch {
	case len(labels) == 0 && unread == 0:
		return []string{"External dependencies: none found, inferred"}
	case len(labels) == 0:
		return []string{fmt.Sprintf("External dependencies: none found in what was read, %s, inferred", notRead(unread))}
	case len(labels) > maxSummaryLabels:
		head = fmt.Sprintf("External dependencies: %d (%s, %d more), inferred", len(labels), strings.Join(labels[:maxSummaryLabels], ", "), len(labels)-maxSummaryLabels)
	default:
		head = fmt.Sprintf("External dependencies: %d (%s), inferred", len(labels), strings.Join(labels, ", "))
	}
	out := []string{head}
	for i, name := range names {
		out = append(out, fmt.Sprintf("%-*s  external, from %s", width, name, froms[i]))
	}
	if unread > 0 {
		out = append(out, notRead(unread))
	}
	return out
}

// notRead is "N sources not read".
func notRead(n int) string {
	if n == 1 {
		return "1 source not read"
	}
	return fmt.Sprintf("%d sources not read", n)
}

// externalLabel names the external of f in the plan line: its identity for
// a vendor or a host, <service>:<resource> for a managed resource and
// <service> for a managed service no resource of the folder names.
func externalLabel(f contract.Inferred) string {
	x := f.External
	parts := strings.Split(x.Identity, "/")
	switch {
	case len(parts) == 5:
		return parts[1] + ":" + parts[4]
	case len(parts) == 2 && parts[0] != "host" && x.Resource != nil && x.Resource.Name != "":
		return parts[1] + ":" + x.Resource.Name
	case len(parts) == 2 && parts[0] != "host":
		return parts[1]
	}
	return x.Identity
}

// externalFrom is every place x is named, joined with " and ".
func externalFrom(x *contract.InferredExternal) string {
	sdks := 0
	for _, s := range x.Sources {
		if s.Kind == "sdk" {
			sdks++
		}
	}
	out := make([]string, len(x.Sources))
	for i, s := range x.Sources {
		out[i] = s.Path
		if s.Line > 0 {
			out[i] = fmt.Sprintf("%s:%d", s.Path, s.Line)
		}
		if note := sourceNote(s, x, sdks == 1); note != "" {
			out[i] += " (" + note + ")"
		}
	}
	return strings.Join(out, " and ")
}

// sourceNote is what follows the place s in x's line: its key, the client
// library (onlySDK: the record keeps one client, so only the one sdk source
// names it) or the resource, with possible for a source that proves no call.
func sourceNote(s contract.InferredExternalSource, x *contract.InferredExternal, onlySDK bool) string {
	resource := s.Kind == "terraform" || s.Kind == "crossplane"
	switch {
	case s.Kind == "sdk" && x.Client != nil && onlySDK:
		return x.Client.SDK + ", possible"
	case s.Kind == "sdk":
		return "SDK, possible"
	case resource && x.Resource != nil && x.Resource.Name != "":
		return x.Resource.Type + " " + x.Resource.Name + ", possible"
	case resource:
		return s.Kind + ", possible"
	}
	return s.Key
}
