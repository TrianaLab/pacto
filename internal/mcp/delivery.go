package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// DeliveryAPI is what the delivery tools call. The command layer adapts
// app.Delivery to it, so this package never reaches a cluster or a registry.
type DeliveryAPI interface {
	Plan(ctx context.Context, req delivery.Request) (delivery.PlanAnswer, error)
	Propose(ctx context.Context, req delivery.Request) (*delivery.OperationObject, delivery.PlanAnswer, error)
	Candidates(ctx context.Context, namespace, stage string) (*delivery.CandidateVersions, error)
	Get(ctx context.Context, namespace, name string) (*delivery.OperationObject, error)
	Cancel(ctx context.Context, namespace, name string) error
	Explain(ctx context.Context, namespace, name string) (*delivery.Explanation, error)
	History(ctx context.Context, namespace, stage string) (*delivery.History, error)
	Blame(ctx context.Context, namespace, stage, digest string) (*delivery.Blame, error)
	Undo(ctx context.Context, namespace, name, stage string) (*delivery.Undo, error)
	VerifyRecords(ctx context.Context, namespace, stage string) (*delivery.StreamVerification, error)
	Record(ctx context.Context, namespace, stage, id string) (*delivery.RecordView, error)
	Lifecycle(ctx context.Context, q delivery.LifecycleQuery) (*delivery.Lifecycle, error)
	// HistoryAll and VerifyAll walk every Stage in namespace, or in every
	// namespace the caller can list for "".
	// HistoryAll keeps only the Stages of service of when it names one.
	HistoryAll(ctx context.Context, namespace string, of delivery.ServiceRef) (*delivery.Histories, error)
	VerifyAll(ctx context.Context, namespace string) (*delivery.StreamVerifications, error)
	// Status says where each Stage of one service stands, as the caller can
	// see it, in promotion order.
	Status(ctx context.Context, q delivery.StatusQuery) (*delivery.ServiceStatus, error)
}

// noApprove is in every delivery tool description, so an agent never goes
// looking for a way around it.
const noApprove = " There is no approve tool: when a person must decide, the plan names who, and they approve themselves."

// checkedPass explains an evidence gate's Pass, which the dashboard and the
// CLI label Checked (delivery.VerdictLabel): Verdict.Result stays Pass.
const checkedPass = " A pacto.diff, pacto.impact or pacto.environment verdict passes once it ran; read its message (for example " +
	"breaks payments) and the plan's floor to learn whether it holds the change. The dashboard and the CLI show it as Checked."

const deliveryInstructions = "Pacto delivery decides which version each environment (a Stage) runs. Use " +
	"pacto_delivery_status to see where each Stage of a service stands and what you may ask for there, " +
	"pacto_delivery_operation_types to learn what you may propose (with a type, what it checks), " +
	"pacto_delivery_candidates to list the versions a Stage may take with their digests, " +
	"pacto_delivery_operation_plan to see what the promoter will do (verdicts, whether a person is needed, " +
	"the one question they are asked and, in next, what proposing it leads to), then propose with the plan's intent digest " +
	"and the same reason and on_behalf_of, and follow it with pacto_delivery_get. A promote's to takes a version (3.0.0), a tag " +
	"or a digest; a version or tag must name exactly one version pacto publish published that the Stage may take. pacto_delivery_explain shows every verdict with the citation behind it and how to run it " +
	"again, and pacto_delivery_history what happened on a Stage, read from its signed records when the recorder runs. " +
	"pacto_delivery_blame says who put a version in place, pacto_delivery_undo_plan returns the plan that would undo an operation " +
	"(it proposes nothing), pacto_delivery_record_get reads one record by the id a history line names and " +
	"pacto_delivery_record_verify checks every record's signature and the chain. pacto_delivery_lifecycle follows one version of a " +
	"service through every Stage: whether it was published and where it arrived, verified, was rolled back or left. Every history answer " +
	"carries asOf and completeness: when it is partial, a change missing from it may still have happened. A plan's precedent " +
	"says what the records attached to the version say happened to it on other Stages. Unknown is never Pass: an operation Pacto cannot " +
	"judge waits for a person. A verdict you cite as a cause is run again by the promoter and refused if it does not hold. " +
	"When a consumer running in the environment does not accept the new version, the plan's order.moveFirst names that " +
	"consumer, its Stage there and, as candidate, a version of it its upstream Stage verified that accepts both versions: " +
	"plan and propose that promote on the consumer's Stage first, then plan this one again. Pacto never moves the other " +
	"service for you, and order.unknown says what it could not see." + checkedPass + noApprove

