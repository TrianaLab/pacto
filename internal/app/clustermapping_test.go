package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const mapIssuer = "https://login.acme.dev"

// mapping is a group mapping of m for mapIssuer approved as alice, rules
// from=to pairs.
func (m member) mapping(rules ...string) delivery.ClusterGroupMapping {
	g := delivery.ClusterGroupMapping{Cluster: m.uid, Hub: joinHub, Issuer: mapIssuer, Approver: "oidc:alice@acme.com", At: "2026-11-20T10:00:00Z"}
	for _, r := range rules {
		from, to, _ := strings.Cut(r, "=")
		g.Rules = append(g.Rules, delivery.GroupRule{From: from, To: to})
	}
	return g
}

// pushMapping signs g as email from issuer and writes it on g's member's
// join stream, then moves the timestamp authority a second on, so the next
// record is signed later.
func (e *joinEnv) pushMapping(t *testing.T, email, issuer string, g delivery.ClusterGroupMapping) {
	t.Helper()
	s, err := signing.Keyless(e.ss.Fulcio, e.ss.TSA, "", func(context.Context) (string, error) { return sigtest.Token(email, issuer), nil }, e.ss.Root)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(delivery.NewClusterGroupMappingStatement(g))
	b, err := s.Sign(t.Context(), delivery.PayloadType, payload)
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := ocireferrer.Open(e.repo, ocireferrer.Options{})
	stream := delivery.ClusterJoinStream(g.Cluster)
	if err := ocireferrer.Push(t.Context(), repo, deliveryrecord.Anchor(stream)); err != nil {
		t.Fatal(err)
	}
	if _, err := deliveryrecord.Push(t.Context(), repo, stream, e.mode, delivery.PredicateClusterGroupMapping, b, nil); err != nil {
		t.Fatal(err)
	}
	e.ss.Advance(time.Second)
}

// approvedMember publishes and approves eu-prod as alice.
func (e *joinEnv) approvedMember(t *testing.T) member {
	t.Helper()
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	return m
}

func (e *joinEnv) mappingsOf(t *testing.T, m member) ([]delivery.GroupMappingState, delivery.ClusterJoinList) {
	t.Helper()
	l := e.svc.ClusterJoins(t.Context(), e.opts())
	return stateOfUID(t, l, m.uid).Mappings, l
}

func TestApprovedMemberShowsItsMapping(t *testing.T) {
	for _, mode := range bothModes {
		t.Run(string(mode), func(t *testing.T) {
			e := newJoinEnv(t, mode)
			m := e.approvedMember(t)
			e.pushMapping(t, "alice@acme.com", joinIssuer, m.mapping("platform=readers"))
			other := m.mapping("sre=oncall")
			other.Issuer = "https://login.other.dev"
			e.pushMapping(t, "alice@acme.com", joinIssuer, other)
			got, l := e.mappingsOf(t, m)
			if len(got) != 2 || got[0].Issuer != other.Issuer || got[1].Issuer != mapIssuer || got[1].ApprovedBy != "oidc:alice@acme.com" ||
				got[1].Rules[0] != (delivery.GroupRule{From: "platform", To: "readers"}) || !strings.HasPrefix(got[1].Record, "sha256:") ||
				got[1].ApprovedAt == "" || l.Completeness != fleet.CompletenessComplete {
				t.Fatalf("%+v %+v", got, l)
			}
		})
	}
}

func TestPendingMemberCarriesNoMapping(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	e.publish(t, m, m.key, snapNow)
	e.pushMapping(t, "alice@acme.com", joinIssuer, m.mapping("platform=readers"))
	if got, _ := e.mappingsOf(t, m); got != nil {
		t.Fatalf("%+v", got)
	}
}

func TestMappingForAnotherHubIgnored(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := e.approvedMember(t)
	g := m.mapping("platform=readers")
	g.Hub = "another-hub"
	e.pushMapping(t, "alice@acme.com", joinIssuer, g)
	if got, l := e.mappingsOf(t, m); got != nil || l.OtherHubs != 1 {
		t.Fatalf("%+v, other hubs %d", got, l.OtherHubs)
	}
}

