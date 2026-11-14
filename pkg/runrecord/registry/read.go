package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// MaxRecords bounds how many run records one read lists across every anchor.
// Registry data is untrusted, so a read past it stops and says so.
var MaxRecords = 1000

// MaxPages bounds the pages of one listing: the anchor tag scan or one
// service's referrers. Registry data is untrusted, so a read past it stops
// and says so.
var MaxPages = 64

// ReadTimeout bounds a whole read of a stats repository.
const ReadTimeout = 30 * time.Second

// errBound stops a listing callback at a bound; the read reports it as data.
var errBound = errors.New("run record read bound reached")

// Skip reasons, as readRecord returns them.
const (
	skipMalformed  = "malformed"
	skipOversized  = "oversized"
	skipUnreadable = "unreadable"
)

// AnchorError is a listing of one anchor that failed. A caller may drop the
// anchor and say so.
type AnchorError struct {
	Tag string
	Err error
}

func (e *AnchorError) Error() string {
	return "reading the run records under " + e.Tag + ": " + e.Err.Error()
}

func (e *AnchorError) Unwrap() error { return e.Err }

// reader is one bounded read.
type reader struct {
	repo   *remote.Repository
	set    stats.RecordSet
	listed int
}

// Read reads the run records attached to anchors, the anchor tags of the
// services to read (runrecord.AnchorTag(service)), or to every anchor in repo
// when anchors is empty. A missing anchor holds no records. A record that
// cannot be counted is in Skipped, a bound that cut the read is in Truncated,
// and a deadline or a failed listing is an error, never fewer records.
func Read(ctx context.Context, repo *remote.Repository, anchors []string) (stats.RecordSet, error) {
	r := &reader{repo: repo}
	if len(anchors) == 0 {
		var err error
		if anchors, err = r.anchorTags(ctx); err != nil {
			return stats.RecordSet{}, err
		}
	}
	for _, tag := range anchors {
		if r.listed >= MaxRecords && r.set.Truncated != "" {
			break
		}
		if err := r.readAnchor(ctx, tag); err != nil {
			return stats.RecordSet{}, err
		}
	}
	return r.set, nil
}

// anchorTags scans repo's tags for run-record anchors, sorted and unique.
func (r *reader) anchorTags(ctx context.Context) ([]string, error) {
	var tags []string
	pages := 0
	err := r.repo.Tags(ctx, "", func(page []string) error {
		if pages++; pages > MaxPages {
			return r.cut(fmt.Sprintf("more than %d pages listing anchors", MaxPages), "")
		}
		for _, t := range page {
			if strings.HasPrefix(t, runrecord.AnchorTagPrefix) {
				tags = append(tags, t)
			}
		}
		return nil
	})
	if err = bounded(ctx, err); err != nil && !repositoryMissing(err) {
		return nil, err
	}
	slices.Sort(tags)
	return slices.Compact(tags), nil
}

// readAnchor lists the referrers of the anchor tagged tag and reads each.
func (r *reader) readAnchor(ctx context.Context, tag string) error {
	anchor, err := r.repo.Resolve(ctx, tag)
	if errors.Is(err, errdef.ErrNotFound) {
		return nil
	}
	if err != nil {
		return anchorErr(ctx, tag, err)
	}
	descs, err := r.list(ctx, anchor, tag)
	if err != nil {
		return anchorErr(ctx, tag, err)
	}
	for _, d := range descs {
		e, skip, err := readRecord(ctx, r.repo, d, tag)
		switch {
		case err != nil:
			return err
		case skip != "":
			r.skip(tag, skip)
		case e != nil:
			r.set.Entries = append(r.set.Entries, *e)
		}
	}
	return nil
}

// list collects the referrers of anchor within the page and record bounds.
func (r *reader) list(ctx context.Context, anchor ocispec.Descriptor, tag string) ([]ocispec.Descriptor, error) {
	var descs []ocispec.Descriptor
	pages := 0
	err := r.repo.Referrers(ctx, anchor, "", func(page []ocispec.Descriptor) error {
		if pages++; pages > MaxPages {
			return r.cut(fmt.Sprintf("more than %d pages listing one service's records", MaxPages), tag)
		}
		room := MaxRecords - r.listed
		take := page[:min(len(page), room)]
		descs = append(descs, take...)
		r.listed += len(take)
		if len(page) > room {
			return r.cut(fmt.Sprintf("more than %d run records", MaxRecords), tag)
		}
		return nil
	})
	return descs, bounded(ctx, err)
}

// cut records the first bound that cut the read and stops the listing.
func (r *reader) cut(msg, tag string) error {
	if r.set.Truncated == "" {
		r.set.Truncated, r.set.TruncatedAnchor = msg, tag
	}
	return errBound
}

func (r *reader) skip(tag, reason string) {
	if r.set.Skipped == nil {
		r.set.Skipped = map[string]stats.Skipped{}
	}
	add := map[string]stats.Skipped{
		skipMalformed:  {Malformed: 1},
		skipOversized:  {Oversized: 1},
		skipUnreadable: {Unreadable: 1},
	}[reason]
	r.set.Skipped[tag] = r.set.Skipped[tag].Add(add)
}

// bounded turns a bound into success (the read reports it) and a failure the
// context caused into the context's error.
func bounded(ctx context.Context, err error) error {
	if errors.Is(err, errBound) {
		return nil
	}
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// repositoryMissing is a tag scan of a repository nothing was published to
// yet: it holds no records.
func repositoryMissing(err error) bool {
	var resp *errcode.ErrorResponse
	return errors.As(err, &resp) && resp.StatusCode == http.StatusNotFound
}

// anchorErr is a failed listing of tag, unless the context caused it.
func anchorErr(ctx context.Context, tag string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return &AnchorError{Tag: tag, Err: err}
}

// readRecord reads the run record referrer d of the anchor tagged wantTag. It
// returns nil and no skip for another tool's referrer.
func readRecord(ctx context.Context, repo *remote.Repository, d ocispec.Descriptor, wantTag string) (*stats.Entry, string, error) {
	// A listing that names another artifact type is another tool's referrer.
	// A legacy registry names the config type, the empty JSON every run
	// record has, so the manifest decides.
	if d.ArtifactType != "" && d.ArtifactType != runrecord.ArtifactType && d.ArtifactType != ocispec.MediaTypeEmptyJSON {
		return nil, "", nil
	}
	if d.Size > runrecord.MaxRecordBytes {
		return nil, skipOversized, nil
	}
	raw, err := content.FetchAll(ctx, repo, d)
	if err != nil {
		return unreadable(ctx)
	}
	var m ocispec.Manifest
	if json.Unmarshal(raw, &m) != nil || m.ArtifactType != runrecord.ArtifactType {
		return nil, "", nil
	}
	if len(m.Layers) != 1 {
		return nil, skipMalformed, nil
	}
	if m.Layers[0].Size > runrecord.MaxRecordBytes {
		return nil, skipOversized, nil
	}
	layer, err := content.FetchAll(ctx, repo, m.Layers[0])
	if err != nil {
		return unreadable(ctx)
	}
	rec, err := runrecord.Parse(layer)
	if err != nil || runrecord.AnchorTag(rec.Service) != wantTag {
		return nil, skipMalformed, nil
	}
	return &stats.Entry{Record: rec, Digest: d.Digest.String(), Anchor: wantTag}, "", nil
}

// unreadable is a skipped record, unless the context caused the failure.
func unreadable(ctx context.Context) (*stats.Entry, string, error) {
	if ctx.Err() != nil {
		return nil, "", ctx.Err()
	}
	return nil, skipUnreadable, nil
}