// NewDeliveryServer builds a server with the authoring tools and the delivery
// tools. writes registers propose and cancel; the command layer passes true
// only when the caller is not a human identity, so a person's credentials in an
// agent's hands propose nothing through this server.
func NewDeliveryServer(version string, api DeliveryAPI, writes bool, a Authoring) *mcpsdk.Server {
	server := newServer(version, baseInstructions+"\n\n"+deliveryInstructions, a)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_operation_types",
		Description: "List the operation types you may propose, with their facet and who may raise them, or with type set " +
			"describe that one: what it checks, when a person is needed, how it is undone and its digest. Read-only." + noApprove,
		InputSchema: inputSchema(map[string]property{"type": {Type: "string", Description: "Type id to describe, e.g. delivery.pacto.trianalab.io/promote; empty lists them all"}}, nil),
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, a struct {
		Type string `json:"type"`
	}) (*mcpsdk.CallToolResult, any, error) {
		if a.Type == "" {
			r, err := jsonResult(delivery.Types())
			return r, nil, err
		}
		t, ok := delivery.TypeByID(a.Type)
		if !ok {
			return errorResult(fmt.Errorf("unknown operation type %q", a.Type)), nil, nil
		}
		r, err := jsonResult(struct {
			delivery.OperationType
			Digest string `json:"digest"`
		}{t, t.Digest()})
		return r, nil, err
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_operation_plan",
		Description: "Plan an operation without doing it: the intent digest, the running version, the predicted " +
			"verdicts of every gate, whether a person must decide, the one question they would be asked and, when a consumer " +
			"running there does not accept the new version, the order: which consumer must move first and the verified " +
			"version of it to promote. next says in plain words what proposing it leads to: which teams must sign, that an " +
			"agent cannot answer and the operation is then Held, and what happens at the approval deadline. A Rejected plan " +
			"asks nobody anything; its failing verdict says what to do first. Read-only." + checkedPass + noApprove,
		InputSchema: requestSchema(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a requestArgs) (*mcpsdk.CallToolResult, any, error) {
		req, err := a.request()
		if err != nil {
			return errorResult(err), nil, nil
		}
		p, err := api.Plan(ctx, req)
		return resultOrError(p, err)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_get",
		Description: "Read an operation: phase, outcome, verdicts and the question a person is asked while it is held. While it " +
			"is held, next says what happens if nobody decides by the approval deadline. Read-only." + checkedPass + noApprove,
		InputSchema: nameSchema(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a nameArgs) (*mcpsdk.CallToolResult, any, error) {
		op, err := api.Get(ctx, a.Namespace, a.Name)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return resultOrError(delivery.AnswerOperation(op), nil)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_candidates",
		Description: "List the versions a Stage may be promoted to, newest first: every version its candidate source offers " +
			"that pacto publish published, with its digest, the version its contract declares and the digest the Stage runs " +
			"now. Pass one of them as to when you plan; note says why the list is empty. Read-only." + noApprove,
		InputSchema: stageSchema(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a stageArgs) (*mcpsdk.CallToolResult, any, error) {
		c, err := api.Candidates(ctx, a.Namespace, a.Stage)
		return resultOrError(c, err)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_explain",
		Description: "Explain an operation: what was asked, where it is, every gate verdict with its citation (for a health " +
			"check, the exact query with absolute time bounds) and how to run that citation again, plus the Stage as it is now, " +
			"its last re-check and what must move first when the plan found one. Read-only." + checkedPass + noApprove,
		InputSchema: nameSchema(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a nameArgs) (*mcpsdk.CallToolResult, any, error) {
		e, err := api.Explain(ctx, a.Namespace, a.Name)
		return resultOrError(e, err)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_history",
		Description: "List what happened on a Stage. With the recorder installed it is read from the Stage's signed records in the " +
			"registry (source records), plus the operations not recorded yet; otherwise from the operations still in the cluster " +
			"(source status, always partial). The answer says asOf, completeness, problems (missing seqs, forks, hidden changes) " +
			"and what was unreadable: when completeness is partial, a change missing from it may still have happened. Leave stage " +
			"empty to walk every Stage in the namespace (or, with no namespace, every Stage you can list) for a fleet audit; that " +
			"answer is complete only when every Stage was read and every history is complete. Pass since and until (an RFC 3339 " +
			"time or a duration before now such as 2h) to list the changes around a time, such as the hour before a problem " +
			"started; a change running across that time, or whose time is unknown, is always listed. Pass cause (text in the " +
			"cause's reason, operation or cited verdict, ignoring case) or correlation_id (exact) to keep only the changes tied " +
			"to an incident; both narrow the timeline too. A line with no record carries a cause read from the Operation, " +
			"unsigned. Read-only." + noApprove,
		InputSchema: historySchema(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a historyArgs) (*mcpsdk.CallToolResult, any, error) {
		return historyTool(ctx, api, a)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_blame",
		Description: "Say who put a version in place on a Stage (by default the one it runs): the newest change that moved the " +
			"Stage to it, who asked, on whose behalf (asserted), why, who approved and what every gate said, from the records. " +
			"No change with completeness partial means unknown, never nobody. Read-only." + noApprove,
		InputSchema: stageSchema("digest"),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a stageArgs) (*mcpsdk.CallToolResult, any, error) {
		b, err := api.Blame(ctx, a.Namespace, a.Stage, a.Digest)
		return resultOrError(b, err)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_undo_plan",
		Description: "Plan how to undo an operation and propose nothing: a rollback bound to it when it is the change the Stage " +
			"runs now, a promote back to the version it replaced when something changed since (every gate runs), or nothing " +
			"when it moved nothing. To act, review the plan and propose it yourself with its intent digest. Read-only." + noApprove,
		InputSchema: inputSchema(map[string]property{
			"namespace": {Type: "string", Description: "Namespace of the operation"},
			"name":      {Type: "string", Description: "Operation name"},
			"stage":     {Type: "string", Description: "Its Stage, needed only when the Operation was deleted and must be found in the records"},
		}, []string{"namespace", "name"}),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
		Stage     string `json:"stage"`
	}) (*mcpsdk.CallToolResult, any, error) {
		u, err := api.Undo(ctx, a.Namespace, a.Name, a.Stage)
		return resultOrError(u, err)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_record_verify",
		Description: "Verify every signed record of a Stage's stream against the trust config this server was started with and " +
			"check the chain from seq 1 to the head the Stage names: ok, the records, problems and what did not verify, plus the " +
			"eval runners' own signatures on the verdicts each record keeps (who ran each exam, checked against the runner keys of the trust config), and the " +
			"cosign command that checks one record without Pacto. Leave stage empty to verify every Stage's stream in the " +
			"namespace (or, with no namespace, every Stage you can list); a Stage with no records does not verify. Read-only." + noApprove,
		InputSchema: auditSchema(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a stageArgs) (*mcpsdk.CallToolResult, any, error) {
		if a.Stage == "" {
			return resultOrError(api.VerifyAll(ctx, a.Namespace))
		}
		v, err := api.VerifyRecords(ctx, a.Namespace, a.Stage)
		return resultOrError(v, err)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_record_get",
		Description: "Read one signed record of a Stage's stream by its id (a history line's record): the change, who asked and " +
			"approved, every verdict with its citation and how to run it again, and whether its signature was checked against " +
			"the trust config. Read-only." + noApprove,
		InputSchema: inputSchema(map[string]property{
			"namespace": {Type: "string", Description: "Namespace of the Stage"},
			"stage":     {Type: "string", Description: "Stage name"},
			"record":    {Type: "string", Description: "The record id (sha256:...), as a history line names it"},
		}, []string{"namespace", "stage", "record"}),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a struct {
		Namespace string `json:"namespace"`
		Stage     string `json:"stage"`
		Record    string `json:"record"`
	}) (*mcpsdk.CallToolResult, any, error) {
		r, err := api.Record(ctx, a.Namespace, a.Stage, a.Record)
		return resultOrError(r, err)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_lifecycle",
		Description: "Follow one version of a service through delivery, by artifact digest, contract version or contract digest: " +
			"whether pacto publish published it (Flux verified its signature) and, on each Stage you can see, when it arrived, was " +
			"verified, re-checked, rolled back or left, each from its signed record or the Operation's unsigned status. asOf and " +
			"completeness say how much was read. Read-only." + noApprove,
		InputSchema: inputSchema(map[string]property{
			"service":   {Type: "string", Description: "The service the version belongs to"},
			"domain":    {Type: "string", Description: "Its registry domain; a Stage that names none matches any"},
			"namespace": {Type: "string", Description: "Only the Stages in this namespace; empty reads every namespace you can list"},
			"digest":    {Type: "string", Description: "The artifact digest Stages point at (one of digest, version or contract)"},
			"version":   {Type: "string", Description: "The version its contract declares (one of digest, version or contract)"},
			"contract":  {Type: "string", Description: "Its contract: a manifest digest or a digest-pinned reference (one of digest, version or contract)"},
		}, []string{"service"}),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a struct {
		Service   string `json:"service"`
		Domain    string `json:"domain"`
		Namespace string `json:"namespace"`
		Digest    string `json:"digest"`
		Version   string `json:"version"`
		Contract  string `json:"contract"`
	}) (*mcpsdk.CallToolResult, any, error) {
		l, err := api.Lifecycle(ctx, delivery.LifecycleQuery{Namespace: a.Namespace, Service: a.Service, Domain: a.Domain,
			Digest: a.Digest, Version: a.Version, Contract: a.Contract})
		return resultOrError(l, err)
	})
	registerDeliveryStatus(server, api, writes)
	if writes {
		registerDeliveryWrites(server, api)
	}
	return server
}

// noProposeNote ends a status answer on a server that registered no write
// tools, so its offers are not read as something this server can do.
const noProposeNote = "; this MCP server registers no propose tool for your identity; a person proposes from the CLI or the dashboard"

func registerDeliveryStatus(server *mcpsdk.Server, api DeliveryAPI, writes bool) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_status",
		Description: "Where each Stage of one service stands, as you can see it: the version and contract each runs, whether it is " +
			"verified and still watched after release (the newest re-check with its verdicts and citations), its Ready condition with " +
			"what fixes it, its candidate, frozen or auto-hold, changes waiting for a person, its level, and offers: each action " +
			"(promote, unfreeze, freeze, undo) with whether you may ask for it and why. With why it also plans the promote of each " +
			"candidate, not proposed, and says what holds it back. The answer carries asOf and completeness: a partial answer means " +
			"Stages you cannot list may exist. Rows are in promotion order, from each Stage's spec.candidates, and each names its " +
			"upstream; cycles are named in note. Each row's state is the one the dashboard board counts it in, or unknown when the " +
			"Stage's operations could not be read. Read-only." + checkedPass + noApprove,
		InputSchema: inputSchema(map[string]property{
			"service":   {Type: "string", Description: "The service whose Stages to show"},
			"domain":    {Type: "string", Description: "Its registry domain; a Stage that names none matches any"},
			"namespace": {Type: "string", Description: "Only the Stages in this namespace; empty reads every namespace you can list"},
			"why":       {Type: "boolean", Description: "Also plan the promote of each Stage's candidate (proposing nothing) and say what holds it back"},
		}, []string{"service"}),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a struct {
		Service   string `json:"service"`
		Domain    string `json:"domain"`
		Namespace string `json:"namespace"`
		Why       bool   `json:"why"`
	}) (*mcpsdk.CallToolResult, any, error) {
		s, err := api.Status(ctx, delivery.StatusQuery{Namespace: a.Namespace, Service: a.Service, Domain: a.Domain, Why: a.Why})
		if err == nil && !writes {
			s.Note += noProposeNote
		}
		return resultOrError(s, err)
	})
}

