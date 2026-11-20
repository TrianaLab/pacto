package app

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sigstore/sigstore-go/pkg/root"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/semver"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// acceptanceReadTimeout bounds reading the acceptances of one subject.
var acceptanceReadTimeout = 15 * time.Second

const (
	// maxAcceptanceManifest is the largest acceptance manifest a reader
	// fetches; the record layer is bounded by delivery.MaxAcceptanceBytes.
	maxAcceptanceManifest = 4 << 10
	// AcceptancesTruncated says the bound was reached before a counted
	// record was found: the answer reads unverified, never accepted.
	AcceptancesTruncated = "ACCEPTANCES_TRUNCATED"
	publicGoodNote       = "no installation signer policy was read; acceptances judged with public-good issuers"
)

// acceptanceStore reads the acceptance records attached to a service's
// bundle in its own repository, verified with the installation signer
// policy (else public-good Sigstore) and judged by delivery.JudgeAcceptance.
// listings remembers each subject's attached listing for one run.
type acceptanceStore struct {
	open     func(path string) (*remote.Repository, error)
	pull     func() publish.Registry
	verify   func(ctx context.Context, raw []byte) (delivery.PublisherIdentity, time.Time, error)
	rules    []delivery.SignerClassRule
	note     string
	now      func() time.Time
	listings map[string][]acceptanceCandidate
}

// newAcceptanceStore builds the store over env's registry credentials and
// the installation signer policy trust reads; with none read, records are
// verified under public-good Sigstore and the store's note says so.
// ponytail: signers are classified by the default rule, as signed approvals
// are; the verified policy's class rules are the upgrade path.
func newAcceptanceStore(ctx context.Context, env sysDeps, trust func(context.Context) (Installation, error)) *acceptanceStore {
	st := &acceptanceStore{open: env.refusalRepo, pull: env.publishRegistry, now: env.now, listings: map[string][]acceptanceCandidate{}}
	var signers []delivery.PersonSigner
	if trust != nil {
		if in, err := trust(ctx); err == nil {
			signers = in.Signers
		}
	}
	if len(signers) > 0 {
		st.verify = policyVerifier(signers)
		return st
	}
	st.note, st.verify = publicGoodNote, publicGoodVerifier()
	return st
}

// policyVerifier verifies a record under each installation signer in turn.
func policyVerifier(signers []delivery.PersonSigner) func(context.Context, []byte) (delivery.PublisherIdentity, time.Time, error) {
	return func(_ context.Context, raw []byte) (delivery.PublisherIdentity, time.Time, error) {
		var errs []error
		for _, sg := range signers {
			id, at, err := signing.VerifyPerson(raw, sg.Issuer, sg.TrustedRoot)
			if err == nil {
				return id, at, nil
			}
			errs = append(errs, err)
		}
		return delivery.PublisherIdentity{}, time.Time{}, errors.Join(errs...)
	}
}

// publicGoodVerifier verifies a record under public-good Sigstore's root,
// fetched on the first record.
func publicGoodVerifier() func(context.Context, []byte) (delivery.PublisherIdentity, time.Time, error) {
	var tm root.TrustedMaterial
	return func(_ context.Context, raw []byte) (delivery.PublisherIdentity, time.Time, error) {
		if tm == nil {
			got, err := publicGoodRoot(signing.PublicTUF)
			if err != nil {
				return delivery.PublisherIdentity{}, time.Time{}, fmt.Errorf("public-good Sigstore's trusted root: %w", err)
			}
			tm = got
		}
		return signing.VerifyKeyless(raw, tm)
	}
}

// acceptanceQuery is what readAcceptances reads: the acceptances attached
// to Subject (<repository>@sha256:<hex>, of Service at Version, owned by
// Owner as that bundle declares) in Repo, on Subject's host only. Expires
// is the expiry of a break a record accepts, "" when unknown; Wants stops
// the read at the first counted record it accepts.
type acceptanceQuery struct {
	Subject, Repo    string
	Service, Version string
	Owner            contract.Owner
	Expires          func(delivery.AcceptedBreak) string
	Wants            func(impact.Acceptance) bool
}

// acceptanceCandidate is one listed record: a referrer manifest, or a
// write-once tag resolved when it is read.
type acceptanceCandidate struct {
	name string
	desc ocispec.Descriptor
	tag  string
}

// readAcceptances reads at most delivery.MaxAcceptances records attached
// to q.Subject and judges each. Referrers come first in listing order, then
// tags sorted by name. A record that does not verify or belongs to another
// subject, service or version is ignored, with a note, and never listed.
// The read stops at the first counted record q.Wants accepts; reaching the
// bound without one adds the AcceptancesTruncated note.
func (st *acceptanceStore) readAcceptances(ctx context.Context, q acceptanceQuery) ([]delivery.JudgedAcceptance, []string, error) {
	subjRepo, dgst, _ := strings.Cut(q.Subject, "@")
	if hostOf(q.Repo) != hostOf(subjRepo) {
		return nil, nil, fmt.Errorf("the acceptances of %s are read only on %s, never on %s", q.Subject, hostOf(subjRepo), hostOf(q.Repo))
	}
	ctx, cancel := context.WithTimeout(ctx, acceptanceReadTimeout)
	defer cancel()
	repo, err := st.open(q.Repo)
	if err != nil {
		return nil, nil, err
	}
	cands, err := st.candidates(ctx, repo, q.Repo, dgst)
	if err != nil {
		return nil, nil, fmt.Errorf("listing the acceptances of %s: %w", q.Subject, err)
	}
	var notes []string
	if st.note != "" {
		notes = append(notes, st.note)
	}
	var out []delivery.JudgedAcceptance
	seen, read := map[digest.Digest]bool{}, 0
	for _, c := range cands {
		d, why, err := resolveCandidate(ctx, repo, c)
		if err != nil {
			return nil, nil, fmt.Errorf("reading the acceptance %s: %w", c.name, err)
		}
		if why == "" && seen[d.Digest] {
			continue // a record that is both a referrer and a tag
		}
		if read++; read > delivery.MaxAcceptances {
			notes = append(notes, fmt.Sprintf("%s: more than %d acceptance records are attached to %s and none read counts, so it reads unverified",
				AcceptancesTruncated, delivery.MaxAcceptances, q.Subject))
			break
		}
		seen[d.Digest] = why == ""
		j, why, err := st.judgeOne(ctx, repo, d, why, q, dgst)
		if err != nil {
			return nil, nil, fmt.Errorf("reading the acceptance %s: %w", c.name, err)
		}
		if why != "" {
			notes = append(notes, "ignored acceptance "+c.name+": "+why)
			continue
		}
		out = append(out, j)
		if j.State == impact.AcceptCounted && q.Wants(j.ForImpact()) {
			break
		}
	}
	return out, notes, nil
}

