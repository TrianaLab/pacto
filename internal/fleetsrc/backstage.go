package fleetsrc

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/backstage"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// ponytail: 500 per page, 10s and 16 MiB are fixed; add flags when a catalog
// needs others.
const (
	backstagePageSize     = 500
	maxBackstageBodyBytes = 16 << 20 // 16 MiB; a page cut at the limit fails to decode
	backstageFields       = "kind,metadata.name,metadata.namespace,metadata.annotations,spec.owner,spec.system,spec.dependsOn"
)

// inCluster reports whether the process runs in a Kubernetes pod; tests swap it.
var inCluster = inClusterEnv

// inClusterEnv reads KUBERNETES_SERVICE_HOST, which the kubelet sets in every pod.
func inClusterEnv() bool { return os.Getenv("KUBERNETES_SERVICE_HOST") != "" }

// CheckBackstageConfig holds every rule on a Backstage catalog URL and its
// reader token: the URL rules of [CheckSourceURL], and a token goes over plain
// http only to a loopback host, or to an in-cluster Service name from inside
// a cluster. A laptop that resolves a .svc name through its DNS search path
// never sends the token in clear text. The error never contains the token.
func CheckBackstageConfig(u, token string) error {
	if err := CheckSourceURL("backstage", u); err != nil {
		return err
	}
	p, _ := url.Parse(u) // CheckSourceURL parsed it
	host := p.Host
	if p.Port() == "" {
		host += ":80" // CheckEndpoint wants an explicit port; plain http defaults to 80
	}
	if token == "" || p.Scheme == "https" || loopback(p.Hostname()) || inCluster() && detect.CheckEndpoint("http://"+host) == nil {
		return nil
	}
	return fmt.Errorf("backstage source http://%s: a reader token travels only over https; use https or run inside the cluster", p.Hostname())
}

// loopback reports whether host is this machine.
func loopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || ip != nil && ip.IsLoopback()
}

// BackstageSource reads the Components of a Backstage catalog through its
// entities/by-query API and contributes their owners, systems and component
// dependencies as service facts. It is advisory: Collect never fails, and an
// unreachable, refusing or capped catalog is the source's own state, so it
// never makes an answer partial.
type BackstageSource struct {
	id, baseURL, token string
	// origin names the detected Service a tokenless source reads
	// ("detected: <svc>.<ns>"); empty for a configured URL.
	origin string
	client *http.Client
	now    func() time.Time
}