func registerDeliveryWrites(server *mcpsdk.Server, api DeliveryAPI) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_operation_propose",
		Description: "Propose an operation. Pass the intent_digest from your plan with the same to, reason and on_behalf_of: " +
			"the digest binds them and the Stage's state, so if one differs or anything moved since, it fails with PlanStale, " +
			"says which parameter differs when it can tell, and returns the fresh plan instead of proposing something you did " +
			"not review." + noApprove,
		InputSchema: requestSchema("intent_digest", "name"),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a requestArgs) (*mcpsdk.CallToolResult, any, error) {
		req, err := a.request()
		if err != nil {
			return errorResult(err), nil, nil
		}
		op, plan, err := api.Propose(ctx, req)
		if err != nil && plan.IntentDigest != "" {
			// PlanStale (or a refused create) after planning: hand back the fresh plan.
			r, jerr := jsonResult(struct {
				Error string              `json:"error"`
				Plan  delivery.PlanAnswer `json:"plan"`
			}{err.Error(), plan})
			r.IsError = true
			return r, nil, jerr
		}
		return resultOrError(struct {
			Operation *delivery.OperationObject `json:"operation"`
			Plan      delivery.PlanAnswer       `json:"plan"`
		}{op, plan}, err)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "pacto_delivery_cancel",
		Description: "Cancel an operation that has not moved the pointer yet. Once it has, cancel is refused: propose a rollback." + noApprove,
		InputSchema: nameSchema(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a nameArgs) (*mcpsdk.CallToolResult, any, error) {
		err := api.Cancel(ctx, a.Namespace, a.Name)
		return resultOrError(map[string]string{"cancelRequested": a.Namespace + "/" + a.Name}, err)
	})
}

