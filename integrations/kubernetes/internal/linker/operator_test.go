/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package linker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
)

func TestForOperatorNilWithoutDeployment(t *testing.T) {
	if l, err := ForOperator(Config{Namespace: opNS}); l != nil || err != nil {
		t.Fatalf("a linker outside the chart: %v %v", l, err)
	}
	if _, err := ForOperator(Config{DeploymentName: opDeploy, Registry: "Not A Registry!"}); err == nil {
		t.Fatal("an unusable registry must fail at start")
	}
}

// pushEntry writes an index entry for payments 2.3.0, bundled under registry,
// and returns its tag; a large annotation makes the manifest over the bound.
func pushEntry(t *testing.T, reg string, annotation string) string {
	t.Helper()
	ctx := context.Background()
	repo, err := ocireferrer.Open(oci.InstallationRepository(reg, oci.IndexRepository), ocireferrer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	key := fleet.NewServiceKeyDomain(contract.OCIDomain(reg+"/payments"), "payments")
	e := orgindex.Entry{SchemaVersion: orgindex.SchemaVersion, Service: string(key), Name: "payments", Version: "2.3.0",
		Bundle: reg + "/payments@" + dA}
	tag, _ := orgindex.Tag(key, e.Version)
	if annotation != "" {
		tag, _ = orgindex.Tag(key, "2.4.0")
	}
	payload, _ := orgindex.Encode(e)
	art := ocireferrer.Build(orgindex.ArtifactType, orgindex.MediaType, payload, nil, map[string]string{"x": annotation})
	if err := ocireferrer.PushWriteOnce(ctx, repo, art, tag); err != nil {
		t.Fatal(err)
	}
	return tag
}

func TestForOperatorReadsTheIndex(t *testing.T) {
	srv := httptest.NewServer(registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	reg := host + "/acme"
	tag := pushEntry(t, reg, "")
	big := pushEntry(t, reg, strings.Repeat("x", orgindex.MaxManifestBytes))

	l, err := ForOperator(Config{DeploymentName: opDeploy, Namespace: opNS, Registry: reg, Insecure: []string{host}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if tags, err := l.Tags(ctx); err != nil || len(tags) != 2 {
		t.Fatalf("tags %v: %v", tags, err)
	}
	e, err := l.Registry.Entry(ctx, tag)
	if err != nil || e.Name != "payments" || e.Bundle != reg+"/payments@"+dA {
		t.Fatalf("entry %+v: %v", e, err)
	}
	if _, err := l.Registry.Entry(ctx, big); err == nil || !strings.Contains(err.Error(), "bound") {
		t.Fatalf("a manifest over the bound was read: %v", err)
	}
	if recs, _, err := l.Registry.Images(ctx, reg+"/payments", dA); err == nil && len(recs) != 0 {
		t.Fatalf("a bundle with no images record has records: %+v", recs)
	}
}

// An index repository no CI has published to yet is an empty index, not a
// read that failed; a registry that fails the listing still fails it.
func TestForOperatorAbsentIndexIsEmpty(t *testing.T) {
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	l, err := ForOperator(Config{DeploymentName: opDeploy, Namespace: opNS, Registry: host + "/acme", Insecure: []string{host}})
	if err != nil {
		t.Fatal(err)
	}
	if tags, err := l.Tags(context.Background()); err != nil || len(tags) != 0 {
		t.Fatalf("an absent index: %v %v", tags, err)
	}
	// zot answers a repository it never stored with 404 NAME_UNKNOWN.
	zot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[{"code":"NAME_UNKNOWN","message":"repository name not known to registry"}]}`))
	}))
	t.Cleanup(zot.Close)
	zhost := strings.TrimPrefix(zot.URL, "http://")
	l, err = ForOperator(Config{DeploymentName: opDeploy, Namespace: opNS, Registry: zhost + "/acme", Insecure: []string{zhost}})
	if err != nil {
		t.Fatal(err)
	}
	if tags, err := l.Tags(context.Background()); err != nil || len(tags) != 0 {
		t.Fatalf("a NAME_UNKNOWN index: %v %v", tags, err)
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	t.Cleanup(broken.Close)
	bhost := strings.TrimPrefix(broken.URL, "http://")
	l, err = ForOperator(Config{DeploymentName: opDeploy, Namespace: opNS, Registry: bhost + "/acme", Insecure: []string{bhost}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Tags(context.Background()); err == nil {
		t.Fatal("a failed listing read as an empty index")
	}
}

// TestForOperatorIndexOpenHasScope shows both registry clients the linker opens
// carry oci.NewPushScope(cfg.Registry): a write inside the installation index
// passes, a write in a bundle repository outside the scope is refused before it
// leaves the process.
func TestForOperatorIndexOpenHasScope(t *testing.T) {
	srv := httptest.NewServer(registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	reg := host + "/acme"
	tag := pushEntry(t, reg, "")
	ctx := context.Background()

	// A manifest in a bundle repository, written without a scope.
	bundles, err := ocireferrer.Open(reg+"/payments", ocireferrer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	art := ocireferrer.Build(orgindex.ArtifactType, orgindex.MediaType, []byte("{}"), nil, nil)
	if err := ocireferrer.PushWriteOnce(ctx, bundles, art, "v1"); err != nil {
		t.Fatal(err)
	}

	l, err := ForOperator(Config{DeploymentName: opDeploy, Namespace: opNS, Registry: reg, Insecure: []string{host}})
	if err != nil {
		t.Fatal(err)
	}
	r := l.Registry.(registryReader)

	// The index repository client.
	desc, rc, err := r.repo.FetchReference(ctx, tag)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.repo.PushReference(ctx, desc, bytes.NewReader(raw), "in-scope"); err != nil {
		t.Fatalf("a tag inside the index repository was refused: %v", err)
	}
	outside, err := remote.NewRepository(reg + "/payments")
	if err != nil {
		t.Fatal(err)
	}
	outside.Client, outside.PlainHTTP = r.repo.Client, r.repo.PlainHTTP
	if err := outside.PushReference(ctx, desc, bytes.NewReader(raw), "escape"); !errors.Is(err, oci.ErrOutsidePushScope) {
		t.Fatalf("the index client tagged outside the push scope: %v", err)
	}

	// The images client.
	if err := r.images.Tag(ctx, oci.InstallationRepository(reg, oci.IndexRepository), desc.Digest.String(), "images-in-scope"); err != nil {
		t.Fatalf("the images client was refused inside the scope: %v", err)
	}
	if err := r.images.Tag(ctx, reg+"/payments", art.ManifestDesc.Digest.String(), "escape"); !errors.Is(err, oci.ErrOutsidePushScope) {
		t.Fatalf("the images client tagged outside the push scope: %v", err)
	}
}
