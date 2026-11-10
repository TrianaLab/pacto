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
	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/sync/errgroup"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/logging"
)

// ── reads ──────────────────────────────────────────────────────────────

// Listing is how complete a list of delivery objects is: a namespace the
// caller may not list is left out and named.
type Listing struct {
	delivery.Listing

	// everywhere is set when every namespace was listed at once; else listed
	// names the namespaces read in full. scope is the dashboard's namespace.
	everywhere bool
	listed     []string
	scope      string
}

// read says whether the listing read namespace ns in full, so a Stage of ns
// it does not hold does not exist.
func (l Listing) read(ns string) bool { return l.everywhere || slices.Contains(l.listed, ns) }

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
	l := Listing{Listing: delivery.Listing{AsOf: time.Now().UTC().Format(time.RFC3339), Completeness: "complete"}, scope: s.opts.DeliveryNamespace}
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
			l.everywhere = true
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
			stages, ops, l.listed = append(stages, sts[i]...), append(ops, oss[i]...), append(l.listed, ns)
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
	// Environment narrows the three lists, never byEnvironment: the figure
	// that links to it stays whole.
	Environment string `query:"environment" doc:"Keep only what waits to enter this environment: items on Stages whose spec.environment is this"`
	// Except is the figure's folded station: every environment but the ones
	// it draws, so its counts open a list of exactly what they count.
	Except []string `query:"except,explode" doc:"Leave out what waits to enter these environments (repeat the parameter); an empty value names held changes on Stages the caller cannot read. Like environment, it never narrows byEnvironment"`
	// Service and Domain narrow everything, byEnvironment included.
	Service string `query:"service" doc:"Keep only the Stages of the service with exactly this name and the changes on them; it narrows byEnvironment too, and a change on a Stage the caller cannot read is left out"`
	Domain  string `query:"domain" doc:"With service: the service's registry domain, empty for the default. A Stage that names another domain is left out; one that names none matches any"`
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
		// ByEnvironment is over every page and before environment, so paging
		// or narrowing the lists never changes it.
		ByEnvironment []InboxEnvironment `json:"byEnvironment" doc:"What waits to enter each environment, over every page after q, service and domain (environment and except do not narrow it), in the promotion order the Stages declare (candidates.upstreamStage), then by name; only environments where something waits"`
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
	stages, ops = ofService(stages, ops, in.Service, in.Domain)
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
	byStage := stagesByName(stages)
	b.ByEnvironment = inboxByEnvironment(stages, held, errored, frozenStages)
	if in.Environment != "" {
		held, errored = inEnvironment(held, byStage, in.Environment), inEnvironment(errored, byStage, in.Environment)
		frozenStages = slices.DeleteFunc(frozenStages, func(st delivery.StageObject) bool { return st.Spec.Environment != in.Environment })
	}
	if len(in.Except) > 0 {
		held, errored = outsideEnvironments(held, byStage, in.Except), outsideEnvironments(errored, byStage, in.Except)
		frozenStages = slices.DeleteFunc(frozenStages, func(st delivery.StageObject) bool { return slices.Contains(in.Except, st.Spec.Environment) })
	}
	// Only the page is described and drawn: an artifact read per held item
	// and per frozen Stage is the expensive part.
	b.Held, b.HeldPage = pageOf(held, in.Held, in.Limit, inboxPage)
	for _, it := range held {
		if len(it.Owners) > 1 {
			b.HeldShared++
		}
	}
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
	read := describedAll(ctx, api, nil, on...)
	for i, it := range b.Held {
		if byStage[it.Namespace+"/"+it.Stage] != nil {
			b.Held[i].FromVersion, b.Held[i].ToVersion = read[it.From].Version, read[it.To].Version
		}
	}
	b.Frozen = []EnvironmentRow{}
	for _, st := range page {
		b.Frozen = append(b.Frozen, environmentRowOf(st, nil, caller, read, false))
	}
	narrowRows(ctx, api, b.Frozen)
	b.Errored, b.ErroredPage = pageOf(errored, in.Errored, in.Limit, inboxPage)
	return out, nil
}