func resultOrError(v any, err error) (*mcpsdk.CallToolResult, any, error) {
	if err != nil {
		return errorResult(err), nil, nil
	}
	r, err := jsonResult(v)
	return r, nil, err
}

type requestArgs struct {
	Namespace    string `json:"namespace"`
	Stage        string `json:"stage"`
	Type         string `json:"type"`
	To           string `json:"to"`
	From         string `json:"from"`
	Undoes       string `json:"undoes"`
	Reason       string `json:"reason"`
	OnBehalfOf   string `json:"on_behalf_of"`
	IntentDigest string `json:"intent_digest"`
	Name         string `json:"name"`
	// CauseVerdict is a verdict the caller cites as its cause, as JSON.
	CauseVerdict string `json:"cause_verdict"`
}

func (a requestArgs) request() (delivery.Request, error) {
	req := delivery.Request{Namespace: a.Namespace, Stage: a.Stage, Type: a.Type, To: a.To, From: a.From, Undoes: a.Undoes,
		Reason: a.Reason, OnBehalfOf: a.OnBehalfOf, IntentDigest: a.IntentDigest, Name: a.Name}
	if a.CauseVerdict != "" {
		var v delivery.Verdict
		if err := json.Unmarshal([]byte(a.CauseVerdict), &v); err != nil {
			return req, fmt.Errorf("cause_verdict is not a verdict: %w", err)
		}
		req.Cause = &delivery.Cause{Verdict: &v}
	}
	return req, nil
}

