package delivery

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// CandidatesHint says where to find a valid value for an operation's to: the
// CLI command and the MCP tool that list a Stage's candidates.
const CandidatesHint = "list the versions this Stage may take, with their digests, with " +
	"`pacto delivery candidates --stage <stage>` or the pacto_delivery_candidates tool"

// CandidateVersions are the versions a Stage may be promoted to: what its
// candidate source offers that pacto publish published, newest first.
type CandidateVersions struct {
	Namespace string `json:"namespace"`
	Stage     string `json:"stage"`
	Service   string `json:"service"`
	// Running is the digest the Stage runs now.
	// +optional
	Running string `json:"running,omitempty"`
	// Source is where the candidates come from: the candidate OCIRepository
	// or the upstream Stage.
	Source   string             `json:"source"`
	Versions []CandidateVersion `json:"versions"`
	// Note says why the list is empty or may be incomplete.
	// +optional
	Note string `json:"note,omitempty"`
}

// CandidateVersion is one version a Stage may take.
type CandidateVersion struct {
	Digest string `json:"digest"`
	// Version is the service.version its contract declares.
	// +optional
	Version string `json:"version,omitempty"`
	// +optional
	Contract string `json:"contract,omitempty"`
	// Unreadable says why its contract could not be read.
	// +optional
	Unreadable string `json:"unreadable,omitempty"`
}

// CandidateDigests are the digests the published gate admits on stage, newest
// first: from a candidate OCIRepository the ones Flux verified the signature
// of, from an upstream Stage the ones it offered and verified. A digest that
// failed on the Stage is left out: it is never promoted again. upstream is the
// upstream Stage's status, nil when there is none or it cannot be read.
func CandidateDigests(stage StageObject, upstream *StageStatus) []string {
	var from []string
	switch c := stage.Spec.Candidates; {
	case c.UpstreamStage != nil || c.UpstreamRecords != nil:
		if upstream != nil {
			from = slices.DeleteFunc(slices.Clone(upstream.Offered), func(d string) bool { return !slices.Contains(upstream.Verified, d) })
		}
	case c.OCIRepositoryRef != nil:
		from = slices.Clone(stage.Status.Published)
	}
	from = slices.DeleteFunc(from, func(d string) bool { return slices.Contains(stage.Status.FailedDigests, d) })
	slices.Reverse(from)
	return from
}

// ListCandidates reads what every candidate digest of stage carries, through
// describe. All the reads share one registry bound: a registry that does not
// answer costs one bound, and the digests it left unread say so. A digest
// whose artifact names no contract is left out: pacto publish did not publish
// it. why is the reason an upstream Stage offered nothing.
func ListCandidates(ctx context.Context, stage StageObject, upstream *StageStatus, why string, describe func(context.Context, string) Revision) CandidateVersions {
	ctx, cancel := WithRegistryTimeout(ctx)
	defer cancel()
	out := CandidateVersions{Namespace: stage.Metadata.Namespace, Stage: stage.Metadata.Name, Service: stage.Spec.Service.Name,
		Running: stage.Status.Pointer, Source: candidateSource(stage.Spec.Candidates), Versions: []CandidateVersion{}}
	for _, d := range CandidateDigests(stage, upstream) {
		v := CandidateVersion{Digest: d}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			v.Unreadable = "not read, the registry did not answer within " + registryTimeout.String()
			out.Versions = append(out.Versions, v)
			continue
		}
		r := describe(ctx, d)
		switch {
		case r.NoContract:
			continue
		case r.Bundle == nil || r.Bundle.Contract == nil:
			v.Unreadable = r.Err
		default:
			v.Version, v.Contract = r.Bundle.Contract.Service.Version, r.ContractRef
		}
		out.Versions = append(out.Versions, v)
	}
	out.Note = candidatesNote(stage, upstream, why, out.Versions)
	return out
}

func candidateSource(c Candidates) string {
	switch {
	case c.UpstreamStage != nil || c.UpstreamRecords != nil:
		return "upstream Stage " + upstreamName(c)
	case c.OCIRepositoryRef != nil:
		return "candidate OCIRepository " + c.OCIRepositoryRef.Name
	}
	return ""
}

// candidatesNote says why a candidate list is empty or incomplete, or "".
func candidatesNote(stage StageObject, upstream *StageStatus, why string, vs []CandidateVersion) string {
	c := stage.Spec.Candidates
	switch {
	case c.UpstreamStage == nil && c.UpstreamRecords == nil && c.OCIRepositoryRef == nil:
		return "this Stage names no candidate source (spec.candidates), so it can promote nothing"
	case (c.UpstreamStage != nil || c.UpstreamRecords != nil) && upstream == nil && why != "":
		return why + ", so it offers no candidate"
	case (c.UpstreamStage != nil || c.UpstreamRecords != nil) && upstream == nil:
		return "the upstream Stage " + upstreamName(c) + " does not exist or cannot be read, so it offers no candidate"
	case slices.ContainsFunc(vs, func(v CandidateVersion) bool { return v.Unreadable != "" }):
		return "some candidates could not be read, so a version cannot be matched to them: name those by digest"
	case len(vs) == 0 && c.OCIRepositoryRef != nil:
		return "Flux has verified no signed version on " + c.OCIRepositoryRef.Name + " yet: publish one with pacto publish"
	case len(vs) == 0:
		return "the upstream Stage " + upstreamName(c) + " has offered and verified no version yet: promote one there first"
	}
	return ""
}

// Resolve picks the one candidate a version or tag names: the candidates whose
// contract declares that version, plus the candidate tagDigest is, when the
// tag resolves to one ("" when it names no tag). Exactly one must match; none,
// several or a candidate it could not read is refused, with the reason.
func (c CandidateVersions) Resolve(to, tagDigest string) (string, error) {
	var match []string
	for _, v := range c.Versions {
		if (v.Version == to || v.Digest == tagDigest) && !slices.Contains(match, v.Digest) {
			match = append(match, v.Digest)
		}
	}
	unread := slices.ContainsFunc(c.Versions, func(v CandidateVersion) bool { return v.Unreadable != "" })
	switch {
	case len(match) > 1:
		return "", fmt.Errorf("%q names %d versions this Stage may take (%s), so Pacto cannot tell which you mean: pass the digest", to, len(match), strings.Join(match, ", "))
	case unread:
		return "", fmt.Errorf("%q cannot be matched to exactly one published version, because Pacto could not read every candidate (%s): pass the digest instead",
			to, c.unreadable())
	case len(match) == 1:
		return match[0], nil
	case tagDigest != "":
		return "", fmt.Errorf("the tag %q points at %s, which is not a version this Stage may take: only a version pacto publish published that the %s offers is. %s",
			to, tagDigest, c.Source, c.listed())
	}
	return "", fmt.Errorf("no version %q was published for Stage %s/%s. %s", to, c.Namespace, c.Stage, c.listed())
}

// listed names the candidates, or why there are none.
func (c CandidateVersions) listed() string {
	if len(c.Versions) == 0 {
		return "It has no candidate: " + c.Note
	}
	parts := make([]string, len(c.Versions))
	for i, v := range c.Versions {
		parts[i] = v.Version + " (" + v.Digest + ")"
	}
	return "It may take " + strings.Join(parts, ", ")
}

func (c CandidateVersions) unreadable() string {
	var parts []string
	for _, v := range c.Versions {
		if v.Unreadable != "" {
			parts = append(parts, short(v.Digest)+": "+v.Unreadable)
		}
	}
	return strings.Join(parts, "; ")
}
