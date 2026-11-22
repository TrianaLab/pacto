package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const mapIssuer = "https://login.acme.dev"

// approvedEnv is a hub whose member eu-prod alice approved.
func approvedEnv(t *testing.T, mode capabilities.ReferrersMode) (*clusterEnv, joinMember) {
	t.Helper()
	e := newClusterEnv(t, mode)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	if _, _, err := e.approve(t, m); err != nil {
		t.Fatal(err)
	}
	return e, m
}

// mappings are the group mapping records of uid in the registry.
func (e *clusterEnv) mappings(t *testing.T, uid string) []deliveryrecord.Fetched {
	t.Helper()
	recs, _, err := (&deliveryrecord.Reader{}).ListGroupMappings(t.Context(), e.registry(), uid)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func TestMapGroupsWritesPersonSignedRecord(t *testing.T) {
	e, m := approvedEnv(t, capabilities.ReferrersNative)
	out, errOut, err := e.run(t, "map-groups", "aaaa0000", "--issuer", mapIssuer, "--map", "platform=pacto-readers", "--map", "sre=pacto-readers")
	if err != nil || !strings.HasPrefix(out, "mapped 2 groups of eu-prod (aaaa0000) for "+mapIssuer+" as "+alice+"; recorded "+e.repo+"@sha256:") ||
		!strings.Contains(errOut, "To sign a group mapping on cluster "+euProd) || e.logins != 2 {
		t.Fatalf("%q %q %v", out, errOut, err)
	}
	recs := e.mappings(t, m.uid)
	if len(recs) != 1 {
		t.Fatalf("%d records", len(recs))
	}
	id, _, verr := signing.VerifyPerson(recs[0].Bundle, clusterIssuer, e.policy.Signers[0].TrustedRoot)
	st, err := delivery.OpenClusterGroupMapping(recs[0].Bundle, func([]byte) error { return verr })
	if err != nil || id.Subject != "alice@acme.com" || st.Predicate.Approver != alice || st.Predicate.Hub != clusterHubUID ||
		st.Predicate.Issuer != mapIssuer || len(st.Predicate.Rules) != 2 {
		t.Fatalf("%+v %+v %v", id, st.Predicate, err)
	}
	if s := e.state(t, m); len(s.Mappings) != 1 || s.Mappings[0].ApprovedBy != alice {
		t.Fatalf("%+v", s)
	}
}

func TestMapGroupsRefusedWithoutApproveRight(t *testing.T) {
	e, m := approvedEnv(t, capabilities.ReferrersNative)
	e.mayApprove = false
	_, _, err := e.run(t, "map-groups", euProd, "--issuer", mapIssuer, "--map", "platform=readers")
	if err == nil || !strings.Contains(err.Error(), "ClusterApproveForbidden") || len(e.mappings(t, m.uid)) != 0 || e.logins != 1 {
		t.Fatalf("%v, %d logins", err, e.logins)
	}
}

func TestMapGroupsRefusesSystemTarget(t *testing.T) {
	e, m := approvedEnv(t, capabilities.ReferrersNative)
	_, _, err := e.run(t, "map-groups", euProd, "--issuer", mapIssuer, "--map", "admins=system:masters")
	if err == nil || !strings.Contains(err.Error(), "may not start with system") || len(e.mappings(t, m.uid)) != 0 || e.logins != 1 {
		t.Fatalf("%v, %d logins", err, e.logins)
	}
}

// TestMapGroupsClear: --clear with --issuer clears that issuer; without it
// every issuer with a mapping; with none left it signs nothing.
func TestMapGroupsClear(t *testing.T) {
	e, m := approvedEnv(t, capabilities.ReferrersWriteOnceTags)
	for _, iss := range []string{mapIssuer, "https://login.other.dev"} {
		if _, _, err := e.run(t, "map-groups", euProd, "--issuer", iss, "--map", "platform=readers"); err != nil {
			t.Fatal(err)
		}
		e.ss.Advance(1e9)
	}
	out, _, err := e.run(t, "map-groups", euProd, "--clear", "--issuer", mapIssuer)
	if err != nil || !strings.HasPrefix(out, "cleared the group mapping of eu-prod (aaaa0000) for "+mapIssuer) ||
		!strings.Contains(out, "\npushed "+e.repo+":"+deliveryrecord.AnchorTag(delivery.ClusterJoinStream(euProd))+"-group-mapping-") {
		t.Fatalf("%q %v", out, err)
	}
	e.ss.Advance(1e9)
	if s := e.state(t, m); len(s.Mappings) != 1 || s.Mappings[0].Issuer != "https://login.other.dev" {
		t.Fatalf("%+v", s.Mappings)
	}
	if out, _, err := e.run(t, "map-groups", euProd, "--clear"); err != nil || !strings.Contains(out, "cleared the group mapping of eu-prod (aaaa0000) for https://login.other.dev") {
		t.Fatalf("%q %v", out, err)
	}
	logins := e.logins
	if out, _, err := e.run(t, "map-groups", euProd, "--clear"); err != nil || out != "eu-prod (aaaa0000) has no group mapping to clear; nothing was signed\n" || e.logins != logins {
		t.Fatalf("%q %v", out, err)
	}
}

// TestMapGroupsRefuses: bad flags, an unreachable hub, an unknown or pending
// member and no signer are refused before any login.
func TestMapGroupsRefuses(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{euProd, "--issuer", mapIssuer, "--map", "platform"}, `--map "platform" wants <hub group>=<member group>`},
		{[]string{euProd, "--clear", "--map", "a=b"}, "--clear takes no --map"},
		{[]string{euProd, "--issuer", mapIssuer}, "name at least one --map"},
		{[]string{euProd, "--map", "a=b"}, "--issuer is required"},
		{[]string{euProd, "--map", "a=b", "--issuer", mapIssuer, "--context", "a", "--context", "b"}, "name one hub cluster"},
		{[]string{"ffff0000", "--map", "a=b", "--issuer", mapIssuer}, "no cluster ffff0000"},
		{[]string{euProd, "--map", "a=b", "--issuer", mapIssuer}, "eu-prod (aaaa0000) is pending: groups are mapped for an approved member"},
	} {
		if _, _, err := e.run(t, append([]string{"map-groups"}, c.args...)...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%v: want %q, got %v", c.args, c.want, err)
		}
	}
	e, _ = approvedEnv(t, capabilities.ReferrersNative)
	e.policy.Signers = append(e.policy.Signers, delivery.PersonSigner{Issuer: "https://other.example", Subjects: "x"})
	if _, _, err := e.run(t, "map-groups", euProd, "--map", "a=b", "--issuer", mapIssuer); err == nil || !strings.Contains(err.Error(), "pick the approver signer") {
		t.Fatal(err)
	}
	if _, _, err := e.run(t, "map-groups", euProd, "--map", "a=b", "--issuer", "http://insecure"); err == nil {
		t.Fatal("an http issuer was accepted")
	}
	if e.logins != 1 {
		t.Fatalf("%d logins", e.logins)
	}
}

