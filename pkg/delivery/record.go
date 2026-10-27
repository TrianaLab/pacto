package delivery

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Record format: an in-toto Statement v1 whose predicate is one concluded
// operation, a refusal to record one or a seq with no operation behind it.
const (
	StatementType      = "https://in-toto.io/Statement/v1"
	PredicateOperation = "delivery.pacto.trianalab.io/operation/v1"
)

// Record kinds.
const (
	// RecordOperation is a concluded operation, refusals included.
	RecordOperation = "operation"
	// RecordUnrecordable is an operation that could not be recorded as
	// written: it names the check that failed and carries no params.
	RecordUnrecordable = "unrecordable"
	// RecordGap is a seq whose operation is gone.
	RecordGap = "gap"
)

// Statement is an in-toto Statement v1 carrying a record.
type Statement struct {
	Type          string          `json:"_type"`
	Subject       []Subject       `json:"subject"`
	PredicateType string          `json:"predicateType"`
	Predicate     RecordPredicate `json:"predicate"`
}

// Subject is one artifact a record is about, by digest.
type Subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// RecordPredicate is delivery.pacto.trianalab.io/operation/v1.
type RecordPredicate struct {
	Kind         string           `json:"kind"`
	Type         *RecordType      `json:"type,omitempty"`
	Intent       RecordIntent     `json:"intent"`
	Policy       *StageSpec       `json:"policy,omitempty"`
	Actor        *RecordActor     `json:"actor,omitempty"`
	Verdicts     []Verdict        `json:"verdicts,omitempty"`
	Approvals    []RecordApproval `json:"approvals,omitempty"`
	Outcome      *RecordOutcome   `json:"outcome,omitempty"`
	Chain        Chain            `json:"chain"`
	Time         RecordTime       `json:"time"`
	Unrecordable *Refusal         `json:"unrecordable,omitempty"`
	Gap          *Gap             `json:"gap,omitempty"`
	// Trust is the digest of the trust config the recorder verified the
	// stream with before it extended it.
	Trust string `json:"trust,omitempty"`
	// Guarded says admission checked who asked and who approved. Absent, the
	// actor and approvals are only what their writers claimed.
	Guarded bool `json:"guarded,omitempty"`
}

// RecordType cites the operation type by digest.
type RecordType struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// RecordStage is the Stage an operation acted on.
type RecordStage struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UID       string `json:"uid,omitempty"`
}

// RecordIntent is what was asked.
type RecordIntent struct {
	UID           string      `json:"uid,omitempty"`
	Name          string      `json:"name,omitempty"`
	Digest        string      `json:"digest,omitempty"`
	Stage         RecordStage `json:"stage"`
	Params        *Params     `json:"params,omitempty"`
	Reason        string      `json:"reason,omitempty"`
	CorrelationID string      `json:"correlationID,omitempty"`
	Cause         *Cause      `json:"cause,omitempty"`
	Witnessed     bool        `json:"witnessed,omitempty"`
}

// RecordActor is who asked, as the apiserver named them.
type RecordActor struct {
	Class      ActorClass  `json:"class,omitempty"`
	User       string      `json:"user"`
	OnBehalfOf *OnBehalfOf `json:"onBehalfOf,omitempty"`
}

// OnBehalfOf is the person an agent said it acted for. It is asserted, never
// verified, and the record says so.
type OnBehalfOf struct {
	User     string `json:"user"`
	Asserted bool   `json:"asserted"`
}

// RecordApproval is an approval bound to the operation uid and intent digest.
type RecordApproval struct {
	By           string   `json:"by"`
	Decision     Decision `json:"decision"`
	Operation    string   `json:"operation"`
	IntentDigest string   `json:"intentDigest"`
	AuthTime     string   `json:"authTime,omitempty"`
	Note         string   `json:"note,omitempty"`
}

// RecordOutcome is how the operation ended.
type RecordOutcome struct {
	Result       Outcome       `json:"result"`
	Verification Verification  `json:"verification,omitempty"`
	Reason       string        `json:"reason,omitempty"`
	Message      string        `json:"message,omitempty"`
	Pointer      PointerChange `json:"pointer,omitzero"`
	// Level is the level change an autonomy operation made, empty otherwise.
	Level LevelChange `json:"level,omitzero"`
}

