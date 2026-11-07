package oci_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

type refusingTransport struct{ calls int }

var errRefused = errors.New("refused by the test transport")

func (rt *refusingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	rt.calls++
	return nil, errRefused
}

// WithTransport carries every request, on the keychain path and the credential-candidate path.
func TestClient_WithTransportCarriesEveryRequest(t *testing.T) {
	_, host := newTestClient(t)
	for name, kc := range map[string]authn.Keychain{
		"keychain":   authn.DefaultKeychain,
		"candidates": oci.NewKeychain(oci.CredentialOptions{}),
	} {
		rt := &refusingTransport{}
		client := oci.NewClient(kc, oci.WithTransport(rt))
		if _, err := client.ListTags(context.Background(), host+"/test/repo"); err == nil {
			t.Errorf("%s: ListTags succeeded past a refusing transport", name)
		}
		if rt.calls == 0 {
			t.Errorf("%s: the transport saw no request", name)
		}
	}
}
