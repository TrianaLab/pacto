/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package credentials

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/trianalab/pacto/v4/pkg/oci"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// countingReader wraps a fake client and counts Secret Gets; err, when set, is returned instead.
func countingReader(t *testing.T, gets *int, err error, objs ...client.Object) client.Reader {
	t.Helper()
	return fake.NewClientBuilder().WithObjects(objs...).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			*gets++
			if err != nil {
				return err
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}).Build()
}

func tokenSecret(token string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ci", Name: "regcred"},
		Data:       map[string][]byte{"token": []byte(token)},
	}
}

func repoOf(t *testing.T, s string) name.Repository {
	t.Helper()
	r, err := name.NewRepository(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func regOf(t *testing.T, s string) name.Registry {
	t.Helper()
	r, err := name.NewRegistry(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func token(t *testing.T, a authn.Authenticator) string {
	t.Helper()
	cfg, err := a.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	return cfg.RegistryToken + cfg.Password
}

// fakeCloud answers every resource and counts the calls.
type fakeCloud struct{ calls *int }

func (f fakeCloud) Resolve(authn.Resource) (authn.Authenticator, error) {
	*f.calls++
	return authn.FromConfig(authn.AuthConfig{RegistryToken: "cloud"}), nil
}

// swapCloud replaces the cloud sources with a counting fake for one test.
func swapCloud(t *testing.T) *int {
	t.Helper()
	calls := 0
	orig := cloudKeychain
	t.Cleanup(func() { cloudKeychain = orig })
	cloudKeychain = func() *oci.PactoKeychain {
		return oci.NewKeychain(oci.CredentialOptions{Token: "unused"}).Prepend("fake cloud", fakeCloud{calls: &calls})
	}
	return &calls
}

func TestSecretKeychainOnlyForRegistryHost(t *testing.T) {
	gets := 0
	k := SecretKeychain{Reader: countingReader(t, &gets, nil, tokenSecret("t1")), Namespace: "ci", Name: "regcred", Host: "ghcr.io"}
	a, err := k.Resolve(repoOf(t, "ghcr.io/acme/pacto-targets"))
	if err != nil || token(t, a) != "t1" {
		t.Fatalf("ghcr.io = %v, %v; want the Secret's token", a, err)
	}
	gets = 0
	a, err = k.Resolve(repoOf(t, "quay.io/acme/x"))
	if err != nil || a != authn.Anonymous || gets != 0 {
		t.Fatalf("quay.io = %v, %v with %d Gets; want anonymous and no read", a, err, gets)
	}
}

func TestSecretKeychainReadsOnEveryResolve(t *testing.T) {
	gets := 0
	k := SecretKeychain{Reader: countingReader(t, &gets, nil, tokenSecret("t1")), Namespace: "ci", Name: "regcred", Host: "ghcr.io"}
	for range 3 {
		if _, err := k.Resolve(repoOf(t, "ghcr.io/acme/pacto-targets")); err != nil {
			t.Fatal(err)
		}
	}
	if gets != 3 {
		t.Errorf("Gets = %d, want 3", gets)
	}
}

func TestSecretKeychainForbiddenNamesTheGrant(t *testing.T) {
	gets := 0
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "regcred", errors.New("no"))
	k := SecretKeychain{Reader: countingReader(t, &gets, forbidden), Namespace: "ci", Name: "regcred", Host: "ghcr.io"}
	_, err := k.Resolve(repoOf(t, "ghcr.io/acme/pacto-targets"))
	var grant *pullSecretGrantError
	if !errors.As(err, &grant) || !strings.Contains(err.Error(), "Secret ci/regcred") ||
		!strings.Contains(err.Error(), "-secret-grant Role in ci") {
		t.Fatalf("err = %v; want the missing grant named", err)
	}
}

func TestSecretKeychainNotFoundIsAnError(t *testing.T) {
	gets := 0
	k := SecretKeychain{Reader: countingReader(t, &gets, nil), Namespace: "ci", Name: "regcred", Host: "ghcr.io"}
	a, err := k.Resolve(repoOf(t, "ghcr.io/acme/pacto-targets"))
	if err == nil || !apierrors.IsNotFound(err) || a != nil {
		t.Fatalf("Resolve = %v, %v; want a not-found error, never anonymous", a, err)
	}
}

func TestSecretKeychainDockerConfigEntryForHost(t *testing.T) {
	auth := base64.StdEncoding.EncodeToString([]byte("bot:pw"))
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ci", Name: "regcred"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(
			`{"auths":{"quay.io":{"auth":"eDp5"},"ghcr.io":{"auth":"` + auth + `"}}}`)},
	}
	gets := 0
	k := SecretKeychain{Reader: countingReader(t, &gets, nil, s), Namespace: "ci", Name: "regcred", Host: "ghcr.io"}
	a, err := k.Resolve(repoOf(t, "ghcr.io/acme/pacto-targets"))
	if err != nil || token(t, a) != "pw" {
		t.Fatalf("Resolve = %v, %v; want the ghcr.io entry", a, err)
	}
	s.Data = map[string][]byte{"token": nil}
	k.Reader = countingReader(t, &gets, nil, s)
	if _, err := k.Resolve(repoOf(t, "ghcr.io/acme/pacto-targets")); err == nil {
		t.Fatal("a Secret with no usable entry must be an error")
	}
}

