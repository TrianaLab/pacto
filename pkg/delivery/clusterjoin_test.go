package delivery_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// joinKey is the base64 PKIX DER of pub and its key id, hex sha256 of the DER.
func joinKey(t *testing.T, pub any) (string, string) {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	return base64.StdEncoding.EncodeToString(der), hex.EncodeToString(sum[:])
}

func joinSnapshot(j *delivery.SnapshotJoin) delivery.TargetSnapshot {
	return delivery.TargetSnapshot{Cluster: snapCluster, AsOf: "2026-11-17T10:00:00Z",
		Completeness: fleet.CompletenessComplete, Publisher: "pacto/pacto-operator", Join: j}
}

func TestSnapshotJoinCheck(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	key, id := joinKey(t, pub)
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ecKey, ecID := joinKey(t, &ec.PublicKey)
	good := func() *delivery.SnapshotJoin {
		return &delivery.SnapshotJoin{Name: "eu-prod", NameSource: fleet.ClusterNameFromNodeLabel, KeyID: id, PublicKey: key,
			Environments: []delivery.AdvertisedEnvironment{{Name: "production", Source: "the cluster name eu-prod"}}}
	}
	envs := func(n int) []delivery.AdvertisedEnvironment {
		out := make([]delivery.AdvertisedEnvironment, n)
		for i := range out {
			out[i] = delivery.AdvertisedEnvironment{Name: fmt.Sprintf("env-%d", i), Source: "label"}
		}
		return out
	}
	cases := []struct {
		name   string
		edit   func(*delivery.SnapshotJoin)
		reject string
	}{
		{"good block", func(*delivery.SnapshotJoin) {}, ""},
		{"no environments", func(j *delivery.SnapshotJoin) { j.Environments = nil }, ""},
		{"key id mismatch", func(j *delivery.SnapshotJoin) { j.KeyID = strings.Repeat("0", 64) }, "key id"},
		{"non-Ed25519 key", func(j *delivery.SnapshotJoin) { j.PublicKey, j.KeyID = ecKey, ecID }, "Ed25519"},
		{"bad base64", func(j *delivery.SnapshotJoin) { j.PublicKey = "%%%" }, "base64"},
		{"not a PKIX key", func(j *delivery.SnapshotJoin) { j.PublicKey = base64.StdEncoding.EncodeToString([]byte("nope")) }, "public key"},
		{"upper case name", func(j *delivery.SnapshotJoin) { j.Name = "Eu-Prod" }, "not a DNS label"},
		{"non-breaking hyphen", func(j *delivery.SnapshotJoin) { j.Name = "eu\u2011prod" }, "not a DNS label"},
		{"bidi control", func(j *delivery.SnapshotJoin) { j.Name = "eu\u202eprod" }, "not a DNS label"},
		{"shell metacharacter", func(j *delivery.SnapshotJoin) { j.Name = "eu;rm" }, "not a DNS label"},
		{"64 characters", func(j *delivery.SnapshotJoin) { j.Name = strings.Repeat("a", 64) }, "not a DNS label"},
		{"empty name", func(j *delivery.SnapshotJoin) { j.Name = "" }, "not a DNS label"},
		{"name source approval", func(j *delivery.SnapshotJoin) { j.NameSource = fleet.ClusterNameFromApproval }, "name source"},
		{"unknown name source", func(j *delivery.SnapshotJoin) { j.NameSource = "guess" }, "name source"},
		{"17 environments", func(j *delivery.SnapshotJoin) { j.Environments = envs(17) }, "environments"},
		{"16 environments", func(j *delivery.SnapshotJoin) { j.Environments = envs(16) }, ""},
		{"environment not a label", func(j *delivery.SnapshotJoin) { j.Environments[0].Name = "Prod" }, "not a DNS label"},
		{"source non-ASCII", func(j *delivery.SnapshotJoin) { j.Environments[0].Source = "the cluster name eu\u2011prod" }, "source"},
		{"source over 200", func(j *delivery.SnapshotJoin) { j.Environments[0].Source = strings.Repeat("a", 201) }, "source"},
		{"source empty", func(j *delivery.SnapshotJoin) { j.Environments[0].Source = "" }, "source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := good()
			tc.edit(j)
			err := joinSnapshot(j).Check()
			if tc.reject == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.reject) || !strings.HasPrefix(err.Error(), "join: ") {
				t.Fatalf("want a join error naming %q, got %v", tc.reject, err)
			}
		})
	}
}

// TestSnapshotJoinKeyIDIsSigningHint: the key id Check accepts is the
// fingerprint signing.Hint gives the same key.
func TestSnapshotJoinKeyIDIsSigningHint(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	key, id := joinKey(t, pub)
	j := &delivery.SnapshotJoin{Name: "eu-prod", NameSource: fleet.ClusterNameFromUID, KeyID: id, PublicKey: key}
	if err := joinSnapshot(j).Check(); err != nil {
		t.Fatal(err)
	}
	if id != signing.Hint(pub) {
		t.Fatalf("key id %s, hint %s", id, signing.Hint(pub))
	}
}

