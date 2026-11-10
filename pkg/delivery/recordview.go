package delivery

// RecordView is one record of a Stage's stream, read by its id: the change
// it records, who asked and why, and every verdict with how to run it again.
type RecordView struct {
	Namespace    string             `json:"namespace"`
	Stage        string             `json:"stage"`
	Record       string             `json:"record"`
	Verified     bool               `json:"verified" doc:"Its signature was checked against a trust config"`
	Trust        string             `json:"trust,omitempty" doc:"The digest of the trust config it was verified with"`
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
