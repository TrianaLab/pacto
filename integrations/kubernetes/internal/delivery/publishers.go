/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"cmp"
	"context"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/semver"
)

// Bounds of the watch's memos: how long a failed signature read and a failed
// root fetch are kept, and how many digests' signers are kept.
const (
	signerRetry   = 5 * time.Minute
	rootRetry     = time.Minute
	maxSignerMemo = 512
)

// Publishers is the promoter's registry watch (candidates.registry): it reads
// the newest version tags of a repository and judges who signed each digest
// against the installation's publisher rule. A contract never decides: the
// watch reads no contract.
type Publishers struct {
	// Reg returns a registry client that reaches insecureHosts over plain HTTP.
	Reg func(insecureHosts ...string) publish.Registry
	// Root fetches the Sigstore trusted root signatures must chain to. A
	// fetched root is kept; a failure is fetched again after a minute.
	Root func() (root.TrustedMaterial, error)
	// Rule is the installation's publisher rule when HasRule; NoRule says
	// why there is none, and then only a person's pin accepts a signer.
	Rule    delivery.PublisherRule
	HasRule bool
	NoRule  string
	// Resync is how long one listing of a repository is reused.
	Resync time.Duration
	Now    func() time.Time

	mu       sync.Mutex
	tm       root.TrustedMaterial
	rootErr  error
	rootAt   time.Time
	listings map[string]listing
	signers  map[string]signersRead
}

// listing is one read of a repository's newest version tags.
type listing struct {
	at   time.Time
	tags []watchedTag
	err  error
}

// watchedTag is one version tag, the digest it names and the source
// repository its artifact names (org.opencontainers.image.source).
type watchedTag struct {
	version, digest, source string
	err                     error
}

// signersRead is who signed one digest, read once: a digest with a verified
// signer keeps it, an error is read again after signerRetry.
type signersRead struct {
	at      time.Time
	signers []delivery.PublisherIdentity
	err     error
}

// root is the trusted root, fetched once, or the error of the last fetch
// while it is younger than rootRetry.
func (p *Publishers) root() (root.TrustedMaterial, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tm != nil || (p.rootErr != nil && p.Now().Sub(p.rootAt) < rootRetry) {
		return p.tm, p.rootErr
	}
	p.tm, p.rootErr = p.Root()
	p.rootAt = p.Now()
	return p.tm, p.rootErr
}

// list reads repo's newest MaxWatchedTags version tags, reusing a read
// younger than Resync.
func (p *Publishers) list(ctx context.Context, reg publish.Registry, repo string) ([]watchedTag, error) {
	now := p.Now()
	p.mu.Lock()
	l, ok := p.listings[repo]
	p.mu.Unlock()
	if ok && now.Sub(l.at) < cmp.Or(p.Resync, DefaultResync) {
		return l.tags, l.err
	}
	l = listing{at: now}
	tags, err := reg.Tags(ctx, repo)
	if l.err = err; err == nil {
		l.tags = heads(ctx, reg, repo, semver.Filter(tags))
	}
	p.mu.Lock()
	if p.listings == nil {
		p.listings = map[string]listing{}
	}
	p.listings[repo] = l
	p.mu.Unlock()
	return l.tags, l.err
}

// heads reads the digest and source of the newest MaxWatchedTags of
// versions (newest first). A tag that no longer exists is left out.
func heads(ctx context.Context, reg publish.Registry, repo string, versions []string) []watchedTag {
	var out []watchedTag
	for _, v := range versions[:min(len(versions), delivery.MaxWatchedTags)] {
		h, err := reg.Head(ctx, repo+":"+v)
		switch {
		case err != nil:
			out = append(out, watchedTag{version: v, err: err})
		case h != nil:
			out = append(out, watchedTag{version: v, digest: h.Digest, source: h.Annotations[publish.AnnotationSource]})
		}
	}
	return out
}

