package deliveryrecord

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

func requestDigest(n int) string { return fmt.Sprintf("sha256:%064x", n) }

// outcomeBundle is a signed raised outcome of the request numbered n on
// joinUID's request stream.
func outcomeBundle(t *testing.T, n int, state delivery.RequestState) []byte {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	o := delivery.RequestOutcome{Request: requestDigest(n), Cluster: joinUID, State: state, At: "2026-11-23T10:00:00Z",
		Operation: &delivery.RequestedOperation{Namespace: "billing", Name: "billing-prod-req-0"}, Outcome: delivery.Succeeded}
	b, err := delivery.SignRequestOutcome(t.Context(), edSigner(priv), delivery.NewRequestOutcomeStatement(o))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func pushRequest(t *testing.T, repo *remote.Repository, mode capabilities.ReferrersMode, bundle string) {
	t.Helper()
	if _, err := Push(t.Context(), repo, delivery.ClusterRequestStream(joinUID), mode, delivery.PredicateOperationRequest, []byte(bundle), nil); err != nil {
		t.Fatal(err)
	}
}

// TestPushRequestOutcomeAnchorsStream: in either form an outcome pushed to an
// empty repository anchors its member's request stream and is keyed by the
// request it answers.
func TestPushRequestOutcomeAnchorsStream(t *testing.T) {
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			repo := joinServer(t, mode, nil)
			ref, got, err := PushRequestOutcome(t.Context(), repo, outcomeBundle(t, 1, delivery.RequestRaised))
			if err != nil || got != mode || !strings.Contains(ref, "@sha256:") {
				t.Fatalf("%s %s %v", ref, got, err)
			}
			recs, cut, err := (&Reader{}).ListRequestOutcomes(t.Context(), repo, joinUID, requestDigest(1))
			if err != nil || cut || len(recs) != 1 {
				t.Fatalf("%d %v %v", len(recs), cut, err)
			}
			if recs, _, _ := (&Reader{}).ListRequestOutcomes(t.Context(), repo, joinUID, requestDigest(2)); len(recs) != 0 {
				t.Fatal("the AnnUID key did not narrow")
			}
		})
	}
}

// TestPushRequestOutcomeFailures: a bundle that is no outcome, a failed push,
// tag or probe is an error.
func TestPushRequestOutcomeFailures(t *testing.T) {
	if _, _, err := PushRequestOutcome(t.Context(), joinServer(t, capabilities.ReferrersNative, nil), []byte("{}")); err == nil {
		t.Fatal("pushed a bundle that is no outcome")
	}
	put := func(r *http.Request) bool { return r.Method == http.MethodPut }
	probe := func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/referrers/") }
	for name, fails := range map[string]func(*http.Request) bool{"push": put, "probe": probe} {
		if _, _, err := PushRequestOutcome(t.Context(), joinServer(t, capabilities.ReferrersNative, fails), outcomeBundle(t, 1, delivery.RequestRaised)); err == nil {
			t.Fatalf("%s: no error", name)
		}
	}
}

// TestListOperationRequestsRejectedLeaveTheBound: of 60 requests 50 are read
// and the listing is cut; once those are rejected the next call reads the
// other 10.
func TestListOperationRequestsRejectedLeaveTheBound(t *testing.T) {
	repo := joinServer(t, capabilities.ReferrersNative, nil)
	if _, err := EnsureAnchor(t.Context(), repo, delivery.ClusterRequestStream(joinUID)); err != nil {
		t.Fatal(err)
	}
	for i := range 60 {
		pushRequest(t, repo, capabilities.ReferrersNative, fmt.Sprintf(`{"r":%d}`, i))
	}
	r := &Reader{}
	got, cut, err := r.ListOperationRequests(t.Context(), repo, joinUID)
	if err != nil || !cut || len(got) != delivery.MaxOperationRequests {
		t.Fatalf("%d %v %v", len(got), cut, err)
	}
	first := map[string]bool{}
	for _, f := range got {
		first[f.Manifest] = true
		r.Reject(delivery.ClusterRequestStream(joinUID), delivery.PredicateOperationRequest, f.Manifest)
	}
	got, cut, err = r.ListOperationRequests(t.Context(), repo, joinUID)
	if err != nil || cut || len(got) != 10 || slices.ContainsFunc(got, func(f Fetched) bool { return first[f.Manifest] }) {
		t.Fatalf("%d %v %v", len(got), cut, err)
	}
}

// TestListOperationRequestsSkippingRejectsNothing: a hub skips what it read
// for one call only: the next page holds the rest, and a fresh call starts
// from the first page again.
func TestListOperationRequestsSkippingRejectsNothing(t *testing.T) {
	repo := joinServer(t, capabilities.ReferrersNative, nil)
	for i := range 60 {
		pushRequest(t, repo, capabilities.ReferrersNative, fmt.Sprintf(`{"r":%d}`, i))
	}
	r := &Reader{}
	skip := map[string]bool{}
	first, cut, err := r.ListOperationRequestsSkipping(t.Context(), repo, joinUID, skip)
	if err != nil || !cut || len(first) != delivery.MaxOperationRequests {
		t.Fatalf("%d %v %v", len(first), cut, err)
	}
	for _, f := range first {
		skip[f.Manifest] = true
	}
	rest, cut, err := r.ListOperationRequestsSkipping(t.Context(), repo, joinUID, skip)
	if err != nil || cut || len(rest) != 10 || slices.ContainsFunc(rest, func(f Fetched) bool { return skip[f.Manifest] }) {
		t.Fatalf("%d %v %v", len(rest), cut, err)
	}
	if again, _, _ := r.ListOperationRequests(t.Context(), repo, joinUID); len(again) != delivery.MaxOperationRequests {
		t.Fatalf("skipping rejected %d", delivery.MaxOperationRequests-len(again))
	}
}

