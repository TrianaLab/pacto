package delivery_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

var ladderSignedAt = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

const ladderDigest = "sha256:5be1000000000000000000000000000000000000000000000000000000000000"

func goodLadder() delivery.LadderRecord {
	return delivery.LadderRecord{ClusterID: "cid", ProposalDigest: ladderDigest, Dropped: []string{"qa"},
		Signer: delivery.PublisherIdentity{Issuer: "https://idp", Subject: "bob@acme.com"}, At: "2026-11-23T10:01:00Z",
		Rungs: []delivery.LadderRung{
			{Name: "staging", Source: "chart", Provenance: delivery.RungDeclared},
			{Name: "production", Upstream: "staging", Source: "chart", Provenance: delivery.RungDeclared},
			{Name: "preview", Source: "namespaces *-preview, 4 found", Provenance: delivery.RungInferred}}}
}

func TestLadderStreamShape(t *testing.T) {
	if got := delivery.LadderStream("cid"); got != "cid/pacto-delivery/ladder" {
		t.Fatalf("stream %s", got)
	}
}

func TestLadderRecordCheck(t *testing.T) {
	if err := goodLadder().Check(ladderSignedAt); err != nil {
		t.Fatal(err)
	}
	// A record that only lets Pacto create namespaces confirms no proposal.
	nsOnly := goodLadder()
	nsOnly.ProposalDigest, nsOnly.NamespacesDigest = "", ladderDigest
	if err := nsOnly.Check(ladderSignedAt); err != nil {
		t.Fatal(err)
	}
	many := func(prov string, upstream bool) []delivery.LadderRung {
		var rs []delivery.LadderRung
		for i := range 17 {
			r := delivery.LadderRung{Name: "r" + string(rune('a'+i)), Provenance: prov}
			if upstream && i > 0 {
				r.Upstream = rs[i-1].Name
			}
			rs = append(rs, r)
		}
		return rs
	}
	cases := map[string]struct {
		edit func(*delivery.LadderRecord)
		want string
	}{
		"no cluster":       {func(l *delivery.LadderRecord) { l.ClusterID = "" }, "no cluster"},
		"no signer":        {func(l *delivery.LadderRecord) { l.Signer.Subject = "" }, "no signer"},
		"no rung":          {func(l *delivery.LadderRecord) { l.Rungs = nil }, "no rung"},
		"bad digest":       {func(l *delivery.LadderRecord) { l.ProposalDigest = "5be1" }, "proposal digest"},
		"no proposal":      {func(l *delivery.LadderRecord) { l.ProposalDigest = "" }, "proposal digest"},
		"bad namespaces":   {func(l *delivery.LadderRecord) { l.NamespacesDigest = "5be1" }, "namespaces digest"},
		"not a DNS label":  {func(l *delivery.LadderRecord) { l.Rungs[2].Name = "Preview" }, "DNS label"},
		"named twice":      {func(l *delivery.LadderRecord) { l.Rungs[2].Name = "staging" }, "named twice"},
		"declared after":   {func(l *delivery.LadderRecord) { l.Rungs[0], l.Rungs[2] = l.Rungs[2], l.Rungs[0] }, "follows a kept rung"},
		"broken chain":     {func(l *delivery.LadderRecord) { l.Rungs[1].Upstream = "" }, "rung before it"},
		"chain head fed":   {func(l *delivery.LadderRecord) { l.Rungs[0].Upstream = "dev" }, "rung before it"},
		"unknown prov":     {func(l *delivery.LadderRecord) { l.Rungs[2].Provenance = "observed" }, "provenance"},
		"kept not a leaf":  {func(l *delivery.LadderRecord) { l.Rungs[2].Upstream = "production" }, "always a leaf"},
		"too many kept":    {func(l *delivery.LadderRecord) { l.Rungs = many(delivery.RungInferred, false) }, "bound"},
		"too many chained": {func(l *delivery.LadderRecord) { l.Rungs = many(delivery.RungDeclared, true) }, "bound"},
		"dropped kept":     {func(l *delivery.LadderRecord) { l.Dropped = []string{"preview"} }, "also kept"},
		"dropped label":    {func(l *delivery.LadderRecord) { l.Dropped = []string{"QA"} }, "DNS label"},
		"dropped too many": {func(l *delivery.LadderRecord) { l.Dropped = make([]string, 17) }, "bound"},
		"at not RFC 3339":  {func(l *delivery.LadderRecord) { l.At = "yesterday" }, "RFC 3339"},
		"at in the future": {func(l *delivery.LadderRecord) { l.At = "2029-11-23T10:00:00Z" }, "more than 5m0s"},
		"at in the past":   {func(l *delivery.LadderRecord) { l.At = "2026-11-23T09:54:00Z" }, "more than 5m0s"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			l := goodLadder()
			c.edit(&l)
			if err := l.Check(ladderSignedAt); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}
}

// signLadder signs st (any statement) with a fresh key and returns the bundle
// and a verifier that reports ladderSignedAt.
func signLadder(t *testing.T, st any) ([]byte, func([]byte) (time.Time, error)) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	kp, _ := signing.FromPrivateKey(priv)
	payload, _ := json.Marshal(st)
	b, err := signing.Keyed(kp, "").Sign(t.Context(), delivery.PayloadType, payload)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := delivery.PublicKeyPEM(kp.GetPublicKey())
	verify, _ := signing.OpenKey(delivery.TrustedKey{PublicKey: pub})
	return b, func(b []byte) (time.Time, error) { return ladderSignedAt, verify(b) }
}

