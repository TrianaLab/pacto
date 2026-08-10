package dashboard

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// docFleetQuery builds two domains that each contain a "payment-service" with a
// docs/overview.md of its own, plus a "checkout" whose configuration and policy
// reference payment-service. It exercises the document read and the reference
// resolution over the real HTTP transport, including the same-name/two-domain
// case the product API must keep apart.
func docFleetQuery(t *testing.T, overviewA, overviewB string) *fleet.Query {
	t.Helper()
	payment := func(domain, body, fill string) fleet.RawRevision {
		b := newPaymentBundle()
		b.FS = fstest.MapFS{
			"docs/overview.md":  {Data: []byte(body)},
			"docs/oversized.md": {Data: []byte(strings.Repeat("x", fleet.MaxDocumentBytes+1))},
		}
		return fleet.RawRevision{Bundle: b, Domain: domain, Digest: validDigest(fill)}
	}
	checkout := func(domain, fill, target string) fleet.RawRevision {
		// The refs are digest-pinned: a scope named after a service is a label, not
		// evidence, so only the content address makes the destination navigable.
		ref := "oci://" + domain + "/payment-service@" + validDigest(target)
		return fleet.RawRevision{
			Bundle: &contract.Bundle{Contract: &contract.Contract{
				PactoVersion:   "2.0",
				Service:        contract.Service{Name: "checkout", Version: "1.0.0"},
				Configurations: []contract.Configuration{{Name: "payment-service", Ref: ref}},
				Policies:       []contract.Policy{{Name: "payment-service", Ref: ref, Target: "spend"}},
			}, FS: fstest.MapFS{}},
			Domain: domain, Digest: validDigest(fill),
		}
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("s", "local", &fleet.Collection{Revisions: []fleet.RawRevision{
			payment("domain-a", overviewA, "a"), payment("domain-b", overviewB, "b"),
			checkout("domain-a", "c", "a"), checkout("domain-b", "d", "b"),
		}}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func revKeyFor(t *testing.T, q *fleet.Query, domain, name string) string {
	t.Helper()
	v, err := q.GetService(domain + "/" + name)
	if err != nil {
		t.Fatalf("GetService %s/%s: %v", domain, name, err)
	}
	return string(v.Revisions[0].Key)
}

func docURL(base, key, path string) string {
	return base + "/api/fleet/revisions/document?key=" + url.QueryEscape(key) + "&path=" + url.QueryEscape(path)
}

func TestRevisionDocumentEndpoint_ServesTheExactRevisionsDocument(t *testing.T) {
	q := docFleetQuery(t, "# Domain A\n", "# Domain B\n")
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()

	for _, tc := range []struct{ domain, want string }{{"domain-a", "# Domain A\n"}, {"domain-b", "# Domain B\n"}} {
		key := revKeyFor(t, q, tc.domain, "payment-service")
		var doc ProductRevisionDocument
		getJSON(t, docURL(base, key, "docs/overview.md"), http.StatusOK, &doc)
		if doc.Document.Content != tc.want {
			t.Errorf("%s: content = %q, want %q", tc.domain, doc.Document.Content, tc.want)
		}
		// The revision reference is navigable, so a doc viewer can link home.
		if doc.Revision.Href == "" || doc.Revision.Key != key {
			t.Errorf("%s: revision ref = %+v", tc.domain, doc.Revision)
		}
		if doc.Meta.SchemaVersion != fleet.ProductSchemaVersion {
			t.Errorf("%s: missing product envelope", tc.domain)
		}
	}
}

func TestRevisionDocumentEndpoint_RejectsUnknownAndUnavailable(t *testing.T) {
	q := docFleetQuery(t, "# A\n", "# B\n")
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()
	key := revKeyFor(t, q, "domain-a", "payment-service")

	expectStatus(t, docURL(base, "no-such-revision", "docs/overview.md"), http.StatusNotFound)
	expectStatus(t, docURL(base, key, "../../etc/passwd"), http.StatusNotFound)
	expectStatus(t, docURL(base, key, "docs/does-not-exist.md"), http.StatusNotFound)
	// Both params are required, so a missing one is a validation failure rather
	// than a read of some default document.
	expectStatus(t, base+"/api/fleet/revisions/document?key="+url.QueryEscape(key), http.StatusUnprocessableEntity)
	// An oversized document is an explicit failure carrying its reason.
	expectStatus(t, docURL(base, key, "docs/oversized.md"), http.StatusUnprocessableEntity)
}

func TestRevisionDocumentEndpoint_NeedsTheFleetProvider(t *testing.T) {
	base, cancel := startFleetTestServer(t, nil, nil)
	defer cancel()
	expectStatus(t, docURL(base, "k", "docs/overview.md"), http.StatusNotFound)
}

// The revision page must be able to LINK a contract reference, so the resolved
// destination arrives with a canonical href and the raw ref stays alongside it.
func TestProductRevisionDetail_ContractReferencesAreNavigable(t *testing.T) {
	q := docFleetQuery(t, "# A\n", "# B\n")
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()

	for _, domain := range []string{"domain-a", "domain-b"} {
		var det ProductEntityDetail
		getJSON(t, base+"/api/fleet/entities/revision?key="+url.QueryEscape(revKeyFor(t, q, domain, "checkout")), http.StatusOK, &det)
		if det.Revision == nil {
			t.Fatalf("%s: no revision payload", domain)
		}
		wantHref := "/fleet/services/" + url.PathEscape(domain+"/payment-service")
		for _, got := range []struct {
			what       string
			ref        string
			resolution *ProductRefResolution
		}{
			{"configuration", det.Revision.Configurations.Items[0].Ref, det.Revision.Configurations.Items[0].Resolution},
			{"policy", det.Revision.Policies.Items[0].Ref, det.Revision.Policies.Items[0].Resolution},
		} {
			if !strings.Contains(got.ref, domain) {
				t.Errorf("%s %s: the authored ref must survive, got %q", domain, got.what, got.ref)
			}
			if got.resolution == nil || got.resolution.Service == nil {
				t.Fatalf("%s %s: want a resolved destination, got %+v", domain, got.what, got.resolution)
			}
			if got.resolution.Service.Href != wantHref {
				t.Errorf("%s %s: href = %q, want %q", domain, got.what, got.resolution.Service.Href, wantHref)
			}
		}
	}

	// And the referenced service lists its referrers back, navigably.
	var svc ProductEntityDetail
	getJSON(t, base+"/api/fleet/entities/service?key="+url.QueryEscape("domain-a/payment-service"), http.StatusOK, &svc)
	rb := svc.Service.ReferencedBy
	if len(rb.Items) != 1 || rb.Items[0].Key != "domain-a/checkout" || rb.Items[0].Href == "" {
		t.Errorf("referencedBy = %+v", rb.Items)
	}
}

// A reference with no resolution at all must transport as no resolution, not as
// an empty object that reads like a failed lookup.
func TestProductRefResolution_NilStaysNil(t *testing.T) {
	if got := productRefResolution(nil); got != nil {
		t.Errorf("want nil, got %+v", got)
	}
}
