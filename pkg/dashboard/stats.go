package dashboard

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v4/pkg/dashboard/productview"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// LimitationEnvironmentOrderUnknown says environments are listed by name, or
// some of them are, because no Stage graph the caller can read orders them.
const LimitationEnvironmentOrderUnknown = "ENVIRONMENT_ORDER_UNKNOWN"

// The texts a row carries in place of a link it cannot have.
const (
	noteNotInSnapshot = "not in the current snapshot"
	noteAmbiguous     = "ambiguous: more than one service has this name"
)

const day = 24 * time.Hour

// The default and largest page of service rows and records.
const (
	statsPage    = 25
	maxStatsPage = 100
)

// now is the clock behind the stats window and its buckets.
func (s *Server) now() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now()
}

// registerStatsOperations registers the Metrics endpoints when a stats
// repository is configured.
func (s *Server) registerStatsOperations(api huma.API) {
	if s.opts.Stats == nil {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-stats",
		Method:      http.MethodGet,
		Path:        "/api/stats",
		Summary:     "Pacto outcomes over time",
		Description: "Unsigned statistics from the configured stats repository: the same totals as pacto stats, " +
			"per day or week. Services outside the caller's view are counted in hidden.present and never named.",
		Tags:   []string{"Stats"},
		Errors: []int{http.StatusServiceUnavailable},
	}, s.getStats)
	huma.Register(api, huma.Operation{
		OperationID: "get-stats-records",
		Method:      http.MethodGet,
		Path:        "/api/stats/records",
		Summary:     "The records behind a statistic",
		Description: "The visible run records one number of /api/stats counts, newest first, with links " +
			"to the services, revisions and change analyses they name.",
		Tags:   []string{"Stats"},
		Errors: []int{http.StatusServiceUnavailable},
	}, s.getStatsRecords)
}

type statsInput struct {
	Window         string `query:"window" enum:"7d,30d,90d" default:"30d" doc:"How far back the answer looks: daily buckets for 7d, weekly for 30d and 90d"`
	ServicesOffset int    `query:"servicesOffset" minimum:"0" default:"0" doc:"First service row to return"`
	ServicesLimit  int    `query:"servicesLimit" minimum:"1" maximum:"100" default:"25" doc:"Service rows to return"`
}

type statsRecordsInput struct {
	Window      string `query:"window" enum:"7d,30d,90d" default:"30d" doc:"How far back the answer looks"`
	Metric      string `query:"metric" enum:"breaking,protected,clear,blocked,unverified,held,failed,rolledBack,verificationFailed,leadTime,coverage" required:"true" doc:"The number whose records to list"`
	Environment string `query:"environment" maxLength:"253" doc:"Only the records that count under this environment; coverage ignores it"`
	Offset      int    `query:"offset" minimum:"0" default:"0" doc:"First record to return"`
	Limit       int    `query:"limit" minimum:"1" maximum:"100" default:"25" doc:"Records to return"`
}

// StatsBucket is the numbers of one day or week of the window.
type StatsBucket struct {
	Since    time.Time           `json:"since" doc:"Start of the bucket, inclusive, UTC"`
	Until    time.Time           `json:"until" doc:"End of the bucket, exclusive, UTC"`
	Partial  bool                `json:"partial,omitempty" doc:"True when the bucket is shorter than the others: only the first one can be"`
	Days     int                 `json:"days" doc:"Days the bucket spans"`
	Metrics  *stats.Metrics      `json:"metrics,omitempty" doc:"Numbers of the bucket's records; absent when it holds none, which is no records, never 0"`
	Coverage *runrecord.Coverage `json:"coverage,omitempty" doc:"The newest coverage record in the bucket; absent when it holds none"`
}

// StatsEnvironment is one environment's delivery numbers with its place in
// the promotion order.
type StatsEnvironment struct {
	stats.EnvironmentMetrics
	Upstream  []string `json:"upstream" doc:"Environments whose Stages feed a Stage here, as candidates.upstreamStage declares"`
	Cycle     bool     `json:"cycle,omitempty" doc:"A Stage here is on a cycle of upstream links, so the environment is placed by name"`
	Unordered bool     `json:"unordered,omitempty" doc:"Different services order this environment both ways, so it is placed by name"`
}

