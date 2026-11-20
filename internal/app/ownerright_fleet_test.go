package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/signing"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// ownerPass is the fleet's owner pass over reg with the default approver
// admin@acme.com, counting the policy reads.
func ownerPass(reg *provRegistry, reads *int) func(ctx context.Context, revs []fleet.ContractRevision) {
	pass := checkerFor(reg).ownerRights(func(context.Context, string) (*delivery.OwnerPolicy, string) {
		*reads++
		return &delivery.OwnerPolicy{DefaultApprovers: []delivery.SignerRule{{Name: "platform-admins", Issuer: personIssuer, Subject: `^admin@acme\.com$`}}}, ""
	})
	return func(ctx context.Context, revs []fleet.ContractRevision) { pass(ctx, "oci", revs) }
}

// ownerSignedRef pushes a bundle signed by subject, a person, and returns its ref.
func ownerSignedRef(t *testing.T, reg *provRegistry, subject string) string {
	t.Helper()
	_, s := keylessSigstoreFor(t, sigtest.Token(subject, personIssuer))
	return pushSigned(t, reg, s)
}

// passRev is version of app owned by team, read from ref and signed by
// signer (unsigned when empty), a declaration when declared.
func passRev(version, team, ref, signer string, declared bool) fleet.ContractRevision {
	r := ownedRev(version, team, signer, delivery.ClassHuman, declared)
	r.Service, r.ServiceKey, r.ResolvedRef = "app", fleet.NewServiceKey("app"), ref
	r.Key = fleet.RevisionKey("app@sha256:" + version)
	if r.Provenance == nil {
		r.Provenance = &fleet.BundleProvenance{Signature: fleet.BundleSignature{Status: fleet.SignatureUnsigned}}
	}
	return r
}

func stamp(r fleet.ContractRevision) *fleet.OwnerRightCheck { return r.Provenance.OwnerRight }

func TestProvenanceStampsOwnerRight(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := ownerSignedRef(t, reg, "admin@acme.com")
	reads := 0
	revs := []fleet.ContractRevision{
		passRev("1.0.0", "billing", "", "", false),
		passRev("2.0.0", "payments", ref, "admin@acme.com", true),
		passRev("3.0.0", "payments", ref, "admin@acme.com", true),
	}
	ownerPass(reg, &reads)(t.Context(), revs)
	if s := stamp(revs[1]); s == nil || !s.Allowed || !strings.Contains(s.Message, "default-approver:platform-admins") {
		t.Fatalf("approver = %+v", s)
	}
	if s := stamp(revs[2]); s == nil || !s.Allowed || s.Code != "" {
		t.Fatalf("unchanged declaration = %+v", s)
	}
	if stamp(revs[0]) != nil || reads != 1 {
		t.Fatalf("plain push stamped or policy read %d times", reads)
	}
}

// TestFleetStrippedDeclarationKeysStillChecked: a person-signed bundle that
// changes the owner is checked without the declaration keys.
func TestFleetStrippedDeclarationKeysStillChecked(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := ownerSignedRef(t, reg, "eve@acme.com")
	revs := []fleet.ContractRevision{passRev("1.0.0", "billing", "", "", false), passRev("2.0.0", "payments", ref, "eve@acme.com", false)}
	reads := 0
	ownerPass(reg, &reads)(t.Context(), revs)
	if s := stamp(revs[1]); s == nil || s.Allowed || s.Code != delivery.OwnerSignerNotAllowed || !strings.Contains(s.Message, "declared as team billing") {
		t.Fatalf("stripped = %+v", s)
	}
}

// personSignedRefs pushes one bundle signed by each subject, a person,
// through one Sigstore, and returns their refs.
func personSignedRefs(t *testing.T, reg *provRegistry, subjects ...string) []string {
	t.Helper()
	ss, _ := keylessSigstoreFor(t, sigtest.Token(subjects[0], personIssuer))
	var refs []string
	for _, sub := range subjects {
		t.Setenv("SIGSTORE_ID_TOKEN", sigtest.Token(sub, personIssuer))
		s, err := signing.Keyless(ss.Fulcio, ss.TSA, "", signing.IDToken(""), ss.Root)
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, pushSigned(t, reg, s))
	}
	return refs
}

// tagRef tags ref's digest with each version in its repository.
func tagRef(t *testing.T, reg *provRegistry, ref string, versions ...string) {
	t.Helper()
	repo, digest, _ := strings.Cut(contract.TrimOCIScheme(ref), "@")
	for _, v := range versions {
		if err := reg.client.Tag(t.Context(), repo, digest, v); err != nil {
			t.Fatal(err)
		}
	}
}

