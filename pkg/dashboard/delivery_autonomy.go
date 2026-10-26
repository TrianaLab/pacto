package dashboard

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// ── the path to autonomy ───────────────────────────────────────────────
//
// How far each Stage has climbed the autonomy ladder (observe, advise,
// auto-nonbreaking, autonomous), what Pacto proposes next and what stands in
// the way. The dashboard only reads: a rung moves through a proposed and
// approved operation, and a fix is a command or snippet a person copies.

// AutonomyBlocker is one thing that keeps a Stage from the next rung.
type AutonomyBlocker struct {
	Kind    string `json:"kind" doc:"missing-contract, undeclared-range, unlabelled-apply-ref, no-observed-digest, no-record-store, needs-verified, needs-cleared-breaking, incident or partial-graph"`
	Subject string `json:"subject" doc:"The service, consumer or caller it is about"`
	Message string `json:"message,omitempty"`
	// Stages names the Stages (namespace/name) whose proposal lists it; none
	// for an uncontracted caller only the fleet snapshot saw.
	Stages []string    `json:"stages,omitempty"`
	Fix    *BlockerFix `json:"fix,omitempty"`
}

// BlockerKind is how many blockers of one kind match, over every page, so a
// page that shows a few of them can say how many there are.
type BlockerKind struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// BlockerFix is what a person copies to clear a blocker. The dashboard never
// runs it.
type BlockerFix struct {
	Command string `json:"command,omitempty" doc:"A shell command to run"`
	Snippet string `json:"snippet,omitempty" doc:"YAML to add to a contract"`
}

// AutonomyProposal is the next rung Pacto proposes for a Stage, or why not.
type AutonomyProposal struct {
	Rung     string            `json:"rung,omitempty" doc:"Empty: nothing to propose yet"`
	Evidence []string          `json:"evidence,omitempty" doc:"The digests or operations that earned it"`
	Blockers []AutonomyBlocker `json:"blockers,omitempty"`
}

// ShadowDecision is what Pacto would have done with one candidate on an
// observe Stage, and what GitOps then did. It is advisory status, not a
// signed record.
type ShadowDecision struct {
	Candidate    string   `json:"candidate"`
	From         string   `json:"from,omitempty"`
	Predicted    string   `json:"predicted" doc:"Proceeds, Held, Rejected or Waits, as a fully automatic Stage would have"`
	Decision     string   `json:"decision,omitempty" doc:"The decision a person would have been asked: approve or accept-unknown"`
	Owners       []string `json:"owners,omitempty"`
	Question     string   `json:"question,omitempty"`
	IntentDigest string   `json:"intentDigest,omitempty"`
	PlannedAt    string   `json:"plannedAt,omitempty"`
	ObservedAt   string   `json:"observedAt,omitempty" doc:"When GitOps' source reported this candidate; not when it rolled out"`
	Verification string   `json:"verification,omitempty"`
}

// stageLadder is the part of a Stage's status the ladder writes.
type stageLadder struct {
	Rung     string            `json:"rung"`
	Proposal *AutonomyProposal `json:"proposal"`
	Shadow   []ShadowDecision  `json:"shadow"`
	Stream   struct {
		Concluded []struct {
			Type    string `json:"type"`
			Person  bool   `json:"person"`
			Outcome string `json:"outcome"`
		} `json:"concluded"`
	} `json:"stream"`
}

// uncontractedCaller is a workload seen calling a contracted service that no
// contract describes.
type uncontractedCaller struct {
	Caller    string `json:"caller"`
	ToService string `json:"toService"`
}

// rewire reads v by its JSON field names into T.
func rewire[T any](v any) (t T) {
	b, _ := json.Marshal(v) // plain data: never fails
	_ = json.Unmarshal(b, &t)
	return t
}

// ponytail: ladderOf maps the Stage's typed ladder status onto the dashboard's
// wire shape by JSON field names (the names match). Both are vars so tests can
// supply a ladder without building a whole status.
var (
	ladderOf       = func(st delivery.StageObject) stageLadder { return rewire[stageLadder](st.Status) }
	uncontractedOf = func(snap *fleet.FleetSnapshot) []uncontractedCaller {
		var out []uncontractedCaller
		for _, c := range snap.UncontractedCallers {
			out = append(out, uncontractedCaller{Caller: c.Caller, ToService: string(c.ToService)})
		}
		return out
	}
)

