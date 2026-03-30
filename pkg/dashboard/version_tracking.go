package dashboard

import (
	"strings"

	"github.com/Masterminds/semver/v3"
)

// Version policy constants describe how a service tracks its contract version.
const (
	VersionPolicyTracking     = "tracking"      // follows latest (no explicit pin)
	VersionPolicyPinnedTag    = "pinned-tag"    // pinned to a specific semver tag
	VersionPolicyPinnedDigest = "pinned-digest" // pinned to an immutable digest
)

// NormalizeResolutionPolicy converts the operator's resolutionPolicy value
// (Latest, PinnedTag, PinnedDigest) to dashboard constants. Returns empty
// string for unrecognized values.
func NormalizeResolutionPolicy(operatorPolicy string) string {
	switch operatorPolicy {
	case "Latest":
		return VersionPolicyTracking
	case "PinnedTag":
		return VersionPolicyPinnedTag
	case "PinnedDigest":
		return VersionPolicyPinnedDigest
	default:
		return ""
	}
}

// ClassifyVersionPolicy is a conservative fallback that derives the version
// tracking policy from a resolvedRef string. Used only when the operator does
// not provide resolutionPolicy (non-K8s sources, older operator versions).
//
// Rules:
//   - Contains "@sha256:" → pinned-digest
//   - Contains an explicit semver tag → pinned-tag
//   - Otherwise → empty string (unknown/ambiguous)
//
// This fallback intentionally does NOT infer "tracking" because:
//   - "latest" is not a valid Pacto contract version
//   - An unversioned OCI ref may resolve to a concrete semver tag,
//     making it impossible to distinguish tracking from pinned
func ClassifyVersionPolicy(resolvedRef string) string {
	if resolvedRef == "" {
		return ""
	}

	// Digest pin: ref contains @sha256:
	if strings.Contains(resolvedRef, "@sha256:") {
		return VersionPolicyPinnedDigest
	}

	// Extract tag: everything after the last ":" that comes after the last "/".
	tag := extractTag(resolvedRef)
	if tag == "" {
		return ""
	}

	// If the tag is valid semver, it's a pinned tag.
	if _, err := semver.NewVersion(tag); err == nil {
		return VersionPolicyPinnedTag
	}

	// Non-semver tag (e.g. "main", "dev", "latest") — ambiguous, omit.
	return ""
}

// extractTag returns the tag portion of an OCI ref, or empty string if none.
// Handles "registry/repo:tag" and "registry/repo" (no tag).
func extractTag(ref string) string {
	// Strip digest if present.
	if idx := strings.Index(ref, "@"); idx >= 0 {
		ref = ref[:idx]
	}
	lastSlash := strings.LastIndex(ref, "/")
	lastColon := strings.LastIndex(ref, ":")
	if lastColon > lastSlash {
		return ref[lastColon+1:]
	}
	return ""
}

// ComputeLatestAvailable returns the highest semver version from a version list.
// The list is assumed to be sorted descending (latest first), so we return the
// first entry with a valid semver tag.
func ComputeLatestAvailable(versions []Version) string {
	for _, v := range versions {
		if _, err := semver.NewVersion(v.Version); err == nil {
			return v.Version
		}
	}
	return ""
}

// IsUpdateAvailable returns true when latest is a higher semver than current.
func IsUpdateAvailable(current, latest string) bool {
	if current == "" || latest == "" {
		return false
	}
	cur, err := semver.NewVersion(current)
	if err != nil {
		return false
	}
	lat, err := semver.NewVersion(latest)
	if err != nil {
		return false
	}
	return lat.GreaterThan(cur)
}

// MarkCurrentVersion sets IsCurrent=true on the version matching currentVersion.
func MarkCurrentVersion(versions []Version, currentVersion string) {
	if currentVersion == "" {
		return
	}
	for i := range versions {
		if versions[i].Version == currentVersion {
			versions[i].IsCurrent = true
			return
		}
	}
}