// TestFleetUnreadHistoryIsUnverified: with an earlier version of the
// repository unread, the owner before an owner-setting version is unknown,
// so the fleet does not confirm a refusal of it. With no earlier version it
// is a first owner, and the refusal stands.
func TestFleetUnreadHistoryIsUnverified(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := ownerSignedRef(t, reg, "eve@acme.com")
	revs := []fleet.ContractRevision{passRev("2.0.0", "payments", ref, "eve@acme.com", false)}
	reads := 0
	ownerPass(reg, &reads)(t.Context(), revs)
	if s := stamp(revs[0]); s == nil || s.Allowed || s.Code != delivery.OwnerSignerNotAllowed || !strings.Contains(s.Message, "may set the first one") {
		t.Fatalf("first owner = %+v", s)
	}
	tagRef(t, reg, ref, "1.0.0")
	revs = []fleet.ContractRevision{passRev("2.0.0", "payments", ref, "eve@acme.com", false)}
	ownerPass(reg, &reads)(t.Context(), revs)
	if s := stamp(revs[0]); s == nil || s.Allowed || s.Code != delivery.OwnerSignerUnverified || !strings.Contains(s.Message, "the owner before it was not read") {
		t.Fatalf("unread history = %+v", s)
	}
}

// TestFleetGappedHistoryIsUnverified: the fleet read 1.0.0 and 3.0.0 but
// not 2.0.0, which may have handed the service over. An unchanged
// declaration, a refusal and a member's grant judged against 1.0.0's owner
// are unconfirmed; a default approver's grant holds; a CI publish read in
// between closes the gap.
func TestFleetGappedHistoryIsUnverified(t *testing.T) {
	reg := provenanceTestRegistry(t)
	refs := personSignedRefs(t, reg, "eve@acme.com", "ana@acme.com", "admin@acme.com")
	eve, ana, admin := refs[0], refs[1], refs[2]
	tagRef(t, reg, eve, "1.0.0", "2.0.0", "3.0.0")
	reads := 0
	for name, c := range map[string]struct {
		rev  fleet.ContractRevision
		code string
	}{
		"unchanged": {passRev("3.0.0", "payments", eve, "eve@acme.com", true), delivery.OwnerSignerUnverified},
		"refused":   {passRev("3.0.0", "billing", eve, "eve@acme.com", true), delivery.OwnerSignerUnverified},
		"member":    {passRev("3.0.0", "billing", ana, "ana@acme.com", true), delivery.OwnerSignerUnverified},
		"approver":  {passRev("3.0.0", "billing", admin, "admin@acme.com", true), ""},
	} {
		one := passRev("1.0.0", "payments", "", "", false)
		one.Owner.Contacts = []contract.OwnerContact{{Type: "email", Value: "ana@acme.com"}}
		revs := []fleet.ContractRevision{one, c.rev}
		ownerPass(reg, &reads)(t.Context(), revs)
		if s := stamp(revs[1]); s == nil || s.Code != c.code || s.Allowed != (c.code == "") {
			t.Errorf("%s = %+v", name, s)
		}
	}
	closed := []fleet.ContractRevision{passRev("1.0.0", "payments", "", "", false), passRev("2.0.0", "payments", "", "", false), passRev("3.0.0", "billing", eve, "eve@acme.com", true)}
	ownerPass(reg, &reads)(t.Context(), closed)
	if s := stamp(closed[2]); s == nil || s.Code != delivery.OwnerSignerNotAllowed {
		t.Errorf("read history = %+v", s)
	}
}

// TestFleetInferredCIPublishKeepsOwner: with no installation publisher rule
// read, a keyless CI publish is accepted by the inferred rule (status
// unknown) and keeps its pacto.yaml owner, like an unsigned push.
func TestFleetInferredCIPublishKeepsOwner(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ci := "https://github.com/acme/app/.github/workflows/ci.yml@refs/heads/main"
	revs := []fleet.ContractRevision{passRev("1.0.0", "payments", "", ci, false), passRev("2.0.0", "billing", "", ci, false)}
	for i := range revs {
		revs[i].Provenance.Signature = fleet.BundleSignature{Status: fleet.SignatureUnknown, Source: delivery.PublisherSourceInferred, Signer: ci, Issuer: personIssuer}
		revs[i].Provenance.Signers[0].Class = string(delivery.ClassCI)
	}
	reads := 0
	ownerPass(reg, &reads)(t.Context(), revs)
	if stamp(revs[0]) != nil || stamp(revs[1]) != nil || reads != 0 {
		t.Fatalf("stamped %+v %+v", stamp(revs[0]), stamp(revs[1]))
	}
}