// LevelChange is a Stage's move from one level to another.
type LevelChange struct {
	From Level `json:"from"`
	To   Level `json:"to"`
}

// Chain places a record in its stream. Undoes and Cause are the record
// digests of the operations the intent names by uid, when those were recorded.
type Chain struct {
	Stream string `json:"stream"`
	Seq    int64  `json:"seq"`
	Parent string `json:"parent,omitempty"`
	Undoes string `json:"undoes,omitempty"`
	Cause  string `json:"cause,omitempty"`
}

// RecordTime is when the operation concluded. It comes from the operation's
// status, so building the same record twice gives the same payload.
type RecordTime struct {
	Finished string `json:"finished"`
}

// Refusal is the check that made an operation unrecordable.
type Refusal struct {
	Check   string `json:"check"`
	Message string `json:"message"`
}

// Gap names the local seq that had no operation.
type Gap struct {
	Seq int64 `json:"seq"`
}

// RecordSubjects are the digests a record is attached to. Empty ones are left
// out; the anchor is always there.
type RecordSubjects struct {
	To, From, Contract, Anchor string
}

// RecordInput is everything a record of one concluded operation is built from.
type RecordInput struct {
	ClusterID string
	Stage     StageObject
	Operation OperationObject
	// Approvals are the Approval objects status.approvals names, by name; a
	// missing name means the object is gone.
	Approvals map[string]ApprovalSpec
	Subjects  RecordSubjects
	Chain     Chain
	// Trust is the digest of the recorder's trust config.
	Trust string
}

// NewOperationRecord builds the record of one concluded operation.
func NewOperationRecord(in RecordInput) Statement {
	op, spec, st := in.Operation, in.Operation.Spec, in.Operation.Status
	p := RecordPredicate{
		Kind:     RecordOperation,
		Intent:   intent(in),
		Policy:   &in.Stage.Spec,
		Actor:    &RecordActor{Class: st.ActorClass, User: spec.Actor.User},
		Verdicts: st.Verdicts,
		Outcome: &RecordOutcome{Result: st.Outcome, Verification: st.Verification, Reason: st.Reason,
			Message: st.Message, Pointer: st.Pointer},
		Chain:   in.Chain,
		Time:    RecordTime{Finished: st.Times.Finished},
		Trust:   in.Trust,
		Guarded: st.Guarded,
	}
	if spec.Type == TypeAutonomy && st.Outcome == Succeeded {
		p.Outcome.Level = LevelChange{From: Level(spec.Params.From), To: Level(spec.Params.To)}
	}
	params := spec.Params
	p.Intent.Params, p.Intent.Reason, p.Intent.CorrelationID = &params, spec.Reason, spec.CorrelationID
	p.Intent.Cause, p.Intent.Witnessed = spec.Cause, spec.Witnessed
	if t, ok := TypeByID(spec.Type); ok {
		p.Type = &RecordType{ID: t.ID, Version: t.Version, Digest: t.Digest()}
	} else {
		p.Type = &RecordType{ID: spec.Type}
	}
	if spec.OnBehalfOf != nil {
		p.Actor.OnBehalfOf = &OnBehalfOf{User: spec.OnBehalfOf.User, Asserted: true}
	}
	for _, a := range st.Approvals {
		p.Approvals = append(p.Approvals, RecordApproval{By: a.By, Decision: a.Decision, Operation: op.Metadata.UID,
			IntentDigest: in.Approvals[a.Name].IntentDigest, AuthTime: a.AuthTime, Note: a.Note})
	}
	return statement(in.Subjects, p)
}

// NewUnrecordable builds the minimal record of an operation that failed a
// pre-sign check: who, which seq, which check, the intent digest and nothing
// else, so the value that failed the check is never signed.
func NewUnrecordable(in RecordInput, check, message string) Statement {
	p := RecordPredicate{
		Kind:         RecordUnrecordable,
		Intent:       intent(in),
		Chain:        Chain{Stream: in.Chain.Stream, Seq: in.Chain.Seq, Parent: in.Chain.Parent},
		Time:         RecordTime{Finished: in.Operation.Status.Times.Finished},
		Unrecordable: &Refusal{Check: check, Message: message},
		Trust:        in.Trust,
	}
	return statement(RecordSubjects{Anchor: in.Subjects.Anchor}, p)
}