// candidates lists what is attached to repo@dgst once per run: acceptance
// referrers and the write-once acceptance tags of that subject, sorted.
func (st *acceptanceStore) candidates(ctx context.Context, repo *remote.Repository, path, dgst string) ([]acceptanceCandidate, error) {
	key := path + "@" + dgst
	if c, ok := st.listings[key]; ok {
		return c, nil
	}
	subject, err := repo.Resolve(ctx, dgst)
	if err != nil {
		return nil, err
	}
	descs, tags, err := ocireferrer.Attached(ctx, repo, subject, delivery.AcceptanceTagPrefix+subject.Digest.Encoded()+"-")
	if err != nil {
		return nil, err
	}
	var out []acceptanceCandidate
	for _, d := range descs {
		// A registry that ignores a manifest's artifactType lists the config
		// media type, the empty JSON for every ocireferrer artifact: the
		// manifest read says what it is.
		if d.ArtifactType == delivery.AcceptanceArtifactType || d.ArtifactType == ocispec.MediaTypeEmptyJSON {
			out = append(out, acceptanceCandidate{name: d.Digest.String(), desc: d})
		}
	}
	slices.Sort(tags)
	for _, t := range tags {
		out = append(out, acceptanceCandidate{name: t, tag: t})
	}
	st.listings[key] = out
	return out, nil
}

// judgeOne reads and judges the record manifest d: the judged record, or
// why it is ignored (resolved, when resolving its tag already said why), or
// a registry failure.
func (st *acceptanceStore) judgeOne(ctx context.Context, repo *remote.Repository, d ocispec.Descriptor, resolved string, q acceptanceQuery, dgst string) (delivery.JudgedAcceptance, string, error) {
	if resolved != "" {
		return delivery.JudgedAcceptance{}, resolved, nil
	}
	layer, _, why, err := acceptanceLayer(ctx, repo, d, dgst)
	if why != "" || err != nil {
		return delivery.JudgedAcceptance{}, why, err
	}
	bundle, err := content.FetchAll(ctx, repo, layer)
	if err != nil {
		return delivery.JudgedAcceptance{}, "", err
	}
	var signer delivery.PublisherIdentity
	var at time.Time
	stmt, id, err := delivery.OpenAcceptance(bundle, func(b []byte) (err error) {
		signer, at, err = st.verify(ctx, b)
		return err
	})
	if err != nil {
		return delivery.JudgedAcceptance{}, "it does not verify: " + err.Error(), nil
	}
	if why := foreignAcceptance(stmt, q, dgst); why != "" {
		return delivery.JudgedAcceptance{}, why, nil
	}
	expires := ""
	if b := stmt.Predicate.Break; b != nil && q.Expires != nil {
		expires = q.Expires(*b)
	}
	state, reason := delivery.JudgeAcceptance(stmt, signer, st.rules, q.Owner, expires, st.now())
	return delivery.JudgedAcceptance{Record: stmt.Predicate, ID: id, Ref: q.Repo + "@" + d.Digest.String(), Signer: signer.Subject,
		Issuer: signer.Issuer, State: state, Reason: reason, SignedAt: at}, "", nil
}

// resolveCandidate is the manifest descriptor of c: a referrer's own, or
// what its tag names when the tag's suffix is that manifest's 12 hex.
func resolveCandidate(ctx context.Context, repo *remote.Repository, c acceptanceCandidate) (ocispec.Descriptor, string, error) {
	if c.tag == "" {
		return c.desc, "", nil
	}
	d, err := repo.Resolve(ctx, c.tag)
	switch {
	case errors.Is(err, errdef.ErrNotFound):
		return d, "the tag was listed and is gone", nil
	case err != nil:
		return d, "", err
	}
	if suffix := c.tag[strings.LastIndex(c.tag, "-")+1:]; len(suffix) != 12 || !strings.HasPrefix(d.Digest.Encoded(), suffix) {
		return d, "the tag does not name the manifest it points at", nil
	}
	return d, "", nil
}

// acceptanceLayer reads the manifest d, at most maxAcceptanceManifest
// bytes, and returns its one record layer and its subject when it is an
// acceptance (of dgst, unless dgst is "") of at most
// delivery.MaxAcceptanceBytes. Sizes are checked on each descriptor before
// anything is fetched.
func acceptanceLayer(ctx context.Context, repo *remote.Repository, d ocispec.Descriptor, dgst string) (ocispec.Descriptor, string, string, error) {
	if d.Size > maxAcceptanceManifest {
		return ocispec.Descriptor{}, "", fmt.Sprintf("its manifest is larger than %d bytes", maxAcceptanceManifest), nil
	}
	raw, err := content.FetchAll(ctx, repo, d)
	if err != nil {
		return ocispec.Descriptor{}, "", "", err
	}
	var m ocispec.Manifest
	_ = json.Unmarshal(raw, &m) // a manifest that does not decode is no acceptance
	switch {
	case m.ArtifactType != delivery.AcceptanceArtifactType || len(m.Layers) != 1:
		return ocispec.Descriptor{}, "", "it is no acceptance record", nil
	case m.Subject == nil || dgst != "" && m.Subject.Digest != digest.Digest(dgst):
		return ocispec.Descriptor{}, "", "it is attached to another subject", nil
	case m.Layers[0].Size > delivery.MaxAcceptanceBytes:
		return ocispec.Descriptor{}, "", fmt.Sprintf("its record is larger than %d bytes", delivery.MaxAcceptanceBytes), nil
	}
	return m.Layers[0], m.Subject.Digest.String(), "", nil
}

