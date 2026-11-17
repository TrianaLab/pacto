package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard/productview"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

var statsNowAt = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func sha(c byte) string { return "sha256:" + strings.Repeat(string(c), 64) }

// statsFleet: orders (revisions a and b) runs in shop, billing in vault.
// With twoOrders a second orders service, in the payments domain, runs in
// vault too.
func statsFleetQuery(t *testing.T, twoOrders bool) *fleet.Query {
	t.Helper()
	rev := func(name, domain string, d byte) fleet.RawRevision {
		c := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: name, Version: fmt.Sprintf("1.0.%d", d), Owner: contract.Owner{Team: "t"}}}
		return fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: sha(d), Domain: domain}
	}
	target := func(ns, name, domain string) fleet.RawTarget {
		return fleet.RawTarget{Scope: ns, Kind: fleet.SourceKindKubernetes, Name: name, Service: name, Domain: domain, Compliance: fleet.StatusCompliant}
	}
	col := &fleet.Collection{
		Revisions: []fleet.RawRevision{rev("orders", "", 'a'), rev("orders", "", 'b'), rev("billing", "", 'c')},
		Targets:   []fleet.RawTarget{target("shop", "orders", ""), target("vault", "billing", "")},
	}
	if twoOrders {
		col.Revisions = append(col.Revisions, rev("orders", "payments", 'd'))
		col.Targets = append(col.Targets, target("vault", "orders", "payments"))
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return statsNowAt.Add(-time.Hour) }},
		fleet.NewMemorySource("k8s", "kubernetes", col))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func ago(h int) time.Time { return statsNowAt.Add(-time.Duration(h) * time.Hour) }

// ciRec is a diff or impact record of a change of service from old to new.
func ciRec(kind, service, old, new string, at time.Time) runrecord.Record {
	return runrecord.Record{Version: runrecord.Version, ID: runrecord.ID(kind, service, old, new), Kind: kind, Service: service,
		OldRevision: old, NewRevision: new, Outcome: runrecord.OutcomePassed, RecordedAt: at}
}

func blockedRec(service, old, new string, at time.Time) runrecord.Record {
	r := ciRec(runrecord.KindImpact, service, old, new, at)
	r.Gate = &runrecord.Gate{Verdict: runrecord.VerdictBlocked}
	r.Consumers = &runrecord.Consumers{Blocking: 2}
	r.Completeness = "complete"
	r.Classification = "BREAKING"
	return r
}

var opN int

// opRec is a succeeded promote of service on Stage ns/stage delivering to env.
func opRec(service, ns, stage, env string, at time.Time) runrecord.Record {
	opN++
	return runrecord.Record{Version: runrecord.Version, ID: fmt.Sprintf("00000000-0000-0000-0000-%012d", opN), Kind: runrecord.KindOperation,
		Service: service, Type: "promote", Outcome: string(delivery.Succeeded), Verification: string(delivery.Verified), RecordedAt: at,
		Environment: env, Stage: &runrecord.StageRef{Namespace: ns, Name: stage, Cluster: "cluster-uid-1"}, FinishedAt: at.Format(time.RFC3339)}
}

func coverageRec(services int, at time.Time) runrecord.Record {
	return runrecord.CoverageRecord(runrecord.Coverage{Services: services, SnapshotAsOf: at, Completeness: "complete"}, "4.6.0", at.Add(time.Minute))
}

func recordSet(recs ...runrecord.Record) stats.RecordSet {
	var s stats.RecordSet
	for i, r := range recs {
		s.Entries = append(s.Entries, stats.Entry{Record: r, Digest: fmt.Sprintf("sha256:%064d", i), Anchor: runrecord.AnchorTag(r.Service)})
	}
	return s
}

// standardSet: a blocked breaking change of orders (a to b), a diff of
// billing, a promote of orders in shop, a promote of billing in vault and a
// coverage record.
func standardSet() stats.RecordSet {
	return recordSet(
		blockedRec("orders", sha('a'), sha('b'), ago(30)),
		ciRec(runrecord.KindDiff, "billing", sha('c'), sha('e'), ago(20)),
		opRec("orders", "shop", "prod", "production", ago(10)),
		opRec("billing", "vault", "books", "books-env", ago(9)),
		coverageRec(7, ago(5)),
	)
}

func fixedSet(set stats.RecordSet) func(context.Context, []string) (stats.RecordSet, error) {
	return func(context.Context, []string) (stats.RecordSet, error) { return set, nil }
}

func fleetOf(q *fleet.Query) func(context.Context) (*fleet.Query, error) {
	return func(context.Context) (*fleet.Query, error) { return q, nil }
}

func shopView(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
	return q.Restrict(func(ns string) bool { return ns == "shop" }), nil
}

func statsServer(t *testing.T, opts Options) *httptest.Server {
	t.Helper()
	if opts.Now == nil {
		opts.Now = func() time.Time { return statsNowAt }
	}
	srv := httptest.NewServer(NewServer(opts).APIHandler())
	t.Cleanup(srv.Close)
	return srv
}

// fetch GETs path and returns the status and the raw body.
func fetch(t *testing.T, srv *httptest.Server, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

type statsBody struct {
	stats.Stats
	Buckets          []StatsBucket        `json:"buckets"`
	EnvironmentOrder string               `json:"environmentOrder"`
	Environments     []StatsEnvironment   `json:"environments"`
	Services         []StatsServiceRow    `json:"services"`
	ServicesTotal    int                  `json:"servicesTotal"`
	CoverageNow      *runrecord.Coverage  `json:"coverageNow"`
	EnvironmentsBy   StatsEnvironmentsBy  `json:"environmentsBy"`
	LeadTimeGroups   []StatsLeadTimeGroup `json:"leadTimeGroups"`
}

func getStatsBody(t *testing.T, srv *httptest.Server, query string) (statsBody, string) {
	t.Helper()
	code, raw := fetch(t, srv, "/api/stats"+query)
	if code != http.StatusOK {
		t.Fatalf("GET /api/stats%s = %d: %s", query, code, raw)
	}
	var b statsBody
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatal(err)
	}
	return b, raw
}

type recordsBody struct {
	Provenance   string             `json:"provenance"`
	Completeness string             `json:"completeness"`
	Hidden       stats.Hidden       `json:"hidden"`
	Limitations  []fleet.Limitation `json:"limitations"`
	Total        int                `json:"total"`
	Items        []StatsRecordRow   `json:"items"`
}

func getRecords(t *testing.T, srv *httptest.Server, query string) (recordsBody, string) {
	t.Helper()
	code, raw := fetch(t, srv, "/api/stats/records"+query)
	if code != http.StatusOK {
		t.Fatalf("GET /api/stats/records%s = %d: %s", query, code, raw)
	}
	var b recordsBody
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatal(err)
	}
	return b, raw
}

func hasLimitation(lims []fleet.Limitation, code string) bool {
	for _, l := range lims {
		if l.Code == code {
			return true
		}
	}
	return false
}

func assertNotNamed(t *testing.T, raw string, words ...string) {
	t.Helper()
	for _, w := range words {
		if strings.Contains(raw, w) {
			t.Errorf("the answer names %q: %s", w, raw)
		}
	}
}

func capabilityStats(t *testing.T, opts Options) bool {
	t.Helper()
	code, raw := fetch(t, statsServer(t, opts), "/api/capabilities")
	if code != http.StatusOK {
		t.Fatalf("capabilities = %d", code)
	}
	var c struct {
		Stats bool `json:"stats"`
	}
	_ = json.Unmarshal([]byte(raw), &c)
	return c.Stats
}

func TestStatsNoRegistryNotServed(t *testing.T) {
	srv := statsServer(t, Options{})
	for _, p := range []string{"/api/stats", "/api/stats/records?metric=breaking"} {
		if code, _ := fetch(t, srv, p); code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", p, code)
		}
	}
	if capabilityStats(t, Options{}) {
		t.Error("capabilities.stats is true with no stats repository")
	}
}