func ladderStatement(l delivery.LadderRecord) map[string]any {
	sum := sha256.Sum256([]byte(delivery.LadderStream(l.ClusterID)))
	return map[string]any{"_type": delivery.StatementType, "predicateType": delivery.PredicateEnvironmentLadder, "predicate": l,
		"subject": []delivery.Subject{{Name: "ladder", Digest: map[string]string{"sha256": hex.EncodeToString(sum[:])}}}}
}

func TestOpenLadderRecordStrict(t *testing.T) {
	l := goodLadder()
	b, verify := signLadder(t, ladderStatement(l))
	st, at, err := delivery.OpenLadderRecord(b, verify)
	if err != nil || st.Predicate.ClusterID != "cid" || len(st.Predicate.Rungs) != 3 || !at.Equal(ladderSignedAt) {
		t.Fatalf("opened %+v %v %v", st.Predicate, at, err)
	}
	// An unknown key in the predicate is refused.
	extra := ladderStatement(l)
	raw, _ := json.Marshal(l)
	var pred map[string]any
	_ = json.Unmarshal(raw, &pred)
	pred["groups"] = []string{"admins"}
	extra["predicate"] = pred
	b2, verify2 := signLadder(t, extra)
	if _, _, err := delivery.OpenLadderRecord(b2, verify2); err == nil || !strings.Contains(err.Error(), "groups") {
		t.Fatalf("unknown key: %v", err)
	}
	// Another predicate, a failed signature, a record Check refuses and a
	// subject of another cluster's stream are refused.
	wrong := ladderStatement(l)
	wrong["predicateType"] = delivery.PredicateClusterJoin
	b3, verify3 := signLadder(t, wrong)
	if _, _, err := delivery.OpenLadderRecord(b3, verify3); err == nil {
		t.Fatal("another predicate opened")
	}
	if _, _, err := delivery.OpenLadderRecord(b, func([]byte) (time.Time, error) { return time.Time{}, errors.New("no") }); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("signature: %v", err)
	}
	bad := l
	bad.At = "2029-01-01T00:00:00Z"
	b4, verify4 := signLadder(t, ladderStatement(bad))
	if _, _, err := delivery.OpenLadderRecord(b4, verify4); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("future at: %v", err)
	}
	other := ladderStatement(l)
	other["subject"] = ladderStatement(delivery.LadderRecord{ClusterID: "other"})["subject"]
	b5, verify5 := signLadder(t, other)
	if _, _, err := delivery.OpenLadderRecord(b5, verify5); err == nil || !strings.Contains(err.Error(), "subject") {
		t.Fatalf("subject: %v", err)
	}
}

func verified(user string, at time.Time, edit ...func(*delivery.LadderRecord)) delivery.VerifiedLadder {
	l := goodLadder()
	for _, f := range edit {
		f(&l)
	}
	return delivery.VerifiedLadder{Record: l, User: user, Ref: "reg/approvals@" + user, SignedAt: at}
}

var chart = []string{"staging", "production"}

func TestConfirmedLadderNoRecord(t *testing.T) {
	if rungs, from, why := delivery.ConfirmedLadder(nil, "cid", chart); rungs != nil || from != nil || why != "" {
		t.Fatalf("%+v %+v %q", rungs, from, why)
	}
}

func TestConfirmedLadderReadsKeptRungsAsConfirmedLeaves(t *testing.T) {
	rungs, from, why := delivery.ConfirmedLadder([]delivery.VerifiedLadder{verified("oidc:bob@acme.com", ladderSignedAt)}, "cid", chart)
	want := delivery.LadderRung{Name: "preview", Source: "namespaces *-preview, 4 found", Provenance: delivery.RungConfirmed}
	if len(rungs) != 1 || rungs[0] != want || from.User != "oidc:bob@acme.com" || why != "" {
		t.Fatalf("%+v %+v %q", rungs, from, why)
	}
}