// readCited reads the acceptance record a bundle cites
// (<repository>@sha256:<hex>) by digest, on host only, and judges it
// against the bundle it is attached to, which must be service's; expires
// is a break's expiry, as acceptanceQuery.Expires. It returns why the
// record no longer counts as cited (gone, elsewhere, another service's or
// not verifying) or a registry failure.
//
// ponytail: the manifest is fetched twice, here for its subject and in
// judgeOne; both reads are bounded at maxAcceptanceManifest.
func (st *acceptanceStore) readCited(ctx context.Context, ref, host, service string, expires func(delivery.AcceptedBreak) string) (delivery.JudgedAcceptance, string, error) {
	path, dgst, _ := strings.Cut(ref, "@")
	if _, err := digest.Parse(dgst); err != nil {
		return delivery.JudgedAcceptance{}, "it names no manifest digest", nil
	}
	if h := hostOf(path); h != host {
		return delivery.JudgedAcceptance{}, "it is on " + h + ", read only on " + host, nil
	}
	ctx, cancel := context.WithTimeout(ctx, acceptanceReadTimeout)
	defer cancel()
	repo, err := st.open(path)
	if err != nil {
		return delivery.JudgedAcceptance{}, "", err
	}
	d, err := repo.Resolve(ctx, dgst)
	switch {
	case errors.Is(err, errdef.ErrNotFound):
		return delivery.JudgedAcceptance{}, "the record is gone", nil
	case err != nil:
		return delivery.JudgedAcceptance{}, "", err
	}
	_, subject, why, err := acceptanceLayer(ctx, repo, d, "")
	if why != "" || err != nil {
		return delivery.JudgedAcceptance{}, why, err
	}
	b, err := st.pull().Pull(ctx, path+"@"+subject)
	if err != nil {
		return delivery.JudgedAcceptance{}, "", fmt.Errorf("reading %s@%s: %w", path, subject, err)
	}
	rec, _ := contract.ReadInferredRecord(b.FS) // an unreadable record makes the owner declared as written
	q := acceptanceQuery{Subject: path + "@" + subject, Repo: path, Service: string(fleet.NewServiceKeyDomain(contract.OCIDomain(path), b.Contract.Service.Name)),
		Version: b.Contract.Service.Version, Owner: declaredOwner(b.Contract, rec), Expires: expires}
	if q.Service != service {
		return delivery.JudgedAcceptance{}, "it is attached to " + q.Service + ", not " + service, nil
	}
	return st.judgeOne(ctx, repo, d, "", q, subject)
}

// foreignAcceptance says why a verified statement does not belong to the
// subject it is attached to, "" when it does.
func foreignAcceptance(st delivery.AcceptanceStatement, q acceptanceQuery, dgst string) string {
	algo, hex, _ := strings.Cut(dgst, ":")
	if len(st.Subject) != 1 || st.Subject[0].Digest[algo] != hex {
		return "its statement names another subject"
	}
	if st.Predicate.Service != q.Service || st.Predicate.Version != q.Version {
		return fmt.Sprintf("it accepts for %s %s, not %s %s", st.Predicate.Service, st.Predicate.Version, q.Service, q.Version)
	}
	return ""
}

// AcceptOptions is one pacto accept run: the accepting service (Ref, a
// bundle repository, or Dir, its checkout), what it accepts (every proposed
// range, named fields as key or key=value, or a break as
// <provider>@<version>, read from From when that version is unpublished),
// the intent digest of a plan reviewed earlier, and the reader of the
// installation signer policy (nil reads none).
type AcceptOptions struct {
	Ref, Dir string
	Proposed bool
	Fields   []string
	Break    string
	From     string
	Intent   string
	Trust    func(context.Context) (Installation, error)
	// Signing is how Accept signs: 513-S3's declare signer, where only a
	// person's interactive login signs the record and every other signer
	// writes a draft on behalf of OnBehalfOf.
	Signing    declareSigning
	OnBehalfOf string
	// Agent is a run on an agent's behalf (pacto_accept): it writes a
	// draft and never logs a person in.
	Agent bool
	// Draft is an acceptance draft (<repo>:pacto-accept-draft-<n>) a
	// person accepts: its record is planned again and signed by them.
	Draft string
}

// AcceptPlan is what pacto accept would sign: the record for Service at
// Version, attached to Subject, who may accept it, the acceptances already
// attached and the intent digest that names exactly this plan.
type AcceptPlan struct {
	Service      string                   `json:"service"`
	Version      string                   `json:"version"`
	Subject      string                   `json:"subject"`
	Kind         string                   `json:"kind"`
	Ranges       []delivery.AcceptedRange `json:"ranges,omitempty"`
	Break        *delivery.AcceptedBreak  `json:"break,omitempty"`
	Expires      string                   `json:"expires,omitempty"`
	WhoMayAccept string                   `json:"whoMayAccept"`
	Existing     []impact.Acceptance      `json:"existing,omitempty"`
	Notes        []string                 `json:"notes,omitempty"`
	IntentDigest string                   `json:"intentDigest"`
	// Draft and OnBehalfOf are the acceptance draft a person accepts and
	// the person it asserts it was written for.
	Draft      string `json:"draft,omitempty"`
	OnBehalfOf string `json:"onBehalfOf,omitempty"`
	// owner, repo and record are who may accept, the accepting service's
	// bundle repository and the record the plan would sign.
	owner  contract.Owner
	repo   string
	record delivery.AcceptanceRecord
}

