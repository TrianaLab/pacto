package app

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// MaxListedDrafts bounds the drafts a plan lists; DraftsTotal counts them all.
const MaxListedDrafts = 20

// draftUnreadable is the state of a draft whose bundle or record could not
// be read: listed with its reason, never dropped.
const draftUnreadable = "unreadable"

// DraftView is one draft-<n> tag of the bundle repository: its digest, the
// state its record names (unreadable, with the reason, when it cannot be
// read), who asked for it and for whom, the ranges it requests and whether
// a signature of it verifies.
type DraftView struct {
	Tag        string                  `json:"tag"`
	Digest     string                  `json:"digest,omitempty"`
	State      string                  `json:"state"`
	Asker      contract.InferredSigner `json:"asker"`
	OnBehalfOf string                  `json:"onBehalfOf,omitempty"`
	// Requested are the ranges the draft requests, as "<dependency> <range>".
	Requested []string `json:"requested,omitempty"`
	Signed    bool     `json:"signed"`
	Reason    string   `json:"reason,omitempty"`
	// From is the workload the draft was inferred from.
	From Declared `json:"from,omitzero"`
}

// drafts lists the bundle repository's draft tags (from the tags
// findBaseline read), newest first and at most MaxListedDrafts, each pulled
// by digest and its record read, with the total count.
func (pl *declarePlanner) drafts(ctx context.Context) ([]DraftView, int) {
	type numbered struct {
		tag string
		n   int
	}
	var all []numbered
	for _, t := range pl.tags {
		if n, ok := publish.DraftNumber(t, publish.DraftPrefix); ok {
			all = append(all, numbered{t, n})
		}
	}
	if len(all) == 0 {
		return nil, 0
	}
	slices.SortFunc(all, func(a, b numbered) int { return cmp.Compare(b.n, a.n) })
	tm, err := pl.trustedRoot()
	out := []DraftView{}
	for _, d := range all[:min(len(all), MaxListedDrafts)] {
		out = append(out, pl.draftView(ctx, d.tag, tm, err))
	}
	return out, len(all)
}

// draftView reads one draft tag: an unreadable tag, bundle or record is
// listed as unreadable with why.
func (pl *declarePlanner) draftView(ctx context.Context, tag string, tm root.TrustedMaterial, rootErr error) DraftView {
	v := DraftView{Tag: tag, State: draftUnreadable}
	r, err := pl.reg.Head(ctx, pl.repo()+":"+tag)
	if err != nil || r == nil {
		v.Reason = "the tag could not be read"
		if err != nil {
			v.Reason += ": " + err.Error()
		}
		return v
	}
	v.Digest = r.Digest
	b, err := pl.reg.Pull(ctx, pl.repo()+"@"+r.Digest)
	var rec *contract.InferredRecord
	if err == nil {
		rec, err = contract.ReadInferredRecord(b.FS)
	}
	if err != nil || rec == nil || rec.Declaration == nil {
		v.Reason = "it holds no readable declaration"
		if err != nil {
			v.Reason += ": " + err.Error()
		}
		return v
	}
	dec := rec.Declaration
	v.State, v.Asker, v.OnBehalfOf = dec.State, dec.Asker, dec.OnBehalfOf
	v.From = Declared{InferredFrom: dec.InferredFrom, Cluster: dec.Cluster}
	for _, a := range dec.Requested {
		name := strings.TrimSuffix(strings.TrimPrefix(a.Field, "dependencies["), "].compatibility")
		v.Requested = append(v.Requested, name+" "+a.Range)
	}
	v.Signed, v.Reason = pl.draftSigned(ctx, r.Digest, tm, rootErr)
	return v
}

// draftSigned is whether a signature of the draft repo@digest verifies under
// tm, and why that is unknown when it cannot be checked.
func (pl *declarePlanner) draftSigned(ctx context.Context, digest string, tm root.TrustedMaterial, err error) (bool, string) {
	var signers []delivery.PublisherIdentity
	if err == nil {
		signers, err = publish.SignersOf(ctx, pl.reg, tm, pl.req.Signing.RekorURL != "", pl.repo(), digest)
	}
	if len(signers) == 0 && err != nil && !errors.Is(err, publish.ErrNoSignature) {
		return false, "its signature could not be checked: " + err.Error()
	}
	return len(signers) > 0, ""
}

// trustedRoot is the root findBaseline read, else the request's.
func (pl *declarePlanner) trustedRoot() (root.TrustedMaterial, error) {
	if pl.root != nil {
		return pl.root, nil
	}
	return declareRoot(pl.req.Signing.TrustedRoot)
}
