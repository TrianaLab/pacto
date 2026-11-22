package productview

import (
	"errors"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// joinList is a hub's list with one row in each state.
func joinList() delivery.ClusterJoinList {
	return delivery.ClusterJoinList{Repository: "ghcr.io/acme/pacto-targets", AsOf: "2026-11-20T10:00:00Z", Completeness: fleet.CompletenessComplete,
		Clusters: []delivery.ClusterJoinState{{UID: "a", State: delivery.JoinApproved}, {UID: "p", State: delivery.JoinPending}, {UID: "r", State: delivery.JoinRemoved}}}
}

// TestInstallationPendingNullWhenHidden: pending clusters are counted only
// from a list that hides none and was read whole.
func TestInstallationPendingNullWhenHidden(t *testing.T) {
	var in fleet.InstallationInput
	InstallationPending(&in, joinList(), nil)
	if in.JoinPending == nil || *in.JoinPending != 1 || in.JoinSource != "registry ghcr.io/acme/pacto-targets" || in.JoinAsOf == nil {
		t.Errorf("visible list = %+v", in)
	}
	hidden := joinList()
	hidden.Hidden = 2
	in = fleet.InstallationInput{}
	InstallationPending(&in, hidden, nil)
	if in.JoinPending != nil || in.JoinSource != "2 clusters you may not approve are not counted" {
		t.Errorf("hidden rows = %+v", in)
	}
	partial := joinList()
	partial.Completeness, partial.AsOf = fleet.CompletenessPartial, "not a time"
	in = fleet.InstallationInput{}
	InstallationPending(&in, partial, nil)
	if in.JoinPending != nil || in.JoinAsOf != nil || !strings.Contains(in.JoinSource, "read partly") {
		t.Errorf("partial list = %+v", in)
	}
	in = fleet.InstallationInput{}
	InstallationPending(&in, delivery.ClusterJoinList{}, errors.New("401"))
	if in.JoinPending != nil || in.JoinSource != "the member clusters could not be read: 401" {
		t.Errorf("unread list = %+v", in)
	}
}