func TestStatsCapabilityTrue(t *testing.T) {
	if !capabilityStats(t, Options{Stats: fixedSet(stats.RecordSet{})}) {
		t.Error("capabilities.stats is false with a stats repository")
	}
}

func TestStatsEnvelopeCarriesProvenance(t *testing.T) {
	srv := statsServer(t, Options{Stats: fixedSet(standardSet()), StatsRegistry: "ghcr.io/acme/stats"})
	b, _ := getStatsBody(t, srv, "")
	if b.Provenance != stats.Provenance || !b.AsOf.Equal(statsNowAt) || b.Registry != "ghcr.io/acme/stats" || b.RecordsRead != 5 {
		t.Fatalf("envelope = %+v", b.Stats)
	}
	if !b.Window.Until.Equal(statsNowAt) || !b.Window.Since.Equal(statsNowAt.Add(-30*day)) || b.Limitations == nil || b.Completeness == "" {
		t.Fatalf("window %+v, limitations %v, completeness %q", b.Window, b.Limitations, b.Completeness)
	}
	r, _ := getRecords(t, srv, "?metric=breaking")
	if r.Provenance != stats.Provenance || r.Completeness == "" || r.Limitations == nil {
		t.Fatalf("records envelope = %+v", r)
	}
}

func TestStatsWindowEnum(t *testing.T) {
	srv := statsServer(t, Options{Stats: fixedSet(standardSet())})
	for _, p := range []string{"/api/stats?window=45d", "/api/stats/records?metric=breaking&window=45d", "/api/stats/records?metric=nope", "/api/stats/records"} {
		if code, _ := fetch(t, srv, p); code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", p, code)
		}
	}
}

func TestStatsBucketsDailyForSevenDays(t *testing.T) {
	b, _ := getStatsBody(t, statsServer(t, Options{Stats: fixedSet(standardSet())}), "?window=7d")
	if len(b.Buckets) != 7 {
		t.Fatalf("buckets = %d, want 7", len(b.Buckets))
	}
	for _, bk := range b.Buckets {
		if bk.Partial || bk.Days != 1 || bk.Until.Sub(bk.Since) != day {
			t.Fatalf("bucket %+v is not one full day", bk)
		}
	}
	if !b.Buckets[6].Until.Equal(statsNowAt) {
		t.Fatalf("last bucket ends %v, want now", b.Buckets[6].Until)
	}
}

func TestStatsBucketsWeeklyAlignedToNow(t *testing.T) {
	b, _ := getStatsBody(t, statsServer(t, Options{Stats: fixedSet(standardSet())}), "?window=30d")
	if len(b.Buckets) != 5 {
		t.Fatalf("buckets = %d, want 5", len(b.Buckets))
	}
	first, last := b.Buckets[0], b.Buckets[4]
	if !first.Partial || first.Days != 2 || !first.Since.Equal(statsNowAt.Add(-30*day)) {
		t.Fatalf("first bucket = %+v, want a partial 2-day bucket at the window start", first)
	}
	if last.Partial || last.Days != 7 || !last.Until.Equal(statsNowAt) {
		t.Fatalf("last bucket = %+v, want a full week ending now", last)
	}
	if got := len(statsBuckets(stats.Window{Since: statsNowAt.Add(-90 * day), Until: statsNowAt})); got != 13 {
		t.Fatalf("90d buckets = %d, want 13", got)
	}
}

func TestStatsBucketsEmptyBucketHasNoMetrics(t *testing.T) {
	b, _ := getStatsBody(t, statsServer(t, Options{Stats: fixedSet(standardSet())}), "?window=7d")
	var empty, full int
	for _, bk := range b.Buckets {
		if bk.Metrics == nil {
			empty++
		} else {
			full++
		}
	}
	if empty == 0 || full == 0 {
		t.Fatalf("empty %d, full %d: a bucket with no records must carry no metrics", empty, full)
	}
	if last := b.Buckets[6]; last.Metrics == nil || last.Coverage == nil || last.Coverage.Services != 7 {
		t.Fatalf("last bucket = %+v", last)
	}
}

func TestStatsReadErrorIs503(t *testing.T) {
	failing := func(context.Context, []string) (stats.RecordSet, error) {
		return stats.RecordSet{}, errors.New("dial tcp: secret.internal")
	}
	srv := statsServer(t, Options{Stats: failing, StatsRegistry: "ghcr.io/acme/stats"})
	code, raw := fetch(t, srv, "/api/stats")
	if code != http.StatusServiceUnavailable || !strings.Contains(raw, "ghcr.io/acme/stats") || !strings.Contains(raw, "secret.internal") {
		t.Fatalf("local read failure = %d %s", code, raw)
	}
	q := statsFleetQuery(t, false)
	srv = statsServer(t, Options{Stats: failing, StatsRegistry: "ghcr.io/acme/stats", Fleet: fleetOf(q), View: shopView})
	code, raw = fetch(t, srv, "/api/stats/records?metric=breaking")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("restricted read failure = %d", code)
	}
	assertNotNamed(t, raw, "secret.internal")
}

func TestStatsFleetErrorIs503(t *testing.T) {
	broken := func(context.Context) (*fleet.Query, error) { return nil, errors.New("no snapshot") }
	srv := statsServer(t, Options{Stats: fixedSet(standardSet()), Fleet: broken})
	if code, _ := fetch(t, srv, "/api/stats"); code != http.StatusServiceUnavailable {
		t.Fatalf("fleet failure = %d, want 503", code)
	}
}

func restrictedOpts(t *testing.T, access func(context.Context, []string) (func(string) bool, error)) Options {
	return Options{Stats: fixedSet(standardSet()), Fleet: fleetOf(statsFleetQuery(t, false)), View: shopView, OperationAccess: access}
}

func shopAccess(_ context.Context, nss []string) (func(string) bool, error) {
	return func(ns string) bool { return ns == "shop" }, nil
}

func TestStatsRestrictedHiddenNeverNamed(t *testing.T) {
	srv := statsServer(t, restrictedOpts(t, shopAccess))
	b, raw := getStatsBody(t, srv, "")
	if !b.Hidden.Present || b.Completeness != fleet.CompletenessPartial || !hasLimitation(b.Limitations, stats.LimitationOutsideView) {
		t.Fatalf("hidden %+v, completeness %q, limitations %v", b.Hidden, b.Completeness, b.Limitations)
	}
	assertNotNamed(t, raw, "billing", "vault", "books")
	if len(b.Services) != 1 || b.Services[0].Label != "orders" || b.ServicesTotal != 1 {
		t.Fatalf("services = %+v", b.Services)
	}
	var hidden map[string]any
	_ = json.Unmarshal([]byte(raw), &struct {
		Hidden *map[string]any `json:"hidden"`
	}{&hidden})
	if len(hidden) != 1 {
		t.Fatalf("hidden carries more than present: %v", hidden)
	}
	if len(b.Environments) != 1 || b.Environments[0].Environment != "production" {
		t.Fatalf("environments = %+v", b.Environments)
	}
}

func TestStatsRestrictedNoOperationAccessHidesOperations(t *testing.T) {
	b, raw := getStatsBody(t, statsServer(t, restrictedOpts(t, nil)), "")
	if len(b.Environments) != 0 || !b.Hidden.Present {
		t.Fatalf("environments %+v, hidden %+v: no operation access must hide every operation record", b.Environments, b.Hidden)
	}
	assertNotNamed(t, raw, "production", "cluster-uid-1")
}