// StatsLink is a link to a dashboard page.
type StatsLink struct {
	Label string `json:"label" doc:"What the link names"`
	Href  string `json:"href" doc:"The dashboard route, without the leading #"`
}

// StatsServiceRow is one service's numbers.
type StatsServiceRow struct {
	Label   string        `json:"label" doc:"Service name, as the run records name it"`
	Href    string        `json:"href,omitempty" doc:"The service page; absent when the name resolves to no single service"`
	Note    string        `json:"note,omitempty" doc:"Why there is no link"`
	Metrics stats.Metrics `json:"metrics" doc:"Numbers of the service's records"`
}

// StatsAnswer is the stats envelope with its buckets, ordered environments,
// service rows and current coverage.
type StatsAnswer struct {
	stats.Stats
	Buckets          []StatsBucket       `json:"buckets" doc:"The window per day (7d) or week (30d, 90d), oldest first, aligned back from now"`
	EnvironmentOrder string              `json:"environmentOrder" enum:"stages,name" doc:"stages: environments follow the Stages' upstream links; name: no Stage graph to order by"`
	Environments     []StatsEnvironment  `json:"environments" doc:"Delivery numbers per environment, in environmentOrder"`
	Services         []StatsServiceRow   `json:"services" doc:"One page of per-service numbers"`
	ServicesTotal    int                 `json:"servicesTotal" doc:"Service rows across every page"`
	CoverageNow      *runrecord.Coverage `json:"coverageNow,omitempty" doc:"Coverage of the caller's current fleet snapshot; absent without a fleet"`
}

type statsOutput struct {
	Body StatsAnswer
}

// StatsStage is the Stage an operation record ran on.
type StatsStage struct {
	Namespace   string `json:"namespace" doc:"Stage namespace"`
	Name        string `json:"name" doc:"Stage name"`
	Environment string `json:"environment,omitempty" doc:"The environment the Stage delivers to"`
}

// StatsRecordRow is one record behind a number, with its links.
type StatsRecordRow struct {
	Record      runrecord.Record `json:"record" doc:"The run record"`
	Service     *StatsLink       `json:"service,omitempty" doc:"The service page; absent when the snapshot cannot resolve it"`
	OldRevision *StatsLink       `json:"oldRevision,omitempty" doc:"The old revision page, when the snapshot holds it"`
	NewRevision *StatsLink       `json:"newRevision,omitempty" doc:"The new revision page, when the snapshot holds it"`
	Change      *StatsLink       `json:"change,omitempty" doc:"Change analysis of a gated change"`
	Stage       *StatsStage      `json:"stage,omitempty" doc:"The Stage of an operation record"`
	Note        string           `json:"note,omitempty" doc:"Why a link is missing"`
}

// StatsRecordsAnswer is the records behind one number, with the envelope of
// the answer they come from.
type StatsRecordsAnswer struct {
	AsOf         time.Time          `json:"asOf" doc:"Time the answer was computed, UTC"`
	Window       stats.Window       `json:"window" doc:"Time range the answer covers"`
	Registry     string             `json:"registry,omitempty" doc:"Stats repository read"`
	Provenance   string             `json:"provenance" doc:"Always unsigned statistics: run records are never evidence"`
	RecordsRead  int                `json:"recordsRead" doc:"Records read from the repository"`
	Skipped      stats.Skipped      `json:"skipped" doc:"Records that could not be counted"`
	Truncated    string             `json:"truncated" doc:"The bound that cut the read; empty when the read is whole"`
	Hidden       stats.Hidden       `json:"hidden" doc:"Whether records outside the caller's view exist"`
	Completeness fleet.Completeness `json:"completeness" enum:"complete,partial,empty" doc:"How complete the numbers these records make are"`
	Limitations  []fleet.Limitation `json:"limitations" doc:"Why the answer is incomplete"`
	Total        int                `json:"total" doc:"Records behind the number across every page"`
	Items        []StatsRecordRow   `json:"items" doc:"One page of records, newest first"`
}

type statsRecordsOutput struct {
	Body StatsRecordsAnswer
}

// statsRead is one request's read: the records, the caller's scope and
// fleet view, and the window.
type statsRead struct {
	set    stats.RecordSet
	scope  stats.Scope
	view   *fleet.Query
	window stats.Window
	now    time.Time
}

