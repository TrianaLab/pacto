package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// approvalsSuffix is what the chart's default approvals repository adds to
// the streams repository (delivery.recorder.approvalsRepository).
const approvalsSuffix = "-approvals"

// SplitRecordRef splits a record reference, <repository>:<tag> or
// <repository>@<digest>, into its repository and reference.
func SplitRecordRef(ref string) (repository, reference string, err error) {
	if repo, dg, ok := strings.Cut(ref, "@"); ok && repo != "" && dg != "" {
		return repo, dg, nil
	}
	slash, colon := strings.LastIndex(ref, "/"), strings.LastIndex(ref, ":")
	if slash <= 0 || colon < slash || colon == len(ref)-1 {
		return "", "", fmt.Errorf("%q is not a record reference: want <repository>:<tag> or <repository>@<digest>", ref)
	}
	return ref[:colon], ref[colon+1:], nil
}

// RecordAt reads the record ref names in the registry, with no cluster: an
// operation, approval or intent record. Operation and intent records, and
// approvals the recorder signed, verify with the trust config; an approval a
// person signed verifies against the installation's approver signers. A
// record that does not verify is answered, unverified and with why; a
// registry that cannot be read is an error. For a verified approval it reads
// the operation's stream to say whether the approval counted.
func (d *Delivery) RecordAt(ctx context.Context, ref string) (*delivery.RecordAt, error) {
	path, reference, err := SplitRecordRef(ref)
	if err != nil {
		return nil, err
	}
	if d.Records == nil {
		return nil, errors.New("this client reads no registry")
	}
	repo, err := d.Records(path)
	if err != nil {
		return nil, err
	}
	bounded, cancel := delivery.WithRegistryTimeout(ctx)
	defer cancel()
	predicate, bundle, err := deliveryrecord.ReadAt(bounded, repo, reference)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", ref, err)
	}
	at := &delivery.RecordAt{Ref: ref, Predicate: predicate, AsOf: d.Now().UTC().Format(time.RFC3339)}
	switch predicate {
	case delivery.PredicateOperation:
		err = d.operationAt(at, bundle)
	case delivery.PredicateIntent:
		err = d.intentAt(at, bundle)
	default:
		err = d.approvalAt(ctx, at, path, bundle)
	}
	return at, err
}

// noTrust is why a record is unverified when no trust config was given.
const noTrust = "no trust config was given, so its signature was not checked; pass --trust with the recorder's public key"

// trusted verifies with the trust config, or says it cannot.
func (d *Delivery) trusted(at *delivery.RecordAt, open func(delivery.Verifier) error) {
	if d.Trust == nil {
		at.Note = noTrust
		return
	}
	if err := open(d.RecordVerifier()); err != nil {
		at.Note = "unverified: " + err.Error()
		return
	}
	at.Verified, at.Signer = true, "the recorder (trust config "+d.Trust.Digest()+")"
	at.Note = "signature verified with trust config " + d.Trust.Digest()
}

func (d *Delivery) operationAt(at *delivery.RecordAt, bundle []byte) error {
	st, id, err := delivery.OpenRecord(bundle, deliveryrecord.Unchecked)
	if err != nil {
		return err
	}
	d.trusted(at, func(v delivery.Verifier) error { _, _, err := delivery.OpenRecord(bundle, v); return err })
	trust := ""
	if at.Verified {
		trust = d.Trust.Digest()
	}
	stage := st.Predicate.Intent.Stage
	view := delivery.RecordViewOf(stage.Namespace, stage.Name, delivery.StoredRecord{ID: id, Statement: st}, trust, at.AsOf)
	view.Note = at.Note
	at.Operation = &view
	return nil
}

func (d *Delivery) intentAt(at *delivery.RecordAt, bundle []byte) error {
	st, _, err := delivery.OpenIntent(bundle, deliveryrecord.Unchecked)
	if err != nil {
		return err
	}
	d.trusted(at, func(v delivery.Verifier) error { _, _, err := delivery.OpenIntent(bundle, v); return err })
	at.Intent = &st
	return nil
}