func TestStatsRestrictedHidesCoverageHistory(t *testing.T) {
	b, _ := getStatsBody(t, statsServer(t, restrictedOpts(t, shopAccess)), "")
	if b.Coverage != nil {
		t.Fatalf("a restricted viewer sees coverage history: %+v", b.Coverage)
	}
	for _, bk := range b.Buckets {
		if bk.Coverage != nil {
			t.Fatalf("a restricted bucket carries coverage: %+v", bk)
		}
	}
	if !slices.ContainsFunc(b.Limitations, func(l fleet.Limitation) bool { return l.Code == stats.LimitationCoverageOutsideView }) {
		t.Fatalf("withheld coverage is not said: %+v", b.Limitations)
	}
	restricted, _ := shopView(context.Background(), statsFleetQuery(t, false))
	want := stats.CoverageOf(restricted)
	if b.CoverageNow == nil || !reflect.DeepEqual(*b.CoverageNow, want) || want.Services != 1 {
		t.Fatalf("coverageNow = %+v, want the restricted snapshot's %+v", b.CoverageNow, want)
	}
}

func TestStatsOpenModeHidesOperationRecords(t *testing.T) {
	q := statsFleetQuery(t, false)
	srv := statsServer(t, Options{Stats: fixedSet(standardSet()), Fleet: fleetOf(q), Auth: &Auth{}, OperationAccess: shopAccess})
	b, raw := getStatsBody(t, srv, "")
	if len(b.Environments) != 0 || !b.Hidden.Present || b.Coverage != nil {
		t.Fatalf("open host: environments %+v, hidden %+v, coverage %+v", b.Environments, b.Hidden, b.Coverage)
	}
	assertNotNamed(t, raw, "production", "books", "cluster-uid-1", "\"prod\"")
	if b.ServicesTotal != 2 {
		t.Fatalf("open host services = %d, want the 2 its fleet places", b.ServicesTotal)
	}
}

func TestStatsOpenModeHidesUnplacedServices(t *testing.T) {
	set := recordSet(ciRec(runrecord.KindDiff, "orders", sha('a'), sha('b'), ago(3)), ciRec(runrecord.KindDiff, "phantom", sha('a'), sha('b'), ago(3)))
	srv := statsServer(t, Options{Stats: fixedSet(set), Fleet: fleetOf(statsFleetQuery(t, false)), Auth: &Auth{}})
	b, raw := getStatsBody(t, srv, "")
	if !b.Hidden.Present || b.Runs != 1 || b.ServicesTotal != 1 {
		t.Fatalf("hidden %+v, runs %d, services %d", b.Hidden, b.Runs, b.ServicesTotal)
	}
	assertNotNamed(t, raw, "phantom")
}

func TestStatsLocalModeUnrestricted(t *testing.T) {
	srv := statsServer(t, Options{Stats: fixedSet(standardSet()), Fleet: fleetOf(statsFleetQuery(t, false)), Auth: &Auth{Local: true}})
	b, _ := getStatsBody(t, srv, "")
	if b.Hidden.Present || len(b.Environments) != 2 || b.Coverage == nil || b.ServicesTotal != 2 {
		t.Fatalf("local host: hidden %+v, environments %d, coverage %+v, services %d", b.Hidden, len(b.Environments), b.Coverage, b.ServicesTotal)
	}
}

func TestStatsOperationCheckFailureIs503(t *testing.T) {
	failing := func(context.Context, []string) (func(string) bool, error) { return nil, errors.New("apiserver down") }
	srv := statsServer(t, restrictedOpts(t, failing))
	code, raw := fetch(t, srv, "/api/stats")
	if code != http.StatusServiceUnavailable || !strings.Contains(raw, "PermissionCheckFailed") || !strings.Contains(raw, "checking your Kubernetes permissions failed") {
		t.Fatalf("permission failure = %d %s", code, raw)
	}
}

func TestStatsOperationAccessAsksOperationNamespaces(t *testing.T) {
	var asked []string
	access := func(_ context.Context, nss []string) (func(string) bool, error) {
		asked = nss
		return shopAccess(context.Background(), nss)
	}
	getStatsBody(t, statsServer(t, restrictedOpts(t, access)), "")
	if !reflect.DeepEqual(asked, []string{"shop", "vault"}) {
		t.Fatalf("asked = %v", asked)
	}
}

func TestStatsUnrestrictedUnknownServiceGroupsAsUnknown(t *testing.T) {
	set := recordSet(ciRec(runrecord.KindDiff, "phantom", sha('a'), sha('b'), ago(3)))
	b, _ := getStatsBody(t, statsServer(t, Options{Stats: fixedSet(set), Fleet: fleetOf(statsFleetQuery(t, false))}), "")
	if b.Hidden.Present || b.Runs != 1 || len(b.Services) != 1 {
		t.Fatalf("hidden %+v, runs %d, services %+v", b.Hidden, b.Runs, b.Services)
	}
	if row := b.Services[0]; row.Label != "phantom" || row.Href != "" || row.Note != noteNotInSnapshot {
		t.Fatalf("unplaced service row = %+v", row)
	}
	// Without any fleet, every service is unplaced and counted.
	b, _ = getStatsBody(t, statsServer(t, Options{Stats: fixedSet(set)}), "")
	if b.Runs != 1 || b.CoverageNow != nil || b.Services[0].Note != noteNotInSnapshot {
		t.Fatalf("fleet-less host = %+v", b)
	}
}

// With no service in view there is nothing to read, and the answer says so
// rather than that recording is off.
func TestStatsRestrictedWithoutFleetHidesEverything(t *testing.T) {
	read := func(context.Context, []string) (stats.RecordSet, error) { panic("read with nothing in view") }
	b, _ := getStatsBody(t, statsServer(t, Options{Stats: read, Auth: &Auth{}}), "")
	if b.Runs != 0 || b.Completeness != fleet.CompletenessPartial || !hasLimitation(b.Limitations, stats.LimitationOutsideView) {
		t.Fatalf("runs %d, completeness %s, limitations %+v", b.Runs, b.Completeness, b.Limitations)
	}
}

// A restricted viewer reads only the anchors of their view, as pacto stats
// does: hidden records neither use the bounds nor fail the read.
func TestStatsRestrictedReadsOnlyViewAnchors(t *testing.T) {
	var asked [][]string
	read := func(_ context.Context, services []string) (stats.RecordSet, error) {
		asked = append(asked, services)
		if services == nil {
			return stats.RecordSet{}, errors.New("a hidden anchor answered 500")
		}
		return recordSet(blockedRec("orders", sha('a'), sha('b'), ago(30))), nil
	}
	opts := restrictedOpts(t, shopAccess)
	opts.Stats = read
	b, _ := getStatsBody(t, statsServer(t, opts), "")
	if b.Runs != 1 || b.Truncated != "" || b.Totals == nil || b.Totals.Gates.Blocked != 1 {
		t.Fatalf("stats = %+v", b.Stats)
	}
	for _, a := range asked {
		if !reflect.DeepEqual(a, []string{"orders"}) {
			t.Fatalf("read %v, want only the view's anchors", a)
		}
	}
}

// stagesDelivery serves stages, or err, and nothing else.
type stagesDelivery struct {
	DeliveryAPI
	stages []delivery.StageObject
	err    error
}

func (d stagesDelivery) Stages(context.Context, string) ([]delivery.StageObject, error) {
	return d.stages, d.err
}

func stageTo(name, env, upstream string) delivery.StageObject {
	st := delivery.StageObject{Metadata: delivery.ObjectMeta{Namespace: "shop", Name: name}, Spec: delivery.StageSpec{Environment: env}}
	if upstream != "" {
		st.Spec.Candidates.UpstreamStage = &delivery.StageRef{Name: upstream}
	}
	return st
}

func envOrderSet() stats.RecordSet {
	return recordSet(
		opRec("orders", "shop", "us", "prod-us", ago(5)),
		opRec("orders", "shop", "eu", "prod-eu", ago(5)),
		opRec("orders", "shop", "staging", "staging", ago(5)),
		opRec("orders", "shop", "dev", "dev", ago(5)),
		opRec("orders", "shop", "lab", "lab", ago(5)),
	)
}

func envNames(envs []StatsEnvironment) string {
	var out []string
	for _, e := range envs {
		out = append(out, e.Environment)
	}
	return strings.Join(out, ",")
}

