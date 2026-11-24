package delivery_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const (
	reqCluster = "9f1c-member"
	reqDigest  = "sha256:5e1f000000000000000000000000000000000000000000000000000000000000"
	reqRecord  = "sha256:aaaa000000000000000000000000000000000000000000000000000000000000"
)

var reqSigned = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

func memberRequest() delivery.OperationRequest {
	return delivery.OperationRequest{Cluster: reqCluster, Hub: "hub-uid", Service: delivery.ServiceRef{Name: "billing"},
		Environment: "production", Digest: reqDigest, Requester: "oidc:alice@acme.com",
		Placement: delivery.RequestPlacement{Members: []string{reqCluster}, Source: delivery.RequestPlacedAdvertised, ConfirmedBy: "oidc:alice@acme.com"},
		At:        reqSigned.Format(time.RFC3339), Expires: reqSigned.Add(24 * time.Hour).Format(time.RFC3339)}
}

// signedBundle signs st as a DSSE statement with a fresh key and returns
// the bundle and the key's PEM.
func signedBundle(t *testing.T, st any) ([]byte, string) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	kp, _ := signing.FromPrivateKey(priv)
	payload, _ := json.Marshal(st)
	b, err := signing.Keyed(kp, "").Sign(t.Context(), delivery.PayloadType, payload)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := delivery.PublicKeyPEM(kp.GetPublicKey())
	return b, pub
}

func requestStatement(r delivery.OperationRequest) delivery.OperationRequestStatement {
	return delivery.OperationRequestStatement{Type: delivery.StatementType, PredicateType: delivery.PredicateOperationRequest, Predicate: r,
		Subject: []delivery.Subject{{Name: delivery.ClusterRequestStream(r.Cluster), Digest: map[string]string{"sha256": strings.TrimPrefix(r.Digest, "sha256:")}}}}
}

func TestOperationRequestCheck(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(*delivery.OperationRequest)
		reject string
	}{
		{"valid", func(*delivery.OperationRequest) {}, ""},
		{"policy placement", func(r *delivery.OperationRequest) {
			r.Placement = delivery.RequestPlacement{Source: delivery.RequestPlacedPolicy, Policy: reqRecord}
		}, ""},
		{"cluster not a tag", func(r *delivery.OperationRequest) { r.Cluster = "a/b" }, "cluster"},
		{"no hub", func(r *delivery.OperationRequest) { r.Hub = "" }, "no hub"},
		{"no requester", func(r *delivery.OperationRequest) { r.Requester = "" }, "no requester"},
		{"no service", func(r *delivery.OperationRequest) { r.Service.Name = "" }, "no service"},
		{"no environment", func(r *delivery.OperationRequest) { r.Environment = "" }, "DNS label"},
		{"environment not a label", func(r *delivery.OperationRequest) { r.Environment = "Prod" }, "DNS label"},
		{"version not digest", func(r *delivery.OperationRequest) { r.Digest = "1.5.0" }, "sha256"},
		{"sha512 digest", func(r *delivery.OperationRequest) { r.Digest = "sha512:" + strings.Repeat("0", 128) }, "sha256"},
		{"short digest", func(r *delivery.OperationRequest) { r.Digest = "sha256:5e1f" }, "sha256"},
		{"unknown source", func(r *delivery.OperationRequest) { r.Placement.Source = "inferred" }, "placement source"},
		{"confirmed by another", func(r *delivery.OperationRequest) { r.Placement.ConfirmedBy = "oidc:bob@acme.com" }, "confirmed by"},
		{"bad correlation id", func(r *delivery.OperationRequest) { r.CorrelationID = "a\x1bb" }, "correlation id"},
		{"bad at", func(r *delivery.OperationRequest) { r.At = "yesterday" }, "at"},
		{"bad expires", func(r *delivery.OperationRequest) { r.Expires = "tomorrow" }, "expires"},
		{"expires before at", func(r *delivery.OperationRequest) { r.Expires = reqSigned.Add(-time.Second).Format(time.RFC3339) }, "expires"},
		{"over seven days", func(r *delivery.OperationRequest) {
			r.Expires = reqSigned.Add(delivery.MaxRequestLifetime + time.Second).Format(time.RFC3339)
		}, "expires"},
		{"exactly seven days", func(r *delivery.OperationRequest) {
			r.Expires = reqSigned.Add(delivery.MaxRequestLifetime).Format(time.RFC3339)
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := memberRequest()
			tc.edit(&r)
			err := r.Check()
			if tc.reject == "" && err != nil || tc.reject != "" && (err == nil || !strings.Contains(err.Error(), tc.reject)) {
				t.Fatalf("Check = %v, want %q", err, tc.reject)
			}
		})
	}
}

