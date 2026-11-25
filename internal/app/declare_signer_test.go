package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/sigstore/sigstore-go/pkg/root"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const personIssuer = "https://accounts.google.com"

// noAmbient clears every ambient OIDC token source a test set earlier
// (TestMain unsets them for every test).
func noAmbient(t *testing.T) {
	t.Helper()
	for _, env := range []string{"SIGSTORE_ID_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_URL", "ACTIONS_ID_TOKEN_REQUEST_TOKEN"} {
		t.Setenv(env, "")
	}
}

// sigstoreRoot writes the private Sigstore's trusted root and returns the
// signing options that use it.
func sigstoreRoot(t *testing.T) declareSigning {
	t.Helper()
	ss := testutil.New(t)
	p := filepath.Join(t.TempDir(), "trusted_root.json")
	if err := os.WriteFile(p, ss.RootJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	return declareSigning{FulcioURL: ss.Fulcio, TimestampURL: ss.TSA, TrustedRoot: p}
}

func personToken(email string) string {
	return testutil.TokenWith(map[string]string{"sub": email, "email": email, "email_verified": "true", "iss": personIssuer})
}

// loginService is a Service whose interactive login returns tok and counts
// its calls.
func loginService(tok string, calls *int) *Service {
	s := &Service{}
	s.sys.login = func(_ context.Context, l declareLogin) (string, error) {
		*calls++
		if l.Issuer != sigstoreIssuer || l.ClientID != sigstoreClient {
			return "", errors.New("unexpected login " + l.Issuer + " " + l.ClientID)
		}
		return tok, nil
	}
	return s
}

func TestSignerKeyHasNoIdentity(t *testing.T) {
	t.Setenv("SIGSTORE_ID_TOKEN", personToken("ana@acme.com")) // a key wins over an ambient token
	dir := t.TempDir()
	keyFiles(t, dir)
	c, err := (&Service{}).declareSigner(t.Context(), declareSigning{Key: filepath.Join(dir, "cosign.key")}, false)
	if err != nil || c.Class != delivery.ClassAgent || c.Source != signerKey || c.MayDeclare || c.Identity.Subject != "" || c.Signer.Keypair == nil || c.Why == "" {
		t.Fatalf("%v %+v", err, c)
	}
	if _, err := (&Service{}).declareSigner(t.Context(), declareSigning{Key: filepath.Join(dir, "missing.key")}, false); err == nil ||
		!strings.Contains(err.Error(), "the signing key") {
		t.Fatal(err)
	}
}

func TestSignerAmbientCI(t *testing.T) {
	o := sigstoreRoot(t)
	// The token's sub is not a URL, so its identity comes from
	// job_workflow_ref, where TokenIdentity reads a GitHub Actions identity.
	t.Setenv("SIGSTORE_ID_TOKEN", testutil.TokenWith(map[string]string{"sub": "repo:acme/invoices", "iss": "https://token.actions.githubusercontent.com",
		"job_workflow_ref": "acme/invoices/.github/workflows/ci.yml@refs/heads/main"}))
	c, err := (&Service{}).declareSigner(t.Context(), o, false)
	if err != nil || c.Class != delivery.ClassCI || c.Source != signerAmbient || c.MayDeclare || c.Signer.Fulcio == nil ||
		c.Identity.Subject != "https://github.com/acme/invoices/.github/workflows/ci.yml@refs/heads/main" {
		t.Fatalf("%v %+v", err, c)
	}
}

func TestSignerAmbientWorkloadIdentity(t *testing.T) {
	o := sigstoreRoot(t)
	t.Setenv("SIGSTORE_ID_TOKEN", testutil.Token("system:serviceaccount:ci:builder", "https://kubernetes.default.svc"))
	c, err := (&Service{}).declareSigner(t.Context(), o, false)
	if err != nil || c.Class != delivery.ClassAgent || c.MayDeclare || c.Why == "" || c.Source != signerAmbient || c.Signer.Fulcio == nil {
		t.Fatalf("%v %+v", err, c)
	}
}

func TestSignerAmbientHumanMayNotDeclare(t *testing.T) {
	o := sigstoreRoot(t)
	t.Setenv("SIGSTORE_ID_TOKEN", personToken("ana@acme.com"))
	calls := 0
	c, err := loginService("", &calls).declareSigner(t.Context(), o, false)
	if err != nil || c.Class != delivery.ClassHuman || c.MayDeclare || calls != 0 || c.Identity.Subject != "ana@acme.com" {
		t.Fatalf("%v %+v %d", err, c, calls)
	}
}

func TestSignerAmbientTokenUnreadable(t *testing.T) {
	srv := probedServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer srv.Close()
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", srv.URL+"/?x=1")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "t")
	if _, err := (&Service{}).declareSigner(t.Context(), declareSigning{}, false); err == nil || !strings.Contains(err.Error(), "the ambient OIDC token") {
		t.Fatal(err)
	}
}

func TestSignerAgentNeverUsesHumanToken(t *testing.T) {
	t.Setenv("SIGSTORE_ID_TOKEN", personToken("ana@acme.com"))
	c, err := (&Service{}).declareSigner(t.Context(), declareSigning{}, true)
	if err != nil || c.Source != signerNone || c.Signer.Fulcio != nil || c.Signer.Keypair != nil || c.MayDeclare ||
		c.Identity != (delivery.PublisherIdentity{Issuer: signerNone, Class: delivery.ClassAgent, ClassifiedBy: c.ClassifiedBy}) ||
		!strings.Contains(c.Why, "never used by an agent") {
		t.Fatalf("%v %+v", err, c)
	}
	// An agent with no ambient token never logs in: its draft is unsigned.
	noAmbient(t)
	calls := 0
	if c, err := loginService("", &calls).declareSigner(t.Context(), declareSigning{}, true); err != nil || c.Source != signerNone || calls != 0 {
		t.Fatalf("%v %+v %d", err, c, calls)
	}
}

func TestSignerLoginWhenNoAmbient(t *testing.T) {
	t.Parallel()
	o := sigstoreRoot(t)
	o.PolicyNote = "no installation signer policy was read (forbidden); readers judge this signature with their own"
	calls := 0
	c, err := loginService(personToken("ana@acme.com"), &calls).declareSigner(t.Context(), o, false)
	if err != nil || calls != 1 || !c.MayDeclare || c.Source != signerLogin || c.Class != delivery.ClassHuman || c.PolicyNote != o.PolicyNote ||
		c.Identity.Subject != "ana@acme.com" || c.Identity.Issuer != personIssuer || c.Signer.Fulcio == nil {
		t.Fatalf("%v %+v %d", err, c, calls)
	}
	// The token is the login's alone.
	if tok, _ := c.Signer.IDToken(t.Context()); tok != personToken("ana@acme.com") {
		t.Fatal(tok)
	}
}

func TestSignerLoginNoPersonWritesDraft(t *testing.T) {
	t.Parallel()
	o := sigstoreRoot(t)
	calls := 0
	c, err := loginService(testutil.TokenWith(map[string]string{"email": "builder@acme.iam.gserviceaccount.com", "iss": personIssuer}), &calls).declareSigner(t.Context(), o, false)
	if err != nil || c.MayDeclare || c.Class != delivery.ClassAgent || c.Why == "" {
		t.Fatalf("%v %+v", err, c)
	}
}

func TestSignerLoginFailures(t *testing.T) {
	t.Parallel()
	o := sigstoreRoot(t)
	calls := 0
	// A failed login, a token naming nobody and an unreadable trusted root fail the run.
	failing := &Service{}
	failing.sys.login = func(context.Context, declareLogin) (string, error) { return "", errors.New("closed the browser") }
	if _, err := failing.declareSigner(t.Context(), o, false); err == nil || !strings.Contains(err.Error(), "the Sigstore login: closed the browser") {
		t.Fatal(err)
	}
	if _, err := loginService(testutil.Token("x", personIssuer), &calls).declareSigner(t.Context(), o, false); err == nil ||
		!strings.Contains(err.Error(), "the Sigstore login's token") {
		t.Fatal(err)
	}
	o.TrustedRoot = filepath.Join(t.TempDir(), "missing.json")
	if _, err := loginService(personToken("ana@acme.com"), &calls).declareSigner(t.Context(), o, false); err == nil || !strings.Contains(err.Error(), "the trusted root") {
		t.Fatal(err)
	}
}

func TestDeclareRootDefaultsToPublicGood(t *testing.T) {
	ss := testutil.New(t)
	old := publicGoodRoot
	defer func() { publicGoodRoot = old }()
	publicGoodRoot = func(string) (root.TrustedMaterial, error) { return ss.Root, nil }
	if tm, err := declareRoot(""); err != nil || tm != ss.Root {
		t.Fatal(err)
	}
	publicGoodRoot = func(string) (root.TrustedMaterial, error) { return nil, errors.New("tuf down") }
	if _, err := declareRoot(""); err == nil || !strings.Contains(err.Error(), "public-good Sigstore's trusted root: tuf down") {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(bad, []byte("{"), 0o600)
	if _, err := declareRoot(bad); err == nil || !strings.Contains(err.Error(), "the trusted root "+bad) {
		t.Fatal(err)
	}
}

// deviceIdP is an OpenID provider that answers sigstore's device code flow.
func deviceIdP(t *testing.T, tokenAnswer func(issuer string) map[string]any) string {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	var srv *httptest.Server
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		write(w, map[string]any{"issuer": srv.URL, "authorization_endpoint": srv.URL + "/auth", "token_endpoint": srv.URL + "/token",
			"jwks_uri": srv.URL + "/keys", "device_authorization_endpoint": srv.URL + "/device", "code_challenge_methods_supported": []string{"S256"}})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		write(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/device", func(w http.ResponseWriter, _ *http.Request) {
		write(w, map[string]any{"device_code": "dc", "user_code": "WXYZ-1234", "verification_uri": srv.URL + "/activate", "expires_in": 600, "interval": 1})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		out := tokenAnswer(srv.URL)
		if claims, ok := out["claims"]; ok {
			raw, _ := jwt.Signed(signer).Claims(claims).Serialize()
			out = map[string]any{"id_token": raw}
		}
		write(w, out)
	})
	srv = probedServer(t, mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestSignerDeviceUsesOAuthflow(t *testing.T) {
	t.Parallel()
	issuer := deviceIdP(t, func(iss string) map[string]any {
		return map[string]any{"claims": map[string]any{"iss": iss, "aud": sigstoreClient, "sub": "ana", "email": "ana@acme.com",
			"email_verified": true, "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix()}}
	})
	var prompts []string
	tok, err := interactiveLogin(t.Context(), declareLogin{Issuer: issuer, ClientID: sigstoreClient, Device: true,
		Prompt: func(s string) { prompts = append(prompts, s) }})
	if err != nil || tok == "" || len(prompts) == 0 || !strings.Contains(prompts[0], "WXYZ-1234") {
		t.Fatalf("%v %q %q", err, tok, prompts)
	}
	if sub, _, err := signing.TokenIdentity(tok); err != nil || sub != "ana@acme.com" {
		t.Fatal(sub, err)
	}
	refused := deviceIdP(t, func(string) map[string]any { return map[string]any{"error": "access_denied"} })
	if _, err := interactiveLogin(t.Context(), declareLogin{Issuer: refused, ClientID: sigstoreClient, Device: true, Prompt: func(string) {}}); err == nil ||
		!strings.Contains(err.Error(), "the device login") {
		t.Fatal(err)
	}
	if _, err := interactiveLogin(t.Context(), declareLogin{Issuer: "http://127.0.0.1:1", Device: true}); err == nil ||
		!strings.Contains(err.Error(), "the identity provider http://127.0.0.1:1") {
		t.Fatal(err)
	}
	// Without Device the browser login runs: it fails here at discovery.
	if _, err := interactiveLogin(t.Context(), declareLogin{Issuer: "http://127.0.0.1:1", Prompt: func(string) {}}); err == nil {
		t.Fatal("the browser login reached no identity provider and still answered")
	}
	// The caller's browser login runs with the fresh-login config, email included.
	fresh := func(_ context.Context, c authn.FreshConfig) (string, error) {
		return c.IssuerURL + " " + c.ClientID + " " + strings.Join(c.ExtraScopes, ","), nil
	}
	if tok, err := interactiveLogin(t.Context(), declareLogin{Issuer: issuer, ClientID: sigstoreClient, Fresh: fresh}); err != nil || tok != issuer+" sigstore email" {
		t.Fatal(tok, err)
	}
	if (&Service{}).env().login == nil {
		t.Fatal("no default login")
	}
}

func TestSignerPolicyRuleWins(t *testing.T) {
	o := sigstoreRoot(t)
	o.Rules = []delivery.SignerClassRule{{Name: "release-bot", Issuer: personIssuer, Subject: `^bot@acme\.com$`, Class: delivery.ClassAgent}}
	t.Setenv("SIGSTORE_ID_TOKEN", personToken("bot@acme.com"))
	c, err := (&Service{}).declareSigner(t.Context(), o, false)
	if err != nil || c.Class != delivery.ClassAgent || c.ClassifiedBy != "policy:release-bot" {
		t.Fatalf("%v %+v", err, c)
	}
}

func TestInstallationSignerIssuerNotListed(t *testing.T) {
	t.Parallel()
	p := &delivery.ClassPolicy{Signers: []delivery.PersonSigner{{Issuer: "https://other.example", Subjects: `.*@acme\.com`}}}
	id := delivery.PublisherIdentity{Subject: "ana@acme.com", Issuer: personIssuer, Class: delivery.ClassHuman}
	if err := checkInstallationSigner(id, p); err == nil || !strings.Contains(err.Error(), "lists no issuer "+personIssuer) {
		t.Fatal(err)
	}
	// The login refuses before it signs.
	o := sigstoreRoot(t)
	o.Policy = p
	calls := 0
	if _, err := loginService(personToken("ana@acme.com"), &calls).declareSigner(t.Context(), o, false); err == nil || !strings.Contains(err.Error(), "lists no issuer") {
		t.Fatal(err)
	}
}

func TestInstallationSignerSubjectNoMatch(t *testing.T) {
	t.Parallel()
	p := &delivery.ClassPolicy{Signers: []delivery.PersonSigner{{Issuer: personIssuer, Subjects: `.*@acme\.com`}}}
	for sub, ok := range map[string]bool{"ana@acme.com": true, "eve@acme.com.evil.io": false, "eve@evil.io": false} {
		err := checkInstallationSigner(delivery.PublisherIdentity{Subject: sub, Issuer: personIssuer}, p)
		if (err == nil) != ok || (err != nil && !strings.Contains(err.Error(), "matches none of the subjects")) {
			t.Errorf("%s: %v", sub, err)
		}
	}
	bad := &delivery.ClassPolicy{Signers: []delivery.PersonSigner{{Issuer: personIssuer, Subjects: `(`}}}
	if err := checkInstallationSigner(delivery.PublisherIdentity{Subject: "ana@acme.com", Issuer: personIssuer}, bad); err == nil {
		t.Fatal("an unparsable subjects pattern allowed a signer")
	}
}

func TestInstallationSignerUnreadablePrintsNote(t *testing.T) {
	t.Parallel()
	p, _, note := readSignerPolicy(t.Context(), dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()))
	if p != nil || !strings.HasPrefix(note, "no installation signer policy was read (") || !strings.HasSuffix(note, "); readers judge this signature with their own") {
		t.Fatalf("%v %q", p, note)
	}
	if err := checkInstallationSigner(delivery.PublisherIdentity{Subject: "ana@acme.com", Issuer: personIssuer}, nil); err != nil {
		t.Fatal(err)
	}
	cm := policyConfigMap(map[string]any{delivery.PolicyKey: "{"})
	if p, _, note := readSignerPolicy(t.Context(), dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), cm)); p != nil || !strings.Contains(note, "the delivery identity policy") {
		t.Fatalf("%v %q", p, note)
	}
}

func policyConfigMap(data map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": delivery.PolicyConfigMap, "namespace": delivery.PolicyNamespace}, "data": data}}
}

func TestReadSignerPolicyOneGet(t *testing.T) {
	t.Parallel()
	cm := policyConfigMap(map[string]any{delivery.PolicyKey: `{"signers":[{"issuer":"` + personIssuer + `","subjects":".*@acme\\.com"}]}`})
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), cm)
	p, _, note := readSignerPolicy(t.Context(), dyn)
	if p == nil || note != "" || len(p.Signers) != 1 || p.Signers[0].Issuer != personIssuer {
		t.Fatalf("%+v %q", p, note)
	}
	acts := dyn.Actions()
	if len(acts) != 1 {
		t.Fatalf("%d actions: %+v", len(acts), acts)
	}
	get, ok := acts[0].(k8stesting.GetAction)
	if !ok || get.GetResource().Resource != "configmaps" || get.GetNamespace() != "pacto-delivery" || get.GetName() != "pacto-delivery-identities" {
		t.Fatalf("%+v", acts[0])
	}
}

