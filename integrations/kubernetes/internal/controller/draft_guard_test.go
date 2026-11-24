/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/registry"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pactov1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// The revision mirror, over the real loader and a real registry, mirrors the
// published 1.0.0 only: the draft-<n> tags beside it never become a
// PactoRevision.
func TestRevisionMirrorSkipsDraftTags(t *testing.T) {
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	t.Setenv(loader.InsecureRegistriesEnvVar, host)
	for tag, version := range map[string]string{"1.0.0": "1.0.0", "draft-1": "1.1.0", "draft-2": "1.1.0"} {
		yaml := "pactoVersion: \"2.4\"\nservice: {name: invoices, version: " + version + "}\n"
		c, err := contract.Parse(strings.NewReader(yaml))
		if err != nil {
			t.Fatal(err)
		}
		b := &contract.Bundle{Contract: c, FS: fstest.MapFS{"pacto.yaml": {Data: []byte(yaml)}}}
		if _, err := oci.NewClient(authn.DefaultKeychain).Push(t.Context(), host+"/acme/invoices:"+tag, b); err != nil {
			t.Fatalf("push %s: %v", tag, err)
		}
	}
	pacto := &pactov1.Pacto{ObjectMeta: metav1.ObjectMeta{Name: "invoices", Namespace: "default", UID: "invoices-uid"}}
	r := newReconciler(pacto)
	r.Loader = loader.New()
	if err := r.syncAllRevisions(t.Context(), pacto, "oci://"+host+"/acme/invoices", nil); err != nil {
		t.Fatalf("syncAllRevisions: %v", err)
	}
	revs := &pactov1.PactoRevisionList{}
	if err := r.List(t.Context(), revs, client.InNamespace("default")); err != nil {
		t.Fatal(err)
	}
	if len(revs.Items) != 1 || revs.Items[0].Spec.Source.OCI != host+"/acme/invoices:1.0.0" {
		t.Fatalf("revisions = %+v; want the published 1.0.0 only", revs.Items)
	}
}
