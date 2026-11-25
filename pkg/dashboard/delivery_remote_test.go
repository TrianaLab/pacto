package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

const euID = "0e1f2a3b-0000-4000-8000-00000000e0e0"

var homeCluster = fleet.Cluster{ID: "home-uid", Name: "hub"}

// hidingDelivery is a remote that left out namespaces the reader may not list.
type hidingDelivery struct {
	*fakeDelivery
	hidden int
}

func (h hidingDelivery) HiddenNamespaces() int { return h.hidden }

// euCluster is a remote with a Stage named like the home one (prod/payments),
// held on a person, and an Errored change on a frozen Stage.
func euCluster() *fakeDelivery {
	f := sampleCluster("acme:team-checkout")
	f.stages = f.stages[:1]
	f.ops = f.ops[:1]
	return f
}

// startRemotes serves home with remotes read on every request.
func startRemotes(t *testing.T, home *fakeDelivery, remotes ...RemoteDelivery) string {
	t.Helper()
	return startRemotesWith(t, home, Options{Fleet: ownedFleet(t), Auth: (&authed{state: AuthState{SignedIn: true}}).auth(false, true)}, remotes...)
}

// startRemotesWith serves home over o with remotes read on every request.
func startRemotesWith(t *testing.T, home *fakeDelivery, o Options, remotes ...RemoteDelivery) string {
	t.Helper()
	o.DeliveryRemotes = func(*http.Request) (fleet.Cluster, []RemoteDelivery) { return homeCluster, remotes }
	return startDeliveryWith(t, home, o, true, nil)
}

func eu(api DeliveryAPI) RemoteDelivery {
	return RemoteDelivery{Cluster: fleet.Cluster{ID: euID, Name: "eu-prod"}, API: api, Context: "arn:eu"}
}

func clusterOf(v any) string {
	c, _ := v.(map[string]any)["cluster"].(string)
	return c
}

func clustersIn(items any) []string {
	var out []string
	for _, it := range items.([]any) {
		out = append(out, clusterOf(it))
	}
	return out
}

func TestListsMergeRemotesStamped(t *testing.T) {
	remote := euCluster()
	remote.stages[0].Spec.Candidates.UpstreamStage = &delivery.StageRef{Name: "gone"}
	base := startRemotes(t, sampleCluster("acme:team-checkout"), eu(remote))
	_, envs := call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	cls := envs["clusters"].([]any)
	expect(t, len(cls) == 2 && cls[0].(map[string]any)["id"] == "home-uid" && cls[0].(map[string]any)["name"] == "hub" &&
		cls[1].(map[string]any)["id"] == euID && cls[1].(map[string]any)["completeness"] == "complete", "clusters %v", cls)
	expect(t, envs["completeness"] == "complete", "every cluster read: %v", envs["completeness"])
	var eu map[string]any
	for _, r := range envs["environments"].([]any) {
		if clusterOf(r) == euID {
			eu = r.(map[string]any)
		}
	}
	expect(t, eu != nil && eu["stage"] == "payments" && len(eu["actions"].([]any)) == 0 && eu["offers"] == nil,
		"a remote row only reads: %v", eu)
	expect(t, eu["upstream"].(map[string]any)["problem"] == "missing", "a cluster read whole has no such upstream: %v", eu["upstream"])
	expect(t, len(remote.stages) == 1 && remote.stages[0].Metadata.Cluster == "", "the host's objects are never stamped in place")
	// Describe and History read a remote Stage in its own cluster.
	f := fanned{DeliveryAPI: sampleCluster(), by: map[string]DeliveryAPI{euID: remote}}
	st := stage("prod", "payments", "payments", delivery.StageStatus{})
	st.Metadata.Cluster = euID
	expect(t, f.Describe(context.Background(), &st, dRun).Bundle != nil, "described in its cluster")
	remote.history = &delivery.History{Note: "eu"}
	h, _ := f.History(context.Background(), "prod", "payments", &st)
	expect(t, h.Note == "eu", "history in its cluster: %v", h)
	h, _ = f.History(context.Background(), "prod", "payments", nil)
	expect(t, h.Note != "eu", "an unlisted Stage reads at home: %v", h)
}

