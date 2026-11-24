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
	maxBackstagePages     = backstage.MaxCatalogEntities / backstagePageSize // 10; a catalog whose pages bring no Components still ends
	maxBackstageBodyBytes = 16 << 20                                         // 16 MiB; a larger page is an error that names the bound
	backstageFields       = "kind,metadata.name,metadata.namespace,metadata.annotations,spec.owner,spec.system,spec.dependsOn,apiVersion,metadata.title,metadata.description,spec.type"
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
	in := inCluster()
	if token == "" || p.Scheme == "https" || loopback(p.Hostname()) || in && detect.CheckEndpoint("http://"+host) == nil {
		return nil
	}
	if in {
		return fmt.Errorf("backstage source http://%s: a reader token travels only over https; use https or the in-cluster Service name http://<service>.<namespace>.svc:<port>", p.Hostname())
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
	// origin marks a tokenless source over a detected Service ("detected");
	// empty for a configured URL. It never names the Service: a restricted
	// view redacts the detection row that does, never a source's Ref.
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
// [backstage.MaxCatalogEntities] of them, and its scaffolder Templates. It
// never returns an error: a failed read is an unavailable state with a
// sanitized reason, a capped one partial. Every collection says it lists
// templates, so a failed read never reads as "no templates".
func (s *BackstageSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	if t, ok := s.client.Transport.(*http.Transport); ok {
		// Each refresh builds a new detected source: its connections must not outlive it.
		defer t.CloseIdleConnections()
	}
	at := s.now()
	state := fleet.SourceState{Status: fleet.SourceAvailable, ObservedAt: &at, LastSuccessfulSync: &at, Ref: s.origin}
	col := &fleet.Collection{Advisory: true, State: &state, ListsTemplates: true}
	read, capped, err := s.read(ctx, at)
	trimmed := read.Trimmed
	switch {
	case err != nil:
		if err.Code == codeAuthFailed && s.origin != "" {
			err.Message = "the catalog refused the request; set dashboard.backstage.url and tokenSecret to read this catalog"
		}
		state = fleet.SourceState{Status: fleet.SourceUnavailable, ObservedAt: &at, Error: err, Ref: s.origin}
	case capped || trimmed > 0:
		state.Status = fleet.SourcePartial
		// Trimmed dependencies leave out no template; a cap may.
		col.TemplatesComplete = !capped
		if capped {
			col.Limitations = append(col.Limitations, fleet.Limitation{Code: fleet.LimitationSourcePartial, Source: s.id,
				Message: fmt.Sprintf("the read stopped at %d components or %d pages", backstage.MaxCatalogEntities, maxBackstagePages)})
		}
		if trimmed > 0 {
			col.Limitations = append(col.Limitations, fleet.Limitation{Code: fleet.LimitationSourcePartial, Source: s.id,
				Message: fmt.Sprintf("%d components list more than %d component dependencies; the rest were left out", trimmed, backstage.MaxFactDependsOn)})
		}
		fallthrough
	default:
		col.ServiceFacts, col.InferredTemplates = read.Facts, s.templates(read.Templates)
	}
	return col, nil
}

// templates fills where each template was read and, for a configured
// catalog only, its scaffolder link and that a person may accept it: a
// detected endpoint is an in-cluster Service a browser cannot open and
// anyone could have created.
func (s *BackstageSource) templates(ts []fleet.RawInferredTemplate) []fleet.RawInferredTemplate {
	from := "Backstage (in-cluster)"
	if s.origin == "" {
		u, _ := url.Parse(s.baseURL) // CheckSourceURL parsed it
		from = "Backstage (" + u.Host + ")"
	}
	for i := range ts {
		ts[i].From = from
		if s.origin == "" {
			ns, name, _ := strings.Cut(strings.TrimPrefix(ts[i].Entity, backstage.TemplateEntityPrefix), "/")
			ts[i].URL, ts[i].Acceptable = backstage.ScaffolderURL(s.baseURL, ns, name), true
		}
	}
	return ts
}

// read pages until the last page, the component cap or the page cap, which
// it reports, keeping the templates of every page read and counting the
// components whose dependencies were trimmed.
func (s *BackstageSource) read(ctx context.Context, at time.Time) (backstage.CatalogRead, bool, *fleet.SourceError) {
	var out backstage.CatalogRead
	cursor := ""
	for range maxBackstagePages {
		page, err := s.page(ctx, cursor, at)
		if err != nil {
			return backstage.CatalogRead{}, false, err
		}
		out.Facts, out.Templates, out.Trimmed = append(out.Facts, page.Facts...), append(out.Templates, page.Templates...), out.Trimmed+page.Trimmed
		if len(out.Facts) >= backstage.MaxCatalogEntities {
			capped := page.Next != "" || len(out.Facts) > backstage.MaxCatalogEntities
			out.Facts = out.Facts[:backstage.MaxCatalogEntities]
			return out, capped, nil
		}
		if page.Next == "" {
			return out, false, nil
		}
		cursor = page.Next
	}
	// A catalog still paging after maxBackstagePages pages (empty pages, a
	// repeating cursor) is capped, never read forever.
	return out, true, nil
}

// page reads one by-query page: the first with the filter for Components and
// Templates (the catalog ORs repeated filters), a later one by its cursor,
// which carries the filter.
func (s *BackstageSource) page(ctx context.Context, cursor string, at time.Time) (backstage.CatalogRead, *fleet.SourceError) {
	q := url.Values{"fields": {backstageFields}, "limit": {fmt.Sprint(backstagePageSize)}}
	if cursor == "" {
		q["filter"] = []string{"kind=component", "kind=template"}
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
		return backstage.CatalogRead{}, &fleet.SourceError{Code: "UNREACHABLE", Message: "the catalog could not be reached"}
	}
	defer func() { _ = resp.Body.Close() }()
	if serr := backstageStatus(resp.StatusCode); serr != nil {
		return backstage.CatalogRead{}, serr
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBackstageBodyBytes+1))
	if err != nil {
		return backstage.CatalogRead{}, &fleet.SourceError{Code: "UNREACHABLE", Message: "the catalog answer could not be read"}
	}
	if len(data) > maxBackstageBodyBytes {
		return backstage.CatalogRead{}, &fleet.SourceError{Code: "TOO_LARGE", Message: fmt.Sprintf("a catalog page is larger than %d MiB", maxBackstageBodyBytes>>20)}
	}
	read, err := backstage.ReadCatalogPage(data, at)
	if err != nil {
		return backstage.CatalogRead{}, &fleet.SourceError{Code: "INVALID", Message: "the catalog answer is not an entities/by-query page"}
	}
	return read, nil
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
