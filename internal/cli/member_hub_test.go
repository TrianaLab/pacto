package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

func TestMemberHubNeedsHost(t *testing.T) {
	if _, err := newMemberHub("", nil, slog.Default()); err == nil {
		t.Fatal("a hub without an external host is refused")
	}
	v := viper.New()
	expect(t, memberHost(v) == "", "nothing configured")
	v.Set("dashboard.members-host", "pacto.acme.dev")
	expect(t, memberHost(v) == "pacto.acme.dev", "--members-host")
	v.Set("dashboard.oidc-redirect-url", "https://hub.acme.dev:8443/auth/callback")
	expect(t, memberHost(v) == "hub.acme.dev:8443", "the redirect URL's host wins: %s", memberHost(v))

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	list := func(context.Context) delivery.ClusterJoinList { return delivery.ClusterJoinList{} }
	m := dashboardMembers(context.Background(), viper.New(), list, logger)
	expect(t, m == nil && strings.Contains(logs.String(), "--members-host"), "no host: no hub, a warning: %s", logs.String())
	expect(t, dashboardMembers(context.Background(), v, nil, logger) == nil, "no join source: no hub")
}

func TestDashboardMembersExchange(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	list := func(context.Context) delivery.ClusterJoinList { return delivery.ClusterJoinList{} }
	v := viper.New()
	v.Set("dashboard.members-host", "hub.acme.dev")
	m := dashboardMembers(ctx, v, list, slog.Default())
	expect(t, m != nil && m.exchange == nil, "no issuer: no exchange, mapping only")
	v.Set("dashboard.oidc-issuer-url", "https://login.acme.dev")
	v.Set("dashboard.oidc-client-id", "dash")
	v.Set("dashboard.oidc-client-secret", "s")
	expect(t, dashboardMembers(ctx, v, list, slog.Default()).exchange != nil, "issuer and client credentials: exchange")
}

// joinOf is a list approving uid with key, complete or not.
func joinOf(complete bool, uid string, key ed25519.PublicKey, state delivery.JoinState) delivery.ClusterJoinList {
	der, _ := x509.MarshalPKIXPublicKey(key)
	l := delivery.ClusterJoinList{Completeness: fleet.CompletenessPartial, Clusters: []delivery.ClusterJoinState{{UID: uid, State: state,
		ApprovedKeyID: signing.Hint(key), ApprovedPublicKey: base64.StdEncoding.EncodeToString(der),
		Mappings: []delivery.GroupMappingState{{Issuer: "https://a", Record: "sha256:1"}}}}}
	if complete {
		l.Completeness = fleet.CompletenessComplete
	}
	return l
}

func TestApprovalsRefreshFailureEmptiesAfterTwoIntervals(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	at := time.Unix(1_800_000_000, 0)
	a := &approvals{now: func() time.Time { return at }, interval: 30 * time.Second}
	_, _, ok := a.lookup(context.Background(), "u1")
	expect(t, !ok && a.mapping("u1", "https://a") == nil, "nothing read: nobody approved")
	a.update(joinOf(true, "u1", pub, delivery.JoinApproved))
	got, keyID, ok := a.lookup(context.Background(), "u1")
	expect(t, ok && got.Equal(pub) && keyID == signing.Hint(pub), "approved: %v", ok)
	expect(t, a.mapping("u1", "https://a").Record == "sha256:1" && a.mapping("u1", "https://b") == nil, "mapping per issuer")
	unread := delivery.ClusterJoinList{Completeness: fleet.CompletenessPartial}
	at = at.Add(59 * time.Second)
	a.update(unread)
	_, _, ok = a.lookup(context.Background(), "u1")
	expect(t, ok, "a failed read keeps the list for two intervals")
	at = at.Add(time.Second)
	a.update(unread)
	_, _, ok = a.lookup(context.Background(), "u1")
	expect(t, !ok, "then empties it")
	a.update(joinOf(false, "u1", pub, delivery.JoinPending))
	_, _, ok = a.lookup(context.Background(), "u1")
	expect(t, !ok, "a pending member is not approved")
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 1024)
	der, _ := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	a.update(delivery.ClusterJoinList{Completeness: fleet.CompletenessComplete, Clusters: []delivery.ClusterJoinState{
		{UID: "u2", State: delivery.JoinApproved, ApprovedPublicKey: base64.StdEncoding.EncodeToString(der)},
		{UID: "u3", State: delivery.JoinApproved, ApprovedPublicKey: "%%"}}})
	_, _, ok2 := a.lookup(context.Background(), "u2")
	_, _, ok3 := a.lookup(context.Background(), "u3")
	expect(t, !ok2 && !ok3, "only an Ed25519 key is accepted")
}

func TestApprovalsRunStopsWithContext(t *testing.T) {
	a := &approvals{now: time.Now, interval: time.Millisecond}
	ctx, stop := context.WithCancel(context.Background())
	n := 0
	done := make(chan struct{})
	go func() {
		a.run(ctx, func(context.Context) delivery.ClusterJoinList {
			if n++; n == 3 {
				stop()
			}
			return delivery.ClusterJoinList{}
		})
		close(done)
	}()
	<-done
	expect(t, n == 3, "read once per interval until ctx ends: %d", n)
}

func TestIssuerModelAddsMappingOnlyWhenReadsUseIt(t *testing.T) {
	g := &delivery.GroupMappingState{Rules: []delivery.GroupRule{{From: "a", To: "b"}}, ApprovedBy: "oidc:alice", ApprovedAt: "not a time"}
	if got := issuerModel("iss", fleet.IssuerShared, g); got.MappedBy != "" || got.Mapping != "" {
		t.Errorf("shared issuer carries a mapping: %+v", got)
	}
	if got := issuerModel("iss", fleet.IssuerNotAccepted, nil); got.MappedBy != "" || got.Model != fleet.IssuerNotAccepted {
		t.Errorf("no mapping = %+v", got)
	}
	if got := issuerModel("iss", fleet.IssuerGroupMapping, g); got.MappedBy != "oidc:alice" || got.Mapping != "a=b" || got.MappedAt != nil {
		t.Errorf("mapping with an unreadable time = %+v", got)
	}
	// The mapping in force decides between the two: a cleared mapping no
	// longer reads as one, an approved one serves a refused issuer.
	if got := issuerModel("iss", fleet.IssuerGroupMapping, nil); got.Model != fleet.IssuerNotAccepted || got.MappedBy != "" {
		t.Errorf("cleared mapping = %+v", got)
	}
	if got := issuerModel("iss", fleet.IssuerNotAccepted, g); got.Model != fleet.IssuerGroupMapping || got.MappedBy != "oidc:alice" {
		t.Errorf("refused issuer with an approved mapping = %+v", got)
	}
}
