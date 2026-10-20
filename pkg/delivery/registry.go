package delivery

import (
	"context"
	"strings"

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

// ResolveRevision reads the contract the artifact repo@digest names. Failures
// are carried in Revision.Err rather than returned: plan() turns an unreadable
// contract into an Unknown verdict, which holds the change for a person.
func ResolveRevision(ctx context.Context, reg Registry, repo, digest string) Revision {
	r := Revision{Digest: digest}
	if digest == "" {
		return r
	}
	ann, err := reg.Annotations(ctx, repo+"@"+digest)
	if err != nil {
		r.Err = err.Error()
		return r
	}
	ref := ann[ContractAnnotation]
	if ContractDigest(ref) == "" {
		r.Err = "the artifact has no digest-pinned " + ContractAnnotation + " annotation"
		r.NoContract = true
		return r
	}
	r.ContractRef = ref
	b, err := reg.Pull(ctx, contract.TrimOCIScheme(ref))
	if err != nil {
		r.Err = err.Error()
		return r
	}
	r.Bundle = b
	return r
}
