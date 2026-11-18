package delivery

// RecordView is one record of a Stage's stream, read by its id: the change
// it records, who asked and why, and every verdict with how to run it again.
type RecordView struct {
	Namespace    string             `json:"namespace"`
	Stage        string             `json:"stage"`
	Record       string             `json:"record"`
	Verified     bool               `json:"verified" doc:"Its signature was checked against a trust config"`
	Trust        string             `json:"trust,omitempty" doc:"The digest of the trust config it was verified with; a host with no trust config, such as the WASM demo, names the key instead"`
	Entry        HistoryEntry       `json:"entry"`
	IntentReason string             `json:"intentReason,omitempty"`
	OnBehalfOf   string             `json:"onBehalfOf,omitempty" doc:"Asserted by the actor, not verified"`
	Verdicts     []ExplainedVerdict `json:"verdicts" doc:"Every gate verdict the record keeps, with how to run its citation again"`
	Note         string             `json:"note"`
	AsOf         string             `json:"asOf" doc:"When the stream was read (RFC 3339)"`
}

// RecordViewOf is rec, read from the stream of ns/stage, as a RecordView.
// trust is the digest of the trust config the stream was verified with, ""
// when none was given.
func RecordViewOf(ns, stage string, rec StoredRecord, trust, asOf string) RecordView {
	p := rec.Statement.Predicate
	v := RecordView{Namespace: ns, Stage: stage, Record: rec.ID, Verified: trust != "", Trust: trust, Entry: RecordEntry(rec),
		IntentReason: p.Intent.Reason, Verdicts: []ExplainedVerdict{}, AsOf: asOf,
		Note: "read from the record in the registry; no trust config was given, so its signature was not checked"}
	if trust != "" {
		v.Note = "read from the signed record in the registry, verified with trust config " + trust
	}
	if p.Actor != nil && p.Actor.OnBehalfOf != nil {
		v.OnBehalfOf = p.Actor.OnBehalfOf.User
	}
	for _, x := range p.Verdicts {
		v.Verdicts = append(v.Verdicts, ExplainedVerdict{Verdict: x, Rerun: RerunHint(x)})
	}
	return v
}

// Whether an approval record counted: only the operation record of its
// operation can say.
const (
	Counted        = "counted"
	NotCounted     = "not counted"
	CountedUnknown = "unknown"
)

// RecordAt is one record read by its registry reference, with no cluster:
// an operation, approval or intent record, verified when it could be.
type RecordAt struct {
	Ref       string `json:"ref" doc:"The reference it was read from"`
	Predicate string `json:"predicate" doc:"The record's predicate type: operation/v1, approval/v1 or operation-intent/v1"`
	Verified  bool   `json:"verified" doc:"Its signature was checked: against the trust config, or for a person's approval against the installation's signers"`
	Signer    string `json:"signer,omitempty" doc:"Who signed it, as verified"`
	// SignerClass, on a verified keyless approval, is who signed it: only a
	// person's (human) decides; any other is a proposal.
	SignerClass  ActorClass `json:"signerClass,omitempty" enum:"human,agent,ci" doc:"On a verified keyless approval: whether a person (human), an agent or a CI workload signed it; only a person's approval decides"`
	ClassifiedBy string     `json:"classifiedBy,omitempty" doc:"What classified the signer: policy:<rule name> or default:<reason>"`
	Note         string     `json:"note" doc:"What was checked, or why it is unverified"`
	// Counted, on an approval, is counted, not counted or unknown.
	Counted    string             `json:"counted,omitempty" enum:"counted,not counted,unknown" doc:"On a verified approval: whether the operation record lists it"`
	CountedWhy string             `json:"countedWhy,omitempty" doc:"Why it is unknown whether the approval counted"`
	AsOf       string             `json:"asOf" doc:"When it was read (RFC 3339)"`
	Operation  *RecordView        `json:"operation,omitempty"`
	Approval   *ApprovalStatement `json:"approval,omitempty"`
	Intent     *IntentStatement   `json:"intent,omitempty"`
}
