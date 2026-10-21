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
}

// History is what happened on a Stage, read from the live Operation objects
// until signed records exist. It is partial: a deleted operation is gone.
type History struct {
	Stage        StageNow       `json:"stage"`
	AsOf         string         `json:"asOf"`
	Completeness string         `json:"completeness"`
	Note         string         `json:"note"`
	Entries      []HistoryEntry `json:"entries"`
}
