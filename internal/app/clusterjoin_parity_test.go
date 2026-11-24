package app

import (
	"context"
	"reflect"
	"testing"

	"oras.land/oras-go/v2/registry/remote"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// TestReadJoinsSameStatesAsClusterJoins: the moved core, called as the
// operator calls it, answers the list ClusterJoins answers over the same
// registry, approved, pending and mapped members alike.
func TestReadJoinsSameStatesAsClusterJoins(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m, p := newMember(memberA, "eu-prod"), newMember(memberB, "us-prod")
	e.publish(t, m, m.key, snapNow)
	e.publish(t, p, p.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	e.pushMapping(t, "alice@acme.com", joinIssuer, m.mapping("platform=readers"))
	want := e.svc.ClusterJoins(t.Context(), e.opts())
	r := &snapshotReader{cache: &targetSnapshotCache{}, now: e.clock}
	got, repo := deliveryrecord.ReadJoins(t.Context(), deliveryrecord.JoinRead{Repository: e.repo, Hub: joinHub, Policy: e.policy, Records: &deliveryrecord.Reader{},
		Authorize: func(ctx context.Context, user, uid string) (bool, error) {
			return (&Service{}).authorizeRecord(ctx, e.authorize, user, uid, e.clock)
		},
		Fetch: func(ctx context.Context, repo *remote.Repository, ref string) ([]byte, string, error) {
			return r.fetch(ctx, repo, e.repo, ref)
		},
		Forbidden: apierrors.IsForbidden, VerifyPerson: verifyPerson, OpenKey: signing.OpenKey, Open: openTargets, Now: e.clock})
	if repo == nil || len(want.Clusters) != 2 || stateOfUID(t, want, m.uid).State != delivery.JoinApproved || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}