// AcceptPlan plans one pacto accept run against the accepting service's
// newest published version and writes nothing.
func (s *Service) AcceptPlan(ctx context.Context, opts AcceptOptions) (*AcceptPlan, error) {
	if opts.Draft != "" {
		return s.planDraft(ctx, opts)
	}
	env := s.env()
	reg := env.publishRegistry()
	repo, err := acceptRepo(opts)
	if err != nil {
		return nil, err
	}
	version, dgst, b, err := newestPublished(ctx, reg, repo, opts)
	if err != nil {
		return nil, err
	}
	c := b.Contract
	rec, err := contract.ReadInferredRecord(b.FS)
	if err != nil {
		return nil, fmt.Errorf("the inferred record of %s:%s: %w", repo, version, err)
	}
	p := &AcceptPlan{Service: string(fleet.NewServiceKeyDomain(contract.OCIDomain(repo), c.Service.Name)), Version: version, Subject: repo + "@" + dgst}
	r := delivery.AcceptanceRecord{Service: p.Service, Version: version}
	if opts.Break != "" {
		r.Kind = delivery.AcceptKindBreak
		if r.Break, p.Expires, err = acceptBreak(ctx, reg, repo, c, rec, &opts); err != nil {
			return nil, err
		}
	} else {
		r.Kind = delivery.AcceptKindRanges
		if r.Ranges, err = acceptFields(rec, opts); err != nil {
			return nil, err
		}
	}
	p.Kind, p.Ranges, p.Break = r.Kind, r.Ranges, r.Break
	owner := declaredOwner(c, rec)
	p.WhoMayAccept, _ = delivery.OwnerRoute(c.Service.Name, owner)
	p.owner, p.repo, p.record = owner, repo, r
	judged, notes, err := newAcceptanceStore(ctx, env, opts.Trust).readAcceptances(ctx, acceptanceQuery{Subject: p.Subject, Repo: repo,
		Service: p.Service, Version: version, Owner: owner, Expires: sameBreakExpiry(r.Break, p.Expires), Wants: wantsSame(r.Break)})
	if err != nil {
		return nil, err
	}
	for _, j := range judged {
		p.Existing = append(p.Existing, j.ForImpact())
	}
	p.Notes = notes
	p.IntentDigest = delivery.AcceptanceIntent(r, p.Subject)
	if opts.Intent != "" && opts.Intent != p.IntentDigest {
		return p, &DeclareError{Code: codePlanStale, Message: "the plan changed since you reviewed it (intent " + opts.Intent + ", now " + p.IntentDigest + ")",
			CLI: acceptCommand(opts, "")}
	}
	return p, nil
}

// planDraft plans accepting the acceptance draft opts.Draft: the draft's
// record planned again against the registry now, refused PlanStale when
// anything moved since the draft was written, with the person it is on
// behalf of and what its signature shows.
func (s *Service) planDraft(ctx context.Context, opts AcceptOptions) (*AcceptPlan, error) {
	repo, err := acceptRepo(opts)
	if err != nil {
		return nil, err
	}
	notFound := &DeclareError{Code: codeDraftNotFound, Message: opts.Draft + " holds no acceptance draft for " + repo, CLI: acceptCommand(AcceptOptions{Ref: repo}, "--proposed --dry-run")}
	if repoOf(opts.Draft) != repo {
		return nil, notFound
	}
	d, signed, err := s.readAcceptanceDraft(ctx, opts.Draft)
	if err != nil {
		return nil, err
	}
	if subject, _, _ := strings.Cut(d.Subject, "@"); subject != repo || d.Record.Intent == "" {
		return nil, notFound
	}
	note, err := draftSignature(d, signed, opts.Signing.TrustedRoot)
	if err != nil {
		return nil, err
	}
	p, err := s.AcceptPlan(ctx, draftOptions(opts, d, repo))
	if p != nil {
		p.Draft, p.OnBehalfOf, p.Notes = opts.Draft, d.OnBehalfOf, append(p.Notes, note)
	}
	return p, err
}

// draftOptions is the pacto accept run that plans d's record again: its
// fields (key=value where the value is not the proposal) or its break, with
// the intent digest the person reviewed, else the draft's.
func draftOptions(opts AcceptOptions, d delivery.AcceptanceDraft, repo string) AcceptOptions {
	o := opts
	o.Ref, o.Dir, o.Draft, o.Proposed, o.Fields, o.Break = repo, "", "", false, nil, ""
	o.Intent = cmp.Or(opts.Intent, d.Record.Intent)
	if b := d.Record.Break; b != nil {
		_, name := fleet.ParseServiceKey(fleet.ServiceKey(b.Provider))
		o.Break = name + "@" + b.Version
	}
	for _, r := range d.Record.Ranges {
		f := r.Field
		if r.Proposed != "" && r.Value != r.Proposed {
			f += "=" + r.Value
		}
		o.Fields = append(o.Fields, f)
	}
	return o
}

// draftSignature says what an acceptance draft's signature shows: nothing
// for an unsigned draft or a signer naming no identity (its proposer is
// asserted), else the asker it names, verified. bundle is the signed draft,
// nil when it is unsigned.
func draftSignature(d delivery.AcceptanceDraft, bundle []byte, trustedRoot string) (string, error) {
	a := d.Asker
	switch {
	case bundle == nil:
		return "unsigned draft, proposer asserted", nil
	case a.Subject == "" || a.Issuer == signerKey:
		return "the draft's signer names no identity Pacto can check: its proposer is asserted", nil
	}
	tm, err := declareRoot(trustedRoot)
	if err != nil {
		return "", err
	}
	got, _, err := signing.VerifyKeyless(bundle, tm)
	if err == nil {
		err = sameSigner(delivery.PublisherIdentity{Subject: a.Subject, Issuer: a.Issuer}, []delivery.PublisherIdentity{got})
	}
	if err != nil {
		return "", &DeclareError{Code: codeSignerUnverified, Message: "the draft is not signed by the asker it names: " + err.Error()}
	}
	return "the draft is signed by " + a.Subject + " (" + a.Issuer + ")", nil
}

