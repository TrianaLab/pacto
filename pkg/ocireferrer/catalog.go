package ocireferrer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/errcode"
)

// catalogPageSize is the _catalog page size [Catalog] asks for.
const catalogPageSize = 100

var (
	// ErrCatalogUnsupported is wrapped by a [Catalog] listing the registry
	// does not serve (404 or 405).
	ErrCatalogUnsupported = errors.New("serves no _catalog")
	// ErrCatalogRefused is wrapped by a [Catalog] listing the registry
	// refused to the credential (401 or 403).
	ErrCatalogRefused = errors.New("refused _catalog to this credential")
	// ErrCatalogTruncated is returned with the names read when a [Catalog]
	// listing passed its name or page bound.
	ErrCatalogTruncated = errors.New("the _catalog listing passed its bound")

	errCatalogDone = errors.New("done")
)

// Catalog lists the repositories under path on the registry holding repo,
// every repository when path is empty. It seeks to path (the catalog is
// lexically ordered) and stops at the first name sorted past path + "/", so
// the repositories of a shared registry outside path are never paged
// through. It keeps at most max names and reads at most maxTagPages pages;
// past either bound it returns what it read with ErrCatalogTruncated.
func Catalog(ctx context.Context, repo *remote.Repository, path string, max int) ([]string, error) {
	reg := catalogRegistry(clientOf(repo), repo)
	reg.RepositoryListPageSize = catalogPageSize
	prefix := ""
	if path != "" {
		prefix = path + "/"
	}
	out, seen, err := catalogWalk(ctx, reg, path, prefix, max)
	// ponytail: zot answers a last that names no repository with an empty
	// list, so an empty seek walks again from the start. On distribution
	// that costs one walk only when nothing sorts past path.
	if err == nil && seen == 0 && path != "" {
		out, _, err = catalogWalk(ctx, reg, "", prefix, max)
	}
	var resp *errcode.ErrorResponse
	host := repo.Reference.Registry
	switch {
	case err == nil, errors.Is(err, errCatalogDone):
		return out, nil
	case errors.Is(err, ErrCatalogTruncated):
		return out, err
	case errors.As(err, &resp) && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed):
		return nil, fmt.Errorf("%s %w", host, ErrCatalogUnsupported)
	case errors.As(err, &resp) && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden):
		return nil, fmt.Errorf("%s %w", host, ErrCatalogRefused)
	}
	return nil, err
}

// catalogWalk pages the catalog from last, keeps the names under prefix
// (at most max) and stops at the first name sorted past prefix. seen is how
// many names the registry returned.
func catalogWalk(ctx context.Context, reg *remote.Registry, last, prefix string, max int) (out []string, seen int, err error) {
	pages := 0
	err = reg.Repositories(ctx, last, func(names []string) error {
		if pages++; pages > maxTagPages {
			return ErrCatalogTruncated
		}
		seen += len(names)
		for _, n := range names {
			switch {
			case strings.HasPrefix(n, prefix):
				if len(out) == max {
					return ErrCatalogTruncated
				}
				out = append(out, n)
			case n > prefix:
				return errCatalogDone
			}
		}
		return nil
	})
	return out, seen, err
}
