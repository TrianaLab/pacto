package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
)

// indexFixture is a registry whose organization index lists payments and
// web; web depends on payments and on stripe, whose inferred contract lies
// under the installation's own pacto-inferred with no index entry.
type indexFixture struct {
	*ciEnv
	svc *app.Service
	// now is the build clock each export pins.
	now time.Time
}

const ownedYAML = "service:\n  name: %s\n  version: 1.0.0\n  owner:\n    team: pay\n"

func newIndexFixture(t *testing.T) *indexFixture {
	t.Helper()
	return newIndexFixtureWith(t, true)
}

// newIndexFixtureWith is newIndexFixture, pushing stripe's inferred contract
// only when inferred is set, so web's ref to it dangles otherwise.
func newIndexFixtureWith(t *testing.T, inferred bool) *indexFixture {
	t.Helper()
	e := newCIEnv(t)
	f := &indexFixture{ciEnv: e, svc: app.NewService(oci.NewClient(authn.DefaultKeychain, oci.WithInsecureRegistries(e.host)), nil),
		now: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	e.write(t, map[string]string{
		"payments/pacto.yaml": strings.Replace(ownedYAML, "%s", "payments", 1),
		"web/pacto.yaml": strings.Replace(ownedYAML, "%s", "web", 1) + "dependencies:\n" +
			"  - ref: oci://" + e.host + "/acme/payments\n    compatibility: \"^1.0.0\"\n" +
			"  - ref: " + f.stripeRef() + "\n    compatibility: \"^1.0.0\"\n",
		"stripe/pacto.yaml": "service: {name: stripe, version: 1.0.0, external: stripe}\n",
	})
	e.publish(t, "payments", "1.0.0")
	e.publish(t, "web", "1.0.0")
	if inferred {
		f.pushInferred(t)
	}
	return f
}

// stripeRef is the inferred contract of stripe under the installation.
func (f *indexFixture) stripeRef() string {
	return "oci://" + f.host + "/acme/pacto-inferred/stripe:1.0.0"
}

// pushInferred pushes stripe's inferred contract, writing no index entry.
func (f *indexFixture) pushInferred(t *testing.T) {
	t.Helper()
	if _, err := f.svc.Push(t.Context(), app.PushOptions{Ref: f.stripeRef(), Path: filepath.Join(f.root, "stripe"), Index: app.IndexNone}); err != nil {
		t.Fatal(err)
	}
}

// index is the organization index the pushes wrote.
func (f *indexFixture) index() string { return "oci://" + f.host + "/acme/pacto-index" }

// forge writes an index entry for a service whose bundle reference is not
// a digest, so the read is partial.
func (f *indexFixture) forge(t *testing.T) {
	t.Helper()
	key := fleet.NewServiceKeyDomain("", "evil")
	tag, _ := orgindex.Tag(key, "1.0.0")
	payload, _ := orgindex.Encode(orgindex.Entry{SchemaVersion: orgindex.SchemaVersion, Service: string(key), Name: "evil", Version: "1.0.0", Bundle: "evil@sha256:bad"})
	repo, err := ocireferrer.Open(f.host+"/acme/pacto-index", ocireferrer.Options{Insecure: []string{f.host}})
	if err == nil {
		err = ocireferrer.PushWriteOnce(t.Context(), repo, ocireferrer.Build(orgindex.ArtifactType, orgindex.MediaType, payload, nil, nil), tag)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// execExportSvc runs pacto export backstage over svc with the build clock
// pinned at now.
func execExportSvc(t *testing.T, svc *app.Service, now time.Time, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("PACTO_NO_UPDATE_CHECK", "1")
	prev := fleetNow
	fleetNow = func() time.Time { return now }
	t.Cleanup(func() { fleetNow = prev })
	root := NewRootCommand(svc, VersionInfo{Version: "test"})
	root.SetArgs(append([]string{"export", "backstage"}, args...))
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	err := root.Execute()
	return out.String(), errb.String(), err
}

// export runs pacto export backstage over the fixture's service.
func (f *indexFixture) export(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return execExportSvc(t, f.svc, f.now, args...)
}

func TestExportBackstageIndexOnly(t *testing.T) {
	f := newIndexFixture(t)
	out, errOut, err := f.export(t, "--index", f.index())
	if err != nil {
		t.Fatalf("%v: %s", err, errOut)
	}
	for _, want := range []string{"name: payments", "name: web", "pacto.trianalab.io/owner-provenance: declared",
		"pacto.trianalab.io/inferred-contract: " + f.host + "/acme/pacto-inferred/stripe"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
}

func TestExportBackstageIndexOnlyNeedsNoDelivery(t *testing.T) {
	f := newIndexFixture(t)
	t.Setenv("PACTO_DELIVERY_TRUST", "")
	a, _, err := f.export(t, "--index", f.index())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PACTO_DELIVERY_TRUST", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "missing"))
	b, _, err := f.export(t, "--index", f.index())
	if err != nil || a != b {
		t.Errorf("err %v; output changed with delivery and Kubernetes configuration:\n%s\n---\n%s", err, a, b)
	}
}

func TestExportBackstagePartialIndexExits2(t *testing.T) {
	f := newIndexFixture(t)
	f.forge(t)
	path := filepath.Join(t.TempDir(), "entities.yaml")
	out, errOut, err := f.export(t, "--index", f.index(), "-o", path)
	if exportExitCode(err) != 2 || out != "" || !strings.Contains(errOut, "registry-index") || exists(path) {
		t.Errorf("err %v, stdout %q, stderr %q", err, out, errOut)
	}
}

func TestExportBackstageIndexMatchesDashboardRoute(t *testing.T) {
	f := newIndexFixture(t)
	cli, _, err := f.export(t, "--index", f.index())
	if err != nil {
		t.Fatal(err)
	}
	rec := f.route(t, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("route = %d: %s", rec.Code, rec.Body)
	}
	asOf := regexp.MustCompile(`(?m)^\s*pacto\.trianalab\.io/as-of: .*\n`)
	if c, r := asOf.ReplaceAllString(cli, ""), asOf.ReplaceAllString(rec.Body.String(), ""); c != r || !strings.Contains(c, "name: web") {
		t.Errorf("the CLI and the route differ:\n--- cli\n%s\n--- route\n%s", c, r)
	}
}

// exists reports whether path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestExportBackstageIndexWithTargetSnapshotHasInferredServices(t *testing.T) {
	f := newIndexFixture(t)
	repo, trust := publishedTargetsWith(t, map[string]any{"workloads": []any{map[string]any{"namespace": "shop", "kind": "Deployment", "name": "cart"}}})
	t.Setenv("PACTO_INSECURE_REGISTRIES", f.host+","+os.Getenv("PACTO_INSECURE_REGISTRIES"))
	f.now = time.Now().Add(time.Minute) // after the asOf the publisher stamps
	out, errOut, err := f.export(t, "--index", f.index(), "--target-snapshot", repo, "--delivery-trust", trust)
	if err != nil {
		t.Fatalf("%v: %s", err, errOut)
	}
	for _, want := range []string{"name: web", "pacto.trianalab.io/provenance: inferred", "pacto.trianalab.io/source: workload",
		"pacto.trianalab.io/workload: Deployment c-6f1e/shop/cart"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
}

// route serves GET /api/backstage/entities over the fixture's index, built
// through dashboardFleetOptions as the dashboard command builds it, narrowed
// by view when it is set.
func (f *indexFixture) route(t *testing.T, view func(context.Context, *fleet.Query) (*fleet.Query, error)) *httptest.ResponseRecorder {
	t.Helper()
	fopts := dashboardFleetOptions("", nil, nil, nil, dashboardSources{index: f.index()})
	srv := dashboard.NewServer(dashboard.Options{View: view, Fleet: func(ctx context.Context) (*fleet.Query, error) {
		snap, err := f.svc.Fleet(ctx, fopts)
		if err != nil {
			return nil, err
		}
		return fleet.NewQuery(snap), nil
	}})
	mux := http.NewServeMux()
	srv.RegisterRawRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/backstage/entities", nil))
	return rec
}
