/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package targetsnapshot

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const ns = "prod"

var gvkPacto = gvkPactoList.GroupVersion().WithKind("Pacto")

// testRegistry is an in-process OCI registry with the Referrers API.
type testRegistry struct {
	host string
	// fault, when set, answers the requests it matches with its status.
	fault func(*http.Request) int
}

func newTestRegistry(t *testing.T) *testRegistry {
	t.Helper()
	r := &testRegistry{}
	inner := registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if r.fault != nil {
			if code := r.fault(req); code != 0 {
				http.Error(w, `{"errors":[{"code":"DENIED"}]}`, code)
				return
			}
		}
		inner.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	r.host = strings.TrimPrefix(srv.URL, "http://")
	return r
}

// edSigner signs with key through sigstore-go.
func edSigner(key ed25519.PrivateKey) core.Signer {
	kp, _ := signing.FromPrivateKey(key)
	return signing.Keyed(kp, "")
}

type failingSigner struct{}

func (failingSigner) Sign(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("kms down")
}

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	s.AddKnownTypeWithName(gvkPacto, &unstructured.Unstructured{})
	s.AddKnownTypeWithName(gvkPactoList, &unstructured.UnstructuredList{})
	return s
}

// kindOf is the kind of an unstructured list the publisher reads.
func kindOf(list client.ObjectList) string {
	return strings.TrimSuffix(list.GetObjectKind().GroupVersionKind().Kind, "List")
}

// pactoObj is a reconciled Pacto in ns for service.
func pactoObj(name, service, contractDigest, status string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"namespace": ns, "name": name},
		"status": map[string]any{
			"contractStatus":   status,
			"lastReconciledAt": "2026-10-20T10:00:00Z",
			"contract": map[string]any{"serviceName": service, "version": "1.0.0", "owner": map[string]any{"team": "team-" + service},
				"resolvedRef": "oci://reg.example/pactos/" + service + "@" + contractDigest},
		},
	}}
	u.SetGroupVersionKind(gvkPacto)
	return u
}