// ofService keeps the Stages of service and domain and the operations on
// them; everything when service is empty. An operation on a Stage the caller
// cannot read is left out: Pacto cannot tell whose it is.
func ofService(stages []delivery.StageObject, ops []delivery.OperationObject, service, domain string) ([]delivery.StageObject, []delivery.OperationObject) {
	if service == "" {
		return stages, ops
	}
	stages = filter(stages, func(st delivery.StageObject) bool { return delivery.OfService(st, service, domain) })
	byStage := stagesByName(stages)
	return stages, filter(ops, func(o delivery.OperationObject) bool { return byStage[o.Metadata.Namespace+"/"+o.Spec.Stage] != nil })
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

// EnvironmentRow is one Stage: one service in one environment, as the
// shared row (delivery.StageRow) plus what only the board shows.
type EnvironmentRow struct {
	delivery.StageRow
	// Shadow is set on an observe Stage only: newest first.
	Shadow   []ShadowDecision `json:"shadow,omitempty" doc:"On an observe Stage: what Pacto would have done with each candidate and what GitOps did, newest first. Advisory, not signed"`
	Revision string           `json:"revision,omitempty" doc:"The fleet revision whose contract the running artifact names, when this dashboard's fleet holds it"`
}

// environmentRowOf is one Stage as the board shows it; held counts the held
// operations by namespace/Stage and read says what each digest carries. The
// level and shadow come from ladderOf, so tests can supply a ladder.
func environmentRowOf(st delivery.StageObject, held map[string]int, caller delivery.Caller, read map[string]delivery.Described, withService bool) EnvironmentRow {
	row := EnvironmentRow{StageRow: delivery.RowOf(st, delivery.RowInput{Held: held[st.Metadata.Namespace+"/"+st.Metadata.Name],
		Caller: caller, Read: read, WithService: withService})}
	if lad := ladderOf(st); lad.Level != "" {
		row.Level = lad.Level
		if lad.Level == "observe" {
			row.Shadow = lad.Shadow
		}
	}
	return row
}

// environmentsPage is how many services one page of the board holds.
const environmentsPage = 25

type environmentsInput struct {
	PageQuery
	Environment string   `query:"environment" doc:"Keep only the services with a Stage in this environment, and only those Stages"`
	Tier        string   `query:"tier" enum:"dev,test,prod" doc:"Keep only the services with a Stage in an environment whose name says this tier (inferred from the name, never an order), and only those Stages"`
	State       string   `query:"state" enum:"failed,frozen,autohold,waiting,unverified,empty,verified" doc:"Keep only the services with a Stage in this state (the one counts.byEnvironment counts it in), and only those Stages"`
	Service     string   `query:"service" doc:"Keep only the Stages of the service with exactly this name; with service, each row also offers freeze and undo"`
	Domain      string   `query:"domain" doc:"With service: the service's registry domain, empty for the default. A Stage that names another domain is left out; one that names none matches any"`
	Except      []string `query:"except,explode" doc:"Leave out the Stages in these environments (repeat the parameter), and the services left with none: the figure's folded station lists every environment but the ones it draws"`
}

// envTiers are name prefixes and the tier a name like it is inferred to be.
// A tier is a label inferred from the name, never a promotion order: the
// order comes from the Stages' declared upstreams (SPEC 21).
var envTiers = []struct{ prefix, tier string }{
	{"dev", "dev"}, {"sandbox", "dev"}, {"test", "test"}, {"qa", "test"}, {"stag", "test"},
	{"perf", "test"}, {"uat", "test"}, {"preprod", "test"}, {"prod", "prod"},
}

// envTier is the tier an environment's name says; "" when no prefix matches,
// so it is never guessed.
func envTier(env string) string {
	for _, o := range envTiers {
		if strings.HasPrefix(strings.ToLower(env), o.prefix) {
			return o.tier
		}
	}
	return ""
}

type environmentsOutput struct {
	Body struct {
		Listing
		Environments []EnvironmentRow `json:"environments" doc:"The Stages of the services on this page"`
		// Page counts services, not Stages: a page is whole rows of the board.
		Page Page `json:"page" doc:"Which services this page holds; offset and limit count services"`
		// EnvironmentNames are the same on every page, so paging never moves
		// the environment filter or the count. The board shares no column by
		// them: each row follows its own service's path (SPEC 21 r3).
		EnvironmentNames []string `json:"environmentNames" doc:"Every environment of the services that match q, over every page, in the promotion order the Stages declare (candidates.upstreamStage), then by name"`
		// EnvironmentTiers names the tier of each environment whose name says it.
		EnvironmentTiers map[string]string `json:"environmentTiers" doc:"dev, test or prod for each environment whose name says which, inferred from the name only; an environment it cannot tell is left out"`
		// Counts are over every page, so the page's summary is never one page's.
		Counts StageCounts `json:"counts" doc:"Failed, frozen and waiting Stages over every page, after q, environment, tier and state; byEnvironment before environment, tier and state"`
		// Promotion is the service's own path, only with service.
		Promotion *PromotionGraph `json:"promotion,omitempty" doc:"With service: that service's promotion graph, from its Stages' declared upstreams"`
	}
}

// StageCounts is how many Stages are in each state that needs a person.
type StageCounts struct {
	Failed  int `json:"failed" doc:"Stages whose running digest failed"`
	Frozen  int `json:"frozen"`
	Waiting int `json:"waiting" doc:"Stages with a change waiting for a person"`
	// ByEnvironment is the fleet figure's tally (B2): environment and tier
	// pick from it, so they never narrow it.
	ByEnvironment []EnvironmentCount `json:"byEnvironment" doc:"Every environment of the services that match q and service, over every page, with its Stages counted by state; in the promotion order the Stages declare, then by name"`
}

// keeps is the environment, tier and state filters. They narrow the rows,
// not the columns a person picks from: a service with no Stage there is left
// out.
func (in *environmentsInput) keeps(st delivery.StageObject, held map[string]int) bool {
	return (in.Environment == "" || st.Spec.Environment == in.Environment) && !slices.Contains(in.Except, st.Spec.Environment) &&
		(in.Tier == "" || envTier(st.Spec.Environment) == in.Tier) &&
		(in.State == "" || stageState(st, held) == in.State)
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
	byStage := stagesByName(stages)
	stages = filter(stages, func(st delivery.StageObject) bool {
		return matches(in.Q, st.Spec.Service.Name) && delivery.OfService(st, in.Service, in.Domain)
	})
	out := &environmentsOutput{}
	out.Body.Listing, out.Body.Environments = l, []EnvironmentRow{}
	out.Body.EnvironmentNames, out.Body.EnvironmentTiers = environmentsListed(stages)
	byEnv := countByEnvironment(stages, held)
	if in.Service != "" {
		out.Body.Promotion = promotionGraph(stages, byStage, l)
		out.Body.Promotion.Last = lastPromotion(out.Body.Promotion, ops)
	}
	stages = filter(stages, func(st delivery.StageObject) bool { return in.keeps(st, held) })
	var services []string
	services, out.Body.Counts = countStages(stages, held)
	out.Body.Counts.ByEnvironment = byEnv
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
	read := describedAll(ctx, api, nil, on...)
	for _, st := range shown {
		row := environmentRowOf(st, held, caller, read, in.Service != "")
		row.Upstream = delivery.UpstreamListed(&st, byStage, l.read, l.scope)
		out.Body.Environments = append(out.Body.Environments, row)
	}
	if in.Service != "" {
		undoOffers(ctx, api, shown, out.Body.Environments, caller)
	}
	narrowRows(ctx, api, out.Body.Environments)
	s.revisionLinks(ctx, out.Body.Environments)
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

// environmentsListed is every environment of stages, in the promotion order
// the Stages declare, and the tier each environment's name says.
func environmentsListed(stages []delivery.StageObject) ([]string, map[string]string) {
	tiers := map[string]string{}
	for _, st := range stages {
		if tier := envTier(st.Spec.Environment); tier != "" {
			tiers[st.Spec.Environment] = tier
		}
	}
	envs, _, _, _ := delivery.PromotionOrder(stages)
	return envs, tiers
}

// PublishedVersion is one digest a service's Stages know about.
type PublishedVersion struct {
	Digest   string `json:"digest"`
	Version  string `json:"version,omitempty"`
	Contract string `json:"contract,omitempty" doc:"The digest-pinned contract the artifact names (delivery.pacto.trianalab.io/contract)"`
	// SourceRevision is read with the version, from the same artifact.
	SourceRevision string `json:"sourceRevision,omitempty" doc:"The source revision pacto publish stamped on the artifact (org.opencontainers.image.revision). An annotation, not signed"`
	// Signature follows delivery.SignatureEvidence, the rule the lifecycle
	// answer uses too.
	Signature string              `json:"signature" enum:"signed,unsigned,unknown"`
	Stages    []PublishedOnStages `json:"stages"`
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
	Service string `query:"service" doc:"Keep only the versions of the service with exactly this name; it narrows counts too"`
	Domain  string `query:"domain" doc:"With service: the service's registry domain, empty for the default. A Stage that names another domain is left out; one that names none matches any"`
	Signed  string `query:"signed" enum:"yes,no,unknown" doc:"yes: keep only the services whose every version is signed; no: keep only the services with an unsigned version, and only those versions; unknown: keep only the services with no unsigned version and a version whose signature Pacto cannot tell, and only those versions"`
}

type publishedOutput struct {
	Body struct {
		Listing
		Services []PublishedService `json:"services"`
		Page     Page               `json:"page" doc:"Which services this page holds; offset and limit count services"`
		Counts   PublishedCounts    `json:"counts" doc:"Over every page after q, service and domain, before signed"`
	}
}

// PublishedCounts is how many of the versions the Stages know are signed.
type PublishedCounts struct {
	Services         int      `json:"services" doc:"Services with a version the Stages know; unsignedServices, unknownServices and the rest partition them"`
	Versions         int      `json:"versions" doc:"Distinct digests per service the Stages know"`
	Signed           int      `json:"signed"`
	Unsigned         int      `json:"unsigned" doc:"Versions an entry Stage holds that Flux never verified a signature on: who built them cannot be traced"`
	Unknown          int      `json:"unknown" doc:"Versions whose signature Pacto cannot tell from the Stages the caller lists"`
	UnsignedServices int      `json:"unsignedServices" doc:"Services with at least one unsigned version"`
	UnknownServices  int      `json:"unknownServices" doc:"Services with no unsigned version and at least one version whose signature Pacto cannot tell"`
	FirstUnsigned    []string `json:"firstUnsigned" doc:"Up to 4 of the services with an unsigned version, by name"`
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
	byService, readFrom := publishedDigests(stages, in.Q, in.Service, in.Domain)
	out := &publishedOutput{}
	out.Body.Counts = countPublished(byService)
	for svc, vs := range byService {
		if in.Signed != "" {
			// The figure's three lanes: the service's lane, then only the
			// versions that put it there.
			lane, keep := signatureLane(vs), map[string]string{"yes": delivery.SignatureSigned, "no": delivery.SignatureUnsigned, "unknown": delivery.SignatureUnknown}[in.Signed]
			if lane != keep {
				clear(vs)
			}
			maps.DeleteFunc(vs, func(_ string, v *PublishedVersion) bool { return v.Signature != keep })
		}
		if len(vs) == 0 && in.Signed != "" {
			delete(byService, svc)
		}
	}
	names := slices.Sorted(maps.Keys(byService))
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
		v.Contract, v.Version, v.SourceRevision = revs[i].ContractRef, version(ctx, revs[i]), revs[i].SourceRevision
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

// signatureLane is the figure's lane of a service with versions vs: unsigned
// when one version is, else unknown when one version's signature cannot be
// told, else signed.
func signatureLane(vs map[string]*PublishedVersion) string {
	lane := delivery.SignatureSigned
	for _, v := range vs {
		if v.Signature == delivery.SignatureUnsigned {
			return delivery.SignatureUnsigned
		}
		if v.Signature == delivery.SignatureUnknown {
			lane = delivery.SignatureUnknown
		}
	}
	return lane
}

// countPublished counts the versions of byService by their signature.
func countPublished(byService map[string]map[string]*PublishedVersion) PublishedCounts {
	c := PublishedCounts{FirstUnsigned: []string{}}
	for _, svc := range slices.Sorted(maps.Keys(byService)) {
		vs := byService[svc]
		if len(vs) == 0 {
			continue
		}
		c.Services++
		for _, v := range vs {
			c.Versions++
			switch v.Signature {
			case delivery.SignatureUnsigned:
				c.Unsigned++
			case delivery.SignatureUnknown:
				c.Unknown++
			}
		}
		switch signatureLane(vs) {
		case delivery.SignatureUnsigned:
			c.UnsignedServices++
			if len(c.FirstUnsigned) < 4 {
				c.FirstUnsigned = append(c.FirstUnsigned, svc)
			}
		case delivery.SignatureUnknown:
			c.UnknownServices++
		}
	}
	c.Signed = c.Versions - c.Unsigned - c.Unknown
	return c
}

// publishedDigests groups every digest the Stages of services matching q,
// service and domain remember by service, with the Stage to read each one's
// artifact from.
func publishedDigests(stages []delivery.StageObject, q, service, domain string) (byService map[string]map[string]*PublishedVersion, readFrom map[*PublishedVersion]*delivery.StageObject) {
	byService = map[string]map[string]*PublishedVersion{}
	readFrom = map[*PublishedVersion]*delivery.StageObject{}
	evidence := map[*PublishedVersion]*delivery.SignatureEvidence{}
	for i := range stages {
		st := &stages[i]
		ss := st.Status
		svc := st.Spec.Service.Name
		if !matches(q, svc) || !delivery.OfService(*st, service, domain) {
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
				byService[svc][d], readFrom[v], evidence[v] = v, st, &delivery.SignatureEvidence{}
			}
			evidence[v].Add(*st, d)
			on := PublishedOnStages{Namespace: st.Metadata.Namespace, Stage: st.Metadata.Name, Environment: st.Spec.Environment,
				Running: ss.Pointer == d, Verified: slices.Contains(ss.Verified, d), Failed: slices.Contains(ss.FailedDigests, d)}
			if !slices.Contains(v.Stages, on) {
				v.Stages = append(v.Stages, on)
			}
		}
	}
	for v, e := range evidence {
		v.Signature = e.Signature()
	}
	return byService, readFrom
}

type historyInput struct {
	Namespace     string `query:"namespace" doc:"Namespace of one Stage (with stage); empty lists every Stage the caller can see"`
	Stage         string `query:"stage" doc:"One Stage's name"`
	Service       string `query:"service" doc:"Without a stage: keep only the Stages of the service with exactly this name"`
	Domain        string `query:"domain" doc:"With service: the service's registry domain, empty for the default. A Stage that names another domain is left out; one that names none matches any"`
	Since         string `query:"since" doc:"RFC 3339 time: keep only changes that ran at or after it. A change with no time, or still in flight, is kept"`
	Until         string `query:"until" doc:"RFC 3339 time: keep only changes that started at or before it. A change with no time is kept"`
	Timed         bool   `query:"timed" doc:"With since or until: keep only the changes whose own time (finished, else applied, else since when waiting) is at or after since and before until, as a timeline column counts them. A change with no time, or one that only overlaps the window, is left out"`
	Cause         string `query:"cause" doc:"Keep only changes whose cause (reason, operation or cited verdict) names this text, ignoring case. A line with no record carries an unsigned cause. It narrows the timeline too"`
	CorrelationID string `query:"correlationID" doc:"Keep only changes with exactly this correlation id. It narrows the timeline too"`
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
	// UndoOffers are keyed by the change's operation name.
	UndoOffers map[string]delivery.Offer `json:"undoOffers,omitempty" doc:"For each change on this page that moved the Stage, by its operation name, whether the caller may undo it and why. Offers only describe: GET /api/delivery/undo decides between a rollback and a promote back"`
}

type historyOutput struct {
	Body struct {
		Listing
		Histories []StageHistory `json:"histories"`
		Page      Page           `json:"page" doc:"Which Stages this page holds. With a stage, offset, limit and q page its changes instead (see entriesPage)"`
		// Counts are over every page; without a stage only.
		Counts *HistoryCounts `json:"counts,omitempty" doc:"Without a stage: Stages that wait for a person and frozen Stages, over every page"`
		// Timeline is over every page; without a stage only (one Stage's is on
		// its history).
		Timeline *delivery.HistoryTimeline `json:"timeline,omitempty" doc:"Without a stage: the changes still in the cluster on every Stage that matches, by time and kind, over since..until or the last day. Signed records are read per Stage, so a missing record shows only on one Stage's timeline. It counts the changes left after cause and correlationID"`
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
	// Revisions link a version to its revision page.
	Revisions map[string]string `json:"revisions,omitempty" doc:"The fleet revision key each artifact digest named here belongs to, when this dashboard's fleet holds it"`
	People    map[string]string `json:"people,omitempty" doc:"The names of the people named here who signed in to this dashboard, by username"`
}

func (s *Server) deliveryHistory(ctx context.Context, in *historyInput) (*historyOutput, error) {
	since, until, err := queryWindow(in.Since, in.Until)
	if err != nil {
		return nil, err
	}
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	keep := delivery.EntryFilter{Cause: in.Cause, CorrelationID: in.CorrelationID}
	out := &historyOutput{}
	out.Body.Listing = Listing{Listing: delivery.Listing{AsOf: time.Now().UTC().Format(time.RFC3339), Completeness: "complete"}}
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
			return matches(in.Q, st.Metadata.Namespace+"/"+st.Metadata.Name, st.Spec.Service.Name, st.Spec.Environment) &&
				delivery.OfService(st, in.Service, in.Domain)
		})
		out.Body.Counts = historyCounts(stages, ops)
		out.Body.Timeline = clusterTimeline(stages, ops, since, until, keep)
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
		window := delivery.WindowEntries
		if in.Timed {
			window = delivery.SliceEntries
		}
		h := new(delivery.FilterEntries(window(*hs[i], since, until), keep))
		sh := StageHistory{Namespace: t.Metadata.Namespace, History: *h, ProblemCount: len(h.Problems), UnreadableCount: len(h.Unreadable)}
		entries := slices.Clone(h.Entries)
		slices.Reverse(entries) // newest first: what a reader looks for is the latest change
		if one {
			start, end := delivery.TimelineWindow(since, until, time.Now())
			sh.Timeline = new(delivery.Timeline(entries, start, end))
			entries = filter(entries, func(e delivery.HistoryEntry) bool {
				return matches(in.Q, e.Operation, e.Type, e.Actor, string(e.Outcome), string(e.Phase), string(e.Verification), e.Reason,
					e.CorrelationID, causeReason(e.Cause))
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
	historyUndoOffers(ctx, api, targets, out.Body.Histories, describe)
	read := describedAll(ctx, api, nil, on...)
	versionsIn(out.Body.Versions, read)
	out.Body.Revisions = s.revisionLabels(ctx, read, on...)
	out.Body.People = s.named(actors...)
	return out, nil
}

// causeReason is the reason of c, empty when there is none.
func causeReason(c *delivery.Cause) string {
	if c == nil {
		return ""
	}
	return c.Reason
}

// historyUndoOffers says, for each change on each history's page that moved
// its Stage, whether the caller may undo it (UndoOffer), then narrows the
// offers with one RBAC review per namespace that has one available. It reads
// no history: hs[i] is the page of targets[i]. read false means the Stage
// could not be read, so there is no status to judge an undo against.
func historyUndoOffers(ctx context.Context, api DeliveryAPI, targets []delivery.StageObject, hs []StageHistory, read bool) {
	if !read {
		for i := range hs {
			hs[i].Note += "; undo offers need the Stage, which could not be read"
		}
		return
	}
	caller, err := api.WhoAmI(ctx)
	if err != nil {
		for i := range hs {
			hs[i].Note += "; undo offers need who you are, which could not be read: " + err.Error()
		}
		return
	}
	rows := make([]*delivery.StageRow, len(hs))
	for i := range hs {
		rows[i] = &delivery.StageRow{Namespace: hs[i].Namespace}
		for _, e := range hs[i].Entries {
			if e.Operation != "" && e.Pointer != nil && e.Pointer.After != "" {
				rows[i].Offers = append(rows[i].Offers, delivery.UndoOffer(targets[i].Status, e, caller))
			}
		}
	}
	delivery.NarrowOffers(ctx, rows, delivery.CreateOperations(api.Can))
	for i, r := range rows {
		for _, o := range r.Offers {
			if hs[i].UndoOffers == nil {
				hs[i].UndoOffers = map[string]delivery.Offer{}
			}
			hs[i].UndoOffers[o.Operation] = o
		}
	}
}

// queryWindow reads the since and until query parameters and refuses a window
// that ends before it starts.
func queryWindow(sinceQ, untilQ string) (since, until time.Time, err error) {
	if since, err = queryTime("since", sinceQ); err != nil {
		return since, until, err
	}
	if until, err = queryTime("until", untilQ); err != nil {
		return since, until, err
	}
	if err = delivery.CheckWindow(since, until); err != nil {
		err = huma.Error400BadRequest(err.Error())
	}
	return since, until, err
}

// queryTime reads an optional RFC 3339 query parameter.
func queryTime(name, v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, huma.Error400BadRequest(name + " is not an RFC 3339 time")
	}
	return t, nil
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

// clusterTimeline counts the Operations still in the cluster on stages that
// keep keeps, by time and kind (B7): reading every Stage's signed records for
// a fleet figure would read every record stream on every poll.
func clusterTimeline(stages []delivery.StageObject, ops []delivery.OperationObject, since, until time.Time, keep delivery.EntryFilter) *delivery.HistoryTimeline {
	byStage := stagesByName(stages)
	var entries []delivery.HistoryEntry
	for _, o := range ops {
		if e := delivery.StatusEntry(o); byStage[o.Metadata.Namespace+"/"+o.Spec.Stage] != nil && keep.Keeps(e) {
			entries = append(entries, e)
		}
	}
	start, end := delivery.TimelineWindow(since, until, time.Now())
	return new(delivery.Timeline(entries, start, end))
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

// versionsIn adds to into (a new map when nil) every version read names.
func versionsIn(into map[string]string, read map[string]delivery.Described) map[string]string {
	if into == nil {
		into = map[string]string{}
	}
	for d, r := range read {
		if r.Version != "" {
			into[d] = r.Version
		}
	}
	return into
}

// digestsOn are digests to read from one Stage's artifacts.
type digestsOn struct {
	stage   *delivery.StageObject
	digests []string
}

// describedAll adds to into (a new map when nil) what the digests of many
// Stages carry, all read at once: a page that asked Stage by Stage read two
// artifacts at a time. A digest keeps its entry when its version is empty,
// so a contract named by an artifact whose bundle failed still counts.
func describedAll(ctx context.Context, api DeliveryAPI, into map[string]delivery.Described, on ...digestsOn) map[string]delivery.Described {
	if into == nil {
		into = map[string]delivery.Described{}
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
		r := revs[i]
		desc := delivery.Described{Version: version(ctx, r), Contract: r.ContractRef, Source: r.SourceRevision, Err: r.Err}
		if r.Bundle != nil {
			desc.Team = r.Bundle.Contract.Service.Owner.DisplayString()
		}
		into[d] = desc
	}
	return into
}

// describeParallel is how many reads run at once: a Describe reads a
// registry, so a page of fifty read one after another took fifty round
// trips; the host caches what it read, so a refresh is cheap either way.
const describeParallel = 8

// describeAll describes n digests at once (delivery.DescribeAll); at(i)
// names the Stage to read the i-th from and its digest. out[i] answers at(i).
func describeAll(ctx context.Context, api DeliveryAPI, n int, at func(int) (*delivery.StageObject, string)) []delivery.Revision {
	return delivery.DescribeAll(ctx, n, at, api.Describe)
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

// stageLabels names the version and the fleet revision of the digests of a
// change on a Stage read by name; nothing, with the reason logged, when the
// Stage cannot be read.
// ponytail: a digest that cannot be read is asked again each time it is
// named; the host caches only what it read.
func (s *Server) stageLabels(ctx context.Context, api DeliveryAPI, namespace, stage string, digests ...string) Labels {
	st, err := api.Stage(ctx, namespace, stage)
	if err != nil {
		logging.LoggerFromContext(ctx).Warn("reading a Stage to name versions", "namespace", namespace, "stage", stage, "error", err)
		return Labels{}
	}
	on := digestsOn{st, digests}
	read := describedAll(ctx, api, nil, on)
	return Labels{Versions: versionsIn(nil, read), Revisions: s.revisionLabels(ctx, read, on)}
}

// revisionMatcher finds the fleet revision whose contract digest is a
// digest-pinned contract reference's, of the service named (and of the
// domain, when one is named): "" when none or several match, never a guess.
// It reads only the snapshot as the viewer sees it, so a hidden revision is
// never named; nil when there is no snapshot.
func (s *Server) revisionMatcher(ctx context.Context) func(service, domain, ref string) string {
	q := s.publishedQuery(ctx)
	if q == nil {
		return nil
	}
	byDigest := map[string][]*fleet.ContractRevision{}
	for _, r := range q.SnapshotForSerialization().Revisions {
		if r.Digest != "" {
			byDigest[r.Digest] = append(byDigest[r.Digest], r)
		}
	}
	return func(service, domain, ref string) string {
		var key fleet.RevisionKey
		for _, r := range byDigest[delivery.ContractDigest(ref)] {
			if r.Service == service && (domain == "" || r.Domain == domain) {
				if key != "" {
					return "" // several revisions: never a guess
				}
				key = r.Key
			}
		}
		return string(key)
	}
}

// revisionLinks sets each row's revision: the fleet revision its running
// artifact's contract belongs to.
func (s *Server) revisionLinks(ctx context.Context, rows []EnvironmentRow) {
	if match := s.revisionMatcher(ctx); match != nil {
		for i := range rows {
			rows[i].Revision = match(rows[i].Service, rows[i].Domain, rows[i].ContractRef)
		}
	}
}

// revisionLabels maps each digest named on a Stage to the fleet revision its
// contract belongs to, by the rule of revisionLinks.
func (s *Server) revisionLabels(ctx context.Context, read map[string]delivery.Described, on ...digestsOn) map[string]string {
	match := s.revisionMatcher(ctx)
	if match == nil {
		return nil
	}
	out := map[string]string{}
	for _, o := range on {
		for _, d := range o.digests {
			if k := match(o.stage.Spec.Service.Name, o.stage.Spec.Service.Domain, read[d].Contract); k != "" {
				out[d] = k
			}
		}
	}
	return out
}

// undoOffers adds to each row the offer to undo the change its Stage runs,
// one history read per row, describeParallel at once. rows[i] is shown[i].
func undoOffers(ctx context.Context, api DeliveryAPI, shown []delivery.StageObject, rows []EnvironmentRow, caller delivery.Caller) {
	offers := make([]*delivery.Offer, len(rows))
	var g errgroup.Group
	g.SetLimit(describeParallel)
	for i := range rows {
		g.Go(func() error {
			b, err := api.Blame(ctx, rows[i].Namespace, rows[i].Stage, "")
			offers[i] = delivery.UndoOfferFor(shown[i].Status, b, err, caller)
			return nil
		})
	}
	_ = g.Wait()
	for i, o := range offers {
		if o != nil {
			rows[i].Offers = append(rows[i].Offers, *o)
		}
	}
}

// narrowRows makes unavailable what the caller's RBAC refuses (NarrowOffers).
func narrowRows(ctx context.Context, api DeliveryAPI, rows []EnvironmentRow) {
	ptrs := make([]*delivery.StageRow, len(rows))
	for i := range rows {
		ptrs[i] = &rows[i].StageRow
	}
	delivery.NarrowOffers(ctx, ptrs, delivery.CreateOperations(api.Can))
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
