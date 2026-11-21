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

// githubIssuer is the issuer of GitHub Actions OIDC tokens.
const githubIssuer = "https://token.actions.githubusercontent.com"

// CIToken is an OIDC token of a CI workload: the fake Fulcio issues a
// certificate whose URI SAN is subjectURI and whose extensions attest issuer,
// the source repository and its ref, as Fulcio does for GitHub Actions. With
// no repository it is any workload identity: a URI SAN, no source extensions.
// From GitHub's issuer the token carries the claims GitHub writes: repository
// in its owner/repo form (a URL is trimmed to it) and job_workflow_ref, the
// workflow subjectURI names.
func CIToken(subjectURI, issuer, repository, ref string) string {
	claims := map[string]string{"sub": subjectURI, "iss": issuer, "repository": repository, "ref": ref}
	if issuer == githubIssuer {
		claims["repository"] = strings.TrimPrefix(repository, "https://github.com/")
		claims["job_workflow_ref"] = strings.TrimPrefix(subjectURI, "https://github.com/")
	}
	return TokenWith(claims)
}

// sourceRepository is the source repository URI Fulcio attests for a token's
// repository claim: GitHub's owner/repo form becomes its github.com URL.
func sourceRepository(issuer, repository string) string {
	if issuer == githubIssuer && repository != "" && !strings.Contains(repository, "://") {
		return "https://github.com/" + repository
	}
	return repository
}

// Fulcio's certificate extensions a CI leaf carries: the issuer (v2), the
// source repository URI and its ref, each a DER UTF8String.
var (
	oidIssuerV2      = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}
	oidSourceRepo    = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 12}
	oidSourceRepoRef = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 14}
)

// ciLeaf is the certificate Fulcio issues a CI workload: a URI SAN and the
// extensions sigstore-go reads into certificate.Extensions.
func ciLeaf(subject, issuer, repository, ref string, pub crypto.PublicKey, parent *x509.Certificate, parentKey crypto.Signer) (*x509.Certificate, error) {
	san, err := url.Parse(subject)
	if err != nil {
		return nil, err
	}
	ext := func(id asn1.ObjectIdentifier, v string) pkix.Extension {
		der, _ := asn1.MarshalWithParams(v, "utf8")
		return pkix.Extension{Id: id, Value: der}
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), URIs: []*url.URL{san},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(9 * time.Minute),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		ExtraExtensions: []pkix.Extension{ext(oidIssuerV2, issuer)},
	}
	if repository != "" {
		tmpl.ExtraExtensions = append(tmpl.ExtraExtensions, ext(oidSourceRepo, repository), ext(oidSourceRepoRef, ref))
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, parentKey)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
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
		var claims struct{ Sub, Iss, Repository, Ref string }
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
		leaf, err := leafCert(claims.Sub, claims.Iss, sourceRepository(claims.Iss, claims.Repository), claims.Ref, pub, inter, interKey)
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

// leafCert is the certificate Fulcio issues for sub from iss: a CI leaf (a
// URI SAN and the source extensions) when sub is a URL or a repository is
// named, as Fulcio issues for a GitHub workflow, else an email SAN.
func leafCert(sub, iss, repository, ref string, pub crypto.PublicKey, parent *x509.Certificate, parentKey crypto.Signer) (*x509.Certificate, error) {
	if repository == "" && !strings.Contains(sub, "://") {
		return ca.GenerateLeafCert(sub, iss, time.Now().Add(-time.Minute), signerOf{pub}, parent, parentKey)
	}
	return ciLeaf(sub, iss, repository, ref, pub, parent, parentKey)
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