// approvalAt verifies an approval record by who says they signed it, and
// says whether it counted.
func (d *Delivery) approvalAt(ctx context.Context, at *delivery.RecordAt, path string, bundle []byte) error {
	st, id, err := delivery.OpenApproval(bundle, func([]byte) error { return nil })
	if err != nil {
		return err
	}
	at.Approval = &st
	p := st.Predicate
	streams, subject := path, ""
	if p.Signer.Kind == delivery.SignerPerson {
		// ponytail: a person's approval lives in the approvals repository; its
		// operation records are read from the chart's default streams
		// repository beside it. Another name is unknown.
		streams = strings.TrimSuffix(path, approvalsSuffix)
		subject = d.personAt(ctx, at, bundle)
	} else {
		d.trusted(at, func(v delivery.Verifier) error {
			_, _, err := delivery.OpenApproval(bundle, func(b []byte) error { return v(p.Operation.Stage.Stream(), 0, b) })
			return err
		})
		if at.Verified && p.Signer.OnBehalfOf != nil {
			at.Signer = "the recorder on behalf of " + p.Signer.OnBehalfOf.User + " (verified by " + p.Signer.OnBehalfOf.Verified + ")"
		}
	}
	if !at.Verified {
		return nil
	}
	if at.SignerClass != "" && !delivery.ReadsAsDecision(at.SignerClass) {
		at.Note = "proposal recorded (signature verified): only a person's approval decides"
		at.Counted = delivery.NotCounted
		at.CountedWhy = "not counted (signed by " + string(at.SignerClass) + " " + subject + ": a proposal)"
		return nil
	}
	at.Note = "decision recorded (signature verified); it counted only if the operation record lists it"
	if streams == path && p.Signer.Kind == delivery.SignerPerson {
		at.Counted, at.CountedWhy = delivery.CountedUnknown, "the operation records' repository is not known from "+path+
			", which does not carry the chart's default name <streams repository>"+approvalsSuffix
		return nil
	}
	at.Counted, at.CountedWhy = d.counted(ctx, streams, p.Operation, id)
	return nil
}

// personAt verifies a person's keyless approval against the installation's
// approver signers and returns the subject its certificate names.
func (d *Delivery) personAt(ctx context.Context, at *delivery.RecordAt, bundle []byte) string {
	policy, perr := d.classPolicy(ctx)
	a, err := signing.OpenPersonApproval(bundle, policy)
	switch {
	case perr != nil:
		at.Note = "unverified: the installation's approver signers could not be read: " + perr.Error()
	case err != nil && d.PolicyNamespace != "":
		at.Note = "unverified: the installation's published identity policy does not accept this signer: " + err.Error()
	case err != nil && len(policy.Signers) == 0:
		at.Note = "unverified: no approver signer was given to verify it with; pass --signers with the installation's identity policy"
	case err != nil:
		at.Note = "unverified: " + err.Error() + "; pass --signers with the installation's identity policy"
	default:
		at.Verified, at.Signer = true, a.SAN+" (keyless, issuer "+a.Issuer+")"
		at.SignerClass, at.ClassifiedBy = a.Class, a.ClassifiedBy
	}
	return a.SAN
}

// counted says whether the operation record of op lists the approval record
// id, read from op's stream in the repository streams. Without a trust
// config, an unreadable stream or no record of op yet it is unknown.
func (d *Delivery) counted(ctx context.Context, streams string, op delivery.ApprovedOperation, id string) (string, string) {
	if d.Trust == nil {
		return delivery.CountedUnknown, "no trust config was given to verify the operation record with"
	}
	stream := op.Stage.Stream()
	repo, err := d.Records(streams)
	var l deliveryrecord.Listing
	if err == nil {
		bounded, cancel := delivery.WithRegistryTimeout(ctx)
		defer cancel()
		l, err = deliveryrecord.ListStream(bounded, repo, stream, d.RecordVerifier())
	}
	if err != nil {
		return delivery.CountedUnknown, "the stream " + stream + " could not be read: " + err.Error()
	}
	i := slices.IndexFunc(l.Records, func(r delivery.StoredRecord) bool {
		return r.Statement.Predicate.Kind == delivery.RecordOperation && r.Statement.Predicate.Intent.UID == op.UID
	})
	if i < 0 {
		return delivery.CountedUnknown, "the stream " + stream + " in " + streams + " holds no record of operation " + op.Name + " yet"
	}
	if slices.ContainsFunc(l.Records[i].Statement.Predicate.Approvals, func(a delivery.RecordApproval) bool { return a.Record == id }) {
		return delivery.Counted, ""
	}
	return delivery.NotCounted, ""
}