func deliveryOpts(api DeliveryAPI) Options {
	return Options{Stats: fixedSet(envOrderSet()), Delivery: func(*http.Request) (DeliveryAPI, error) { return api, nil }}
}

func TestStatsEnvironmentsFollowStageOrder(t *testing.T) {
	api := stagesDelivery{stages: []delivery.StageObject{
		stageTo("dev", "dev", ""), stageTo("staging", "staging", "dev"), stageTo("eu", "prod-eu", "staging"), stageTo("us", "prod-us", "staging"),
	}}
	b, _ := getStatsBody(t, statsServer(t, deliveryOpts(api)), "")
	if got := envNames(b.Environments); got != "dev,staging,prod-eu,prod-us,lab" || b.EnvironmentOrder != "stages" {
		t.Fatalf("order = %s (%s)", got, b.EnvironmentOrder)
	}
	if up := b.Environments[2].Upstream; !reflect.DeepEqual(up, []string{"dev", "staging"}) {
		t.Fatalf("prod-eu upstream = %v", up)
	}
	if !hasLimitation(b.Limitations, LimitationEnvironmentOrderUnknown) {
		t.Fatal("lab has no Stage, yet no limitation says it follows by name")
	}
	// Every environment held: no limitation.
	api.stages = append(api.stages, stageTo("lab", "lab", ""))
	b, _ = getStatsBody(t, statsServer(t, deliveryOpts(api)), "")
	if hasLimitation(b.Limitations, LimitationEnvironmentOrderUnknown) || b.EnvironmentOrder != "stages" {
		t.Fatalf("fully ordered answer: %s, %v", b.EnvironmentOrder, b.Limitations)
	}
}

func TestStatsEnvironmentsByNameWithoutDelivery(t *testing.T) {
	b, _ := getStatsBody(t, statsServer(t, Options{Stats: fixedSet(envOrderSet())}), "")
	if got := envNames(b.Environments); got != "dev,lab,prod-eu,prod-us,staging" || b.EnvironmentOrder != "name" ||
		!hasLimitation(b.Limitations, LimitationEnvironmentOrderUnknown) {
		t.Fatalf("order = %s (%s), limitations %v", got, b.EnvironmentOrder, b.Limitations)
	}
	// Stages that name none of these environments order nothing.
	b, _ = getStatsBody(t, statsServer(t, deliveryOpts(stagesDelivery{stages: []delivery.StageObject{stageTo("x", "other", "")}})), "")
	if b.EnvironmentOrder != "name" {
		t.Fatalf("unrelated Stages order = %s", b.EnvironmentOrder)
	}
	// No records: nothing to order.
	b, _ = getStatsBody(t, statsServer(t, Options{Stats: fixedSet(stats.RecordSet{}), Delivery: deliveryOpts(stagesDelivery{}).Delivery}), "")
	if b.EnvironmentOrder != "stages" || len(b.Environments) != 0 {
		t.Fatalf("empty answer = %s %v", b.EnvironmentOrder, b.Environments)
	}
	// Nor without delivery: no operation record, no limitation about their order.
	b, _ = getStatsBody(t, statsServer(t, Options{Stats: fixedSet(recordSet(ciRec(runrecord.KindDiff, "orders", sha('a'), sha('b'), ago(3))))}), "")
	if b.EnvironmentOrder != "stages" || hasLimitation(b.Limitations, LimitationEnvironmentOrderUnknown) {
		t.Fatalf("no operations, no delivery = %s %v", b.EnvironmentOrder, b.Limitations)
	}
}

func TestStatsDeliveryReadFailureFallsBackToName(t *testing.T) {
	b, raw := getStatsBody(t, statsServer(t, deliveryOpts(stagesDelivery{err: errors.New("apiserver: secret detail")})), "")
	if b.EnvironmentOrder != "name" || envNames(b.Environments) != "dev,lab,prod-eu,prod-us,staging" {
		t.Fatalf("order = %s (%s)", envNames(b.Environments), b.EnvironmentOrder)
	}
	assertNotNamed(t, raw, "secret detail")
	opts := deliveryOpts(nil)
	opts.Delivery = func(*http.Request) (DeliveryAPI, error) { return nil, errors.New("no identity") }
	if b, _ = getStatsBody(t, statsServer(t, opts), ""); b.EnvironmentOrder != "name" {
		t.Fatalf("delivery refusal order = %s", b.EnvironmentOrder)
	}
}

func revisionKey(t *testing.T, q *fleet.Query, digest string) fleet.RevisionKey {
	t.Helper()
	for k, r := range q.SnapshotForSerialization().Revisions {
		if r.Digest == digest {
			return k
		}
	}
	t.Fatalf("no revision %s", digest)
	return ""
}

func TestStatsLinksResolveServiceRevisionChange(t *testing.T) {
	q := statsFleetQuery(t, false)
	srv := statsServer(t, Options{Stats: fixedSet(standardSet()), Fleet: fleetOf(q)})
	b, _ := getStatsBody(t, srv, "")
	if b.Services[1].Label != "orders" || b.Services[1].Href != productview.Entity(fleet.KindService, "orders") {
		t.Fatalf("services = %+v", b.Services)
	}
	r, _ := getRecords(t, srv, "?metric=blocked")
	if r.Total != 1 {
		t.Fatalf("blocked records = %+v", r)
	}
	row := r.Items[0]
	oldKey, newKey := revisionKey(t, q, sha('a')), revisionKey(t, q, sha('b'))
	if row.Service == nil || row.Service.Href != productview.Entity(fleet.KindService, "orders") ||
		row.OldRevision == nil || row.OldRevision.Href != productview.Entity(fleet.KindRevision, string(oldKey)) ||
		row.NewRevision == nil || row.NewRevision.Href != productview.Entity(fleet.KindRevision, string(newKey)) || row.Note != "" {
		t.Fatalf("row = %+v", row)
	}
	if row.Change == nil || row.Change.Href != productview.Changes("orders", oldKey, newKey) {
		t.Fatalf("change = %+v", row.Change)
	}
}

func unresolvedSet() stats.RecordSet {
	return recordSet(ciRec(runrecord.KindDiff, "phantom", sha('a'), sha('b'), ago(3)), blockedRec("orders", sha('a'), sha('f'), ago(2)),
		opRec("orders", "shop", "prod", "production", ago(1)), coverageRec(3, ago(1)))
}

func TestStatsLinksUnresolvedAreText(t *testing.T) {
	srv := statsServer(t, Options{Stats: fixedSet(unresolvedSet()), Fleet: fleetOf(statsFleetQuery(t, false))})
	r, _ := getRecords(t, srv, "?metric=blocked")
	row := r.Items[0]
	if row.NewRevision != nil || row.OldRevision == nil || row.Note != noteNotInSnapshot || row.Change == nil || strings.Contains(row.Change.Href, "new=") {
		t.Fatalf("unknown new revision row = %+v", row)
	}
	r, _ = getRecords(t, srv, "?metric=held")
	if r.Total != 0 {
		t.Fatalf("held = %+v", r)
	}
	r, _ = getRecords(t, srv, "?metric=leadTime")
	if r.Total != 0 {
		t.Fatalf("leadTime (no publish) = %+v", r)
	}
	r, _ = getRecords(t, srv, "?metric=coverage")
	if r.Total != 1 || r.Items[0].Service != nil || r.Items[0].Note != "" {
		t.Fatalf("coverage row = %+v", r.Items)
	}
	b, _ := getStatsBody(t, srv, "")
	if b.Services[1].Label != "phantom" || b.Services[1].Href != "" || b.Services[1].Note != noteNotInSnapshot {
		t.Fatalf("services = %+v", b.Services)
	}
}

// A phantom diff and an operation link nothing they cannot resolve.
func TestStatsLinksRowsLinkOnlyWhatResolves(t *testing.T) {
	set := unresolvedSet()
	links := newStatsLinks(statsFleetQuery(t, false))
	if row := links.row(set.Entries[0].Record); row.Service != nil || row.Note != noteNotInSnapshot {
		t.Fatalf("phantom row = %+v", row)
	}
	if row := links.row(set.Entries[2].Record); row.Service == nil || row.Stage == nil || row.Stage.Environment != "production" || row.NewRevision != nil {
		t.Fatalf("operation row = %+v", row)
	}
}