func TestListsKeepSameNameInTwoClusters(t *testing.T) {
	remote := euCluster()
	remote.stages = append(remote.stages, stage("other", "orphan", "orphan", delivery.StageStatus{Frozen: true}))
	remote.ops = append(remote.ops, operation("other", "broke", "orphan", delivery.OperationStatus{Phase: delivery.PhaseConcluded, Outcome: delivery.Errored}))
	base := startRemotes(t, sampleCluster("acme:team-checkout"), eu(remote))
	_, envs := call(t, http.MethodGet, base+"/api/delivery/environments?service=payments", nil)
	expect(t, strings.Join(clustersIn(envs["environments"]), ",") == ","+euID, "two payments rows: %v", envs["environments"])
	nodes := envs["promotion"].(map[string]any)["nodes"].([]any)
	got := clustersIn(nodes)
	slices.Sort(got)
	expect(t, strings.Join(got, ",") == ","+euID, "two nodes: %v", nodes)
	_, inbox := call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	held := inbox["held"].([]any)
	expect(t, strings.Join(clustersIn(held), ",") == ",,"+euID, "the eu change is its own item: %v", held)
	errored := inbox["errored"].([]any)
	expect(t, strings.Join(clustersIn(errored), ",") == ","+euID && errored[1].(map[string]any)["readOnly"] == true,
		"errored on the frozen eu Stage: %v", errored)
	_, hist := call(t, http.MethodGet, base+"/api/delivery/history", nil)
	hs := hist["histories"].([]any)
	var pays []string
	for _, h := range hs {
		if m := h.(map[string]any); m["namespace"] == "prod" {
			pays = append(pays, clusterOf(h))
			if clusterOf(h) == euID {
				expect(t, m["undoOffers"] == nil, "a remote history offers no undo: %v", m)
			}
		}
	}
	expect(t, strings.Join(pays, ",") == ","+euID, "two histories: %v", hs)
}

func TestInboxRemoteItemReadOnlyWithNext(t *testing.T) {
	base := startRemotes(t, sampleCluster("acme:team-checkout"), eu(euCluster()))
	_, inbox := call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	held := inbox["held"].([]any)
	expect(t, strings.Join(clustersIn(held), ",") == ",,"+euID, "held %v", held)
	last := held[2].(map[string]any)
	expect(t, last["readOnly"] == true && last["next"] == "kubectl config use-context arn:eu && pacto delivery approve for-checkout -n prod" &&
		last["service"] == "payments", "a remote item only reads and says how to act there: %v", last)
	for _, h := range held[:2] {
		m := h.(map[string]any)
		expect(t, m["readOnly"] == nil && m["next"] == nil, "a home item acts here: %v", m)
	}
}

