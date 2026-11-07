package fleet

// CreationStep is one step of creating a service from a golden path: what
// pacto init --from and pacto_create report when they create it, and what the
// fleet derives for a service that names its origin. It is derived each time
// and never persisted. A step is done only on evidence; whatever Pacto cannot
// observe is unknown with the reason in Detail.
type CreationStep struct {
	Step string `json:"step" enum:"signature,contract,validation,plugin,delivery,published,fleet,deployed,provisioning"`
	// Name is the plugin's name for a plugin step.
	Name   string `json:"name,omitempty"`
	Status string `json:"status" enum:"done,planned,skipped,unknown,failed,not-applicable"`
	Detail string `json:"detail,omitempty"`
}

// Creation steps.
const (
	StepSignature    = "signature"
	StepContract     = "contract"
	StepValidation   = "validation"
	StepPlugin       = "plugin"
	StepDelivery     = "delivery"
	StepPublished    = "published"
	StepFleet        = "fleet"
	StepDeployed     = "deployed"
	StepProvisioning = "provisioning"
)

// Creation step statuses.
const (
	StepStatusDone          = "done"
	StepStatusPlanned       = "planned"
	StepStatusSkipped       = "skipped"
	StepStatusUnknown       = "unknown"
	StepStatusFailed        = "failed"
	StepStatusNotApplicable = "not-applicable"
)
