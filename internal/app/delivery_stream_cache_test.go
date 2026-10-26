package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
)

// TestStreamCacheSharesAVerifiedStream: a stream whose head has not moved is
// read once; a moved head reads the registry again.
func TestStreamCacheSharesAVerifiedStream(t *testing.T) {
	e := newRecordEnv(t, opFixture("ship", shipSpec(dlA), map[string]any{"phase": "Concluded", "outcome": "Succeeded", "seq": int64(2)}))
	_, r2 := e.twoRecords()
	e.d.Streams = &StreamCache{}
	if h, err := e.d.History(context.Background(), "prod", "payments-prod"); err != nil || h.Head != r2 {
		t.Fatalf("first read: %+v %v", h, err)
	}
	reads := 0
	e.d.Records = func(string, ...string) (*remote.Repository, error) { reads++; return nil, errors.New("registry down") }
	h, err := e.d.History(context.Background(), "prod", "payments-prod")
	if err != nil || h.Head != r2 || h.Source != delivery.SourceRecords || reads != 0 {
		t.Fatalf("a cached stream must not read the registry again: %+v %v reads=%d", h, err, reads)
	}
	if _, err := e.d.Blame(context.Background(), "prod", "payments-prod", ""); err != nil || reads != 0 {
		t.Fatalf("blame shares the read: %v reads=%d", err, reads)
	}
	// A new record moves the head: the cache no longer answers.
	e.setStream(map[string]any{"stream": map[string]any{"id": recStream, "seq": int64(3), "head": "sha256:other", "anchor": deliveryrecord.AnchorRef(e.host+"/delivery/streams", recStream)}})
	if h, _ = e.d.History(context.Background(), "prod", "payments-prod"); reads != 1 || h.Completeness == "complete" {
		t.Fatalf("a moved head reads again: %+v reads=%d", h, reads)
	}
}

func TestStreamCacheKeepsOnlyCleanReads(t *testing.T) {
	var c *StreamCache
	c.put("k", &streamRead{}) // a nil cache is a no-op
	if c.get("k") != nil {
		t.Fatal("a nil cache holds nothing")
	}
	c = &StreamCache{}
	c.put("", &streamRead{})
	dirty := &streamRead{RecordsRead: delivery.RecordsRead{Unreadable: []string{"x"}}}
	c.put("dirty", dirty)
	prob := &streamRead{RecordsRead: delivery.RecordsRead{Fold: delivery.Fold{Problems: []string{"gap"}}}}
	c.put("prob", prob)
	if c.get("") != nil || c.get("dirty") != nil || c.get("prob") != nil {
		t.Fatal("an unkeyed, unreadable or problem read is never kept")
	}
	c.put("ok", &streamRead{anchor: "a"})
	got := c.get("ok")
	got.Fold.Problems = append(got.Fold.Problems, "mutated")
	if again := c.get("ok"); again.anchor != "a" || len(again.Fold.Problems) != 0 {
		t.Fatalf("a caller's append must not leak into the cache: %+v", again)
	}
	for i := range maxStreams {
		c.put(fmt.Sprint(i), &streamRead{})
	}
	if c.get("ok") != nil || len(c.m) != 1 {
		t.Fatalf("a full cache starts again: %d entries", len(c.m))
	}
}

func TestStreamKey(t *testing.T) {
	d := &Delivery{}
	if d.streamKey(delivery.Stream{Anchor: "a", ID: "s"}) != "" {
		t.Error("no head, no key")
	}
	k1 := d.streamKey(delivery.Stream{Anchor: "a", ID: "s", Head: "h"})
	trust, _ := delivery.ParseTrust([]byte(testTrustPEM(t)))
	d.Trust = &trust
	if k2 := d.streamKey(delivery.Stream{Anchor: "a", ID: "s", Head: "h"}); k1 == "" || k1 == k2 {
		t.Errorf("the trust is part of the key: %q %q", k1, k2)
	}
}

func testTrustPEM(t *testing.T) string {
	t.Helper()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pemKey, err := delivery.PublicKeyPEM(pub)
	if err != nil {
		t.Fatal(err)
	}
	return pemKey
}