// TestSnapshotWithoutJoinReadsAsBefore: a 509-S6 snapshot opens and checks
// unchanged, with no join block.
func TestSnapshotWithoutJoinReadsAsBefore(t *testing.T) {
	b, pub := signedSnapshot(t)
	trust := &delivery.TrustConfig{Keys: []delivery.TrustedKey{{Name: "recorder", PublicKey: pub, Streams: []string{snapCluster + "/*/*"}}}}
	st, v, err := delivery.OpenTargetSnapshot(b, trust, signing.OpenKey)
	if err != nil || !v.Verified || st.Predicate.Join != nil {
		t.Fatalf("verdict %+v, join %+v, %v", v, st.Predicate.Join, err)
	}
}

func labelled(env string) delivery.PactoView {
	var p delivery.PactoView
	p.Metadata.Labels = map[string]string{delivery.EnvironmentLabel: env}
	return p
}

func TestAdvertisedEnvironments(t *testing.T) {
	pactos := []delivery.PactoView{labelled("staging"), labelled("production"), labelled("production"),
		labelled("production"), labelled("Not_A_Label"), {}}
	got := delivery.AdvertisedEnvironments(pactos, "eu-prod-staging", fleet.ClusterNameFromNodeLabel)
	want := []delivery.AdvertisedEnvironment{
		{Name: "production", Source: "label delivery.pacto.trianalab.io/environment on 3 Pactos"},
		{Name: "staging", Source: "label delivery.pacto.trianalab.io/environment on 1 Pacto"},
		{Name: "prod", Source: "the cluster name eu-prod-staging"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := delivery.AdvertisedEnvironments(nil, "useast1", fleet.ClusterNameFromNodeLabel); len(got) != 0 {
		t.Fatalf("no token in useast1: %v", got)
	}
	if got := delivery.AdvertisedEnvironments(nil, "prod-1234", fleet.ClusterNameFromUID); len(got) != 0 {
		t.Fatalf("a uid name names no environment: %v", got)
	}
	many := make([]delivery.PactoView, 0, 20)
	for i := range 20 {
		many = append(many, labelled(fmt.Sprintf("env-%02d", i)))
	}
	got = delivery.AdvertisedEnvironments(many, "dev", fleet.ClusterNameFromContext)
	if len(got) != 16 || got[15].Name != "env-15" {
		t.Fatalf("bound 16, labels first: %v", got)
	}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	key, id := joinKey(t, pub)
	if err := joinSnapshot(&delivery.SnapshotJoin{Name: "eu-prod", NameSource: fleet.ClusterNameFromNodeLabel,
		KeyID: id, PublicKey: key, Environments: got}).Check(); err != nil {
		t.Fatalf("advertised environments fail the check: %v", err)
	}
}

// TestJoinStatesNewestDecides: the newest counted record decides, a newer
// removal that verified but did not count keeps the member pending, an
// approval of another key is pending, and an approved row keeps the
// reasons of the records that did not count (R2-1, R2-2).
func TestJoinStatesNewestDecides(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	pub, id := joinKey(t, key.Public())
	_, old, _ := ed25519.GenerateKey(rand.Reader)
	oldPub, oldID := joinKey(t, old.Public())
	at := func(s int) time.Time { return time.Date(2026, 11, 20, 10, 0, s, 0, time.UTC) }
	rec := func(dec delivery.JoinDecision, kid, kpub string, s int, counted bool, why string) delivery.VerifiedJoin {
		return delivery.VerifiedJoin{Record: delivery.ClusterJoin{Cluster: "m", Hub: "h", KeyID: kid, PublicKey: kpub, Decision: dec},
			User: "oidc:alice", SignedAt: at(s), Verified: true, Counted: counted, Why: why}
	}
	cand := []delivery.JoinCandidate{{UID: "m", Join: &delivery.SnapshotJoin{Name: "m", KeyID: id, PublicKey: pub}, SelfSigned: true}}
	for name, c := range map[string]struct {
		recs  []delivery.VerifiedJoin
		state delivery.JoinState
		why   string
	}{
		"newer unchecked removal": {[]delivery.VerifiedJoin{rec(delivery.JoinApprove, id, pub, 1, true, ""), rec(delivery.JoinRemove, id, pub, 2, false, "a removal could not be checked")},
			delivery.JoinPending, "a newer removal by oidc:alice was not counted"},
		"older unchecked removal": {[]delivery.VerifiedJoin{rec(delivery.JoinRemove, id, pub, 1, false, "a removal could not be checked"), rec(delivery.JoinApprove, id, pub, 2, true, "")},
			delivery.JoinApproved, "a removal could not be checked"},
		"another key": {[]delivery.VerifiedJoin{rec(delivery.JoinApprove, oldID, oldPub, 1, true, "")}, delivery.JoinPending, "the key changed from"},
	} {
		st, _ := delivery.JoinStates("h", cand, map[string]delivery.JoinRecords{"m": {Verified: c.recs}})
		if len(st) != 1 || st[0].State != c.state || !strings.Contains(st[0].Reason, c.why) {
			t.Errorf("%s: %+v", name, st)
		}
	}
}
