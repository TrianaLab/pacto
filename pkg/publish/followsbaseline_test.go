package publish

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// declBundle is deriveBundle at version carrying a declaration record that
// names baseline (repo@digest at baseVersion; "" for none).
func declBundle(host, version, baseVersion, baseline string, health bool) *contract.Bundle {
	b := deriveBundle(host, version, workloadService, health)
	d := &contract.InferredDeclaration{State: contract.DeclarationDeclared,
		Asker: contract.InferredSigner{Subject: "alice@acme.dev", Issuer: "https://idp", Class: contract.SignerClassHuman}}
	if baseline != "" {
		d.Baseline = &contract.InferredBaseline{Version: baseVersion, Bundle: baseline}
	}
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Declaration: d}
	b.FS = contract.WithInferredRecord(b.FS, rec.Marshal())
	return b
}

// declRegistry pushes a signed 1.0.0 payments bundle (no health) and
// returns the registry, its repository, the 1.0.0 digest and a verify that
// accepts what signed holds.
func declRegistry(t *testing.T) (*Client, string, string, signedSet) {
	t.Helper()
	host, reg := newRegistry(t)
	repo := host + "/pacto/payments"
	d := pushBundle(t, reg, host, "1.0.0", deriveBundle(host, "1.0.0", workloadService, false))
	return reg, repo, d, signedSet{repo + "@" + d: true}
}

func TestVerifiedBaselineSkipsUnverified(t *testing.T) {
	reg, repo, d, signed := declRegistry(t)
	host := strings.TrimSuffix(repo, "/pacto/payments")
	pushBundle(t, reg, host, "1.1.0", deriveBundle(host, "1.1.0", workloadService, false))
	var seen []string
	got, err := VerifiedBaseline(context.Background(), reg, repo, []string{"1.0.0", "1.1.0", "1.2.0-rc.1"}, func(ctx context.Context, tag, ref string) error {
		seen = append(seen, tag)
		return signed.verify(ctx, tag, ref)
	})
	if err != nil || got.Baseline != "1.0.0" || got.BaselineDigest != d || strings.Join(seen, ",") != "1.1.0,1.0.0" ||
		len(got.Notes) != 1 || got.Notes[0] != "ignored 1.1.0: no signature by the publisher identity" {
		t.Fatalf("%v %+v %v", err, got, seen)
	}
}

func TestVerifiedBaselineNotCountedNotePrintsReason(t *testing.T) {
	reg, repo, _, _ := declRegistry(t)
	host := strings.TrimSuffix(repo, "/pacto/payments")
	pushBundle(t, reg, host, "0.1.0", deriveBundle(host, "0.1.0", workloadService, false))
	why := "declaration signed by bob@acme.dev, not the asker alice@acme.dev"
	verify := func(_ context.Context, tag, _ string) error {
		if tag == "1.0.0" {
			return NotCounted{Reason: why}
		}
		return nil
	}
	d, err := VerifiedBaseline(context.Background(), reg, repo, []string{"1.0.0", "0.1.0"}, verify)
	if err != nil || d.Baseline != "0.1.0" || len(d.Notes) != 1 || d.Notes[0] != "ignored 1.0.0: "+why || d.skipped != "1.0.0" {
		t.Fatalf("%v %+v", err, d)
	}
	_, err = VerifiedBaseline(context.Background(), reg, repo, []string{"1.0.0"}, verify)
	if nc := (NotCounted{Reason: why}); !errors.Is(nc, ErrSignatureInvalid) || nc.Error() != why || err == nil || !strings.Contains(err.Error(), "no verifiable baseline in ") ||
		!strings.Contains(err.Error(), "or is a counted registry declaration") {
		t.Fatal(err)
	}
}

func TestDeriveVersionBaselineRecord(t *testing.T) {
	reg, repo, _, signed := declRegistry(t)
	host := strings.TrimSuffix(repo, "/pacto/payments")
	in := DeriveInput{Repo: repo, Local: deriveBundle(host, "", workloadService, true), Unchanged: never, Verify: signed.verify}
	if d, err := DeriveVersion(context.Background(), reg, in); err != nil || d.BaselineRecord != nil {
		t.Fatalf("%v %+v", err, d)
	}
	dd := pushBundle(t, reg, host, "1.1.0", declBundle(host, "1.1.0", "", "", false))
	signed[repo+"@"+dd] = true
	d, err := DeriveVersion(context.Background(), reg, in)
	if err != nil || d.BaselineRecord == nil || d.BaselineRecord.Declaration == nil || d.Version != "1.1.1" {
		t.Fatalf("%v %+v", err, d)
	}
}