func TestParsePullSecretRef(t *testing.T) {
	for _, c := range []struct {
		in, ns, name string
		ok           bool
	}{
		{"ci/regcred", "ci", "regcred", true},
		{"ci/reg.cred", "ci", "reg.cred", true},
		{"regcred", "", "", false},
		{"/regcred", "", "", false},
		{"ci/", "", "", false},
		{"Ci/regcred", "", "", false},
		{"ci/a/b", "", "", false},
	} {
		ns, n, err := ParsePullSecretRef(c.in)
		if (err == nil) != c.ok || ns != c.ns || n != c.name {
			t.Errorf("ParsePullSecretRef(%q) = %q, %q, %v", c.in, ns, n, err)
		}
	}
}

var installPrefixes = []string{"ghcr.io/acme/pacto-index", "ghcr.io/acme/pacto-inferred", "ghcr.io/acme/pacto-targets"}

func TestOperatorKeychainPrependsInstallation(t *testing.T) {
	calls := swapCloud(t)
	t.Setenv("PACTO_REGISTRY_TOKEN", "env")
	gets := 0
	kc, err := OperatorKeychain(countingReader(t, &gets, nil, tokenSecret("t1")), "ci/regcred", installPrefixes, false)
	if err != nil {
		t.Fatal(err)
	}
	cands, err := kc.Candidates(repoOf(t, "ghcr.io/acme/pacto-targets"))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(cands))
	for _, c := range cands {
		names = append(names, c.Name)
	}
	// installation first, then the chain without cloud sources: env token answers, google does not appear.
	if len(names) < 2 || names[0] != "installation" || names[1] != "env token" || token(t, cands[0].Authenticator) != "t1" {
		t.Fatalf("candidates = %v; want installation (the Secret) then env token", names)
	}
	for _, n := range names {
		if n == "google" || n == "ecr" || n == "acr" {
			t.Errorf("candidates list the host-wide cloud source %q", n)
		}
	}
	if *calls != 0 {
		t.Errorf("the Secret answered, but the cloud source was called %d times", *calls)
	}
}