// TestFleetBaselineLaunderingRefused: naming an earlier refused declaration
// as baseline gains nothing: the reference is the effective owner.
func TestFleetBaselineLaunderingRefused(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := ownerSignedRef(t, reg, "eve@acme.com")
	refused := passRev("2.0.0", "payments", ref, "eve@acme.com", true)
	laundered := passRev("3.0.0", "payments", ref, "eve@acme.com", true)
	laundered.Record.Declaration.Baseline = &contract.InferredBaseline{Version: "2.0.0", Bundle: contract.TrimOCIScheme(ref)}
	revs := []fleet.ContractRevision{passRev("1.0.0", "billing", "", "", false), refused, laundered}
	reads := 0
	ownerPass(reg, &reads)(t.Context(), revs)
	for _, r := range revs[1:] {
		if s := stamp(r); s == nil || s.Allowed || !strings.Contains(s.Message, "declared as team billing") {
			t.Fatalf("%s = %+v", r.Version, s)
		}
	}
}

// TestOwnerRightsBudgetSpentIsUnverified: a check the budget or the trusted
// root cut short stamps the owner unverified, never leaves it to count.
func TestOwnerRightsBudgetSpentIsUnverified(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := ownerSignedRef(t, reg, "admin@acme.com")
	revs := []fleet.ContractRevision{passRev("1.0.0", "billing", "", "", false), passRev("2.0.0", "payments", ref, "admin@acme.com", false)}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	before, reads := reg.requests.Load(), 0
	ownerPass(reg, &reads)(ctx, revs)
	if s := stamp(revs[1]); s == nil || s.Allowed || s.Code != delivery.OwnerSignerUnverified || reg.requests.Load() != before || reads != 0 {
		t.Fatalf("a spent budget stamped %+v or asked the registry", s)
	}
	// A trusted root that cannot be read stamps it unverified too.
	revs[1].Provenance.OwnerRight = nil
	pc := checkerFor(reg)
	pc.rootErr, pc.rootAt = os.ErrNotExist, pc.now()
	pc.ownerRights(func(context.Context, string) (*delivery.OwnerPolicy, string) { return nil, "" })(t.Context(), "oci", revs)
	if s := stamp(revs[1]); s == nil || s.Code != delivery.OwnerSignerUnverified {
		t.Fatalf("no root stamped %+v", s)
	}
}

func TestOwnerRightsNoOwnerChangeAllowedUnchanged(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := ownerSignedRef(t, reg, "eve@acme.com")
	revs := []fleet.ContractRevision{passRev("1.0.0", "billing", "", "", false), passRev("2.0.0", "billing", ref, "eve@acme.com", true)}
	before, reads := reg.requests.Load(), 0
	ownerPass(reg, &reads)(t.Context(), revs)
	// One tag listing says no version was left unread; no signature is read.
	if s := stamp(revs[1]); s == nil || !s.Allowed || reg.requests.Load()-before > 2 || reads != 0 {
		t.Fatalf("unchanged = %+v", s)
	}
}

func TestOwnerRightsUnsignedPlainPushUnchecked(t *testing.T) {
	reg := provenanceTestRegistry(t)
	revs := []fleet.ContractRevision{passRev("1.0.0", "billing", "", "", false), passRev("2.0.0", "payments", "", "", false)}
	revs[1].Provenance = nil
	reads := 0
	ownerPass(reg, &reads)(t.Context(), revs)
	if stamp(revs[0]) != nil || revs[1].Provenance != nil || reads != 0 {
		t.Fatalf("plain pushes stamped: %+v", revs)
	}
}

func TestOwnerRightCheckEnumMatchesDeliveryCodes(t *testing.T) {
	want := strings.Join(delivery.OwnerRightCodes, ",")
	for name, typ := range map[string]reflect.Type{"fleet.OwnerRightCheck": reflect.TypeFor[fleet.OwnerRightCheck](), "delivery.OwnerRight": reflect.TypeFor[delivery.OwnerRight]()} {
		f, _ := typ.FieldByName("Code")
		if got := f.Tag.Get("enum"); got != want {
			t.Errorf("%s.Code enum = %q, want %q", name, got, want)
		}
	}
}

func TestFleetOwnerPolicyReadsTheMount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(`{"emailIssuers":["https://idp.example"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, why := (&Service{OwnerPolicyPath: path}).fleetOwnerPolicy("")(t.Context(), "reg.io/acme/app")
	if p == nil || why != "" || p.EmailIssuers[0] != "https://idp.example" {
		t.Fatalf("%+v %q", p, why)
	}
}
