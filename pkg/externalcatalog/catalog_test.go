package externalcatalog

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/externalid"
)

func mustParse(t *testing.T, y string) *File {
	t.Helper()
	f, err := ParseFile([]byte(y))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	return f
}

func validateErr(t *testing.T, y string) string {
	t.Helper()
	err := mustParse(t, y).Validate()
	if err == nil {
		t.Fatalf("Validate() = nil for:\n%s", y)
	}
	return err.Error()
}

func wantContains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%q does not mention %q", got, w)
		}
	}
}

const head = "apiVersion: catalog.pacto.trianalab.io/v1\nentries:\n"

func TestDefaultCatalogValid(t *testing.T) {
	f, err := load(defaultBytes)
	if err != nil {
		t.Fatalf("embedded default catalog: %v", err)
	}
	c := Default()
	if c.Len() != len(f.Entries) || c.Len() < 24 {
		t.Fatalf("Default().Len() = %d, file entries %d", c.Len(), len(f.Entries))
	}
	e, ref, ok := c.Entry("stripe")
	if !ok || e.Class != ClassVendorAPI || ref.Origin != OriginDefault || ref.Copy != CopyEmbedded ||
		ref.Signature != SignatureUnknown || ref.Digest != DefaultDigest() || ref.Repository != DefaultRepository {
		t.Fatalf("Entry(stripe) = %+v %+v %v", e, ref, ok)
	}
	if _, _, ok := c.Entry("nope"); ok {
		t.Fatal("Entry(nope) found")
	}
	if string(DefaultBytes()) != string(defaultBytes) {
		t.Fatal("DefaultBytes differs from the embedded bytes")
	}
}

func TestParseFileErrors(t *testing.T) {
	if _, err := ParseFile(nil); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("empty: %v", err)
	}
	if _, err := ParseFile(make([]byte, MaxFileBytes+1)); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("oversize: %v", err)
	}
	if _, err := ParseFile([]byte("entries: [")); err == nil {
		t.Error("bad yaml accepted")
	}
}

func TestValidateRejectsUnknownKey(t *testing.T) {
	for _, y := range []string{
		head + "  - identity: stripe\n    class: vendor-api\n    color: blue\n",
		head + "publish:\n  sign: true\n",
	} {
		if _, err := ParseFile([]byte(y)); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("ParseFile accepted an unknown key: %v\n%s", err, y)
		}
	}
}

func TestValidateRejectsAPIVersionClassAndIdentity(t *testing.T) {
	got := validateErr(t, "apiVersion: v0\nentries:\n  - identity: Stripe\n    class: saas\n  - identity: other-egress\n    class: legacy\n    aliases: [\" \"]\n")
	wantContains(t, got, "apiVersion", "entries[0].identity", "entries[0].class", "entries[1].identity", "reserved identity", "entries[1].aliases: empty alias")
}

func TestValidateDuplicateClaims(t *testing.T) {
	got := validateErr(t, head+
		"  - identity: stripe\n    class: vendor-api\n    aliases: [stripe]\n    hosts: [api.stripe.com]\n    keyPatterns: [STRIPE_*]\n"+
		"  - identity: stripe\n    class: vendor-api\n"+
		"  - identity: other\n    class: vendor-api\n    aliases: [Stripe]\n    hosts: [api.stripe.com]\n    keyPatterns: [STRIPE_*]\n")
	wantContains(t, got,
		"entries[1].identity: \"stripe\" duplicates entries[0]",
		"entries[2].aliases: \"Stripe\" is already claimed by entries[0]",
		"entries[2].hosts",
		"entries[2].keyPatterns")
}