// acceptCommand is the pacto accept command that plans opts again, with
// extra flags.
func acceptCommand(opts AcceptOptions, extra string) string {
	cmd := "pacto accept " + cmp.Or(opts.Dir, opts.Ref)
	switch {
	case opts.Break != "":
		cmd += " --break " + opts.Break
	case opts.Proposed:
		cmd += " --proposed"
	}
	for _, f := range opts.Fields {
		cmd += " --field '" + f + "'"
	}
	return strings.TrimSpace(cmd + " " + extra)
}

// acceptRepo is the accepting service's bundle repository: Ref, or the
// publish.bundle its checkout Dir declares.
func acceptRepo(opts AcceptOptions) (string, error) {
	if opts.Dir == "" {
		return publish.Repo(opts.Ref), nil
	}
	b, err := loadLocalBundle(opts.Dir)
	if err != nil {
		return "", err
	}
	if b.Contract.Publish == nil || b.Contract.Publish.Bundle == "" {
		return "", &DeclareError{Code: codeNotPublished, Message: opts.Dir + " declares no publish.bundle, so no published version can be accepted for it",
			CLI: "pacto ci --publish " + opts.Dir}
	}
	return publish.Repo(b.Contract.Publish.Bundle), nil
}

// newestPublished is repo's newest final version: its tag, manifest digest
// and bundle. A repository with none is NotPublished.
func newestPublished(ctx context.Context, reg publish.Registry, repo string, opts AcceptOptions) (string, string, *contract.Bundle, error) {
	tags, err := reg.Tags(ctx, repo)
	if err != nil {
		return "", "", nil, fmt.Errorf("listing the versions in %s: %w", repo, err)
	}
	for _, t := range semver.Filter(tags) {
		if strings.Contains(t, "-") {
			continue // a candidate is never what an acceptance attaches to
		}
		head, err := reg.Head(ctx, repo+":"+t)
		if err != nil || head == nil {
			return "", "", nil, fmt.Errorf("reading %s:%s: %w", repo, t, cmp.Or(err, errdef.ErrNotFound))
		}
		b, err := reg.Pull(ctx, repo+"@"+head.Digest)
		if err != nil {
			return "", "", nil, fmt.Errorf("reading %s:%s: %w", repo, t, err)
		}
		return t, head.Digest, b, nil
	}
	return "", "", nil, &DeclareError{Code: codeNotPublished, Message: repo + " holds no published version to attach an acceptance to",
		CLI: "pacto ci --publish " + cmp.Or(opts.Dir, ".")}
}

// declaredOwner is c's owner unless its record says the owner was inferred:
// an inferred owner never accepts.
func declaredOwner(c *contract.Contract, rec *contract.InferredRecord) contract.Owner {
	if rec != nil && slices.ContainsFunc(rec.Fields, func(f contract.Inferred) bool { return strings.HasPrefix(f.Field, "service.owner") }) {
		return contract.Owner{}
	}
	return c.Service.Owner
}

// acceptFields selects the fields a ranges acceptance accepts: every
// proposed range with --proposed, and each named field (a proposed range,
// with an optional =<value>, or a proposed ref).
func acceptFields(rec *contract.InferredRecord, opts AcceptOptions) ([]delivery.AcceptedRange, error) {
	proposals := map[string]contract.Inferred{}
	var ranges []string
	if rec != nil {
		for _, f := range rec.Fields {
			switch {
			case strings.HasSuffix(f.Field, ".compatibility") && f.Proposed != "":
				proposals[f.Field], ranges = f, append(ranges, f.Field)
			case strings.HasSuffix(f.Field, ".ref") && strings.HasPrefix(f.Source, contract.SourceIndex):
				proposals[f.Field] = f
			}
		}
	}
	chosen := map[string]delivery.AcceptedRange{}
	if opts.Proposed {
		for _, k := range ranges {
			f := proposals[k]
			chosen[k] = delivery.AcceptedRange{Field: k, Value: f.Proposed, Proposed: f.Proposed, Source: f.Source}
		}
	}
	for _, arg := range opts.Fields {
		a, err := acceptField(proposals, arg)
		if err != nil {
			return nil, err
		}
		chosen[a.Field] = a
	}
	if len(chosen) == 0 {
		return nil, &DeclareError{Code: codeAcceptNothingProposed, Message: "the newest published version proposes no dependency range to accept",
			CLI: "pacto ci --publish " + cmp.Or(opts.Dir, ".")}
	}
	out := make([]delivery.AcceptedRange, 0, len(chosen))
	for _, k := range slices.Sorted(maps.Keys(chosen)) {
		out = append(out, chosen[k])
	}
	return out, nil
}

// acceptField is one --field: key or key=value, a field the record proposes.
func acceptField(proposals map[string]contract.Inferred, arg string) (delivery.AcceptedRange, error) {
	key, value, hasValue := strings.Cut(arg, "=")
	f, ok := proposals[key]
	refuse := func(msg string) error {
		return &DeclareError{Code: codeFieldNotAcceptable, Message: msg, CLI: "pacto declare <service> --registry <registry>"}
	}
	switch {
	case !ok:
		return delivery.AcceptedRange{}, refuse(key + " is no proposed dependency ref or range of the newest published version: declare other fields with pacto declare")
	case !hasValue && f.Proposed != "":
		return delivery.AcceptedRange{Field: key, Value: f.Proposed, Proposed: f.Proposed, Source: f.Source}, nil
	case !hasValue:
		return delivery.AcceptedRange{Field: key, Value: f.Value, Source: f.Source}, nil
	case f.Proposed == "":
		return delivery.AcceptedRange{}, refuse(key + " is accepted as proposed: drop =" + value)
	}
	if _, err := contract.ParseRange(value); err != nil || value == contract.CompatibilityUnknown {
		return delivery.AcceptedRange{}, refuse(value + " is not a semver range to accept for " + key)
	}
	return delivery.AcceptedRange{Field: key, Value: value, Proposed: f.Proposed, Source: f.Source}, nil
}

