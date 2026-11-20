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

func TestOwnerRightsBudgetSpentLeavesUnstamped(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := ownerSignedRef(t, reg, "admin@acme.com")
	revs := []fleet.ContractRevision{passRev("1.0.0", "billing", "", "", false), passRev("2.0.0", "payments", ref, "admin@acme.com", true)}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	before, reads := reg.requests.Load(), 0
	ownerPass(reg, &reads)(ctx, revs)
	if stamp(revs[1]) != nil || reg.requests.Load() != before || reads != 0 {
		t.Fatalf("a spent budget stamped %+v or asked the registry", stamp(revs[1]))
	}
	// A trusted root that cannot be read leaves it unstamped too.
	pc := checkerFor(reg)
	pc.rootErr, pc.rootAt = os.ErrNotExist, pc.now()
	pc.ownerRights(func(context.Context, string) (*delivery.OwnerPolicy, string) { return nil, "" })(t.Context(), "oci", revs)
	if stamp(revs[1]) != nil {
		t.Fatalf("no root stamped %+v", stamp(revs[1]))
	}
}

func TestOwnerRightsNoOwnerChangeAllowedUnchanged(t *testing.T) {
	reg := provenanceTestRegistry(t)
	revs := []fleet.ContractRevision{passRev("1.0.0", "billing", "", "", false), passRev("2.0.0", "billing", "", "eve@acme.com", true)}
	before, reads := reg.requests.Load(), 0
	ownerPass(reg, &reads)(t.Context(), revs)
	if s := stamp(revs[1]); s == nil || !s.Allowed || reg.requests.Load() != before || reads != 0 {
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
