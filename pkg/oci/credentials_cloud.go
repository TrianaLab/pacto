//go:build !js

package oci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	ecr "github.com/awslabs/amazon-ecr-credential-helper/ecr-login"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/v1/google"
)

// The cloud sources sit behind package variables so tests swap in fakes. None
// of the defaults is a function literal, so every default line runs in a test.
var (
	googleKeychain              = google.Keychain
	ecrHelper      authn.Helper = ecr.NewECRHelper(ecr.WithLogger(io.Discard))
	acrToken                    = defaultACRToken
	acrClient                   = &http.Client{Timeout: 10 * time.Second}
	acrExchangeURL              = defaultACRExchangeURL
)

// acrUsername is the fixed user name ACR expects alongside a refresh token.
const acrUsername = "00000000-0000-0000-0000-000000000000"

// acrScope is the AAD scope an ACR refresh-token exchange accepts.
const acrScope = "https://containerregistry.azure.net/.default"

// hostScoped answers only for the hosts match accepts, and never calls kc for
// any other host. The host filter lives here, in Pacto, so a ghcr.io pull can
// never reach a cloud metadata server whatever the third-party source does.
type hostScoped struct {
	match func(host string) bool
	kc    authn.Keychain
}

func (h hostScoped) Resolve(t authn.Resource) (authn.Authenticator, error) {
	if !h.match(t.RegistryStr()) {
		return authn.Anonymous, nil
	}
	return h.kc.Resolve(t)
}

// googleHost reports whether host is Artifact Registry or Container Registry.
func googleHost(host string) bool {
	return host == "gcr.io" || hasLabelSuffix(host, ".gcr.io") || hasLabelSuffix(host, ".pkg.dev")
}

var ecrHostPattern = regexp.MustCompile(`^\d{12}\.dkr\.ecr(-fips)?\.[a-z0-9-]+\.amazonaws\.com(\.cn)?$`)

// ecrHost reports whether host is a private ECR registry or ECR Public.
func ecrHost(host string) bool {
	return host == "public.ecr.aws" || ecrHostPattern.MatchString(host)
}

// acrHost reports whether host is an Azure Container Registry in any cloud.
func acrHost(host string) bool {
	for _, suffix := range []string{".azurecr.io", ".azurecr.cn", ".azurecr.us"} {
		if hasLabelSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// hasLabelSuffix reports whether host ends in suffix with at least one label
// before it ("acme.azurecr.io" yes, "azurecr.io" no).
func hasLabelSuffix(host, suffix string) bool {
	return len(host) > len(suffix) && strings.HasSuffix(host, suffix)
}

// cloudSources returns the Google, Amazon ECR and Azure ACR workload-identity
// sources, each answering only for its own hosts.
func cloudSources() []namedKeychain {
	return []namedKeychain{
		{name: "google", kc: hostScoped{match: googleHost, kc: googleKeychain}},
		{name: "ecr", kc: hostScoped{match: ecrHost, kc: authn.NewKeychainFromHelper(ecrHelper)}},
		{name: "acr", kc: hostScoped{match: acrHost, kc: acrKeychain{}}},
	}
}

// CloudKeychain returns only the cloud workload-identity sources. A
// cluster-side process builds its keychain with SkipCloud and places this one
// behind its installation prefixes, so a tenant's reference never rides on the
// installation identity.
func CloudKeychain() *PactoKeychain {
	return &PactoKeychain{sources: cloudSources()}
}

// acrKeychain trades the machine's Azure identity for an ACR refresh token.
type acrKeychain struct{}

func (acrKeychain) Resolve(t authn.Resource) (authn.Authenticator, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	aad, err := acrToken(ctx)
	if err != nil {
		if credentialUnavailable(err) {
			return authn.Anonymous, nil
		}
		return nil, fmt.Errorf("acr: %w", err)
	}
	refresh, err := acrExchange(ctx, t.RegistryStr(), aad)
	if err != nil {
		return nil, err
	}
	return authn.FromConfig(authn.AuthConfig{Username: acrUsername, Password: refresh}), nil
}

// unavailableType is azidentity's unexported "this credential cannot try"
// error type. azidentity exports no type to match it, so the type is taken
// from the exported constructor.
var unavailableType = reflect.TypeOf(azidentity.NewCredentialUnavailableError(""))

// credentialUnavailable reports whether err says no Azure identity is present,
// as opposed to an identity that failed.
func credentialUnavailable(err error) bool {
	target := reflect.New(unavailableType)
	return errors.As(err, target.Interface())
}

// defaultACRToken takes an AAD token for ACR from the default Azure chain
// (environment, workload identity, managed identity, developer tools).
func defaultACRToken(ctx context.Context) (string, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return "", err
	}
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{acrScope}})
	if err != nil {
		return "", err
	}
	return tok.Token, nil
}

// defaultACRExchangeURL is always https: the AAD token is valid for every ACR
// the identity reaches, so it never travels in clear, whatever
// PACTO_INSECURE_REGISTRIES holds.
func defaultACRExchangeURL(host string) string {
	return "https://" + host + "/oauth2/exchange"
}

// acrExchange posts the AAD token to the registry's exchange endpoint and
// returns the ACR refresh token.
func acrExchange(ctx context.Context, host, aad string) (string, error) {
	form := url.Values{"grant_type": {"access_token"}, "service": {host}, "access_token": {aad}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, acrExchangeURL(host), strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("acr: exchange for %s: %w", host, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := acrClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("acr: exchange for %s: %w", host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("acr: exchange for %s: status %d", host, resp.StatusCode)
	}
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil || body.RefreshToken == "" {
		return "", fmt.Errorf("acr: exchange for %s: status %d with no refresh_token", host, resp.StatusCode)
	}
	return body.RefreshToken, nil
}
