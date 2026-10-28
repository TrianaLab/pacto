package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/fields"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// RecordRepo opens a repository for reading records, reaching insecureHosts
// over plain HTTP. ocireferrer.Open with the caller's keychain is the
// production one.
type RecordRepo func(path string, insecureHosts ...string) (*remote.Repository, error)

var errNoTrust = errors.New("record verify needs a trust config: pass --trust with the recorder's public key or a trust config file")

// precedentLimit is how many recorded operations a plan's precedent lists.
const precedentLimit = 5

// streamRead is one Stage's records, folded, or why they could not be read.
type streamRead struct {
	delivery.RecordsRead
	anchor string
}

// readStream reads a Stage's stream from its anchor. nil means the Stage has
// no records to read: no recorder wrote its anchor, or this client reads no
// registry. A registry that could not be read is an error, never no records.
// A read is O(records); with Streams set, a stream whose head has not moved
// is read once.
func (d *Delivery) readStream(ctx context.Context, stage *delivery.StageObject) (*streamRead, error) {
	s := stage.Status.Stream
	if d.Records == nil || s.Anchor == "" {
		return nil, nil
	}
	key := d.streamKey(s)
	if r := d.Streams.get(key); r != nil {
		return r, nil
	}
	path, _, err := deliveryrecord.ParseAnchorRef(s.Anchor, s.ID)
	if err != nil {
		return nil, err
	}
	repo, err := d.Records(path)
	if err != nil {
		return nil, err
	}
	bounded, cancel := delivery.WithRegistryTimeout(ctx)
	defer cancel()
	l, err := deliveryrecord.ListStream(bounded, repo, s.ID, d.verifier())
	if err != nil {
		return nil, fmt.Errorf("reading the records of %s: %w", s.Anchor, err)
	}
	r := &streamRead{RecordsRead: delivery.RecordsRead{Fold: delivery.FoldStream(l.Records), Unreadable: l.Unreadable}, anchor: s.Anchor}
	if d.Trust != nil {
		r.Trust = d.Trust.Digest()
	}
	if s.Head != "" && !slices.ContainsFunc(r.Fold.Records, func(x delivery.StoredRecord) bool { return x.ID == s.Head }) {
		r.Fold.Problems = append(r.Fold.Problems, "the head "+s.Head+" the Stage names is not in the registry: it lost history the cluster saw")
	}
	d.Streams.put(key, r)
	return r, nil
}

// streamKey names a stream's content: anchor, stream, head and trust. A
// Stage that names no head yet has no fixed content, so it gets no key.
func (d *Delivery) streamKey(s delivery.Stream) string {
	if s.Head == "" {
		return ""
	}
	trust := ""
	if d.Trust != nil {
		trust = d.Trust.Digest()
	}
	return strings.Join([]string{s.Anchor, s.ID, s.Head, trust}, "\x00")
}

// verifier checks records against the trust config, or checks nothing when
// there is none, which every answer then says.
func (d *Delivery) verifier() delivery.Verifier {
	if d.Trust == nil {
		return deliveryrecord.Unchecked
	}
	v, _ := signing.TrustVerifier(*d.Trust) // validated when the trust config was parsed
	return v
}

// history reads a Stage's history and returns, with it, the records and the
// live operations it was built from. st is the Stage when the caller already
// read it; nil reads it.
func (d *Delivery) history(ctx context.Context, ns, stage string, st *delivery.StageObject) (*delivery.History, *streamRead, []delivery.OperationObject, error) {
	if st == nil {
		var err error
		if st, err = d.Stage(ctx, ns, stage); err != nil {
			return nil, nil, nil, err
		}
	}
	ops, err := d.operationsWhere(ctx, ns, fields.OneTermEqualSelector("spec.stage", stage),
		func(o delivery.OperationObject) bool { return o.Spec.Stage == stage })
	if err != nil {
		return nil, nil, nil, err
	}
	r, readErr := d.readStream(ctx, st)
	var rec *delivery.RecordsRead
	if r != nil {
		rec = &r.RecordsRead
	}
	h, ops := delivery.AssembleHistory(st, ops, rec, readErr, d.Now().UTC().Format(time.RFC3339))
	return h, r, ops, nil
}

