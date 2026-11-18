package ocireferrer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"
)

// tagPageSize is the tag listing page size [Tags] asks for when the caller
// set none.
const tagPageSize = 100

// maxTagPages bounds one tag listing, as maxReferrerPages bounds a referrers
// listing: past it the listing fails, never truncates. A var so a test can
// lower it.
var maxTagPages = 1000

// ErrTagTaken is returned by [PushWriteOnce] when the tag already names
// another manifest. A write-once tag is never moved.
var ErrTagTaken = errors.New("the tag already names another manifest")

var (
	errTagsUnsorted  = errors.New("the registry does not sort tags")
	errTagsPastBound = errors.New("passed the page bound")
	errTagsDone      = errors.New("done")
)

// Tags lists every tag of repo that starts with prefix. It seeks to just
// below prefix and stops at the first tag sorted past it, so it reads only
// the tags it returns, plus at most one page, on a registry that sorts tags
// as the distribution spec says. On one that does not, it scans every tag
// from the start. Either way it reads at most maxTagPages pages: the listing
// is complete or an error. An empty prefix lists every tag. It sets
// repo.TagListPageSize when the caller left it zero.
func Tags(ctx context.Context, repo *remote.Repository, prefix string) ([]string, error) {
	if repo.TagListPageSize == 0 {
		repo.TagListPageSize = tagPageSize
	}
	last := ""
	if prefix != "" {
		last = prefix[:len(prefix)-1] + string(prefix[len(prefix)-1]-1)
	}
	out, err := scanTags(ctx, repo, last, prefix, true)
	switch {
	case errors.Is(err, errTagsUnsorted):
		if out, err = scanTags(ctx, repo, "", prefix, false); errors.Is(err, errTagsPastBound) {
			return nil, fmt.Errorf("listing the tags of %s: the registry does not sort tags, and the full scan passed %d pages", repo.Reference, maxTagPages)
		}
	case errors.Is(err, errTagsPastBound):
		return nil, fmt.Errorf("listing the tags of %s from %q: more than %d pages", repo.Reference, last, maxTagPages)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// scanTags walks repo's tags after last and keeps those with prefix. With
// seek it checks the order and stops past prefix.
func scanTags(ctx context.Context, repo *remote.Repository, last, prefix string, seek bool) ([]string, error) {
	var out []string
	pages, prev := 0, last
	err := repo.Tags(ctx, last, func(tags []string) error {
		if pages++; pages > maxTagPages {
			return errTagsPastBound
		}
		if seek && (!slices.IsSorted(tags) || len(tags) > 0 && tags[0] < prev) {
			return errTagsUnsorted
		}
		for _, t := range tags {
			switch {
			case strings.HasPrefix(t, prefix):
				out = append(out, t)
			case seek && t > prefix:
				return errTagsDone
			}
			prev = t
		}
		return nil
	})
	if errors.Is(err, errTagsDone) {
		err = nil
	}
	return out, err
}

// Attached lists what is attached to subject: the native Referrers listing
// and, when tagPrefix is set, the names of repo's tags with that prefix
// (write-once records). A registry without the Referrers API is tolerated
// only with a tagPrefix, where the tags are the whole answer. The caller
// resolves the tags.
func Attached(ctx context.Context, repo *remote.Repository, subject ocispec.Descriptor, tagPrefix string) ([]ocispec.Descriptor, []string, error) {
	var descs []ocispec.Descriptor
	err := repo.Referrers(ctx, subject, "", func(page []ocispec.Descriptor) error {
		descs = append(descs, page...)
		return nil
	})
	if err != nil && (tagPrefix == "" || !errors.Is(err, errdef.ErrUnsupported)) {
		return nil, nil, err
	}
	if tagPrefix == "" {
		return descs, nil, nil
	}
	tags, err := Tags(ctx, repo, tagPrefix)
	if err != nil {
		return nil, nil, err
	}
	return descs, tags, nil
}

// PushWriteOnce writes art under tag, once: a tag that already names art is
// done (an idempotent retry), and one that names another manifest is
// [ErrTagTaken], never moved. After the push it resolves tag again and fails
// unless tag names art, so a write is confirmed before it is done. It never
// writes the shared sha256-<hex> referrers-tag index: the tag is
// content-addressed by the caller and one tag holds one record.
func PushWriteOnce(ctx context.Context, repo *remote.Repository, art Artifact, tag string) error {
	want := art.ManifestDesc.Digest
	got, err := repo.Resolve(ctx, tag)
	switch {
	case err == nil && got.Digest == want:
		return nil
	case err == nil:
		return fmt.Errorf("%w: %s names %s, not %s", ErrTagTaken, tag, got.Digest, want)
	case !errors.Is(err, errdef.ErrNotFound):
		return err
	}
	if err := pushBlobs(ctx, repo, art); err != nil {
		return err
	}
	if err := repo.PushReference(ctx, art.ManifestDesc, bytes.NewReader(art.Manifest), tag); err != nil {
		return err
	}
	if got, err = repo.Resolve(ctx, tag); err != nil {
		return fmt.Errorf("the write-once tag %s is not readable after the push: %w", tag, err)
	}
	if got.Digest != want {
		return fmt.Errorf("the write-once tag %s names %s after the push, not %s", tag, got.Digest, want)
	}
	return nil
}
