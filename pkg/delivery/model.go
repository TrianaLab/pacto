// Package delivery is the pure core of Pacto delivery: the operation model, the
// Tier 0 operation types, the intent digest and plan(), the one function the
// promoter, the CLI and the MCP tools all call to decide whether a change may
// move, whether a person must agree and who.
//
// Experimental: not covered by semantic versioning. Delivery is Alpha, and
// this API may change in a minor release.
//
// The decision path (plan, the verdict mappings and the intent digest) holds no
// Kubernetes, registry or clock dependency: what it reads arrives in PlanInput
// and through the Registry interface. The package also holds the gate
// providers that call out over HTTP (http, mira and eval, in gate.go, mira.go
// and eval.go); they run only where the promoter is configured with them, and
// CheckTimeout bounds each check. The wire types below are
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
	// FacetLevel is the Stage's autonomy level (status.level).
	FacetLevel = "level"
	// FacetUpstream is what the Stage accepts from the upstream stream it
	// follows (status.upstreamAcknowledged).
	FacetUpstream = "upstream"
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

// Applier kinds a Stage's ApplyRef may name.
const (
	ApplierFlux   = "OCIRepository"
	ApplierArgoCD = "Application"
)

// ApplyRef names the Stage's applier object, in the Stage's namespace.
type ApplyRef struct {
	// Kind is OCIRepository (Flux, the default) or Application (Argo CD, with
	// an OCI source and automated sync).
	// +optional
	// +kubebuilder:validation:Enum=OCIRepository;Application
	Kind string `json:"kind,omitempty"`
	Name string `json:"name"`
}

// Candidates says where new versions come from. At most one is set, so the
// version offered and the version judged come from the same source; a Stage
// that sets none is not Ready and never promotes.
// +kubebuilder:validation:XValidation:rule="(has(self.ociRepositoryRef) ? 1 : 0) + (has(self.upstreamStage) ? 1 : 0) + (has(self.upstreamRecords) ? 1 : 0) <= 1",message="set at most one of candidates.ociRepositoryRef, candidates.upstreamStage and candidates.upstreamRecords"
type Candidates struct {
	// OCIRepositoryRef is a Flux OCIRepository the promoter only reads: the
	// candidate is the @sha256: part of its status.artifact.revision.
	// +optional
	OCIRepositoryRef *LocalRef `json:"ociRepositoryRef,omitempty"`
	// UpstreamStage offers that Stage's verified digests minus its failed ones.
	// +optional
	UpstreamStage *StageRef `json:"upstreamStage,omitempty"`
	// UpstreamRecords offers what a Stage in another cluster verified, read
	// from its signed records: the promoter must trust the key that signs
	// that stream (promoter.upstreamTrust), or it offers nothing.
	// +optional
	UpstreamRecords *RemoteStage `json:"upstreamRecords,omitempty"`
}

// RemoteStage is a Stage in another cluster, known only by its signed records.
type RemoteStage struct {
	// Repository (host/path) is the upstream recorder's streams repository.
	Repository string `json:"repository"`
	// Stream is the upstream Stage's stream id, <clusterID>/<namespace>/<name>:
	// its status.stream.id.
	Stream string `json:"stream"`
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
	// check: a health provider set in delivery.gates.providers, or the built-in
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
	// Provider is a gate provider set in delivery.gates.providers, or
	// pacto.runtime. A Stage cannot name a URL: providers are operator
	// configuration.
	// +kubebuilder:validation:MinLength=1
	Provider string `json:"provider"`
	// Params are the provider's settings (thresholds, filters).
	// +optional
	Params map[string]string `json:"params,omitempty"`
	// Suite names a check the version's contract ships (its checks[].name)
	// for an eval provider to run; without it the provider runs the only check
	// the version ships. A version whose contract ships no check of that name,
	// or with no suite ships none or more than one, is held for a person
	// before it ships.
	// +optional
	// +kubebuilder:validation:MinLength=1
	Suite string `json:"suite,omitempty"`
	// SuiteDigest pins the suite's bytes ("sha256:" and 64 hex digits). A
	// version whose suite has another digest changed its own exam: it is held
	// for a person before it ships, and an eval check of it is Unknown.
	// +optional
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	SuiteDigest string `json:"suiteDigest,omitempty"`
	// RequireObservedModel makes an eval check Unknown unless its runner
	// says which model answered (observedModel) and the contract declares
	// an external dependency with a version to compare it with. A model the
	// runner names that no such version matches fails the check either way.
	// +optional
	RequireObservedModel bool `json:"requireObservedModel,omitempty"`
}

