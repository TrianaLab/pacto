/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package credentials

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/trianalab/pacto/v4/pkg/oci"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// installationSource names the operator's installation credentials in a keychain.
const installationSource = "installation"

// cloudKeychain is oci.CloudKeychain behind a variable, so tests swap in a fake.
var cloudKeychain = oci.CloudKeychain

// SecretKeychain answers for one registry host with one Secret, read through
// the uncached reader on every resolve: no informer starts and a rotated
// Secret takes effect at once. Every other host is anonymous, so the Secret
// never goes to another registry.
type SecretKeychain struct {
	Reader          client.Reader
	Namespace, Name string
	Host            string
}

// Resolve implements authn.Keychain.
func (k SecretKeychain) Resolve(t authn.Resource) (authn.Authenticator, error) {
	if normalizeRegistryHost(t.RegistryStr()) != normalizeRegistryHost(k.Host) {
		return authn.Anonymous, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	secret := &corev1.Secret{}
	if err := k.Reader.Get(ctx, client.ObjectKey{Namespace: k.Namespace, Name: k.Name}, secret); err != nil {
		if apierrors.IsForbidden(err) {
			return nil, &pullSecretGrantError{Namespace: k.Namespace, Name: k.Name}
		}
		return nil, fmt.Errorf("registryPullSecret %s/%s: %w", k.Namespace, k.Name, err)
	}
	cfg, err := FromSecret(secret, t.RegistryStr())
	if err != nil {
		return nil, err
	}
	return authn.FromConfig(*cfg), nil
}

// pullSecretGrantError is a registryPullSecret the operator may not read.
type pullSecretGrantError struct{ Namespace, Name string }

func (e *pullSecretGrantError) Error() string {
	return fmt.Sprintf("the operator may not read Secret %s/%s: registryPullSecret renders its grant; "+
		"check the chart's -secret-grant Role in %s", e.Namespace, e.Name, e.Namespace)
}

// ParsePullSecretRef splits a registryPullSecret value, namespace/name.
func ParsePullSecretRef(s string) (ns, name string, err error) {
	ns, name, ok := strings.Cut(s, "/")
	if !ok || len(validation.IsDNS1123Label(ns)) > 0 || len(validation.IsDNS1123Subdomain(name)) > 0 {
		return "", "", fmt.Errorf("registryPullSecret %q must be namespace/name", s)
	}
	return ns, name, nil
}

// PrefixKeychain answers only for repositories under Prefixes: a repository
// equal to a prefix or below prefix + "/". A host-only resource (oras resolves
// credentials per host) is answered only with AllowHostOnly, and only for a
// prefix's host. Inner is never called for anything else.
type PrefixKeychain struct {
	Prefixes      []string
	AllowHostOnly bool
	Inner         authn.Keychain
}

// Resolve implements authn.Keychain.
func (k PrefixKeychain) Resolve(t authn.Resource) (authn.Authenticator, error) {
	if !k.covers(t) {
		return authn.Anonymous, nil
	}
	return k.Inner.Resolve(t)
}

func (k PrefixKeychain) covers(t authn.Resource) bool {
	repo := canonicalRepo(t.String())
	hostOnly := !strings.Contains(repo, "/")
	for _, p := range k.Prefixes {
		p = canonicalRepo(p)
		switch {
		case hostOnly:
			if k.AllowHostOnly && repo == normalizeRegistryHost(p) {
				return true
			}
		case repo == p || strings.HasPrefix(repo, p+"/"):
			return true
		}
	}
	return false
}

// canonicalRepo normalizes the host of host[/path] the way pull Secrets match
// hosts (docker.io is index.docker.io).
func canonicalRepo(s string) string {
	host, path, ok := strings.Cut(s, "/")
	host = normalizeRegistryHost(host)
	if !ok {
		return host
	}
	return host + "/" + path
}

// TenantPrefixes is the one repository a tenant-chosen ref (a Pacto's
// contractRef, its tag listing and its configuration refs) may read with the
// installation identity: the inferred contracts under registry. The snapshot,
// index, streams, approvals and stats repositories never serve a tenant ref,
// so a tenant cannot list their tags with the installation's grants.
func TenantPrefixes(registry string) []string {
	if registry == "" {
		return nil
	}
	return []string{canonicalRepo(oci.InstallationRepository(registry, oci.InferredRepository))}
}

// InstallationPrefixes is every repository the installation may push under:
// the installation repositories of registry first, then each extra prefix not
// already listed. OperatorKeychain binds the pull Secret to the host of the
// first entry, the registry's.
func InstallationPrefixes(registry string, extra []string) []string {
	out := oci.NewPushScope(registry).Prefixes()
	for _, p := range extra {
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// OperatorKeychain is the cluster-side keychain: the pre-cloud chain (env,
// pacto login, gh, docker) with one source in front of it, installation, that
// holds the named pull Secret and the cloud workload identity behind the push
// prefixes. With no prefixes the installation source answers nothing, so a
// tenant's contract ref never rides on the installation identity. The pull
// Secret answers only for the host of prefixes[0] (see InstallationPrefixes).
func OperatorKeychain(r client.Reader, pullSecret string, prefixes []string, allowHostOnly bool) (*oci.PactoKeychain, error) {
	inner := authn.Keychain(cloudKeychain())
	if pullSecret != "" {
		if len(prefixes) == 0 {
			return nil, fmt.Errorf("registryPullSecret %q needs registry", pullSecret)
		}
		ns, name, err := ParsePullSecretRef(pullSecret)
		if err != nil {
			return nil, err
		}
		host, _, _ := strings.Cut(prefixes[0], "/")
		inner = authn.NewMultiKeychain(SecretKeychain{Reader: r, Namespace: ns, Name: name, Host: host}, inner)
	}
	creds := oci.EnvCredentialOptions()
	creds.SkipCloud = true
	return oci.NewKeychain(creds).Prepend(installationSource,
		PrefixKeychain{Prefixes: prefixes, AllowHostOnly: allowHostOnly, Inner: inner}), nil
}