func TestOperationRequestTimely(t *testing.T) {
	r := memberRequest()
	if code, why := r.Timely(reqSigned, reqSigned.Add(time.Hour), 0); code != "" {
		t.Fatalf("in time: %s %s", code, why)
	}
	far := memberRequest()
	far.At = reqSigned.Add(6 * time.Minute).Format(time.RFC3339)
	far.Expires = reqSigned.Add(7 * time.Hour).Format(time.RFC3339)
	if code, why := far.Timely(reqSigned, reqSigned, 0); code != delivery.RefusalRequestExpired || !strings.Contains(why, "signed at") {
		t.Fatalf("at far from signedAt: %s %s", code, why)
	}
	if code, why := r.Timely(reqSigned, reqSigned, 12*time.Hour); code != delivery.RefusalRequestExpired || !strings.Contains(why, "12h0m0s") {
		t.Fatalf("expires past signedAt+ttl: %s %s", code, why)
	}
	week := memberRequest()
	week.Expires = reqSigned.Add(delivery.MaxRequestLifetime).Format(time.RFC3339)
	if code, _ := week.Timely(reqSigned, reqSigned, 0); code != "" {
		t.Fatalf("ttl 0 keeps seven days: %s", code)
	}
	if code, _ := week.Timely(reqSigned.Add(-time.Hour), reqSigned, 0); code != delivery.RefusalRequestExpired {
		t.Fatalf("signed an hour before At, lives a week from At: %s", code)
	}
	if code, why := r.Timely(reqSigned, reqSigned.Add(25*time.Hour), 0); code != delivery.RefusalRequestExpired || !strings.Contains(why, "expired") {
		t.Fatalf("past expires: %s %s", code, why)
	}
}

func TestOpenOperationRequestStrict(t *testing.T) {
	ok := func([]byte) error { return nil }
	b, _ := signedBundle(t, requestStatement(memberRequest()))
	st, err := delivery.OpenOperationRequest(b, ok)
	if err != nil || st.Predicate.Requester != "oidc:alice@acme.com" {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err := delivery.OpenOperationRequest(b, func([]byte) error { return errors.New("no") }); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatal(err)
	}
	raw := map[string]any{"_type": delivery.StatementType, "predicateType": delivery.PredicateOperationRequest, "subject": []any{},
		"predicate": map[string]any{"cluster": reqCluster, "extra": 1}}
	unknown, _ := signedBundle(t, raw)
	if _, err := delivery.OpenOperationRequest(unknown, ok); err == nil || !strings.Contains(err.Error(), "extra") {
		t.Fatalf("unknown key: %v", err)
	}
	other := requestStatement(memberRequest())
	other.PredicateType = delivery.PredicateClusterJoin
	ob, _ := signedBundle(t, other)
	if _, err := delivery.OpenOperationRequest(ob, ok); err == nil {
		t.Fatal("another predicate opened")
	}
	var env map[string]any
	_ = json.Unmarshal(b, &env)
	d := env["dsseEnvelope"].(map[string]any)
	d["signatures"] = append(d["signatures"].([]any), d["signatures"].([]any)[0])
	two, _ := json.Marshal(env)
	if _, err := delivery.OpenOperationRequest(two, ok); err == nil || !strings.Contains(err.Error(), "2 signatures") {
		t.Fatalf("two signatures: %v", err)
	}
	bad := memberRequest()
	bad.Hub = ""
	bb, _ := signedBundle(t, requestStatement(bad))
	if _, err := delivery.OpenOperationRequest(bb, ok); err == nil || !strings.Contains(err.Error(), "no hub") {
		t.Fatal(err)
	}
	moved := requestStatement(memberRequest())
	moved.Subject[0].Name = delivery.ClusterRequestStream("other")
	mb, _ := signedBundle(t, moved)
	if _, err := delivery.OpenOperationRequest(mb, ok); err == nil || !strings.Contains(err.Error(), "subject") {
		t.Fatal(err)
	}
}