// acceptBreak reads the break opts.Break names (<provider>@<version>) from
// that provider version, published or in opts.From, and returns what the
// record names and the break's expiry ("" when none is recorded).
func acceptBreak(ctx context.Context, reg publish.Registry, repo string, c *contract.Contract, rec *contract.InferredRecord, opts *AcceptOptions) (*delivery.AcceptedBreak, string, error) {
	i := strings.LastIndex(opts.Break, "@")
	if i <= 0 || i == len(opts.Break)-1 {
		return nil, "", fmt.Errorf("--break takes <provider>@<version>, not %q", opts.Break)
	}
	name, version := opts.Break[:i], opts.Break[i+1:]
	notAnnounced := func(msg string) error {
		return &DeclareError{Code: codeBreakNotAnnounced, Message: msg, CLI: acceptCommand(*opts, "--from <checkout of "+name+" "+version+">")}
	}
	prepo := providerRepo(c, rec, name)
	pb, bundle, err := breakSource(ctx, reg, prepo, name, version, opts.From)
	if err != nil || pb == nil {
		return nil, "", cmp.Or(err, notAnnounced(name+" "+version+" is not published and no --from names its checkout or a pushed ref"))
	}
	if pb.Contract.Service.Name != name {
		return nil, "", notAnnounced("--from " + opts.From + " holds " + pb.Contract.Service.Name + ", not " + name)
	}
	for k, b := range pb.Contract.Breaks {
		if slices.Contains(b.Consumers, c.Service.Name) {
			prec, _ := contract.ReadInferredRecord(pb.FS) // an unreadable record proposes no expiry
			// A dependency by name resolves in the consumer's domain.
			provider := fleet.NewServiceKeyDomain(contract.OCIDomain(cmp.Or(prepo, repo)), name)
			return &delivery.AcceptedBreak{Provider: string(provider), Version: version,
				Announcement: delivery.BreakAnnouncement(b), Bundle: bundle}, cmp.Or(b.Expires, recordedExpiry(prec, k)), nil
		}
	}
	return nil, "", notAnnounced(name + " " + version + " announces no break for " + c.Service.Name)
}

// providerRepo is the bundle repository of c's dependency name: its ref,
// else the ref the record proposes for it; "" when neither names one.
func providerRepo(c *contract.Contract, rec *contract.InferredRecord, name string) string {
	for _, d := range c.Dependencies {
		if d.Name == name && d.Ref != "" && !d.External {
			return repoOf(d.Ref)
		}
	}
	if rec != nil {
		for _, f := range rec.Fields {
			if f.Field == "dependencies["+name+"].ref" {
				return repoOf(f.Value)
			}
		}
	}
	return ""
}

// repoOf is the repository of an oci:// reference, without tag or digest.
func repoOf(ref string) string {
	r := publish.Repo(ref)
	r, _, _ = strings.Cut(r, "@")
	if i := strings.LastIndex(r, ":"); i > strings.LastIndex(r, "/") {
		r = r[:i]
	}
	return r
}

// breakSource reads the provider version: published in repo, else from
// from (a checkout or an oci:// ref). It returns the bundle and, when
// published, <repo>@<digest>; nil when neither holds it.
func breakSource(ctx context.Context, reg publish.Registry, repo, name, version, from string) (*contract.Bundle, string, error) {
	if repo != "" {
		head, err := reg.Head(ctx, repo+":"+version)
		if err != nil {
			return nil, "", fmt.Errorf("reading %s %s: %w", name, version, err)
		}
		if head != nil {
			b, err := reg.Pull(ctx, repo+"@"+head.Digest)
			return b, repo + "@" + head.Digest, err
		}
	}
	switch {
	case from == "":
		return nil, "", nil
	case strings.HasPrefix(from, "oci://"):
		b, err := reg.Pull(ctx, publish.Repo(from))
		return b, "", err
	}
	b, err := loadLocalBundle(from)
	return b, "", err
}

// recordedExpiry is the breaks[i].expires the record proposes, "" when none.
func recordedExpiry(rec *contract.InferredRecord, i int) string {
	if rec == nil {
		return ""
	}
	key := fmt.Sprintf("breaks[%d].expires", i)
	for _, f := range rec.Fields {
		if f.Field == key {
			return f.Value
		}
	}
	return ""
}

// sameBreakExpiry is the expiry of b for a record that accepts exactly b,
// "" for any other break.
func sameBreakExpiry(b *delivery.AcceptedBreak, expires string) func(delivery.AcceptedBreak) string {
	return func(got delivery.AcceptedBreak) string {
		if b != nil && got.Provider == b.Provider && got.Version == b.Version && got.Announcement == b.Announcement {
			return expires
		}
		return ""
	}
}

// wantsSame accepts a counted record of the same kind: the same break, or
// any ranges acceptance.
func wantsSame(b *delivery.AcceptedBreak) func(impact.Acceptance) bool {
	return func(a impact.Acceptance) bool {
		if b == nil || a.Break == nil {
			return b == nil && a.Break == nil
		}
		return *a.Break == impact.BreakRef{Provider: b.Provider, Version: b.Version, Announcement: b.Announcement}
	}
}

// acceptanceDraftPrefix starts every acceptance draft tag.
const acceptanceDraftPrefix = delivery.AcceptanceDraftTagPrefix

