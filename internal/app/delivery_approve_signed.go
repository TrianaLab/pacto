package app

import (
	"cmp"
	"context"
	"errors"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
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

// SignedApproval is the record ApproveSigned pushed, the form it was written
// in and what the client could not check before signing it.
type SignedApproval struct {
	delivery.ApprovalRecordRef
	Mode   capabilities.ReferrersMode
	Caveat string
}

// ApproveSigned records a person's decision as an approval record they sign
// keyless (signer), pushed to the Stage's approvals repository, never to its
// operation-record stream. issuer and san are the certificate's OIDC issuer
// and email. It refuses an approver ApproverIdentity calls an agent first,
// then any decision the promoter would not count, as Approve does. The
// workload access is asked for the keyless identity itself, with no groups,
// as the promoter asks it; a client that may not ask about another identity
// falls back to its own review and says so in Caveat.
func (d *Delivery) ApproveSigned(ctx context.Context, ns, name string, decision delivery.Decision, note string, signer delivery.Signer, issuer, san string) (SignedApproval, error) {
	op, st, policy, err := d.ApprovalTarget(ctx, ns, name)
	if err != nil {
		return SignedApproval{}, err
	}
	s := st.Status.Stream
	if s.Approvals == "" {
		return SignedApproval{}, errors.New("this Stage keeps no approvals repository: the recorder is off or has not anchored it yet")
	}
	path, anchor, err := deliveryrecord.ParseAnchorRef(s.Approvals, s.ID)
	if err != nil {
		return SignedApproval{}, err
	}
	id, source, err := delivery.ApproverIdentity(issuer, san, policy)
	if err != nil {
		return SignedApproval{}, err
	}
	if code, why := delivery.KeylessRefusal(id, source); code != "" {
		return SignedApproval{}, errors.New(code + ": " + why)
	}
	spec := delivery.ApprovalSpec{Operation: name, OperationUID: op.Metadata.UID, IntentDigest: op.Spec.IntentDigest,
		Decision: decision, Note: note, By: id.User}
	access, caveat := d.keylessAccess(ctx, ns, st.Spec.WorkloadOf(), id.User)
	freshness := delivery.PublishedFreshness(st.Status.AuthFreshness)
	if code, why := delivery.ApprovalRefusal(op.Spec, op.Status, op.Metadata.UID, spec, d.Now(), policy, freshness, access); code != "" {
		return SignedApproval{}, errors.New("the promoter would not count this decision, so none was signed: " + why)
	}
	cluster, _, _ := strings.Cut(s.ID, "/")
	stage := delivery.RecordStage{Cluster: cluster, Namespace: ns, Name: st.Metadata.Name, UID: cmp.Or(op.Status.StageUID, st.Metadata.UID)}
	statement := delivery.NewApprovalRecord(stage, anchor.Digest.String(), *op, spec,
		delivery.ApprovalSigner{Kind: delivery.SignerPerson}, d.Now().UTC().Format(time.RFC3339))
	repo, err := d.Records(path)
	if err != nil {
		return SignedApproval{}, err
	}
	bounded, cancel := delivery.WithRegistryTimeout(ctx)
	defer cancel()
	// The approvals repository may be on another host than the streams
	// repository, so its own probe picks the form, never the stream's mode.
	caps, err := ocireferrer.Probes.Probe(bounded, repo, anchor.Digest.String(), false)
	if err != nil {
		return SignedApproval{}, err
	}
	ref, err := pushApproval(bounded, signer, repo, s.ID, caps.Referrers, statement)
	return SignedApproval{ApprovalRecordRef: ref, Mode: cmp.Or(caps.Referrers, capabilities.ReferrersNative), Caveat: caveat}, err
}

// keylessAccess asks whether user, with no groups, may update w in ns: a
// SubjectAccessReview, as the promoter asks it of a keyless approver. A
// client the apiserver does not let ask about another identity falls back to
// its own SelfSubjectAccessReview, and caveat says what that cannot show.
func (d *Delivery) keylessAccess(ctx context.Context, ns string, w delivery.WorkloadRef, user string) (delivery.WorkloadAccess, string) {
	a, group := delivery.AccessFor(delivery.StageSpec{Workload: &w}, ns)
	if a.Unverified != "" {
		return a, ""
	}
	review := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "authorization.k8s.io/v1", "kind": "SubjectAccessReview",
		"spec": map[string]any{"user": user, "resourceAttributes": map[string]any{
			"namespace": ns, "verb": "update", "group": group, "resource": a.Resource, "name": a.Name}},
	}}
	out, err := d.Dynamic.Resource(gvrSubjectAccess).Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return d.WorkloadAccess(ctx, ns, w), "Pacto could not ask whether " + user + " itself may " + a.Phrase() +
			" (" + err.Error() + "); the promoter counts the decision only if that user, without your groups, may"
	}
	a.Source = delivery.AccessBySubjectReview
	a.Allowed, _, _ = unstructured.NestedBool(out.Object, "status", "allowed")
	if e, _, _ := unstructured.NestedString(out.Object, "status", "evaluationError"); !a.Allowed && e != "" {
		a.Unverified = "the SubjectAccessReview could not be evaluated: " + e
		return a, ""
	}
	a.At = d.Now().UTC().Format(time.RFC3339)
	return a, ""
}
