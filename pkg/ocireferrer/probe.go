package ocireferrer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

const (
	// probeTimeout bounds one probe of a host.
	probeTimeout = 10 * time.Second
	// probeRetryAfter is how long a failed probe is remembered, so a refresh
	// loop does not hammer a registry that is down.
	probeRetryAfter = time.Minute
	// maxProbeBody bounds how much of a referrers error body the probe reads.
	maxProbeBody = 1 << 20
)

// Prober probes registry hosts and remembers each answer per host.
type Prober struct {
	// Now is the clock; nil is time.Now.
	Now  func() time.Time
	mu   sync.Mutex
	seen map[string]probed
}

type probed struct {
	caps capabilities.Capabilities
	err  error
	at   time.Time
}

// Probes is the process prober: one probe per registry host per process.
var Probes = &Prober{}

// Probe reports what the registry holding repo supports. subject is a tag
// or digest repo holds; pushed says the caller just wrote an OCI artifact
// manifest (with artifactType) to this host.
//
// A probe that cannot tell is an error, never a mode: a network error, a 5xx,
// a NAME_UNKNOWN or a 200 referrers answer that is not an OCI index. With an
// error the returned capabilities carry only CheckedAt, when the failed probe
// was tried (a remembered failure keeps its first attempt time).
//
// ponytail: no TTL; a registry that gains Referrers is seen after a restart.
// Add a TTL here if one ever flips in place.
func (p *Prober) Probe(ctx context.Context, repo *remote.Repository, subject string, pushed bool) (capabilities.Capabilities, error) {
	key := cacheKey(repo)
	p.mu.Lock()
	if e, ok := p.seen[key]; ok && (e.err == nil || p.now().Sub(e.at) < probeRetryAfter) {
		if e.err == nil && pushed {
			e.caps.ArtifactType = capabilities.ArtifactTypeAccepted
			p.seen[key] = e
		}
		p.mu.Unlock()
		return e.caps, e.err
	}
	p.mu.Unlock()

	at := p.now()
	caps, err := p.probe(ctx, repo, subject)
	switch {
	case err != nil:
		caps.CheckedAt = at
	case pushed:
		caps.ArtifactType = capabilities.ArtifactTypeAccepted
	}
	// A probe the caller cancelled says nothing about the registry.
	if ctx.Err() == nil {
		p.mu.Lock()
		if p.seen == nil {
			p.seen = map[string]probed{}
		}
		p.seen[key] = probed{caps: caps, err: err, at: at}
		p.mu.Unlock()
	}
	return caps, err
}

func (p *Prober) now() time.Time {
	if p.Now == nil {
		return time.Now()
	}
	return p.Now()
}

// cacheKey is one entry per host and scheme.
func cacheKey(repo *remote.Repository) string {
	return fmt.Sprintf("%s|%t", repo.Reference.Registry, repo.PlainHTTP)
}

func (p *Prober) probe(ctx context.Context, repo *remote.Repository, subject string) (capabilities.Capabilities, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	host := repo.Reference.Registry
	fail := func(err error) (capabilities.Capabilities, error) {
		return capabilities.Capabilities{}, fmt.Errorf("probing registry %s: %w", host, err)
	}
	client := clientOf(repo)
	desc, err := repo.Resolve(ctx, subject)
	if err != nil {
		return fail(err)
	}
	referrers, err := probeReferrers(ctx, client, repo, desc)
	if err != nil {
		return fail(err)
	}
	listing, err := probeCatalog(ctx, client, repo)
	if err != nil {
		return fail(err)
	}
	return capabilities.Capabilities{
		Host: host, Referrers: referrers, Listing: listing,
		ArtifactType: capabilities.ArtifactTypeUnknown,
		Auth:         probeAuth(ctx, client, repo), CheckedAt: p.now(),
	}, nil
}

// clientOf is the client every request of repo goes through.
func clientOf(repo *remote.Repository) remote.Client {
	if repo.Client == nil {
		return auth.DefaultClient
	}
	return repo.Client
}

