package oci

import (
	"context"
	"errors"
	"fmt"
	"strings"

	msemver "github.com/Masterminds/semver/v3"
	"github.com/trianalab/pacto/v3/pkg/logging"
	"github.com/trianalab/pacto/v3/pkg/semver"
)

// TagLister can list available tags for an OCI repository.
type TagLister interface {
	ListTags(ctx context.Context, repo string) ([]string, error)
}

// ErrNoMatchingTag means the repository published no semver tag the request
// could accept. It is a sentinel so a caller can tell "the registry holds
// nothing for you" apart from "the registry could not be asked" without
// matching on message text.
var ErrNoMatchingTag = errors.New("no matching tag")

// HasExplicitTag reports whether an OCI reference includes an explicit tag
// or digest (e.g. "repo:v1" or "repo@sha256:...").
func HasExplicitTag(ref string) bool {
	if strings.Contains(ref, "@") {
		return true
	}
	lastSlash := strings.LastIndex(ref, "/")
	lastColon := strings.LastIndex(ref, ":")
	return lastColon > lastSlash
}

// BestTag selects the highest semver tag from tags. If constraint is non-empty,
// only tags satisfying the semver constraint are considered.
//
// Which tags count as versions, and which of them is highest, is [semver]'s
// answer and not a second one: a repository's version list must not depend on
// whether the resolver or the dashboard asked. All that is left here is the
// constraint, which is the only part of the question this package owns.
func BestTag(tags []string, constraint string) (string, error) {
	// Descending, so the first tag that satisfies the constraint is the best one.
	sorted := semver.Filter(tags)

	if constraint == "" {
		if len(sorted) == 0 {
			return "", fmt.Errorf("no semver tags found: %w", ErrNoMatchingTag)
		}
		return sorted[0], nil
	}

	c, err := msemver.NewConstraint(constraint)
	if err != nil {
		return "", fmt.Errorf("invalid constraint %q: %w", constraint, err)
	}
	for _, tag := range sorted {
		// Filter already accepted these, so the re-parse cannot fail.
		v, _ := msemver.NewVersion(tag)
		if c.Check(v) {
			return tag, nil
		}
	}
	return "", fmt.Errorf("no tags satisfy constraint %q: %w", constraint, ErrNoMatchingTag)
}

// ResolveRef resolves an OCI reference that may be missing a tag by querying
// available tags and selecting the best semver match. If the ref already has
// an explicit tag or digest, it is returned unchanged.
func ResolveRef(ctx context.Context, lister TagLister, ref, constraint string) (string, error) {
	if HasExplicitTag(ref) {
		logging.LoggerFromContext(ctx).Debug("reference has explicit tag", "ref", ref)
		return ref, nil
	}
	logging.LoggerFromContext(ctx).Debug("resolving best tag for reference", "ref", ref, "constraint", constraint)
	tags, err := lister.ListTags(ctx, ref)
	if err != nil {
		return "", err
	}
	tag, err := BestTag(tags, constraint)
	if err != nil {
		return "", err
	}
	logging.LoggerFromContext(ctx).Debug("resolved tag", "ref", ref, "tag", tag)
	return ref + ":" + tag, nil
}