// requestSchema is the plan input, plus the extra propose-only fields.
func requestSchema(extra ...string) map[string]any {
	props := map[string]property{
		"namespace": {Type: "string", Description: "Namespace of the Stage"},
		"stage":     {Type: "string", Description: "Stage name"},
		"type":      {Type: "string", Description: "Operation type (default delivery.pacto.trianalab.io/promote)"},
		"to": {Type: "string", Description: "The version to move to: a version (3.0.0), a tag or an artifact digest (sha256:...). " +
			"A version or tag must name exactly one version pacto publish published that this Stage may take, or it is refused; " +
			"pacto_delivery_candidates lists them. For an autonomy operation, the level"},
		"from":         {Type: "string", Description: "Digest the Stage runs now (defaults to its pointer)"},
		"undoes":       {Type: "string", Description: "For a rollback: the name of the operation it undoes"},
		"reason":       {Type: "string", Description: "Why, for the record"},
		"on_behalf_of": {Type: "string", Description: "The person you act for (asserted, not verified)"},
		"cause_verdict": {Type: "string", Description: "A verdict you cite as the cause, as JSON (a verdict from pacto_delivery_explain); " +
			"the promoter runs it again and refuses a cause that does not hold"},
	}
	for _, e := range extra {
		switch e {
		case "intent_digest":
			props[e] = property{Type: "string", Description: "The intent digest from your plan"}
		case "name":
			props[e] = property{Type: "string", Description: "Operation name (generated when empty)"}
		}
	}
	return inputSchema(props, []string{"namespace", "stage"})
}

type stageArgs struct {
	Namespace string `json:"namespace"`
	Stage     string `json:"stage"`
	Digest    string `json:"digest"`
}

// stageSchema is a Stage input, with digest for blame.
func stageSchema(extra ...string) map[string]any {
	props := map[string]property{
		"namespace": {Type: "string", Description: "Namespace of the Stage"},
		"stage":     {Type: "string", Description: "Stage name"},
	}
	if len(extra) > 0 {
		props["digest"] = property{Type: "string", Description: "The version (sha256:...) to blame; defaults to the one the Stage runs"}
	}
	return inputSchema(props, []string{"namespace", "stage"})
}