// writeAcceptance attaches the signed acceptance bundle to subject in repo:
// a native referrer the Referrers API lists, else a write-once tag. It
// returns where the record is (<repo>@sha256:<hex>). dgst is the subject's
// manifest digest.
func (s *Service) writeAcceptance(ctx context.Context, repo, dgst string, bundle []byte) (string, error) {
	r, err := s.env().refusalRepo(repo)
	if err != nil {
		return "", err
	}
	subject, err := r.Resolve(ctx, dgst)
	if err != nil {
		return "", fmt.Errorf("reading %s@%s: %w", repo, dgst, err)
	}
	art := ocireferrer.Build(delivery.AcceptanceArtifactType, delivery.AcceptanceArtifactType, bundle, &subject, nil)
	caps, err := ocireferrer.Probes.Probe(ctx, r, subject.Digest.String(), false)
	switch {
	case err != nil:
		return "", fmt.Errorf("probing %s: %w", repo, err)
	case caps.Referrers == capabilities.ReferrersNative:
		if err = ocireferrer.Push(ctx, r, art); err == nil {
			err = ocireferrer.Confirm(ctx, r, subject, art.ManifestDesc)
		}
	default:
		err = ocireferrer.PushWriteOnce(ctx, r, art, delivery.AcceptanceTag(subject.Digest, art.ManifestDesc.Digest))
	}
	if err != nil {
		return "", fmt.Errorf("writing the acceptance to %s: %w", repo, err)
	}
	return repo + "@" + art.ManifestDesc.Digest.String(), nil
}

// writeAcceptanceDraft writes d to repo under the next free
// pacto-accept-draft-<n> tag and returns that tag: signed by signer, or
// unsigned when it is nil. When the newest draft already holds d (a CI or
// agent run repeated), its tag is returned and nothing is signed or
// written. A number another run took meanwhile is retried once at the next
// one; a second loss is VersionConflict.
func (s *Service) writeAcceptanceDraft(ctx context.Context, repo string, d delivery.AcceptanceDraft, signer *signing.Signer) (string, error) {
	r, err := s.env().refusalRepo(repo)
	if err != nil {
		return "", err
	}
	tags, err := ocireferrer.Tags(ctx, r, acceptanceDraftPrefix)
	if err != nil {
		return "", fmt.Errorf("listing the acceptance drafts in %s: %w", repo, err)
	}
	n := publish.NextDraft(tags, acceptanceDraftPrefix)
	payload, _ := json.Marshal(d) // plain data; marshalling cannot fail
	// ponytail: the newest draft is compared unverified; whoever may push to
	// the service's own repository could plant a look-alike, and the person
	// accepting it verifies its signature anyway.
	newest := acceptanceDraftPrefix + strconv.Itoa(n-1)
	if prev, _, err := s.readAcceptanceDraft(ctx, repo+":"+newest); err == nil {
		if same, _ := json.Marshal(prev); string(same) == string(payload) {
			return newest, nil
		}
	}
	layer := delivery.AcceptanceDraftArtifactType
	if signer != nil {
		if payload, _, err = delivery.SignAcceptanceDraft(ctx, signer, d); err != nil {
			return "", fmt.Errorf("signing the acceptance draft: %w", err)
		}
		layer = delivery.BundleMediaType
	}
	art := ocireferrer.Build(delivery.AcceptanceDraftArtifactType, layer, payload, nil, nil)
	for try := n; try <= n+1; try++ {
		tag := acceptanceDraftPrefix + strconv.Itoa(try)
		if err = ocireferrer.PushWriteOnce(ctx, r, art, tag); !errors.Is(err, ocireferrer.ErrTagTaken) {
			return tag, err
		}
	}
	return "", &DeclareError{Code: codeVersionConflict, Message: "another run took " + acceptanceDraftPrefix + strconv.Itoa(n) + " and the next draft number in " + repo + ": " + err.Error()}
}

// readAcceptanceDraft reads the acceptance draft ref
// (<repo>:pacto-accept-draft-<n>), strictly decoded and unverified, and
// its signed bundle (nil when it is unsigned). A tag that is not there, or
// does not hold one acceptance draft within the acceptance bounds, is
// DraftNotFound.
func (s *Service) readAcceptanceDraft(ctx context.Context, ref string) (delivery.AcceptanceDraft, []byte, error) {
	repo := repoOf(ref)
	tag := ref[strings.LastIndex(ref, ":")+1:]
	notFound := &DeclareError{Code: codeDraftNotFound, Message: "there is no acceptance draft " + ref}
	if !strings.HasPrefix(tag, acceptanceDraftPrefix) {
		return delivery.AcceptanceDraft{}, nil, notFound
	}
	r, err := s.env().refusalRepo(repo)
	if err != nil {
		return delivery.AcceptanceDraft{}, nil, err
	}
	desc, err := r.Resolve(ctx, tag)
	if errors.Is(err, errdef.ErrNotFound) {
		return delivery.AcceptanceDraft{}, nil, notFound
	}
	var raw []byte
	if err == nil && desc.Size <= maxAcceptanceManifest {
		raw, err = content.FetchAll(ctx, r, desc)
	}
	if err != nil {
		return delivery.AcceptanceDraft{}, nil, fmt.Errorf("reading %s: %w", ref, err)
	}
	var m ocispec.Manifest
	_ = json.Unmarshal(raw, &m) // a manifest that does not decode is no draft
	if m.ArtifactType != delivery.AcceptanceDraftArtifactType || len(m.Layers) != 1 || m.Layers[0].Size > delivery.MaxAcceptanceBytes {
		return delivery.AcceptanceDraft{}, nil, notFound
	}
	payload, err := content.FetchAll(ctx, r, m.Layers[0])
	if err != nil {
		return delivery.AcceptanceDraft{}, nil, fmt.Errorf("reading %s: %w", ref, err)
	}
	d, err := delivery.DecodeAcceptanceDraft(m.Layers[0].MediaType, payload)
	if err != nil {
		notFound.Message = ref + " holds no acceptance draft: " + err.Error()
		return delivery.AcceptanceDraft{}, nil, notFound
	}
	if m.Layers[0].MediaType != delivery.BundleMediaType {
		payload = nil
	}
	return d, payload, nil
}