// Blame says who put digest in place on a Stage, by default the version it
// runs now: the newest change that moved the Stage to it.
func (d *Delivery) Blame(ctx context.Context, ns, stage, digest string) (*delivery.Blame, error) {
	h, r, ops, err := d.history(ctx, ns, stage, nil)
	if err != nil {
		return nil, err
	}
	var recs []delivery.StoredRecord
	if r != nil {
		recs = r.Fold.Records
	}
	return delivery.BlameOf(h, recs, ops, digest), nil
}

// Undo plans how to undo an operation and proposes nothing. stage is needed
// only when the Operation is gone from the cluster and must be found in the
// records.
func (d *Delivery) Undo(ctx context.Context, ns, name, stage string) (*delivery.Undo, error) {
	op, gone, err := d.undoTarget(ctx, ns, name, stage)
	if err != nil {
		return nil, err
	}
	var st *delivery.StageObject
	if op.Status.Pointer.After != "" {
		if st, err = d.Stage(ctx, ns, op.Spec.Stage); err != nil {
			return nil, err
		}
	}
	u, req := delivery.UndoOf(op, gone, st)
	if req == nil {
		return u, nil
	}
	plan, _, err := d.Plan(ctx, *req)
	if err != nil {
		return nil, err
	}
	u.Planned(*req, plan)
	return u, nil
}

// undoTarget is the operation undo works on: from the cluster, or, when it is
// gone, rebuilt from the records of stage.
func (d *Delivery) undoTarget(ctx context.Context, ns, name, stage string) (*delivery.OperationObject, bool, error) {
	op, err := d.Get(ctx, ns, name)
	if err == nil {
		return op, false, nil
	}
	if stage == "" {
		return nil, true, fmt.Errorf("%w; if it was deleted after it was recorded, name its Stage to find it in the records", err)
	}
	op, err = d.recordedOperation(ctx, ns, stage, name)
	return op, true, err
}

// recordedOperation rebuilds what undo needs of an operation from its record.
func (d *Delivery) recordedOperation(ctx context.Context, ns, stage, name string) (*delivery.OperationObject, error) {
	_, r, _, err := d.history(ctx, ns, stage, nil)
	if err != nil {
		return nil, err
	}
	if r != nil {
		for _, rec := range slices.Backward(r.Fold.Records) {
			p := rec.Statement.Predicate
			if p.Intent.Name != name || p.Outcome == nil {
				continue
			}
			o := &delivery.OperationObject{Metadata: delivery.ObjectMeta{Name: name, Namespace: ns, UID: p.Intent.UID}}
			o.Spec.Stage, o.Status.Pointer = stage, p.Outcome.Pointer
			return o, nil
		}
	}
	return nil, fmt.Errorf("operation %s/%s is not in the cluster and no record of Stage %s names it", ns, name, stage)
}

