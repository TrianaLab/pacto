package delivery

// ObjectMeta is the part of Kubernetes object metadata delivery clients read.
type ObjectMeta struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace,omitempty"`
	UID               string            `json:"uid,omitempty"`
	CreationTimestamp string            `json:"creationTimestamp,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Finalizers        []string          `json:"finalizers,omitempty"`
}

// StageObject is a Stage as a client reads it.
type StageObject struct {
	Metadata ObjectMeta  `json:"metadata"`
	Spec     StageSpec   `json:"spec"`
	Status   StageStatus `json:"status"`
}

// OperationObject is an Operation as a client reads it.
type OperationObject struct {
	Metadata ObjectMeta      `json:"metadata"`
	Spec     OperationSpec   `json:"spec"`
	Status   OperationStatus `json:"status"`
}

// Request is what a person or an agent asks for: the input of plan and
// propose. A pointer move names To (promote, override) or Undoes (rollback:
// the name of the operation it undoes).
type Request struct {
	Namespace string `json:"namespace"`
	Stage     string `json:"stage"`
	// Type is a Tier 0 type id; empty means pacto.dev/promote.
	Type string `json:"type,omitempty"`
	To   string `json:"to,omitempty"`
	// From defaults to the Stage's current pointer.
	From       string `json:"from,omitempty"`
	Undoes     string `json:"undoes,omitempty"`
	Reason     string `json:"reason,omitempty"`
	OnBehalfOf string `json:"onBehalfOf,omitempty"`
	// Cause is why, when it is a verdict the caller cites: the promoter runs
	// it again at admission and refuses a cause that does not hold.
	Cause *Cause `json:"cause,omitempty"`
	// IntentDigest, when set on propose, must still match: anything that moved
	// since the plan fails with PlanStale and a fresh plan.
	IntentDigest string `json:"intentDigest,omitempty"`
	// Name of the Operation to create; empty generates one.
	Name string `json:"name,omitempty"`
}

// Explanation is a live operation in plain terms: what was asked, where it
// is, every verdict with its citation and how to run that citation again, and
// the Stage as it stands now.
type Explanation struct {
	Operation string        `json:"operation"`
	Summary   string        `json:"summary"`
	Spec      OperationSpec `json:"spec"`
	Status    struct {
		Phase        Phase        `json:"phase"`
		Outcome      Outcome      `json:"outcome,omitempty"`
		Verification Verification `json:"verification,omitempty"`
		Reason       string       `json:"reason,omitempty"`
		Message      string       `json:"message,omitempty"`
		Question     string       `json:"question,omitempty"`
		ActorClass   ActorClass   `json:"actorClass,omitempty"`
		Seq          int64        `json:"seq,omitempty"`
	} `json:"status"`
	Verdicts []ExplainedVerdict `json:"verdicts"`
	// Order is what must move first, from the plan the promoter admitted.
	Order *Order   `json:"order,omitempty"`
	Stage StageNow `json:"stage"`
}

// ExplainedVerdict is a verdict and how to run its citation again.
type ExplainedVerdict struct {
	Verdict
	Rerun string `json:"rerun"`
}

// StageNow is the Stage an operation acts on, as it stands.
type StageNow struct {
	Name          string   `json:"name"`
	Pointer       string   `json:"pointer,omitempty"`
	Frozen        bool     `json:"frozen,omitempty"`
	FrozenReason  string   `json:"frozenReason,omitempty"`
	AutoHold      bool     `json:"autoHold,omitempty"`
	Verified      []string `json:"verified,omitempty"`
	FailedDigests []string `json:"failedDigests,omitempty"`
	LastCheck     *Recheck `json:"lastCheck,omitempty"`
}

// HistoryEntry is one operation on a Stage.
type HistoryEntry struct {
	Seq          int64        `json:"seq,omitempty"`
	Operation    string       `json:"operation"`
	Type         string       `json:"type"`
	From         string       `json:"from,omitempty"`
	To           string       `json:"to,omitempty"`
	Actor        string       `json:"actor"`
	ActorClass   ActorClass   `json:"actorClass,omitempty"`
	Phase        Phase        `json:"phase,omitempty"`
	Outcome      Outcome      `json:"outcome,omitempty"`
	Verification Verification `json:"verification,omitempty"`
	Reason       string       `json:"reason,omitempty"`
	Finished     string       `json:"finished,omitempty"`
	// Kind is the record's kind (operation, unrecordable or gap); Record is
	// its id. Both are empty for a line read from an Operation's status.
	Kind   string `json:"kind,omitempty"`
	Record string `json:"record,omitempty"`
	// Pointer is the move this operation made, when it moved the Stage.
	Pointer *PointerChange `json:"pointer,omitempty"`
}

// History sources.
const (
	SourceRecords = "records"
	SourceStatus  = "status"
)

// History is what happened on a Stage. With records it is read from the
// signed records in the registry, plus the operations not recorded yet; with
// no records it is read from the Operation objects still in the cluster, and
// is partial. A missing line is never proof that nothing happened unless
// completeness is complete.
type History struct {
	Stage        StageNow `json:"stage"`
	AsOf         string   `json:"asOf"`
	Completeness string   `json:"completeness"`
	Note         string   `json:"note"`
	// Source is records or status.
	Source string `json:"source"`
	// Verified says the records' signatures were checked against a trust
	// config. Records read with none are listed, but nothing vouches for them.
	Verified bool `json:"verified,omitempty"`
	// Head is the newest record's id.
	Head string `json:"head,omitempty"`
	// Problems are gaps, forks and hidden changes the records show.
	Problems []string `json:"problems,omitempty"`
	// Unreadable names what could not be read or verified, and why.
	Unreadable []string       `json:"unreadable,omitempty"`
	Entries    []HistoryEntry `json:"entries"`
}

// Blame says who put a version in place on a Stage: the change that moved the
// Stage to it most recently, who asked, on whose behalf, who approved and
// what every gate said. An empty Change with a partial completeness means
// unknown, never nobody.
type Blame struct {
	Stage        StageNow         `json:"stage"`
	Digest       string           `json:"digest"`
	AsOf         string           `json:"asOf"`
	Completeness string           `json:"completeness"`
	Source       string           `json:"source"`
	Note         string           `json:"note"`
	Change       *HistoryEntry    `json:"change,omitempty"`
	OnBehalfOf   string           `json:"onBehalfOf,omitempty"`
	IntentReason string           `json:"intentReason,omitempty"`
	Approvals    []RecordApproval `json:"approvals,omitempty"`
	Verdicts     []Verdict        `json:"verdicts,omitempty"`
	Unreadable   []string         `json:"unreadable,omitempty"`
}

// Undo is how to undo one operation, as a plan nobody has proposed: a
// rollback when it is the change the Stage runs now, a promote back to the
// version it replaced when something changed since, or nothing when it moved
// nothing. Pacto never proposes it.
type Undo struct {
	Operation string `json:"operation"`
	// Kind is rollback, promote or none.
	Kind    string `json:"kind"`
	Note    string `json:"note"`
	Plan    *Plan  `json:"plan,omitempty"`
	Command string `json:"command,omitempty"`
}

// RecordCheck is one record a verification read.
type RecordCheck struct {
	Seq       int64  `json:"seq"`
	Record    string `json:"record"`
	Manifest  string `json:"manifest"`
	Kind      string `json:"kind"`
	Operation string `json:"operation,omitempty"`
}

// StreamVerification is `record verify`'s answer: every record of a Stage's
// stream verified against the trust config and the chain checked from seq 1
// to the head the Stage names.
type StreamVerification struct {
	Stage      string        `json:"stage"`
	Stream     string        `json:"stream"`
	Anchor     string        `json:"anchor"`
	AsOf       string        `json:"asOf"`
	OK         bool          `json:"ok"`
	Head       string        `json:"head,omitempty"`
	Trust      string        `json:"trust"`
	Records    []RecordCheck `json:"records"`
	Problems   []string      `json:"problems,omitempty"`
	Unreadable []string      `json:"unreadable,omitempty"`
	// Cosign is how to check one record with standard tools, without Pacto.
	Cosign string `json:"cosign"`
}
