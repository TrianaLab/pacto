package dashboard

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

func TestCapabilitiesAndSessionSignedIn(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true, CSRF: "c"}}
	f := &fakeDelivery{caller: person("acme:team-payments")}
	base := startDelivery(t, f, a.auth(false, true), true, nil)
	_, caps := call(t, http.MethodGet, base+"/api/capabilities", nil)
	auth, _ := caps["auth"].(map[string]any)
	expect(t, caps["delivery"] == true && caps["miraURL"] == "http://mira", "capabilities %v", caps)
	expect(t, auth["mode"] == "shared" && auth["login"] == true && auth["signedIn"] == true, "capabilities %v", caps)
	_, sess := call(t, http.MethodGet, base+"/api/session", nil)
	caller, _ := sess["caller"].(map[string]any)
	expect(t, sess["mode"] == "shared" && sess["signedIn"] == true, "session %v", sess)
	expect(t, sess["csrf"] == "c" && caller["user"] == "https://idp#carol", "session %v", sess)
	expect(t, sess["requireOwnerTeam"] == nil, "an ownerless question is anyone's without the rule: %v", sess)
	f.policy = &delivery.ClassPolicy{RequireOwnerTeam: true}
	_, sess = call(t, http.MethodGet, base+"/api/session", nil)
	expect(t, sess["requireOwnerTeam"] == true, "the session says when an ownerless question is nobody's: %v", sess)
	f.policy = nil
	code, _ := call(t, http.MethodGet, base+"/auth/login", nil)
	expect(t, code == http.StatusTeapot, "/auth/* goes to the identity layer: %d", code)
	f.callerErr = errors.New("apiserver down")
	_, sess = call(t, http.MethodGet, base+"/api/session", nil)
	expect(t, sess["callerError"] == "apiserver down", "a WhoAmI failure is reported: %v", sess)
}

func TestCapabilitiesAndSessionWithTokenSignIn(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true, CSRF: "c", Method: "token", User: "system:serviceaccount:shop:viewer", Groups: []string{"system:serviceaccounts"}}}
	f := &fakeDelivery{caller: person()}
	au := a.auth(false, false)
	au.TokenLogin, au.LoginName = true, "Keycloak"
	base := startDelivery(t, f, au, true, nil)
	_, caps := call(t, http.MethodGet, base+"/api/capabilities", nil)
	auth, _ := caps["auth"].(map[string]any)
	expect(t, caps["delivery"] == true && auth["mode"] == "shared" && auth["login"] == false && auth["tokenLogin"] == true, "capabilities %v", caps)
	_, sess := call(t, http.MethodGet, base+"/api/session", nil)
	expect(t, sess["tokenLogin"] == true && sess["loginName"] == "Keycloak" && sess["method"] == "token" && sess["user"] == "system:serviceaccount:shop:viewer", "session %v", sess)
}

func TestCapabilitiesAndSessionWithoutDelivery(t *testing.T) {
	a := &authed{state: AuthState{SignedIn: true}}
	f := &fakeDelivery{caller: person()}
	// Shared without sign-in: no delivery.
	base := startDelivery(t, f, a.auth(false, false), true, nil)
	_, caps := call(t, http.MethodGet, base+"/api/capabilities", nil)
	expect(t, caps["delivery"] == false, "a shared dashboard without sign-in has no delivery: %v", caps)
	// Local mode, delivery not installed.
	base = startDelivery(t, f, a.auth(true, false), false, Refuse(http.StatusUnauthorized, "LoginRequired", "sign in"))
	_, caps = call(t, http.MethodGet, base+"/api/capabilities", nil)
	expect(t, caps["delivery"] == false && caps["auth"].(map[string]any)["mode"] == "local", "delivery not installed: %v", caps)
	_, sess := call(t, http.MethodGet, base+"/api/session", nil)
	expect(t, sess["mode"] == "local" && sess["caller"] == nil, "no caller when delivery is not installed: %v", sess)
	// A factory that refuses is reported.
	base = startDelivery(t, f, a.auth(true, false), true, Refuse(http.StatusUnauthorized, "LoginRequired", "sign in"))
	_, sess = call(t, http.MethodGet, base+"/api/session", nil)
	expect(t, strings.Contains(sess["callerError"].(string), "LoginRequired"), "a factory refusal is reported: %v", sess)
	// No identity layer at all.
	srv := startServer(t, NewServer(Options{UI: testUI()}))
	_, sess = call(t, http.MethodGet, srv+"/api/session", nil)
	expect(t, sess["mode"] == "none", "no auth: %v", sess)
	_, caps = call(t, http.MethodGet, srv+"/api/capabilities", nil)
	expect(t, caps["delivery"] == false && caps["auth"].(map[string]any)["mode"] == "none", "no auth: %v", caps)
	code, _ := call(t, http.MethodGet, srv+"/api/delivery/inbox", nil)
	expect(t, code == http.StatusNotFound, "no delivery provider registers no delivery route: %d", code)
}