// VerifyRecords verifies every record of a Stage's stream against the trust
// config and checks the chain.
func (d *Delivery) VerifyRecords(ctx context.Context, ns, stage string) (*delivery.StreamVerification, error) {
	if d.Trust == nil {
		return nil, errNoTrust
	}
	st, err := d.Stage(ctx, ns, stage)
	if err != nil {
		return nil, err
	}
	r, err := d.readStream(ctx, st)
	switch {
	case err != nil:
		return nil, err
	case r == nil:
		return nil, fmt.Errorf("Stage %s/%s has no records: the recorder has not written its stream anchor", ns, stage)
	}
	v := &delivery.StreamVerification{Stage: ns + "/" + stage, Stream: st.Status.Stream.ID, Anchor: r.anchor,
		AsOf: d.Now().UTC().Format(time.RFC3339), Head: r.Fold.Head, Trust: d.Trust.Digest(),
		Problems:   slices.Concat(r.Fold.Problems, delivery.UnrecordableProblems(r.Fold.Records)),
		Unreadable: r.Unreadable, Records: []delivery.RecordCheck{}}
	v.OK = len(v.Problems) == 0 && len(v.Unreadable) == 0
	runners, _ := d.Trust.Runners(signing.OpenKey) // validated when the trust config was parsed
	for _, rec := range r.Fold.Records {
		p := rec.Statement.Predicate
		check := delivery.RecordCheck{Seq: p.Chain.Seq, Record: rec.ID, Manifest: rec.Manifest, Kind: p.Kind,
			Operation: p.Intent.Name, Guarded: p.Guarded, Runners: runnerChecks(p, runners, d.Trust.RunnerProviders())}
		for _, rc := range check.Runners {
			v.OK = v.OK && rc.OK
		}
		v.Records = append(v.Records, check)
	}
	repo, anchor, _ := strings.Cut(r.anchor, "@")
	manifest := "<record manifest>"
	if len(v.Records) > 0 {
		manifest = v.Records[0].Manifest
	}
	signer := "--key recorder.pub"
	if k, ok := d.Trust.For(v.Stream); ok && k.Identity != "" {
		signer = fmt.Sprintf("--trusted-root trusted_root.json --certificate-identity '%s' --certificate-oidc-issuer '%s' --use-signed-timestamps",
			k.Identity, k.Issuer)
		if signing.WithoutSCT(k.TrustedRoot) {
			signer += " --insecure-ignore-sct"
		}
	}
	v.Cosign = fmt.Sprintf("oras blob fetch --output record.json %[1]s@$(oras manifest fetch %[1]s@%[2]s | jq -r '.layers[0].digest') && "+
		"cosign verify-blob-attestation --bundle record.json %[5]s --type %[3]s --insecure-ignore-tlog "+
		"--digest %[4]s --digestAlg sha256", repo, manifest, delivery.PredicateOperation, strings.TrimPrefix(anchor, "sha256:"), signer)
	return v, nil
}

// runnerChecks checks the runner's own signature on every eval verdict of
// record p: one that keeps no signed answer, or whose runner no runner key of
// the trust config vouches for, does not verify, so who ran the exam and what
// it found is never taken on the promoter's word.
func runnerChecks(p delivery.RecordPredicate, runners delivery.RunnerVerifier, providers []string) []delivery.RunnerCheck {
	var out []delivery.RunnerCheck
	for _, x := range delivery.EvalVerdicts(p, providers) {
		rc := delivery.RunnerCheck{Gate: x.Gate, Cited: x.Citation["runner"]}
		runner, err := delivery.CheckRunner(x, p, runners)
		rc.Runner, rc.OK = runner, err == nil
		if err != nil {
			rc.Problem = err.Error()
		}
		out = append(out, rc)
	}
	return out
}

// precedent is what the records attached to a version say happened to it:
// the newest recorded operations, on any Stage, whose target it was.
func (d *Delivery) precedent(ctx context.Context, stage *delivery.StageObject, to string) *delivery.Precedent {
	if d.Records == nil || stage.Status.Stream.Anchor == "" {
		return nil
	}
	p := &delivery.Precedent{Completeness: "partial"}
	l, err := d.subjectRecords(ctx, stage, to)
	if err != nil {
		p.Note = "the records of " + to + " could not be read: " + err.Error()
		return p
	}
	for _, rec := range l.Records {
		pr := rec.Statement.Predicate
		if pr.Intent.Params == nil || pr.Intent.Params.To != to || pr.Outcome == nil {
			continue
		}
		e := delivery.PrecedentEntry{Stream: pr.Chain.Stream, Seq: pr.Chain.Seq, Outcome: pr.Outcome.Result,
			Verification: pr.Outcome.Verification, Reason: pr.Outcome.Reason, Finished: pr.Time.Finished, Record: rec.ID}
		if pr.Type != nil {
			e.Type = pr.Type.ID
		}
		p.Entries = append(p.Entries, e)
	}
	slices.SortStableFunc(p.Entries, func(a, b delivery.PrecedentEntry) int { return strings.Compare(b.Finished, a.Finished) })
	p.Entries = p.Entries[:min(len(p.Entries), precedentLimit)]
	switch {
	case d.Trust == nil:
		p.Note = "no trust config was given, so no signature was checked"
	case len(l.Unreadable) > 0:
		p.Note = "some records of this version could not be verified: " + strings.Join(l.Unreadable, "; ")
	default:
		p.Completeness = "complete"
		p.Note = fmt.Sprintf("the newest %d recorded operations whose target was this version, on any Stage", len(p.Entries))
	}
	return p
}

