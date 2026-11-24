package fleet

import (
	"slices"
	"strings"
	"testing"
)

var createPin = "oci://ghcr.io/acme/http-service@sha256:" + strings.Repeat("a", 64)

// creatableFleet is the pluginPath golden path (requesting postgresql and
// orders, which offers nothing) resolved to ref, with gift-cards, postgresql
// and orders.
func creatableFleet(t *testing.T, requested, resolved string) *Query {
	t.Helper()
	path := ociRev(acme, pluginPath(), pathFiles)
	path.RequestedRef, path.ResolvedRef = requested, resolved
	path.Provenance = &BundleProvenance{Signature: BundleSignature{Status: SignatureVerified, Signer: "platform@acme.dev"}}
	return buildDomainSnap(t, "oci", &Collection{Revisions: []RawRevision{
		path, createdFrom(pathContentHash(t)), postgres(), ociRev(acme, usesPostgres("orders", "1.0.0"), nil),
	}})
}

var pathKey = NewServiceKeyDomain(acme, "http-service")

func TestCreatablePath(t *testing.T) {
	p, why := creatableFleet(t, "oci://ghcr.io/acme/http-service:1.0.0", createPin).CreatablePath(pathKey)
	if why != "" || p.Key != pathKey || p.Name != "http-service" || p.Version != "1.0.0" || p.Ref != createPin {
		t.Fatalf("path = %+v, reason %q", p, why)
	}
	if p.Signature == nil || p.Signature.Status != SignatureVerified || p.Signature.Signer != "platform@acme.dev" {
		t.Errorf("signature = %+v", p.Signature)
	}
	if !slices.Equal(p.Requests, []string{"postgresql"}) {
		t.Errorf("requests = %v", p.Requests)
	}
}

func TestCreatablePathSignatureUnknownWithoutProvenance(t *testing.T) {
	q := creatableFleet(t, "oci://ghcr.io/acme/http-service:1.0.0", createPin)
	q.snap.Revisions[q.snap.Services[pathKey].Revisions[0]].Provenance = nil
	p, _ := q.CreatablePath(pathKey)
	if p.Signature == nil || p.Signature.Status != SignatureUnknown {
		t.Errorf("an unchecked signature is unknown: %+v", p.Signature)
	}
}

func TestCreatablePathRefusesUnpinned(t *testing.T) {
	for _, tt := range []struct{ name, requested, resolved string }{
		{"tag", "oci://ghcr.io/acme/http-service:1.0.0", ""},
		{"local directory", "file:///srv/paths/http-service", ""},
		{"resolved tag", "oci://ghcr.io/acme/http-service:1.0.0", "oci://ghcr.io/acme/http-service:1.0.0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, why := creatableFleet(t, tt.requested, tt.resolved).CreatablePath(pathKey)
			if p.Ref != "" || !strings.Contains(why, "no digest-pinned reference") {
				t.Errorf("path = %+v, reason %q", p, why)
			}
		})
	}
}

func TestCreatablePathRefusesUnreadable(t *testing.T) {
	q := creatableFleet(t, "oci://ghcr.io/acme/http-service:1.0.0", createPin)
	q.snap.Services[pathKey].Revisions = nil
	if _, why := q.CreatablePath(pathKey); why != unpinnedCreatablePath {
		t.Errorf("reason %q", why)
	}
}

func TestCreatablePathRefusesInferred(t *testing.T) {
	q := creatableFleet(t, "oci://ghcr.io/acme/http-service:1.0.0", createPin)
	q.snap.Services[pathKey].Declaration = DeclarationInferred
	if p, why := q.CreatablePath(pathKey); p.Ref != "" || why != "an inferred golden path is accepted first" {
		t.Errorf("path = %+v, reason %q", p, why)
	}
}

func TestCreatablePathRefusesService(t *testing.T) {
	q := creatableFleet(t, "oci://ghcr.io/acme/http-service:1.0.0", createPin)
	_, missing := q.CreatablePath(NewServiceKeyDomain(acme, "nope"))
	if missing != "no golden path with this key" {
		t.Fatalf("missing reason %q", missing)
	}
	if p, why := q.CreatablePath(NewServiceKeyDomain(acme, "orders")); p.Ref != "" || why != missing {
		t.Errorf("an ordinary service: %+v, %q", p, why)
	}
}

func TestCreatablePathInferredTemplateKeyReadsMissing(t *testing.T) {
	q := tmplFleet(t, tmplCatalog(tmpl("springboot-service")))
	if l := q.InferredTemplates(""); l == nil || len(l.Items) != 1 {
		t.Fatalf("inferred templates = %+v", l)
	}
	_, missing := q.CreatablePath(NewServiceKeyDomain(acme, "nope"))
	for _, k := range []ServiceKey{NewServiceKeyDomain(acme, "springboot-service"), ServiceKey("springboot-service"), ServiceKey("template:default/springboot-service")} {
		if p, why := q.CreatablePath(k); p.Ref != "" || why != missing {
			t.Errorf("%s: %+v, %q", k, p, why)
		}
	}
}

func TestCreatablePathHiddenReadsMissing(t *testing.T) {
	q := hiddenPathFleet(t)
	for _, rk := range q.snap.Services[pathKey].Revisions {
		q.snap.Revisions[rk].ResolvedRef = createPin
	}
	if _, why := q.CreatablePath(pathKey); why != "" {
		t.Fatalf("unrestricted reason %q", why)
	}
	r := q.Restrict(anyCluster(shopOnly), anyClusterKind(nil))
	_, missing := r.CreatablePath(NewServiceKeyDomain(acme, "nope"))
	if p, why := r.CreatablePath(pathKey); p.Ref != "" || why != missing {
		t.Errorf("hidden path: %+v, %q (missing %q)", p, why, missing)
	}
}
