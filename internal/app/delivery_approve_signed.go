package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// ApprovalTarget is what deciding operation name needs: the operation, its
// Stage and the published identity policy.
func (d *Delivery) ApprovalTarget(ctx context.Context, ns, name string) (*delivery.OperationObject, *delivery.StageObject, delivery.ClassPolicy, error) {
	op, err := d.Get(ctx, ns, name)
	if err != nil {
		return nil, nil, delivery.ClassPolicy{}, err
	}
	st, err := d.Stage(ctx, ns, op.Spec.Stage)
	if err != nil {
		return nil, nil, delivery.ClassPolicy{}, err
	}
	p, err := d.classPolicy(ctx)
	return op, st, p, err
}

// ApproveSigned records a person's decision as an approval record they sign
// keyless (signer), pushed to the Stage's approvals repository, never to its
// operation-record stream. issuer and san are the certificate's OIDC issuer
// and email. It refuses an approver ApproverIdentity calls an agent first,
// then any decision the promoter would not count, as Approve does.
func (d *Delivery) ApproveSigned(ctx context.Context, ns, name string, decision delivery.Decision, note string, signer delivery.Signer, issuer, san string) (delivery.ApprovalRecordRef, error) {
	op, st, policy, err := d.ApprovalTarget(ctx, ns, name)
	if err != nil {
		return delivery.ApprovalRecordRef{}, err
	}
	s := st.Status.Stream
	if s.Approvals == "" {
		return delivery.ApprovalRecordRef{}, errors.New("this Stage keeps no approvals repository: the recorder is off or has not anchored it yet")
	}
	path, anchor, err := deliveryrecord.ParseAnchorRef(s.Approvals, s.ID)
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
	spec := delivery.ApprovalSpec{Operation: name, OperationUID: op.Metadata.UID, IntentDigest: op.Spec.IntentDigest,
		Decision: decision, Note: note, By: id.User}
	access := d.WorkloadAccess(ctx, ns, st.Spec.WorkloadOf())
	freshness := delivery.PublishedFreshness(st.Status.AuthFreshness)
	if code, why := delivery.ApprovalRefusal(op.Spec, op.Status, op.Metadata.UID, spec, d.Now(), policy, freshness, access); code != "" {
		return delivery.ApprovalRecordRef{}, errors.New("the promoter would not count this decision, so none was signed: " + why)
	}
	cluster, _, _ := strings.Cut(s.ID, "/")
	stage := delivery.RecordStage{Cluster: cluster, Namespace: ns, Name: st.Metadata.Name, UID: cmp.Or(op.Status.StageUID, st.Metadata.UID)}
	statement := delivery.NewApprovalRecord(stage, anchor.Digest.String(), *op, spec,
		delivery.ApprovalSigner{Kind: delivery.SignerPerson}, d.Now().UTC().Format(time.RFC3339))
	bundle, digest, err := delivery.SignApproval(ctx, signer, statement)
	if err != nil {
		return delivery.ApprovalRecordRef{}, fmt.Errorf("signing the approval: %w", err)
	}
	repo, err := d.Records(path)
	if err != nil {
		return delivery.ApprovalRecordRef{}, err
	}
	mode := capabilities.ReferrersMode(cmp.Or(s.RegistryMode, string(capabilities.ReferrersNative)))
	bounded, cancel := delivery.WithRegistryTimeout(ctx)
	defer cancel()
	ref, err := deliveryrecord.Push(bounded, repo, s.ID, mode, delivery.PredicateApproval, bundle,
		map[string]string{deliveryrecord.AnnUID: op.Metadata.UID})
	if err != nil {
		return delivery.ApprovalRecordRef{}, fmt.Errorf("pushing the approval to %s: %w", path, err)
	}
	return delivery.ApprovalRecordRef{Digest: digest, Ref: ref}, nil
}
