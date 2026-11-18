package delivery

import (
	"context"
	"encoding/json"
	"strings"
)

// Approval and intent records: in-toto statements on a Stage's record
// stream beside its operation records. An approval record is one decision on
// one operation; an intent record is what an admitted operation asks, so a
// reader with no cluster credentials sees it before it concludes.
const (
	PredicateApproval = "delivery.pacto.trianalab.io/approval/v1"
	PredicateIntent   = "delivery.pacto.trianalab.io/operation-intent/v1"
)

// Who signed an approval record, and how the person it names was verified.
const (
	// SignerPerson: the person's own client signed it keyless.
	SignerPerson = "person"
	// SignerRecorder: the recorder signed it on the person's behalf.
	SignerRecorder = "recorder"
	// VerifiedByAdmission: the admission policy bound the person to the
	// Approval the recorder signed for.
	VerifiedByAdmission = "admission"
)

// ApprovalSigner is who signed an approval record.
type ApprovalSigner struct {
	// Kind is person or recorder.
	Kind string `json:"kind"`
	// OnBehalfOf is the person a recorder-signed record speaks for.
	// +optional
	OnBehalfOf *ApprovalBehalf `json:"onBehalfOf,omitempty"`
}

// ApprovalBehalf is the person a recorder signed for, and who verified them.
type ApprovalBehalf struct {
	User     string `json:"user"`
	Verified string `json:"verified"`
}

// ApprovalStatement is an in-toto Statement v1 carrying an approval record.
type ApprovalStatement struct {
	Type          string            `json:"_type"`
	Subject       []Subject         `json:"subject"`
	PredicateType string            `json:"predicateType"`
	Predicate     ApprovalPredicate `json:"predicate"`
}

// ApprovalPredicate is delivery.pacto.trianalab.io/approval/v1.
type ApprovalPredicate struct {
	Operation     ApprovedOperation `json:"operation"`
	Decision      Decision          `json:"decision"`
	Note          string            `json:"note,omitempty"`
	CorrelationID string            `json:"correlationID,omitempty"`
	Signer        ApprovalSigner    `json:"signer"`
	Time          string            `json:"time"`
}

// ApprovedOperation is the operation an approval record answers.
type ApprovedOperation struct {
	Name         string      `json:"name"`
	UID          string      `json:"uid"`
	IntentDigest string      `json:"intentDigest"`
	Stage        RecordStage `json:"stage"`
}

// IntentStatement is an in-toto Statement v1 carrying an intent record.
type IntentStatement struct {
	Type          string          `json:"_type"`
	Subject       []Subject       `json:"subject"`
	PredicateType string          `json:"predicateType"`
	Predicate     IntentPredicate `json:"predicate"`
}

// IntentPredicate is delivery.pacto.trianalab.io/operation-intent/v1.
type IntentPredicate struct {
	Intent  RecordIntent `json:"intent"`
	Actor   RecordActor  `json:"actor"`
	Guarded bool         `json:"guarded,omitempty"`
	// Approvals is where a client pushes a keyless approval of this
	// operation: <approvals repository>@<anchor digest>.
	Approvals string `json:"approvals,omitempty"`
	Time      string `json:"time"`
}

// NewApprovalRecord builds the record of approval a on op, on the stream
// anchored at anchorDigest. at is when it was decided (RFC 3339): the
// Approval's creation, so building it twice gives the same payload.
func NewApprovalRecord(stage RecordStage, anchorDigest string, op OperationObject, a ApprovalSpec, signer ApprovalSigner, at string) ApprovalStatement {
	return ApprovalStatement{Type: StatementType, Subject: subjects(anchorDigest, ""), PredicateType: PredicateApproval,
		Predicate: ApprovalPredicate{
			Operation: ApprovedOperation{Name: op.Metadata.Name, UID: op.Metadata.UID, IntentDigest: a.IntentDigest, Stage: stage},
			Decision:  a.Decision, Note: a.Note, CorrelationID: op.Spec.CorrelationID, Signer: signer, Time: at,
		}}
}

// NewIntentRecord builds the intent record of the admitted operation op.
// approvals is the Stage's approvals reference; at is when op was admitted.
// An intent whose params or reason look like a secret is recorded without
// them, as an unrecordable operation record is.
func NewIntentRecord(stage RecordStage, anchorDigest, approvals string, op OperationObject, at string) IntentStatement {
	spec, st := op.Spec, op.Status
	params := spec.Params
	in := RecordIntent{UID: op.Metadata.UID, Name: op.Metadata.Name, Digest: spec.IntentDigest, Stage: stage,
		Params: &params, Reason: spec.Reason, CorrelationID: spec.CorrelationID, Cause: spec.Cause, Witnessed: spec.Witnessed}
	actor := RecordActor{Class: st.ActorClass, ClassifiedBy: st.ActorClassifiedBy, User: spec.Actor.User}
	if spec.OnBehalfOf != nil {
		actor.OnBehalfOf = &OnBehalfOf{User: spec.OnBehalfOf.User, Asserted: true}
	}
	to := ""
	if _, err := ParseDigest(spec.Params.To); err == nil {
		to = spec.Params.To
	}
	s := IntentStatement{Type: StatementType, Subject: subjects(anchorDigest, to), PredicateType: PredicateIntent,
		Predicate: IntentPredicate{Intent: in, Actor: actor, Guarded: st.Guarded, Approvals: approvals, Time: at}}
	if b, _ := json.Marshal(s); secretPattern.Match(b) {
		s.Predicate.Intent.Params, s.Predicate.Intent.Reason, s.Predicate.Intent.Cause = nil, "", nil
	}
	return s
}

// subjects are an approval or intent record's subjects: the stream anchor
// and, when it is a digest, the version the operation moves to.
func subjects(anchor, to string) []Subject {
	var out []Subject
	for _, x := range []struct{ name, digest string }{{"anchor", anchor}, {"to", to}} {
		if algo, hex, ok := strings.Cut(x.digest, ":"); ok {
			out = append(out, Subject{Name: x.name, Digest: map[string]string{algo: hex}})
		}
	}
	return out
}

// SignApproval signs an approval record and returns its bundle and id.
func SignApproval(ctx context.Context, s Signer, st ApprovalStatement) (bundle []byte, id string, err error) {
	return signStatement(ctx, s, st)
}

// SignIntent signs an intent record and returns its bundle and id.
func SignIntent(ctx context.Context, s Signer, st IntentStatement) (bundle []byte, id string, err error) {
	return signStatement(ctx, s, st)
}
