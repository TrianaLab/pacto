/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package linker

import (
	"context"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"

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
