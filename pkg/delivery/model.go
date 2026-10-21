// Package delivery is the pure core of Pacto delivery: the operation model, the
// Tier 0 operation types, the intent digest and plan(), the one function the
// promoter, the CLI and the MCP tools all call to decide whether a change may
// move, whether a person must agree and who.
//
// It holds no Kubernetes, registry or clock dependency. The wire types below are
// embedded verbatim by the delivery CRDs (delivery.pacto.trianalab.io/v1alpha1 in
// integrations/kubernetes), so a client decoding a Stage or an Operation reads
// exactly the struct the controller wrote, and the intent digest a client
// computes is byte-for-byte the one the controller recomputes.
package delivery

import "encoding/json"

// Facets an operation may change. Nothing else may reconcile a facet.
const (
	FacetPointer = "pointer"
	FacetFreeze  = "freeze"
)

// Result is a gate verdict.
type Result string

const (
	Pass    Result = "Pass"
	Fail    Result = "Fail"
	Unknown Result = "Unknown"
)

// Phase is where an operation is in its lifecycle.
type Phase string

const (
	PhasePending   Phase = "Pending"
	PhaseHeld      Phase = "Held"
	PhaseRunning   Phase = "Running"
	PhaseVerifying Phase = "Verifying"
	PhaseConcluded Phase = "Concluded"
)

// Outcome is how a concluded operation ended.
type Outcome string

const (
	Succeeded  Outcome = "Succeeded"
	Failed     Outcome = "Failed"
	Errored    Outcome = "Errored"
	Cancelled  Outcome = "Cancelled"
	Rejected   Outcome = "Rejected"
	Expired    Outcome = "Expired"
	Superseded Outcome = "Superseded"
	Witnessed  Outcome = "Witnessed"
)

// Verification qualifies a Succeeded outcome.
type Verification string

const (
	Verified        Verification = "Verified"
	AcceptedUnknown Verification = "AcceptedUnknown"
	Unverified      Verification = "Unverified"
)

// Decision is what a human approval says.
type Decision string

const (
	Approve       Decision = "approve"
	Reject        Decision = "reject"
	AcceptUnknown Decision = "accept-unknown"
)

// ActorClass is who raised an operation, as the promoter classifies the
// apiserver identity. It is derived, never asserted by the client.
type ActorClass string

const (
	ClassController ActorClass = "controller"
	ClassAgent      ActorClass = "agent"
	ClassHuman      ActorClass = "human"
	ClassBreakGlass ActorClass = "break-glass"
)

// ServiceRef is the Pacto ServiceID a Stage delivers: the domain-qualified
// service name its contracts declare.
type ServiceRef struct {
	// Domain is the registry and organisation scope (empty for the default).
	// +optional
	Domain string `json:"domain,omitempty"`
	// Name is the contract's service.name.
	Name string `json:"name"`
}

// LocalRef names an object in the Stage's own namespace.
type LocalRef struct {
	Name string `json:"name"`
}

// Candidates says where new versions come from. Exactly one is set.
type Candidates struct {
	// OCIRepositoryRef is a Flux OCIRepository the promoter only reads: the
	// candidate is the @sha256: part of its status.artifact.revision.
	// +optional
	OCIRepositoryRef *LocalRef `json:"ociRepositoryRef,omitempty"`
	// UpstreamStage offers that Stage's verified digests minus its failed ones.
	// +optional
	UpstreamStage *StageRef `json:"upstreamStage,omitempty"`
}

