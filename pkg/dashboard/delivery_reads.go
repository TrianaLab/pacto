package dashboard

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"golang.org/x/sync/errgroup"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/logging"
)

// ── reads ──────────────────────────────────────────────────────────────

// Listing is how complete a list of delivery objects is: a namespace the
// caller may not list is left out and named.
type Listing struct {
	AsOf         string   `json:"asOf"`
	Completeness string   `json:"completeness" enum:"complete,partial"`
	Unreadable   []string `json:"unreadable,omitempty" doc:"Namespaces the caller may not list, or * when the namespaces could not be known"`
}

func forbidden(err error) bool {
	var de *DeliveryError
	return errors.As(err, &de) && de.Status == http.StatusForbidden
}

// lists reads every Stage the caller can see, and with ops every Operation:
// in the dashboard's namespace when it has one; else cluster-wide when
// allowed; else in each namespace the caller may list Stages in; else, when
// the host cannot tell those, partially, per namespace of the fleet snapshot.
// A namespace that refuses is dropped and named.
// ponytail: a list per request with the caller's token, polled by the UI;
// add a per-session informer if list load shows on the apiserver.
func (s *Server) lists(ctx context.Context, api DeliveryAPI, withOps bool) ([]delivery.StageObject, []delivery.OperationObject, Listing, error) {
	l := Listing{AsOf: time.Now().UTC().Format(time.RFC3339), Completeness: "complete"}
	namespaces := []string{s.opts.DeliveryNamespace}
	if namespaces[0] == "" {
		// Both at once: for a new person each waits on a permission check.
		var stages []delivery.StageObject
		var ops []delivery.OperationObject
		var serr, oerr error
		var g errgroup.Group
		g.Go(func() error { stages, serr = api.Stages(ctx, ""); return nil })
		g.Go(func() error { ops, oerr = operations(ctx, api, "", withOps); return nil })
		_ = g.Wait()
		err := serr
		if err == nil {
			err = oerr
		}
		if err == nil {
			return stages, ops, l, nil
		}
		if !forbidden(err) {
			return nil, nil, l, err
		}
		switch nss, err := api.StageNamespaces(ctx); {
		case err != nil:
			return nil, nil, l, err
		case nss != nil:
			return listEach(ctx, api, nss, l, withOps)
		}
		// The namespaces asked are a guess (where the fleet's workloads run,
		// not necessarily where Stages live), so the answer is never
		// complete: "*" says namespaces outside the guess were not read.
		l.Completeness, l.Unreadable, namespaces = "partial", []string{"*"}, s.namespaces(ctx)
	}
	return listEach(ctx, api, namespaces, l, withOps)
}

// operations lists a namespace's Operations, or nothing when they are not
// wanted: a list that never reads them never pays for them.
func operations(ctx context.Context, api DeliveryAPI, ns string, want bool) ([]delivery.OperationObject, error) {
	if !want {
		return nil, nil
	}
	return api.Operations(ctx, ns)
}

// listEach lists each namespace, describeParallel at once (one after
// another, a person who reads forty namespaces waited on eighty round trips
// in a row), dropping (and naming) one that refuses.
func listEach(ctx context.Context, api DeliveryAPI, namespaces []string, l Listing, withOps bool) ([]delivery.StageObject, []delivery.OperationObject, Listing, error) {
	sts, oss, errs := make([][]delivery.StageObject, len(namespaces)), make([][]delivery.OperationObject, len(namespaces)), make([]error, len(namespaces))
	var g errgroup.Group
	g.SetLimit(describeParallel)
	for i, ns := range namespaces {
		g.Go(func() error {
			if sts[i], errs[i] = api.Stages(ctx, ns); errs[i] == nil {
				oss[i], errs[i] = operations(ctx, api, ns, withOps)
			}
			return nil
		})
	}
	_ = g.Wait()
	var stages []delivery.StageObject
	var ops []delivery.OperationObject
	for i, ns := range namespaces {
		switch err := errs[i]; {
		case forbidden(err):
			l.Completeness, l.Unreadable = "partial", append(l.Unreadable, ns)
		case err != nil:
			return nil, nil, l, err
		default:
			stages, ops = append(stages, sts[i]...), append(ops, oss[i]...)
		}
	}
	return stages, ops, l, nil
}

// inScope refuses a namespace outside the dashboard's own, as if it were
// not there.
func (s *Server) inScope(ns string) error {
	if s.opts.DeliveryNamespace != "" && ns != s.opts.DeliveryNamespace {
		return Refuse(http.StatusNotFound, "OutOfScope", "this dashboard shows only namespace "+s.opts.DeliveryNamespace)
	}
	return nil
}

// namespaces are the scopes of the fleet snapshot's targets; nil when there
// is no snapshot to ask.
func (s *Server) namespaces(ctx context.Context) []string {
	q := s.publishedQuery(ctx)
	if q == nil {
		return nil
	}
	out := []string{}
	for _, t := range q.SnapshotForSerialization().Targets {
		if t.Scope != "" && !slices.Contains(out, t.Scope) {
			out = append(out, t.Scope)
		}
	}
	slices.Sort(out)
	return out
}

