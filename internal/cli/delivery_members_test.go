package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/spf13/viper"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// memberSources are the sources members holds, collected, by id.
func memberSources(t *testing.T, m delivery.Members) map[string]*fleet.Collection {
	t.Helper()
	out := map[string]*fleet.Collection{}
	for _, src := range m.Sources {
		col, err := src.Collect(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		out[src.ID()] = col
	}
	return out
}

// TestDeliveryDetectedSetsMembers: pacto delivery --detected plans over the
// hub's approved members, read from the registry the detection advertises;
// without it a plan reads none.
func TestDeliveryDetectedSetsMembers(t *testing.T) {
	e, m := approvedEnv(t, capabilities.ReferrersNative)
	cmd := newDeliveryCommand(viper.New(), e.d)
	c, _, err := deliveryClientFor(cmd, e.d, e.hub())
	expect(t, err == nil && c.Members == nil, "no --detected, no members: %v", err)
	expect(t, cmd.ParseFlags([]string{"--detected", detectedFile(t, e.repo)}) == nil, "flags")
	c, _, err = deliveryClientFor(cmd, e.d, e.hub())
	expect(t, err == nil && c.Members != nil, "members: %v", err)
	got := c.Members(t.Context(), "prod", nil)
	cols := memberSources(t, got)
	member := cols["member-"+m.uid]
	expect(t, got.Home != nil && got.Home.ID == clusterHubUID && member != nil && member.State.Status == fleet.SourceAvailable && member.Cluster.Name == "eu-prod",
		"the approved member is read on its cluster: %+v %v", got.Home, cols)
}

// TestMCPPlanAndDashboardPlanAgreeWithMember: the dashboard's delivery and
// its MCP endpoint plan over the same members, and remote MCP reads the
// hub's members only when it reads the hub (--k8s --detected k8s:).
func TestMCPPlanAndDashboardPlanAgreeWithMember(t *testing.T) {
	e, _ := approvedEnv(t, capabilities.ReferrersNative)
	d := e.d
	d.dynamicFor = func(*rest.Config) (dynamic.Interface, error) { return e.hub(), nil }
	id := identityFor(t, viper.New(), "127.0.0.1", &rest.Config{Host: "https://k"})
	svc := app.NewService(nil, nil)
	id.members = svc.PlanMembers(dashboardJoins(d, &rest.Config{}, detectedFile(t, e.repo), delivery.PolicyNamespace))
	api, err := id.delivery(httptest.NewRequest(http.MethodGet, "/", nil))
	expect(t, err == nil, "delivery: %v", err)
	dash := memberSources(t, api.(dashDelivery).act.Members(t.Context(), "prod", nil))
	mcp := memberSources(t, deliveryClientsFor(id.trust, id.policyNS, id.members, d)(e.hub()).Members(t.Context(), "prod", nil))
	expect(t, len(dash) == 1 && reflect.DeepEqual(dash, mcp), "dashboard %v, mcp %v", dash, mcp)

	cmd := newMCPCommand(svc, "test", d)
	clients, err := remoteDeliveryClients(cmd, svc, d)
	expect(t, err == nil && clients(e.hub()).Members == nil, "remote MCP reads no hub without --k8s --detected: %v", err)
	d.clusterConfig = func() (*rest.Config, error) { return &rest.Config{}, nil }
	expect(t, cmd.ParseFlags([]string{"--k8s", "--detected", "k8s:pacto-system/pacto-operator-detected"}) == nil, "flags")
	clients, err = remoteDeliveryClients(cmd, svc, d)
	expect(t, err == nil && clients(e.hub()).Members != nil, "remote MCP reads the hub's members: %v", err)
}

// TestDeliveryDetectedOnAMemberPlansAsItsPromoter: on a cluster whose
// operator advertises it is a member, pacto delivery --detected plans as the
// member's promoter does: one unread cluster, so the plan asks a person for
// any change that is not plainly non-breaking.
func TestDeliveryDetectedOnAMemberPlansAsItsPromoter(t *testing.T) {
	e, _ := approvedEnv(t, capabilities.ReferrersNative)
	b, _ := json.Marshal(detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: time.Now(), Cluster: clusterHubUID,
		Installation: &detect.Installation{Registry: "r", Targets: e.repo, Member: true}})
	path := filepath.Join(t.TempDir(), "detected.json")
	expect(t, os.WriteFile(path, b, 0o600) == nil, "write")
	cmd := newDeliveryCommand(viper.New(), e.d)
	expect(t, cmd.ParseFlags([]string{"--detected", path}) == nil, "flags")
	c, _, err := deliveryClientFor(cmd, e.d, e.hub())
	expect(t, err == nil && reflect.DeepEqual(c.Members(t.Context(), "prod", nil), delivery.MemberOnlyUnread()), "member plan: %v", err)
}
