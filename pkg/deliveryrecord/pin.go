package deliveryrecord

import (
	"cmp"
	"context"
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
// attached than MaxPins.
type PinListing struct {
	Pins       []Pin    `json:"pins"`
	Unreadable []string `json:"unreadable,omitempty"`
	Cut        bool     `json:"cut,omitempty"`
}

// ApprovalCheck reads the approval record at ref (<repository>@<manifest
// digest>), verifies it and returns it with the person who decided.
type ApprovalCheck func(ctx context.Context, ref string) (st delivery.ApprovalStatement, approver string, err error)

// ListPins reads the newest MaxPins pins of svc from repo. A pin is used
// only when verify accepts its signature, it names svc and the approval
// record it cites verifies through approval, approves the trust operation of
// the same identity it names and was given by the approver it names; any
// other pin is unreadable, never used. A listing or fetch failure is an
// error: the registry could not be read, which is never the same as no pin.
// ponytail: the trust operation is matched by its identity-derived name
// (40 bits of sha256); reading its intent record for the exact params is the
// upgrade path.
func (r *Reader) ListPins(ctx context.Context, repo *remote.Repository, svc delivery.ServiceRef, verify func([]byte) error, approval ApprovalCheck) (PinListing, error) {
	stream := delivery.PinStream(svc)
	fetched, cut, err := r.ListPredicate(ctx, repo, stream, delivery.PredicateIdentityPin, "", MaxPins)
	if err != nil {
		return PinListing{}, err
	}
	out := PinListing{Pins: []Pin{}, Cut: cut}
	for _, f := range fetched {
		p, id, why := usablePin(ctx, f.Bundle, svc, verify, approval)
		if why != "" {
			out.Unreadable = append(out.Unreadable, f.Manifest+": "+why)
			continue
		}
		out.Pins = append(out.Pins, Pin{ID: id, Manifest: f.Manifest, Pin: p})
	}
	slices.SortStableFunc(out.Pins, func(a, b Pin) int { return cmp.Compare(b.Pin.At, a.Pin.At) })
	return out, nil
}

// usablePin opens one pin of svc and checks the approval it cites, or says
// why it cannot be used.
func usablePin(ctx context.Context, bundle []byte, svc delivery.ServiceRef, verify func([]byte) error, approval ApprovalCheck) (delivery.IdentityPin, string, string) {
	st, id, err := delivery.OpenPin(bundle, verify)
	if err != nil {
		return delivery.IdentityPin{}, "", err.Error()
	}
	p := st.Predicate
	if p.Service != svc {
		return p, "", fmt.Sprintf("names service %q, read from the pins of %q", p.Service.Name, svc.Name)
	}
	a, approver, err := approval(ctx, p.Approval)
	if err != nil {
		return p, "", "the approval record " + p.Approval + " it cites does not verify: " + err.Error()
	}
	op := a.Predicate.Operation
	// The Stage's name prefixes it: TrustOperationName("", id) is the suffix.
	trust := delivery.TrustOperationName("", delivery.PublisherIdentity{Issuer: p.Issuer, Subject: p.Subject, Repository: p.Repository})
	switch {
	case a.Predicate.Decision != delivery.Approve:
		return p, "", "the approval record it cites decides " + string(a.Predicate.Decision) + ", not approve"
	case op.UID != p.Operation:
		return p, "", "the approval record it cites answers operation " + op.UID + ", not " + p.Operation
	case !strings.HasSuffix(op.Name, trust):
		return p, "", "the approval record it cites answers " + op.Name + ", not the trust operation of " + p.Subject
	case approver != p.Approver:
		return p, "", "the approval record it cites was given by " + approver + ", not " + p.Approver
	}
	return p, id, ""
}