// Gates configures the Stage's gates. The pre-gates are fixed by the operation
// type; the Pacto contract check always verifies, beside the Stage's checks.
type Gates struct {
	// +optional
	Impact ImpactGate `json:"impact,omitzero"`
	// +optional
	Verify VerifyGates `json:"verify,omitzero"`
}

// ImpactGate tunes pacto.impact.
type ImpactGate struct {
	// DeclaredConsumersOnly says every consumer of the service has a
	// contract Pacto can see, so a breaking change no Pacto depends on ships
	// without a person. Off by default: a service with no contract can still call it, so
	// a breaking change with no consumer in sight waits for a person to accept
	// the unknown.
	// +optional
	DeclaredConsumersOnly bool `json:"declaredConsumersOnly,omitempty"`
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

// RecordsPolicy says whether automation may move the Stage while its records
// cannot be written.
type RecordsPolicy struct {
	// Required holds every promote or rollback an agent or the promoter
	// raises for a person to approve while the recorder is not writing this
	// Stage's records (RecordStoreReady is not True) or more than five of
	// its operations wait for a record (MaxRecordsPending).
	// +optional
	Required bool `json:"required,omitempty"`
}

// StageSpec is one service in one environment.
type StageSpec struct {
	Service ServiceRef `json:"service"`
	// Environment is the environment the Stage delivers to, such as prod.
	// A promote matches it against the environments the version's contract
	// declares (environments[].name): a contract that declares environments
	// must declare this one, with values that satisfy its configuration
	// schemas, or the promote waits for a person (pacto.environment). It
	// also places the Pactos and Stages a change's consumers run on.
	Environment string `json:"environment"`
	// ApplyRef is the object the promoter points at a version: a Flux
	// OCIRepository (it sets spec.ref.digest) or an Argo CD Application (it
	// sets spec.source.targetRevision). Nothing else is ever written on it.
	ApplyRef ApplyRef `json:"applyRef"`
	// +optional
	Candidates Candidates `json:"candidates,omitzero"`
	// StartLevel is where a new Stage begins. It is read once, when
	// status.level is empty; after that only an autonomy operation moves the
	// level. Creating a Stage is an admin action, so any level is accepted, and
	// a Stage whose status is lost seeds back to this level. Only observe and
	// advise keep Pacto from shipping without a person.
	// +optional
	// +kubebuilder:validation:Enum=observe;advise;auto-nonbreaking;autonomous
	// +kubebuilder:default=observe
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="startLevel is fixed at creation; propose delivery.pacto.trianalab.io/autonomy to change the level"
	StartLevel Level `json:"startLevel,omitempty"`
	// +optional
	Autonomy AutonomyPolicy `json:"autonomy,omitzero"`
	// +optional
	Gates Gates `json:"gates,omitzero"`
	// +optional
	Approval ApprovalPolicy `json:"approval,omitzero"`
	// +optional
	Deadlines Deadlines `json:"deadlines,omitzero"`
	// +optional
	Records RecordsPolicy `json:"records,omitzero"`
	// Workload is the workload the service runs as, in the Stage's namespace:
	// the one object pacto.contract and pacto.runtime may judge. The Pacto
	// resource ships inside every version and names its own target, so
	// without this a version could point its own check at a decoy. Empty is
	// the Deployment named spec.service.name.
	// +optional
	Workload *WorkloadRef `json:"workload,omitempty"`
}

// WorkloadRef names a workload in the Stage's namespace.
type WorkloadRef struct {
	// Kind is the workload kind, such as Deployment or StatefulSet.
	// +kubebuilder:validation:MinLength=1
	Kind string `json:"kind"`
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// WorkloadOf is the workload the Stage's checks must have judged: spec.workload,
// or the Deployment named spec.service.name.
func (s StageSpec) WorkloadOf() WorkloadRef {
	if s.Workload != nil {
		return *s.Workload
	}
	return WorkloadRef{Kind: "Deployment", Name: s.Service.Name}
}

// AutonomyPolicy tunes when Pacto proposes the next level.
type AutonomyPolicy struct {
	// ProposeAfter is how many qualifying promotions earn a proposal.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=20
	// +kubebuilder:default=10
	ProposeAfter int `json:"proposeAfter,omitempty"`
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
	// Concluded are the newest operations given a seq, each with how it
	// concluded, written in the compare-and-swap that took the seq. An
	// operation whose own status write then failed concludes again from
	// here: the same seq and outcome, never a second seq.
	// +optional
	Concluded []Concluded `json:"concluded,omitempty"`
}

// RecordMark names one record of a stream.
type RecordMark struct {
	// Stream is the stream id.
	Stream string `json:"stream"`
	Seq    int64  `json:"seq"`
	// Record is the record's id, the digest of its signed payload.
	Record string `json:"record"`
}

// Concluded is one operation's seq and conclusion, as the Stage holds it.
type Concluded struct {
	UID string `json:"uid"`
	Seq int64  `json:"seq"`
	// +optional
	Outcome Outcome `json:"outcome,omitempty"`
	// +optional
	Verification Verification `json:"verification,omitempty"`
	// Reason is "Code: message".
	// +optional
	Reason string `json:"reason,omitempty"`
	// Type is the operation type.
	// +optional
	Type string `json:"type,omitempty"`
	// Person is true when the operation carried an approval or a person
	// raised it.
	// +optional
	Person bool `json:"person,omitempty"`
	// ClearedBreaking is a promote of a change that is not NON_BREAKING whose
	// impact verdict passed: every consumer accepts it.
	// +optional
	ClearedBreaking bool `json:"clearedBreaking,omitempty"`
	// Cause is the uid of the operation whose failure made the promoter
	// raise this one: an automatic rollback, freeze or step-down.
	// +optional
	Cause string `json:"cause,omitempty"`
}

// ObservedRevision is the digest GitOps' source reports while a Stage
// observes, and since when Pacto saw it (RFC 3339). It says what the source
// fetched, not that it rolled out.
type ObservedRevision struct {
	Digest string `json:"digest"`
	Since  string `json:"since"`
}

// Shadow is what Pacto would have decided for a candidate on a fully
// automatic Stage while this one observes, and what then happened. It is
// advisory status, never a record.
type Shadow struct {
	Candidate string `json:"candidate"`
	// +optional
	From      string     `json:"from,omitempty"`
	Predicted Prediction `json:"predicted"`
	// Decision is the floor's decision: approve or accept-unknown.
	// +optional
	Decision Decision `json:"decision,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=16
	Owners []string `json:"owners,omitempty"`
	// +optional
	Question     string `json:"question,omitempty"`
	IntentDigest string `json:"intentDigest"`
	// +optional
	ImpactDigest string `json:"impactDigest,omitempty"`
	// PlannedAt is when Pacto planned it (RFC 3339).
	PlannedAt string `json:"plannedAt"`
	// ObservedAt is when GitOps' source reported this candidate (RFC 3339):
	// not proof of rollout.
	// +optional
	ObservedAt string `json:"observedAt,omitempty"`
	// Verification is the shadow verify on the observed digest.
	// +optional
	Verification Verification `json:"verification,omitempty"`
}

// Proposal is the next level Pacto proposes for a Stage, or why not yet.
type Proposal struct {
	// Level is empty while nothing is proposed.
	// +optional
	Level Level `json:"level,omitempty"`
	// Evidence names the digests or operation uids that earned it.
	// +optional
	// +kubebuilder:validation:MaxItems=20
	Evidence []string `json:"evidence,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=32
	Blockers []Blocker `json:"blockers,omitempty"`
}

// Blocker is one reason the next level is not proposed.
type Blocker struct {
	// Kind is missing-contract, undeclared-range, unlabelled-apply-ref,
	// no-observed-digest, no-record-store, needs-verified,
	// needs-cleared-breaking, incident or partial-graph.
	Kind string `json:"kind"`
	// Subject is the service, consumer or caller identity.
	// +optional
	Subject string `json:"subject,omitempty"`
	Message string `json:"message"`
}

// StageStatus is written by the promoter's operation and stage reconcilers and
// by the recorder (Stream.Head, Stream.Anchor and RecordStoreReady), each
// through a compare-and-swap on the Stage's resourceVersion.
type StageStatus struct {
	// Pointer is the digest the Stage moved the environment to. It is set
	// when an operation holding the pointer lock starts to apply, before
	// Flux or Argo CD runs it: Applied on that operation says when they did.
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
	// FailedDigests are the last 50 versions that failed here; none of them is
	// promoted again. An older failure drops out.
	// +optional
	FailedDigests []string `json:"failedDigests,omitempty"`
	// +optional
	Frozen bool `json:"frozen,omitempty"`
	// +optional
	FrozenReason string `json:"frozenReason,omitempty"`
	// AutoHold stops auto-promotion after a person or agent rolled back or
	// promoted back to a version the Stage already verified.
	// +optional
	AutoHold bool `json:"autoHold,omitempty"`
	// RolledBack holds the uids of the failed operations the promoter raised
	// its one automatic rollback for, oldest first (cap 50). A failure listed
	// here never gets a second one.
	// +optional
	RolledBack []string `json:"rolledBack,omitempty"`
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
	// Stage cannot promote: its applier cannot be read, lacks the apply-ref
	// label or (on Argo CD) has no single OCI source or no automated sync; its
	// candidate OCIRepository does not exist or sets no spec.verify naming the
	// publisher; the records of its upstream in another cluster are not
	// trusted or cannot be read; or it names no candidate source at all. The
	// recorder adds RecordStoreReady: False when the registry has no
	// Referrers API, cannot be reached or holds a stream it refuses to fork.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []Condition `json:"conditions,omitempty"`
	// UpstreamSeen is the newest record this Stage read from the upstream
	// stream it follows (candidates.upstreamRecords). A later read that ends
	// before it, or holds another record at its seq, offers nothing, so
	// records removed from the upstream's tail never go unseen.
	// +optional
	UpstreamSeen RecordMark `json:"upstreamSeen,omitzero"`
	// UpstreamAcknowledged holds the ids of the unrecordable stand-ins in the
	// upstream stream that a person on this Stage checked and acknowledged,
	// oldest first (cap 50). They no longer stop the Stage offering.
	// +optional
	UpstreamAcknowledged []string `json:"upstreamAcknowledged,omitempty"`
	// Candidate is the newest eligible candidate the stage reconciler saw.
	// +optional
	Candidate string `json:"candidate,omitempty"`
	// LastCheck is the newest re-check of the running version after it shipped.
	// +optional
	LastCheck *Recheck `json:"lastCheck,omitempty"`
	// AuthFreshness is how old a login the promoter accepts on an approval
	// (a Go duration). Clients ask for a fresh login with a margin under it
	// (DecisionFreshnessFor). Empty: no login is too old, and a person
	// decides with the session they have.
	// +optional
	AuthFreshness string `json:"authFreshness,omitempty"`
	// ObservedGeneration is the Stage generation the stage reconciler last
	// judged Ready from.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Level is the Stage's autonomy level. The stage reconciler seeds it from
	// spec.startLevel; after that only an autonomy operation writes it.
	// +optional
	Level Level `json:"level,omitempty"`
	// ObservedRevision is what GitOps' source reports while the Stage observes.
	// +optional
	ObservedRevision *ObservedRevision `json:"observedRevision,omitempty"`
	// Shadow holds the newest would-have decisions while the Stage observes,
	// newest first.
	// +optional
	// +kubebuilder:validation:MaxItems=20
	Shadow []Shadow `json:"shadow,omitempty"`
	// Proposal is the next level Pacto proposes, or why not yet.
	// +optional
	Proposal *Proposal `json:"proposal,omitempty"`
}

// Condition is a Kubernetes status condition, so `kubectl wait
// --for=condition=Ready`, kstatus and Argo CD read it. Write it with
// SetCondition.
type Condition struct {
	Type string `json:"type"`
	// +kubebuilder:validation:Enum=True;False;Unknown
	Status string `json:"status"`
	// +optional
	Reason string `json:"reason,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// LastTransitionTime is when Status last changed (RFC 3339).
	// +optional
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
	// ObservedGeneration is the Stage generation the condition was set from.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
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

// Params are the operation parameters. On the pointer facet From and To are
// OCI manifest digests; on the level facet they are level names.
type Params struct {
	// +optional
	From string `json:"from,omitempty"`
	// +optional
	To string `json:"to,omitempty"`
	// Undoes is the uid of the operation a rollback undoes.
	// +optional
	Undoes string `json:"undoes,omitempty"`
	// Record is the id of the upstream stand-in an acknowledge accepts.
	// +optional
	Record string `json:"record,omitempty"`
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
	// +kubebuilder:validation:Enum=delivery.pacto.trianalab.io/promote;delivery.pacto.trianalab.io/rollback;delivery.pacto.trianalab.io/override;delivery.pacto.trianalab.io/unfreeze;delivery.pacto.trianalab.io/freeze;delivery.pacto.trianalab.io/autonomy;delivery.pacto.trianalab.io/acknowledge
	Type string `json:"type"`
	// Stage is the name of a Stage in the operation's namespace.
	Stage string `json:"stage"`
	// +kubebuilder:validation:Enum=pointer;freeze;level;upstream
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
	// declared: nobody may answer when identities.requireOwnerTeam is true
	// (the default), else any person.
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
	// ToVersion is the contract service.version of To, when it could be read.
	// +optional
	ToVersion string `json:"toVersion,omitempty"`
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
	// Held is when the operation was last held for a person. An approval
	// created before it answered an earlier question and does not count.
	// +optional
	Held string `json:"held,omitempty"`
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
	// Guarded says the promoter found its admission policies guarding the
	// cluster when it admitted this operation. The promoter admits nothing
	// otherwise, so it is always true. A record from an earlier promoter or
	// another cluster may lack it, and its actor and approvers are then only
	// what their writers claimed.
	// +optional
	Guarded bool `json:"guarded,omitempty"`
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
	// ApprovedTeams are the teams the question is asked of (plan.floor.owners)
	// that an approval counted for since the operation was last held. A change
	// asked of several teams goes ahead only once every one of them answered.
	// +optional
	ApprovedTeams []string `json:"approvedTeams,omitempty"`
	// +optional
	Pointer PointerChange `json:"pointer,omitzero"`
	// Applied is true once the pointer moved for this operation.
	// +optional
	Applied bool `json:"applied,omitempty"`
	// +optional
	Times Times `json:"times,omitzero"`
	// +optional
	Seq int64 `json:"seq,omitempty"`
	// Policy is the Stage spec the operation concluded under, taken when it
	// concluded, so its record says what governed it however late it is
	// written.
	// +optional
	Policy *StageSpec `json:"policy,omitempty"`
	// StageUID is the uid of the Stage that gave the operation its seq. A
	// Stage deleted and recreated with the same name starts its seqs over,
	// and an operation left over from the old one is not part of the new one.
	// +optional
	StageUID string `json:"stageUID,omitempty"`
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
	Operation    string `json:"operation"`
	OperationUID string `json:"operationUID"`
	IntentDigest string `json:"intentDigest"`
	// +kubebuilder:validation:Enum=approve;reject;accept-unknown
	Decision Decision `json:"decision"`
	// +optional
	Note string `json:"note,omitempty"`
	// By must equal the apiserver username of whoever created the Approval.
	By string `json:"by"`
	// AuthTime is the login time the apiserver reported for that user
	// (userInfo.extra["delivery.pacto.trianalab.io/auth-time"], RFC 3339 or Unix seconds).
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
