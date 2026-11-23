package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// acceptPlanOf is a planner that records the draft it was asked to plan.
func acceptPlanOf(drafts *[]string) func(context.Context, fleet.ServiceKey, *fleet.ContractRevision, string) (AcceptPlanAnswer, error) {
	return func(_ context.Context, k fleet.ServiceKey, _ *fleet.ContractRevision, fromDraft string) (AcceptPlanAnswer, error) {
		*drafts = append(*drafts, fromDraft)
		return AcceptPlanAnswer{Plan: &AcceptPlanView{Service: string(k), IntentDigest: "sha256:i", Draft: fromDraft}, Command: "pacto accept x"}, nil
	}
}

// executeServer serves q with a planner and d as the declarer, behind auth.
func executeServer(t *testing.T, q *fleet.Query, auth *Auth, view func(context.Context, *fleet.Query) (*fleet.Query, error), d Declarer) string {
	t.Helper()
	var drafts []string
	return serveOptions(t, Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }, View: view, Auth: auth,
		AcceptPlan: acceptPlanOf(&drafts), Declare: d})
}

func TestFleetAcceptPlanUnchanged(t *testing.T) {
	// Without a declarer the plan answers as 508-S7 shipped it: no execute
	// offer, and no write route.
	var drafts []string
	q := eventFleetQuery(t)
	base := serveOptions(t, Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }, Auth: viewerAuth(true, AuthState{}), AcceptPlan: acceptPlanOf(&drafts)})
	code, out := call(t, http.MethodPost, base+"/api/fleet/accept", map[string]string{"serviceKey": libKey(t, base)})
	raw, _ := json.Marshal(out)
	if code != http.StatusOK || out["execute"] != nil || string(raw) != `{"command":"pacto accept x","plan":{"intentDigest":"sha256:i","ranges":null,"service":"`+libKey(t, base)+`","subject":"","version":"","whoMayAccept":""}}` {
		t.Errorf("%d %s", code, raw)
	}
	if code, _ := call(t, http.MethodPost, base+"/api/fleet/accept/execute", map[string]string{"serviceKey": libKey(t, base), "intentDigest": "sha256:i", "mode": "draft"}); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
		t.Errorf("execute answered %d without a declarer", code)
	}
	if !slices.Equal(drafts, []string{""}) {
		t.Errorf("planned %v", drafts)
	}
}

func TestAcceptPlanCarriesExecuteOfferAndDraft(t *testing.T) {
	var drafts []string
	q := eventFleetQuery(t)
	d := &fakeDeclarer{write: true}
	base := serveOptions(t, Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }, Auth: viewerAuth(false, AuthState{SignedIn: true}),
		AcceptPlan: acceptPlanOf(&drafts), Declare: d})
	var ans AcceptPlanAnswer
	postJSON(t, base+"/api/fleet/accept", map[string]string{"serviceKey": libKey(t, base), "fromDraft": "pacto-accept-draft-3"}, http.StatusOK, &ans)
	if ans.Execute == nil || ans.Execute.SignTicket != "ticket-sha256:i" || ans.Plan.Draft != "pacto-accept-draft-3" || !slices.Equal(drafts, []string{"pacto-accept-draft-3"}) {
		t.Errorf("%+v %v", ans, drafts)
	}
	// A draft is named by its tag alone, never by another repository.
	postJSON(t, base+"/api/fleet/accept", map[string]string{"serviceKey": libKey(t, base), "fromDraft": "evil/repo:pacto-accept-draft-1"}, http.StatusUnprocessableEntity, nil)
}

func TestAcceptExecuteRouteAbsentWithoutDeclarer(t *testing.T) {
	q := eventFleetQuery(t)
	body := map[string]string{"serviceKey": libKey(t, ""), "intentDigest": "sha256:i", "mode": "draft"}
	for name, opts := range map[string]Options{
		"no declarer":           {AcceptPlan: acceptPlanOf(new([]string))},
		"declarer cannot write": {AcceptPlan: acceptPlanOf(new([]string)), Declare: &fakeDeclarer{}},
		"no planner":            {Declare: &fakeDeclarer{write: true}},
	} {
		opts.Fleet, opts.Auth = func(context.Context) (*fleet.Query, error) { return q, nil }, viewerAuth(true, AuthState{})
		base := serveOptions(t, opts)
		if code, _ := call(t, http.MethodPost, base+"/api/fleet/accept/execute", body); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
			t.Errorf("%s: execute answered %d", name, code)
		}
	}
}