// signersOf reads who signed repo@digest, once per digest (signersRead).
func (p *Publishers) signersOf(ctx context.Context, reg publish.Registry, tm root.TrustedMaterial, repo, digest string) ([]delivery.PublisherIdentity, error) {
	now := p.Now()
	p.mu.Lock()
	s, ok := p.signers[digest]
	p.mu.Unlock()
	if ok && (s.err == nil || now.Sub(s.at) < signerRetry) {
		return s.signers, s.err
	}
	signers, err := publish.SignersOf(ctx, reg, tm, len(tm.RekorLogs()) > 0, repo, digest)
	p.mu.Lock()
	if p.signers == nil || len(p.signers) >= maxSignerMemo {
		// ponytail: dropping the whole memo at its bound re-reads up to 512
		// digests once; an LRU if a promoter watches that many.
		p.signers = map[string]signersRead{}
	}
	p.signers[digest] = signersRead{at: now, signers: signers, err: err}
	p.mu.Unlock()
	return signers, err
}

// watch judges the newest version tags of repo for svc, newest first. The
// signature read is memoised per digest; the judgement runs on every pass,
// with this service and rule, so two services never share a verdict.
func (p *Publishers) watch(ctx context.Context, reg publish.Registry, tm root.TrustedMaterial, svc delivery.ServiceRef, repo string) ([]delivery.PublisherVerdict, error) {
	tags, err := p.list(ctx, reg, repo)
	if err != nil {
		return nil, err
	}
	var rules []delivery.PublisherRule
	if p.HasRule {
		rules = []delivery.PublisherRule{p.Rule}
	}
	out := make([]delivery.PublisherVerdict, 0, len(tags))
	for _, t := range tags {
		var v delivery.PublisherVerdict
		if t.err != nil {
			v = publish.JudgeRead(t.digest, nil, t.err, svc, t.source, rules, delivery.PublisherSourceRule, nil)
		} else {
			signers, err := p.signersOf(ctx, reg, tm, repo, t.digest)
			v = publish.JudgeRead(t.digest, signers, err, svc, t.source, rules, delivery.PublisherSourceRule, nil)
		}
		v.Version = t.version
		out = append(out, v)
	}
	return out, nil
}

// registryReady runs the registry watch of a Stage with candidates.registry
// and returns its Ready condition. Accepted digests join status.published, a
// judged digest no longer accepted leaves it, and every verdict lands in
// status.publisher, in one status write. Unknown never publishes: a watch
// that cannot judge leaves status.published as it is.
func (r *StageReconciler) registryReady(ctx context.Context, stage *dv1.Stage) (delivery.Condition, error) {
	p := r.Publishers
	if p == nil {
		return notReady(delivery.ReadyReasonPublisherUnverifiable, "the promoter has no publisher check configured (delivery.publishers), so the Stage never promotes"), nil
	}
	repo, insecure, err := r.watchedRepo(ctx, stage)
	switch {
	case readyNamesIt(err):
		return notReady(delivery.ReadyReasonRegistryUnreadable, "the applier the registry watch reads its repository from cannot be read: "+err.Error()), nil
	case err != nil:
		return delivery.Condition{}, err
	}
	tm, err := p.root()
	if err != nil {
		why := "Sigstore's trusted root could not be fetched (" + err.Error() + "), so no version of " + repo + " is judged"
		return notReady(delivery.ReadyReasonPublisherUnverifiable, why), r.writePublisher(ctx, stage, p, nil, why)
	}
	verdicts, err := p.watch(ctx, p.Reg(insecure...), tm, stage.Spec.Service, repo)
	if err != nil {
		why := "the registry " + repo + " cannot be listed (" + err.Error() + "), so no version is published until it can"
		return notReady(delivery.ReadyReasonRegistryUnreadable, why), r.writePublisher(ctx, stage, p, nil, why)
	}
	want := delivery.Condition{Type: conditionReady, Status: statusTrue, Reason: conditionReady, Message: "the Stage can promote published versions"}
	return want, r.writePublisher(ctx, stage, p, verdicts, p.NoRule)
}

// watchedRepo is the repository the Stage's registry watch reads: the one it
// names, or its applier's, reached over plain HTTP when the applier says so.
func (r *StageReconciler) watchedRepo(ctx context.Context, stage *dv1.Stage) (string, []string, error) {
	if repo := stage.Spec.Candidates.Registry.Repository; repo != "" {
		return repo, nil, nil
	}
	a, err := r.Cluster.applier(ctx, stage.Namespace, stage.Spec.ApplyRef)
	if err != nil {
		return "", nil, err
	}
	src, hosts := a.src(), []string(nil)
	if src.insecure {
		hosts = []string{src.host()}
	}
	return src.repo, hosts, nil
}

