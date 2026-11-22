package deliveryrecord

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// mappingBundle is a group mapping of joinUID signed with a fresh keyed
// signer.
func mappingBundle(t *testing.T) []byte {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	m := delivery.ClusterGroupMapping{Cluster: joinUID, Hub: "hub", Issuer: "https://login.acme.dev",
		Rules: []delivery.GroupRule{{From: "platform", To: "readers"}}, Approver: "oidc:alice@acme.com", At: "2026-11-20T10:00:00Z"}
	b, err := delivery.SignClusterGroupMapping(t.Context(), edSigner(priv), delivery.NewClusterGroupMappingStatement(m))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestGroupMappingRoundTripsNativeAndWriteOnce: in either form a pushed
// group mapping anchors its member's join stream, sits under the
// group-mapping kind's tags, reads back through ListGroupMappings and is
// never read as a join record.
func TestGroupMappingRoundTripsNativeAndWriteOnce(t *testing.T) {
	stream := delivery.ClusterJoinStream(joinUID)
	if !strings.HasPrefix(KindTag(stream, KindGroupMapping, digest.FromString("x")), AnchorTag(stream)+"-group-mapping-") {
		t.Fatal("group mappings are not tagged as their kind")
	}
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			repo := joinServer(t, mode, nil)
			b := mappingBundle(t)
			ref, got, err := PushClusterGroupMapping(t.Context(), repo, b)
			if err != nil || got != mode || !strings.HasPrefix(ref, repo.Reference.Registry+"/acme/pacto-targets@sha256:") {
				t.Fatalf("%q %q %v", ref, got, err)
			}
			r := &Reader{}
			recs, cut, err := r.ListGroupMappings(t.Context(), repo, joinUID)
			if err != nil || cut || len(recs) != 1 || string(recs[0].Bundle) != string(b) {
				t.Fatalf("%d records, %v %v", len(recs), cut, err)
			}
			if joins, _, _, err := r.ListClusterJoins(t.Context(), repo, joinUID); err != nil || len(joins) != 0 {
				t.Fatalf("a group mapping read as %d join records, %v", len(joins), err)
			}
		})
	}
}

// TestPushClusterGroupMappingFailures: a bundle that is no group mapping and
// an anchor that cannot be pushed write nothing.
func TestPushClusterGroupMappingFailures(t *testing.T) {
	if _, _, err := PushClusterGroupMapping(t.Context(), joinServer(t, capabilities.ReferrersNative, nil), joinBundle(t)); err == nil {
		t.Fatal("pushed a join record as a group mapping")
	}
	put := func(r *http.Request) bool { return r.Method == http.MethodPut }
	if _, _, err := PushClusterGroupMapping(t.Context(), joinServer(t, capabilities.ReferrersNative, put), mappingBundle(t)); err == nil {
		t.Fatal("no error")
	}
}