func TestValidateManagedCloudTwoSegments(t *testing.T) {
	got := validateErr(t, head+"  - identity: aws/rds/1/eu-west-1/db\n    class: managed-cloud\n  - identity: aws\n    class: managed-cloud\n")
	wantContains(t, got, "entries[0].identity: a managed-cloud", "entries[1].identity: a managed-cloud")
	if err := mustParse(t, head+"  - identity: aws/rds\n    class: managed-cloud\n").Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateKeyPatternForm(t *testing.T) {
	got := validateErr(t, head+"  - identity: x\n    class: legacy\n    keyPatterns: [stripe_*, \"A**\", \"*A\", A*B]\n    hosts: [API.example.com, \"*.*.example.com\", \"*.example.com\"]\n")
	for _, bad := range []string{`"stripe_*"`, `"A**"`, `"*A"`, `"A*B"`, `"API.example.com"`, `"*.*.example.com"`} {
		wantContains(t, got, bad)
	}
	if strings.Contains(got, `"*.example.com" must`) {
		t.Errorf("a valid host pattern was refused: %s", got)
	}
}

func TestValidateTooManyEntries(t *testing.T) {
	f := &File{APIVersion: APIVersion, Entries: make([]Entry, MaxEntries+1)}
	wantContains(t, f.Validate().Error(), "over the limit")
}

const mergeDef = head +
	"  - identity: stripe\n    class: vendor-api\n    aliases: [stripe, payments]\n    hosts: [api.stripe.com, pay.example.com]\n    keyPatterns: [STRIPE_*, PAY_*]\n" +
	"  - identity: twilio\n    class: vendor-api\n    label: Twilio\n"

func TestMergeOrgReplacesEntry(t *testing.T) {
	org := mustParse(t, head+"  - identity: twilio\n    class: legacy\n    label: Our Twilio\n  - identity: corp/mq\n    class: legacy\n")
	orgRef := Ref{Origin: OriginOrganization, Digest: "sha256:org"}
	c := Merge(mustParse(t, mergeDef), DefaultRef(), org, &orgRef)
	if c.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", c.Len())
	}
	e, ref, ok := c.Entry("twilio")
	if !ok || e.Label != "Our Twilio" || e.Class != ClassLegacy || ref.Origin != OriginOrganization {
		t.Fatalf("Entry(twilio) = %+v %+v", e, ref)
	}
	if _, ref, _ := c.Entry("stripe"); ref.Origin != OriginDefault {
		t.Fatalf("stripe origin = %q", ref.Origin)
	}
}

func TestMergeOrgClaimWins(t *testing.T) {
	org := mustParse(t, head+"  - identity: corp/pay\n    class: legacy\n    aliases: [Payments]\n    hosts: [pay.example.com]\n    keyPatterns: [PAY_*]\n")
	c := Merge(mustParse(t, mergeDef), DefaultRef(), org, &Ref{Origin: OriginOrganization})
	e, _, _ := c.Entry("stripe")
	if strings.Join(e.Aliases, ",") != "stripe" || strings.Join(e.Hosts, ",") != "api.stripe.com" || strings.Join(e.KeyPatterns, ",") != "STRIPE_*" {
		t.Fatalf("default stripe kept an organization claim: %+v", e)
	}
	o, _, _ := c.Entry(externalid.ID("corp/pay"))
	if strings.Join(o.Aliases, ",") != "Payments" {
		t.Fatalf("organization entry = %+v", o)
	}
}

func TestRefsAreCopies(t *testing.T) {
	c := Merge(mustParse(t, mergeDef), DefaultRef(), mustParse(t, head), &Ref{Origin: OriginOrganization})
	refs := c.Refs()
	if len(refs) != 2 || refs[0].Origin != OriginDefault || refs[1].Origin != OriginOrganization {
		t.Fatalf("Refs() = %+v", refs)
	}
	refs[0].Origin = "mutated"
	if c.Refs()[0].Origin != OriginDefault {
		t.Fatal("Refs() exposed the catalog's slice")
	}
	if got := Merge(mustParse(t, mergeDef), DefaultRef(), nil, nil).Refs(); len(got) != 1 {
		t.Fatalf("Refs() without an extension = %+v", got)
	}
}

func TestMustDefaultPanicsOnInvalidBytes(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("mustDefault accepted an invalid catalog")
		}
	}()
	mustDefault([]byte("apiVersion: v0\n"))
}