func (s *Server) readStats(ctx context.Context, window string) (*statsRead, error) {
	now := s.now().UTC()
	w, err := stats.ParseWindow(window, "", now)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	restricted := s.opts.View != nil || s.authSummary(ctx).Mode == "open"
	r := &statsRead{window: w, now: now}
	if r.scope, r.view, err = s.statsScope(ctx, restricted); err != nil {
		return nil, err
	}
	// A restricted viewer reads only the anchors of the services in their
	// view, as pacto stats does, so hidden records never use the bounds or
	// fail the read; with none in view there is nothing to read.
	switch {
	case !restricted:
		r.set, err = s.opts.Stats(ctx, nil)
	case len(r.scope.Names) > 0:
		r.set, err = s.opts.Stats(ctx, r.scope.Names)
	}
	if err != nil {
		msg := "reading run records from " + s.opts.StatsRegistry + " failed"
		if restricted {
			return nil, huma.Error503ServiceUnavailable(msg)
		}
		return nil, huma.Error503ServiceUnavailable(msg, err)
	}
	if s.opts.View != nil && s.opts.OperationAccess != nil {
		visible, err := s.opts.OperationAccess(ctx, stats.OperationNamespaces(r.set))
		if err != nil {
			return nil, Refuse(http.StatusServiceUnavailable, "PermissionCheckFailed", "checking your Kubernetes permissions failed: "+err.Error())
		}
		r.scope.OperationVisible = visible
	}
	return r, nil
}

// statsScope is what the caller may count (Decision 3): an identified local
// or identity-less host sees every record; a signed-in viewer sees the
// services of their fleet view and, once readStats asks, the operation
// records of the namespaces they may list operations in; an open host sees
// its fleet's services and no operation record.
func (s *Server) statsScope(ctx context.Context, restricted bool) (stats.Scope, *fleet.Query, error) {
	var full, view *fleet.Query
	if s.opts.Fleet != nil {
		var err error
		if full, err = s.opts.Fleet(ctx); err == nil {
			view, err = s.view(ctx, full)
		}
		if err != nil {
			return stats.Scope{}, nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
		}
	}
	if !restricted {
		sc := stats.Scope{}
		if full != nil {
			sc.Place = stats.FleetPlacement(full, nil)
		}
		return sc, view, nil
	}
	sc := stats.Scope{Restricted: true}
	if full != nil {
		narrowed := view
		if s.opts.View == nil {
			narrowed = nil
		}
		sc.Place, sc.Names = stats.FleetPlacement(full, narrowed), stats.VisibleNames(view)
	}
	return sc, view, nil
}

func (s *Server) getStats(ctx context.Context, in *statsInput) (*statsOutput, error) {
	r, err := s.readStats(ctx, in.Window)
	if err != nil {
		return nil, err
	}
	st := stats.Aggregate(r.set, stats.Query{Window: r.window, By: "service"}, r.scope, r.now)
	st.Registry = s.opts.StatsRegistry
	out := &statsOutput{}
	links := newStatsLinks(r.view)
	out.Body.ServicesTotal = len(st.Groups)
	out.Body.Services = []StatsServiceRow{}
	for _, g := range fleet.Paginate(st.Groups, in.ServicesLimit, in.ServicesOffset, statsPage, maxStatsPage).Items {
		row := StatsServiceRow{Label: g.Label, Metrics: g.Metrics}
		row.Href, row.Note = links.service(g.Key)
		out.Body.Services = append(out.Body.Services, row)
	}
	st.By, st.Groups = "", nil
	out.Body.Buckets = []StatsBucket{}
	for _, b := range statsBuckets(r.window) {
		bs := stats.Aggregate(r.set, stats.Query{Window: stats.Window{Since: b.Since, Until: b.Until}}, r.scope, r.now)
		b.Metrics, b.Coverage = bs.Totals, bs.Coverage
		out.Body.Buckets = append(out.Body.Buckets, b)
	}
	var envs []stats.EnvironmentMetrics
	if st.Totals != nil {
		envs = st.Totals.Environments
	}
	var why string
	out.Body.Environments, out.Body.EnvironmentOrder, why = s.orderEnvironments(ctx, envs)
	if why != "" {
		st.Limitations = append(st.Limitations, fleet.Limitation{Code: LimitationEnvironmentOrderUnknown, Message: why})
	}
	if r.view != nil {
		c := stats.CoverageOf(r.view)
		out.Body.CoverageNow = &c
	}
	out.Body.Stats = *st
	return out, nil
}

