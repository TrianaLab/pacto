package dashboard

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// The session names the caller as the identity layer resolved them; the
// capabilities carry only what their permissions leave out.
func TestSessionNamesTheCaller(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true, CSRF: "c", Name: "Carol", User: "https://idp#carol", Groups: []string{"acme:team-payments"},
		Method: "oidc", AuthTime: "1800000000"}}
	f := &fakeDelivery{caller: person("acme:team-payments")}
	au := a.auth(false, true)
	au.LoginName = "Keycloak"
	base := startDelivery(t, f, au, true, nil)
	_, caps := call(t, http.MethodGet, base+"/api/capabilities", nil)
	expect(t, caps["delivery"] == true && caps["miraURL"] == "http://mira" && reflect.DeepEqual(caps["auth"], map[string]any{"hidden": false}),
		"capabilities leave identity to the session: %v", caps)
	_, sess := call(t, http.MethodGet, base+"/api/session", nil)
	want := map[string]any{"mode": "shared", "login": true, "loginName": "Keycloak", "signedIn": true, "csrf": "c", "name": "Carol",
		"user": "https://idp#carol", "groups": []any{"acme:team-payments"}, "method": "oidc", "authTime": "1800000000"}
	expect(t, reflect.DeepEqual(sess, want), "session %v", sess)
	code, _ := call(t, http.MethodGet, base+"/auth/login", nil)
	expect(t, code == http.StatusTeapot, "/auth/* goes to the identity layer: %d", code)
}

func TestSessionModes(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true, Method: "token", User: "system:serviceaccount:shop:viewer"}}
	f := &fakeDelivery{caller: person()}
	au := a.auth(false, false)
	au.TokenLogin = true
	_, sess := call(t, http.MethodGet, startDelivery(t, f, au, true, nil)+"/api/session", nil)
	expect(t, sess["mode"] == "shared" && sess["login"] == false && sess["tokenLogin"] == true && sess["method"] == "token", "token sign-in: %v", sess)
	// Shared without sign-in identifies nobody and serves no delivery.
	base := startDelivery(t, f, a.auth(false, false), true, nil)
	_, sess = call(t, http.MethodGet, base+"/api/session", nil)
	expect(t, sess["mode"] == "open" && sess["login"] == false, "a shared dashboard without sign-in is open: %v", sess)
	_, caps := call(t, http.MethodGet, base+"/api/capabilities", nil)
	expect(t, caps["delivery"] == false, "an open dashboard has no delivery: %v", caps)
	// Local mode, delivery not installed.
	base = startDelivery(t, f, a.auth(true, false), false, nil)
	_, caps = call(t, http.MethodGet, base+"/api/capabilities", nil)
	expect(t, caps["delivery"] == false, "delivery not installed: %v", caps)
	_, sess = call(t, http.MethodGet, base+"/api/session", nil)
	expect(t, sess["mode"] == "local", "local: %v", sess)
	// No identity layer at all.
	srv := startServer(t, NewServer(Options{UI: testUI()}))
	_, sess = call(t, http.MethodGet, srv+"/api/session", nil)
	expect(t, reflect.DeepEqual(sess, map[string]any{"mode": "none", "login": false, "signedIn": false}), "no auth: %v", sess)
	_, caps = call(t, http.MethodGet, srv+"/api/capabilities", nil)
	expect(t, caps["delivery"] == false && reflect.DeepEqual(caps["auth"], map[string]any{"hidden": false}), "no auth: %v", caps)
	code, _ := call(t, http.MethodGet, srv+"/api/delivery/caller", nil)
	expect(t, code == http.StatusNotFound, "no delivery provider registers no delivery route: %d", code)
	if !reflect.DeepEqual((&Server{}).authState(context.Background()), AuthState{}) {
		t.Fatal("no auth, no state")
	}
}

// The delivery caller is who delivery classifies the caller as, with the
// published policy's team rule.
func TestDeliveryCaller(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true}}
	f := &fakeDelivery{caller: person("acme:team-payments")}
	base := startDelivery(t, f, a.auth(false, true), true, nil)
	code, c := call(t, http.MethodGet, base+"/api/delivery/caller", nil)
	expect(t, code == http.StatusOK && c["user"] == "https://idp#carol" && c["class"] == "human" && c["authTime"] != nil && c["requireOwnerTeam"] == false,
		"caller %d %v", code, c)
	f.policy = &delivery.ClassPolicy{RequireOwnerTeam: true}
	_, c = call(t, http.MethodGet, base+"/api/delivery/caller", nil)
	expect(t, c["requireOwnerTeam"] == true, "the caller says when an ownerless question is nobody's: %v", c)
	f.callerErr = Refuse(http.StatusServiceUnavailable, "ClusterUnreachable", "apiserver down")
	code, c = call(t, http.MethodGet, base+"/api/delivery/caller", nil)
	expect(t, code == http.StatusServiceUnavailable && c["code"] == "ClusterUnreachable", "a WhoAmI failure is the refusal: %d %v", code, c)
	code, c = call(t, http.MethodGet, startDelivery(t, f, a.auth(false, true), false, nil)+"/api/delivery/caller", nil)
	expect(t, code == http.StatusNotFound && c["code"] == "DeliveryNotInstalled", "not installed: %d %v", code, c)
}