// StageRef names a Stage, in the referring Stage's namespace unless Namespace
// says otherwise (environments usually live in namespaces of their own).
type StageRef struct {
	Name string `json:"name"`
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// VerifyGates configures what runs after a version is applied.
type VerifyGates struct {
	// Window is how long the verify gates may stay Unknown before the
	// operation is held for a human (a Go duration, default 5m). Re-checks
	// after release judge the same length of time.
	// +optional
	Window string `json:"window,omitempty"`
	// Checks are this Stage's own verify gates, run beside the Pacto contract
	// check: a health provider the delivery chart configures, or the built-in
	// pacto.runtime claim gate.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	Checks []GateCheck `json:"checks,omitempty"`
	// Recheck is how often the verify gates run again on the version the
	// Stage runs once it shipped (a Go duration, default 1m). A confirmed Fail
	// rolls it back; Unknown never does.
	// +optional
	Recheck string `json:"recheck,omitempty"`
}

// GateCheck is one verify gate a Stage runs.
type GateCheck struct {
	// Name is the gate id its verdicts carry, unique on the Stage.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Provider is a gate provider configured in the delivery chart, or
	// pacto.runtime. A Stage cannot name a URL: providers are operator
	// configuration.
	// +kubebuilder:validation:MinLength=1
	Provider string `json:"provider"`
	// Params are the provider's settings (thresholds, filters).
	// +optional
	Params map[string]string `json:"params,omitempty"`
}

// Gates configures the Stage's gates. The pre-gates are fixed by the operation
// type; the Pacto contract check always verifies, beside the Stage's checks.
type Gates struct {
	// +optional
	Verify VerifyGates `json:"verify,omitzero"`
}

// ponytail: one switch, not the design's CEL predicate; add CEL when a team
// needs a floor that depends on the change.

// ApprovalPolicy can only raise the approval floor, never lower it.
type ApprovalPolicy struct {
	// Always holds every promote for a human, whatever the contracts say.
	// +optional
	Always bool `json:"always,omitempty"`
}

// Deadlines override the operation type's defaults (Go durations).
type Deadlines struct {
	// +optional
	Approval string `json:"approval,omitempty"`
	// +optional
	Execution string `json:"execution,omitempty"`
}

// StageSpec is one service in one environment.
type StageSpec struct {
	Service     ServiceRef `json:"service"`
	Environment string     `json:"environment"`
	// ApplyRef is the Flux OCIRepository the promoter owns. It only ever sets
	// spec.ref.digest on it.
	ApplyRef LocalRef `json:"applyRef"`
	// +optional
	Candidates Candidates `json:"candidates,omitzero"`
	// Auto promotes the newest eligible candidate without anyone asking.
	// +optional
	Auto bool `json:"auto,omitempty"`
	// +optional
	Gates Gates `json:"gates,omitzero"`
	// +optional
	Approval ApprovalPolicy `json:"approval,omitzero"`
	// +optional
	Deadlines Deadlines `json:"deadlines,omitzero"`
}

// Stream numbers a Stage's concluded operations.
type Stream struct {
	// ID is <clusterID>/<namespace>/<name>; the cluster id is the kube-system
	// namespace uid.
	// +optional
	ID string `json:"id,omitempty"`
	// +optional
	Seq int64 `json:"seq,omitempty"`
	// RecordedSeq is the newest local seq the recorder wrote or adopted a
	// record for. Only the recorder writes it.
	// +optional
	RecordedSeq int64 `json:"recordedSeq,omitempty"`
	// Head is the digest of the newest record in the stream. Only the
	// recorder writes it.
	// +optional
	Head string `json:"head,omitempty"`
	// Anchor is where the stream's records are listed:
	// <repository>@<anchor digest>. Only the recorder writes it.
	// +optional
	Anchor string `json:"anchor,omitempty"`
}

// StageStatus is written only by the promoter's operation reconciler.
type StageStatus struct {
	// Pointer is the digest the environment runs.
	// +optional
	Pointer string `json:"pointer,omitempty"`
	// LastOp is the uid of the operation that last moved the pointer.
	// +optional
	LastOp string `json:"lastOp,omitempty"`
	// Locks maps a facet to the uid of the operation holding it.
	// +optional
	Locks map[string]string `json:"locks,omitempty"`
	// Verified holds the newest verified digests, oldest first (cap 10).
	// +optional
	Verified []string `json:"verified,omitempty"`
	// FailedDigests are never promoted again (cap 50).
	// +optional
	FailedDigests []string `json:"failedDigests,omitempty"`
	// +optional
	Frozen bool `json:"frozen,omitempty"`
	// +optional
	FrozenReason string `json:"frozenReason,omitempty"`
	// AutoHold stops auto-promotion after a person chose an older version.
	// +optional
	AutoHold bool `json:"autoHold,omitempty"`
	// AutoRollbacks counts the automatic rollbacks raised per cause.
	// +optional
	AutoRollbacks map[string]int `json:"autoRollbacks,omitempty"`
	// +optional
	Stream Stream `json:"stream,omitzero"`
	// Published holds the candidate digests Flux verified the signature of on
	// the candidate OCIRepository (spec.verify naming the publisher's
	// identity), oldest first (cap 50). Only these are promoted from the
	// OCIRepository.
	// +optional
	Published []string `json:"published,omitempty"`
	// Offered holds the digests a promote carried through pacto.published
	// with a pass (not an accepted unknown) and that then verified here,
	// oldest first (cap 50). Only these are offered to a downstream Stage.
	// +optional
	Offered []string `json:"offered,omitempty"`
	// Conditions holds Ready. It is False, naming what is missing, when the
	// Stage can never promote: its candidate OCIRepository does not exist or
	// sets no spec.verify naming the publisher, or it names no candidate
	// source at all. The recorder adds RecordStoreReady: False when the
	// registry has no Referrers API, cannot be reached or holds a stream it
	// refuses to fork.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []Condition `json:"conditions,omitempty"`
	// Candidate is the newest eligible candidate the stage reconciler saw.
	// +optional
	Candidate string `json:"candidate,omitempty"`
	// LastCheck is the newest re-check of the running version after it shipped.
	// +optional
	LastCheck *Recheck `json:"lastCheck,omitempty"`
}

// Condition is a Kubernetes status condition, so `kubectl wait
// --for=condition=Ready` and status tooling read it.
// ponytail: no lastTransitionTime; add it when a reader needs to know when
// readiness changed.
type Condition struct {
	Type string `json:"type"`
	// +kubebuilder:validation:Enum=True;False;Unknown
	Status string `json:"status"`
	// +optional
	Reason string `json:"reason,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
}

// Recheck is one run of the verify gates on a version that already shipped.
type Recheck struct {
	// Operation is the uid of the operation that shipped the version.
	Operation string `json:"operation"`
	Digest    string `json:"digest"`
	// At is when the gates ran (RFC 3339).
	At       string    `json:"at"`
	Verdicts []Verdict `json:"verdicts,omitempty"`
}

// Params are the Tier 0 operation parameters. Digests are OCI manifest digests.
type Params struct {
	// +optional
	From string `json:"from,omitempty"`
	// +optional
	To string `json:"to,omitempty"`
	// Undoes is the uid of the operation a rollback undoes.
	// +optional
	Undoes string `json:"undoes,omitempty"`
}

// Actor is the apiserver identity that created the operation.
type Actor struct {
	User string `json:"user"`
	// +optional
	Groups []string `json:"groups,omitempty"`
}

// Principal is a person an agent says it acts for. It is asserted, never
// verified.
type Principal struct {
	User string `json:"user"`
}

// Verdict is one gate's answer plus a citation that can be re-run.
type Verdict struct {
	Gate string `json:"gate"`
	// +kubebuilder:validation:Enum=pre;verify
	Phase string `json:"phase"`
	// +kubebuilder:validation:Enum=Pass;Fail;Unknown
	Result Result `json:"result"`
	// +optional
	Citation map[string]string `json:"citation,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
}

// Cause is why an operation was raised. A person or an agent sets one field;
// a verdict they cite is re-run when the operation is admitted. An automatic
// rollback names the failed operation and, when a gate failed it, the verdict.
type Cause struct {
	// Operation is the uid of the operation that caused this one.
	// +optional
	Operation string `json:"operation,omitempty"`
	// +optional
	Verdict *Verdict `json:"verdict,omitempty"`
	// +optional
	Reason string `json:"reason,omitempty"`
}

// OperationSpec is the intent. It is frozen at admission.
type OperationSpec struct {
	// +kubebuilder:validation:Enum=pacto.dev/promote;pacto.dev/rollback;pacto.dev/override;pacto.dev/unfreeze
	Type string `json:"type"`
	// Stage is the name of a Stage in the operation's namespace.
	Stage string `json:"stage"`
	// +kubebuilder:validation:Enum=pointer;freeze
	Facet string `json:"facet"`
	// +optional
	Params Params `json:"params,omitzero"`
	// IntentDigest is what plan() returned; the promoter recomputes it.
	// +optional
	IntentDigest string `json:"intentDigest,omitempty"`
	// +optional
	Reason string `json:"reason,omitempty"`
	// +optional
	CorrelationID string `json:"correlationID,omitempty"`
	Actor         Actor  `json:"actor"`
	// +optional
	OnBehalfOf *Principal `json:"onBehalfOf,omitempty"`
	// +optional
	Cause *Cause `json:"cause,omitempty"`
	// Witnessed marks an override the promoter raised for an edit it saw but
	// did not make. Only the promoter may set it.
	// +optional
	Witnessed bool `json:"witnessed,omitempty"`
}

// Floor is the approval floor plan() computed: whether a human must decide,
// which decision is asked for and whose.
type Floor struct {
	// +optional
	Human bool `json:"human,omitempty"`
	// +optional
	Decision Decision `json:"decision,omitempty"`
	// Owners are the teams the question is routed to and the only ones whose
	// members may answer it: the owners of the consumers that made the floor,
	// or else the team of the Stage's own service. Empty means no team is
	// declared, and any person may answer.
	// +optional
	Owners []string `json:"owners,omitempty"`
	// Consumers are the services that made the floor.
	// +optional
	Consumers []string `json:"consumers,omitempty"`
}

// Prediction is what plan() expects the promoter to do.
type Prediction string

const (
	PredictProceeds Prediction = "Proceeds"
	PredictHeld     Prediction = "Held"
	PredictRejected Prediction = "Rejected"
	PredictWaits    Prediction = "Waits"
)

// Plan is plan()'s answer. It binds to the intent digest.
type Plan struct {
	IntentDigest string `json:"intentDigest"`
	Type         string `json:"type"`
	// +optional
	From string `json:"from,omitempty"`
	// +optional
	To string `json:"to,omitempty"`
	// +optional
	Verdicts []Verdict `json:"verdicts,omitempty"`
	// +optional
	Floor Floor `json:"floor,omitzero"`
	// Question is the one question a human would be asked.
	// +optional
	Question string `json:"question,omitempty"`
	// +optional
	Conflicts []string   `json:"conflicts,omitempty"`
	Predicted Prediction `json:"predicted"`
	// ImpactDigest is the sha256 of the pkg/impact answer the floor came from.
	// +optional
	ImpactDigest string `json:"impactDigest,omitempty"`
	// Order says what must move first when a consumer running in this
	// environment does not accept the new version. Pacto only says it: it
	// never moves another service itself.
	// +optional
	Order *Order `json:"order,omitempty"`
	// Precedent is what happened the last times this version was the target
	// of a recorded operation, on any Stage. Clients fill it from records;
	// the promoter leaves it empty.
	// +optional
	Precedent *Precedent `json:"precedent,omitempty"`
}

// Precedent is what the records attached to a version say happened to it.
// With completeness partial, no entry for a Stage proves nothing.
type Precedent struct {
	Completeness string `json:"completeness"`
	// +optional
	Note string `json:"note,omitempty"`
	// +optional
	Entries []PrecedentEntry `json:"entries,omitempty"`
}

// PrecedentEntry is one recorded operation that moved, or tried to move, a
// Stage to the version.
type PrecedentEntry struct {
	Stream  string  `json:"stream"`
	Seq     int64   `json:"seq"`
	Type    string  `json:"type"`
	Outcome Outcome `json:"outcome"`
	// +optional
	Verification Verification `json:"verification,omitempty"`
	// +optional
	Reason string `json:"reason,omitempty"`
	// +optional
	Finished string `json:"finished,omitempty"`
	Record   string `json:"record"`
}

// Order is what must move before a blocked promote can ship with no person
// approving a break. Compatibility is judged per consumer from the version
// range it declares, not from which fields or endpoints it uses.
type Order struct {
	// MoveFirst names every consumer running in this environment whose
	// declared range does not accept the new version.
	MoveFirst []MoveFirst `json:"moveFirst"`
	// Unknown says why the list may be incomplete: a partial graph, a range
	// Pacto cannot read or a consumer it cannot place. Empty means Pacto saw
	// every consumer running here.
	// +optional
	Unknown []string `json:"unknown,omitempty"`
}

// MoveFirst is one consumer that must move before the provider can.
type MoveFirst struct {
	// Service is the consumer.
	Service string `json:"service"`
	// +optional
	Domain string `json:"domain,omitempty"`
	// Version is the consumer version running here that does not accept the
	// new provider version.
	// +optional
	Version string `json:"version,omitempty"`
	// Accepts is the range that version declares on the provider.
	// +optional
	Accepts string `json:"accepts,omitempty"`
	// Stage is the consumer's Stage in this environment, the one to promote.
	// +optional
	Stage *StageRef `json:"stage,omitempty"`
	// Candidate is the newest version of the consumer its upstream Stage
	// offered and verified that is newer than the one running here, accepts
	// both the provider version running here and the new one, accepts what
	// its other dependencies run here and is accepted by the range every
	// Pacto here declares on it, so it can move first without breaking a
	// range Pacto can see. Absent when there is none or Pacto cannot tell.
	// +optional
	Candidate *Candidate `json:"candidate,omitempty"`
	// Unknown says why Pacto cannot tell whether such a version exists.
	// +optional
	Unknown string `json:"unknown,omitempty"`
	// Message says all of the above in plain words.
	Message string `json:"message"`
}

// Candidate is a verified consumer version that can move first.
type Candidate struct {
	Digest  string `json:"digest"`
	Version string `json:"version,omitempty"`
	// Accepts is the range it declares on the provider; empty when it no
	// longer depends on it.
	// +optional
	Accepts string `json:"accepts,omitempty"`
	// VerifiedOn is the upstream Stage that verified it.
	VerifiedOn StageRef `json:"verifiedOn"`
}

// ApprovalRecord is an approval the promoter consumed.
type ApprovalRecord struct {
	Name     string   `json:"name"`
	By       string   `json:"by"`
	Decision Decision `json:"decision"`
	// +optional
	Note string `json:"note,omitempty"`
	// +optional
	AuthTime string `json:"authTime,omitempty"`
}

// PointerChange is the pointer before and after an operation.
type PointerChange struct {
	// +optional
	Before string `json:"before,omitempty"`
	// +optional
	After string `json:"after,omitempty"`
}

// Times are RFC 3339 timestamps.
type Times struct {
	// +optional
	Admitted string `json:"admitted,omitempty"`
	// +optional
	Started string `json:"started,omitempty"`
	// +optional
	Applied string `json:"applied,omitempty"`
	// +optional
	Finished string `json:"finished,omitempty"`
	// +optional
	ApprovalDeadline string `json:"approvalDeadline,omitempty"`
	// +optional
	ExecutionDeadline string `json:"executionDeadline,omitempty"`
	// +optional
	VerifyDeadline string `json:"verifyDeadline,omitempty"`
}

// OperationStatus is written only by the promoter.
type OperationStatus struct {
	// +optional
	Phase Phase `json:"phase,omitempty"`
	// +optional
	Outcome Outcome `json:"outcome,omitempty"`
	// +optional
	Verification Verification `json:"verification,omitempty"`
	// Reason is the machine reason for the outcome or the hold (PlanStale,
	// PointerMoved, NoStage, ...).
	// +optional
	Reason string `json:"reason,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// Question is what a human is asked while the operation is Held.
	// +optional
	Question string `json:"question,omitempty"`
	// +optional
	ActorClass ActorClass `json:"actorClass,omitempty"`
	// +optional
	Plan *Plan `json:"plan,omitempty"`
	// +optional
	Verdicts []Verdict `json:"verdicts,omitempty"`
	// +optional
	Approvals []ApprovalRecord `json:"approvals,omitempty"`
	// +optional
	Pointer PointerChange `json:"pointer,omitzero"`
	// Applied is true once the pointer moved for this operation.
	// +optional
	Applied bool `json:"applied,omitempty"`
	// +optional
	Times Times `json:"times,omitzero"`
	// +optional
	Seq int64 `json:"seq,omitempty"`
	// Record is the signed record of this operation, once the recorder wrote
	// it. Only the recorder writes it.
	// +optional
	Record *RecordRef `json:"record,omitempty"`
}

// RecordRef names an operation's record.
type RecordRef struct {
	// Digest is the record id: the sha256 of its signed payload.
	Digest string `json:"digest"`
	// Seq is the record's place in the stream.
	Seq int64 `json:"seq"`
	// Kind is operation, or unrecordable when a pre-sign check refused it.
	Kind string `json:"kind"`
	// Refused names the check and why, for an unrecordable record.
	// +optional
	Refused string `json:"refused,omitempty"`
}

// ApprovalSpec is a human decision bound to one operation uid and intent digest.
type ApprovalSpec struct {
	// Operation is the operation's name, for people. Matching uses the uid.
	Operation    string   `json:"operation"`
	OperationUID string   `json:"operationUID"`
	IntentDigest string   `json:"intentDigest"`
	Decision     Decision `json:"decision"`
	// +optional
	Note string `json:"note,omitempty"`
	// By must equal the apiserver username of whoever created the Approval.
	By string `json:"by"`
	// AuthTime is the login time the apiserver reported for that user
	// (userInfo.extra["pacto.dev/auth-time"], RFC 3339 or Unix seconds).
	// +optional
	AuthTime string `json:"authTime,omitempty"`
	// Groups must equal the groups of whoever created the Approval. The
	// promoter accepts it only when one of them names a team the question
	// was routed to.
	// +optional
	Groups []string `json:"groups,omitempty"`
}

// deepCopyJSON copies in into out through JSON. Every wire type here is plain
// JSON data, so the round trip is exact, and it keeps the CRDs that embed these
// types free of hand-maintained field-by-field copies.
// ponytail: JSON round trip per copy; generate field copies if a profile ever
// shows it.
func deepCopyJSON[T any](in, out *T) {
	b, _ := json.Marshal(in)
	var fresh T
	_ = json.Unmarshal(b, &fresh)
	*out = fresh
}

// DeepCopyInto copies the receiver into out.
func (in *StageSpec) DeepCopyInto(out *StageSpec) { deepCopyJSON(in, out) }

// DeepCopyInto copies the receiver into out.
func (in *StageStatus) DeepCopyInto(out *StageStatus) { deepCopyJSON(in, out) }

// DeepCopyInto copies the receiver into out.
func (in *OperationSpec) DeepCopyInto(out *OperationSpec) { deepCopyJSON(in, out) }

// DeepCopyInto copies the receiver into out.
func (in *OperationStatus) DeepCopyInto(out *OperationStatus) { deepCopyJSON(in, out) }

// DeepCopyInto copies the receiver into out.
func (in *ApprovalSpec) DeepCopyInto(out *ApprovalSpec) { deepCopyJSON(in, out) }