func TestMapGroupsRefusesApproverMismatchAndFailedWrite(t *testing.T) {
	e, m := approvedEnv(t, capabilities.ReferrersNative)
	e.email = "eve@evil.example"
	if _, _, err := e.run(t, "map-groups", euProd, "--issuer", mapIssuer, "--map", "a=b"); err == nil || len(e.mappings(t, m.uid)) != 0 {
		t.Fatal(err)
	}
	e.email = "alice@acme.com"
	// The registry refuses writes once the person logged in.
	host, path, _ := strings.Cut(e.repo, "/")
	var refuse atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if refuse.Load() && r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "read-only", http.StatusInternalServerError)
			return
		}
		httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: host}).ServeHTTP(w, r)
	}))
	t.Cleanup(proxy.Close)
	e.repo = strings.TrimPrefix(proxy.URL, "http://") + "/" + path
	e.onLogin = func() { refuse.Store(true) }
	if _, _, err := e.run(t, "map-groups", euProd, "--issuer", mapIssuer, "--map", "a=b"); err == nil || len(e.mappings(t, m.uid)) != 0 {
		t.Fatalf("a write the registry refused succeeded: %v", err)
	}
}

func TestClusterListShowsMapping(t *testing.T) {
	e, _ := approvedEnv(t, capabilities.ReferrersNative)
	if _, _, err := e.run(t, "map-groups", euProd, "--issuer", mapIssuer, "--map", "platform=readers", "--map", "sre=readers"); err != nil {
		t.Fatal(err)
	}
	out, _, err := e.run(t, "list")
	if err != nil || !strings.Contains(out, "  mapping "+alice+" ") || !strings.Contains(out, " (2 rules) for "+mapIssuer+"\n") {
		t.Fatalf("%q %v", out, err)
	}
	out, _, _ = e.run(t, "list", "--output-format", "markdown")
	if !strings.Contains(out, " (2 rules) for "+mapIssuer+" |\n") {
		t.Fatalf("%q", out)
	}
	var b bytes.Buffer
	printClusterList(&b, delivery.ClusterJoinList{Clusters: []delivery.ClusterJoinState{{UID: euProd, Name: "eu-prod", State: delivery.JoinApproved,
		Mappings: []delivery.GroupMappingState{{Issuer: mapIssuer, Rules: []delivery.GroupRule{{From: "a", To: "b"}}, ApprovedBy: alice, ApprovedAt: "2026-11-20T10:00:00Z"}}}}})
	if !strings.Contains(b.String(), "mapping "+alice+" 2026-11-20 (1 rule) for "+mapIssuer) {
		t.Fatalf("%q", b.String())
	}
	if out, _, _ := e.run(t, "list", "--output-format", "json"); !strings.Contains(out, `"mappings"`) || json.Valid([]byte(out)) == false {
		t.Fatalf("%s", out)
	}
}