// IntentAt reads the intent record ref names and requires it to verify with
// the trust config: approving from it needs no cluster, so the record is all
// that says which operation the decision answers.
func (d *Delivery) IntentAt(ctx context.Context, ref string) (*delivery.IntentStatement, error) {
	at, err := d.RecordAt(ctx, ref)
	switch {
	case err != nil:
		return nil, err
	case at.Intent == nil:
		return nil, fmt.Errorf("%s is not an intent record (%s)", ref, at.Predicate)
	case !at.Verified:
		return nil, fmt.Errorf("the intent record %s does not verify, so it cannot say which operation you approve: %s", ref, at.Note)
	}
	return at.Intent, nil
}

// ApproveIntent records a person's decision on the operation intent names,
// signed keyless (signer) as issuer's san, and pushes it to the approvals
// repository the intent names, with no cluster. It refuses an approver
// ApproverIdentity calls an agent; whether the decision counts the promoter
// decides, as for every keyless approval.
func (d *Delivery) ApproveIntent(ctx context.Context, intent *delivery.IntentStatement, decision delivery.Decision, note string, signer delivery.Signer, issuer, san string) (delivery.ApprovalRecordRef, error) {
	in := intent.Predicate.Intent
	stream := in.Stage.Stream()
	if intent.Predicate.Approvals == "" {
		return delivery.ApprovalRecordRef{}, errors.New("the intent names no approvals repository: its Stage keeps none")
	}
	path, anchor, err := deliveryrecord.ParseAnchorRef(intent.Predicate.Approvals, stream)
	if err != nil {
		return delivery.ApprovalRecordRef{}, err
	}
	policy, err := d.classPolicy(ctx)
	if err != nil {
		return delivery.ApprovalRecordRef{}, err
	}
	id, source, err := delivery.ApproverIdentity(issuer, san, policy)
	if err != nil {
		return delivery.ApprovalRecordRef{}, err
	}
	if code, why := delivery.KeylessRefusal(id, source); code != "" {
		return delivery.ApprovalRecordRef{}, errors.New(code + ": " + why)
	}
	op := delivery.OperationObject{Metadata: delivery.ObjectMeta{Name: in.Name, UID: in.UID}, Spec: delivery.OperationSpec{CorrelationID: in.CorrelationID}}
	spec := delivery.ApprovalSpec{Operation: in.Name, OperationUID: in.UID, IntentDigest: in.Digest, Decision: decision, Note: note, By: id.User}
	repo, err := d.Records(path)
	if err != nil {
		return delivery.ApprovalRecordRef{}, err
	}
	bounded, cancel := delivery.WithRegistryTimeout(ctx)
	defer cancel()
	caps, err := ocireferrer.Probes.Probe(bounded, repo, anchor.Digest.String(), false)
	if err != nil {
		return delivery.ApprovalRecordRef{}, err
	}
	return pushApproval(bounded, signer, repo, stream, caps.Referrers,
		delivery.NewApprovalRecord(in.Stage, anchor.Digest.String(), op, spec, delivery.ApprovalSigner{Kind: delivery.SignerPerson}, d.Now().UTC().Format(time.RFC3339)))
}

// pushApproval signs statement and pushes it, in mode, to repo, the
// approvals repository of stream.
func pushApproval(ctx context.Context, signer delivery.Signer, repo *remote.Repository, stream string, mode capabilities.ReferrersMode, statement delivery.ApprovalStatement) (delivery.ApprovalRecordRef, error) {
	bundle, digest, err := delivery.SignApproval(ctx, signer, statement)
	if err != nil {
		return delivery.ApprovalRecordRef{}, fmt.Errorf("signing the approval: %w", err)
	}
	ref, err := deliveryrecord.Push(ctx, repo, stream, cmp.Or(mode, capabilities.ReferrersNative), delivery.PredicateApproval, bundle,
		map[string]string{deliveryrecord.AnnUID: statement.Predicate.Operation.UID})
	if err != nil {
		return delivery.ApprovalRecordRef{}, fmt.Errorf("pushing the approval to %s: %w", repo.Reference.Repository, err)
	}
	return delivery.ApprovalRecordRef{Digest: digest, Ref: ref}, nil
}
