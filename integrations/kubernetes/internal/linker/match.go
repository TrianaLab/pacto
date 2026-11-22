/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package linker links running workloads that no Pacto targets to the
// published contract they run, by the pacto.trianalab.io/contract annotation
// or by the running image digest an indexed bundle's images record names.
// Everything here is pure or reads through an injected Reader.
package linker

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/semver"
)

// AnnotationContract is the workload annotation that names the contract the
// workload runs, as oci://host/repo:tag or oci://host/repo@sha256:<hex>.
const AnnotationContract = "pacto.trianalab.io/contract"

// LimitationAnnotationInvalid: a workload's contract annotation is not a
// strict OCI reference, so it links nothing. The message never repeats the
// value.
const LimitationAnnotationInvalid = "LINK_ANNOTATION_INVALID"

// maxAnnotationLen bounds the contract annotation.
const maxAnnotationLen = 255

// RunningImage is one running container: the repository of its spec image
// with tag and digest removed, the tag it names (empty when pinned by digest
// only) and the repository digest the runtime resolved.
type RunningImage struct {
	Repository, Tag, Digest string
}

// Workload is a workload the linker may link.
type Workload struct {
	Namespace, Kind, Name string
	// Annotation is the AnnotationContract value, "" when unset.
	Annotation string
	// Labels are the pod template labels.
	Labels map[string]string
	Images []RunningImage
	// Targeted says a Pacto targets the workload: the Pacto wins.
	Targeted bool
}

// Candidate is a published version whose signed images record names a
// digest.
type Candidate struct {
	// Service is who the candidate speaks for: its bundle's repository, never
	// the index entry's claim. The linker confirms the fleet key from the
	// bundle it loads.
	Service string
	Version string
	// Bundle is the published bundle, repo@digest.
	Bundle string
	// Repository is the image repository the images record names.
	Repository string
	// Signed says a signature referrer is attached to the images record.
	Signed bool
}

// Link is how a workload is linked to a published contract.
type Link struct {
	// Signal is fleet.LinkSignalImageDigest or fleet.LinkSignalAnnotation.
	Signal string
	// Value is the matched digest or the annotation.
	Value string
	// Ref is the OCI reference the transient Pacto loads.
	Ref string
	// OtherVersions are the other release versions recording the digest.
	OtherVersions []string
}

// Match links w per the link rules: a targeted workload is never linked; a
// valid annotation wins over the digest; otherwise the signed candidates of
// the running digests whose record repository is the container's link when
// they belong to one service. A refusal or ambiguity is the limitation, with
// no link.
func Match(w Workload, byDigest map[string][]Candidate) (Link, fleet.Limitation) {
	switch {
	case w.Targeted:
		return Link{}, fleet.Limitation{}
	case w.Annotation != "":
		if !validAnnotation(w.Annotation) {
			return Link{}, fleet.Limitation{Code: LimitationAnnotationInvalid, Message: caller(w) + " carries a " +
				AnnotationContract + " annotation that is not an oci:// reference to a tag or a sha256 digest, so it links nothing"}
		}
		return Link{Signal: fleet.LinkSignalAnnotation, Value: w.Annotation, Ref: w.Annotation}, fleet.Limitation{}
	}
	return digestLink(w, byDigest)
}

// matched is a candidate and the running digest it matched.
type matched struct {
	Candidate
	digest string
}

func digestLink(w Workload, byDigest map[string][]Candidate) (Link, fleet.Limitation) {
	byService := map[string][]matched{}
	for _, img := range w.Images {
		for _, c := range byDigest[img.Digest] {
			if c.Signed && repositoryOf(c.Repository) == img.Repository {
				byService[c.Service] = append(byService[c.Service], matched{c, img.Digest})
			}
		}
	}
	switch len(byService) {
	case 0:
		return Link{}, fleet.Limitation{}
	case 1:
	default:
		services := slices.Sorted(maps.Keys(byService))
		return Link{}, fleet.Limitation{Code: fleet.LimitationInferredReplacementAmbiguous, Message: fmt.Sprintf(
			"%s runs images that several services published (%s), so it links to none", caller(w), strings.Join(services, ", "))}
	}
	var cands []matched
	for _, cs := range byService {
		cands = cs
	}
	return newest(cands), fleet.Limitation{}
}

// newest links the highest version among cands, prereleases dropped when a
// release is among them; the other versions of that pool are OtherVersions.
func newest(cands []matched) Link {
	if slices.ContainsFunc(cands, func(c matched) bool { return !prerelease(c.Version) }) {
		cands = slices.DeleteFunc(cands, func(c matched) bool { return prerelease(c.Version) })
	}
	slices.SortStableFunc(cands, func(a, b matched) int { return semver.Compare(b.Version, a.Version) })
	best := cands[0]
	var others []string
	for _, c := range cands[1:] {
		if c.Version != best.Version && !slices.Contains(others, c.Version) {
			others = append(others, c.Version)
		}
	}
	return Link{Signal: fleet.LinkSignalImageDigest, Value: best.digest, Ref: best.Bundle, OtherVersions: others}
}

func prerelease(version string) bool {
	core, _, _ := strings.Cut(version, "+")
	return strings.Contains(core, "-")
}

func caller(w Workload) string { return fleet.K8sCaller("", w.Namespace, w.Kind, w.Name) }

var sha256RE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// validAnnotation accepts only oci://host/repo:tag or
// oci://host/repo@sha256:<64 hex>, with no userinfo and within
// maxAnnotationLen.
func validAnnotation(v string) bool {
	rest, ok := strings.CutPrefix(v, "oci://")
	host, _, _ := strings.Cut(rest, "/")
	if !ok || len(v) > maxAnnotationLen || strings.Contains(host, "@") {
		return false
	}
	ref, err := name.ParseReference(rest, name.StrictValidation)
	if err != nil || ref.Context().RegistryStr() != host {
		return false
	}
	repo, digest, pinned := strings.Cut(rest, "@")
	if pinned {
		return sha256RE.MatchString(digest) && !strings.Contains(repo[len(host):], ":")
	}
	return true
}