// TestOlderLadderRecordIgnored: the newest by verified signing time wins,
// whatever order they come in and whatever At claims: a replayed older
// record never rolls the ladder back.
func TestOlderLadderRecordIgnored(t *testing.T) {
	newer := verified("oidc:bob@acme.com", ladderSignedAt.Add(time.Hour), func(l *delivery.LadderRecord) {
		l.Rungs = l.Rungs[:2]
		l.Dropped = []string{"preview"}
	})
	older := verified("oidc:eve@acme.com", ladderSignedAt, func(l *delivery.LadderRecord) { l.At = "2026-11-24T10:00:00Z" })
	for _, recs := range [][]delivery.VerifiedLadder{{newer, older}, {older, newer}} {
		rungs, from, _ := delivery.ConfirmedLadder(recs, "cid", chart)
		if len(rungs) != 0 || from.User != "oidc:bob@acme.com" {
			t.Fatalf("%+v %+v", rungs, from)
		}
	}
}

// TestFutureDatedLadderRecordIgnored: a record whose At is years ahead of its
// verified signing time never opens, so it never reaches ConfirmedLadder; a
// counted one is ordered by SignedAt alone.
func TestFutureDatedLadderRecordIgnored(t *testing.T) {
	l := goodLadder()
	l.At = "2031-01-01T00:00:00Z"
	b, verify := signLadder(t, ladderStatement(l))
	if _, _, err := delivery.OpenLadderRecord(b, verify); err == nil {
		t.Fatal("a future-dated record opened")
	}
}

func TestLadderRecordForAnotherClusterIgnored(t *testing.T) {
	other := verified("oidc:eve@acme.com", ladderSignedAt.Add(time.Hour), func(l *delivery.LadderRecord) { l.ClusterID = "other" })
	if rungs, from, _ := delivery.ConfirmedLadder([]delivery.VerifiedLadder{other}, "cid", chart); rungs != nil || from != nil {
		t.Fatalf("%+v %+v", rungs, from)
	}
}

// TestStaleDeclaredPrefixDropsRecord: the chart chain changed since signing.
// The record's chain is ignored in favour of the chart, its kept leaves stay,
// and a kept rung the chart now declares (or a synonym of one) is dropped.
func TestStaleDeclaredPrefixDropsRecord(t *testing.T) {
	rec := verified("oidc:bob@acme.com", ladderSignedAt, func(l *delivery.LadderRecord) {
		l.Rungs = append(l.Rungs, delivery.LadderRung{Name: "dev", Source: "the cluster name eu-dev", Provenance: delivery.RungInferred})
	})
	rungs, _, why := delivery.ConfirmedLadder([]delivery.VerifiedLadder{rec}, "cid", []string{"development", "staging", "production"})
	if len(rungs) != 1 || rungs[0].Name != "preview" {
		t.Fatalf("rungs %+v", rungs)
	}
	if want := "the installation ladder oidc:bob@acme.com confirmed was staging -> production; the chart value delivery.environments.ladder now names development -> staging -> production, which wins"; why != want {
		t.Fatalf("why %q", why)
	}
	none := verified("oidc:bob@acme.com", ladderSignedAt, func(l *delivery.LadderRecord) { l.Rungs = l.Rungs[2:] })
	if _, _, why := delivery.ConfirmedLadder([]delivery.VerifiedLadder{none}, "cid", chart); !strings.Contains(why, "was empty") {
		t.Fatalf("why %q", why)
	}
}

// TestSignLadderOpens: a record signed through NewLadderStatement and
// SignLadder opens as itself, and a signer that fails signs nothing.
func TestSignLadderOpens(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	kp, _ := signing.FromPrivateKey(priv)
	b, err := delivery.SignLadder(t.Context(), signing.Keyed(kp, ""), delivery.NewLadderStatement(goodLadder()))
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := delivery.PublicKeyPEM(kp.GetPublicKey())
	verify, _ := signing.OpenKey(delivery.TrustedKey{PublicKey: pub})
	st, _, err := delivery.OpenLadderRecord(b, func(b []byte) (time.Time, error) { return ladderSignedAt, verify(b) })
	if err != nil || st.Predicate.ClusterID != "cid" || len(st.Predicate.Rungs) != 3 {
		t.Fatalf("%+v %v", st.Predicate, err)
	}
	if _, err := delivery.SignLadder(t.Context(), downSigner{}, delivery.NewLadderStatement(goodLadder())); err == nil {
		t.Fatal("a failing signer signed")
	}
}

// downSigner is a signer that cannot sign.
type downSigner struct{}

func (downSigner) Sign(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("down")
}