// AutonomyStage is one Stage's rung, its proposal and its newest would-have
// decision.
type AutonomyStage struct {
	Namespace   string            `json:"namespace"`
	Stage       string            `json:"stage"`
	Service     string            `json:"service"`
	Environment string            `json:"environment"`
	Rung        string            `json:"rung,omitempty" doc:"observe, advise, auto-nonbreaking or autonomous; empty when the Stage does not report one"`
	Proposal    *AutonomyProposal `json:"proposal,omitempty"`
	LastShadow  *ShadowDecision   `json:"lastShadow,omitempty"`
}

// ContractShare is how many of the services Pacto has seen have a contract.
type ContractShare struct {
	Services            int `json:"services" doc:"Services with a contract"`
	UncontractedCallers int `json:"uncontractedCallers" doc:"Distinct workloads seen calling a contracted service with no contract of their own"`
}

// PromotionCount is how many promotions in the Stages' windows no person
// asked for or approved.
type PromotionCount struct {
	Count int `json:"count"`
	Of    int `json:"of" doc:"Succeeded promotions in the windows: the last 20 operations per Stage"`
	// Untyped is how many operations in the windows carry no type, so Pacto
	// cannot tell whether they were promotions.
	Untyped int `json:"untyped" doc:"Operations in the windows with no type: Pacto cannot tell whether they were promotions"`
}

// autonomyPage is how many Stages, and how many blockers, one page holds.
const autonomyPage = 25

type autonomyInput struct {
	PageQuery
	Blockers int `query:"blockers" minimum:"0" doc:"Where the page of blockers starts; limit and q apply to it too"`
}

type autonomyOutput struct {
	Body struct {
		Listing
		// Contracted is absent when the dashboard has no fleet snapshot.
		Contracted              *ContractShare    `json:"contracted,omitempty"`
		PromotionsWithoutPerson PromotionCount    `json:"promotionsWithoutPerson" doc:"Counted over every Stage, not only this page"`
		Stages                  []AutonomyStage   `json:"stages"`
		Page                    Page              `json:"page" doc:"Which Stages this page holds"`
		Blockers                []AutonomyBlocker `json:"blockers"`
		BlockersPage            Page              `json:"blockersPage" doc:"Which blockers this page holds, grouped by kind"`
		BlockerKinds            []BlockerKind     `json:"blockerKinds" doc:"How many blockers of each kind match, over every page, in page order"`
	}
}

func (s *Server) deliveryAutonomy(ctx context.Context, in *autonomyInput) (*autonomyOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	stages, _, l, err := s.lists(ctx, api, false)
	if err != nil {
		return nil, err
	}
	var callers []uncontractedCaller
	out := &autonomyOutput{}
	if q := s.publishedQuery(ctx); q != nil {
		snap := q.SnapshotForSerialization()
		callers = uncontractedOf(snap)
		out.Body.Contracted = &ContractShare{Services: len(snap.Services), UncontractedCallers: distinctCallers(callers)}
	}
	b := &out.Body
	b.Listing = l
	rows, blockers, count := autonomy(stages, callers)
	b.PromotionsWithoutPerson = count
	rows = filter(rows, func(r AutonomyStage) bool { return matches(in.Q, r.Namespace+"/"+r.Stage, r.Service, r.Environment) })
	blockers = filter(blockers, func(x AutonomyBlocker) bool {
		return matches(in.Q, append([]string{x.Kind, x.Subject}, x.Stages...)...)
	})
	b.Stages, b.Page = pageOf(rows, in.Offset, in.Limit, autonomyPage)
	b.BlockerKinds = []BlockerKind{}
	for _, x := range blockers { // sorted by kind, so one kind is one run
		if n := len(b.BlockerKinds); n > 0 && b.BlockerKinds[n-1].Kind == x.Kind {
			b.BlockerKinds[n-1].Count++
		} else {
			b.BlockerKinds = append(b.BlockerKinds, BlockerKind{Kind: x.Kind, Count: 1})
		}
	}
	b.Blockers, b.BlockersPage = pageOf(blockers, in.Blockers, in.Limit, autonomyPage)
	return out, nil
}

