package dashboard

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"

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

// lists reads every Stage and Operation the caller can see: in the
// dashboard's namespace when it has one; else cluster-wide when allowed, else,
// partially, per namespace of the fleet snapshot. A namespace that refuses is
// dropped and named.
// ponytail: a list per request with the caller's token, polled by the UI;
// add a per-session informer if list load shows on the apiserver.
func (s *Server) lists(ctx context.Context, api DeliveryAPI) ([]delivery.StageObject, []delivery.OperationObject, Listing, error) {
	l := Listing{AsOf: time.Now().UTC().Format(time.RFC3339), Completeness: "complete"}
	namespaces := []string{s.opts.DeliveryNamespace}
	if namespaces[0] == "" {
		stages, err := api.Stages(ctx, "")
		var ops []delivery.OperationObject
		if err == nil {
			if ops, err = api.Operations(ctx, ""); err == nil {
				return stages, ops, l, nil
			}
		}
		if !forbidden(err) {
			return nil, nil, l, err
		}
		// The namespaces asked are a guess (where the fleet's workloads run,
		// not necessarily where Stages live), so the answer is never
		// complete: "*" says namespaces outside the guess were not read.
		l.Completeness, l.Unreadable, namespaces = "partial", []string{"*"}, s.namespaces(ctx)
	}
	return listEach(ctx, api, namespaces, l)
}

