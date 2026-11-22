package productview

import (
	"fmt"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// InstallationPending fills in the cluster join requests of an
// installation answer from l, the list as its reader sees it: the pending
// rows, read from the registry, or null with why when l could not be read
// (err), hides rows or is partial.
func InstallationPending(in *fleet.InstallationInput, l delivery.ClusterJoinList, err error) {
	if err != nil {
		in.JoinSource = "the member clusters could not be read: " + err.Error()
		return
	}
	if at, err := time.Parse(time.RFC3339, l.AsOf); err == nil {
		in.JoinAsOf = &at
	}
	switch {
	case l.Hidden > 0:
		in.JoinSource = fmt.Sprintf("%d clusters you may not approve are not counted", l.Hidden)
	case l.Repository == "" && l.Completeness == fleet.CompletenessPartial: // the hub's detection could not be read
		in.JoinSource = "the member clusters were not read, so pending clusters are not counted"
	case l.Repository == "":
		n := 0
		in.JoinPending, in.JoinSource = &n, "the hub's operator sets no registry, so no cluster can join"
	case l.Completeness == fleet.CompletenessPartial:
		in.JoinSource = "registry " + l.Repository + " was read partly, so pending clusters are not counted"
	default:
		n := 0
		for _, c := range l.Clusters {
			if c.State == delivery.JoinPending {
				n++
			}
		}
		in.JoinPending, in.JoinSource = &n, "registry "+l.Repository
	}
}
