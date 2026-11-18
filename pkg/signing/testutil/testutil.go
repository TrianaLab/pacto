// Package testutil is a private Sigstore for tests: a certificate authority
// behind a Fulcio API, a timestamp authority and the trusted root that names
// both, so keyless signing runs end to end without the network.
//
// Experimental: not covered by semantic versioning. Delivery is Alpha, and
// this API may change in a minor release.
package testutil

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
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
// (a URI SAN for a URL such as a GitHub workflow, else an email SAN) from
// issuer. Fulcio would check its signature; this one
// does not.
func Token(identity, issuer string) string {
	return TokenWith(map[string]string{"sub": identity, "iss": issuer})
}

// TokenWith is a token carrying claims. The fake Fulcio issues for "sub", so
// a test sets it to the identity the other claims map to.
func TokenWith(claims map[string]string) string {
	raw, _ := json.Marshal(claims)
	return "e30." + base64.RawURLEncoding.EncodeToString(raw) + ".sig"
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
		leaf, err := leafCert(claims.Sub, claims.Iss, pub, inter, interKey)
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

// leafCert is the certificate Fulcio issues for sub from iss: a URI SAN when
// sub is a URL, as Fulcio issues for a GitHub workflow, else an email SAN.
func leafCert(sub, iss string, pub crypto.PublicKey, parent *x509.Certificate, parentKey crypto.Signer) (*x509.Certificate, error) {
	u, err := url.Parse(sub)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ca.GenerateLeafCert(sub, iss, time.Now().Add(-time.Minute), signerOf{pub}, parent, parentKey)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), URIs: []*url.URL{u},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(10 * time.Minute),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		ExtraExtensions: []pkix.Extension{{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}, Value: []byte(iss)}}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, parentKey)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
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
