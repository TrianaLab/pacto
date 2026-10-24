// Package testutil is a private Sigstore for tests: a certificate authority
// behind a Fulcio API, a timestamp authority and the trusted root that names
// both, so keyless signing runs end to end without the network.
package testutil

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/digitorus/timestamp"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
)

// Sigstore is a private Sigstore deployment.
type Sigstore struct {
	// Fulcio and TSA are the base URL of the Fulcio API and the URL of the
	// timestamp authority.
	Fulcio, TSA string
	// Root is the trusted root naming both; RootJSON is its trusted_root.json.
	Root     *root.TrustedRoot
	RootJSON []byte
}

// Token is an OIDC token the fake Fulcio issues a certificate for: identity
// (an email SAN) from issuer. Fulcio would check its signature; this one
// does not.
func Token(identity, issuer string) string {
	claims, _ := json.Marshal(map[string]string{"sub": identity, "iss": issuer})
	return "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
}

// New starts a private Sigstore that lives as long as t.
func New(t testing.TB) *Sigstore {
	t.Helper()
	rootCert, rootKey, err := ca.GenerateRootCa()
	must(t, err)
	inter, interKey, err := ca.GenerateFulcioIntermediate(rootCert, rootKey)
	must(t, err)
	tsaInter, tsaInterKey, err := ca.GenerateTSAIntermediate(rootCert, rootKey)
	must(t, err)
	tsaKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tsaLeaf, err := ca.GenerateTSALeafCert(time.Now().Add(-5*time.Minute), tsaKey, tsaInter, tsaInterKey)
	must(t, err)

	fulcio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var claims struct{ Sub, Iss string }
		parts := strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), ".")
		raw, _ := base64.RawURLEncoding.DecodeString(parts[min(1, len(parts)-1)])
		var req struct {
			PublicKeyRequest struct {
				PublicKey struct{ Content string } `json:"publicKey"`
			} `json:"publicKeyRequest"`
		}
		body, _ := io.ReadAll(r.Body)
		if json.Unmarshal(raw, &claims) != nil || json.Unmarshal(body, &req) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		pub, err := cryptoutils.UnmarshalPEMToPublicKey([]byte(req.PublicKeyRequest.PublicKey.Content))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		leaf, err := ca.GenerateLeafCert(claims.Sub, claims.Iss, time.Now().Add(-time.Minute), signerOf{pub}, inter, interKey)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		chain := []string{pemOf(leaf.Raw), pemOf(inter.Raw)}
		_ = json.NewEncoder(w).Encode(map[string]any{"signedCertificateEmbeddedSct": map[string]any{"chain": map[string]any{"certificates": chain}}})
	}))
	t.Cleanup(fulcio.Close)

	tsa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req, err := timestamp.ParseRequest(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ts := timestamp.Timestamp{HashAlgorithm: req.HashAlgorithm, HashedMessage: req.HashedMessage, Time: time.Now(),
			Policy: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 2}, ExtraExtensions: req.Extensions}
		resp, err := ts.CreateResponseWithOpts(tsaLeaf, tsaKey, crypto.SHA256)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/timestamp-reply")
		_, _ = w.Write(resp)
	}))
	t.Cleanup(tsa.Close)

	start := time.Now().Add(-5 * time.Hour)
	tr, err := root.NewTrustedRoot(root.TrustedRootMediaType01,
		[]root.CertificateAuthority{&root.FulcioCertificateAuthority{Root: rootCert, Intermediates: []*x509.Certificate{inter},
			ValidityPeriodStart: start, URI: fulcio.URL}},
		nil,
		[]root.TimestampingAuthority{&root.SigstoreTimestampingAuthority{Root: rootCert, Intermediates: []*x509.Certificate{tsaInter},
			Leaf: tsaLeaf, ValidityPeriodStart: start, URI: tsa.URL}},
		nil)
	must(t, err)
	js, err := tr.MarshalJSON()
	must(t, err)
	return &Sigstore{Fulcio: fulcio.URL, TSA: tsa.URL + "/api/v1/timestamp", Root: tr, RootJSON: js}
}

// signerOf hands ca.GenerateLeafCert a public key: it only calls Public.
type signerOf struct{ pub crypto.PublicKey }

func (s signerOf) Public() crypto.PublicKey                                { return s.pub }
func (signerOf) Sign(io.Reader, []byte, crypto.SignerOpts) ([]byte, error) { return nil, nil }

func pemOf(der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