func outcome(state delivery.RequestState) delivery.RequestOutcome {
	o := delivery.RequestOutcome{Request: reqRecord, Cluster: reqCluster, State: state, At: reqSigned.Format(time.RFC3339)}
	switch state {
	case delivery.RequestRefused:
		o.Code, o.Reason, o.Next = delivery.RefusalNoStage, "no Stage", "create it"
	case delivery.RequestRaised:
		o.Operation = &delivery.RequestedOperation{Namespace: "billing", Name: "billing-prod-req-aaaa"}
	case delivery.RequestConcluded:
		o.Operation, o.Outcome = &delivery.RequestedOperation{Namespace: "billing", Name: "billing-prod-req-aaaa"}, delivery.Rejected
	}
	return o
}

func TestRequestOutcomeCheck(t *testing.T) {
	cases := []struct {
		name   string
		o      func() delivery.RequestOutcome
		reject string
	}{
		{"refused", func() delivery.RequestOutcome { return outcome(delivery.RequestRefused) }, ""},
		{"raised", func() delivery.RequestOutcome { return outcome(delivery.RequestRaised) }, ""},
		{"concluded", func() delivery.RequestOutcome { return outcome(delivery.RequestConcluded) }, ""},
		{"refused without next", func() delivery.RequestOutcome { o := outcome(delivery.RequestRefused); o.Next = ""; return o }, "next"},
		{"refused without code", func() delivery.RequestOutcome { o := outcome(delivery.RequestRefused); o.Code = ""; return o }, "code"},
		{"raised without operation", func() delivery.RequestOutcome { o := outcome(delivery.RequestRaised); o.Operation = nil; return o }, "operation"},
		{"concluded without outcome", func() delivery.RequestOutcome { o := outcome(delivery.RequestConcluded); o.Outcome = ""; return o }, "outcome"},
		{"unknown state", func() delivery.RequestOutcome { o := outcome(delivery.RequestRaised); o.State = "applied"; return o }, "state"},
		{"bad cluster", func() delivery.RequestOutcome { o := outcome(delivery.RequestRaised); o.Cluster = "a b"; return o }, "cluster"},
		{"bad at", func() delivery.RequestOutcome { o := outcome(delivery.RequestRaised); o.At = "now"; return o }, "at"},
		{"bad request", func() delivery.RequestOutcome { o := outcome(delivery.RequestRaised); o.Request = "abc"; return o }, "request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.o().Check()
			if tc.reject == "" && err != nil || tc.reject != "" && (err == nil || !strings.Contains(err.Error(), tc.reject)) {
				t.Fatalf("Check = %v, want %q", err, tc.reject)
			}
		})
	}
}

// signedOutcome signs o through SignRequestOutcome and returns the bundle
// and the key's PEM.
func signedOutcome(t *testing.T, o delivery.RequestOutcome) ([]byte, string) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	kp, _ := signing.FromPrivateKey(priv)
	b, err := delivery.SignRequestOutcome(t.Context(), signing.Keyed(kp, ""), delivery.NewRequestOutcomeStatement(o))
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := delivery.PublicKeyPEM(kp.GetPublicKey())
	return b, pub
}

