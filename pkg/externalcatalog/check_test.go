package externalcatalog

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCheckReportsDigestAndCount(t *testing.T) {
	s, err := Check(DefaultBytes(), nil)
	if err != nil || s.Digest != DefaultDigest() || s.Entries != Default().Len() {
		t.Fatalf("Check = %+v, %v", s, err)
	}
	if _, err := Check([]byte("apiVersion: v0\n"), nil); err == nil || !strings.Contains(err.Error(), "default catalog") {
		t.Fatalf("invalid default: %v", err)
	}
	if _, err := Check([]byte("entries: ["), nil); err == nil {
		t.Fatal("unparsable default accepted")
	}
}

func TestCheckExtensionMergesOverDefault(t *testing.T) {
	ext := []byte(head + "  - identity: stripe\n    class: vendor-api\n  - identity: corp/mq\n    class: legacy\n")
	s, err := Check(DefaultBytes(), ext)
	if err != nil || s.Digest != Digest(ext) || s.Entries != Default().Len()+1 {
		t.Fatalf("Check = %+v, %v", s, err)
	}
	if _, err := Check(DefaultBytes(), []byte(head+"  - identity: other-egress\n    class: legacy\n")); err == nil || !strings.Contains(err.Error(), "extension catalog") {
		t.Fatalf("invalid extension: %v", err)
	}
}

func TestPushRefusesDigestMismatch(t *testing.T) {
	ctx := context.Background()
	var gotRef string
	ok := func(_ context.Context, ref string, b []byte) (string, error) { gotRef = ref; return Digest(b), nil }
	if d, err := Push(ctx, ok, "oci://r/c:1"); err != nil || d != DefaultDigest() || gotRef != "oci://r/c:1" {
		t.Fatalf("Push = %q, %v", d, err)
	}
	wrong := func(context.Context, string, []byte) (string, error) { return "sha256:other", nil }
	if _, err := Push(ctx, wrong, "r/c:1"); err == nil || !strings.Contains(err.Error(), "embedded digest") {
		t.Fatalf("mismatch accepted: %v", err)
	}
	fail := func(context.Context, string, []byte) (string, error) { return "", errors.New("down") }
	if _, err := Push(ctx, fail, "r/c:1"); err == nil {
		t.Fatal("push error swallowed")
	}
}