// AcceptResult is what pacto accept did: the plan it carried out, who
// signed, and either the record it attached with how it is judged, or the
// draft it wrote and the command a person accepts it with.
type AcceptResult struct {
	Plan      *AcceptPlan `json:"plan,omitempty"`
	Executed  bool        `json:"executed"`
	Signer    SignerView  `json:"signer"`
	Record    string      `json:"record,omitempty"`
	State     string      `json:"state,omitempty"`
	Reason    string      `json:"reason,omitempty"`
	Draft     string      `json:"draft,omitempty"`
	AcceptCLI string      `json:"acceptCli,omitempty"`
}

// Accept plans opts and carries it out. Only a person's interactive Sigstore
// login signs the acceptance, checked to be signed by exactly that person
// before anything is written; every other signer (a key, an ambient token)
// writes the next pacto-accept-draft-<n> a person accepts. Both go to the
// accepting service's own bundle repository with the caller's credentials.
func (s *Service) Accept(ctx context.Context, opts AcceptOptions) (*AcceptResult, error) {
	p, err := s.AcceptPlan(ctx, opts)
	res := &AcceptResult{Plan: p}
	if err != nil {
		return res, err
	}
	sign := opts.Signing
	if sign.Policy == nil {
		sign.Policy = installationSigners(ctx, opts.Trust)
	}
	if opts.Draft != "" {
		// A draft is accepted by a person or not at all: refused before any
		// signer is built, and again below for a login that names no person.
		pc, err := plannedSigner(ctx, sign, opts.Agent)
		if err == nil && !pc.MayDeclare {
			err = personSigningUnavailable(pc, p.repo, opts.Draft)
		}
		if err != nil {
			return res, err
		}
	}
	c, err := s.declareSigner(ctx, sign, opts.Agent)
	if err != nil {
		return res, err
	}
	res.Signer = SignerView{Subject: c.Identity.Subject, Issuer: c.Identity.Issuer, Class: string(c.Class), Source: c.Source, Why: c.Why}
	r := p.record
	r.Intent = p.IntentDigest
	switch {
	case !c.MayDeclare && opts.Draft != "":
		return res, personSigningUnavailable(c, p.repo, opts.Draft)
	case !c.MayDeclare:
		return res, s.draftAcceptance(ctx, res, r, c, opts)
	}
	return res, s.signAcceptance(ctx, res, r, c, sign.Rules)
}

// personSigningUnavailable refuses accepting draft with c, a signer that
// names no person.
func personSigningUnavailable(c signerChoice, repo, draft string) error {
	return &DeclareError{Code: codePersonSigningUnavailable, Message: "accepting a draft needs a person's signature: " + c.Why +
		"; run it without --sign-key and with no ambient OIDC token (SIGSTORE_ID_TOKEN), so you sign after a Sigstore login",
		CLI: "pacto accept " + repo + " --draft " + draft}
}

// installationSigners is the installation signer policy trust reads, nil
// when it reads none: then nobody is refused before signing.
func installationSigners(ctx context.Context, trust func(context.Context) (Installation, error)) *delivery.ClassPolicy {
	if trust == nil {
		return nil
	}
	in, err := trust(ctx)
	if err != nil || len(in.Signers) == 0 {
		return nil
	}
	return &delivery.ClassPolicy{Signers: in.Signers}
}

// signAcceptance signs r as the person c logged in as, checks the bundle is
// signed by exactly that person, attaches it to the plan's subject and
// judges it as readers will.
func (s *Service) signAcceptance(ctx context.Context, res *AcceptResult, r delivery.AcceptanceRecord, c signerChoice, rules []delivery.SignerClassRule) error {
	p := res.Plan
	st := delivery.NewAcceptanceStatement(p.Subject, r)
	bundle, _, err := delivery.SignAcceptance(ctx, c.Signer, st)
	if err != nil {
		return err
	}
	got, _, err := signing.VerifyKeyless(bundle, c.Signer.Root)
	if err == nil {
		err = sameSigner(c.Identity, []delivery.PublisherIdentity{got})
	}
	if err != nil {
		return &DeclareError{Code: codeSignerUnverified, Message: "nothing was written: " + err.Error()}
	}
	_, dgst, _ := strings.Cut(p.Subject, "@")
	if res.Record, err = s.writeAcceptance(ctx, p.repo, dgst, bundle); err != nil {
		return err
	}
	res.Executed = true
	res.State, res.Reason = delivery.JudgeAcceptance(st, got, rules, p.owner, p.Expires, s.env().now())
	return nil
}

// draftAcceptance writes r as an acceptance draft signed by c (unsigned
// when c has no signer), on behalf of opts.OnBehalfOf.
func (s *Service) draftAcceptance(ctx context.Context, res *AcceptResult, r delivery.AcceptanceRecord, c signerChoice, opts AcceptOptions) error {
	p := res.Plan
	var signer *signing.Signer
	if c.Source != signerNone {
		signer = &c.Signer
	}
	tag, err := s.writeAcceptanceDraft(ctx, p.repo, delivery.AcceptanceDraft{Record: r, Subject: p.Subject, OnBehalfOf: opts.OnBehalfOf, Asker: askerOf(c)}, signer)
	if err != nil {
		return err
	}
	res.Executed, res.Draft = true, p.repo+":"+tag
	res.AcceptCLI = "pacto accept " + cmp.Or(opts.Dir, opts.Ref) + " --draft " + res.Draft
	return nil
}
