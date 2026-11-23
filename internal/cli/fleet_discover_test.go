package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// discoverFixture is a registry serving catalogStatus on _catalog (0
// serves it) whose acme prefix holds two golden paths, an offering and a
// service, every push writing its organization index entry.
func discoverFixture(t *testing.T, catalogStatus int) (*ciEnv, *app.Service) {
	t.Helper()
	ciEnvOff(t)
	statsOff(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("PACTO_NO_UPDATE_CHECK", "1")
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{CatalogStatus: catalogStatus}))
	t.Cleanup(srv.Close)
	e := &ciEnv{root: t.TempDir(), host: strings.TrimPrefix(srv.URL, "http://")}
	t.Setenv("PACTO_INSECURE_REGISTRIES", e.host)
	tpl := func(name, version string) string {
		return "pactoVersion: \"2.4\"\nservice:\n  name: " + name + "\n  version: " + version +
			"\n  owner: {team: paths}\n  template:\n    plugins:\n      - name: dockerfile\n"
	}
	e.write(t, map[string]string{
		"go-http-postgres/pacto.yaml": tpl("go-http-postgres", "1.2.0"),
		"node-worker/pacto.yaml":      tpl("node-worker", "0.4.0"),
		"postgresql/pacto.yaml":       "pactoVersion: \"2.4\"\nservice:\n  name: postgresql\n  version: 16.1.0\n  owner: {team: data}\n  offering: postgresql\n",
		"billing/pacto.yaml":          "pactoVersion: \"2.4\"\nservice: {name: billing, version: 1.0.0}\n",
	})
	for rel, tag := range map[string]string{"go-http-postgres": "1.2.0", "node-worker": "0.4.0", "postgresql": "16.1.0", "billing": "1.0.0"} {
		e.publish(t, rel, tag)
	}
	return e, app.NewService(oci.NewClient(authn.DefaultKeychain, oci.WithInsecureRegistries(e.host)), nil)
}

// discoverPrefix is the prefix every fixture bundle lies under.
func (e *ciEnv) discoverPrefix() string { return e.host + "/acme" }

// execDiscover runs the pacto command args over svc.
func execDiscover(t *testing.T, svc *app.Service, args ...string) (string, error) {
	t.Helper()
	root := NewRootCommand(svc, VersionInfo{Version: "test"})
	root.SetArgs(args)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.Execute()
	return out.String(), err
}

// TestFleetEntitiesTemplateFromDiscoveryPartial: on a registry that serves
// no _catalog, --discover lists both golden paths from the index, an unsigned
// one reads unsigned and the answer is partial with the INDEX_ONLY
// reason; the service and the offering are no golden path.
func TestFleetEntitiesTemplateFromDiscoveryPartial(t *testing.T) {
	e, svc := discoverFixture(t, http.StatusNotFound)
	out, err := execDiscover(t, svc, "fleet", "entities", "--local", "", "--template", "--discover", e.discoverPrefix())
	if err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	for _, want := range []string{" go-http-postgres ", " node-worker ", "2 of 2 entit(ies):", "partial",
		e.host + " serves no _catalog; listed from " + e.discoverPrefix() + "/pacto-index"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	for _, deny := range []string{"billing", "postgresql"} {
		if strings.Contains(out, deny) {
			t.Errorf("%q listed as a golden path:\n%s", deny, out)
		}
	}
	out, err = execDiscover(t, svc, "fleet", "entity", "service", "node-worker", "--local", "", "--discover", e.discoverPrefix())
	if err != nil || !strings.Contains(out, "Signature: unsigned: ") {
		t.Errorf("node-worker signature = %v:\n%s", err, out)
	}
}

// TestDiscoverFlagRefusesTagAndDigest: a tag or digest on --discover fails
// the command with CheckDiscover's message.
func TestDiscoverFlagRefusesTagAndDigest(t *testing.T) {
	t.Setenv("PACTO_NO_UPDATE_CHECK", "1")
	for _, ref := range []string{"ghcr.io/acme/x:1.0.0", "ghcr.io/acme/x@sha256:" + strings.Repeat("a", 64)} {
		_, err := execDiscover(t, app.NewService(nil, nil), "fleet", "search", "--local", filepath.Join(t.TempDir(), "none"), "--discover", ref)
		if err == nil || !strings.Contains(err.Error(), "--discover takes <registry>[/<path>] with no tag or digest") {
			t.Errorf("--discover %s: %v", ref, err)
		}
	}
}