func TestStatsLinksAmbiguousServiceIsText(t *testing.T) {
	q := statsFleetQuery(t, true)
	srv := statsServer(t, Options{Stats: fixedSet(recordSet(blockedRec("orders", sha('a'), sha('b'), ago(2)))), Fleet: fleetOf(q)})
	b, _ := getStatsBody(t, srv, "")
	if row := b.Services[0]; row.Href != "" || row.Note != noteAmbiguous {
		t.Fatalf("ambiguous row = %+v", row)
	}
	r, _ := getRecords(t, srv, "?metric=breaking")
	if row := r.Items[0]; row.Service != nil || row.Change != nil || row.Note != noteAmbiguous {
		t.Fatalf("ambiguous record row = %+v", row)
	}
	// An operation record that names its domain still resolves.
	op := opRec("orders", "vault", "prod", "production", ago(1))
	op.ServiceDomain = "payments"
	if row := newStatsLinks(q).row(op); row.Service == nil || row.Service.Href != productview.Entity(fleet.KindService, string(fleet.NewServiceKeyDomain("payments", "orders"))) {
		t.Fatalf("domain operation row = %+v", row)
	}
}

func TestStatsServicesPaged(t *testing.T) {
	var recs []runrecord.Record
	for i := range 30 {
		recs = append(recs, ciRec(runrecord.KindDiff, fmt.Sprintf("svc-%02d", i), sha('a'), sha('b'), ago(2)))
	}
	srv := statsServer(t, Options{Stats: fixedSet(recordSet(recs...))})
	if code, _ := fetch(t, srv, "/api/stats?servicesLimit=101"); code != http.StatusUnprocessableEntity {
		t.Fatalf("servicesLimit=101 = %d, want 422", code)
	}
	b, _ := getStatsBody(t, srv, "")
	if b.ServicesTotal != 30 || len(b.Services) != 25 {
		t.Fatalf("default page = %d of %d", len(b.Services), b.ServicesTotal)
	}
	b, _ = getStatsBody(t, srv, "?servicesOffset=28&servicesLimit=5")
	if len(b.Services) != 2 || b.Services[0].Label != "svc-28" {
		t.Fatalf("last page = %+v", b.Services)
	}
	b, _ = getStatsBody(t, srv, "?servicesOffset=99")
	if len(b.Services) != 0 || b.Services == nil {
		t.Fatalf("past the end = %+v", b.Services)
	}
}

func TestCoverageNowEqualsWriterRecord(t *testing.T) {
	q := statsFleetQuery(t, false)
	b, _ := getStatsBody(t, statsServer(t, Options{Stats: fixedSet(stats.RecordSet{}), Fleet: fleetOf(q)}), "")
	want := stats.CoverageOf(q)
	if b.CoverageNow == nil || !reflect.DeepEqual(*b.CoverageNow, want) {
		t.Fatalf("coverageNow = %+v, want the writer's %+v", b.CoverageNow, want)
	}
	if rec := runrecord.CoverageRecord(want, "test", statsNowAt); !reflect.DeepEqual(*rec.Coverage, *b.CoverageNow) {
		t.Fatalf("writer body %+v differs from coverageNow", rec.Coverage)
	}
}

func TestStatsRecordsBlockedLinksChangeAnalysis(t *testing.T) {
	q := statsFleetQuery(t, false)
	srv := statsServer(t, Options{Stats: fixedSet(standardSet()), Fleet: fleetOf(q)})
	for _, metric := range []string{"blocked", "protected", "breaking"} {
		r, _ := getRecords(t, srv, "?metric="+metric)
		if r.Total != 1 || r.Items[0].Change == nil || !strings.HasPrefix(r.Items[0].Change.Href, "/fleet/changes/orders?old=") {
			t.Fatalf("%s = %+v", metric, r.Items)
		}
	}
}

func TestStatsRecordsRestricted(t *testing.T) {
	srv := statsServer(t, restrictedOpts(t, shopAccess))
	for _, metric := range stats.MetricNames {
		r, raw := getRecords(t, srv, "?metric="+metric)
		if !r.Hidden.Present || r.Completeness != "partial" {
			t.Fatalf("%s: hidden %+v, completeness %s", metric, r.Hidden, r.Completeness)
		}
		assertNotNamed(t, raw, "billing", "vault", "books")
	}
	r, _ := getRecords(t, srv, "?metric=coverage")
	if r.Total != 0 {
		t.Fatalf("restricted coverage records = %+v", r.Items)
	}
	r, _ = getRecords(t, srv, "?metric=held&environment=production")
	if r.Total != 0 {
		t.Fatalf("held = %+v", r.Items)
	}
}

func TestStatsRecordsLimitCapped(t *testing.T) {
	var recs []runrecord.Record
	for i := range 30 {
		recs = append(recs, blockedRec("orders", sha('a'), fmt.Sprintf("sha256:%064d", i), ago(i+1)))
	}
	srv := statsServer(t, Options{Stats: fixedSet(recordSet(recs...))})
	if code, _ := fetch(t, srv, "/api/stats/records?metric=blocked&limit=101"); code != http.StatusUnprocessableEntity {
		t.Fatalf("limit=101 = %d, want 422", code)
	}
	r, _ := getRecords(t, srv, "?metric=blocked")
	if r.Total != 30 || len(r.Items) != 25 {
		t.Fatalf("default page = %d of %d", len(r.Items), r.Total)
	}
	r, _ = getRecords(t, srv, "?metric=blocked&offset=29&limit=100")
	if len(r.Items) != 1 || !r.Items[0].Record.RecordedAt.Equal(ago(30)) {
		t.Fatalf("last page = %+v", r.Items)
	}
}

func TestStatsBehindUnknownMetricIs422(t *testing.T) {
	// The enum guards the route; the handler still refuses a name Behind
	// does not know.
	s := NewServer(Options{Stats: fixedSet(stats.RecordSet{}), Now: func() time.Time { return statsNowAt }})
	if _, err := s.getStatsRecords(context.Background(), &statsRecordsInput{Window: "30d", Metric: "nope", Limit: 25}); err == nil {
		t.Fatal("an unknown metric was answered")
	}
	if _, err := s.getStats(context.Background(), &statsInput{Window: "45x", ServicesLimit: 25}); err == nil {
		t.Fatal("an unparsable window was answered")
	}
	if _, err := s.getStatsRecords(context.Background(), &statsRecordsInput{Window: "45x", Metric: "breaking"}); err == nil {
		t.Fatal("an unparsable window was answered")
	}
}

func TestStatsClockDefaultsToNow(t *testing.T) {
	before := time.Now()
	if got := NewServer(Options{}).now(); got.Before(before) {
		t.Fatalf("now = %v", got)
	}
}

// ownersFleetQuery: orders (team checkout, domain commerce) runs in shop,
// billing (team payments, domain finance) in vault, catalog (no owner, no
// domain) and ledger (team books, a real domain named unknown) in shop.
func ownersFleetQuery(t *testing.T, partial bool) *fleet.Query {
	t.Helper()
	rev := func(name string, owner contract.Owner, domain string, d byte) fleet.RawRevision {
		c := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: name, Version: "1.0.0", Owner: owner, Domain: domain}}
		return fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: sha(d)}
	}
	target := func(ns, name string) fleet.RawTarget {
		return fleet.RawTarget{Scope: ns, Kind: fleet.SourceKindKubernetes, Name: name, Service: name, Compliance: fleet.StatusCompliant}
	}
	col := &fleet.Collection{
		Revisions: []fleet.RawRevision{rev("orders", contract.Owner{Team: "checkout"}, "commerce", 'a'), rev("billing", contract.Owner{Team: "payments"}, "finance", 'c'),
			rev("catalog", contract.Owner{}, "", 'e'), rev("ledger", contract.Owner{Team: "books"}, "unknown", 'f')},
		Targets: []fleet.RawTarget{target("shop", "orders"), target("vault", "billing"), target("shop", "catalog"), target("shop", "ledger")},
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return statsNowAt.Add(-time.Hour) }},
		fleet.NewMemorySource("k8s", "kubernetes", col))
	if err != nil {
		t.Fatal(err)
	}
	if partial {
		snap.Completeness = fleet.CompletenessPartial
	}
	return fleet.NewQuery(snap)
}