// serviceOwners maps a service name to its declared owner, from the fleet
// snapshot.
func (s *Server) serviceOwners(ctx context.Context) map[string]string {
	out := map[string]string{}
	if q := s.publishedQuery(ctx); q != nil {
		for _, svc := range q.SnapshotForSerialization().Services {
			if o := svc.Owner.DisplayString(); o != "" {
				out[svc.Name] = o
			}
		}
	}
	return out
}

// InboxItem is one operation waiting on someone.
type InboxItem struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Stage     string `json:"stage"`
	Type      string `json:"type"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	// FromVersion and ToVersion are the versions the digests carry, when
	// their artifacts could be read.
	FromVersion string            `json:"fromVersion,omitempty"`
	ToVersion   string            `json:"toVersion,omitempty"`
	Phase       delivery.Phase    `json:"phase"`
	Outcome     delivery.Outcome  `json:"outcome,omitempty"`
	Reason      string            `json:"reason,omitempty"`
	Question    string            `json:"question,omitempty"`
	Decision    delivery.Decision `json:"decision,omitempty" doc:"The decision asked for: approve, or accept-unknown"`
	Owners      []string          `json:"owners,omitempty" doc:"The teams that may answer; empty means no team is declared: nobody may answer when identities.requireOwnerTeam is true (the default), else any person"`
	Consumers   []string          `json:"consumers,omitempty" doc:"The services that made a person necessary"`
	Order       *delivery.Order   `json:"order,omitempty" doc:"What must move first: the consumers whose declared range does not accept the new version, their Stages and any verified version upstream that can go first"`
}

func inboxItem(o delivery.OperationObject) InboxItem {
	it := InboxItem{Namespace: o.Metadata.Namespace, Name: o.Metadata.Name, Stage: o.Spec.Stage, Type: o.Spec.Type,
		From: o.Spec.Params.From, To: o.Spec.Params.To, Phase: o.Status.Phase, Outcome: o.Status.Outcome, Reason: o.Status.Reason,
		Question: o.Status.Question}
	if p := o.Status.Plan; p != nil {
		it.Decision, it.Owners, it.Consumers = p.Floor.Decision, p.Floor.Owners, p.Floor.Consumers
		if !o.Status.Applied { // once it shipped, what had to move first is history
			it.Order = p.Order
		}
	}
	if it.Phase == delivery.PhaseHeld && (it.Decision == "" || o.Status.Applied) {
		it.Decision = delivery.AcceptUnknown // held after it shipped: the verify gates stayed Unknown
	}
	return it
}

// inboxPage is how many items of each inbox section one page holds.
const inboxPage = 25

type inboxInput struct {
	Held    int    `query:"held" minimum:"0" doc:"Where the page of held changes starts"`
	Frozen  int    `query:"frozen" minimum:"0" doc:"Where the page of frozen Stages starts"`
	Errored int    `query:"errored" minimum:"0" doc:"Where the page of Errored changes starts"`
	Limit   int    `query:"limit" minimum:"0" maximum:"200" doc:"How many of each section per page; 0 means 25"`
	Q       string `query:"q" doc:"Keep only what names this text (namespace, Stage, service or change), ignoring case"`
}

type inboxOutput struct {
	Body struct {
		Listing
		Held        []InboxItem      `json:"held" doc:"Held operations whose question names one of the caller's teams; for break-glass also those that name no team"`
		HeldPage    Page             `json:"heldPage"`
		HeldShared  int              `json:"heldShared" doc:"How many held changes, over every page, more than one team may answer"`
		Frozen      []EnvironmentRow `json:"frozen" doc:"Frozen Stages of the caller's services"`
		FrozenPage  Page             `json:"frozenPage"`
		Errored     []InboxItem      `json:"errored" doc:"Errored operations of the caller's services whose Stage is still frozen"`
		ErroredPage Page             `json:"erroredPage"`
	}
}

func (s *Server) deliveryInbox(ctx context.Context, in *inboxInput) (*inboxOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := api.WhoAmI(ctx)
	if err != nil {
		return nil, err
	}
	stages, ops, l, err := s.lists(ctx, api, true)
	if err != nil {
		return nil, err
	}
	owners := s.serviceOwners(ctx)
	// A service whose owner the snapshot does not know is shown: an inbox
	// that hides what it cannot attribute hides work.
	mine := func(service string) bool {
		o, known := owners[service]
		return !known || caller.Policy.InTeams(caller.Groups, []string{o})
	}
	// frozen holds the caller's Stages that are frozen now. Concluded
	// operations are never deleted, so an Errored one waits on someone only
	// while the pause it caused lasts; after that it is history.
	frozen, frozenStages := frozenOf(stages, mine, in.Q)
	held, errored := inboxQueues(ops, in.Q, frozen, caller)
	out := &inboxOutput{}
	b := &out.Body
	b.Listing = l
	// Only the page is described and drawn: an artifact read per held item
	// and per frozen Stage is the expensive part.
	b.Held, b.HeldPage = pageOf(held, in.Held, in.Limit, inboxPage)
	for _, it := range held {
		if len(it.Owners) > 1 {
			b.HeldShared++
		}
	}
	byStage := stagesByName(stages)
	var page []delivery.StageObject
	page, b.FrozenPage = pageOf(frozenStages, in.Frozen, in.Limit, inboxPage)
	// Every artifact the page names is read in one go.
	var on []digestsOn
	for _, it := range b.Held {
		if st := byStage[it.Namespace+"/"+it.Stage]; st != nil { // nil: the caller cannot see the Stage
			on = append(on, digestsOn{st, []string{it.From, it.To}})
		}
	}
	for i := range page {
		on = append(on, digestsOn{&page[i], []string{page[i].Status.Pointer, page[i].Status.Candidate}})
	}
	teams := map[string]string{}
	vers := versionsAll(ctx, api, nil, teams, on...)
	for i, it := range b.Held {
		if byStage[it.Namespace+"/"+it.Stage] != nil {
			b.Held[i].FromVersion, b.Held[i].ToVersion = vers[it.From], vers[it.To]
		}
	}
	b.Frozen = []EnvironmentRow{}
	for _, st := range page {
		b.Frozen = append(b.Frozen, environmentRow(st, nil, caller, vers, teams))
	}
	mayRaise(ctx, api, b.Frozen)
	b.Errored, b.ErroredPage = pageOf(errored, in.Errored, in.Limit, inboxPage)
	return out, nil
}

// frozenOf returns the caller's frozen Stages by namespace/name, and those
// of them matching q.
func frozenOf(stages []delivery.StageObject, mine func(string) bool, q string) (map[string]bool, []delivery.StageObject) {
	frozen := map[string]bool{}
	var frozenStages []delivery.StageObject
	for _, st := range stages {
		if st.Status.Frozen && mine(st.Spec.Service.Name) {
			frozen[st.Metadata.Namespace+"/"+st.Metadata.Name] = true
			if matches(q, st.Metadata.Namespace, st.Metadata.Name, st.Spec.Service.Name) {
				frozenStages = append(frozenStages, st)
			}
		}
	}
	return frozen, frozenStages
}

// inboxQueues splits the operations matching q into those held on the caller
// and those errored on a Stage that is still frozen.
func inboxQueues(ops []delivery.OperationObject, q string, frozen map[string]bool, caller delivery.Caller) (held, errored []InboxItem) {
	held, errored = []InboxItem{}, []InboxItem{}
	for _, o := range ops {
		it := inboxItem(o)
		if !matches(q, it.Namespace, it.Stage, it.Name, strings.Join(it.Consumers, " ")) {
			continue
		}
		switch {
		case it.Phase == delivery.PhaseHeld && waitsOn(&o, it, caller):
			held = append(held, it)
		case it.Outcome == delivery.Errored && frozen[it.Namespace+"/"+it.Stage]:
			errored = append(errored, it)
		}
	}
	return held, errored
}

// waitsOn: held op it waits on caller, whose decision the promoter would
// count. A question that names no team is also break-glass's: under
// requireOwnerTeam nobody else can resolve it.
func waitsOn(op *delivery.OperationObject, it InboxItem, caller delivery.Caller) bool {
	return answerRefusal(op, caller, it.Decision) == "" ||
		len(it.Owners) == 0 && caller.Class == delivery.ClassBreakGlass && caller.User != op.Spec.Actor.User
}

// stagesByName indexes Stages by namespace/name.
func stagesByName(stages []delivery.StageObject) map[string]*delivery.StageObject {
	out := map[string]*delivery.StageObject{}
	for i := range stages {
		out[stages[i].Metadata.Namespace+"/"+stages[i].Metadata.Name] = &stages[i]
	}
	return out
}

// EnvironmentRow is one Stage: one service in one environment.
type EnvironmentRow struct {
	Namespace        string   `json:"namespace"`
	Stage            string   `json:"stage"`
	Service          string   `json:"service"`
	Environment      string   `json:"environment"`
	Pointer          string   `json:"pointer,omitempty" doc:"The digest the environment runs"`
	Version          string   `json:"version,omitempty" doc:"The version its contract declares, when the artifact could be read"`
	Verified         bool     `json:"verified" doc:"The running digest passed its verify gates here"`
	Failed           bool     `json:"failed" doc:"The running digest failed here"`
	Frozen           bool     `json:"frozen"`
	FrozenReason     string   `json:"frozenReason,omitempty"`
	AutoHold         bool     `json:"autoHold"`
	Held             int      `json:"held" doc:"Operations on this Stage waiting for a person"`
	Candidate        string   `json:"candidate,omitempty" doc:"The newest eligible candidate"`
	CandidateVersion string   `json:"candidateVersion,omitempty" doc:"The version the candidate's contract declares, when the artifact could be read"`
	Ready            string   `json:"ready,omitempty" doc:"The Ready condition: True, or False with the reason the Stage cannot promote"`
	ReadyMessage     string   `json:"readyMessage,omitempty" doc:"Why the Stage cannot promote, in words, when Ready is not True"`
	Actions          []string `json:"actions" doc:"What the caller's class may ask for here: promote, unfreeze"`
	Level            string   `json:"level,omitempty" doc:"observe, advise, auto-nonbreaking or autonomous; empty when the Stage does not report one"`
	// Shadow is set on an observe Stage only: newest first.
	Shadow []ShadowDecision `json:"shadow,omitempty" doc:"On an observe Stage: what Pacto would have done with each candidate and what GitOps did, newest first. Advisory, not signed"`
}

// environmentRow is one Stage as the board shows it; held counts the held
// operations by namespace/Stage, vers names the versions of digests and teams
// the owner their contracts declare.
func environmentRow(st delivery.StageObject, held map[string]int, caller delivery.Caller, vers, teams map[string]string) EnvironmentRow {
	ss := st.Status
	row := EnvironmentRow{Namespace: st.Metadata.Namespace, Stage: st.Metadata.Name, Service: st.Spec.Service.Name, Environment: st.Spec.Environment,
		Pointer: ss.Pointer, Verified: slices.Contains(ss.Verified, ss.Pointer), Failed: slices.Contains(ss.FailedDigests, ss.Pointer),
		Frozen: ss.Frozen, FrozenReason: ss.FrozenReason, AutoHold: ss.AutoHold, Candidate: ss.Candidate, Actions: []string{}}
	if lad := ladderOf(st); lad.Level != "" {
		row.Level = lad.Level
		if lad.Level == "observe" {
			row.Shadow = lad.Shadow
		}
	}
	for _, c := range ss.Conditions {
		if c.Type == "Ready" {
			row.Ready = strings.TrimSuffix(c.Status+": "+c.Reason, ": ")
			if c.Status != "True" {
				row.ReadyMessage = c.Message
			}
		}
	}
	row.Version, row.CandidateVersion = vers[ss.Pointer], vers[ss.Candidate]
	row.Held = held[row.Namespace+"/"+row.Stage]
	if delivery.MayRaise(delivery.TypePromote, caller.Class, false) && ss.Candidate != "" && ss.Candidate != ss.Pointer {
		row.Actions = append(row.Actions, "promote")
	}
	if (ss.Frozen || ss.AutoHold) && delivery.MayRaise(delivery.TypeUnfreeze, caller.Class, false) && mayUnfreeze(caller, teams[ss.Pointer]) {
		row.Actions = append(row.Actions, "unfreeze")
	}
	return row
}

// mayUnfreeze is the promoter's admission rule for an unfreeze, so the board
// never offers one it would refuse: a person unfreezes only a Stage whose
// running contract names their team (Policy.InTeams); with no team named or
// readable, anyone, or nobody with RequireOwnerTeam. Break-glass may always.
func mayUnfreeze(caller delivery.Caller, team string) bool {
	if caller.Class != delivery.ClassHuman {
		return true
	}
	var teams []string
	if team != "" {
		teams = []string{team}
	}
	return caller.Policy.InTeams(caller.Groups, teams)
}

// environmentsPage is how many services one page of the board holds.
const environmentsPage = 25

type environmentsInput struct {
	PageQuery
	Environment string `query:"environment" doc:"Keep only the services with a Stage in this environment, and only those Stages"`
	Tier        string `query:"tier" enum:"dev,test,prod" doc:"Keep only the services with a Stage in an environment of this tier, and only those Stages"`
}

// envOrder is the way changes travel, matched as name prefixes: each entry's
// tier, in order. ponytail: a name heuristic; an upstream-Stage chain would
// order environments that share no known prefix.
var envOrder = []struct{ prefix, tier string }{
	{"dev", "dev"}, {"sandbox", "dev"}, {"test", "test"}, {"qa", "test"}, {"stag", "test"},
	{"perf", "test"}, {"uat", "test"}, {"preprod", "test"}, {"prod", "prod"},
}

// envRank is where an environment sits in envOrder and its tier; a name no
// prefix matches comes last with no tier, so it is never guessed.
func envRank(env string) (int, string) {
	for i, o := range envOrder {
		if strings.HasPrefix(strings.ToLower(env), o.prefix) {
			return i, o.tier
		}
	}
	return len(envOrder), ""
}

// sortEnvironments orders names the way changes travel, then the rest with
// numbers in number order (env-2 before env-10).
func sortEnvironments(envs []string) {
	slices.SortFunc(envs, func(a, b string) int {
		ra, _ := envRank(a)
		rb, _ := envRank(b)
		return cmp.Or(cmp.Compare(ra, rb), naturalCompare(a, b), cmp.Compare(a, b))
	})
}

// naturalCompare compares runs of digits as numbers and the rest as text.
func naturalCompare(a, b string) int {
	for a != "" && b != "" {
		da, db := digitRun(a), digitRun(b)
		if da > 0 && db > 0 {
			na, nb := strings.TrimLeft(a[:da], "0"), strings.TrimLeft(b[:db], "0")
			if c := cmp.Or(cmp.Compare(len(na), len(nb)), cmp.Compare(na, nb)); c != 0 {
				return c
			}
			a, b = a[da:], b[db:]
			continue
		}
		if c := cmp.Compare(a[0], b[0]); c != 0 {
			return c
		}
		a, b = a[1:], b[1:]
	}
	return cmp.Compare(len(a), len(b))
}

func digitRun(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

type environmentsOutput struct {
	Body struct {
		Listing
		Environments []EnvironmentRow `json:"environments" doc:"The Stages of the services on this page"`
		// Page counts services, not Stages: a page is whole rows of the board.
		Page Page `json:"page" doc:"Which services this page holds; offset and limit count services"`
		// EnvironmentNames are the board's columns on every page, so paging
		// never moves a column.
		EnvironmentNames []string `json:"environmentNames" doc:"Every environment of the services that match q, over every page, ordered the way changes travel: dev, test, prod, then the rest"`
		// EnvironmentTiers names the tier of each environment whose name says it.
		EnvironmentTiers map[string]string `json:"environmentTiers" doc:"dev, test or prod for each environment whose name says which; an environment it cannot tell is left out"`
		// Counts are over every page, so the page's summary is never one page's.
		Counts StageCounts `json:"counts" doc:"Failed, frozen and waiting Stages over every page, after q, environment and tier"`
	}
}

// StageCounts is how many Stages are in each state that needs a person.
type StageCounts struct {
	Failed  int `json:"failed" doc:"Stages whose running digest failed"`
	Frozen  int `json:"frozen"`
	Waiting int `json:"waiting" doc:"Stages with a change waiting for a person"`
}

func (s *Server) deliveryEnvironments(ctx context.Context, in *environmentsInput) (*environmentsOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := api.WhoAmI(ctx)
	if err != nil {
		return nil, err
	}
	stages, ops, l, err := s.lists(ctx, api, true)
	if err != nil {
		return nil, err
	}
	held := map[string]int{}
	for _, o := range ops {
		if o.Status.Phase == delivery.PhaseHeld {
			held[o.Metadata.Namespace+"/"+o.Spec.Stage]++
		}
	}
	stages = filter(stages, func(st delivery.StageObject) bool { return matches(in.Q, st.Spec.Service.Name) })
	out := &environmentsOutput{}
	out.Body.Listing, out.Body.Environments = l, []EnvironmentRow{}
	out.Body.EnvironmentNames, out.Body.EnvironmentTiers = environmentColumns(stages)
	// The environment and tier filters narrow the rows, not the columns a
	// person picks from: a service with no Stage there is left out.
	stages = filter(stages, func(st delivery.StageObject) bool {
		_, tier := envRank(st.Spec.Environment)
		return (in.Environment == "" || st.Spec.Environment == in.Environment) && (in.Tier == "" || tier == in.Tier)
	})
	var services []string
	services, out.Body.Counts = countStages(stages, held)
	var page []string
	page, out.Body.Page = pageOf(slices.Compact(services), in.Offset, in.Limit, environmentsPage)
	var shown []delivery.StageObject
	for _, st := range stages {
		if slices.Contains(page, st.Spec.Service.Name) {
			shown = append(shown, st)
		}
	}
	var on []digestsOn
	for i := range shown {
		on = append(on, digestsOn{&shown[i], []string{shown[i].Status.Pointer, shown[i].Status.Candidate}})
	}
	teams := map[string]string{}
	vers := versionsAll(ctx, api, nil, teams, on...)
	for _, st := range shown {
		out.Body.Environments = append(out.Body.Environments, environmentRow(st, held, caller, vers, teams))
	}
	mayRaise(ctx, api, out.Body.Environments)
	slices.SortFunc(out.Body.Environments, func(a, b EnvironmentRow) int {
		return cmp.Or(cmp.Compare(a.Service, b.Service), cmp.Compare(a.Environment, b.Environment), cmp.Compare(a.Namespace, b.Namespace))
	})
	return out, nil
}

// countStages is the sorted services of stages and how many of the stages
// are failed, frozen or waiting for a person.
func countStages(stages []delivery.StageObject, held map[string]int) ([]string, StageCounts) {
	services, c := []string{}, StageCounts{}
	for _, st := range stages {
		services = append(services, st.Spec.Service.Name)
		if slices.Contains(st.Status.FailedDigests, st.Status.Pointer) {
			c.Failed++
		}
		if st.Status.Frozen {
			c.Frozen++
		}
		if held[st.Metadata.Namespace+"/"+st.Metadata.Name] > 0 {
			c.Waiting++
		}
	}
	slices.Sort(services)
	return services, c
}

// environmentColumns is the board's columns for stages, ordered the way
// changes travel, and the tier of each environment whose name says it.
func environmentColumns(stages []delivery.StageObject) ([]string, map[string]string) {
	envs, tiers := []string{}, map[string]string{}
	for _, st := range stages {
		envs = append(envs, st.Spec.Environment)
		if _, tier := envRank(st.Spec.Environment); tier != "" {
			tiers[st.Spec.Environment] = tier
		}
	}
	slices.Sort(envs)
	envs = slices.Compact(envs)
	sortEnvironments(envs)
	return envs, tiers
}

// PublishedVersion is one digest a service's Stages know about.
type PublishedVersion struct {
	Digest   string `json:"digest"`
	Version  string `json:"version,omitempty"`
	Contract string `json:"contract,omitempty" doc:"The digest-pinned contract the artifact names (delivery.pacto.trianalab.io/contract)"`
	// Signed: Flux verified its signature on a candidate source, so pacto
	// publish published it.
	Signed bool                `json:"signed"`
	Stages []PublishedOnStages `json:"stages"`
}

// PublishedOnStages is where one version stands on one Stage.
type PublishedOnStages struct {
	Namespace   string `json:"namespace"`
	Stage       string `json:"stage"`
	Environment string `json:"environment"`
	Running     bool   `json:"running"`
	Verified    bool   `json:"verified"`
	Failed      bool   `json:"failed"`
}

// PublishedService is the versions of one service.
type PublishedService struct {
	Service  string             `json:"service"`
	Versions []PublishedVersion `json:"versions"`
}

// publishedPage is how many services one page of published versions holds.
const publishedPage = 10

type publishedInput struct {
	PageQuery
}

type publishedOutput struct {
	Body struct {
		Listing
		Services []PublishedService `json:"services"`
		Page     Page               `json:"page" doc:"Which services this page holds; offset and limit count services"`
	}
}

// deliveryPublished reads the versions from what the Stages remember:
// ponytail: digests the caller's Stages know (published, offered, verified,
// failed, running), not every bundle tag in the registry; list registry tags
// when someone needs versions no Stage has seen.
func (s *Server) deliveryPublished(ctx context.Context, in *publishedInput) (*publishedOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	stages, _, l, err := s.lists(ctx, api, false)
	if err != nil {
		return nil, err
	}
	// First where each digest stands, which costs nothing; then only the
	// page's artifacts are read.
	byService, readFrom := publishedDigests(stages, in.Q)
	names := slices.Sorted(maps.Keys(byService))
	out := &publishedOutput{}
	out.Body.Listing, out.Body.Services = l, []PublishedService{}
	names, out.Body.Page = pageOf(names, in.Offset, in.Limit, publishedPage)
	var page []*PublishedVersion
	for _, svc := range names {
		for _, v := range byService[svc] {
			page = append(page, v)
		}
	}
	revs := describeAll(ctx, api, len(page), func(i int) (*delivery.StageObject, string) { return readFrom[page[i]], page[i].Digest })
	for i, v := range page {
		v.Contract, v.Version = revs[i].ContractRef, version(ctx, revs[i])
	}
	for _, svc := range names {
		ps := PublishedService{Service: svc, Versions: []PublishedVersion{}}
		for _, v := range byService[svc] {
			ps.Versions = append(ps.Versions, *v)
		}
		slices.SortFunc(ps.Versions, func(a, b PublishedVersion) int {
			return cmp.Or(compareVersions(b.Version, a.Version), cmp.Compare(a.Digest, b.Digest))
		})
		out.Body.Services = append(out.Body.Services, ps)
	}
	return out, nil
}

// publishedDigests groups every digest the Stages of services matching q
// remember by service, with the Stage to read each one's artifact from.
func publishedDigests(stages []delivery.StageObject, q string) (byService map[string]map[string]*PublishedVersion, readFrom map[*PublishedVersion]*delivery.StageObject) {
	byService = map[string]map[string]*PublishedVersion{}
	readFrom = map[*PublishedVersion]*delivery.StageObject{}
	for i := range stages {
		st := &stages[i]
		ss := st.Status
		svc := st.Spec.Service.Name
		if !matches(q, svc) {
			continue
		}
		if byService[svc] == nil {
			byService[svc] = map[string]*PublishedVersion{}
		}
		for _, d := range slices.Concat(ss.Published, ss.Offered, ss.Verified, ss.FailedDigests, []string{ss.Pointer}) {
			if d == "" {
				continue
			}
			v := byService[svc][d]
			if v == nil {
				v = &PublishedVersion{Digest: d, Stages: []PublishedOnStages{}}
				byService[svc][d], readFrom[v] = v, st
			}
			v.Signed = v.Signed || slices.Contains(ss.Published, d)
			on := PublishedOnStages{Namespace: st.Metadata.Namespace, Stage: st.Metadata.Name, Environment: st.Spec.Environment,
				Running: ss.Pointer == d, Verified: slices.Contains(ss.Verified, d), Failed: slices.Contains(ss.FailedDigests, d)}
			if !slices.Contains(v.Stages, on) {
				v.Stages = append(v.Stages, on)
			}
		}
	}
	return byService, readFrom
}

type historyInput struct {
	Namespace string `query:"namespace" doc:"Namespace of one Stage (with stage); empty lists every Stage the caller can see"`
	Stage     string `query:"stage" doc:"One Stage's name"`
	// With a Stage, the page is of its changes; without, of Stages, each
	// with its newest changes.
	PageQuery
}

// Pages of a history: Stages per page across every Stage, the newest change
// each of them shows there (a one-line summary that links to the rest), and
// changes per page of one Stage.
const (
	historyStagesPage  = 25
	historyRecent      = 1
	historyEntriesPage = 50
)

// StageHistory is one Stage's history and the namespace the Stage lives in,
// which the history itself does not carry. Its entries are newest first, and
// only a page of them.
type StageHistory struct {
	Namespace string `json:"namespace"`
	delivery.History
	EntriesPage Page `json:"entriesPage" doc:"Which of the Stage's changes entries holds, newest first"`
	// The summary of every Stage shows how many problems a history has, not
	// what they are: the lists stay on the Stage's own page.
	ProblemCount    int `json:"problemCount,omitempty" doc:"How many problems the records show. Without a stage, problems and unreadable are left out and only these counts are sent"`
	UnreadableCount int `json:"unreadableCount,omitempty" doc:"How many things could not be read or verified"`
}

type historyOutput struct {
	Body struct {
		Listing
		Histories []StageHistory `json:"histories"`
		Page      Page           `json:"page" doc:"Which Stages this page holds. With a stage, offset, limit and q page its changes instead (see entriesPage)"`
		// Counts are over every page; without a stage only.
		Counts *HistoryCounts `json:"counts,omitempty" doc:"Without a stage: Stages that wait for a person and frozen Stages, over every page"`
		Labels
	}
}

// HistoryCounts is how many Stages, over every page, need a person. Record
// problems are left out: counting them reads every Stage's records.
type HistoryCounts struct {
	Waiting int `json:"waiting" doc:"Stages with a change waiting for a person"`
	Frozen  int `json:"frozen"`
}

// Labels say in words what the identifiers in an answer stand for.
type Labels struct {
	Versions map[string]string `json:"versions,omitempty" doc:"The version each digest named here carries, when its artifact could be read"`
	People   map[string]string `json:"people,omitempty" doc:"The names of the people named here who signed in to this dashboard, by username"`
}

func (s *Server) deliveryHistory(ctx context.Context, in *historyInput) (*historyOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	out := &historyOutput{}
	out.Body.Listing = Listing{AsOf: time.Now().UTC().Format(time.RFC3339), Completeness: "complete"}
	var targets []delivery.StageObject
	// describe: the Stages were read, so their artifacts can be asked for
	// versions. A Stage that cannot be read still has a history to ask for.
	describe := true
	one := in.Namespace != "" && in.Stage != ""
	if one {
		if err := s.inScope(in.Namespace); err != nil {
			return nil, err
		}
		st, err := api.Stage(ctx, in.Namespace, in.Stage)
		if err != nil {
			st, describe = &delivery.StageObject{Metadata: delivery.ObjectMeta{Namespace: in.Namespace, Name: in.Stage}}, false
		}
		targets, out.Body.Page = []delivery.StageObject{*st}, Page{Total: 1, Limit: 1}
	} else {
		stages, ops, l, err := s.lists(ctx, api, true)
		if err != nil {
			return nil, err
		}
		stages = filter(stages, func(st delivery.StageObject) bool {
			return matches(in.Q, st.Metadata.Namespace+"/"+st.Metadata.Name, st.Spec.Service.Name, st.Spec.Environment)
		})
		out.Body.Counts = historyCounts(stages, ops)
		slices.SortFunc(stages, func(a, b delivery.StageObject) int {
			return cmp.Or(cmp.Compare(a.Metadata.Namespace, b.Metadata.Namespace), cmp.Compare(a.Metadata.Name, b.Metadata.Name))
		})
		out.Body.Listing = l
		// Only the page's histories are read: each one reads and verifies a
		// whole record stream.
		targets, out.Body.Page = pageOf(stages, in.Offset, in.Limit, historyStagesPage)
	}
	out.Body.Histories = []StageHistory{}
	out.Body.Versions = map[string]string{}
	var actors []string
	var on []digestsOn
	hs, errs := historiesOf(ctx, api, targets, describe)
	for i, t := range targets {
		if errs[i] != nil {
			return nil, errs[i]
		}
		h := hs[i]
		sh := StageHistory{Namespace: t.Metadata.Namespace, History: *h, ProblemCount: len(h.Problems), UnreadableCount: len(h.Unreadable)}
		entries := slices.Clone(h.Entries)
		slices.Reverse(entries) // newest first: what a reader looks for is the latest change
		if one {
			entries = filter(entries, func(e delivery.HistoryEntry) bool {
				return matches(in.Q, e.Operation, e.Type, e.Actor, string(e.Outcome), string(e.Phase), string(e.Verification), e.Reason)
			})
			sh.Entries, sh.EntriesPage = pageOf(entries, in.Offset, in.Limit, historyEntriesPage)
		} else {
			sh.Entries, sh.EntriesPage = pageOf(entries, 0, historyRecent, historyRecent)
			sh.Problems, sh.Unreadable = nil, nil
		}
		digests := []string{h.Stage.Pointer}
		for _, e := range sh.Entries {
			digests, actors = append(digests, e.From, e.To), append(actors, e.Actor)
		}
		if describe {
			on = append(on, digestsOn{&targets[i], digests})
		}
		out.Body.Histories = append(out.Body.Histories, sh)
	}
	versionsAll(ctx, api, out.Body.Versions, nil, on...)
	out.Body.People = s.named(actors...)
	return out, nil
}

// historyCounts counts the Stages that wait for a person and the frozen ones.
func historyCounts(stages []delivery.StageObject, ops []delivery.OperationObject) *HistoryCounts {
	held := map[string]bool{}
	for _, o := range ops {
		if o.Status.Phase == delivery.PhaseHeld {
			held[o.Metadata.Namespace+"/"+o.Spec.Stage] = true
		}
	}
	c := &HistoryCounts{}
	for _, st := range stages {
		if held[st.Metadata.Namespace+"/"+st.Metadata.Name] {
			c.Waiting++
		}
		if st.Status.Frozen {
			c.Frozen++
		}
	}
	return c
}

// historiesOf reads the history of each target, describeParallel at once: on
// a cluster each one lists the Stage's changes and reads a record stream, so a
// page of 25 read one after another was 50 round trips in a row. read says the
// targets came from the apiserver, so the host is handed them instead of
// getting each Stage again.
func historiesOf(ctx context.Context, api DeliveryAPI, targets []delivery.StageObject, read bool) ([]*delivery.History, []error) {
	hs, errs := make([]*delivery.History, len(targets)), make([]error, len(targets))
	var g errgroup.Group
	g.SetLimit(describeParallel)
	for i, t := range targets {
		g.Go(func() error {
			var listed *delivery.StageObject
			if read {
				listed = &t
			}
			hs[i], errs[i] = api.History(ctx, t.Metadata.Namespace, t.Metadata.Name, listed)
			return nil
		})
	}
	_ = g.Wait()
	return hs, errs
}

// versions adds to into (a new map when nil) the version each digest's
// contract declares, leaving out what cannot be read.
// ponytail: a digest that cannot be read is asked again each time it is
// named; the host caches only what it read.
func versions(ctx context.Context, api DeliveryAPI, stage *delivery.StageObject, into map[string]string, digests ...string) map[string]string {
	return versionsAll(ctx, api, into, nil, digestsOn{stage, digests})
}

// digestsOn are digests to read from one Stage's artifacts.
type digestsOn struct {
	stage   *delivery.StageObject
	digests []string
}

// versionsAll is versions for the digests of many Stages, all read at once:
// a page that asked Stage by Stage read two artifacts at a time. A non-nil
// teams gets the owner each read contract declares (Owner.DisplayString).
func versionsAll(ctx context.Context, api DeliveryAPI, into, teams map[string]string, on ...digestsOn) map[string]string {
	if into == nil {
		into = map[string]string{}
	}
	var ask []string
	from := map[string]*delivery.StageObject{}
	for _, o := range on {
		for _, d := range o.digests {
			// An autonomy change's from and to are level names, not digests
			// (an OCI digest is always algorithm:hex): no artifact to read.
			if _, known := into[d]; !known && strings.Contains(d, ":") && !slices.Contains(ask, d) {
				ask, from[d] = append(ask, d), o.stage
			}
		}
	}
	revs := describeAll(ctx, api, len(ask), func(i int) (*delivery.StageObject, string) { return from[ask[i]], ask[i] })
	for i, d := range ask {
		if v := version(ctx, revs[i]); v != "" {
			into[d] = v
			if teams != nil {
				teams[d] = revs[i].Bundle.Contract.Service.Owner.DisplayString()
			}
		}
	}
	return into
}

// describeParallel is how many digests are described at once. A Describe
// reads a registry, so a page of fifty read one after another took fifty
// round trips; the host caches what it read, so a refresh is cheap either way.
const describeParallel = 8

// describeAll describes n digests, describeParallel at once; at(i) names the
// Stage to read the i-th from and its digest. out[i] answers at(i).
func describeAll(ctx context.Context, api DeliveryAPI, n int, at func(int) (*delivery.StageObject, string)) []delivery.Revision {
	out := make([]delivery.Revision, n)
	var g errgroup.Group
	g.SetLimit(describeParallel)
	for i := range n {
		g.Go(func() error {
			st, d := at(i)
			out[i] = api.Describe(ctx, st, d)
			return nil
		})
	}
	_ = g.Wait()
	return out
}

// version is the version a revision's contract declares; "" when it has
// none, with the reason logged when its artifact could not be read. An
// artifact that names no contract is not a failure and logs nothing.
func version(ctx context.Context, r delivery.Revision) string {
	if r.Bundle == nil {
		if r.Err != "" && !r.NoContract {
			logging.LoggerFromContext(ctx).Warn("reading the version of a digest", "digest", r.Digest, "error", r.Err)
		}
		return ""
	}
	return r.Bundle.Contract.Service.Version
}

// stageVersions is versions for the digests of a change on a Stage read by
// name; nil, with the reason logged, when the Stage cannot be read.
func stageVersions(ctx context.Context, api DeliveryAPI, namespace, stage string, digests ...string) map[string]string {
	st, err := api.Stage(ctx, namespace, stage)
	if err != nil {
		logging.LoggerFromContext(ctx).Warn("reading a Stage to name versions", "namespace", namespace, "stage", stage, "error", err)
		return nil
	}
	return versions(ctx, api, st, nil, digests...)
}

// maxPeople bounds the names a dashboard remembers.
// ponytail: the names of the people who signed in to this process, forgotten
// when full or on restart; ask the identity provider's directory if teams
// need the names of people who never opened the dashboard.
const maxPeople = 1024

// remember keeps the name of a person who signed in.
func (s *Server) remember(user, name string) {
	if user == "" || name == "" {
		return
	}
	s.peopleMu.Lock()
	defer s.peopleMu.Unlock()
	if s.people == nil || len(s.people) >= maxPeople {
		s.people = map[string]string{}
	}
	s.people[user] = name
}

// named are the names this dashboard knows of users.
func (s *Server) named(users ...string) map[string]string {
	s.peopleMu.Lock()
	defer s.peopleMu.Unlock()
	out := map[string]string{}
	for _, u := range users {
		if n, ok := s.people[u]; ok {
			out[u] = n
		}
	}
	return out
}

// compareVersions orders semantic versions as versions (1.10.0 after 1.9.0).
// A version that does not parse sorts before every one that does, then by
// text, so in a newest-first list it comes last.
func compareVersions(a, b string) int {
	va, ea := semver.NewVersion(a)
	vb, eb := semver.NewVersion(b)
	switch {
	case ea == nil && eb == nil:
		return va.Compare(vb)
	case ea == nil:
		return 1
	case eb == nil:
		return -1
	}
	return cmp.Compare(a, b)
}
