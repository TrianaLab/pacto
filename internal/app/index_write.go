package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// indexWriteTimeout bounds writing one organization index entry.
var indexWriteTimeout = 30 * time.Second

// IndexNone turns the organization index off for publish, push and impact.
const IndexNone = "none"

// IndexNever is what an HTTP pacto mcp server sets: impact reads no index
// and its answer carries no consumerIndex, as on a server whose callers sign
// in. The --index flag refuses it, so only a caller can set it.
const IndexNever = "never"

// errIndexNotFound is every index candidate absent: no repository, or one
// with no tag.
var errIndexNotFound = errors.New("no organization index")

// digestPattern finds digests in registry error text, which the index text
// never carries.
var digestPattern = regexp.MustCompile(`sha256:[0-9a-f]{64}`)

// indexCandidates are the index repositories to try, in order: override
// alone when set, else <root>/pacto-index under each registry root of the
// provider repository (D8).
func indexCandidates(override, providerRepo string) []string {
	if override != "" {
		return []string{contract.TrimOCIScheme(override)}
	}
	var out []string
	for _, root := range registryRoots(providerRepo) {
		out = append(out, root+"/"+orgindex.RepositoryName)
	}
	return out
}

// indexRepository is the first candidate that lists a tag, with its tags and
// a note naming the absent candidates it fell through. Only a definite not found
// (NAME_UNKNOWN, a 404) or an empty listing falls through: any other error
// stops at that candidate, so a denied index never hides behind another.
// Every candidate absent returns the first with errIndexNotFound.
func (s *Service) indexRepository(ctx context.Context, candidates []string) (repo, note string, tags []string, err error) {
	env := s.env()
	var absent []string
	for _, c := range candidates {
		r, err := env.refusalRepo(c)
		if err == nil {
			tags, err = ocireferrer.Tags(ctx, r, "")
		}
		switch {
		case err == nil && len(tags) > 0:
			if len(absent) > 0 {
				note = "after " + strings.Join(absent, ", ") + ": not found"
			}
			return c, note, tags, nil
		case err != nil && !ocireferrer.RepositoryNotFound(err):
			return c, "", nil, err
		}
		absent = append(absent, c)
	}
	return candidates[0], "", nil, fmt.Errorf("%w at %s", errIndexNotFound, strings.Join(candidates, " or "))
}

// keepIndexEntry writes the organization index entry of b, published as
// bundleRef (repo:version) with digest, and returns what to tell the person
// who published: where it was written, or why not (starting "not indexed").
// It never changes the publish's outcome and never overwrites an entry.
// override is --index: "" derives the repository, IndexNone writes nothing.
func (s *Service) keepIndexEntry(ctx context.Context, b *contract.Bundle, bundleRef, digest string, pub orgindex.Publisher, override string) string {
	if override == IndexNone {
		return ""
	}
	bundleRepo, tag, _ := contract.SplitOCIRef(contract.TrimOCIScheme(bundleRef))
	if tag == "" || tag != b.Contract.Service.Version {
		return "not indexed: pushed as " + cmp.Or(tag, "a digest") + "; the index names versions"
	}
	ctx, cancel := context.WithTimeout(ctx, indexWriteTimeout)
	defer cancel()
	repo, entryTag, note, err := s.writeIndexEntry(ctx, b, bundleRepo, digest, pub, override)
	switch {
	case errors.Is(err, ocireferrer.ErrTagTaken):
		return "not indexed in " + repo + ": " + repo + ":" + entryTag + " names another entry; index entries are never overwritten"
	case err != nil:
		return digestPattern.ReplaceAllString("not indexed in "+repo+": "+err.Error(), "sha256:…")
	}
	return strings.TrimSpace("indexed as " + repo + ":" + entryTag + " " + note)
}

// writeIndexEntry builds, binds and writes the entry; repo is where it was
// to go, also on failure.
func (s *Service) writeIndexEntry(ctx context.Context, b *contract.Bundle, bundleRepo, digest string, pub orgindex.Publisher, override string) (repo, tag, note string, err error) {
	env := s.env()
	key := fleet.NewServiceKeyDomain(contract.OCIDomain(bundleRepo), b.Contract.Service.Name)
	mode := s.registryMode(ctx, bundleRepo, digest)
	repo, note, _, err = s.indexRepository(ctx, indexCandidates(override, bundleRepo))
	if err != nil && !errors.Is(err, errIndexNotFound) {
		return repo, "", "", err
	}
	e := orgindex.NewEntry(key, b.Contract, bundleRepo+"@"+digest, pub, mode)
	if tag, err = orgindex.Tag(key, e.Version); err != nil {
		return repo, "", "", err
	}
	if err = e.Validate(repo, tag); err != nil {
		return repo, tag, "", err
	}
	payload, err := orgindex.Encode(e)
	if err != nil {
		return repo, tag, "", err
	}
	r, err := env.refusalRepo(repo)
	if err != nil {
		return repo, tag, "", err
	}
	art := ocireferrer.Build(orgindex.ArtifactType, orgindex.MediaType, payload, nil, nil)
	return repo, tag, note, ocireferrer.PushWriteOnce(ctx, r, art, tag)
}

// registryMode is the probe's mode for the registry holding bundleRepo, the
// just-published digest its subject, or "unknown" when the probe cannot tell.
func (s *Service) registryMode(ctx context.Context, bundleRepo, digest string) string {
	r, err := s.env().refusalRepo(bundleRepo)
	if err != nil {
		return "unknown"
	}
	caps, err := ocireferrer.Probes.Probe(ctx, r, digest, false)
	if err != nil {
		return "unknown"
	}
	return caps.Mode()
}

// publisherOf is the publisher an entry claims, from the signing policy that
// signed the version: the keyless identity and issuer, or the key's hint.
func publisherOf(p signing.Policy) orgindex.Publisher {
	pub := orgindex.Publisher{Identity: p.Identity, Issuer: p.Issuer}
	if p.Key != nil {
		pub.Key = signing.Hint(p.Key)
	}
	return pub
}
