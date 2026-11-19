package orgindex

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var (
	key    = fleet.NewServiceKeyDomain("ghcr.io/acme/contracts", "checkout")
	digest = "sha256:" + strings.Repeat("a", 64)
	bundle = "ghcr.io/acme/contracts/checkout@" + digest
)

func entry(t *testing.T) (Entry, string) {
	t.Helper()
	c := &contract.Contract{Service: contract.Service{Name: "checkout", Version: "1.2.0"},
		Dependencies: []contract.Dependency{{Name: "ledger", Ref: "oci://ghcr.io/acme/contracts/ledger:2.0.0", Compatibility: "^2.0.0"}, {Name: "auth"}}}
	tag, err := Tag(key, "1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	return NewEntry(key, c, bundle, Publisher{Key: "abc"}, "referrers=native listing=catalog"), tag
}

func TestTagIsServiceKeyHashAndVersion(t *testing.T) {
	sum := sha256.Sum256([]byte(key))
	tag, err := Tag(key, "1.2.0")
	if want := hex.EncodeToString(sum[:])[:16] + "-1.2.0"; err != nil || tag != want || Prefix(key) != want[:16] {
		t.Fatalf("Tag = %q %v, want %q", tag, err, want)
	}
}

func TestTagReplacesBuildMetadataPlus(t *testing.T) {
	if tag, _ := Tag(key, "1.5.0+build.7"); !strings.HasSuffix(tag, "-1.5.0_build.7") {
		t.Fatalf("Tag = %q", tag)
	}
}

func TestTagRefusesOverlongVersion(t *testing.T) {
	if _, err := Tag(key, "1.0.0-"+strings.Repeat("x", 120)); err == nil || !strings.Contains(err.Error(), "over the 128") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseTagRoundTrip(t *testing.T) {
	tag, _ := Tag(key, "1.5.0-rc.1+build.7")
	if p, v, ok := ParseTag(tag); !ok || p != Prefix(key) || v != "1.5.0-rc.1+build.7" {
		t.Fatalf("ParseTag = %q %q %v", p, v, ok)
	}
}

func TestParseTagIgnoresForeignTags(t *testing.T) {
	for _, tag := range []string{"latest", "1.0.0", "0123456789abcdef", "0123456789abcdef-", "0123456789abcdeX-1.0.0", "0123-1.0.0"} {
		if _, _, ok := ParseTag(tag); ok {
			t.Errorf("ParseTag(%q) ok", tag)
		}
	}
}

func TestEncodeIsDeterministic(t *testing.T) {
	e, _ := entry(t)
	a, err := Encode(e)
	b, _ := Encode(e)
	if err != nil || string(a) != string(b) {
		t.Fatalf("Encode: %s %s %v", a, b, err)
	}
	if e.Dependencies[0].Name != "auth" || !strings.HasPrefix(string(a), `{"schemaVersion":"pacto.dev/index-entry/v1","service":`) {
		t.Fatalf("entry: %s", a)
	}
}

func TestEncodeRefusesOversizedEntry(t *testing.T) {
	e, _ := entry(t)
	e.Name = strings.Repeat("x", MaxEntryBytes)
	if _, err := Encode(e); err == nil || !strings.Contains(err.Error(), "over the 65536-byte bound") {
		t.Fatalf("err = %v", err)
	}
}

func validateErr(t *testing.T, mutate func(*Entry, *string, *string), want string) {
	t.Helper()
	e, tag := entry(t)
	repo := "ghcr.io/acme/pacto-index"
	mutate(&e, &repo, &tag)
	err := e.Validate(repo, tag)
	switch {
	case want == "" && err != nil:
		t.Fatalf("Validate: %v", err)
	case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
		t.Fatalf("Validate = %v, want %q", err, want)
	}
}

func TestValidateRefusesForeignHost(t *testing.T) {
	validateErr(t, func(e *Entry, _, _ *string) { e.Bundle = "evil.example/acme/contracts/checkout@" + digest }, "not the index host ghcr.io")
}

func TestValidateRefusesBundleOutsideRoot(t *testing.T) {
	validateErr(t, func(e *Entry, _, _ *string) { e.Bundle = "ghcr.io/other/checkout@" + digest }, "outside ghcr.io/acme")
	validateErr(t, func(_ *Entry, repo, _ *string) { *repo = "ghcr.io/acme/elsewhere" }, "outside ghcr.io/acme/elsewhere")
}

func TestValidateRefusesServiceKeyMismatch(t *testing.T) {
	validateErr(t, func(e *Entry, _, _ *string) { e.Name = "ledger" }, "but its bundle")
}

func TestValidateRefusesTagMismatch(t *testing.T) {
	validateErr(t, func(_ *Entry, _, tag *string) { *tag = "0123456789abcdef-1.2.0" }, "names another service or version")
	validateErr(t, func(_ *Entry, _, tag *string) { *tag = Prefix(key) + "-9.9.9" }, "names another service or version")
	validateErr(t, func(_ *Entry, _, tag *string) { *tag = "latest" }, "names another service or version")
}

func TestValidateRefusesBundleWithoutDigest(t *testing.T) {
	for _, b := range []string{"ghcr.io/acme/contracts/checkout:1.2.0", "ghcr.io/acme/contracts/checkout@sha256:abc", "ghcr.io/acme/contracts/checkout@sha512:" + strings.Repeat("a", 64), "ghcr.io/acme/contracts/checkout@sha256:" + strings.Repeat("Z", 64)} {
		validateErr(t, func(e *Entry, _, _ *string) { e.Bundle = b }, "not pinned by a sha256 digest")
	}
}

func TestValidateAcceptsBoundEntry(t *testing.T) {
	validateErr(t, func(*Entry, *string, *string) {}, "")
	// A host-level index holds every repository on its host.
	validateErr(t, func(_ *Entry, repo, _ *string) { *repo = "ghcr.io/pacto-index" }, "")
}