// NewGap builds the record of a local seq whose operation is gone. at is when
// the recorder gave up waiting for it.
func NewGap(clusterID string, stage ObjectMeta, localSeq int64, chain Chain, anchor, at string) Statement {
	p := RecordPredicate{
		Kind:   RecordGap,
		Intent: RecordIntent{Stage: RecordStage{Cluster: clusterID, Namespace: stage.Namespace, Name: stage.Name, UID: stage.UID}},
		Chain:  Chain{Stream: chain.Stream, Seq: chain.Seq, Parent: chain.Parent},
		Time:   RecordTime{Finished: at},
		Gap:    &Gap{Seq: localSeq},
	}
	return statement(RecordSubjects{Anchor: anchor}, p)
}

func intent(in RecordInput) RecordIntent {
	m := in.Stage.Metadata
	return RecordIntent{UID: in.Operation.Metadata.UID, Name: in.Operation.Metadata.Name,
		Digest: in.Operation.Spec.IntentDigest,
		Stage:  RecordStage{Cluster: in.ClusterID, Namespace: m.Namespace, Name: m.Name, UID: m.UID}}
}

func statement(s RecordSubjects, p RecordPredicate) Statement {
	var subj []Subject
	for _, x := range []struct{ name, digest string }{{"to", s.To}, {"from", s.From}, {"contract", s.Contract}, {"anchor", s.Anchor}} {
		if algo, hex, ok := strings.Cut(x.digest, ":"); ok {
			subj = append(subj, Subject{Name: x.name, Digest: map[string]string{algo: hex}})
		}
	}
	return Statement{Type: StatementType, Subject: subj, PredicateType: PredicateOperation, Predicate: p}
}

// Pre-sign checks that fail the same way however often they run.
const (
	CheckSchema    = "schema"
	CheckSensitive = "sensitive-value"
	CheckApproval  = "approval-binding"
	// CheckSize fails a record whose signed bundle is larger than a reader
	// fetches; the recorder runs it after signing.
	CheckSize = "size"
)

// CheckRecord runs the deterministic pre-sign checks on a record of in. It
// returns the failed check and why, or "" when the record may be signed.
func CheckRecord(in RecordInput, st Statement) (check, message string) {
	p := st.Predicate
	switch {
	case p.Chain.Stream == "" || p.Chain.Seq < 1:
		return CheckSchema, "the record has no stream or seq"
	case p.Intent.UID == "" || p.Intent.Digest == "":
		return CheckSchema, "the operation has no uid or intent digest"
	case p.Time.Finished == "":
		return CheckSchema, "the operation has no finished time"
	case p.Outcome == nil || p.Outcome.Result == "":
		return CheckSchema, "the operation has no outcome"
	case len(st.Subject) == 0:
		return CheckSchema, "the record has no subject"
	}
	for _, a := range in.Operation.Status.Approvals {
		obj, ok := in.Approvals[a.Name]
		switch {
		case !ok:
			return CheckApproval, "approval " + a.Name + " is gone, so its binding cannot be checked"
		case obj.OperationUID != in.Operation.Metadata.UID || obj.IntentDigest != in.Operation.Spec.IntentDigest:
			return CheckApproval, "approval " + a.Name + " is bound to another operation or intent"
		}
	}
	b, _ := json.Marshal(st)
	if loc := secretPattern.FindIndex(b); loc != nil {
		return CheckSensitive, fmt.Sprintf("the record holds a value that looks like a secret at byte %d", loc[0])
	}
	return "", ""
}

// secretPattern matches values that look like credentials: a private key
// block, a bearer token, a JWT, common cloud and forge token shapes and
// password-style pairs. Records hold references, never secrets.
// ponytail: a fixed pattern list; it misses secrets with no telltale shape.
var secretPattern = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----` +
	`|(?i:bearer)\s+[A-Za-z0-9._~+/-]{20,}` +
	`|eyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}` +
	`|AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{20}|xox[abprs]-[A-Za-z0-9-]{10,}` +
	`|(?i:password|passwd|secret|token|api[_-]?key)"?\s*[:=]\s*"?[^\s",}]{4,}`)
