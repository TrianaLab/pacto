package deliveryrecord

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// Apply refusals live on one stream per service
// ([delivery.ApplyRefusalStream]) in the recorder's streams repository,
// written like identity pins. Anyone who reads the registry reads them.

// MaxApplyRefusals bounds how many apply refusals of a service
// ListApplyRefusals reads.
const MaxApplyRefusals = 20

// PushApplyRefusal writes bundle, a signed apply refusal
// (delivery.SignApplyRefusal), on its service's stream in repo in mode, and
// returns where it is. The stream's anchor must exist.
func PushApplyRefusal(ctx context.Context, repo *remote.Repository, mode capabilities.ReferrersMode, bundle []byte) (string, error) {
	st, _, err := delivery.OpenApplyRefusal(bundle, func([]byte) error { return nil }) // its own signer made it
	if err != nil {
		return "", err
	}
	return Push(ctx, repo, delivery.ApplyRefusalStream(st.Predicate.Service), mode, delivery.PredicateApplyRefusal, bundle, nil)
}

// ApplyRefusal is one apply refusal read back and verified.
type ApplyRefusal struct {
	// ID is the refusal's id: the sha256 of its signed payload.
	ID string `json:"id"`
	// Manifest is the digest of the manifest that carried it.
	Manifest string                `json:"manifest"`
	Refusal  delivery.ApplyRefusal `json:"refusal"`
}

// ApplyRefusalListing is a service's apply refusals, newest first, one per
// (Stage, digest). Unreadable names every refusal that could not be used,
// with why; Cut says more were attached than MaxApplyRefusals.
type ApplyRefusalListing struct {
	Refusals   []ApplyRefusal `json:"refusals"`
	Unreadable []string       `json:"unreadable,omitempty"`
	Cut        bool           `json:"cut,omitempty"`
}

// ListApplyRefusals reads the newest MaxApplyRefusals apply refusals of svc
// from repo and keeps the newest per (Stage, digest). A refusal is listed
// only when verify accepts its signature and it names svc; any other is
// unreadable and never takes the bound again. A listing or fetch failure is
// an error: the registry could not be read, which is never the same as no
// refusal.
func (r *Reader) ListApplyRefusals(ctx context.Context, repo *remote.Repository, svc delivery.ServiceRef, verify func([]byte) error) (ApplyRefusalListing, error) {
	stream := delivery.ApplyRefusalStream(svc)
	fetched, cut, err := r.ListPredicate(ctx, repo, stream, delivery.PredicateApplyRefusal, "", MaxApplyRefusals)
	if err != nil {
		return ApplyRefusalListing{}, err
	}
	out := ApplyRefusalListing{Refusals: []ApplyRefusal{}, Cut: cut}
	for _, f := range fetched {
		st, id, err := delivery.OpenApplyRefusal(f.Bundle, verify)
		why := ""
		switch {
		case err != nil:
			why = err.Error()
		case st.Predicate.Service != svc:
			why = fmt.Sprintf("names service %q, read from the apply refusals of %q", st.Predicate.Service.Name, svc.Name)
		}
		if why != "" {
			r.Reject(stream, delivery.PredicateApplyRefusal, f.Manifest)
			out.Unreadable = append(out.Unreadable, f.Manifest+": "+why)
			continue
		}
		out.Refusals = append(out.Refusals, ApplyRefusal{ID: id, Manifest: f.Manifest, Refusal: st.Predicate})
	}
	slices.SortStableFunc(out.Refusals, func(a, b ApplyRefusal) int { return cmp.Compare(b.Refusal.At, a.Refusal.At) })
	seen := map[[3]string]bool{}
	out.Refusals = slices.DeleteFunc(out.Refusals, func(a ApplyRefusal) bool {
		k := [3]string{a.Refusal.Namespace, a.Refusal.Stage, a.Refusal.Digest}
		dup := seen[k]
		seen[k] = true
		return dup
	})
	return out, nil
}
