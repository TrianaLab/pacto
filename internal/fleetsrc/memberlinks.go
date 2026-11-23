package fleetsrc

import (
	"context"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// memberLinkSource carries the hub's member link facts into a snapshot. It
// is advisory and listed nowhere: Build only copies its links onto the
// cluster records.
type memberLinkSource struct {
	links func() map[string]fleet.ClusterLink
}

// NewMemberLinkSource is the source of the link facts links returns at each
// build, by cluster id.
func NewMemberLinkSource(links func() map[string]fleet.ClusterLink) fleet.Source {
	return memberLinkSource{links: links}
}

func (memberLinkSource) ID() string   { return "member-links" }
func (memberLinkSource) Kind() string { return fleet.SourceKindMemberLinks }

// Collect never fails.
func (s memberLinkSource) Collect(context.Context) (*fleet.Collection, error) {
	return &fleet.Collection{Advisory: true, ClusterLinks: s.links()}, nil
}