func TestOperatorKeychainWithoutRegistryAnswersNothing(t *testing.T) {
	calls := swapCloud(t)
	kc, err := OperatorKeychain(nil, "", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []authn.Resource{repoOf(t, "ghcr.io/acme/pacto-targets"), regOf(t, "ghcr.io")} {
		cands, err := kc.Candidates(r)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cands {
			if c.Name == "installation" {
				t.Errorf("%s: the installation source answered with no registry", r)
			}
		}
	}
	if *calls != 0 {
		t.Errorf("cloud calls = %d, want 0", *calls)
	}
}

func TestOperatorKeychainPullSecretWithoutRegistryFails(t *testing.T) {
	if _, err := OperatorKeychain(nil, "ci/regcred", nil, false); err == nil || !strings.Contains(err.Error(), "needs registry") {
		t.Fatalf("err = %v; want registryPullSecret needs registry", err)
	}
	if _, err := OperatorKeychain(nil, "regcred", installPrefixes, false); err == nil {
		t.Fatal("a malformed registryPullSecret must fail")
	}
}

func TestPrefixKeychainAnswersUnderPrefix(t *testing.T) {
	for _, c := range []struct {
		res  authn.Resource
		want bool
	}{
		{repoOf(t, "ghcr.io/acme/pacto-targets"), true},
		{repoOf(t, "ghcr.io/acme/pacto-inferred/stripe"), true},
		{repoOf(t, "ghcr.io/acme/pacto-targetsx"), false},
		{repoOf(t, "ghcr.io/acme/other"), false},
		{repoOf(t, "quay.io/acme/pacto-targets"), false},
		{regOf(t, "ghcr.io"), false},
	} {
		calls := 0
		k := PrefixKeychain{Prefixes: installPrefixes, Inner: fakeCloud{calls: &calls}}
		a, err := k.Resolve(c.res)
		if err != nil {
			t.Fatal(err)
		}
		if got := a != authn.Anonymous; got != c.want || (calls == 1) != c.want {
			t.Errorf("%s: answered %v with %d inner calls, want %v", c.res, got, calls, c.want)
		}
	}
}

func TestPrefixKeychainNormalizesDockerHub(t *testing.T) {
	calls := 0
	k := PrefixKeychain{Prefixes: []string{"docker.io/acme/pacto-targets"}, Inner: fakeCloud{calls: &calls}}
	if a, _ := k.Resolve(repoOf(t, "docker.io/acme/pacto-targets")); a == authn.Anonymous {
		t.Error("docker.io and index.docker.io are the same registry")
	}
}

func TestPrefixKeychainHostOnlyNeedsAllowHostOnly(t *testing.T) {
	calls := 0
	k := PrefixKeychain{Prefixes: installPrefixes, Inner: fakeCloud{calls: &calls}}
	if a, _ := k.Resolve(regOf(t, "ghcr.io")); a != authn.Anonymous || calls != 0 {
		t.Fatal("a host-only resource must be anonymous without AllowHostOnly")
	}
	k.AllowHostOnly = true
	if a, _ := k.Resolve(regOf(t, "ghcr.io")); a == authn.Anonymous || calls != 1 {
		t.Fatal("AllowHostOnly must answer for the prefix's host")
	}
	if a, _ := k.Resolve(regOf(t, "quay.io")); a != authn.Anonymous || calls != 1 {
		t.Fatal("AllowHostOnly must not answer for another host")
	}
}

func TestTenantRefOutsideRegistryGetsNoInstallationCredential(t *testing.T) {
	calls := swapCloud(t)
	gets := 0
	kc, err := OperatorKeychain(countingReader(t, &gets, nil, tokenSecret("t1")), "ci/regcred", installPrefixes, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"ghcr.io/other/x", "europe-docker.pkg.dev/acme/finance-private/payments"} {
		cands, err := kc.Candidates(repoOf(t, ref))
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cands {
			if c.Name == "installation" {
				t.Errorf("%s reached the installation identity", ref)
			}
		}
	}
	if gets != 0 || *calls != 0 {
		t.Errorf("Secret Gets = %d, cloud calls = %d; want 0 and 0", gets, *calls)
	}
}

func TestInstallationPrefixesPutTheRegistryFirst(t *testing.T) {
	got := InstallationPrefixes("ghcr.io/acme", []string{"quay.io/acme/streams", "", "ghcr.io/acme/pacto-targets"})
	want := append(slices.Clone(installPrefixes), "quay.io/acme/streams")
	if !slices.Equal(got, want) {
		t.Errorf("InstallationPrefixes = %v, want %v", got, want)
	}
	if got := InstallationPrefixes("", nil); len(got) != 0 {
		t.Errorf("no registry = %v, want none", got)
	}
}