// subjectRecords reads the records attached to digest in the Stage's apply
// repository.
func (d *Delivery) subjectRecords(ctx context.Context, stage *delivery.StageObject, digest string) (deliveryrecord.Listing, error) {
	path, insecure, err := d.applyRepo(ctx, stage)
	if err != nil {
		return deliveryrecord.Listing{}, err
	}
	repo, err := d.Records(path, insecure...)
	if err != nil {
		return deliveryrecord.Listing{}, err
	}
	bounded, cancel := delivery.WithRegistryTimeout(ctx)
	defer cancel()
	desc, err := repo.Resolve(bounded, digest)
	if err != nil {
		return deliveryrecord.Listing{}, err
	}
	return deliveryrecord.List(bounded, repo, desc, d.verifier())
}

// A fleet audit walks every Stage in the caller's scope. One walk, the
// namespace listing included, reads at most auditLimit Stages, auditParallel
// at once, and gives up on what is left after auditTimeout; what it did not
// read makes the answer partial.
const (
	auditLimit    = 500
	auditParallel = 8
	auditTimeout  = 2 * time.Minute
)

// audit runs read on every Stage, bounded as above. out[i] is nil when Stage
// i could not be read; unreadable says which and why.
func audit[T any](ctx context.Context, stages []delivery.StageObject, read func(context.Context, delivery.StageObject) (*T, error)) (out []*T, unreadable []string) {
	ctx, cancel := context.WithTimeout(ctx, auditTimeout)
	defer cancel()
	if len(stages) > auditLimit {
		unreadable = append(unreadable, fmt.Sprintf("%d Stages were not read: one walk reads at most %d", len(stages)-auditLimit, auditLimit))
		stages = stages[:auditLimit]
	}
	out, errs := make([]*T, len(stages)), make([]error, len(stages))
	var g errgroup.Group
	g.SetLimit(auditParallel)
	for i, s := range stages {
		g.Go(func() error {
			out[i], errs[i] = read(ctx, s)
			return nil
		})
	}
	_ = g.Wait()
	for i, err := range errs {
		if err != nil {
			unreadable = append(unreadable, stages[i].Metadata.Namespace+"/"+stages[i].Metadata.Name+": "+err.Error())
		}
	}
	return out, unreadable
}

// HistoryAll reads the history of every Stage in namespace, or in every
// namespace the caller can list for "".
func (d *Delivery) HistoryAll(ctx context.Context, namespace string) (*delivery.Histories, error) {
	ctx, cancel := context.WithTimeout(ctx, auditTimeout) // the walk counts against the audit's time
	defer cancel()
	stages, forbidden, err := d.auditStages(ctx, namespace)
	if err != nil {
		return nil, err
	}
	out := d.Histories(ctx, stages)
	if len(forbidden) > 0 {
		out.Unreadable, out.Completeness = append(forbidden, out.Unreadable...), "partial"
		out.Note += "; the namespaces listed as unreadable were not read, so a change in them may still have happened"
	}
	return out, nil
}

