package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// memberList is a hub's list: one approved member and one pending cluster.
func memberList() delivery.ClusterJoinList {
	return delivery.ClusterJoinList{Hub: delivery.ClusterRef{UID: "hub-uid"}, Repository: "ghcr.io/acme/pacto-targets", AsOf: "2026-11-20T10:00:00Z",
		Completeness: fleet.CompletenessComplete, CanApprove: true, Clusters: []delivery.ClusterJoinState{
			{UID: "9f1c4e2a", Name: "eu-prod", NameSource: fleet.ClusterNameFromApproval, State: delivery.JoinApproved},
			{UID: "77ad31b0", Name: "us-west", NameSource: fleet.ClusterNameFromNodeLabel, State: delivery.JoinPending, Reason: "no approval yet"}}}
}

func joinsOf(l delivery.ClusterJoinList, err error) func(context.Context) (delivery.ClusterJoinList, error) {
	return func(context.Context) (delivery.ClusterJoinList, error) { return l, err }
}

func TestClusterRouteServesList(t *testing.T) {
	srv := statsServer(t, Options{ClusterJoins: joinsOf(memberList(), nil)})
	code, body := fetch(t, srv, "/api/fleet/clusters")
	var got delivery.ClusterJoinList
	if err := json.Unmarshal([]byte(body), &got); code != http.StatusOK || err != nil {
		t.Fatalf("status %d, %v: %s", code, err, body)
	}
	if len(got.Clusters) != 2 || got.Clusters[1].State != delivery.JoinPending || got.Repository != "ghcr.io/acme/pacto-targets" || !got.CanApprove {
		t.Errorf("list = %+v", got)
	}
	// capabilities counts the pending row from the same list.
	_, caps := fetch(t, srv, "/api/capabilities")
	var c struct {
		Installation fleet.Installation `json:"installation"`
	}
	_ = json.Unmarshal([]byte(caps), &c)
	if p := c.Installation.Clusters.Pending; p == nil || *p != 1 || !strings.Contains(c.Installation.Clusters.Source, "registry ghcr.io/acme/pacto-targets") {
		t.Errorf("installation.clusters = %+v", c.Installation.Clusters)
	}
}

// TestClusterRouteErrorIsProblem: a list that could not be read is a 502
// problem, never an empty list, and capabilities leaves pending null.
func TestClusterRouteErrorIsProblem(t *testing.T) {
	srv := statsServer(t, Options{ClusterJoins: joinsOf(delivery.ClusterJoinList{}, errors.New("401 from ghcr.io"))})
	code, body := fetch(t, srv, "/api/fleet/clusters")
	if code != http.StatusBadGateway || !strings.Contains(body, "401 from ghcr.io") || strings.Contains(body, `"clusters"`) {
		t.Errorf("status %d: %s", code, body)
	}
	_, caps := fetch(t, srv, "/api/capabilities")
	if !strings.Contains(caps, `"pending":null`) || !strings.Contains(caps, "the member clusters could not be read: 401 from ghcr.io") {
		t.Errorf("capabilities = %s", caps)
	}
}

func TestClusterRouteAbsentWithoutProvider(t *testing.T) {
	if code, _ := fetch(t, statsServer(t, Options{}), "/api/fleet/clusters"); code != http.StatusNotFound {
		t.Errorf("status %d, want 404", code)
	}
}

func TestOpenAPIListsClusterRoute(t *testing.T) {
	raw, err := ExportOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Paths["/api/fleet/clusters"]["get"].OperationID != "get-fleet-clusters" {
		t.Errorf("paths = %v", doc.Paths["/api/fleet/clusters"])
	}
	if s := string(doc.Components.Schemas["Delivery.ClusterJoinState"]); !strings.Contains(s, `"pending","approved","removed"`) {
		t.Errorf("ClusterJoinState = %s", s)
	}
	if doc.Components.Schemas["Delivery.ClusterJoinList"] == nil {
		t.Error("no ClusterJoinList schema")
	}
}

// installOf generates "kind: <from>" for the engine it is asked, recording
// what it was asked.
func installOf(asked *string, err error) func(context.Context, string) ([]byte, error) {
	return func(_ context.Context, from string) ([]byte, error) {
		*asked = from
		return []byte("registry: ghcr.io/acme\nkind: " + from + "\n"), err
	}
}

