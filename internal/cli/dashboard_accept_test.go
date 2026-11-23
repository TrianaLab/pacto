package cli

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// dashboardAcceptEnv is a registry holding reports 1.6.0, whose record
// proposes ledger's range, behind a spy that counts every request other than
// a read once watching starts.
type dashboardAcceptEnv struct {
	svc    *app.Service
	repo   string
	writes atomic.Int32
	watch  atomic.Bool
}

func newDashboardAcceptEnv(t *testing.T) *dashboardAcceptEnv {
	t.Helper()
	ciEnvOff(t)
	statsOff(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("PACTO_NO_UPDATE_CHECK", "1")
	e := &dashboardAcceptEnv{}
	reg := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if e.watch.Load() && r.Method != http.MethodGet && r.Method != http.MethodHead {
			e.writes.Add(1)
		}
		reg.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	t.Setenv("PACTO_INSECURE_REGISTRIES", host)
	a := &acceptE2E{ciEnv: &ciEnv{root: t.TempDir(), host: host}, repo: host + "/acme/reports"}
	a.publishReports(t, "1.6.0")
	e.repo = a.repo
	e.svc = app.NewService(oci.NewClient(authn.DefaultKeychain, oci.WithInsecureRegistries(host)), nil)
	e.watch.Store(true)
	return e
}

// revision is a fleet revision of reports read from ref.
func revision(ref string, publish *contract.Publish) *fleet.ContractRevision {
	return &fleet.ContractRevision{Contract: &contract.Contract{Service: contract.Service{Name: "reports"}, Publish: publish}, ResolvedRef: ref}
}

func TestDashboardAcceptPlanCommand(t *testing.T) {
	e := newDashboardAcceptEnv(t)
	plan := dashboardAcceptPlan(e.svc, nil)
	want, err := e.svc.AcceptPlan(t.Context(), app.AcceptOptions{Ref: e.repo, Proposed: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, rev := range []*fleet.ContractRevision{
		revision("oci://"+e.repo+"@sha256:"+strings.Repeat("a", 64), nil),
		revision("", &contract.Publish{Bundle: "oci://" + e.repo}),
		// The repository the fleet read the revision from wins over the
		// publish.bundle its contract declares.
		revision("oci://"+e.repo+":1.6.0", &contract.Publish{Bundle: "oci://" + e.repo + "-hidden"}),
	} {
		ans, err := plan(t.Context(), "reports", rev, "")
		if err != nil {
			t.Fatal(err)
		}
		if ans.Command != "pacto accept oci://"+e.repo+" --proposed --intent "+want.IntentDigest || ans.Plan == nil || ans.Refusal != nil {
			t.Errorf("answer = %+v", ans)
		}
		if p := ans.Plan; p.IntentDigest != want.IntentDigest || len(p.Ranges) != 1 || p.Ranges[0].Proposed != "^2.3.0" || p.WhoMayAccept == "" {
			t.Errorf("plan = %+v", p)
		}
	}
}

func TestDashboardAcceptNeverWrites(t *testing.T) {
	e := newDashboardAcceptEnv(t)
	plan := dashboardAcceptPlan(e.svc, func(context.Context) (app.Installation, error) { return app.Installation{}, nil })
	if _, err := plan(t.Context(), "reports", revision("oci://"+e.repo+":1.6.0", nil), ""); err != nil {
		t.Fatal(err)
	}
	// A revision naming no repository and a repository holding no version
	// are refused NotPublished, each with the command a person runs.
	for _, rev := range []*fleet.ContractRevision{revision("/srv/reports", nil), revision("oci://"+e.repo+"-none:1.0.0", nil)} {
		ans, err := plan(t.Context(), "reports", rev, "")
		if err != nil || ans.Refusal == nil || ans.Refusal.Code != "NotPublished" || ans.Refusal.Command == "" || ans.Plan != nil {
			t.Errorf("%s: %+v %v", rev.ResolvedRef, ans, err)
		}
	}
	if n := e.writes.Load(); n != 0 {
		t.Errorf("the dashboard's plan sent %d writes to the registry", n)
	}
}

func TestDashboardAcceptRegistryErrorIsReturned(t *testing.T) {
	e := newDashboardAcceptEnv(t)
	if _, err := dashboardAcceptPlan(e.svc, nil)(t.Context(), "reports", revision("oci://127.0.0.1:1/acme/reports:1.0.0", nil), ""); err == nil {
		t.Error("an unreachable registry must be an error, never an empty plan")
	}
}