// ownersSet: production delivers orders twice and billing once.
func ownersSet() stats.RecordSet {
	return recordSet(
		opRec("orders", "shop", "prod", "production", ago(10)),
		opRec("orders", "shop", "prod", "production", ago(8)),
		opRec("billing", "vault", "prod", "production", ago(9)),
		ciRec(runrecord.KindDiff, "orders", sha('a'), sha('b'), ago(20)),
	)
}

func groupEnvs(groups []StatsEnvironmentGroup, key string) []stats.EnvironmentMetrics {
	for _, g := range groups {
		if g.Key == key {
			return g.Environments
		}
	}
	return nil
}

// aggregateEnvGroups is what pacto stats --by says per group, as
// environmentsBy carries it.
func aggregateEnvGroups(set stats.RecordSet, w stats.Window, by string, sc stats.Scope) []StatsEnvironmentGroup {
	out := []StatsEnvironmentGroup{}
	for _, g := range stats.Aggregate(set, stats.Query{Window: w, By: by}, sc, statsNowAt).Groups {
		envs := g.Metrics.Environments
		if envs == nil {
			envs = []stats.EnvironmentMetrics{}
		}
		out = append(out, StatsEnvironmentGroup{Key: g.Key, Label: g.Label, Environments: envs})
	}
	return out
}

func TestStatsEnvironmentsByOwner(t *testing.T) {
	q, set := ownersFleetQuery(t, false), ownersSet()
	b, _ := getStatsBody(t, statsServer(t, Options{Stats: fixedSet(set), Fleet: fleetOf(q)}), "")
	by := b.EnvironmentsBy
	checkout, payments := groupEnvs(by.Owner, "team:checkout"), groupEnvs(by.Owner, "team:payments")
	if len(checkout) != 1 || checkout[0].Environment != "production" || checkout[0].Operations != 2 ||
		len(payments) != 1 || payments[0].Environment != "production" || payments[0].Operations != 1 {
		t.Fatalf("production must count under each owner with only its promotions: %+v", by.Owner)
	}
	if finance := groupEnvs(by.Domain, "finance"); len(finance) != 1 || finance[0].Operations != 1 {
		t.Fatalf("domains = %+v", by.Domain)
	}
}

func TestStatsEnvironmentsByEqualsAggregate(t *testing.T) {
	q, set := ownersFleetQuery(t, false), ownersSet()
	b, _ := getStatsBody(t, statsServer(t, Options{Stats: fixedSet(set), Fleet: fleetOf(q)}), "")
	by := b.EnvironmentsBy
	if by.Attribution == nil || by.Attribution.Provenance != "inferred" || by.Attribution.Partial || by.Attribution.SnapshotID == "" {
		t.Fatalf("attribution = %+v", by.Attribution)
	}
	sc := stats.Scope{Place: stats.FleetPlacement(q, nil), Snapshot: statsAttribution(q)}
	for name, got := range map[string][]StatsEnvironmentGroup{"owner": by.Owner, "domain": by.Domain} {
		if want := aggregateEnvGroups(set, b.Window, name, sc); !reflect.DeepEqual(got, want) {
			t.Fatalf("by %s = %+v, stats.Aggregate says %+v", name, got, want)
		}
	}
	if hasLimitation(b.Limitations, stats.LimitationAttributionPartial) || b.Completeness != fleet.CompletenessComplete {
		t.Fatalf("a complete snapshot: %s, %v", b.Completeness, b.Limitations)
	}
}

func TestStatsEnvironmentsByUnknownKeys(t *testing.T) {
	set := recordSet(
		opRec("phantom", "shop", "prod", "production", ago(5)),
		opRec("catalog", "shop", "prod", "production", ago(5)),
		opRec("ledger", "shop", "prod", "production", ago(5)),
	)
	b, _ := getStatsBody(t, statsServer(t, Options{Stats: fixedSet(set), Fleet: fleetOf(ownersFleetQuery(t, false))}), "")
	keys := func(groups []StatsEnvironmentGroup) string {
		var out []string
		for _, g := range groups {
			out = append(out, g.Key+"="+g.Label)
		}
		return strings.Join(out, ",")
	}
	if got := keys(b.EnvironmentsBy.Owner); got != "team:books=books,(unknown)=unknown,(unowned)=unowned" {
		t.Fatalf("owner keys = %s", got)
	}
	if got := keys(b.EnvironmentsBy.Domain); got != "unknown=unknown,(none)=none,(unknown)=unknown" {
		t.Fatalf("domain keys = %s: a real domain named unknown must stay apart from (unknown)", got)
	}
}

func TestStatsEnvironmentsByAttributionPartial(t *testing.T) {
	b, _ := getStatsBody(t, statsServer(t, Options{Stats: fixedSet(ownersSet()), Fleet: fleetOf(ownersFleetQuery(t, true))}), "")
	n := 0
	for _, l := range b.Limitations {
		if l.Code == stats.LimitationAttributionPartial {
			n++
		}
	}
	if n != 1 || b.EnvironmentsBy.Attribution == nil || !b.EnvironmentsBy.Attribution.Partial || b.Completeness != fleet.CompletenessPartial {
		t.Fatalf("partial snapshot: %d ATTRIBUTION_PARTIAL, attribution %+v, completeness %s", n, b.EnvironmentsBy.Attribution, b.Completeness)
	}
}

func TestStatsEnvironmentsByRestrictedNeverNamesHidden(t *testing.T) {
	opts := Options{Stats: fixedSet(ownersSet()), Fleet: fleetOf(ownersFleetQuery(t, false)), View: shopView, OperationAccess: shopAccess}
	b, raw := getStatsBody(t, statsServer(t, opts), "")
	assertNotNamed(t, raw, "billing", "payments", "finance", "vault")
	if checkout := groupEnvs(b.EnvironmentsBy.Owner, "team:checkout"); len(b.EnvironmentsBy.Owner) != 1 || len(checkout) != 1 || checkout[0].Operations != 2 {
		t.Fatalf("restricted owners = %+v", b.EnvironmentsBy.Owner)
	}
}

func TestStatsEnvironmentsByEmptyIsArray(t *testing.T) {
	b, raw := getStatsBody(t, statsServer(t, Options{Stats: fixedSet(stats.RecordSet{})}), "")
	if !strings.Contains(raw, `"environmentsBy":{"owner":[],"domain":[]}`) || b.EnvironmentsBy.Attribution != nil {
		t.Fatalf("empty answer: %s", raw)
	}
}

// promoted is a publish of digest d by service and its verified promote on
// the Stage ns/env into env lead later, finishing at at.
func promoted(service, ns, env string, d byte, at time.Time, lead time.Duration) []runrecord.Record {
	pub := runrecord.Record{Version: runrecord.Version, ID: runrecord.ID(runrecord.KindPublish, service, "", sha(d)), Kind: runrecord.KindPublish,
		Service: service, NewRevision: sha(d), DeployDigest: sha(d), Outcome: runrecord.OutcomePassed, RecordedAt: at.Add(-lead)}
	op := opRec(service, ns, env, env, at)
	op.To = sha(d)
	return []runrecord.Record{pub, op}
}

func joined(parts ...[]runrecord.Record) stats.RecordSet {
	var recs []runrecord.Record
	for _, p := range parts {
		recs = append(recs, p...)
	}
	return recordSet(recs...)
}

