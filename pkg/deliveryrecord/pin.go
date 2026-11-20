package deliveryrecord

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// Identity pins live on one stream per service ([delivery.PinStream]) in the
// recorder's streams repository, written like approval records: a referrer
// of the stream's anchor, or a write-once [KindTag] where the registry serves
// no Referrers API. Anyone who reads the registry reads them; no cluster is
// asked.

// MaxPins bounds how many pins of a service ListPins reads.
const MaxPins = 20

// PushPin writes bundle, a signed identity pin (delivery.SignPin), on its
// service's pin stream in repo in mode, and returns where it is. The stream's
// anchor must exist: the recorder anchors it like any stream.
func PushPin(ctx context.Context, repo *remote.Repository, mode capabilities.ReferrersMode, bundle []byte) (string, error) {
	st, _, err := delivery.OpenPin(bundle, func([]byte) error { return nil }) // its own signer made it
	if err != nil {
		return "", err
	}
	return Push(ctx, repo, delivery.PinStream(st.Predicate.Service), mode, delivery.PredicateIdentityPin, bundle, nil)
}

// Pin is one identity pin read back and verified.
type Pin struct {
	// ID is the pin's id: the sha256 of its signed payload.
	ID string `json:"id"`
	// Manifest is the digest of the manifest that carried it.
	Manifest string               `json:"manifest"`
	Pin      delivery.IdentityPin `json:"pin"`
}

// PinListing is a service's pins, newest first by signed time. Unreadable
// names every pin that could not be used, with why; Cut says more pins were
// attached than MaxPins; Incomplete says the approval record of a pin could
// not be read, so it may be usable on a later read.
type PinListing struct {
	Pins       []Pin    `json:"pins"`
	Unreadable []string `json:"unreadable,omitempty"`
	Cut        bool     `json:"cut,omitempty"`
	Incomplete bool     `json:"incomplete,omitempty"`
}

// ApprovalCheck reads the approval record at ref (<repository>@<manifest
// digest>), verifies it and returns it with the person who decided.
type ApprovalCheck func(ctx context.Context, ref string) (st delivery.ApprovalStatement, approver string, err error)

// PinApproval is the ApprovalCheck pins are read with: it fetches the
// approval record a pin cites through open and names who decided. A record
// a person signed is verified by person, which also refuses one that only
// proposes; one the recorder signed is verified by verify, the pin's own
// keys, and decides for the person it was signed on behalf of.
func PinApproval(open func(path string) (*remote.Repository, error), person func(bundle []byte) (delivery.ApprovalStatement, string, error), verify func([]byte) error) ApprovalCheck {
	return func(ctx context.Context, ref string) (delivery.ApprovalStatement, string, error) {
		path, manifest, _ := strings.Cut(ref, "@")
		repo, err := open(path)
		var bundle []byte
		if err == nil {
			bundle, err = Fetch(ctx, repo, manifest, delivery.PredicateApproval)
		}
		if err != nil {
			return delivery.ApprovalStatement{}, "", err
		}
		st, _, err := delivery.OpenApproval(bundle, func([]byte) error { return nil }) // Fetch read one
		if err == nil && st.Predicate.Signer.Kind == delivery.SignerPerson {
			return person(bundle)
		}
		if _, _, err = delivery.OpenApproval(bundle, verify); err != nil {
			return delivery.ApprovalStatement{}, "", err
		}
		if st.Predicate.Signer.OnBehalfOf == nil {
			return delivery.ApprovalStatement{}, "", errors.New("the recorder signed it for nobody")
		}
		return st, st.Predicate.Signer.OnBehalfOf.User, nil
	}
}

// ListPins reads the newest MaxPins pins of svc from repo that the Stages
// of namespace ns use (every namespace when ns is empty). A pin is used only
// when verify accepts its signature, it names svc and the approval record it
// cites verifies through approval, approves the trust operation of the same
// identity it names in the namespace it names and was given by the approver
// it names; any other pin is unreadable, never used, and once it is known
// not to verify it never takes the bound again. A pin of another namespace
// is simply not this namespace's. A listing or fetch failure is an error: the
// registry could not be read, which is never the same as no pin.
// ponytail: the trust operation is matched by its identity-derived name
// (40 bits of sha256); reading its intent record for the exact params is the
// upgrade path.
func (r *Reader) ListPins(ctx context.Context, repo *remote.Repository, ns string, svc delivery.ServiceRef, verify func([]byte) error, approval ApprovalCheck) (PinListing, error) {
	stream := delivery.PinStream(svc)
	fetched, cut, err := r.ListPredicate(ctx, repo, stream, delivery.PredicateIdentityPin, "", MaxPins)
	if err != nil {
		return PinListing{}, err
	}
	out := PinListing{Pins: []Pin{}, Cut: cut}
	for _, f := range fetched {
		p, id, why, reject := usablePin(ctx, f.Bundle, svc, verify, approval)
		switch {
		case why != "":
			if reject {
				r.Reject(stream, delivery.PredicateIdentityPin, f.Manifest)
			}
			out.Incomplete = out.Incomplete || !reject
			out.Unreadable = append(out.Unreadable, f.Manifest+": "+why)
		case ns == "" || p.Namespace == ns:
			out.Pins = append(out.Pins, Pin{ID: id, Manifest: f.Manifest, Pin: p})
		}
	}
	slices.SortStableFunc(out.Pins, func(a, b Pin) int { return cmp.Compare(b.Pin.At, a.Pin.At) })
	return out, nil
}

// usablePin opens one pin of svc and checks the approval it cites, or says
// why it cannot be used and whether that will never change (reject): every
// reason but an approval record that could not be read.
func usablePin(ctx context.Context, bundle []byte, svc delivery.ServiceRef, verify func([]byte) error, approval ApprovalCheck) (p delivery.IdentityPin, id, why string, reject bool) {
	st, id, err := delivery.OpenPin(bundle, verify)
	if err != nil {
		return delivery.IdentityPin{}, "", err.Error(), true
	}
	p = st.Predicate
	if p.Service != svc {
		return p, "", fmt.Sprintf("names service %q, read from the pins of %q", p.Service.Name, svc.Name), true
	}
	a, approver, err := approval(ctx, p.Approval)
	if err != nil {
		return p, "", "the approval record " + p.Approval + " it cites does not verify: " + err.Error(), false
	}
	op := a.Predicate.Operation
	// The Stage's name prefixes it: TrustOperationName("", id) is the suffix.
	trust := delivery.TrustOperationName("", delivery.PublisherIdentity{Issuer: p.Issuer, Subject: p.Subject, Repository: p.Repository})
	switch {
	case a.Predicate.Decision != delivery.Approve:
		why = "the approval record it cites decides " + string(a.Predicate.Decision) + ", not approve"
	case op.UID != p.Operation:
		why = "the approval record it cites answers operation " + op.UID + ", not " + p.Operation
	case !strings.HasSuffix(op.Name, trust):
		why = "the approval record it cites answers " + op.Name + ", not the trust operation of " + p.Subject
	case p.Namespace == "" || op.Stage.Namespace != p.Namespace:
		why = fmt.Sprintf("the approval record it cites answers an operation in namespace %q, not %q", op.Stage.Namespace, p.Namespace)
	case approver != p.Approver:
		why = "the approval record it cites was given by " + approver + ", not " + p.Approver
	default:
		return p, id, "", false
	}
	return p, "", why, true
}