// TestMappingOrderUsesSigningTime: a mapping whose At is forward-dated does
// not outrank a clear signed after it.
func TestMappingOrderUsesSigningTime(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := e.approvedMember(t)
	g := m.mapping("platform=readers")
	g.At = "2030-01-01T00:00:00Z"
	e.pushMapping(t, "alice@acme.com", joinIssuer, g)
	e.pushMapping(t, "alice@acme.com", joinIssuer, m.mapping("sre=oncall"))
	if got, _ := e.mappingsOf(t, m); len(got) != 1 || got[0].Rules[0].From != "sre" {
		t.Fatalf("%+v", got)
	}
	e.pushMapping(t, "alice@acme.com", joinIssuer, m.mapping())
	if got, _ := e.mappingsOf(t, m); got != nil {
		t.Fatalf("a clear left %+v", got)
	}
}

func TestMappingApprovedByMustMatchCertificate(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	e.allow["oidc:bob@acme.com"] = true
	m := e.approvedMember(t)
	e.pushMapping(t, "bob@acme.com", joinIssuer, m.mapping("platform=readers")) // claims alice
	got, l := e.mappingsOf(t, m)
	if got != nil || !saysLimitation(l, "have group mapping records that were not counted") {
		t.Fatalf("%+v %+v", got, l.Limitations)
	}
}

func TestMappingsSkipUnverified(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := e.approvedMember(t)
	e.pushMapping(t, "alice@acme.com", joinIssuer, m.mapping("platform=readers"))
	e.pushMapping(t, "alice@acme.com", "https://other-idp.example", m.mapping("sre=oncall")) // no signer trusts it
	got, l := e.mappingsOf(t, m)
	if len(got) != 1 || got[0].Rules[0].From != "platform" || !saysLimitation(l, "not counted") || l.Completeness != fleet.CompletenessComplete {
		t.Fatalf("%+v %+v", got, l)
	}
}

// TestMappingApproverWhoMayNoLongerApproveSkipped: a newer mapping by a user
// who may not approve the member is skipped and the older one stays; a
// clear by them still clears, since clearing only narrows reads.
func TestMappingApproverWhoMayNoLongerApproveSkipped(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := e.approvedMember(t)
	bob := m.mapping("everyone=admins")
	bob.Approver = "oidc:bob@acme.com"
	e.pushMapping(t, "bob@acme.com", joinIssuer, bob)
	if got, l := e.mappingsOf(t, m); got != nil || !saysLimitation(l, "not counted") {
		t.Fatalf("%+v", got)
	}
	e.pushMapping(t, "alice@acme.com", joinIssuer, m.mapping("platform=readers"))
	e.pushMapping(t, "bob@acme.com", joinIssuer, bob)
	if got, _ := e.mappingsOf(t, m); len(got) != 1 || got[0].ApprovedBy != "oidc:alice@acme.com" {
		t.Fatalf("%+v", got)
	}
	bob.Rules = nil
	e.pushMapping(t, "bob@acme.com", joinIssuer, bob)
	if got, _ := e.mappingsOf(t, m); got != nil {
		t.Fatalf("%+v", got)
	}
}

// TestMappingReviewFailureUsesNone: a review that fails or is forbidden
// leaves the issuer with no mapping and the list partial.
func TestMappingReviewFailureUsesNone(t *testing.T) {
	for name, err := range map[string]error{
		"failed":    errors.New("apiserver down"),
		"forbidden": apierrors.NewForbidden(schema.GroupResource{Resource: "subjectaccessreviews"}, "", errors.New("no")),
	} {
		t.Run(name, func(t *testing.T) {
			e := newJoinEnv(t, capabilities.ReferrersNative)
			m := e.approvedMember(t)
			e.pushMapping(t, "alice@acme.com", joinIssuer, m.mapping("platform=readers"))
			bob := m.mapping("sre=oncall")
			bob.Approver = "oidc:bob@acme.com"
			e.pushMapping(t, "bob@acme.com", joinIssuer, bob)
			e.errFor = map[string]error{"oidc:bob@acme.com": err}
			if got, l := e.mappingsOf(t, m); got != nil || l.Completeness != fleet.CompletenessPartial {
				t.Fatalf("%+v %+v", got, l)
			}
		})
	}
}

