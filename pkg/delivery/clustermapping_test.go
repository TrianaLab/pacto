package delivery_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

func goodMapping() delivery.ClusterGroupMapping {
	return delivery.ClusterGroupMapping{Cluster: memberUID, Hub: hubUID, Issuer: "https://login.acme.dev",
		Rules: []delivery.GroupRule{{From: "platform", To: "readers"}}, Approver: "oidc:alice@acme.com", At: "2026-11-20T10:00:00Z"}
}

func TestClusterGroupMappingValidate(t *testing.T) {
	many := make([]delivery.GroupRule, delivery.MaxGroupRules+1)
	for i := range many {
		many[i] = delivery.GroupRule{From: fmt.Sprint("g", i), To: "readers"}
	}
	cases := []struct {
		name   string
		edit   func(*delivery.ClusterGroupMapping)
		reject string
	}{
		{"good", func(*delivery.ClusterGroupMapping) {}, ""},
		{"clear", func(m *delivery.ClusterGroupMapping) { m.Rules = nil }, ""},
		{"bad uid", func(m *delivery.ClusterGroupMapping) { m.Cluster = "a/b" }, "cluster"},
		{"empty hub", func(m *delivery.ClusterGroupMapping) { m.Hub = "" }, "no hub"},
		{"empty approver", func(m *delivery.ClusterGroupMapping) { m.Approver = "" }, "no approver"},
		{"http issuer", func(m *delivery.ClusterGroupMapping) { m.Issuer = "http://login.acme.dev" }, "not an https URL"},
		{"no issuer", func(m *delivery.ClusterGroupMapping) { m.Issuer = "" }, "not an https URL"},
		{"unparsable issuer", func(m *delivery.ClusterGroupMapping) { m.Issuer = "https://%zz" }, "not an https URL"},
		{"65 rules", func(m *delivery.ClusterGroupMapping) { m.Rules = many }, "over the 64 bound"},
		{"empty from", func(m *delivery.ClusterGroupMapping) { m.Rules = []delivery.GroupRule{{To: "readers"}} }, "no hub group"},
		{"empty to", func(m *delivery.ClusterGroupMapping) { m.Rules = []delivery.GroupRule{{From: "platform"}} }, "maps to no group"},
		{"system to", func(m *delivery.ClusterGroupMapping) {
			m.Rules = []delivery.GroupRule{{From: "platform", To: "system:masters"}}
		}, "system:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := goodMapping()
			tc.edit(&m)
			err := m.Validate()
			if (tc.reject == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.reject)) {
				t.Fatalf("Validate = %v, want %q", err, tc.reject)
			}
		})
	}
}

// TestGroupMappingValidateRefusesSystemTargets: no member group starting
// with system: is ever accepted, whatever hub group maps to it.
func TestGroupMappingValidateRefusesSystemTargets(t *testing.T) {
	for _, to := range []string{"system:masters", "system:serviceaccounts", "system:nodes", "system:authenticated"} {
		m := goodMapping()
		m.Rules = append(m.Rules, delivery.GroupRule{From: "admins", To: to})
		if err := m.Validate(); err == nil {
			t.Errorf("%s accepted", to)
		}
	}
}

func TestGroupMappingApply(t *testing.T) {
	m := goodMapping()
	m.Rules = []delivery.GroupRule{{From: "platform", To: "readers"}, {From: "sre", To: "readers"}, {From: "sre", To: "oncall"}, {From: "x", To: "never"}}
	if got := m.Apply([]string{"sre", "platform", "other"}); !slices.Equal(got, []string{"oncall", "readers"}) {
		t.Fatalf("%v", got)
	}
	if got := m.Apply(nil); got == nil || len(got) != 0 {
		t.Fatalf("%#v", got)
	}
}

// signMapping signs st with a fresh key and returns the bundle and that
// key's verifier.
func signMapping(t *testing.T, st delivery.ClusterGroupMappingStatement) ([]byte, func([]byte) error) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	kp, _ := signing.FromPrivateKey(priv)
	b, err := delivery.SignClusterGroupMapping(t.Context(), signing.Keyed(kp, ""), st)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := delivery.PublicKeyPEM(kp.GetPublicKey())
	verify, _ := signing.OpenKey(delivery.TrustedKey{PublicKey: pub})
	return b, verify
}

// TestClusterGroupMappingOpenRoundTrip: a signed mapping opens under its
// key, and is refused under another key, as a join record, when Validate
// refuses it or when its subject names another issuer.
func TestClusterGroupMappingOpenRoundTrip(t *testing.T) {
	m := goodMapping()
	b, verify := signMapping(t, delivery.NewClusterGroupMappingStatement(m))
	st, err := delivery.OpenClusterGroupMapping(b, verify)
	got, _ := json.Marshal(st.Predicate)
	want, _ := json.Marshal(m)
	if err != nil || string(got) != string(want) {
		t.Fatalf("opened %s, %v", got, err)
	}
	if _, err := delivery.OpenClusterGroupMapping(b, func([]byte) error { return errors.New("no") }); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("a bad signature opened: %v", err)
	}
	if _, err := delivery.OpenClusterGroupMapping(signJoin(t, goodJoin(t))); err == nil {
		t.Fatal("a join record opened as a group mapping")
	}
	m.Rules = []delivery.GroupRule{{From: "admins", To: "system:masters"}}
	if _, err := delivery.OpenClusterGroupMapping(signMapping(t, delivery.NewClusterGroupMappingStatement(m))); err == nil || !strings.Contains(err.Error(), "system:") {
		t.Fatalf("a mapping Validate refuses opened: %v", err)
	}
	m = goodMapping()
	moved := delivery.NewClusterGroupMappingStatement(m)
	moved.Subject[0].Digest["sha256"] = strings.Repeat("0", 64)
	if _, err := delivery.OpenClusterGroupMapping(signMapping(t, moved)); err == nil || !strings.Contains(err.Error(), "subject") {
		t.Fatalf("a mapping about another issuer opened: %v", err)
	}
}
