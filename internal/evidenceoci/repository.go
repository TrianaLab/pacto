package evidenceoci

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

// maxReferrerPages bounds one subject scan. It exists only so a registry that
// returns a self-referential Link header cannot spin forever; exceeding it is an
// error, never a truncated result, so a scan is either complete or failed.
const maxReferrerPages = 1000

// RepositoryOptions configures how the evidence store reaches its contract
// registries. Credentials come from Pacto's existing ordered OCI keychain: there
// is no second login command, credential file or registry-auth model.
type RepositoryOptions struct {
	// Keychain is the shared Pacto keychain (env token, env user/pass, pacto
	// login, gh, docker). A nil keychain is anonymous access.
	Keychain authn.Keychain
	// Insecure lists registry hosts to reach over plain HTTP, mirroring
	// [github.com/trianalab/pacto/v3/pkg/oci.WithInsecureRegistries]. Loopback
	// hosts are plain HTTP without being listed, matching how Pacto already
	// resolves contract references.
	Insecure map[string]bool
	// PageSize sets the Referrers page size. Zero leaves the registry's default;
	// a small value is how tests exercise multi-page enumeration.
	PageSize int
}

// newRepository opens the ORAS repository holding subj's contract revision. Its
// referrers capability is asserted up front, which is what forbids oras-go's
// legacy referrers-tag fallback: a registry without the native OCI 1.1 endpoint
// surfaces an error instead of silently reading a mutable tag.
func newRepository(subj Subject, opts RepositoryOptions) (*remote.Repository, error) {
	repo, err := remote.NewRepository(subj.Path())
	if err != nil {
		return nil, fmt.Errorf("evidence oci: %q: %w", subj.Path(), err)
	}
	repo.PlainHTTP = plainHTTP(subj.Registry, opts.Insecure)
	repo.ReferrerListPageSize = opts.PageSize
	repo.ReferrerListMaxPages = maxReferrerPages
	repo.Client = &auth.Client{
		Client:     retry.DefaultClient,
		Cache:      auth.NewCache(),
		Credential: credentialFunc(opts.Keychain),
	}
	// Never false: an unsupported endpoint must fail the read, not fall back. The
	// only error is "already set", which a repository built one line ago cannot be.
	_ = repo.SetReferrersCapability(true)
	return repo, nil
}

// plainHTTP reports whether host is reached over http. A host is plain HTTP when
// it was explicitly allowed, or when go-containerregistry already treats it as
// such (loopback), so the evidence store and `pacto pull` agree on the scheme.
func plainHTTP(host string, insecure map[string]bool) bool {
	if insecure[host] {
		return true
	}
	reg, err := name.NewRegistry(host)
	return err == nil && reg.Scheme() == "http"
}

// credentialFunc adapts Pacto's ordered keychain to ORAS. It resolves the first
// non-anonymous source for the host, exactly as go-containerregistry's
// remote.WithAuthFromKeychain does for contract pulls.
//
// ponytail: no fall-through to the next source on a 401. The CLI retries other
// sources because a developer laptop accumulates stale credentials; a server is
// configured with one mounted Docker config Secret, and a rejected credential
// there is a misconfiguration worth reporting rather than papering over. Add
// per-source retry here if a deployment ever needs it.
func credentialFunc(kc authn.Keychain) auth.CredentialFunc {
	if kc == nil {
		return auth.StaticCredential("", auth.EmptyCredential)
	}
	return func(_ context.Context, hostport string) (auth.Credential, error) {
		reg, err := name.NewRegistry(hostport)
		if err != nil {
			return auth.EmptyCredential, err
		}
		authenticator, err := kc.Resolve(reg)
		if err != nil {
			return auth.EmptyCredential, err
		}
		cfg, err := authenticator.Authorization()
		if err != nil || cfg == nil {
			return auth.EmptyCredential, err
		}
		return credentialFromConfig(*cfg), nil
	}
}

// credentialFromConfig converts one resolved go-containerregistry auth config.
// The base64 "auth" field is decoded here because `pacto login` stores exactly
// that, and ORAS has no equivalent pre-encoded field.
func credentialFromConfig(cfg authn.AuthConfig) auth.Credential {
	cred := auth.Credential{
		Username:     cfg.Username,
		Password:     cfg.Password,
		RefreshToken: cfg.IdentityToken,
		AccessToken:  cfg.RegistryToken,
	}
	if cred.Username == "" && cred.Password == "" && cfg.Auth != "" {
		if raw, err := base64.StdEncoding.DecodeString(cfg.Auth); err == nil {
			if user, pass, ok := strings.Cut(string(raw), ":"); ok {
				cred.Username, cred.Password = user, pass
			}
		}
	}
	return cred
}
