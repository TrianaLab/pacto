package evidenceingest

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidenceenvelope"
)

func TestContributorsOfListsGrantedProducers(t *testing.T) {
	pub1, _ := testKeypair()
	pub2, _ := testKeypair2()
	ts := evidenceenvelope.MapTrustStore{
		"my-idp-b": {PublicKey: pub2, ProducerID: "my-idp", ContractRepos: []string{"ghcr.io/acme/contracts", "ghcr.io/acme/pacto-inferred/my-idp"}},
		"my-idp-a": {PublicKey: pub1, ProducerID: "my-idp", ContractRepos: []string{"ghcr.io/acme/pacto-inferred/my-idp"}},
		"other-a":  {PublicKey: pub1, ProducerID: "other", ContractRepos: []string{"ghcr.io/acme/contracts"}},
	}
	want := []ProducerContributor{{
		Producer:   "my-idp",
		Repository: "ghcr.io/acme/pacto-inferred/my-idp/services",
		Keys: []ContributorKey{
			{KeyID: "my-idp-a", PublicKey: base64.StdEncoding.EncodeToString(pub1)},
			{KeyID: "my-idp-b", PublicKey: base64.StdEncoding.EncodeToString(pub2)},
		},
	}}
	if got := ContributorsOf(ts); !reflect.DeepEqual(got, want) {
		t.Fatalf("contributors = %+v, want %+v", got, want)
	}
}

func TestContributorsOfSortsProducers(t *testing.T) {
	pub, _ := testKeypair()
	ts := evidenceenvelope.MapTrustStore{
		"b": {PublicKey: pub, ProducerID: "b-idp", ContractRepos: []string{"ghcr.io/acme/pacto-inferred/b-idp"}},
		"a": {PublicKey: pub, ProducerID: "a-idp", ContractRepos: []string{"ghcr.io/acme/pacto-inferred/a-idp"}},
	}
	got := ContributorsOf(ts)
	if len(got) != 2 || got[0].Producer != "a-idp" || got[1].Producer != "b-idp" {
		t.Fatalf("contributors = %+v, want a-idp then b-idp", got)
	}
}

func TestContributorsOfIgnoresUnboundKeys(t *testing.T) {
	pub, _ := testKeypair()
	ts := evidenceenvelope.MapTrustStore{
		"free": {PublicKey: pub, ContractRepos: []string{"ghcr.io/acme/pacto-inferred/free"}},
		"open": {PublicKey: pub, ProducerID: "open"},
	}
	if got := ContributorsOf(ts); len(got) != 0 {
		t.Fatalf("contributors = %+v, want none", got)
	}
}

func TestProducersEndpointCarriesContributors(t *testing.T) {
	pub, _ := testKeypair()
	contributors := ContributorsOf(evidenceenvelope.MapTrustStore{
		"k": {PublicKey: pub, ProducerID: "my-idp", ContractRepos: []string{"ghcr.io/acme/pacto-inferred/my-idp"}},
	})
	key := base64.StdEncoding.EncodeToString(pub)
	for _, tc := range []struct {
		contributors []ProducerContributor
		body         string
	}{
		// With no contributor the body is byte-identical to what it was before.
		{nil, `{"producers":["env-a"]}`},
		{contributors, `{"producers":["env-a"],"contributors":[{"producer":"my-idp","repository":"ghcr.io/acme/pacto-inferred/my-idp/services","keys":[{"keyId":"k","publicKey":"` + key + `"}]}]}`},
	} {
		mux := http.NewServeMux()
		NewHandler(nil, []string{"env-a"}, nil).WithContributors(tc.contributors).Routes(mux)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ProducersPath, nil))
		if got := strings.TrimSpace(rec.Body.String()); got != tc.body {
			t.Errorf("body = %s, want %s", got, tc.body)
		}
	}
}

// A producer's grant entry is also a contract-repo prefix, so only this rule
// keeps a contribution from becoming a compliance subject (Decision 15).
func TestEnvelopeOnContributionRefRefused(t *testing.T) {
	pub, priv := testKeypair()
	trust := evidenceenvelope.MapTrustStore{keyID: {PublicKey: pub, ProducerID: "env-a",
		ContractRepos: []string{"ghcr.io/acme/pacto-inferred/env-a"}}}
	resolver := fakeResolver{c: contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "payments"}}}
	a := NewAcceptor(trust, resolver, NewMemoryStore(), fixedNow)
	// Any repository below the grant, not only services, is refused.
	for i, repo := range []string{"services", "checkout", "checkout/deeper"} {
		es := testEvidenceSet()
		es.ContractRef = "oci://ghcr.io/acme/pacto-inferred/env-a/" + repo + "@sha256:" + strings.Repeat("b", 64)
		_, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, uint64(i+1), "e"+strings.ReplaceAll(repo, "/", "-"), es))
		if !errors.Is(err, ErrContractRefPolicy) || !strings.Contains(err.Error(), "a contribution is an inferred record, never a compliance subject") {
			t.Fatalf("%s: accept = %v, want the contribution refusal", repo, err)
		}
	}
	if underInferred("oci://ghcr.io/pacto-inferred/payments@sha256:x") || underInferred("ghcr.io") {
		t.Error("an organization named pacto-inferred, or a bare host, is under pacto-inferred")
	}
}