func envByName(t *testing.T, envs []StatsEnvironment, name string) StatsEnvironment {
	t.Helper()
	for _, e := range envs {
		if e.Environment == name {
			return e
		}
	}
	t.Fatalf("no environment %s in %s", name, envNames(envs))
	return StatsEnvironment{}
}

func depthOf(e StatsEnvironment) string {
	if e.Depth == nil {
		return "none"
	}
	return fmt.Sprint(*e.Depth)
}

func groupKeys(groups []StatsLeadTimeGroup) string {
	var out []string
	for _, g := range groups {
		out = append(out, g.Key)
	}
	return strings.Join(out, ",")
}

// ladderStages: test feeds prod, prod feeds prod-eu and prod-us; solo is an
// isolated single Stage. The names sort opposite to the Stage graph (zz-test
// first in the graph, last by name) so a depth read from a name would show.
func ladderStages() []delivery.StageObject {
	return []delivery.StageObject{
		stageTo("zz-test", "zz-test", ""), stageTo("mm-prod", "mm-prod", "zz-test"),
		stageTo("aa-eu", "aa-eu", "mm-prod"), stageTo("ab-us", "ab-us", "mm-prod"), stageTo("solo", "solo", ""),
	}
}

// ladderSet promotes into every ladder environment: aa-eu leads 1h, 1h and
// 1h, ab-us 10h, zz-test 2h, mm-prod 3h and solo 4h, all inside the
// newest bucket of a 30d window, plus one aa-eu promotion two weeks back.
func ladderSet() stats.RecordSet {
	h := time.Hour
	return joined(
		promoted("orders", "shop", "aa-eu", '1', ago(2), h), promoted("orders", "shop", "aa-eu", '2', ago(3), h),
		promoted("orders", "shop", "aa-eu", '3', ago(4), h), promoted("orders", "shop", "ab-us", '4', ago(2), 10*h),
		promoted("orders", "shop", "zz-test", '5', ago(2), 2*h), promoted("orders", "shop", "mm-prod", '6', ago(2), 3*h),
		promoted("orders", "shop", "solo", '7', ago(2), 4*h), promoted("orders", "shop", "aa-eu", '8', ago(14*24), 5*h),
	)
}

func ladderOpts(stages []delivery.StageObject, set stats.RecordSet) Options {
	api := stagesDelivery{stages: stages}
	return Options{Stats: fixedSet(set), Delivery: func(*http.Request) (DeliveryAPI, error) { return api, nil }}
}

func TestStatsEnvironmentDepthFromStages(t *testing.T) {
	b, _ := getStatsBody(t, statsServer(t, ladderOpts(ladderStages(), ladderSet())), "")
	for name, want := range map[string]string{"zz-test": "0", "mm-prod": "1", "aa-eu": "2", "ab-us": "2", "solo": "0"} {
		if got := depthOf(envByName(t, b.Environments, name)); got != want {
			t.Fatalf("%s depth = %s, want %s", name, got, want)
		}
	}
	eu := envByName(t, b.Environments, "aa-eu")
	if !reflect.DeepEqual(eu.Upstream, []string{"mm-prod", "zz-test"}) || !reflect.DeepEqual(eu.Parents, []string{"mm-prod"}) {
		t.Fatalf("aa-eu upstream %v, parents %v: parents is the transitive reduction", eu.Upstream, eu.Parents)
	}
	for name, want := range map[string]bool{"aa-eu": true, "ab-us": true, "mm-prod": false, "zz-test": false, "solo": false} {
		if got := envByName(t, b.Environments, name).ChainEnd; got != want {
			t.Fatalf("%s chainEnd = %v, want %v (a root-only environment is never a chain end)", name, got, want)
		}
	}
	if p := envByName(t, b.Environments, "zz-test").Parents; p != nil {
		t.Fatalf("a root has parents %v", p)
	}
}

func TestStatsEnvironmentDepthCycleIsAbsent(t *testing.T) {
	stages := []delivery.StageObject{stageTo("a", "a", "b"), stageTo("b", "b", "a"), stageTo("c", "c", "b"), stageTo("r", "r", "")}
	set := joined(promoted("orders", "shop", "a", '1', ago(2), time.Hour), promoted("orders", "shop", "b", '2', ago(2), time.Hour),
		promoted("orders", "shop", "c", '3', ago(2), time.Hour), promoted("orders", "shop", "r", '4', ago(2), time.Hour))
	b, _ := getStatsBody(t, statsServer(t, ladderOpts(stages, set)), "")
	for _, name := range []string{"a", "b", "c"} {
		if e := envByName(t, b.Environments, name); e.Depth != nil || e.ChainEnd || e.Parents != nil {
			t.Fatalf("%s on or below a cycle: depth %s, chainEnd %v, parents %v", name, depthOf(e), e.ChainEnd, e.Parents)
		}
	}
	if depthOf(envByName(t, b.Environments, "r")) != "0" {
		t.Fatal("an environment off the cycle loses its depth")
	}
	if got := groupKeys(b.LeadTimeGroups); got != "depth-0,all" {
		t.Fatalf("groups = %s", got)
	}
}

func TestStatsEnvironmentDepthNoReadableStage(t *testing.T) {
	set := joined(promoted("orders", "shop", "lab", '9', ago(2), time.Hour))
	set.Entries = append(set.Entries, ladderSet().Entries...)
	b, _ := getStatsBody(t, statsServer(t, ladderOpts(ladderStages(), set)), "")
	if lab := envByName(t, b.Environments, "lab"); lab.Depth != nil || lab.ChainEnd || lab.Parents != nil || len(lab.Upstream) != 0 {
		t.Fatalf("lab has no readable Stage, yet %+v", lab)
	}
	if !hasLimitation(b.Limitations, LimitationEnvironmentOrderUnknown) || b.EnvironmentOrder != "stages" {
		t.Fatalf("partial order: %s %v", b.EnvironmentOrder, b.Limitations)
	}
	for _, g := range b.LeadTimeGroups {
		if g.Key == "all" {
			t.Fatal("an all group beside a chain end")
		}
	}
}

func TestStatsNameOrderHasNoDepth(t *testing.T) {
	b, _ := getStatsBody(t, statsServer(t, Options{Stats: fixedSet(ladderSet())}), "")
	for _, e := range b.Environments {
		if e.Depth != nil || e.ChainEnd || e.Parents != nil {
			t.Fatalf("name order placed %+v", e)
		}
	}
	if len(b.LeadTimeGroups) != 1 || b.LeadTimeGroups[0].Key != "all" || b.LeadTimeGroups[0].Label != "every environment" ||
		b.LeadTimeGroups[0].Environments != 5 || b.LeadTimeGroups[0].LeadTime.Joined != 8 {
		t.Fatalf("groups = %+v", b.LeadTimeGroups)
	}
	// No environment, no group.
	b, raw := getStatsBody(t, statsServer(t, Options{Stats: fixedSet(stats.RecordSet{})}), "")
	if !strings.Contains(raw, `"leadTimeGroups":[]`) || len(b.LeadTimeGroups) != 0 {
		t.Fatalf("empty answer: %s", raw)
	}
}