func TestRequestOutcomeRoundTrip(t *testing.T) {
	b, pub := signedOutcome(t, outcome(delivery.RequestConcluded))
	trust := &delivery.TrustConfig{Keys: []delivery.TrustedKey{{PublicKey: pub, Streams: []string{reqCluster + "/*/*"}}}}
	st, err := delivery.OpenRequestOutcome(b, trust, reqCluster, signing.OpenKey)
	if err != nil || st.Predicate.Outcome != delivery.Rejected || st.Predicate.Operation.Name != "billing-prod-req-aaaa" {
		t.Fatalf("%+v %v", st, err)
	}
	if st, err := delivery.OpenRequestOutcome(b, nil, "", nil); err != nil || st.Predicate.Cluster != reqCluster {
		t.Fatalf("unverified read: %v", err)
	}
	if _, err := delivery.SignRequestOutcome(t.Context(), failSigner{}, delivery.NewRequestOutcomeStatement(outcome(delivery.RequestRaised))); err == nil {
		t.Fatal("a failing signer signed")
	}
	bad := outcome(delivery.RequestRaised)
	bad.Operation = nil
	bb, _ := signedOutcome(t, bad)
	if _, err := delivery.OpenRequestOutcome(bb, nil, "", nil); err == nil {
		t.Fatal("an outcome Check refuses opened")
	}
	if _, err := delivery.OpenRequestOutcome([]byte("{"), nil, "", nil); err == nil {
		t.Fatal("garbage opened")
	}
	st2 := delivery.NewRequestOutcomeStatement(outcome(delivery.RequestRaised))
	st2.Subject[0].Name = "elsewhere"
	moved, _ := signedBundle(t, st2)
	if _, err := delivery.OpenRequestOutcome(moved, nil, "", nil); err == nil || !strings.Contains(err.Error(), "subject") {
		t.Fatal(err)
	}
}

type failSigner struct{}

func (failSigner) Sign(context.Context, string, []byte) ([]byte, error) { return nil, errors.New("no") }

func TestOpenRequestOutcomeScopedToCluster(t *testing.T) {
	b, pub := signedOutcome(t, outcome(delivery.RequestRaised))
	_, other := signedOutcome(t, outcome(delivery.RequestRaised))
	for name, k := range map[string]delivery.TrustedKey{
		"scoped to another uid": {PublicKey: pub, Streams: []string{"other-uid/*/*"}},
		"unscoped":              {PublicKey: pub},
		"with a role":           {PublicKey: pub, Streams: []string{reqCluster + "/*/*"}, Role: delivery.RoleRunner},
	} {
		if _, err := delivery.OpenRequestOutcome(b, &delivery.TrustConfig{Keys: []delivery.TrustedKey{k}}, reqCluster, signing.OpenKey); err == nil {
			t.Fatalf("%s verified", name)
		}
	}
	trust := &delivery.TrustConfig{Keys: []delivery.TrustedKey{
		{PublicKey: "not pem", Streams: []string{reqCluster + "/*/*"}},
		{PublicKey: other, Streams: []string{reqCluster + "/*/*"}},
		{PublicKey: pub, Streams: []string{reqCluster + "/pacto-targets/requests"}}}}
	if _, err := delivery.OpenRequestOutcome(b, trust, reqCluster, signing.OpenKey); err != nil {
		t.Fatalf("the scoped signer did not verify: %v", err)
	}
	other2 := outcome(delivery.RequestRaised)
	other2.Cluster = "other-uid"
	ob, opub := signedOutcome(t, other2)
	if _, err := delivery.OpenRequestOutcome(ob, &delivery.TrustConfig{Keys: []delivery.TrustedKey{{PublicKey: opub, Streams: []string{"*/*/*"}}}}, reqCluster, signing.OpenKey); err == nil ||
		!strings.Contains(err.Error(), "names cluster other-uid") {
		t.Fatalf("an outcome naming another cluster: %v", err)
	}
}

func TestClusterRequestStreamBesideJoin(t *testing.T) {
	if got := delivery.ClusterRequestStream("c1"); got != "c1/pacto-targets/requests" ||
		strings.TrimSuffix(got, "requests") != strings.TrimSuffix(delivery.ClusterJoinStream("c1"), "join") {
		t.Fatal(got)
	}
}