// A --clear of every issuer is refused when the mappings could not all be
// read: an unread mapping is unknown, not absent, and stays in force.
func TestMapGroupsClearAllRefusedOnPartialList(t *testing.T) {
	member := delivery.ClusterJoinState{UID: euProd, Name: "eu-prod"}
	partial := delivery.ClusterJoinList{Completeness: fleet.CompletenessPartial,
		Limitations: []fleet.Limitation{{Code: fleet.LimitationSourceUnavailable, Message: "the group mappings of 1 cluster could not be read, so their reads use none"}}}
	err := clearReadable(partial, member, "", nil)
	if err == nil || !strings.Contains(err.Error(), "could not be read") || !strings.Contains(err.Error(), "--issuer") {
		t.Fatalf("clear all on a partial list: %v", err)
	}
	if clearReadable(partial, member, mapIssuer, nil) != nil || clearReadable(partial, member, "", []delivery.GroupRule{{From: "a", To: "b"}}) != nil {
		t.Error("a clear of one issuer, or a mapping, is refused on a partial list")
	}
	if clearReadable(delivery.ClusterJoinList{Completeness: fleet.CompletenessComplete}, member, "", nil) != nil {
		t.Error("a complete list refuses clear all")
	}
}

// End to end: a mapping record the registry will not serve leaves the list
// partial, so a --clear of every issuer is refused and nothing is signed.
func TestMapGroupsClearAllRefusedWhenAMappingIsUnread(t *testing.T) {
	e, m := approvedEnv(t, capabilities.ReferrersNative)
	if _, _, err := e.run(t, "map-groups", euProd, "--issuer", mapIssuer, "--map", "platform=readers"); err != nil {
		t.Fatal(err)
	}
	blob := fmt.Sprintf("sha256:%x", sha256.Sum256(e.mappings(t, m.uid)[0].Bundle))
	host, path, _ := strings.Cut(e.repo, "/")
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/blobs/"+blob) {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: host}).ServeHTTP(w, r)
	}))
	t.Cleanup(proxy.Close)
	e.repo = strings.TrimPrefix(proxy.URL, "http://") + "/" + path
	logins := e.logins
	_, _, err := e.run(t, "map-groups", euProd, "--clear")
	if err == nil || !strings.Contains(err.Error(), "the group mappings of eu-prod (aaaa0000) could not all be read") || e.logins != logins {
		t.Fatalf("%v, %d logins", err, e.logins-logins)
	}
}