func TestStatsLeadTimeGroupsChainEnd(t *testing.T) {
	set := ladderSet()
	b, _ := getStatsBody(t, statsServer(t, ladderOpts(ladderStages(), set)), "")
	if got := groupKeys(b.LeadTimeGroups); got != "chain-end,depth-0,depth-1,depth-2" {
		t.Fatalf("groups = %s", got)
	}
	labels := map[string]string{"chain-end": "chain end", "depth-0": "entry", "depth-1": "hop 2", "depth-2": "hop 3"}
	members := map[string][]string{"chain-end": {"aa-eu", "ab-us"}, "depth-0": {"zz-test", "solo"}, "depth-1": {"mm-prod"}, "depth-2": {"aa-eu", "ab-us"}}
	window := stats.LeadTimesIn(set, stats.Query{Window: b.Window}, stats.Scope{}, statsNowAt, members)
	for _, g := range b.LeadTimeGroups {
		if g.Label != labels[g.Key] || g.Environments != len(members[g.Key]) || !reflect.DeepEqual(g.LeadTime, window[g.Key]) {
			t.Fatalf("%s = %+v, want label %q and %+v", g.Key, g, labels[g.Key], window[g.Key])
		}
		if (g.Key == "chain-end") != (g.Depth == nil) {
			t.Fatalf("%s depth = %v", g.Key, g.Depth)
		}
		if len(g.Buckets) != len(b.Buckets) {
			t.Fatalf("%s has %d buckets, the answer %d", g.Key, len(g.Buckets), len(b.Buckets))
		}
		for i, bk := range b.Buckets {
			want := stats.LeadTimesIn(set, stats.Query{Window: stats.Window{Since: bk.Since, Until: bk.Until}}, stats.Scope{}, statsNowAt, members)[g.Key]
			if !reflect.DeepEqual(g.Buckets[i], want) {
				t.Fatalf("%s bucket %d = %+v, want %+v", g.Key, i, g.Buckets[i], want)
			}
		}
	}
	end := b.LeadTimeGroups[0].LeadTime
	if end.Joined != 5 || *end.P50Seconds != 3600 || *end.P90Seconds != 10*3600 {
		t.Fatalf("chain end = %+v: the pooled promotions are 1h, 1h, 1h, 5h and 10h", end)
	}
}

func TestStatsLeadTimeGroupsNoChainEndFallsBackToAll(t *testing.T) {
	stages := []delivery.StageObject{stageTo("a", "a", "b"), stageTo("b", "b", "a")}
	set := joined(promoted("orders", "shop", "a", '1', ago(2), time.Hour), promoted("orders", "shop", "b", '2', ago(2), 3*time.Hour))
	b, _ := getStatsBody(t, statsServer(t, ladderOpts(stages, set)), "")
	if b.EnvironmentOrder != "stages" || groupKeys(b.LeadTimeGroups) != "all" || b.LeadTimeGroups[0].Environments != 2 || b.LeadTimeGroups[0].LeadTime.Joined != 2 {
		t.Fatalf("%s: groups = %+v", b.EnvironmentOrder, b.LeadTimeGroups)
	}
}

func TestStatsLeadTimeGroupsUnknownBucket(t *testing.T) {
	b, _ := getStatsBody(t, statsServer(t, ladderOpts(ladderStages(), ladderSet())), "")
	end := b.LeadTimeGroups[0]
	last := len(end.Buckets) - 1
	if !end.Buckets[0].Unknown || end.Buckets[0].P50Seconds != nil || end.Buckets[0].Joined != 0 {
		t.Fatalf("a bucket with no promotion = %+v, want unknown", end.Buckets[0])
	}
	if end.Buckets[last].Unknown || end.Buckets[last].Joined != 4 {
		t.Fatalf("newest bucket = %+v", end.Buckets[last])
	}
	if !slices.ContainsFunc(end.Buckets, func(l stats.LeadTime) bool { return !l.Unknown && l.Joined == 1 }) {
		t.Fatalf("the two-week-old promotion has no bucket: %+v", end.Buckets)
	}
}

func TestStatsRestrictedEnvironmentFieldsVisibleOnly(t *testing.T) {
	h := time.Hour
	set := joined(
		promoted("orders", "shop", "staging", '1', ago(3), h), promoted("orders", "shop", "production", '2', ago(2), 2*h),
		promoted("billing", "vault", "books-env", '3', ago(2), 9*h),
	)
	api := stagesDelivery{stages: []delivery.StageObject{stageTo("staging", "staging", ""), stageTo("production", "production", "staging")}}
	opts := Options{Stats: fixedSet(set), Fleet: fleetOf(statsFleetQuery(t, false)), View: shopView, OperationAccess: shopAccess,
		Delivery: func(*http.Request) (DeliveryAPI, error) { return api, nil }}
	b, raw := getStatsBody(t, statsServer(t, opts), "")
	assertNotNamed(t, raw, "billing", "vault", "books-env")
	prod := envByName(t, b.Environments, "production")
	if len(b.Environments) != 2 || depthOf(prod) != "1" || !prod.ChainEnd || !reflect.DeepEqual(prod.Parents, []string{"staging"}) {
		t.Fatalf("restricted environments = %+v", b.Environments)
	}
	if groupKeys(b.LeadTimeGroups) != "chain-end,depth-0,depth-1" || b.LeadTimeGroups[0].LeadTime.Joined != 1 || *b.LeadTimeGroups[0].LeadTime.P50Seconds != 7200 {
		t.Fatalf("restricted groups = %+v", b.LeadTimeGroups)
	}
}

func TestStatsEnvironmentDepthDanglingUpstreamIsAbsent(t *testing.T) {
	// production names staging, whose Stage the caller cannot read (or that
	// no longer exists): something feeds production, so it is no entry.
	stages := []delivery.StageObject{stageTo("production", "production", "staging"), stageTo("dr", "dr", "production"), stageTo("r", "r", "")}
	set := joined(promoted("orders", "shop", "production", '1', ago(2), time.Hour), promoted("orders", "shop", "dr", '2', ago(2), time.Hour),
		promoted("orders", "shop", "r", '3', ago(2), time.Hour))
	b, _ := getStatsBody(t, statsServer(t, ladderOpts(stages, set)), "")
	for _, name := range []string{"production", "dr"} {
		if e := envByName(t, b.Environments, name); e.Depth != nil || e.ChainEnd || e.Parents != nil {
			t.Fatalf("%s at or below a dangling upstream: depth %s, chainEnd %v, parents %v", name, depthOf(e), e.ChainEnd, e.Parents)
		}
	}
	if depthOf(envByName(t, b.Environments, "r")) != "0" || groupKeys(b.LeadTimeGroups) != "depth-0,all" {
		t.Fatalf("r depth %s, groups %s", depthOf(envByName(t, b.Environments, "r")), groupKeys(b.LeadTimeGroups))
	}
}

// partialStages refuses the cluster-wide list and the namespace prod.
type partialStages struct {
	stagesDelivery
}

func (d partialStages) Stages(_ context.Context, ns string) ([]delivery.StageObject, error) {
	if ns == "" || ns == "prod" {
		return nil, &DeliveryError{Status: http.StatusForbidden}
	}
	return d.stages, nil
}

func (partialStages) StageNamespaces(context.Context) ([]string, error) {
	return []string{"shop", "prod"}, nil
}

func TestStatsPartialStageReadSaysSo(t *testing.T) {
	api := partialStages{stagesDelivery{stages: []delivery.StageObject{stageTo("dev", "dev", ""), stageTo("staging", "staging", "dev")}}}
	set := joined(promoted("orders", "shop", "dev", '1', ago(2), time.Hour), promoted("orders", "shop", "staging", '2', ago(2), time.Hour))
	opts := Options{Stats: fixedSet(set), Delivery: func(*http.Request) (DeliveryAPI, error) { return api, nil }}
	b, _ := getStatsBody(t, statsServer(t, opts), "")
	if b.EnvironmentOrder != "stages" || !slices.ContainsFunc(b.Limitations, func(l fleet.Limitation) bool {
		return l.Code == LimitationEnvironmentOrderUnknown && strings.Contains(l.Message, "some Stages could not be read")
	}) {
		t.Fatalf("a partial Stage read reads as complete: %s %v", b.EnvironmentOrder, b.Limitations)
	}
}

func TestStatsEmptyWindowStaysEmptyOnPartialSnapshot(t *testing.T) {
	b, _ := getStatsBody(t, statsServer(t, Options{Stats: fixedSet(stats.RecordSet{}), Fleet: fleetOf(ownersFleetQuery(t, true))}), "")
	if b.Completeness != fleet.CompletenessEmpty || hasLimitation(b.Limitations, stats.LimitationAttributionPartial) {
		t.Fatalf("no records, partial snapshot: completeness %s, limitations %v", b.Completeness, b.Limitations)
	}
}
