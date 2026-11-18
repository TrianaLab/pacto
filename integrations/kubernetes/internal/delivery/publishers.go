/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
	"oras.land/oras-go/v2/registry/remote"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/semver"
	"github.com/trianalab/pacto/v4/pkg/signing"
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
	// Policy is the signed delivery policy's tag reference
	// (delivery.publishers.policy); empty: the rule above decides. A
	// verified policy replaces it; one never verified gives no rule.
	Policy string
	// PolicyVerify checks a policy bundle against the policy keys of
	// delivery.publishers.policySigners.
	PolicyVerify func(bundle []byte) error
	// Pins reads the pins of a service; nil reads none.
	Pins func(ctx context.Context, svc delivery.ServiceRef) (deliveryrecord.PinListing, error)
	// CannotPin says why a new signer cannot be confirmed (no recorder, or
	// no key to verify the pins it writes), so no trust operation is
	// raised; empty when one can.
	CannotPin string
	// Resync is how long one listing of a repository is reused.
	Resync time.Duration
	Now    func() time.Time

	mu       sync.Mutex
	policies map[string]delivery.PublisherPolicy // verified in this process, by digest
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
// signature read is memoised per digest; the judgement and the signers'
// classes run on every pass, with this service, rule and policy, so two
// services never share a verdict and a new policy reclassifies at once.
func (p *Publishers) watch(ctx context.Context, reg publish.Registry, tm root.TrustedMaterial, svc delivery.ServiceRef, repo string, b basis) ([]delivery.PublisherVerdict, error) {
	tags, err := p.list(ctx, reg, repo)
	if err != nil {
		return nil, err
	}
	out := make([]delivery.PublisherVerdict, 0, len(tags))
	for _, t := range tags {
		var v delivery.PublisherVerdict
		if t.err != nil {
			v = publish.JudgeRead(t.digest, nil, t.err, svc, t.source, b.rules, b.source, b.pins)
		} else {
			signers, err := p.signersOf(ctx, reg, tm, repo, t.digest)
			signers = slices.Clone(signers) // the memo's, classified on every pass like the verdict
			for i := range signers {
				signers[i] = delivery.Classified(signers[i], b.classes)
			}
			v = publish.JudgeRead(t.digest, signers, err, svc, t.source, b.rules, b.source, b.pins)
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
	reg := p.Reg(insecure...)
	b := p.basisFor(ctx, reg, stage.Spec.Service, stage.Status.Publisher)
	tm, err := p.root()
	if err != nil {
		why := "Sigstore's trusted root could not be fetched (" + err.Error() + "), so no version of " + repo + " is judged"
		return notReady(delivery.ReadyReasonPublisherUnverifiable, why), r.writePublisher(ctx, stage, b, nil, why)
	}
	verdicts, err := p.watch(ctx, reg, tm, stage.Spec.Service, repo, b)
	if err != nil {
		why := "the registry " + repo + " cannot be listed (" + err.Error() + "), so no version is published until it can"
		return notReady(delivery.ReadyReasonRegistryUnreadable, why), r.writePublisher(ctx, stage, b, nil, why)
	}
	ask, err := r.trustAsk(ctx, stage, verdicts, b)
	if err != nil {
		return delivery.Condition{}, err
	}
	want := delivery.Condition{Type: conditionReady, Status: statusTrue, Reason: conditionReady, Message: "the Stage can promote published versions"}
	if err := r.writePublisher(ctx, stage, b, verdicts, strings.Join(slices.DeleteFunc(append(b.msgs, ask.why), func(m string) bool { return m == "" }), "; ")); err != nil {
		return want, err
	}
	if ask.spec == nil {
		return want, nil
	}
	return want, r.raiseNamed(ctx, stage, ask.name, *ask.spec)
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
func (r *StageReconciler) writePublisher(ctx context.Context, stage *dv1.Stage, b basis, verdicts []delivery.PublisherVerdict, msg string) error {
	now, old := r.Now(), stage.Status.Publisher
	next := publisherStatus(b, old, verdicts, msg)
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

// publisherStatus is the next status.publisher: what the watch judged
// against (b), msg and the verdicts, each keeping the time it was first
// reached while it holds. nil verdicts keep the last ones. The policy and
// pin high-water marks only move forward: b leaves them empty when it read
// nothing newer.
func publisherStatus(b basis, old *delivery.PublisherStatus, verdicts []delivery.PublisherVerdict, msg string) delivery.PublisherStatus {
	if old == nil {
		old = &delivery.PublisherStatus{}
	}
	next := delivery.PublisherStatus{Source: b.source, Rule: b.rule, PolicyRef: b.policyRef, Pins: len(b.pins), Message: msg,
		PolicyDigest: cmp.Or(b.policyDigest, old.PolicyDigest), PolicyAt: cmp.Or(b.policyAt, old.PolicyAt),
		PinDigest: cmp.Or(b.pinDigest, old.PinDigest), PinAt: cmp.Or(b.pinAt, old.PinAt)}
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

// basis is what a Stage's watch judges against on one pass: the rules and
// what they are (rule, policy or none), the policy's signer classes, the service's pins and the policy
// and pin high-water marks they moved to, and what could not be read.
type basis struct {
	rules                  []delivery.PublisherRule
	classes                []delivery.SignerClassRule
	source                 string
	rule                   *delivery.PublisherRule
	policyRef              string
	policyDigest, policyAt string
	pins                   []delivery.IdentityPin
	pinDigest, pinAt       string
	msgs                   []string
}

// basisFor reads what svc's watch judges against: the verified policy when
// one is configured (its rules replace the derived rule, and one never
// verified gives no rule), else the derived rule, and svc's pins. old is the
// Stage's status.publisher, which holds the high-water marks.
func (p *Publishers) basisFor(ctx context.Context, reg publish.Registry, svc delivery.ServiceRef, old *delivery.PublisherStatus) basis {
	if old == nil {
		old = &delivery.PublisherStatus{}
	}
	b := basis{source: delivery.PublisherSourceNone}
	switch {
	case p.Policy != "":
		b.policyRef = p.Policy
		pol, digest, why := p.policy(ctx, reg, old)
		if pol != nil {
			b.rules, b.classes, b.source, b.policyDigest, b.policyAt = pol.Publishers, pol.Signers, delivery.PublisherSourcePolicy, digest, pol.At
		}
		b.msgs = append(b.msgs, why)
	case p.HasRule:
		rule := p.Rule
		b.rules, b.source, b.rule = []delivery.PublisherRule{rule}, delivery.PublisherSourceRule, &rule
	default:
		b.msgs = append(b.msgs, p.NoRule)
	}
	var why string
	b.pins, b.pinDigest, b.pinAt, why = p.pinsFor(ctx, svc, old)
	b.msgs = append(b.msgs, why)
	return b
}

// policy is the delivery policy in force: the one its tag names, verified,
// unless that cannot be read or verified or was signed before the policy the
// Stage already used (policyAt), and then that last one, verified again. nil
// when neither verifies; why says what happened.
func (p *Publishers) policy(ctx context.Context, reg publish.Registry, old *delivery.PublisherStatus) (*delivery.PublisherPolicy, string, string) {
	keep := func(why string) (*delivery.PublisherPolicy, string, string) {
		if old.PolicyDigest != "" {
			if last, err := p.verifiedPolicy(ctx, reg, old.PolicyDigest); err == nil {
				return &last, old.PolicyDigest, why + "; the policy " + old.PolicyDigest + " stays"
			}
		}
		return nil, "", why + ", so only pinned signers are accepted"
	}
	h, err := reg.Head(ctx, p.Policy)
	if err == nil && h == nil {
		err = errors.New("it does not exist")
	}
	if err != nil {
		return keep("the delivery policy " + p.Policy + " could not be read (" + err.Error() + ")")
	}
	pol, err := p.verifiedPolicy(ctx, reg, h.Digest)
	switch {
	case err != nil:
		return keep("the delivery policy " + p.Policy + " could not be verified (" + err.Error() + ")")
	case h.Digest != old.PolicyDigest && parseOr(pol.At).Before(parseOr(old.PolicyAt)):
		return keep(fmt.Sprintf("the delivery policy %s now names %s signed at %s, older than %s", p.Policy, h.Digest, pol.At, old.PolicyAt))
	}
	return &pol, h.Digest, ""
}

// verifiedPolicy reads and verifies the policy at digest, once per process:
// a digest a status names is verified again after a restart.
func (p *Publishers) verifiedPolicy(ctx context.Context, reg publish.Registry, digest string) (delivery.PublisherPolicy, error) {
	p.mu.Lock()
	pol, ok := p.policies[digest]
	p.mu.Unlock()
	if ok {
		return pol, nil
	}
	bundle, _, err := reg.Layer(ctx, policyRepo(p.Policy), digest)
	if err == nil {
		pol, err = delivery.OpenPolicy(bundle, p.PolicyVerify)
	}
	if err != nil {
		return delivery.PublisherPolicy{}, err
	}
	p.mu.Lock()
	if p.policies == nil {
		p.policies = map[string]delivery.PublisherPolicy{}
	}
	p.policies[digest] = pol
	p.mu.Unlock()
	return pol, nil
}

// policyRepo is the repository of a policy reference, repo:tag or
// repo@digest.
func policyRepo(ref string) string {
	if repo, _, ok := strings.Cut(ref, "@"); ok {
		return repo
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[:i]
	}
	return ref
}

// pinsFor reads svc's pins and the high-water mark they move to. When the
// newest readable pin is older than the Stage's mark (pinAt), the newer one
// was deleted: no pin is used, so deleting a pin never brings an older
// signer back.
func (p *Publishers) pinsFor(ctx context.Context, svc delivery.ServiceRef, old *delivery.PublisherStatus) (pins []delivery.IdentityPin, digest, at, why string) {
	if p.Pins == nil {
		return nil, "", "", ""
	}
	l, err := p.Pins(ctx, svc)
	if err != nil {
		return nil, "", "", "the pins of " + svc.Name + " could not be read (" + err.Error() + "), so no pinned signer is accepted"
	}
	if old.PinAt != "" && (len(l.Pins) == 0 || parseOr(l.Pins[0].Pin.At).Before(parseOr(old.PinAt))) {
		return nil, "", "", "the newest pin " + old.PinDigest + " signed at " + old.PinAt + " is gone; no older pin is used"
	}
	for _, x := range l.Pins {
		pins = append(pins, x.Pin)
	}
	if len(l.Unreadable) > 0 {
		why = fmt.Sprintf("%d pins of %s could not be used: %s", len(l.Unreadable), svc.Name, strings.Join(l.Unreadable, "; "))
	}
	if len(l.Pins) == 0 {
		return nil, "", "", why
	}
	return pins, l.Pins[0].Manifest, l.Pins[0].Pin.At, why
}

// PinVerifier is the verifier of identity pins: the keys of pinSigner, this
// cluster's recorder (the chart's pin-signer.yaml), and those keys of
// recordTrust, the recorder's trust config, that list an explicit pins/
// stream. Either may be empty; ok is false when neither names a key.
func PinVerifier(pinSigner, recordTrust []byte) (verify delivery.Verifier, ok bool, err error) {
	var keys delivery.TrustConfig
	for i, raw := range [][]byte{pinSigner, recordTrust} {
		if len(raw) == 0 {
			continue
		}
		t, err := delivery.ParseTrust(raw)
		if err != nil {
			return nil, false, err
		}
		if i == 1 {
			t = t.PinKeys()
		}
		keys.Keys = append(keys.Keys, t.Keys...)
	}
	if len(keys.Keys) == 0 {
		return nil, false, nil
	}
	verify, err = signing.TrustVerifier(keys)
	return verify, err == nil, err
}

// PinReader reads a service's pins from the streams repository repo: each
// verified with verify as a record of the service's pin stream (this
// cluster's recorder key, or a recorder.trust key scoped to pins/), and each
// citing an approval record that verifies: a person's keyless record against
// policy, a recorder-signed one with the same keys as the pin.
func PinReader(repo string, opts ocireferrer.Options, verify delivery.Verifier, policy delivery.ClassPolicy) func(context.Context, delivery.ServiceRef) (deliveryrecord.PinListing, error) {
	reader := &deliveryrecord.Reader{} // one per process: a manifest read once is never fetched again
	return func(ctx context.Context, svc delivery.ServiceRef) (deliveryrecord.PinListing, error) {
		r, err := ocireferrer.Open(repo, opts)
		if err != nil {
			return deliveryrecord.PinListing{}, err
		}
		ctx, cancel := delivery.WithRegistryTimeout(ctx)
		defer cancel()
		stream := delivery.PinStream(svc)
		v := func(b []byte) error { return verify(stream, 0, b) }
		return reader.ListPins(ctx, r, svc, v, approvalAt(opts, policy, v))
	}
}

// approvalAt reads and verifies the approval record a pin cites and names
// who decided: the person a keyless record's certificate names, or the
// person a recorder-signed record was signed for.
func approvalAt(opts ocireferrer.Options, policy delivery.ClassPolicy, verify func([]byte) error) deliveryrecord.ApprovalCheck {
	open := func(path string) (*remote.Repository, error) { return ocireferrer.Open(path, opts) }
	return deliveryrecord.PinApproval(open, func(bundle []byte) (delivery.ApprovalStatement, string, error) {
		s, err := openSigned(bundle, policy)
		if why := s.proposal(); err == nil && why != "" {
			err = errors.New(why)
		}
		return s.st, s.identity.User, err
	}, verify)
}

// trust is the trust operation a pass raises, or why none is raised.
type trust struct {
	name string
	spec *delivery.OperationSpec
	why  string
}

// trustAsk is the trust operation the newest digest the watch did not
// accept asks for, when a CI identity no rule, pin or policy covers signed
// it: one per identity (its name), and one pending per Stage, so a stream of
// tags signed by arbitrary CI identities never floods the inbox. A pinned
// service whose next signer differs is asked again, never accepted.
func (r *StageReconciler) trustAsk(ctx context.Context, stage *dv1.Stage, verdicts []delivery.PublisherVerdict, b basis) (trust, error) {
	i := slices.IndexFunc(verdicts, func(v delivery.PublisherVerdict) bool { return v.Verdict != delivery.VerdictAccepted })
	if i < 0 || verdicts[i].Verdict != delivery.VerdictNewSigner {
		return trust{}, nil
	}
	v := verdicts[i]
	if r.Publishers.CannotPin != "" {
		return trust{why: r.Publishers.CannotPin}, nil
	}
	name := delivery.TrustOperationName(stage.Name, *v.Identity)
	ops, err := stageOps(ctx, r.Client, stage.Namespace, stage.Name)
	if err != nil {
		return trust{}, err
	}
	for _, o := range ops {
		if o.Spec.Type == delivery.TypeTrust && o.Status.Phase != delivery.PhaseConcluded && o.Name != name {
			return trust{why: "the trust operation " + o.Name + " is pending, so " + v.Identity.Subject + " is asked once it concludes"}, nil
		}
	}
	reason := "new signer " + v.Identity.Subject
	if len(b.pins) > 0 {
		reason = "signer changed from " + b.pins[0].Subject + " to " + v.Identity.Subject
	}
	return trust{name: name, spec: &delivery.OperationSpec{Type: delivery.TypeTrust, Stage: stage.Name, Facet: delivery.FacetTrust,
		Params: delivery.Params{From: stage.Status.Pointer, To: v.Digest, Publisher: v.Identity}, Reason: reason,
		Actor: delivery.Actor{User: r.Policy.ControllerUser}}}, nil
}
