package deliveryrecord

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

// Publish refusals live in the service's contract bundle repository, attached
// to one deterministic anchor per service, so pacto publish (which refuses)
// and the dashboard (which shows them) find the same place from the contract
// reference alone.
const (
	// RefusalsType is the artifact and layer type of a refusal anchor.
	RefusalsType = "application/vnd.pacto.publish.refusals.v1+json"
	// AnnAt is when a refusal was made. It is unsigned and only orders the
	// listing, so a reader fetches the newest refusals and no others.
	AnnAt = "dev.pacto.at"
)

// MaxRefusals bounds how many refusals ListRefusals fetches.
var MaxRefusals = 20

// RefusalAnchor is the anchor every refusal of service is attached to.
func RefusalAnchor(service string) ocireferrer.Artifact {
	payload, _ := json.Marshal(map[string]string{"refusals": service}) // a string map; marshalling cannot fail
	return ocireferrer.Build(RefusalsType, RefusalsType, payload, nil, nil)
}

// RefusalAnchorTag keeps a refusal anchor from being collected by a registry
// that removes untagged manifests.
func RefusalAnchorTag(service string) string {
	sum := sha256.Sum256([]byte(service))
	return "refusals-" + hex.EncodeToString(sum[:])
}

// PushRefusal attaches a signed refusal bundle (delivery.SignRefusal) to its
// service's anchor in repo, writing and tagging the anchor first, and confirms
// the Referrers API lists it. Pushing the same bundle again changes nothing.
func PushRefusal(ctx context.Context, repo *remote.Repository, bundle []byte) error {
	st, _, err := delivery.OpenRefusal(bundle, func([]byte) error { return nil }) // its own signer made it
	if err != nil {
		return err
	}
	service := st.Predicate.Service
	anchor := RefusalAnchor(service)
	if err := ocireferrer.Push(ctx, repo, anchor); err != nil {
		return err
	}
	if err := repo.Tag(ctx, anchor.ManifestDesc, RefusalAnchorTag(service)); err != nil {
		return err
	}
	ann := map[string]string{AnnBundleContent: "dsse-envelope", AnnPredicateType: delivery.PredicatePublishRefusal, AnnAt: st.Predicate.At}
	art := ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, bundle, &anchor.ManifestDesc, ann)
	if err := ocireferrer.Push(ctx, repo, art); err != nil {
		return err
	}
	return ocireferrer.Confirm(ctx, repo, anchor.ManifestDesc, art.ManifestDesc)
}

// Refusal is one verified publish refusal read back from a registry.
type Refusal struct {
	// ID is the refusal's id: the sha256 of its signed payload.
	ID string `json:"id"`
	// Manifest is the digest of the referrer manifest that carried it.
	Manifest  string                    `json:"manifest"`
	Statement delivery.RefusalStatement `json:"statement"`
}

// RefusalListing is what a service's refusals read back as, newest first.
// Unreadable names every refusal referrer that could not be verified, with why.
type RefusalListing struct {
	Refusals   []Refusal `json:"refusals"`
	Unreadable []string  `json:"unreadable,omitempty"`
}

// ListRefusals reads the newest MaxRefusals refusals of service from repo and
// verifies each with verify. A refusal on the anchor that names another
// service is unreadable. A listing or fetch failure is an error: the registry
// could not be read, which is never the same as no refusal.
// ponytail: the newest are picked by the unsigned AnnAt annotation before
// anything is fetched; a registry that drops descriptor annotations gives an
// arbitrary MaxRefusals of them. Page through by time if that ever matters.
func ListRefusals(ctx context.Context, repo *remote.Repository, service string, verify func([]byte) error) (RefusalListing, error) {
	var descs []ocispec.Descriptor
	if err := repo.Referrers(ctx, RefusalAnchor(service).ManifestDesc, "", func(page []ocispec.Descriptor) error {
		descs = append(descs, page...)
		return nil
	}); err != nil {
		return RefusalListing{}, err
	}
	slices.SortStableFunc(descs, func(a, b ocispec.Descriptor) int { return cmp.Compare(b.Annotations[AnnAt], a.Annotations[AnnAt]) })
	out := RefusalListing{Refusals: []Refusal{}}
	seen := map[string]bool{}
	for _, d := range descs[:min(len(descs), MaxRefusals)] {
		bundle, bad, err := fetchBundle(ctx, repo, d, delivery.PredicatePublishRefusal)
		switch {
		case err != nil:
			return RefusalListing{}, err
		case bad != "":
			out.Unreadable = append(out.Unreadable, bad)
			continue
		case bundle == nil:
			continue // another tool's referrer
		}
		st, id, err := delivery.OpenRefusal(bundle, verify)
		switch {
		case err != nil:
			out.Unreadable = append(out.Unreadable, d.Digest.String()+": "+err.Error())
		case st.Predicate.Service != service:
			out.Unreadable = append(out.Unreadable, fmt.Sprintf("%s: names service %q, read from the refusals of %q", d.Digest, st.Predicate.Service, service))
		case !seen[id]:
			seen[id] = true
			out.Refusals = append(out.Refusals, Refusal{ID: id, Manifest: d.Digest.String(), Statement: st})
		}
	}
	slices.SortStableFunc(out.Refusals, func(a, b Refusal) int { return cmp.Compare(b.Statement.Predicate.At, a.Statement.Predicate.At) })
	return out, nil
}