// listEach lists each namespace, dropping (and naming) one that refuses.
func listEach(ctx context.Context, api DeliveryAPI, namespaces []string, l Listing) ([]delivery.StageObject, []delivery.OperationObject, Listing, error) {
	var stages []delivery.StageObject
	var ops []delivery.OperationObject
	for _, ns := range namespaces {
		st, err := api.Stages(ctx, ns)
		var os []delivery.OperationObject
		if err == nil {
			os, err = api.Operations(ctx, ns)
		}
		switch {
		case forbidden(err):
			l.Completeness, l.Unreadable = "partial", append(l.Unreadable, ns)
		case err != nil:
			return nil, nil, l, err
		default:
			stages, ops = append(stages, st...), append(ops, os...)
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

type inboxOutput struct {
	Body struct {
		Listing
		Held    []InboxItem      `json:"held" doc:"Held operations whose question names one of the caller's teams; for break-glass also those that name no team"`
		Frozen  []EnvironmentRow `json:"frozen" doc:"Frozen Stages of the caller's services"`
		Errored []InboxItem      `json:"errored" doc:"Errored operations of the caller's services whose Stage is still frozen"`
	}
}

func (s *Server) deliveryInbox(ctx context.Context, _ *struct{}) (*inboxOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := api.WhoAmI(ctx)
	if err != nil {
		return nil, err
	}
	stages, ops, l, err := s.lists(ctx, api)
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
	frozen := map[string]bool{}
	out := &inboxOutput{}
	out.Body.Listing, out.Body.Held, out.Body.Frozen, out.Body.Errored = l, []InboxItem{}, []EnvironmentRow{}, []InboxItem{}
	for _, st := range stages {
		if st.Status.Frozen && mine(st.Spec.Service.Name) {
			frozen[st.Metadata.Namespace+"/"+st.Metadata.Name] = true
			out.Body.Frozen = append(out.Body.Frozen, s.environmentRow(ctx, api, st, nil, caller))
		}
	}
	byStage := stagesByName(stages)
	for _, o := range ops {
		it := inboxItem(o)
		switch {
		// Held waits on whoever the promoter would count. A question that
		// names no team is also break-glass's: under requireOwnerTeam nobody
		// else can resolve it.
		case it.Phase == delivery.PhaseHeld && (answerRefusal(&o, caller, it.Decision) == "" ||
			len(it.Owners) == 0 && caller.Class == delivery.ClassBreakGlass && caller.User != o.Spec.Actor.User):
			out.Body.Held = append(out.Body.Held, withVersions(ctx, api, byStage[it.Namespace+"/"+it.Stage], it))
		case it.Outcome == delivery.Errored && frozen[it.Namespace+"/"+it.Stage]:
			out.Body.Errored = append(out.Body.Errored, it)
		}
	}
	return out, nil
}

// stagesByName indexes Stages by namespace/name.
func stagesByName(stages []delivery.StageObject) map[string]*delivery.StageObject {
	out := map[string]*delivery.StageObject{}
	for i := range stages {
		out[stages[i].Metadata.Namespace+"/"+stages[i].Metadata.Name] = &stages[i]
	}
	return out
}

// withVersions names the versions an item moves between, read from its
// Stage's artifacts; nil when the caller cannot see the Stage.
func withVersions(ctx context.Context, api DeliveryAPI, st *delivery.StageObject, it InboxItem) InboxItem {
	if st != nil {
		v := versions(ctx, api, st, nil, it.From, it.To)
		it.FromVersion, it.ToVersion = v[it.From], v[it.To]
	}
	return it
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
	Actions          []string `json:"actions" doc:"What the caller's class may ask for here: promote, unfreeze"`
}

func (s *Server) environmentRow(ctx context.Context, api DeliveryAPI, st delivery.StageObject, ops []delivery.OperationObject, caller delivery.Caller) EnvironmentRow {
	ss := st.Status
	row := EnvironmentRow{Namespace: st.Metadata.Namespace, Stage: st.Metadata.Name, Service: st.Spec.Service.Name, Environment: st.Spec.Environment,
		Pointer: ss.Pointer, Verified: slices.Contains(ss.Verified, ss.Pointer), Failed: slices.Contains(ss.FailedDigests, ss.Pointer),
		Frozen: ss.Frozen, FrozenReason: ss.FrozenReason, AutoHold: ss.AutoHold, Candidate: ss.Candidate, Actions: []string{}}
	for _, c := range ss.Conditions {
		if c.Type == "Ready" {
			row.Ready = strings.TrimSuffix(c.Status+": "+c.Reason, ": ")
		}
	}
	v := versions(ctx, api, &st, nil, ss.Pointer, ss.Candidate)
	row.Version, row.CandidateVersion = v[ss.Pointer], v[ss.Candidate]
	for _, o := range ops {
		if o.Metadata.Namespace == row.Namespace && o.Spec.Stage == row.Stage && o.Status.Phase == delivery.PhaseHeld {
			row.Held++
		}
	}
	if delivery.MayRaise(delivery.TypePromote, caller.Class, false) && ss.Candidate != "" && ss.Candidate != ss.Pointer {
		row.Actions = append(row.Actions, "promote")
	}
	if (ss.Frozen || ss.AutoHold) && delivery.MayRaise(delivery.TypeUnfreeze, caller.Class, false) {
		row.Actions = append(row.Actions, "unfreeze")
	}
	return row
}

type environmentsOutput struct {
	Body struct {
		Listing
		Environments []EnvironmentRow `json:"environments"`
	}
}

func (s *Server) deliveryEnvironments(ctx context.Context, _ *struct{}) (*environmentsOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := api.WhoAmI(ctx)
	if err != nil {
		return nil, err
	}
	stages, ops, l, err := s.lists(ctx, api)
	if err != nil {
		return nil, err
	}
	out := &environmentsOutput{}
	out.Body.Listing, out.Body.Environments = l, []EnvironmentRow{}
	for _, st := range stages {
		out.Body.Environments = append(out.Body.Environments, s.environmentRow(ctx, api, st, ops, caller))
	}
	slices.SortFunc(out.Body.Environments, func(a, b EnvironmentRow) int {
		return cmp.Or(cmp.Compare(a.Service, b.Service), cmp.Compare(a.Environment, b.Environment), cmp.Compare(a.Namespace, b.Namespace))
	})
	return out, nil
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

type publishedOutput struct {
	Body struct {
		Listing
		Services []PublishedService `json:"services"`
	}
}

// deliveryPublished reads the versions from what the Stages remember:
// ponytail: digests the caller's Stages know (published, offered, verified,
// failed, running), not every bundle tag in the registry; list registry tags
// when someone needs versions no Stage has seen.
func (s *Server) deliveryPublished(ctx context.Context, _ *struct{}) (*publishedOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	stages, _, l, err := s.lists(ctx, api)
	if err != nil {
		return nil, err
	}
	byService := map[string]map[string]*PublishedVersion{}
	for _, st := range stages {
		ss := st.Status
		svc := st.Spec.Service.Name
		if byService[svc] == nil {
			byService[svc] = map[string]*PublishedVersion{}
		}
		for _, d := range slices.Concat(ss.Published, ss.Offered, ss.Verified, ss.FailedDigests, []string{ss.Pointer}) {
			if d == "" {
				continue
			}
			v := byService[svc][d]
			if v == nil {
				r := api.Describe(ctx, &st, d)
				v = &PublishedVersion{Digest: d, Contract: r.ContractRef, Stages: []PublishedOnStages{}}
				v.Version = version(ctx, r)
				byService[svc][d] = v
			}
			v.Signed = v.Signed || slices.Contains(ss.Published, d)
			on := PublishedOnStages{Namespace: st.Metadata.Namespace, Stage: st.Metadata.Name, Environment: st.Spec.Environment,
				Running: ss.Pointer == d, Verified: slices.Contains(ss.Verified, d), Failed: slices.Contains(ss.FailedDigests, d)}
			if !slices.Contains(v.Stages, on) {
				v.Stages = append(v.Stages, on)
			}
		}
	}
	out := &publishedOutput{}
	out.Body.Listing, out.Body.Services = l, []PublishedService{}
	for svc, versions := range byService {
		ps := PublishedService{Service: svc, Versions: []PublishedVersion{}}
		for _, v := range versions {
			ps.Versions = append(ps.Versions, *v)
		}
		slices.SortFunc(ps.Versions, func(a, b PublishedVersion) int {
			return cmp.Or(compareVersions(b.Version, a.Version), cmp.Compare(a.Digest, b.Digest))
		})
		out.Body.Services = append(out.Body.Services, ps)
	}
	slices.SortFunc(out.Body.Services, func(a, b PublishedService) int { return cmp.Compare(a.Service, b.Service) })
	return out, nil
}

type historyInput struct {
	Namespace string `query:"namespace" doc:"Namespace of one Stage (with stage); empty lists every Stage the caller can see"`
	Stage     string `query:"stage" doc:"One Stage's name"`
}

// StageHistory is one Stage's history and the namespace the Stage lives in,
// which the history itself does not carry.
type StageHistory struct {
	Namespace string `json:"namespace"`
	delivery.History
}

type historyOutput struct {
	Body struct {
		Listing
		Histories []StageHistory `json:"histories"`
		Labels
	}
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
	if in.Namespace != "" && in.Stage != "" {
		if err := s.inScope(in.Namespace); err != nil {
			return nil, err
		}
		st, err := api.Stage(ctx, in.Namespace, in.Stage)
		if err != nil {
			st, describe = &delivery.StageObject{Metadata: delivery.ObjectMeta{Namespace: in.Namespace, Name: in.Stage}}, false
		}
		targets = []delivery.StageObject{*st}
	} else {
		stages, _, l, err := s.lists(ctx, api)
		if err != nil {
			return nil, err
		}
		out.Body.Listing, targets = l, stages
	}
	out.Body.Histories = []StageHistory{}
	out.Body.Versions = map[string]string{}
	var actors []string
	for _, t := range targets {
		h, err := api.History(ctx, t.Metadata.Namespace, t.Metadata.Name)
		if err != nil {
			return nil, err
		}
		digests := []string{h.Stage.Pointer}
		for _, e := range h.Entries {
			digests, actors = append(digests, e.From, e.To), append(actors, e.Actor)
		}
		if describe {
			versions(ctx, api, &t, out.Body.Versions, digests...)
		}
		out.Body.Histories = append(out.Body.Histories, StageHistory{Namespace: t.Metadata.Namespace, History: *h})
	}
	out.Body.People = s.named(actors...)
	return out, nil
}

// versions adds to into (a new map when nil) the version each digest's
// contract declares, leaving out what cannot be read.
// ponytail: a digest that cannot be read is asked again each time it is
// named; the host caches only what it read.
func versions(ctx context.Context, api DeliveryAPI, stage *delivery.StageObject, into map[string]string, digests ...string) map[string]string {
	if into == nil {
		into = map[string]string{}
	}
	for _, d := range digests {
		if _, known := into[d]; known || d == "" {
			continue
		}
		if v := version(ctx, api.Describe(ctx, stage, d)); v != "" {
			into[d] = v
		}
	}
	return into
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