func TestMappingListingFailureOrCutUsesNone(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	jr := &joinRead{s: e.svc, opts: JoinOptions{Repository: "127.0.0.1:1/acme/pacto-targets"}}
	if got := jr.mappings(t.Context(), memberA); got != nil || len(jr.perCluster[noteMappingsUnread]) != 1 {
		t.Fatalf("%+v %+v", got, jr.perCluster)
	}
	m := e.approvedMember(t)
	e.svc.ClusterJoins(t.Context(), e.opts()) // the join record is read and remembered
	for range delivery.MaxJoinRecords + 1 {
		e.pushMapping(t, "alice@acme.com", joinIssuer, m.mapping("platform=readers", "x=y"))
	}
	if got, l := e.mappingsOf(t, m); got != nil || !saysLimitation(l, "group mapping records and use none") || l.Completeness != fleet.CompletenessPartial {
		t.Fatalf("%+v %+v", got, l)
	}
}

func TestMappingSupersededRecordsRejected(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := e.approvedMember(t)
	for i := range delivery.MaxJoinRecords {
		e.pushMapping(t, "alice@acme.com", joinIssuer, m.mapping("platform=readers"+strings.Repeat("x", i)))
		e.mappingsOf(t, m) // each read rejects what the newest supersedes
	}
	e.pushMapping(t, "alice@acme.com", joinIssuer, m.mapping("sre=oncall"))
	if got, _ := e.mappingsOf(t, m); len(got) != 1 || got[0].Rules[0].From != "sre" {
		t.Fatalf("%+v", got)
	}
}

func TestNoSignersReadsNoMapping(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := e.approvedMember(t)
	e.pushMapping(t, "alice@acme.com", joinIssuer, m.mapping("platform=readers"))
	e.policy = delivery.ClassPolicy{}
	if got, _ := e.mappingsOf(t, m); got != nil {
		t.Fatalf("%+v", got)
	}
}

func saysLimitation(l delivery.ClusterJoinList, part string) bool {
	for _, lim := range l.Limitations {
		if strings.Contains(lim.Message, part) {
			return true
		}
	}
	return false
}

func TestWriteClusterGroupMapping(t *testing.T) {
	for _, mode := range bothModes {
		t.Run(string(mode), func(t *testing.T) {
			e := newJoinEnv(t, mode)
			m := e.approvedMember(t)
			ref, got, err := e.svc.WriteClusterGroupMapping(t.Context(), m.mapping("platform=readers"), e.personSigner(t), e.repo)
			if err != nil || got != mode || !strings.HasPrefix(ref, e.repo+"@sha256:") {
				t.Fatalf("%q %q %v", ref, got, err)
			}
			if ms, _ := e.mappingsOf(t, m); len(ms) != 1 || !strings.HasSuffix(ref, "@"+ms[0].Record) {
				t.Fatalf("%+v", ms)
			}
		})
	}
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m := newMember(memberA, "eu-prod")
	bad := m.mapping("admins=system:masters")
	failing := signerFunc(func(context.Context, string, []byte) ([]byte, error) { return nil, errors.New("signer down") })
	for _, c := range []struct {
		rec  delivery.ClusterGroupMapping
		repo string
		want string
	}{
		{bad, e.repo, "system:"},
		{m.mapping(), "Not A Repo", "invalid"},
		{m.mapping(), e.repo, "signer down"},
	} {
		if _, _, err := e.svc.WriteClusterGroupMapping(t.Context(), c.rec, failing, c.repo); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("want %q, got %v", c.want, err)
		}
	}
}
