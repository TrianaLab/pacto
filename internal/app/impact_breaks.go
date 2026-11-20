package app

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/graph"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// breakInput is what the new revision announces in breaks[], by consumer
// name, and, when opts reads them, a reader of the acceptances attached to
// each consumer's revisions; nil when it announces nothing.
func (s *Service) breakInput(ctx context.Context, opts ImpactOptions, newBundle *contract.Bundle, snap *fleet.FleetSnapshot) *impact.BreakInput {
	c := newBundle.Contract
	if len(c.Breaks) == 0 {
		return nil
	}
	rec, _ := contract.ReadInferredRecord(newBundle.FS) // an unreadable record proposes no expiry
	epoch := s.checkoutExpiry(ctx, opts)
	in := &impact.BreakInput{Announced: map[string]impact.BreakAnnouncement{}}
	for i, b := range c.Breaks {
		ann := impact.BreakAnnouncement{Reason: b.Reason, Announcement: delivery.BreakAnnouncement(b), Expires: b.Expires, ExpiresSource: "declared"}
		switch {
		case b.Expires != "":
		case recordedExpiry(rec, i) != "":
			ann.Expires, ann.ExpiresSource = recordedExpiry(rec, i), "pacto.inferred.yaml"
		default:
			ann.Expires, ann.ExpiresSource = epoch()
		}
		for _, name := range b.Consumers {
			in.Announced[name] = ann
		}
	}
	if opts.Acceptances {
		r := &acceptanceReader{s: s, snap: snap, in: in, store: newAcceptanceStore(ctx, s.env(), impactTrust(opts))}
		in.Acceptances = func(k fleet.ServiceKey) ([]impact.Acceptance, bool) { return r.read(ctx, k) }
	}
	return in
}

// impactAcceptances reads the acceptances of a consumer for the answer's
// range coverage: the break reader when there is one, else a reader with no
// announcement; nil when opts reads none.
func (s *Service) impactAcceptances(ctx context.Context, opts ImpactOptions, snap *fleet.FleetSnapshot, breaks *impact.BreakInput) func(fleet.ServiceKey) ([]impact.Acceptance, bool) {
	switch {
	case breaks != nil:
		return breaks.Acceptances
	case !opts.Acceptances:
		return nil
	}
	r := &acceptanceReader{s: s, snap: snap, in: &impact.BreakInput{}, store: newAcceptanceStore(ctx, s.env(), impactTrust(opts))}
	return func(k fleet.ServiceKey) ([]impact.Acceptance, bool) { return r.read(ctx, k) }
}

// checkoutExpiry is the default expiry of an undated break when the new
// revision is a checkout: HEAD's commit time + 30 days, as pacto ci records
// it. Anything else has none, which never accepts.
func (s *Service) checkoutExpiry(ctx context.Context, opts ImpactOptions) func() (string, string) {
	return func() (string, string) {
		ref := graph.ParseDependencyRef(opts.NewPath)
		if !ref.IsLocal() {
			return "", ""
		}
		epoch, _ := strconv.ParseInt(commitEpoch(ctx, opts.Getenv, s.env().run, ref.Location), 10, 64)
		return discover.BreakExpiry(epoch, s.localInputs(ref.Location, "").Commit)
	}
}

// impactTrust is how acceptances are verified: opts.Signers, else the
// installation trust the fleet's provenance checker reads, else none.
func impactTrust(opts ImpactOptions) func(context.Context) (Installation, error) {
	switch {
	case opts.Signers != nil:
		p := opts.Signers
		return func(context.Context) (Installation, error) { return Installation{Signers: p.Signers}, nil }
	case opts.Fleet.Provenance != nil:
		return opts.Fleet.Provenance.Trust
	}
	return nil
}

// acceptanceReader reads the acceptances attached to a consumer's lookup
// set: the newest published version of each of its bundle repositories and
// every revision of it running on a target, pinned by digest.
type acceptanceReader struct {
	s     *Service
	snap  *fleet.FleetSnapshot
	in    *impact.BreakInput
	store *acceptanceStore
}

// acceptanceSubject is one revision whose acceptances are read.
type acceptanceSubject struct {
	repo, subject, version string
	c                      *contract.Contract
	rec                    *contract.InferredRecord
}