func (s *Server) getStatsRecords(ctx context.Context, in *statsRecordsInput) (*statsRecordsOutput, error) {
	r, err := s.readStats(ctx, in.Window)
	if err != nil {
		return nil, err
	}
	q := stats.Query{Window: r.window}
	recs, err := stats.Behind(r.set, q, r.scope, r.now, in.Metric, in.Environment)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	st := stats.Aggregate(r.set, q, r.scope, r.now)
	out := &statsRecordsOutput{}
	b := &out.Body
	b.AsOf, b.Window, b.Registry, b.Provenance = st.AsOf, st.Window, s.opts.StatsRegistry, st.Provenance
	b.RecordsRead, b.Skipped, b.Truncated, b.Hidden = st.RecordsRead, st.Skipped, st.Truncated, st.Hidden
	b.Completeness, b.Limitations = st.Completeness, st.Limitations
	b.Total, b.Items = len(recs), []StatsRecordRow{}
	links := newStatsLinks(r.view)
	for _, rec := range fleet.Paginate(recs, in.Limit, in.Offset, statsPage, maxStatsPage).Items {
		b.Items = append(b.Items, links.row(rec))
	}
	return out, nil
}

// statsBuckets parts w into days for a 7-day window and weeks otherwise,
// aligned back from until so only the first bucket can be short.
func statsBuckets(w stats.Window) []StatsBucket {
	size := 7 * day
	if w.Until.Sub(w.Since) <= 7*day {
		size = day
	}
	var out []StatsBucket
	for end := w.Until; end.After(w.Since); end = end.Add(-size) {
		start := end.Add(-size)
		if start.Before(w.Since) {
			start = w.Since
		}
		span := end.Sub(start)
		out = append(out, StatsBucket{Since: start, Until: end, Partial: span < size, Days: int((span + day - 1) / day)})
	}
	slices.Reverse(out)
	return out
}

// orderEnvironments puts envs in the promotion order of the Stages the caller
// can read (SPEC 21); what that order does not hold follows by name, and why
// says so.
func (s *Server) orderEnvironments(ctx context.Context, envs []stats.EnvironmentMetrics) (out []StatsEnvironment, mode, why string) {
	out = []StatsEnvironment{}
	if len(envs) == 0 {
		return out, "stages", "" // nothing to order, so no Stage to read
	}
	order, upstream, cycle, unordered, why := s.stageOrder(ctx)
	rank := map[string]int{}
	for i, env := range order {
		rank[env] = i
	}
	held := 0
	for _, e := range envs {
		row := StatsEnvironment{EnvironmentMetrics: e, Upstream: []string{}, Cycle: cycle[e.Environment], Unordered: unordered[e.Environment]}
		if up := upstream[e.Environment]; up != nil {
			row.Upstream = up
		}
		if _, ok := rank[e.Environment]; ok {
			held++
		}
		out = append(out, row)
	}
	// envs arrive in name order, so a stable sort keeps it for those the
	// order does not hold, after those it does.
	slices.SortStableFunc(out, func(a, b StatsEnvironment) int {
		ra, oka := rank[a.Environment]
		rb, okb := rank[b.Environment]
		switch {
		case oka && okb:
			return ra - rb
		case oka:
			return -1
		case okb:
			return 1
		}
		return 0
	})
	switch {
	case why != "":
		return out, "name", why
	case held == 0:
		return out, "name", "no Stage you can read delivers to these environments, so they are listed by name"
	case held < len(envs):
		return out, "stages", "some environments have no Stage you can read, so they follow the others by name"
	}
	return out, "stages", ""
}

