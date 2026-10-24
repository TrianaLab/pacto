package delivery

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// Registry is the read-only registry access delivery needs. *oci.Client
// implements it.
type Registry interface {
	Annotations(ctx context.Context, ref string) (map[string]string, error)
	Pull(ctx context.Context, ref string) (*contract.Bundle, error)
}

// RepoFromURL turns a Flux OCIRepository spec.url ("oci://host/path") into a
// repository reference.
func RepoFromURL(url string) string { return strings.TrimPrefix(url, "oci://") }

// registryTimeout bounds one registry lookup made while planning. A registry
// that answers takes milliseconds; one that is down otherwise holds a plan (and
// a propose, and a dashboard request) for as long as the transport retries.
var registryTimeout = 10 * time.Second

// WithRegistryTimeout bounds a registry lookup made while planning.
func WithRegistryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, registryTimeout)
}

// ResolveRevision reads the contract the artifact repo@digest names. Failures
// are carried in Revision.Err rather than returned: plan() turns an unreadable
// contract into an Unknown verdict, which holds the change for a person.
//
// The lookup is bounded: a registry that does not answer within the bound
// gives an unreadable contract that says so, never a plan that waits on it.
func ResolveRevision(ctx context.Context, reg Registry, repo, digest string) Revision {
	r := Revision{Digest: digest}
	if digest == "" {
		return r
	}
	bounded, cancel := WithRegistryTimeout(ctx)
	defer cancel()
	timedOut := func(err error) string {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return "the registry did not answer within " + registryTimeout.String()
		}
		return err.Error()
	}
	ann, err := reg.Annotations(bounded, repo+"@"+digest)
	if err != nil {
		r.Err = timedOut(err)
		return r
	}
	ref := ann[ContractAnnotation]
	if ContractDigest(ref) == "" {
		r.Err = "the artifact has no digest-pinned " + ContractAnnotation + " annotation"
		r.NoContract = true
		return r
	}
	r.ContractRef = ref
	b, err := reg.Pull(bounded, contract.TrimOCIScheme(ref))
	if err != nil {
		r.Err = timedOut(err)
		return r
	}
	r.Bundle = b
	return r
}