// 514 D9 holds with remotes: an operation record of a registered remote
// cluster stays hidden from stats and counted, never named.
func TestOperationRecordsOtherClusterStillHidden(t *testing.T) {
	other := opRec("orders", "shop", "prod", "far-env", ago(8))
	other.Stage.Cluster = euID
	set := standardSet()
	set.Entries = append(set.Entries, stats.Entry{Record: other, Digest: fmt.Sprintf("sha256:%064d", 99), Anchor: runrecord.AnchorTag("orders")})
	o := restrictedOpts(t, shopAccess)
	o.Stats = fixedSet(set)
	o.Now = func() time.Time { return statsNowAt }
	base := startRemotesWith(t, sampleCluster("acme:team-checkout"), o, eu(euCluster()))
	resp, err := http.Get(base + "/api/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	rawb, _ := io.ReadAll(resp.Body)
	var b statsBody
	if err := json.Unmarshal(rawb, &b); err != nil {
		t.Fatalf("%v: %s", err, rawb)
	}
	expect(t, b.Hidden.OtherClusters == 1 && len(b.Environments) == 1 && hasLimitation(b.Limitations, stats.LimitationOtherClusters),
		"hidden %+v, environments %+v, limitations %v", b.Hidden, b.Environments, b.Limitations)
	assertNotNamed(t, string(rawb), euID, "eu-prod", "far-env")
}

// A remote op whose Stage the reader cannot see in that cluster is judged
// there, unverified: never asked of the dashboard's own cluster.
func TestRemoteOpWithoutStageAskedInItsCluster(t *testing.T) {
	remote := euCluster()
	remote.stages = nil
	home := sampleCluster("acme:team-checkout")
	home.stages = nil
	base := startRemotes(t, home, eu(remote))
	_, inbox := call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	expect(t, remote.accesses.Load() == 0 && home.accesses.Load() == 0, "no review for an unread Stage")
	expect(t, inbox["completeness"] == "partial", "an unchecked change marks the answer partial: %v", inbox)
	_, envs := call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	expect(t, len(envs["environments"].([]any)) == 0, "no rows: %v", envs)
}

func TestRemoteErrorPartialClusterListing(t *testing.T) {
	down := RemoteDelivery{Cluster: fleet.Cluster{ID: "down-uid"}, Err: errors.New("the review took longer than 5s")}
	refusing := euCluster()
	refusing.listErr = errors.New("connection refused")
	noOps := euCluster()
	noOps.opsErr = errors.New("operations forbidden")
	asOf := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	recorded := RemoteDelivery{Cluster: fleet.Cluster{ID: "rec-uid", Name: "ap"}, API: euCluster(), AsOf: &asOf}
	base := startRemotes(t, sampleCluster("acme:team-checkout"), down, RemoteDelivery{Cluster: fleet.Cluster{ID: "r1"}, API: refusing},
		RemoteDelivery{Cluster: fleet.Cluster{ID: "r2"}, API: noOps}, recorded)
	_, inbox := call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	expect(t, inbox["completeness"] == "partial", "partial: %v", inbox["completeness"])
	cls := inbox["clusters"].([]any)
	why := func(i int) string { return cls[i].(map[string]any)["why"].(string) }
	expect(t, len(cls) == 5 && why(1) == "the review took longer than 5s" && why(2) == "connection refused" && why(3) == "operations forbidden",
		"each unread cluster says why: %v", cls)
	expect(t, cls[4].(map[string]any)["asOf"] == "2026-11-20T08:00:00Z" && cls[4].(map[string]any)["completeness"] == "complete",
		"a recorded cluster carries its records' time: %v", cls[4])
	expect(t, strings.Join(clustersIn(inbox["held"]), ",") == ",,rec-uid", "only the read cluster's items: %v", inbox["held"])
}

func TestHiddenRemoteCountedNotListed(t *testing.T) {
	hidden := RemoteDelivery{Cluster: fleet.Cluster{ID: "secret-uid", Name: "secret"}, Hidden: true}
	partly := RemoteDelivery{Cluster: fleet.Cluster{ID: euID, Name: "eu-prod"}, API: hidingDelivery{euCluster(), 2}}
	base := startRemotes(t, sampleCluster(), hidden, partly)
	code, envs := call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	raw, _ := json.Marshal(envs)
	expect(t, code == http.StatusOK && envs["hiddenClusters"] == float64(1) && !strings.Contains(string(raw), "secret"),
		"a hidden cluster is counted, never named: %s", raw)
	cls := envs["clusters"].([]any)
	expect(t, len(cls) == 2 && cls[1].(map[string]any)["hidden"] == float64(2) && cls[1].(map[string]any)["completeness"] == "partial" &&
		envs["completeness"] == "partial", "denied namespaces are counted: %v", cls)
	// With only a hidden remote the answer is partial too.
	base = startRemotes(t, sampleCluster(), hidden)
	_, envs = call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	expect(t, envs["completeness"] == "partial" && len(envs["clusters"].([]any)) == 1, "partial: %v", envs)
}

func TestHistoryRoutesByCluster(t *testing.T) {
	remote := euCluster()
	remote.history = &delivery.History{Note: "from eu", Completeness: "complete"}
	remote.blame = &delivery.Blame{Digest: dRun}
	remote.undo = &delivery.Undo{Kind: "none"}
	remote.record = &delivery.RecordView{}
	home := sampleCluster()
	base := startRemotes(t, home, eu(remote))
	q := "namespace=prod&stage=payments&cluster=" + euID
	_, hist := call(t, http.MethodGet, base+"/api/delivery/history?"+q, nil)
	h := hist["histories"].([]any)[0].(map[string]any)
	expect(t, h["note"] == "from eu" && h["cluster"] == euID && h["undoOffers"] == nil, "the eu history: %v", h)
	_, hist = call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=gone&cluster="+euID, nil)
	expect(t, hist["histories"].([]any)[0].(map[string]any)["cluster"] == euID, "an unread Stage keeps its cluster: %v", hist)
	for _, p := range []string{"blame?" + q, "undo?namespace=prod&name=for-checkout&cluster=" + euID, "record?id=sha256:r&" + q} {
		code, body := call(t, http.MethodGet, base+"/api/delivery/"+p, nil)
		expect(t, code == http.StatusOK, "%s: %d %v", p, code, body)
	}
	code, op := call(t, http.MethodGet, base+"/api/delivery/operations/prod/for-checkout?cluster="+euID, nil)
	expect(t, code == http.StatusOK && len(op["actions"].([]any)) == 0, "a remote change offers no action: %d %v", code, op)
	code, pr := call(t, http.MethodGet, base+"/api/delivery/progress?"+q, nil)
	expect(t, code == http.StatusOK, "progress: %d %v", code, pr)
	expect(t, len(home.planned) == 0, "nothing read in eu is planned at home: %v", home.planned)
}

func TestUnknownClusterIs404(t *testing.T) {
	base := startRemotes(t, sampleCluster(), eu(euCluster()),
		RemoteDelivery{Cluster: fleet.Cluster{ID: "secret-uid"}, Hidden: true},
		RemoteDelivery{Cluster: fleet.Cluster{ID: "down-uid", Name: "down"}, Err: errors.New("unreachable")})
	for _, c := range []string{"nope", "secret-uid"} {
		for _, p := range []string{"blame", "history"} {
			code, body := call(t, http.MethodGet, base+"/api/delivery/"+p+"?namespace=prod&stage=payments&cluster="+c, nil)
			expect(t, code == http.StatusNotFound && body["code"] == "ClusterUnknown", "%s %s: %d %v", p, c, code, body)
		}
	}
	code, body := call(t, http.MethodGet, base+"/api/delivery/blame?namespace=prod&stage=payments&cluster=down-uid", nil)
	expect(t, code == http.StatusServiceUnavailable && body["code"] == "ClusterUnreachable" && strings.Contains(body["detail"].(string), "down"),
		"down: %d %v", code, body)
}

func TestRemoteWritesRefuse(t *testing.T) {
	home := sampleCluster("acme:team-checkout")
	base := startRemotes(t, home, eu(euCluster()), RemoteDelivery{Cluster: fleet.Cluster{ID: "h-uid", Name: "hidden"}, Hidden: true})
	use := "kubectl config use-context arn:eu && "
	for _, c := range []struct{ method, path, next string }{
		{http.MethodPost, "plan?cluster=" + euID, use + "pacto delivery plan --stage payments -n prod"},
		{http.MethodPost, "operations?cluster=" + euID, use + "pacto delivery propose --stage payments -n prod"},
		{http.MethodPost, "operations/prod/for-checkout/decision?cluster=" + euID, use + "pacto delivery approve for-checkout -n prod"},
		{http.MethodDelete, "operations/prod/for-checkout?cluster=" + euID, use + "pacto delivery cancel for-checkout -n prod"},
		{http.MethodDelete, "operations/prod/op?cluster=h-uid", "pacto delivery cancel op -n prod (in cluster h-uid)"},
	} {
		var body any = delivery.Request{Type: delivery.TypePromote, Namespace: "prod", Stage: "payments", To: dNext}
		if strings.Contains(c.path, "decision") {
			body = map[string]any{"decision": "approve"}
		}
		code, out := call(t, c.method, base+"/api/delivery/"+c.path, body)
		expect(t, code == http.StatusConflict && out["code"] == "RemoteClusterReadOnly" && strings.HasSuffix(out["detail"].(string), c.next),
			"%s %s: %d %v", c.method, c.path, code, out)
	}
	expect(t, len(home.planned)+len(home.proposed)+len(home.approved)+len(home.cancelled) == 0, "nothing acted at home")
}

// The dashboard's own cluster, by the id its listings name, is "": reads
// answer there and writes act there.
func TestHomeClusterIDIsHome(t *testing.T) {
	home := sampleCluster("acme:team-checkout")
	home.blame = &delivery.Blame{Digest: dRun}
	base := startRemotes(t, home, eu(euCluster()))
	code, body := call(t, http.MethodGet, base+"/api/delivery/blame?namespace=prod&stage=payments&cluster="+homeCluster.ID, nil)
	expect(t, code == http.StatusOK, "blame at home by id: %d %v", code, body)
	_, hist := call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments&cluster="+homeCluster.ID, nil)
	h := hist["histories"].([]any)[0].(map[string]any)
	expect(t, clusterOf(h) == "" && hist["clusters"] == nil, "home by id reads as home: %v", hist)
	code, body = call(t, http.MethodPost, base+"/api/delivery/plan?cluster="+homeCluster.ID,
		delivery.Request{Type: delivery.TypePromote, Namespace: "prod", Stage: "payments", To: dNext})
	expect(t, code == http.StatusOK && len(home.planned) == 1, "plan at home by id: %d %v", code, body)
}

// A remote held change whose decider cannot be reviewed from here still
// shows, read-only: who may decide is answered in that cluster.
func TestInboxRemoteUnverifiedStaysReadOnly(t *testing.T) {
	remote := euCluster()
	remote.accessErr = "this dashboard only reads cluster eu-prod"
	base := startRemotes(t, sampleCluster("acme:team-checkout"), eu(remote))
	_, inbox := call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	held := inbox["held"].([]any)
	expect(t, strings.Join(clustersIn(held), ",") == ",,"+euID, "held %v", held)
	expect(t, held[2].(map[string]any)["readOnly"] == true, "read-only: %v", held[2])
	expect(t, !strings.Contains(fmt.Sprint(inbox["unreadable"]), euID), "no uid in the notes: %v", inbox)
}

// A one-Stage history of a remote names its cluster in the listing; cluster
// without a Stage is refused, never dropped.
func TestOneStageRemoteHistoryNamesCluster(t *testing.T) {
	remote := euCluster()
	remote.history = &delivery.History{Completeness: "complete"}
	base := startRemotes(t, sampleCluster(), eu(remote))
	_, hist := call(t, http.MethodGet, base+"/api/delivery/history?namespace=prod&stage=payments&cluster="+euID, nil)
	cls, _ := hist["clusters"].([]any)
	expect(t, len(cls) == 2 && cls[0].(map[string]any)["name"] == "hub" && cls[1].(map[string]any)["name"] == "eu-prod", "clusters %v", hist)
	for _, q := range []string{"stage=payments&cluster=" + euID, "cluster=" + euID} {
		code, body := call(t, http.MethodGet, base+"/api/delivery/history?"+q, nil)
		expect(t, code == http.StatusBadRequest && body["code"] == "ClusterNeedsStage", "%s: %d %v", q, code, body)
	}
}

// Answers that stay home-cluster answers are the same with remotes.
func TestRemotesLeaveOtherAnswersUnchanged(t *testing.T) {
	home := sampleCluster("acme:team-payments")
	plain := startDelivery(t, home, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	fanned := startRemotes(t, home, eu(euCluster()))
	for _, p := range []string{"published", "trust", "autonomy", "refusals", "lifecycle?service=payments&digest=" + dRun} {
		want, got := rawGet(t, plain+"/api/delivery/"+p), rawGet(t, fanned+"/api/delivery/"+p)
		expect(t, want == got, "%s changed with remotes:\n%s\n%s", p, want, got)
	}
}

func TestSingleClusterListingJSONUnchanged(t *testing.T) {
	home := sampleCluster("acme:team-checkout")
	plain := startDelivery(t, home, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	none := startRemotes(t, home)
	for _, p := range []string{"inbox", "environments?service=payments", "history"} {
		want, got := rawGet(t, plain+"/api/delivery/"+p), rawGet(t, none+"/api/delivery/"+p)
		expect(t, want == got, "%s changed with no remote:\n%s\n%s", p, want, got)
		expect(t, !regexp.MustCompile(`"(cluster|clusters|hiddenClusters|readOnly)"`).MatchString(want), "%s names a cluster: %s", p, want)
	}
}

var asOfField = regexp.MustCompile(`"asOf":"[^"]*"`)

// rawGet is the body of a GET with every asOf blanked.
func rawGet(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	b, _ := io.ReadAll(resp.Body)
	return asOfField.ReplaceAllString(string(b), `"asOf":""`)
}

func TestRemoteNextWithoutContextNamesTheCluster(t *testing.T) {
	r := RemoteDelivery{Cluster: fleet.Cluster{ID: "uid-1"}}
	expect(t, r.next("pacto delivery cancel op -n prod") == "pacto delivery cancel op -n prod (in cluster uid-1)", "next %q", r.next("x"))
}

// unreadableDelivery is a remote that says what of it could not be read.
type unreadableDelivery struct {
	*fakeDelivery
	unreadable []string
}

func (u unreadableDelivery) Unreadable() []string { return u.unreadable }

// TestRemoteUnreadablePartialCluster: what a remote could not read or verify
// lists under its cluster, which reads partial; Act is the next step of a
// cluster no context reaches.
func TestRemoteUnreadablePartialCluster(t *testing.T) {
	member := RemoteDelivery{Cluster: fleet.Cluster{ID: euID, Name: "eu-prod"}, API: unreadableDelivery{euCluster(), []string{"billing/billing-prod: 2 records did not verify"}},
		Act: "decide it on the hub"}
	base := startRemotes(t, sampleCluster(), member)
	_, envs := call(t, http.MethodGet, base+"/api/delivery/environments", nil)
	cl := envs["clusters"].([]any)[1].(map[string]any)
	expect(t, cl["completeness"] == "partial" && cl["unreadable"].([]any)[0] == "billing/billing-prod: 2 records did not verify" && envs["completeness"] == "partial",
		"unreadable records list under the member: %v", cl)
	expect(t, member.next("pacto delivery approve op -n prod") == "decide it on the hub", "act %q", member.next("x"))
}