type historyArgs struct {
	Namespace     string `json:"namespace"`
	Stage         string `json:"stage"`
	Since         string `json:"since"`
	Until         string `json:"until"`
	Service       string `json:"service"`
	Domain        string `json:"domain"`
	Cause         string `json:"cause"`
	CorrelationID string `json:"correlation_id"`
}

// historySchema is the audit schema plus a time window and, for a walk, a
// service.
func historySchema() map[string]any {
	return inputSchema(map[string]property{
		"namespace": {Type: "string", Description: "Namespace of the Stage; empty with no stage walks every namespace you can list"},
		"stage":     {Type: "string", Description: "Stage name; empty walks every Stage"},
		"since":     {Type: "string", Description: "RFC 3339 time, or a duration before now such as 2h; only changes at or after it"},
		"until":     {Type: "string", Description: "RFC 3339 time, or a duration before now such as 30m; only changes at or before it"},
		"cause": {Type: "string", Description: "Only changes whose cause (reason, operation or cited verdict) names this text, ignoring case; " +
			"it narrows the timeline too"},
		"correlation_id": {Type: "string", Description: "Only changes with exactly this correlation id; it narrows the timeline too"},
		"service":        {Type: "string", Description: "Without a stage: only the Stages of this service"},
		"domain":         {Type: "string", Description: "With service: its registry domain; a Stage that names none matches any"},
	}, nil)
}

// historyTool answers pacto_delivery_history: one Stage's history, or a walk
// of every Stage, with only the changes in the window a asks for.
func historyTool(ctx context.Context, api DeliveryAPI, a historyArgs) (*mcpsdk.CallToolResult, any, error) {
	now := time.Now()
	since, err := when("since", a.Since, now)
	if err != nil {
		return errorResult(err), nil, nil
	}
	until, err := when("until", a.Until, now)
	if err == nil {
		err = delivery.CheckWindow(since, until)
	}
	if err == nil && a.Domain != "" && a.Service == "" {
		err = errors.New("domain needs service")
	}
	if err != nil {
		return errorResult(err), nil, nil
	}
	f := delivery.EntryFilter{Cause: a.Cause, CorrelationID: a.CorrelationID}
	if a.Stage == "" {
		hs, err := api.HistoryAll(ctx, a.Namespace, delivery.ServiceRef{Name: a.Service, Domain: a.Domain})
		if err != nil {
			return errorResult(err), nil, nil
		}
		start, end := delivery.TimelineWindow(since, until, now)
		var all []delivery.HistoryEntry
		for i := range hs.Histories {
			hs.Histories[i].History = delivery.FilterEntries(delivery.WindowEntries(hs.Histories[i].History, since, until), f)
			all = append(all, hs.Histories[i].Entries...)
		}
		hs.Timeline = new(delivery.Timeline(all, start, end))
		return resultOrError(hs, nil)
	}
	if a.Service != "" || a.Domain != "" {
		return errorResult(errors.New("service filters the walk; leave stage empty")), nil, nil
	}
	h, err := api.History(ctx, a.Namespace, a.Stage)
	if err == nil {
		*h = delivery.FilterEntries(delivery.WindowEntries(*h, since, until), f)
		start, end := delivery.TimelineWindow(since, until, now)
		h.Timeline = new(delivery.Timeline(h.Entries, start, end))
	}
	return resultOrError(h, err)
}

// when reads the tool argument name: an RFC 3339 time or a duration before
// now; empty is none.
func when(name, v string, now time.Time) (time.Time, error) {
	t, err := delivery.ParseWhen(v, now)
	if err != nil {
		return t, fmt.Errorf("%s %w", name, err)
	}
	return t, nil
}

// auditSchema is one Stage, or with stage empty every Stage in the
// namespace, or with both empty every Stage the caller can list.
func auditSchema() map[string]any {
	return inputSchema(map[string]property{
		"namespace": {Type: "string", Description: "Namespace of the Stage; empty with no stage walks every namespace you can list"},
		"stage":     {Type: "string", Description: "Stage name; empty walks every Stage"},
	}, nil)
}

type nameArgs struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

func nameSchema() map[string]any {
	return inputSchema(map[string]property{
		"namespace": {Type: "string", Description: "Namespace of the operation"},
		"name":      {Type: "string", Description: "Operation name"},
	}, []string{"namespace", "name"})
}