func TestOwnHub(t *testing.T) {
	at := func(m int) time.Time { return reqSigned.Add(time.Duration(m) * time.Minute) }
	rec := func(hub, key string, d delivery.JoinDecision, m int) delivery.VerifiedJoin {
		return delivery.VerifiedJoin{Record: delivery.ClusterJoin{Cluster: reqCluster, Hub: hub, KeyID: key, Decision: d}, SignedAt: at(m), Verified: true}
	}
	unverified := rec("hub-x", "k1", delivery.JoinApprove, 9)
	unverified.Verified = false
	otherCluster := rec("hub-y", "k1", delivery.JoinApprove, 9)
	otherCluster.Record.Cluster = "else"
	cases := []struct {
		name    string
		recs    []delivery.VerifiedJoin
		hub, in string
	}{
		{"one approving hub", []delivery.VerifiedJoin{rec("hub-a", "k1", delivery.JoinApprove, 1), unverified, otherCluster}, "hub-a", ""},
		{"none", nil, "", "no hub"},
		{"two hubs", []delivery.VerifiedJoin{rec("hub-b", "k1", delivery.JoinApprove, 1), rec("hub-a", "k1", delivery.JoinApprove, 2)}, "", "hub-a, hub-b"},
		{"another key", []delivery.VerifiedJoin{rec("hub-a", "k2", delivery.JoinApprove, 1)}, "", "no hub"},
		{"newer removal", []delivery.VerifiedJoin{rec("hub-a", "k1", delivery.JoinApprove, 1), rec("hub-a", "k1", delivery.JoinRemove, 2)}, "", "no hub"},
		{"removal on a tie", []delivery.VerifiedJoin{rec("hub-a", "k1", delivery.JoinApprove, 1), rec("hub-a", "k1", delivery.JoinRemove, 1)}, "", "no hub"},
		{"approved again", []delivery.VerifiedJoin{rec("hub-a", "k1", delivery.JoinRemove, 1), rec("hub-a", "k1", delivery.JoinApprove, 2)}, "hub-a", ""},
	}
	for _, tc := range cases {
		hub, why := delivery.OwnHub(reqCluster, "k1", tc.recs)
		if hub != tc.hub || tc.in == "" && why != "" || !strings.Contains(why, tc.in) {
			t.Errorf("%s: %q %q", tc.name, hub, why)
		}
	}
}

func named(name string) []delivery.AdvertisedEnvironment {
	return delivery.AdvertisedEnvironments(nil, name, fleet.ClusterNameFromNodeLabel)
}

func TestAllowlistDefaults(t *testing.T) {
	svc := delivery.ServiceRef{Name: "billing"}
	var a delivery.RequestAllowlist
	if code, why := a.Refusal(svc, "billing", "production", []string{"billing"}, named("eu-prod")); code != "" {
		t.Fatalf("observed namespace, hosted environment: %s", why)
	}
	if code, why := a.Refusal(svc, "other", "production", []string{"billing"}, named("eu-prod")); code != delivery.RefusalOutsideAllowlist || !strings.Contains(why, "observes") {
		t.Fatalf("unobserved: %s %s", code, why)
	}
	if code, _ := a.Refusal(svc, "anything", "prod", nil, named("eu-prod")); code != "" {
		t.Fatal("empty observed observes every namespace; prod is production")
	}
	if code, why := a.Refusal(svc, "billing", "staging", nil, named("eu-prod")); code != delivery.RefusalOutsideAllowlist || !strings.Contains(why, "staging") || !strings.Contains(why, "production") {
		t.Fatalf("eu-prod takes no staging: %s %s", code, why)
	}
	if code, _ := a.Refusal(svc, "billing", "staging", nil, named("cluster-a")); code != "" {
		t.Fatal("a name that advertises none hosts every environment")
	}
	if got := a.Hosted(named("cluster-a")); got != nil {
		t.Fatalf("every environment is nil: %v", got)
	}
}