// baseURL is the scheme and host every request of repo goes to.
func baseURL(repo *remote.Repository) string {
	scheme := "https"
	if repo.PlainHTTP {
		scheme = "http"
	}
	return scheme + "://" + repo.Reference.Host()
}

// probeReferrers asks the Referrers API about desc with its own GET: oras-go
// folds a 404 and a 200 of the wrong content type into one error. A 200 OCI
// index is native; a 404 without NAME_UNKNOWN is write-once-tags; anything
// else fails.
func probeReferrers(ctx context.Context, client remote.Client, repo *remote.Repository, desc ocispec.Descriptor) (capabilities.ReferrersMode, error) {
	url := fmt.Sprintf("%s/v2/%s/referrers/%s", baseURL(repo), repo.Reference.Repository, desc.Digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", ocispec.MediaTypeImageIndex)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		// Exact, as oras-go compares it: a probe never says native for an
		// answer the reader then refuses.
		if resp.Header.Get("Content-Type") == ocispec.MediaTypeImageIndex {
			return capabilities.ReferrersNative, nil
		}
		return "", fmt.Errorf("the referrers endpoint answered 200 with content type %q, not an OCI index", resp.Header.Get("Content-Type"))
	case http.StatusNotFound:
		var body struct{ Errors errcode.Errors }
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxProbeBody))
		_ = json.Unmarshal(raw, &body)
		if slices.ContainsFunc(body.Errors, func(e errcode.Error) bool { return e.Code == errcode.ErrorCodeNameUnknown }) {
			return "", fmt.Errorf("the referrers endpoint answered NAME_UNKNOWN for repository %s", repo.Reference.Repository)
		}
		return capabilities.ReferrersWriteOnceTags, nil
	}
	return "", fmt.Errorf("the referrers endpoint answered %s", resp.Status)
}

// errFirstPage stops a catalog listing after its first page.
var errFirstPage = errors.New("first page read")

// probeCatalog reads one _catalog page: success is catalog, an explicit
// refusal (401, 403, 404, 405) is tags, any other error fails.
func probeCatalog(ctx context.Context, client remote.Client, repo *remote.Repository) (capabilities.ListingMode, error) {
	reg := catalogRegistry(client, repo)
	reg.RepositoryListPageSize = 1
	err := reg.Repositories(ctx, "", func([]string) error { return errFirstPage })
	var resp *errcode.ErrorResponse
	switch {
	case errors.Is(err, errFirstPage):
		return capabilities.ListingCatalog, nil
	case errors.As(err, &resp) && slices.Contains([]int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed}, resp.StatusCode):
		return capabilities.ListingTags, nil
	}
	return "", err
}

// catalogRegistry is the registry holding repo, reached through client.
func catalogRegistry(client remote.Client, repo *remote.Repository) *remote.Registry {
	return &remote.Registry{
		RepositoryOptions: remote.RepositoryOptions{
			Client: client, PlainHTTP: repo.PlainHTTP,
			Reference: registry.Reference{Registry: repo.Reference.Registry},
		},
	}
}

// probeAuth reads the /v2/ challenge without credentials: anonymous, basic,
// bearer or unknown.
func probeAuth(ctx context.Context, client remote.Client, repo *remote.Repository) string {
	if a, ok := client.(*auth.Client); ok {
		client = a.Client
		if a.Client == nil {
			client = http.DefaultClient
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL(repo)+"/v2/", nil)
	if err != nil {
		return "unknown"
	}
	resp, err := client.Do(req)
	if err != nil {
		return "unknown"
	}
	_ = resp.Body.Close()
	challenge := strings.ToLower(resp.Header.Get("WWW-Authenticate"))
	switch {
	case resp.StatusCode == http.StatusOK:
		return "anonymous"
	case resp.StatusCode == http.StatusUnauthorized && strings.HasPrefix(challenge, "bearer"):
		return "bearer"
	case resp.StatusCode == http.StatusUnauthorized && strings.HasPrefix(challenge, "basic"):
		return "basic"
	}
	return "unknown"
}