// stageOrder is the promotion order of the Stages the caller can read, or
// why there is none. A delivery read failure never fails the answer.
func (s *Server) stageOrder(ctx context.Context) (order []string, upstream map[string][]string, cycle, unordered map[string]bool, why string) {
	if s.opts.Delivery == nil || s.authSummary(ctx).Mode == "open" {
		return nil, nil, nil, nil, "this dashboard reads no delivery Stages, so environments are listed by name"
	}
	api, err := s.deliveryFor(ctx)
	var stages []delivery.StageObject
	if err == nil {
		stages, _, _, err = s.lists(ctx, api, false)
	}
	if err != nil {
		return nil, nil, nil, nil, "the Stages could not be read, so environments are listed by name"
	}
	order, upstream, cycle, unordered = delivery.PromotionOrder(stages)
	return order, upstream, cycle, unordered, ""
}

// statsLinks resolves the names run records carry to dashboard links, from
// indexes built once per request over the caller's view (Decision 8).
type statsLinks struct {
	keys      map[string][]fleet.ServiceKey
	known     map[fleet.ServiceKey]bool
	revisions map[fleet.ServiceKey]map[string]fleet.RevisionKey
}

func newStatsLinks(view *fleet.Query) statsLinks {
	l := statsLinks{keys: map[string][]fleet.ServiceKey{}, known: map[fleet.ServiceKey]bool{}, revisions: map[fleet.ServiceKey]map[string]fleet.RevisionKey{}}
	if view == nil {
		return l
	}
	snap := view.SnapshotForSerialization()
	for key, svc := range snap.Services {
		l.keys[svc.Name] = append(l.keys[svc.Name], key)
		l.known[key] = true
	}
	for key, rev := range snap.Revisions {
		if l.revisions[rev.ServiceKey] == nil {
			l.revisions[rev.ServiceKey] = map[string]fleet.RevisionKey{}
		}
		l.revisions[rev.ServiceKey][rev.Digest] = key
	}
	return l
}

// resolve is the one service a name, or a domain-qualified key, names.
func (l statsLinks) resolve(name string) (fleet.ServiceKey, string) {
	switch keys := l.keys[name]; {
	case len(keys) == 1:
		return keys[0], ""
	case len(keys) > 1:
		return "", noteAmbiguous
	case l.known[fleet.ServiceKey(name)]:
		return fleet.ServiceKey(name), ""
	}
	return "", noteNotInSnapshot
}

// service is the service page of name, or the note that says why not.
func (l statsLinks) service(name string) (href, note string) {
	key, note := l.resolve(name)
	if key == "" {
		return "", note
	}
	return productview.Entity(fleet.KindService, string(key)), ""
}

func (l statsLinks) revision(key fleet.ServiceKey, digest string) *StatsLink {
	rk, ok := l.revisions[key][digest]
	if digest == "" || !ok {
		return nil
	}
	return &StatsLink{Label: digest, Href: productview.Entity(fleet.KindRevision, string(rk))}
}

// row is a record with every link the caller's view resolves.
func (l statsLinks) row(r runrecord.Record) StatsRecordRow {
	row := StatsRecordRow{Record: r}
	name := r.Service
	if r.Kind == runrecord.KindOperation && r.ServiceDomain != "" {
		name = string(fleet.NewServiceKeyDomain(r.ServiceDomain, r.Service))
	}
	if st := r.Stage; st != nil {
		row.Stage = &StatsStage{Namespace: st.Namespace, Name: st.Name, Environment: r.Environment}
	}
	if r.Kind == runrecord.KindCoverage {
		return row
	}
	key, note := l.resolve(name)
	if key == "" {
		row.Note = note
		return row
	}
	row.Service = &StatsLink{Label: r.Service, Href: productview.Entity(fleet.KindService, string(key))}
	if r.Kind != runrecord.KindDiff && r.Kind != runrecord.KindImpact {
		return row
	}
	row.OldRevision, row.NewRevision = l.revision(key, r.OldRevision), l.revision(key, r.NewRevision)
	if (r.NewRevision != "" && row.NewRevision == nil) || (r.OldRevision != "" && row.OldRevision == nil) {
		row.Note = noteNotInSnapshot
	}
	if r.Gate != nil {
		var oldKey, newKey fleet.RevisionKey
		if row.OldRevision != nil {
			oldKey = l.revisions[key][r.OldRevision]
		}
		if row.NewRevision != nil {
			newKey = l.revisions[key][r.NewRevision]
		}
		row.Change = &StatsLink{Label: "change analysis", Href: productview.Changes(key, oldKey, newKey)}
	}
	return row
}