func TestAllowlistUsesNameOnlyLikeManagedStages(t *testing.T) {
	var p delivery.PactoView
	p.Metadata.Labels = map[string]string{delivery.EnvironmentLabel: "staging"}
	pactos := []delivery.PactoView{p}
	full := delivery.AdvertisedEnvironments(pactos, "cluster-a", fleet.ClusterNameFromNodeLabel)
	if len(full) != 1 || full[0].Name != "staging" {
		t.Fatalf("the join block names the label: %v", full)
	}
	var a delivery.RequestAllowlist
	if code, why := a.Refusal(delivery.ServiceRef{Name: "billing"}, "billing", "production", nil, named("cluster-a")); code != "" ||
		len(delivery.HostedRungs([]delivery.LadderRung{{Name: "production"}}, named("cluster-a"))) != 1 {
		t.Fatalf("production is placed on cluster-a by name alone: %s", why)
	}
}

func TestAllowlistNarrowsOnly(t *testing.T) {
	svc := delivery.ServiceRef{Name: "billing"}
	a := delivery.RequestAllowlist{Namespaces: []string{"billing", "other"}}
	if code, why := a.Refusal(svc, "other", "production", []string{"billing"}, nil); code != delivery.RefusalOutsideAllowlist || !strings.Contains(why, "observes") {
		t.Fatalf("a listed namespace not observed: %s %s", code, why)
	}
	// The reason names the refused namespace and never the listed ones: a
	// hub may show it to a reader who may not read them.
	if code, why := a.Refusal(svc, "third", "production", nil, nil); code == "" || !strings.Contains(why, "allow.namespaces") || strings.Contains(why, "other") {
		t.Fatalf("an unlisted namespace: %s", why)
	}
	envs := delivery.RequestAllowlist{Environments: []string{"prod", "staging"}}
	if got := envs.Hosted(named("eu-prod")); len(got) != 1 || got[0] != "production" {
		t.Fatalf("allow.environments narrows the name's: %v", got)
	}
	if got := envs.Hosted(named("cluster-a")); len(got) != 2 {
		t.Fatalf("allow.environments narrows every environment: %v", got)
	}
	if got := (delivery.RequestAllowlist{Environments: []string{"dev"}}).Hosted(named("eu-prod")); got == nil || len(got) != 0 {
		t.Fatalf("nothing hosted is not every environment: %#v", got)
	}
	if code, _ := envs.Refusal(svc, "billing", "development", nil, named("cluster-a")); code == "" {
		t.Fatal("development is not in allow.environments")
	}
	svcs := delivery.RequestAllowlist{Services: []string{"pay*", "fraud"}}
	if code, _ := svcs.Refusal(delivery.ServiceRef{Name: "payments"}, "billing", "production", nil, nil); code != "" {
		t.Fatal("a glob match is allowed")
	}
	if code, why := svcs.Refusal(svc, "billing", "production", nil, nil); code == "" || !strings.Contains(why, "allow.services") {
		t.Fatalf("billing matches no glob: %s", why)
	}
}

func TestRefusalCodesHaveNextCommand(t *testing.T) {
	r := memberRequest()
	seen := map[string]bool{}
	for _, code := range []string{delivery.RefusalNotForThisCluster, delivery.RefusalRequestExpired, delivery.RefusalNoStage, delivery.RefusalAmbiguousStage,
		delivery.RefusalOutsideAllowlist, delivery.RefusalNotAllowedHere, delivery.RefusalNotAPerson, delivery.RefusalPointerMoved} {
		next := delivery.RefusalNext(code, r, "billing")
		if next == "" || seen[next] && code != delivery.RefusalNotAPerson || !strings.Contains(next, "pacto ") && !strings.Contains(next, "kubectl ") {
			t.Errorf("%s: %q", code, next)
		}
		seen[next] = true
	}
	if n := delivery.RefusalNext(delivery.RefusalNotAllowedHere, r, "billing"); !strings.Contains(n, "--user=oidc:alice@acme.com -n billing") {
		t.Fatal(n)
	}
}

// TestCheckMemberRequestNeedsService: a member request with no service is
// refused before anything is read.
func TestCheckMemberRequestNeedsService(t *testing.T) {
	var hr *delivery.HubRefusalError
	if err := delivery.CheckMemberRequest(delivery.Request{Environment: "prod"}, "r"); !errors.As(err, &hr) || hr.Code != delivery.RefusalStageOrMembers {
		t.Fatal(err)
	}
}
