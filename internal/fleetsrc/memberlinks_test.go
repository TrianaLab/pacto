package fleetsrc

import (
	"context"
	"reflect"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestNewMemberLinkSourceMapsHubLinks(t *testing.T) {
	links := map[string]fleet.ClusterLink{"aaaa": {Channel: fleet.LinkLive}}
	src := NewMemberLinkSource(func() map[string]fleet.ClusterLink { return links })
	col, err := src.Collect(context.Background())
	if err != nil || !col.Advisory || !reflect.DeepEqual(col.ClusterLinks, links) {
		t.Errorf("collection = %+v, %v", col, err)
	}
	if src.ID() != "member-links" || src.Kind() != fleet.SourceKindMemberLinks {
		t.Errorf("source = %s/%s", src.ID(), src.Kind())
	}
}
