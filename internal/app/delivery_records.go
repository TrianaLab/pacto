package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	fold       delivery.Fold
	unreadable []string
	anchor     string
}

// readStream reads a Stage's stream from its anchor. nil means the Stage has
// no records to read: no recorder wrote its anchor, or this client reads no
// registry. A registry that could not be read is an error, never no records.
// ponytail: every read fetches every record of the stream, O(records) per
// call; cache verified records by manifest digest (they never change) if
// dashboard polling or long streams show on the registry.
func (d *Delivery) readStream(ctx context.Context, stage *delivery.StageObject) (*streamRead, error) {
	s := stage.Status.Stream
	if d.Records == nil || s.Anchor == "" {
		return nil, nil
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
	r := &streamRead{fold: delivery.FoldStream(l.Records), unreadable: l.Unreadable, anchor: s.Anchor}
	if s.Head != "" && !slices.ContainsFunc(r.fold.Records, func(x delivery.StoredRecord) bool { return x.ID == s.Head }) {
		r.fold.Problems = append(r.fold.Problems, "the head "+s.Head+" the Stage names is not in the registry: it lost history the cluster saw")
	}
	return r, nil
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

// complete is the completeness of an answer read from records.
func (d *Delivery) complete(r *streamRead) string {
	if d.Trust == nil || len(r.fold.Problems) > 0 || len(r.unreadable) > 0 {
		return "partial"
	}
	return "complete"
}

func (d *Delivery) recordsNote(r *streamRead) string {
	switch {
	case d.Trust == nil:
		return "read from the records in the registry, but no trust config was given, so no signature was checked; " +
			"pass --trust to verify them"
	case len(r.fold.Problems) > 0 || len(r.unreadable) > 0:
		return "read from the signed records in the registry, which show problems or hold records that could not be verified; " +
			"a change missing here may still have happened"
	}
	return "read from the signed records in the registry, verified with trust config " + d.Trust.Digest() +
		", plus the operations not recorded yet"
}

// statusEntry is an Operation's status as a history line.
func statusEntry(o delivery.OperationObject) delivery.HistoryEntry {
	s := o.Status
	e := delivery.HistoryEntry{Seq: s.Seq, Operation: o.Metadata.Name, Type: o.Spec.Type, From: o.Spec.Params.From,
		To: o.Spec.Params.To, Actor: o.Spec.Actor.User, ActorClass: s.ActorClass, Phase: s.Phase, Outcome: s.Outcome,
		Verification: s.Verification, Reason: s.Reason, Finished: s.Times.Finished}
	if s.Pointer.After != "" {
		ptr := s.Pointer
		e.Pointer = &ptr
	}
	if s.Record != nil {
		e.UnverifiedRecord = s.Record.Digest
	}
	return e
}

// missingSeqs names the local seqs the Stage concluded after the recorder's
// last that neither a listed Operation nor a conclusion still being written
// holds: their record is not written yet and their Operation is gone, so what
// they did cannot be read at all. An Operation an earlier Stage of the same
// name numbered holds nothing here.
func missingSeqs(st *delivery.StageObject, ops []delivery.OperationObject) []string {
	held, live := map[int64]bool{}, map[string]bool{}
	for _, o := range ops {
		if o.Status.StageUID == "" || o.Status.StageUID == st.Metadata.UID {
			live[o.Metadata.UID], held[o.Status.Seq] = true, true
		}
	}
	for _, c := range st.Status.Stream.Concluded {
		held[c.Seq] = held[c.Seq] || live[c.UID]
	}
	var out []string
	for seq := st.Status.Stream.RecordedSeq + 1; seq <= st.Status.Stream.Seq; seq++ {
		if !held[seq] {
			out = append(out, fmt.Sprintf("local seq %d concluded, but neither its record nor its Operation can be read", seq))
		}
	}
	return out
}

// history reads a Stage's history and returns, with it, the records and the
// live operations it was built from.
func (d *Delivery) history(ctx context.Context, ns, stage string) (*delivery.History, *streamRead, []delivery.OperationObject, error) {
	st, err := d.Stage(ctx, ns, stage)
	if err != nil {
		return nil, nil, nil, err
	}
	ops, err := d.Operations(ctx, ns)
	if err != nil {
		return nil, nil, nil, err
	}
	ops = slices.DeleteFunc(ops, func(o delivery.OperationObject) bool { return o.Spec.Stage != stage })
	slices.SortStableFunc(ops, func(a, b delivery.OperationObject) int {
		return cmp.Or(cmp.Compare(inFlight(a), inFlight(b)), cmp.Compare(a.Status.Seq, b.Status.Seq),
			cmp.Compare(a.Metadata.CreationTimestamp, b.Metadata.CreationTimestamp))
	})
	h := &delivery.History{Stage: stageNow(st), AsOf: d.Now().UTC().Format(time.RFC3339), Completeness: "partial",
		Note: historyNote, Source: delivery.SourceStatus, Entries: []delivery.HistoryEntry{}}
	r, err := d.readStream(ctx, st)
	if err != nil {
		h.Unreadable, h.Note = []string{err.Error()}, "the records could not be read, so this is "+historyNote
	}
	recorded := map[string]bool{}
	if r != nil {
		r.fold.Problems = append(r.fold.Problems, missingSeqs(st, ops)...)
		h.Source, h.Head, h.Problems, h.Unreadable = delivery.SourceRecords, r.fold.Head, r.fold.Problems, r.unreadable
		h.Completeness, h.Note, h.Verified = d.complete(r), d.recordsNote(r), d.Trust != nil
		for _, rec := range r.fold.Records {
			uid := rec.Statement.Predicate.Intent.UID
			recorded[uid] = true
			h.Entries = append(h.Entries, withUnsignedMove(delivery.RecordEntry(rec), ops, uid))
		}
	}
	for _, o := range ops {
		if !recorded[o.Metadata.UID] {
			h.Entries = append(h.Entries, statusEntry(o))
		}
	}
	return h, r, ops, nil
}

// withUnsignedMove fills an unrecordable record's line, which carries no
// params or outcome, from its Operation's status while the Operation is still
// in the cluster. Its reason still says Unrecordable: what it adds is not signed.
func withUnsignedMove(e delivery.HistoryEntry, ops []delivery.OperationObject, uid string) delivery.HistoryEntry {
	i := slices.IndexFunc(ops, func(o delivery.OperationObject) bool { return o.Metadata.UID == uid })
	if e.Kind != delivery.RecordUnrecordable || i < 0 {
		return e
	}
	s := statusEntry(ops[i])
	e.Type, e.From, e.To, e.Actor, e.ActorClass = s.Type, s.From, s.To, s.Actor, s.ActorClass
	e.Outcome, e.Verification, e.Pointer = s.Outcome, s.Verification, s.Pointer
	return e
}

// Blame says who put digest in place on a Stage, by default the version it
// runs now: the newest change that moved the Stage to it.
func (d *Delivery) Blame(ctx context.Context, ns, stage, digest string) (*delivery.Blame, error) {
	h, r, ops, err := d.history(ctx, ns, stage)
	if err != nil {
		return nil, err
	}
	b := &delivery.Blame{Stage: h.Stage, Digest: cmp.Or(digest, h.Stage.Pointer), AsOf: h.AsOf, Completeness: h.Completeness,
		Source: h.Source, Unreadable: h.Unreadable}
	if b.Digest == "" {
		b.Note = "the Stage runs nothing yet"
		return b, nil
	}
	for i := len(h.Entries) - 1; i >= 0; i-- {
		e := h.Entries[i]
		if e.Pointer == nil || e.Pointer.After != b.Digest {
			continue
		}
		b.Change = &e
		b.Note = "the newest change that moved the Stage to " + b.Digest
		if e.Record != "" && e.Kind != delivery.RecordUnrecordable {
			p := recordByID(r, e.Record).Statement.Predicate
			b.Approvals, b.Verdicts, b.IntentReason = p.Approvals, p.Verdicts, p.Intent.Reason
			if p.Actor != nil && p.Actor.OnBehalfOf != nil {
				b.OnBehalfOf = p.Actor.OnBehalfOf.User
			}
			return b, nil
		}
		// Read from the Operation's status: no signed record vouches for it,
		// whatever the history's completeness.
		b.Completeness, b.Unsigned = "partial", []string{"change", "approvals", "verdicts", "intentReason", "onBehalfOf"}
		b.Note += ", read from its Operation's unsigned status"
		if e.Record != "" {
			b.Note += ": its record could not vouch for it (" + e.Reason + ")"
		}
		o := opByName(ops, e.Operation)
		b.Verdicts, b.IntentReason = o.Status.Verdicts, o.Spec.Reason
		for _, a := range o.Status.Approvals {
			b.Approvals = append(b.Approvals, delivery.RecordApproval{By: a.By, Decision: a.Decision, Operation: o.Metadata.UID,
				AuthTime: a.AuthTime, Note: a.Note})
		}
		if o.Spec.OnBehalfOf != nil {
			b.OnBehalfOf = o.Spec.OnBehalfOf.User
		}
		return b, nil
	}
	noChange(b, h.Entries)
	return b, nil
}

// noChange says why no change in entries moved the Stage to b's digest, and
// how sure that is. An unrecordable record whose move is unknown may be the
// change, so it makes the answer partial.
func noChange(b *delivery.Blame, entries []delivery.HistoryEntry) {
	hidden := slices.ContainsFunc(entries, func(e delivery.HistoryEntry) bool {
		return e.Kind == delivery.RecordUnrecordable && e.Pointer == nil
	})
	switch {
	case b.Completeness == "complete" && hidden:
		b.Completeness = "partial"
		b.Note = "no readable change moved the Stage to " + b.Digest + ", but an unrecordable record hides what its " +
			"change moved, and that change may have"
	case b.Completeness == "complete":
		b.Note = "no change in the stream moved the Stage to " + b.Digest
	default:
		b.Note = "no change Pacto could read moved the Stage to " + b.Digest + ", and the history is partial, " +
			"so who did it is unknown, not nobody"
	}
}

func recordByID(r *streamRead, id string) delivery.StoredRecord {
	i := slices.IndexFunc(r.fold.Records, func(x delivery.StoredRecord) bool { return x.ID == id })
	return r.fold.Records[i]
}

func opByName(ops []delivery.OperationObject, name string) delivery.OperationObject {
	i := slices.IndexFunc(ops, func(o delivery.OperationObject) bool { return o.Metadata.Name == name })
	return ops[i]
}

// Undo plans how to undo an operation and proposes nothing. stage is needed
// only when the Operation is gone from the cluster and must be found in the
// records.
func (d *Delivery) Undo(ctx context.Context, ns, name, stage string) (*delivery.Undo, error) {
	u := &delivery.Undo{Operation: ns + "/" + name, Kind: "none"}
	op, gone, err := d.undoTarget(ctx, ns, name, stage)
	if err != nil {
		return nil, err
	}
	ptr := op.Status.Pointer
	if ptr.After == "" {
		u.Note = "it moved nothing, so there is nothing to undo"
		return u, nil
	}
	st, err := d.Stage(ctx, ns, op.Spec.Stage)
	if err != nil {
		return nil, err
	}
	req := delivery.Request{Namespace: ns, Stage: op.Spec.Stage, Reason: "undo " + name}
	switch {
	case !gone && st.Status.LastOp == op.Metadata.UID && st.Status.Pointer == ptr.After:
		u.Kind, req.Type, req.Undoes = "rollback", delivery.TypeRollback, name
		u.Note = "it is the change the Stage runs now, so a rollback bound to it undoes it"
	case ptr.Before == "":
		u.Note = "it brought the Stage its first version; there is no earlier version to go back to"
		return u, nil
	case gone && st.Status.LastOp == op.Metadata.UID && st.Status.Pointer == ptr.After:
		u.Kind, req.Type, req.To = "promote", delivery.TypePromote, ptr.Before
		u.Note = "the Stage still runs this change, but its Operation is gone, so a rollback cannot name it; going back to " +
			ptr.Before + " is a promote, and every gate runs"
	case st.Status.Pointer == ptr.Before:
		u.Note = "the Stage already runs " + ptr.Before + ", the version it replaced"
		return u, nil
	default:
		u.Kind, req.Type, req.To = "promote", delivery.TypePromote, ptr.Before
		u.Note = "the Stage changed since (it runs " + orDash(st.Status.Pointer) + "), and only the change it runs now can be " +
			"rolled back, so going back to " + ptr.Before + " is a promote, and every gate runs"
	}
	plan, _, err := d.Plan(ctx, req)
	if err != nil {
		return nil, err
	}
	u.Plan = &plan
	u.Command = fmt.Sprintf("pacto delivery propose -n %s --stage %s --type %s", ns, op.Spec.Stage, strings.TrimPrefix(req.Type, "pacto.dev/"))
	if req.Undoes != "" {
		u.Command += " --undoes " + name
	} else {
		u.Command += " --to " + req.To
	}
	u.Command += fmt.Sprintf(" --reason %q --intent-digest %s", req.Reason, plan.IntentDigest)
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
	_, r, _, err := d.history(ctx, ns, stage)
	if err != nil {
		return nil, err
	}
	if r != nil {
		for _, rec := range slices.Backward(r.fold.Records) {
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
		AsOf: d.Now().UTC().Format(time.RFC3339), Head: r.fold.Head, Trust: d.Trust.Digest(), Problems: r.fold.Problems,
		Unreadable: r.unreadable, Records: []delivery.RecordCheck{}}
	v.OK = len(v.Problems) == 0 && len(v.Unreadable) == 0
	for _, rec := range r.fold.Records {
		p := rec.Statement.Predicate
		v.Records = append(v.Records, delivery.RecordCheck{Seq: p.Chain.Seq, Record: rec.ID, Manifest: rec.Manifest, Kind: p.Kind,
			Operation: p.Intent.Name})
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
	}
	v.Cosign = fmt.Sprintf("oras blob fetch --output record.json %[1]s@$(oras manifest fetch %[1]s@%[2]s | jq -r '.layers[0].digest') && "+
		"cosign verify-blob-attestation --bundle record.json %[5]s --type %[3]s --insecure-ignore-tlog "+
		"--digest %[4]s --digestAlg sha256", repo, manifest, delivery.PredicateOperation, strings.TrimPrefix(anchor, "sha256:"), signer)
	return v, nil
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
		return d.History(ctx, s.Metadata.Namespace, s.Metadata.Name)
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
