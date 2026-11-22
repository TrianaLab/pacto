package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
