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
	// IntentDigest, when set on propose, must still match: anything that moved
	// since the plan fails with PlanStale and a fresh plan.
	IntentDigest string `json:"intentDigest,omitempty"`
	// Name of the Operation to create; empty generates one.
	Name string `json:"name,omitempty"`
}