// read judges every acceptance attached to consumer's lookup set; complete
// is false when a listing failed or reached its bound without a counted
// record.
func (r *acceptanceReader) read(ctx context.Context, consumer fleet.ServiceKey) ([]impact.Acceptance, bool) {
	_, name := fleet.ParseServiceKey(consumer)
	ann := r.in.Announced[name]
	subjects, complete := r.subjects(ctx, consumer)
	var out []impact.Acceptance
	expires := func(delivery.AcceptedBreak) string { return ann.Expires }
	for _, sub := range subjects {
		service := string(fleet.NewServiceKeyDomain(contract.OCIDomain(sub.repo), sub.c.Service.Name))
		judged, notes, err := r.store.readAcceptances(ctx, acceptanceQuery{Subject: sub.subject, Repo: sub.repo,
			Service: service, Version: sub.version, Owner: declaredOwner(sub.c, sub.rec), Expires: expires,
			Wants: func(a impact.Acceptance) bool { return a.Break != nil && a.Break.Announcement == ann.Announcement }})
		if err != nil || slices.ContainsFunc(notes, func(n string) bool { return strings.HasPrefix(n, AcceptancesTruncated) }) {
			complete = false
		}
		cited, ok := r.cited(ctx, sub, service, expires)
		complete = complete && ok
		for _, j := range append(judged, cited...) {
			if !slices.ContainsFunc(out, func(a impact.Acceptance) bool { return a.Record == j.Ref }) {
				out = append(out, j.ForImpact())
			}
		}
	}
	return out, complete
}

// cited re-reads and re-judges the acceptances sub's record cites
// (carried by pacto ci from an earlier version); ok is false when one
// could not be read. A record that no longer counts as cited is left out.
func (r *acceptanceReader) cited(ctx context.Context, sub acceptanceSubject, service string, expires func(delivery.AcceptedBreak) string) ([]delivery.JudgedAcceptance, bool) {
	if sub.rec == nil {
		return nil, true
	}
	var out []delivery.JudgedAcceptance
	ok := true
	for _, c := range sub.rec.Inputs.Acceptances {
		j, why, err := r.store.readCited(ctx, c.Record, hostOf(sub.repo), service, expires)
		switch {
		case err != nil:
			ok = false
		case why == "":
			out = append(out, j)
		}
	}
	return out, ok
}

// subjects is consumer's lookup set; complete is false when a repository's
// newest version could not be read.
func (r *acceptanceReader) subjects(ctx context.Context, consumer fleet.ServiceKey) ([]acceptanceSubject, bool) {
	domain, name := fleet.ParseServiceKey(consumer)
	var out []acceptanceSubject
	seen := map[string]bool{}
	add := func(sub acceptanceSubject) {
		if !seen[sub.subject] {
			seen[sub.subject] = true
			out = append(out, sub)
		}
	}
	running := map[fleet.RevisionKey]bool{}
	for _, t := range r.snap.Targets {
		if t.ServiceKey == consumer {
			running[t.ContractRevision] = true
		}
	}
	var repos []string
	for _, k := range slices.Sorted(maps.Keys(r.snap.Revisions)) {
		rev := r.snap.Revisions[k]
		if rev.ServiceKey != consumer || rev.Contract == nil {
			continue
		}
		repos = append(repos, revisionRepos(rev)...)
		if pinned := contract.TrimOCIScheme(rev.ResolvedRef); running[k] && strings.Contains(pinned, "@sha256:") {
			repo, _, _ := strings.Cut(pinned, "@")
			add(acceptanceSubject{repo: repo, subject: pinned, version: rev.Version, c: rev.Contract, rec: rev.Record})
		}
	}
	if len(repos) == 0 && domain != "" {
		repos = []string{domain + "/" + name}
	}
	complete := true
	reg := r.s.env().publishRegistry()
	for _, repo := range slices.Compact(slices.Sorted(slices.Values(repos))) {
		version, dgst, b, err := newestPublished(ctx, reg, repo, AcceptOptions{})
		if err != nil {
			complete = complete && isNotPublished(err)
			continue
		}
		rec, _ := contract.ReadInferredRecord(b.FS) // an unreadable record makes the owner declared as written
		add(acceptanceSubject{repo: repo, subject: repo + "@" + dgst, version: version, c: b.Contract, rec: rec})
	}
	return out, complete
}

// revisionRepos are the bundle repositories a revision names: its publish
// section's and the one it was resolved from.
func revisionRepos(rev *fleet.ContractRevision) []string {
	var out []string
	if p := rev.Contract.Publish; p != nil && p.Bundle != "" {
		out = append(out, repoOf(p.Bundle))
	}
	if ref := cmp.Or(rev.ResolvedRef, rev.RequestedRef); strings.HasPrefix(ref, contract.OCIScheme) {
		out = append(out, repoOf(ref))
	}
	return out
}

// isNotPublished reports whether err says a repository holds no version:
// nothing to read, not a failed read.
func isNotPublished(err error) bool {
	var de *DeclareError
	return errors.As(err, &de) && de.Code == codeNotPublished
}