func TestAcceptExecuteNeedsIdentity(t *testing.T) {
	body := map[string]string{"serviceKey": libKey(t, ""), "intentDigest": "sha256:i", "mode": "draft"}
	for name, c := range map[string]struct {
		auth *Auth
		code string
	}{
		"open host":    {openAuth(), "IdentityRequired"},
		"link session": {viewerAuth(false, AuthState{SignedIn: true, FleetOnly: true}), "SignInLinkReadsFleet"},
	} {
		d := &fakeDeclarer{write: true}
		base := executeServer(t, eventFleetQuery(t), c.auth, nil, d)
		code, out := call(t, http.MethodPost, base+"/api/fleet/accept/execute", body)
		if code != http.StatusForbidden || out["code"] != c.code || len(d.targets) != 0 {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
}

func TestAcceptExecuteHiddenServiceNotFound(t *testing.T) {
	hideAll := func(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(_, _ string) bool { return false }, nil), nil
	}
	d := &fakeDeclarer{write: true}
	base := executeServer(t, eventFleetQuery(t), viewerAuth(false, AuthState{SignedIn: true}), hideAll, d)
	code, _ := call(t, http.MethodPost, base+"/api/fleet/accept/execute", map[string]string{"serviceKey": libKey(t, base), "intentDigest": "sha256:i", "mode": "draft"})
	if code != http.StatusNotFound || len(d.targets) != 0 {
		t.Errorf("hidden service answered %d", code)
	}
	failing := serveOptions(t, Options{Fleet: func(context.Context) (*fleet.Query, error) { return nil, errors.New("down") },
		Auth: viewerAuth(true, AuthState{}), AcceptPlan: acceptPlanOf(new([]string)), Declare: d})
	if code, _ := call(t, http.MethodPost, failing+"/api/fleet/accept/execute", map[string]string{"serviceKey": "x", "intentDigest": "sha256:i", "mode": "draft"}); code < 500 {
		t.Errorf("a failed snapshot answered %d", code)
	}
}

// runsQuery is a fleet where reports runs as a Pacto in prod (its workload
// unknown) and as the Deployment reports-api in cluster 0b6f2a4c-3d1e-4f5a-9b8c-7d6e5f4a3b2c.
func runsQuery(t *testing.T) *fleet.Query {
	t.Helper()
	now := time.Now().UTC()
	ref := "oci://x/reports@" + validDigest("a")
	b := &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "reports", Version: "1.0.0"}}, RawYAML: []byte("reports"), FS: fstest.MapFS{}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return now }},
		fleet.NewMemorySource("local", "local", &fleet.Collection{
			Revisions: []fleet.RawRevision{{Bundle: b, ResolvedRef: ref, Digest: validDigest("a")}},
			Targets: []fleet.RawTarget{
				{Cluster: &fleet.Cluster{ID: "1c7f3b5d-4e2f-4a6b-8c9d-8e7f6a5b4c3d"}, Scope: "prod", Kind: fleet.SourceKindKubernetes, Name: "reports", Service: "reports", ResolvedRef: ref, EvidenceAt: &now},
				{Cluster: &fleet.Cluster{ID: "0b6f2a4c-3d1e-4f5a-9b8c-7d6e5f4a3b2c"}, Scope: "dev", Kind: fleet.SourceKindKubernetes, Name: "reports-dev", Service: "reports", ResolvedRef: ref, EvidenceAt: &now,
					Workload: &fleet.WorkloadRef{Kind: "Deployment", Name: "reports-api"}},
				{Scope: "files", Kind: "local", Name: "reports", Service: "reports", ResolvedRef: ref, EvidenceAt: &now},
			},
		}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func TestAcceptExecuteHandsTheDeclarerWhereTheServiceRuns(t *testing.T) {
	q := runsQuery(t)
	v, err := q.GetService("reports")
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDeclarer{write: true}
	base := executeServer(t, q, viewerAuth(false, AuthState{SignedIn: true}), nil, d)
	var ans AcceptExecuteAnswer
	postJSON(t, base+"/api/fleet/accept/execute", map[string]string{"serviceKey": string(v.Service.Key), "intentDigest": "sha256:i", "mode": "sign"}, http.StatusOK, &ans)
	if ans.Draft == "" || ans.Command != "sign" || len(d.targets) != 1 {
		t.Fatalf("%+v %+v", ans, d.targets)
	}
	tg := d.targets[0]
	got := []string{}
	for _, w := range tg.Runs {
		got = append(got, w.Cluster+"/"+w.Namespace+"/"+w.Kind+"/"+w.Name)
	}
	slices.Sort(got)
	if tg.Key != string(v.Service.Key) || tg.Rev == nil || !slices.Equal(got, []string{"0b6f2a4c-3d1e-4f5a-9b8c-7d6e5f4a3b2c/dev/Deployment/reports-api", "1c7f3b5d-4e2f-4a6b-8c9d-8e7f6a5b4c3d/prod/Pacto/reports"}) {
		t.Errorf("target %+v runs %v", tg, got)
	}
	d.err = errors.New("boom")
	if code, _ := call(t, http.MethodPost, base+"/api/fleet/accept/execute", map[string]string{"serviceKey": string(v.Service.Key), "intentDigest": "sha256:i", "mode": "draft"}); code < 500 {
		t.Errorf("a failed execution answered %d", code)
	}
}

func TestOpenAPIAcceptExecute(t *testing.T) {
	raw, err := ExportOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	post, _ := doc["paths"].(map[string]any)["/api/fleet/accept/execute"].(map[string]any)["post"].(map[string]any)
	if post == nil || post["operationId"] != "fleet-accept-execute" {
		t.Fatalf("execute operation %v", post)
	}
	if got := sortedKeys(schemaFieldEnum(t, doc, "AcceptExecuteInput", "mode")); !equalStrs(got, sortedSet("sign", "draft")) {
		t.Errorf("mode enum %v", got)
	}
	if got := sortedKeys(schemaFieldEnum(t, doc, "AcceptDraftView", "state")); !equalStrs(got, sortedSet("draft", "unreadable")) {
		t.Errorf("state enum %v", got)
	}
}