// writePublisher writes what the watch judged: verdicts (nil when it could
// not judge, which keeps status.published and the last verdicts) and msg. It
// writes only on a change, or once per resync to move checkedAt.
func (r *StageReconciler) writePublisher(ctx context.Context, stage *dv1.Stage, p *Publishers, verdicts []delivery.PublisherVerdict, msg string) error {
	now, old := r.Now(), stage.Status.Publisher
	next := publisherStatus(p, old, verdicts, msg)
	published := applyVerdicts(stage.Status.Published, verdicts)
	if old != nil && samePublisher(*old, next) && slices.Equal(published, stage.Status.Published) &&
		now.Sub(parseOr(old.CheckedAt)) < cmp.Or(r.Resync, DefaultResync) {
		return nil
	}
	next.CheckedAt = now.UTC().Format(time.RFC3339)
	if err := r.updateStatus(ctx, stage, func(s *dv1.Stage) {
		s.Status.Published = applyVerdicts(s.Status.Published, verdicts)
		s.Status.Publisher = &next
	}); err != nil {
		return err
	}
	stage.Status.Published, stage.Status.Publisher = published, &next
	return nil
}

// publisherStatus is the next status.publisher: the rule, msg and the
// verdicts, each keeping the time it was first reached while it holds. nil
// verdicts keep the last ones.
func publisherStatus(p *Publishers, old *delivery.PublisherStatus, verdicts []delivery.PublisherVerdict, msg string) delivery.PublisherStatus {
	next := delivery.PublisherStatus{Source: delivery.PublisherSourceNone, Message: msg}
	if p.HasRule {
		rule := p.Rule
		next.Source, next.Rule = delivery.PublisherSourceRule, &rule
	}
	if old == nil {
		old = &delivery.PublisherStatus{}
	}
	if verdicts == nil {
		next.Verdicts = old.Verdicts
		return next
	}
	next.Verdicts = make([]delivery.PublisherVerdict, 0, len(verdicts))
	for _, v := range verdicts[:min(len(verdicts), delivery.MaxPublisherVerdicts)] {
		if i := slices.IndexFunc(old.Verdicts, func(o delivery.PublisherVerdict) bool { return sameVerdict(o, v) }); i >= 0 {
			v.At = old.Verdicts[i].At
		}
		next.Verdicts = append(next.Verdicts, v)
	}
	return next
}

// sameVerdict reports whether two verdicts say the same thing about the same
// tag, whenever each was reached.
func sameVerdict(a, b delivery.PublisherVerdict) bool {
	a.At, b.At = "", ""
	return reflect.DeepEqual(a, b)
}

// samePublisher reports whether two publisher statuses differ only in
// checkedAt.
func samePublisher(a, b delivery.PublisherStatus) bool {
	a.CheckedAt, b.CheckedAt = "", ""
	return reflect.DeepEqual(a, b)
}

// applyVerdicts adds every accepted digest to published (oldest tag first)
// and drops a judged digest no longer accepted. A digest outside the window
// stays.
// ponytail: the window (MaxWatchedTags) bounds the registry reads; re-judging
// all of status.published is the upgrade path.
func applyVerdicts(published []string, verdicts []delivery.PublisherVerdict) []string {
	out := slices.Clone(published)
	for _, v := range slices.Backward(verdicts) {
		switch {
		case v.Digest == "":
		case v.Verdict == delivery.VerdictAccepted:
			if !slices.Contains(out, v.Digest) {
				out = delivery.AddDigest(out, v.Digest, delivery.MaxPublished)
			}
		default: // two tags of one digest get one verdict: it is judged per digest
			out = delivery.RemoveDigest(out, v.Digest)
		}
	}
	return out
}

// newestAccepted is the digest of the newest verdict the watch accepted, ""
// when none.
func newestAccepted(p *delivery.PublisherStatus) string {
	if p == nil {
		return ""
	}
	if i := slices.IndexFunc(p.Verdicts, func(v delivery.PublisherVerdict) bool { return v.Verdict == delivery.VerdictAccepted }); i >= 0 {
		return p.Verdicts[i].Digest
	}
	return ""
}