// TestListRequestOutcomesPerRequest: outcomes of two requests on one stream
// list apart, and requests are never outcomes.
func TestListRequestOutcomesPerRequest(t *testing.T) {
	repo := joinServer(t, capabilities.ReferrersNative, nil)
	for _, n := range []int{1, 1, 2} {
		if _, _, err := PushRequestOutcome(t.Context(), repo, outcomeBundle(t, n, delivery.RequestRaised)); err != nil {
			t.Fatal(err)
		}
	}
	pushRequest(t, repo, capabilities.ReferrersNative, `{"request":1}`)
	r := &Reader{}
	one, _, _ := r.ListRequestOutcomes(t.Context(), repo, joinUID, requestDigest(1))
	two, _, _ := r.ListRequestOutcomes(t.Context(), repo, joinUID, requestDigest(2))
	reqs, _, _ := r.ListOperationRequests(t.Context(), repo, joinUID)
	if len(one) != 2 || len(two) != 1 || len(reqs) != 1 {
		t.Fatalf("one %d two %d requests %d", len(one), len(two), len(reqs))
	}
}

// TestListRequestOutcomesWriteOnceTags: where the registry serves no
// Referrers API outcomes take outcome-kind write-once tags and read back.
func TestListRequestOutcomesWriteOnceTags(t *testing.T) {
	repo := joinServer(t, capabilities.ReferrersWriteOnceTags, nil)
	if _, _, err := PushRequestOutcome(t.Context(), repo, outcomeBundle(t, 3, delivery.RequestConcluded)); err != nil {
		t.Fatal(err)
	}
	tags, err := listTags(t, repo)
	if err != nil || !slices.ContainsFunc(tags, func(s string) bool { return strings.Contains(s, "-"+KindOutcome+"-") }) {
		t.Fatalf("tags %v %v", tags, err)
	}
	got, _, err := (&Reader{}).ListRequestOutcomes(t.Context(), repo, joinUID, requestDigest(3))
	if err != nil || len(got) != 1 {
		t.Fatalf("%d %v", len(got), err)
	}
}

func listTags(t *testing.T, repo *remote.Repository) ([]string, error) {
	t.Helper()
	var out []string
	err := repo.Tags(t.Context(), "", func(tags []string) error { out = append(out, tags...); return nil })
	return out, err
}

// TestKindOfMemberRequest: requests and outcomes take their own kinds.
func TestKindOfMemberRequest(t *testing.T) {
	if kindOf(delivery.PredicateOperationRequest) != KindRequest || kindOf(delivery.PredicateRequestOutcome) != KindOutcome ||
		KindRequest != "request" || KindOutcome != "outcome" {
		t.Fatal("kinds")
	}
}

// requestBundle is a signed request on joinUID's request stream.
func requestBundle(t *testing.T) []byte {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	r := delivery.OperationRequest{Cluster: joinUID, Hub: "hub", Service: delivery.ServiceRef{Name: "billing"}, Environment: "production",
		Digest: requestDigest(7), Requester: "oidc:alice@acme.com", At: "2026-11-23T10:00:00Z", Expires: "2026-11-24T10:00:00Z",
		Placement: delivery.RequestPlacement{Source: delivery.RequestPlacedAdvertised, ConfirmedBy: "oidc:alice@acme.com"}}
	b, err := delivery.SignOperationRequest(t.Context(), edSigner(priv), delivery.NewOperationRequestStatement(r))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestPushOperationRequestAnchorsStream: in either form a request pushed to
// an empty repository anchors its member's request stream and reads back;
// a bundle that is no request, a failed push or probe is an error.
func TestPushOperationRequestAnchorsStream(t *testing.T) {
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			repo := joinServer(t, mode, nil)
			ref, got, err := PushOperationRequest(t.Context(), repo, requestBundle(t))
			if err != nil || got != mode || !strings.Contains(ref, "@sha256:") {
				t.Fatalf("%s %s %v", ref, got, err)
			}
			if recs, cut, err := (&Reader{}).ListOperationRequests(t.Context(), repo, joinUID); err != nil || cut || len(recs) != 1 {
				t.Fatalf("%d %v %v", len(recs), cut, err)
			}
		})
	}
	if _, _, err := PushOperationRequest(t.Context(), joinServer(t, capabilities.ReferrersNative, nil), []byte("{}")); err == nil {
		t.Fatal("pushed a bundle that is no request")
	}
	put := func(r *http.Request) bool { return r.Method == http.MethodPut }
	probe := func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/referrers/") }
	for name, fails := range map[string]func(*http.Request) bool{"push": put, "probe": probe} {
		if _, _, err := PushOperationRequest(t.Context(), joinServer(t, capabilities.ReferrersNative, fails), requestBundle(t)); err == nil {
			t.Fatalf("%s: no error", name)
		}
	}
}