// NewBackstageSource returns a Backstage source over baseURL that sends token,
// when set, as a bearer token. The client refuses redirects and times out
// after 10s. A URL or token [CheckBackstageConfig] refuses is an error.
func NewBackstageSource(id, baseURL, token string, now func() time.Time) (*BackstageSource, error) {
	if err := CheckBackstageConfig(baseURL, token); err != nil {
		return nil, err
	}
	return &BackstageSource{id: id, baseURL: strings.TrimRight(baseURL, "/"), token: token, now: now, client: &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// NewDetectedBackstageSource returns a Backstage source over a detected
// in-cluster endpoint. It takes no token: the reader token goes only to a URL
// an operator configured, never to a Service anyone could have created. Its
// state names origin, and its client skips any egress proxy.
func NewDetectedBackstageSource(id, endpoint, origin string, now func() time.Time) (*BackstageSource, error) {
	s, err := NewBackstageSource(id, endpoint, "", now)
	if err != nil {
		return nil, err
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	s.origin, s.client.Transport = origin, t
	return s, nil
}

// ID implements [fleet.Source].
func (s *BackstageSource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *BackstageSource) Kind() string { return backstage.SourceKind }

// Collect pages through the catalog's Components, at most
// [backstage.MaxCatalogEntities] of them. It never returns an error: a failed
// read is an unavailable state with a sanitized reason, a capped one partial.
func (s *BackstageSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	if t, ok := s.client.Transport.(*http.Transport); ok {
		// Each refresh builds a new detected source: its connections must not outlive it.
		defer t.CloseIdleConnections()
	}
	at := s.now()
	state := fleet.SourceState{Status: fleet.SourceAvailable, ObservedAt: &at, LastSuccessfulSync: &at, Ref: s.origin}
	col := &fleet.Collection{Advisory: true, State: &state}
	facts, capped, err := s.read(ctx, at)
	switch {
	case err != nil:
		if err.Code == codeAuthFailed && s.origin != "" {
			err.Message = "the catalog refused the request; set dashboard.backstage.url and tokenSecret to read this catalog"
		}
		state = fleet.SourceState{Status: fleet.SourceUnavailable, ObservedAt: &at, Error: err, Ref: s.origin}
	case capped:
		state.Status = fleet.SourcePartial
		col.Limitations = []fleet.Limitation{{Code: fleet.LimitationSourcePartial, Source: s.id,
			Message: fmt.Sprintf("the read stopped at %d components", backstage.MaxCatalogEntities)}}
		fallthrough
	default:
		col.ServiceFacts = facts
	}
	return col, nil
}

// read pages until the last page or the cap, which it reports.
func (s *BackstageSource) read(ctx context.Context, at time.Time) (facts []fleet.RawServiceFact, capped bool, err *fleet.SourceError) {
	cursor := ""
	for {
		page, next, err := s.page(ctx, cursor, at)
		if err != nil {
			return nil, false, err
		}
		facts = append(facts, page...)
		if len(facts) >= backstage.MaxCatalogEntities {
			return facts[:backstage.MaxCatalogEntities], next != "" || len(facts) > backstage.MaxCatalogEntities, nil
		}
		if next == "" {
			return facts, false, nil
		}
		cursor = next
	}
}

// page reads one by-query page: the first with the filter, a later one by
// its cursor, which carries the filter.
func (s *BackstageSource) page(ctx context.Context, cursor string, at time.Time) ([]fleet.RawServiceFact, string, *fleet.SourceError) {
	q := url.Values{"fields": {backstageFields}, "limit": {fmt.Sprint(backstagePageSize)}}
	if cursor == "" {
		q.Set("filter", "kind=component")
	} else {
		q.Set("cursor", cursor)
	}
	// baseURL passed CheckSourceURL, so the request always builds.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/api/catalog/entities/by-query?"+q.Encode(), nil)
	req.Header.Set("Accept", "application/json")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", &fleet.SourceError{Code: "UNREACHABLE", Message: "the catalog could not be reached"}
	}
	defer func() { _ = resp.Body.Close() }()
	if serr := backstageStatus(resp.StatusCode); serr != nil {
		return nil, "", serr
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBackstageBodyBytes))
	if err != nil {
		return nil, "", &fleet.SourceError{Code: "UNREACHABLE", Message: "the catalog answer could not be read"}
	}
	facts, next, err := backstage.ReadCatalogPage(data, at)
	if err != nil {
		return nil, "", &fleet.SourceError{Code: "INVALID", Message: "the catalog answer is not an entities/by-query page"}
	}
	return facts, next, nil
}

// codeAuthFailed is the error code of a catalog that refused the request.
const codeAuthFailed = "AUTH_FAILED"

// backstageStatus is why a status code is no page, nil for a 2xx. A refused
// redirect surfaces here as its 3xx.
func backstageStatus(code int) *fleet.SourceError {
	switch {
	case code >= 200 && code <= 299:
		return nil
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return &fleet.SourceError{Code: codeAuthFailed, Message: "the catalog refused the request; set a reader token"}
	case code == http.StatusNotFound:
		return &fleet.SourceError{Code: "NOT_FOUND", Message: "the catalog has no entities/by-query API (Backstage 1.12 or later)"}
	}
	return &fleet.SourceError{Code: "UNAVAILABLE", Message: fmt.Sprintf("the catalog answered %d", code)}
}
