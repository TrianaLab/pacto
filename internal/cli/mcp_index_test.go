package cli

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// indexedPayments publishes payments 1.0.0 and 2.0.0 (a BREAKING change) and
// two consumers, web and shop, whose pushes write their organization index
// entries. It returns the env, the MCP server's service over the registry and
// a counter of index requests made after the setup.
func indexedPayments(t *testing.T) (*ciEnv, *app.Service, *atomic.Int32) {
	t.Helper()
	ciEnvOff(t)
	statsOff(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("PACTO_NO_UPDATE_CHECK", "1")
	var reqs atomic.Int32
	counting := atomic.Bool{}
	reg := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if counting.Load() && strings.Contains(r.URL.Path, "/pacto-index/") {
			reqs.Add(1)
		}
		reg.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	e := &ciEnv{root: t.TempDir(), host: strings.TrimPrefix(srv.URL, "http://")}
	t.Setenv("PACTO_INSECURE_REGISTRIES", e.host)
	e.write(t, map[string]string{
		"payments/pacto.yaml":   "service: {name: payments, version: 1.0.0}\ninterfaces:\n  - name: api\n    type: openapi\n    ref: openapi.yaml\n",
		"payments/openapi.yaml": "openapi: 3.0.0\ninfo: {title: payments, version: \"1\"}\npaths: {}\n",
		"web/pacto.yaml":        consumerYAML("web", e.host),
		"shop/pacto.yaml":       consumerYAML("shop", e.host),
	})
	e.publish(t, "payments", "1.0.0")
	e.publish(t, "web", "1.0.0")
	e.publish(t, "shop", "1.0.0")
	e.write(t, map[string]string{"payments/pacto.yaml": "service: {name: payments, version: 2.0.0}\n"})
	e.publish(t, "payments", "2.0.0")
	counting.Store(true)
	return e, app.NewService(oci.NewClient(authn.DefaultKeychain, oci.WithInsecureRegistries(e.host)), nil), &reqs
}

// mcpProvider is pacto_impact's provider of a pacto mcp --fleet server over
// transport, with no local bundles.
func mcpProvider(t *testing.T, svc *app.Service, transport string, addMissing, trusted bool) func(context.Context, *fleet.FleetSnapshot, pactomcp.ImpactRequest) (*impact.Result, error) {
	t.Helper()
	cmd := newMCPCommand(svc, "v", defaultDeps())
	cmd.SetContext(t.Context())
	for k, v := range map[string]string{"transport": transport, "fleet": "true", "local": "", "target-snapshot": "off"} {
		if err := cmd.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	return mcpImpactProvider(cmd, svc, defaultDeps(), addMissing, trusted)
}

func consumerNames(res *impact.Result) []string {
	var names []string
	for _, c := range res.Consumers {
		names = append(names, c.Service)
	}
	slices.Sort(names)
	return names
}

// TestMCPImpactStdioReadsIndex: over stdio pacto_impact finds the consumers
// the organization index lists, as pacto impact does.
func TestMCPImpactStdioReadsIndex(t *testing.T) {
	e, svc, reqs := indexedPayments(t)
	res, err := mcpProvider(t, svc, "stdio", true, true)(t.Context(), &fleet.FleetSnapshot{},
		pactomcp.ImpactRequest{OldRef: "oci://" + e.host + "/acme/payments:1.0.0", NewRef: "oci://" + e.host + "/acme/payments:2.0.0"})
	if err != nil || res.ConsumerIndex == nil || res.ConsumerIndex.Status != impact.IndexRead || res.ConsumerIndex.Consumers != 2 {
		t.Fatalf("res %+v, %v", res, err)
	}
	if got := consumerNames(res); !slices.Equal(got, []string{"shop", "web"}) || reqs.Load() == 0 {
		t.Fatalf("consumers %v, %d index requests", got, reqs.Load())
	}
}

// visibleSnapshot is a fleet view holding both payments revisions, pinned
// by digest at the registry.
func visibleSnapshot(t *testing.T, host string) *fleet.FleetSnapshot {
	t.Helper()
	client := oci.NewClient(authn.DefaultKeychain, oci.WithInsecureRegistries(host))
	snap := &fleet.FleetSnapshot{Revisions: map[fleet.RevisionKey]*fleet.ContractRevision{}}
	for _, v := range []string{"1.0.0", "2.0.0"} {
		ref := host + "/acme/payments:" + v
		digest, err := client.Resolve(t.Context(), ref)
		if err != nil {
			t.Fatal(err)
		}
		snap.Revisions[fleet.RevisionKey("payments@"+digest)] = &fleet.ContractRevision{Service: "payments", RequestedRef: "oci://" + ref,
			ResolvedRef: "oci://" + host + "/acme/payments@" + digest, Digest: digest}
	}
	return snap
}

// TestMCPImpactHTTPReadsNoIndex: an HTTP server without --auth-issuer adds
// the new revision (addMissing) and still never reads the index, since every
// caller would read it as this process. Like an authenticated server it says
// nothing of an index: no consumerIndex, so no flag the operator never passed.
func TestMCPImpactHTTPReadsNoIndex(t *testing.T) {
	e, svc, reqs := indexedPayments(t)
	snap := visibleSnapshot(t, e.host)
	reqs.Store(0)
	res, err := mcpProvider(t, svc, "http", true, false)(t.Context(), snap,
		pactomcp.ImpactRequest{OldRef: "oci://" + e.host + "/acme/payments:1.0.0", NewRef: "oci://" + e.host + "/acme/payments:2.0.0"})
	if err != nil || res.ConsumerIndex != nil || reqs.Load() != 0 {
		t.Fatalf("res %+v, %v, %d index requests", res, err, reqs.Load())
	}
}

// TestMCPImpactAuthenticatedHTTPReadsNoIndex: a server whose callers sign in
// adds nothing to the snapshot, so it reads no index and says nothing of one.
func TestMCPImpactAuthenticatedHTTPReadsNoIndex(t *testing.T) {
	e, svc, reqs := indexedPayments(t)
	snap := visibleSnapshot(t, e.host)
	reqs.Store(0)
	res, err := mcpProvider(t, svc, "http", false, false)(t.Context(), snap,
		pactomcp.ImpactRequest{OldRef: "oci://" + e.host + "/acme/payments:1.0.0", NewRef: "oci://" + e.host + "/acme/payments:2.0.0"})
	if err != nil || res.ConsumerIndex != nil || reqs.Load() != 0 {
		t.Fatalf("res %+v, %v, %d index requests", res, err, reqs.Load())
	}
}