func TestFollowsBaselineFirstDeclaration(t *testing.T) {
	host, reg := newRegistry(t)
	repo := host + "/pacto/payments"
	ctx := context.Background()
	b := declBundle(host, "0.1.0", "", "", false)
	if ok, why, err := FollowsBaseline(ctx, reg, repo, []string{"0.1.0", "x"}, "0.1.0", b, signedSet{}.verify, "0.1.0"); !ok || why != "" || err != nil {
		t.Fatalf("%v %q %v", ok, why, err)
	}
	if ok, why, _ := FollowsBaseline(ctx, reg, repo, []string{"0.2.0"}, "0.2.0", b, signedSet{}.verify, "0.1.0"); ok || !strings.Contains(why, "a first declaration is 0.1.0") {
		t.Fatalf("%v %q", ok, why)
	}
	named := declBundle(host, "0.1.0", "0.0.9", repo+"@sha256:"+strings.Repeat("a", 64), false)
	if ok, why, _ := FollowsBaseline(ctx, reg, repo, []string{"0.1.0"}, "0.1.0", named, signedSet{}.verify, "0.1.0"); ok || !strings.Contains(why, "holds no final release below 0.1.0") {
		t.Fatalf("%v %q", ok, why)
	}
}

func TestFollowsBaselineNamesNextLowerRelease(t *testing.T) {
	reg, repo, d, signed := declRegistry(t)
	host := strings.TrimSuffix(repo, "/pacto/payments")
	b := declBundle(host, "1.0.1", "1.0.0", repo+"@"+d, true) // adds a health capability: a patch
	ok, why, err := FollowsBaseline(context.Background(), reg, repo, []string{"1.0.0", "1.0.1"}, "1.0.1", b, signed.verify, "0.1.0")
	if !ok || why != "" || err != nil {
		t.Fatalf("%v %q %v", ok, why, err)
	}
	if ok, why, _ := FollowsBaseline(context.Background(), reg, repo, []string{"1.0.0"}, "1.0.1", b, signedSet{}.verify, "0.1.0"); ok || !strings.Contains(why, "counts as its baseline") {
		t.Fatalf("%v %q", ok, why)
	}
}

func TestFollowsBaselineRefusesWrongBaselineDigest(t *testing.T) {
	reg, repo, _, signed := declRegistry(t)
	host := strings.TrimSuffix(repo, "/pacto/payments")
	for _, b := range []*contract.Bundle{declBundle(host, "1.1.0", "1.0.0", repo+"@sha256:"+strings.Repeat("b", 64), true), declBundle(host, "1.1.0", "", "", true)} {
		if ok, why, err := FollowsBaseline(context.Background(), reg, repo, []string{"1.0.0"}, "1.1.0", b, signed.verify, "0.1.0"); ok || err != nil || !strings.Contains(why, "the newest counted release below 1.1.0 is 1.0.0") {
			t.Fatalf("%v %q %v", ok, why, err)
		}
	}
}

func TestFollowsBaselineRefusesVersionJump(t *testing.T) {
	reg, repo, d, signed := declRegistry(t)
	host := strings.TrimSuffix(repo, "/pacto/payments")
	b := declBundle(host, "99.0.0", "1.0.0", repo+"@"+d, true)
	ok, why, err := FollowsBaseline(context.Background(), reg, repo, []string{"1.0.0", "99.0.0"}, "99.0.0", b, signed.verify, "0.1.0")
	if ok || err != nil || why != "version 99.0.0 does not follow 1.0.0: the next version is 1.0.1" {
		t.Fatalf("%v %q %v", ok, why, err)
	}
}

func TestFollowsBaselineUnreadIsError(t *testing.T) {
	reg, repo, d, signed := declRegistry(t)
	host := strings.TrimSuffix(repo, "/pacto/payments")
	b := declBundle(host, "1.1.0", "1.0.0", repo+"@"+d, true)
	unread := func(context.Context, string, string) error { return errors.New("referrers: 503") }
	if ok, _, err := FollowsBaseline(context.Background(), reg, repo, []string{"1.0.0"}, "1.1.0", b, unread, "0.1.0"); ok || err == nil {
		t.Fatalf("%v %v", ok, err)
	}
	pullFails := brokenRegistry{reg, "pull"}
	if ok, _, err := FollowsBaseline(context.Background(), pullFails, repo, []string{"1.0.0"}, "1.1.0", b, signed.verify, "0.1.0"); ok || err == nil || !strings.Contains(err.Error(), "reading the baseline 1.0.0") {
		t.Fatalf("%v %v", ok, err)
	}
}