func installServer(t *testing.T, l delivery.ClusterJoinList, listErr error, asked *string, err error) *httptest.Server {
	return statsServer(t, Options{ClusterJoins: joinsOf(l, listErr), ClusterInstall: installOf(asked, err)})
}

func TestInstallRouteAttachment(t *testing.T) {
	var asked string
	srv := installServer(t, memberList(), nil, &asked, nil)
	resp, err := http.Get(srv.URL + "/api/fleet/clusters/install?from=flux")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || asked != "flux" || string(body) != "registry: ghcr.io/acme\nkind: flux\n" {
		t.Fatalf("status %d, asked %q: %s", resp.StatusCode, asked, body)
	}
	if ct, cd := resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"); ct != "application/yaml" || cd != `attachment; filename="pacto-members-flux.yaml"` {
		t.Errorf("headers %q %q", ct, cd)
	}
	if code, _ := fetch(t, srv, "/api/fleet/clusters/install?from=capi"); code != http.StatusUnprocessableEntity {
		t.Errorf("from=capi: status %d", code)
	}
}

func TestInstallRouteForbiddenWithoutApprove(t *testing.T) {
	l := memberList()
	l.CanApprove = false
	asked := ""
	code, body := fetch(t, installServer(t, l, nil, &asked, nil), "/api/fleet/clusters/install?from=argocd")
	if code != http.StatusForbidden || asked != "" || strings.Contains(body, "registry") {
		t.Errorf("status %d, asked %q: %s", code, asked, body)
	}
	code, body = fetch(t, installServer(t, l, errors.New("401 from ghcr.io"), &asked, nil), "/api/fleet/clusters/install?from=argocd")
	if code != http.StatusBadGateway || asked != "" || !strings.Contains(body, "401 from ghcr.io") {
		t.Errorf("unread list: status %d, asked %q: %s", code, asked, body)
	}
}

// TestInstallRouteIgnoresHubURLParam: the route takes no hub URL from the
// request, so a crafted link never points members at another hub.
func TestInstallRouteIgnoresHubURLParam(t *testing.T) {
	var asked string
	code, body := fetch(t, installServer(t, memberList(), nil, &asked, nil), "/api/fleet/clusters/install?from=argocd&hubUrl=https://evil.example&hub.url=wss://evil.example")
	if code != http.StatusOK || asked != "argocd" || strings.Contains(body, "hub:") || strings.Contains(body, "evil") {
		t.Errorf("status %d: %s", code, body)
	}
}

func TestInstallRouteNeedsChartVersion(t *testing.T) {
	var asked string
	code, body := fetch(t, installServer(t, memberList(), nil, &asked, delivery.ErrNoChartVersion), "/api/fleet/clusters/install?from=argocd")
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "pacto cluster install --from argocd --chart-version") {
		t.Errorf("status %d: %s", code, body)
	}
	code, body = fetch(t, installServer(t, memberList(), nil, &asked, errors.New("no Flux kubeconfig ref was detected")), "/api/fleet/clusters/install?from=flux")
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "could not be generated: no Flux kubeconfig ref") {
		t.Errorf("status %d: %s", code, body)
	}
}

// TestInstallRouteHubReadIs502: a failure to read the hub is the upstream's,
// not the request's, so it answers 502 rather than 422.
func TestInstallRouteHubReadIs502(t *testing.T) {
	var asked string
	inner := errors.New("the candidate clusters were not read: Forbidden")
	err := HubReadError{Err: inner}
	if !errors.Is(err, inner) {
		t.Error("HubReadError hides the error it wraps")
	}
	code, body := fetch(t, installServer(t, memberList(), nil, &asked, fmt.Errorf("wrapped: %w", err)), "/api/fleet/clusters/install?from=flux")
	if code != http.StatusBadGateway || !strings.Contains(body, "the hub could not be read for the member install") || !strings.Contains(body, "Forbidden") {
		t.Errorf("status %d: %s", code, body)
	}
}

func TestInstallRouteAbsentWithoutJoins(t *testing.T) {
	var asked string
	for _, o := range []Options{{ClusterInstall: installOf(&asked, nil)}, {ClusterJoins: joinsOf(memberList(), nil)}} {
		if code, _ := fetch(t, statsServer(t, o), "/api/fleet/clusters/install?from=argocd"); code != http.StatusNotFound {
			t.Errorf("status %d, want 404", code)
		}
	}
}