func TestSameSignerMismatch(t *testing.T) {
	t.Parallel()
	planned := delivery.PublisherIdentity{Subject: "ana@acme.com", Issuer: personIssuer}
	if err := sameSigner(planned, []delivery.PublisherIdentity{{Subject: "ana@acme.com", Issuer: personIssuer, Class: delivery.ClassHuman}}); err != nil {
		t.Fatal(err)
	}
	for _, got := range []delivery.PublisherIdentity{{Subject: "eve@acme.com", Issuer: personIssuer}, {Subject: "ana@acme.com", Issuer: "https://other.example"}} {
		if err := sameSigner(planned, []delivery.PublisherIdentity{got}); err == nil || !strings.Contains(err.Error(), "the pushed bundle is signed by") {
			t.Errorf("%+v: %v", got, err)
		}
	}
}

func TestSameSignerRefusesTwoSigners(t *testing.T) {
	t.Parallel()
	planned := delivery.PublisherIdentity{Subject: "ana@acme.com", Issuer: personIssuer}
	for _, got := range [][]delivery.PublisherIdentity{nil, {planned, planned}} {
		if err := sameSigner(planned, got); err == nil || !strings.Contains(err.Error(), "expected exactly one by ana@acme.com") {
			t.Errorf("%d: %v", len(got), err)
		}
	}
}

func TestRekorNoticeForPublicRekorOnly(t *testing.T) {
	t.Parallel()
	person := delivery.PublisherIdentity{Subject: "ana@acme.com", Issuer: personIssuer, Class: delivery.ClassHuman}
	if got := rekorNotice(person, signing.PublicRekor); got != "your email ana@acme.com is recorded in the public Rekor transparency log" {
		t.Fatal(got)
	}
	if got := rekorNotice(person, "https://rekor.acme.internal"); got != "" {
		t.Fatal(got)
	}
	if got := rekorNotice(delivery.PublisherIdentity{Subject: "https://github.com/acme/ci", Class: delivery.ClassCI}, signing.PublicRekor); got != "" {
		t.Fatal(got)
	}
}