func distinctCallers(cs []uncontractedCaller) int {
	seen := map[string]bool{}
	for _, c := range cs {
		seen[c.Caller] = true
	}
	return len(seen)
}

// autonomy folds the Stages and the uncontracted callers into the page.
func autonomy(stages []delivery.StageObject, callers []uncontractedCaller) ([]AutonomyStage, []AutonomyBlocker, PromotionCount) {
	rows, count := []AutonomyStage{}, PromotionCount{}
	byKey := map[[2]string]*AutonomyBlocker{}
	add := func(b AutonomyBlocker, stage, service string) {
		k := [2]string{b.Kind, b.Subject}
		have := byKey[k]
		if have == nil {
			have = &AutonomyBlocker{Kind: b.Kind, Subject: b.Subject, Message: b.Message}
			byKey[k] = have
		}
		if stage != "" && !slices.Contains(have.Stages, stage) {
			have.Stages = append(have.Stages, stage)
		}
		have.Fix = fixFor(have.Kind, have.Subject, service, have.Fix)
	}
	for _, st := range stages {
		lad := ladderOf(st)
		row := AutonomyStage{Namespace: st.Metadata.Namespace, Stage: st.Metadata.Name, Service: st.Spec.Service.Name,
			Environment: st.Spec.Environment, Rung: lad.Rung, Proposal: lad.Proposal}
		if len(lad.Shadow) > 0 {
			row.LastShadow = &lad.Shadow[0]
		}
		rows = append(rows, row)
		if lad.Proposal != nil {
			for _, b := range lad.Proposal.Blockers {
				add(b, row.Namespace+"/"+row.Stage, row.Service)
			}
		}
		for _, c := range lad.Stream.Concluded {
			if c.Type == "" {
				count.Untyped++
			}
			if c.Type == delivery.TypePromote && c.Outcome == string(delivery.Succeeded) {
				count.Of++
				if !c.Person {
					count.Count++
				}
			}
		}
	}
	for _, c := range callers {
		add(AutonomyBlocker{Kind: "missing-contract", Subject: c.Caller,
			Message: c.Caller + " calls " + c.ToService + " at runtime but has no contract"}, "", "")
	}
	blockers := []AutonomyBlocker{}
	for _, b := range byKey {
		slices.Sort(b.Stages)
		blockers = append(blockers, *b)
	}
	slices.SortFunc(blockers, func(a, b AutonomyBlocker) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Subject, b.Subject))
	})
	slices.SortFunc(rows, func(a, b AutonomyStage) int {
		return cmp.Or(cmp.Compare(a.Service, b.Service), cmp.Compare(a.Environment, b.Environment), cmp.Compare(a.Namespace, b.Namespace))
	})
	return rows, blockers, count
}

// fixFor is what a person copies for a blocker: a pacto init command for a
// missing contract, a dependencies entry for a range the consumer does not
// declare. service is the Stage's service the blocker came from; an
// undeclared-range fix grows by one entry per service.
func fixFor(kind, subject, service string, have *BlockerFix) *BlockerFix {
	switch kind {
	case "missing-contract":
		ns := ""
		if rest, ok := strings.CutPrefix(subject, "k8s:"); ok {
			ns, _, _ = strings.Cut(rest, "/")
		}
		cmd := "pacto init --cluster --out ./pacto-drafts"
		if ns != "" {
			cmd = "pacto init --cluster --namespace " + ns + " --out ./pacto-drafts"
		}
		return &BlockerFix{Command: cmd}
	case "undeclared-range":
		if service == "" || have != nil && strings.Contains(have.Snippet, "- name: "+service+"\n") {
			return have
		}
		if have == nil {
			// Placeholders that do not parse as a range, so the copied
			// snippet fails validation until a person picks the versions.
			have = &BlockerFix{Snippet: "# in " + subject + "'s pacto.yaml\ndependencies:\n"}
		}
		have.Snippet += "  - name: " + service + "\n    ref: \"<where " + service + " is published>\"\n    required: true\n" +
			"    compatibility: \"<the " + service + " versions " + subject + " accepts>\"\n"
		return have
	}
	return have
}