// auditStages lists the Stages a fleet audit walks: those in namespace, or in
// every namespace for "". A caller who may not list Stages cluster-wide gets
// the Stages of each namespace it can read, and every namespace it cannot is
// named in forbidden.
func (d *Delivery) auditStages(ctx context.Context, namespace string) (stages []delivery.StageObject, forbidden []string, err error) {
	stages, err = d.Stages(ctx, namespace)
	if namespace != "" || !apierrors.IsForbidden(err) {
		return stages, nil, err
	}
	type named struct {
		Metadata delivery.ObjectMeta `json:"metadata"`
	}
	nss, nerr := listInto[named](ctx, d.Dynamic.Resource(gvrNamespaces), "", "namespaces")
	if nerr != nil {
		return nil, nil, fmt.Errorf("%w; walking the namespaces one by one instead: %w; name one with -n", err, nerr)
	}
	per, errs := make([][]delivery.StageObject, len(nss)), make([]error, len(nss))
	var read atomic.Int64
	var g errgroup.Group
	g.SetLimit(auditParallel)
	for i, ns := range nss {
		g.Go(func() error {
			per[i], errs[i] = d.namespaceStages(ctx, ns.Metadata.Name, &read)
			return nil
		})
	}
	_ = g.Wait()
	for i, err := range errs {
		if err != nil {
			forbidden = append(forbidden, err.Error()) // it names the namespace
		}
		stages = append(stages, per[i]...)
	}
	return stages, forbidden, nil
}

// namespaceStages lists one namespace's Stages for the walk, unless the walk
// already read auditLimit Stages or ran out of time; read counts them.
func (d *Delivery) namespaceStages(ctx context.Context, ns string, read *atomic.Int64) ([]delivery.StageObject, error) {
	switch {
	case read.Load() >= auditLimit:
		return nil, fmt.Errorf("namespace %s was not read: one walk reads at most %d Stages", ns, auditLimit)
	case ctx.Err() != nil:
		return nil, fmt.Errorf("namespace %s was not read: %w", ns, ctx.Err())
	}
	s, err := d.Stages(ctx, ns)
	read.Add(int64(len(s)))
	return s, err
}

// Histories reads the history of each of stages.
func (d *Delivery) Histories(ctx context.Context, stages []delivery.StageObject) *delivery.Histories {
	hs, unreadable := audit(ctx, stages, func(ctx context.Context, s delivery.StageObject) (*delivery.History, error) {
		return d.HistoryOf(ctx, s)
	})
	out := &delivery.Histories{AsOf: d.Now().UTC().Format(time.RFC3339), Completeness: "complete", Unreadable: unreadable,
		Histories: []delivery.StageHistory{}}
	for i, h := range hs {
		if h == nil {
			continue
		}
		out.Histories = append(out.Histories, delivery.StageHistory{Namespace: stages[i].Metadata.Namespace, History: *h})
		if h.Completeness != "complete" {
			out.Completeness = "partial"
		}
	}
	out.Note = fmt.Sprintf("the histories of %d of %d Stages", len(out.Histories), len(stages))
	if len(unreadable) > 0 {
		out.Completeness = "partial"
		out.Note += "; the Stages listed as unreadable were not read, so a change on them may still have happened"
	}
	return out
}

// VerifyAll verifies the stream of every Stage in namespace, or in every
// namespace the caller can list for "". A Stage with no records does not
// verify.
func (d *Delivery) VerifyAll(ctx context.Context, namespace string) (*delivery.StreamVerifications, error) {
	if d.Trust == nil {
		return nil, errNoTrust
	}
	ctx, cancel := context.WithTimeout(ctx, auditTimeout)
	defer cancel()
	stages, forbidden, err := d.auditStages(ctx, namespace)
	if err != nil {
		return nil, err
	}
	vs, unreadable := audit(ctx, stages, func(ctx context.Context, s delivery.StageObject) (*delivery.StreamVerification, error) {
		return d.VerifyRecords(ctx, s.Metadata.Namespace, s.Metadata.Name)
	})
	unreadable = append(forbidden, unreadable...)
	out := &delivery.StreamVerifications{AsOf: d.Now().UTC().Format(time.RFC3339), Completeness: "complete", Trust: d.Trust.Digest(),
		OK: len(unreadable) == 0, Unreadable: unreadable, Streams: []delivery.StreamVerification{}}
	if len(unreadable) > 0 {
		out.Completeness = "partial"
	}
	for _, v := range vs {
		if v != nil {
			out.Streams = append(out.Streams, *v)
			out.OK = out.OK && v.OK
		}
	}
	return out, nil
}
