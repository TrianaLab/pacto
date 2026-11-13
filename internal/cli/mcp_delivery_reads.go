package cli

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

type deliveryAPIKey struct{}

// mcpDeliveryReads runs the dashboard's delivery read routes in process, so
// an agent reads exactly what a person reads on the dashboard. One dashboard
// server per process answers from the fleet the fleet tools answer from,
// narrowed by the same view, and lists delivery in namespace (the fleet
// --namespace, empty for cluster-wide) as the dashboard does. The returned
// function binds one caller: act is their delivery client and who keys their
// cached Stage namespaces.
func mcpDeliveryReads(id *dashboardIdentity, fm *pactomcp.FleetMode, namespace string) func(act *app.Delivery, who string) pactomcp.DeliveryReads {
	opts := dashboard.Options{DeliveryNamespace: namespace, Delivery: func(r *http.Request) (dashboard.DeliveryAPI, error) {
		return r.Context().Value(deliveryAPIKey{}).(dashboard.DeliveryAPI), nil
	}}
	if id.cluster != nil {
		opts.DeliveryInstalled = id.deliveryInstalled
	}
	if fm != nil {
		opts.Fleet = fm.Src
		if v := fm.Opts.View; v != nil {
			opts.View = func(ctx context.Context, q *fleet.Query) (*fleet.Query, error) {
				return v(ctx, auth.TokenInfoFromContext(ctx), q)
			}
		}
	}
	h := dashboard.NewServer(opts).APIHandler()
	return func(act *app.Delivery, who string) pactomcp.DeliveryReads {
		api := newDashDelivery(id, act, who)
		return func(ctx context.Context, route string, q url.Values) (json.RawMessage, error) {
			if err := checkView(ctx, opts); err != nil {
				return nil, err
			}
			r, _ := http.NewRequestWithContext(context.WithValue(ctx, deliveryAPIKey{}, dashboard.DeliveryAPI(api)), http.MethodGet,
				"/api/delivery/"+route+"?"+q.Encode(), nil) // a constant method and a relative URL never fail
			rec := &bodyRecorder{header: http.Header{}, code: http.StatusOK}
			h.ServeHTTP(rec, r)
			if rec.code >= 300 {
				return nil, routeError(rec)
			}
			return rec.Bytes(), nil
		}
	}
}

// checkView fails a read whose caller the view cannot narrow for: the
// dashboard routes would read a failed view as no snapshot and show inbox
// items of services whose owner it then cannot tell.
func checkView(ctx context.Context, opts dashboard.Options) error {
	if opts.View == nil {
		return nil
	}
	q, err := opts.Fleet(ctx)
	if err == nil {
		_, err = opts.View(ctx, q)
	}
	return err
}

// routeError is the refusal a route answered with, as the dashboard's
// DeliveryError. A validation failure keeps each field it names, as the
// dashboard's client sees them.
func routeError(rec *bodyRecorder) error {
	var b struct {
		Code   string `json:"code"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Errors []struct {
			Location string `json:"location"`
			Message  string `json:"message"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(rec.Bytes(), &b) // a body that is not JSON still has a status
	detail := b.Detail
	for _, e := range b.Errors {
		detail += "; " + e.Location + ": " + e.Message
	}
	return &dashboard.DeliveryError{Status: rec.code, Code: cmp.Or(b.Code, b.Title, http.StatusText(rec.code)), Detail: detail}
}

// bodyRecorder keeps an in-process route's status and body.
type bodyRecorder struct {
	bytes.Buffer
	header http.Header
	code   int
}

func (r *bodyRecorder) Header() http.Header { return r.header }

func (r *bodyRecorder) WriteHeader(code int) { r.code = code }

// remoteCallerKey keys a reviewed caller's cached answers: stable for one
// identity and never empty, so two callers never share an entry. The JSON
// encoding keeps a user with a newline from colliding with another user's
// groups.
func remoteCallerKey(p authn.Person) string {
	b, _ := json.Marshal([]any{p.User, slices.Sorted(slices.Values(p.Groups))}) // strings always marshal
	sum := sha256.Sum256(b)
	return string(sum[:])
}
