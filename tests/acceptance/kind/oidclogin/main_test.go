package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeIdP has one connector: a password form, then a redirect with a code.
// A second login in the same provider session skips the form.
func fakeIdP(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"authorization_endpoint": srv.URL + "/auth", "token_endpoint": srv.URL + "/token"})
	})
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		back := url.QueryEscape(r.URL.Query().Get("redirect_uri") + "?state=" + r.URL.Query().Get("state"))
		if c, err := r.Cookie("idp_session"); err == nil && c.Value == "alice" && r.URL.Query().Get("prompt") != "login" {
			http.Redirect(w, r, r.URL.Query().Get("redirect_uri")+"?code=sso&state="+r.URL.Query().Get("state"), http.StatusFound)
			return
		}
		http.Redirect(w, r, "/login?back="+back, http.StatusFound)
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`<form method="post" action="/login?back=` + url.QueryEscape(r.URL.Query().Get("back")) + `&amp;x=1">`))
			return
		}
		_ = r.ParseForm()
		if r.PostForm.Get("login") != "alice" || r.PostForm.Get("password") != "pw" {
			http.Error(w, "bad credentials", http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "idp_session", Value: "alice"})
		http.Redirect(w, r, r.URL.Query().Get("back")+"&code=c1", http.StatusSeeOther)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if id, secret, _ := r.BasicAuth(); id != "client" || secret != "s" {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": "id-" + r.PostForm.Get("code")})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func fakeDashboard(t *testing.T, idp string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		prompt := ""
		if r.URL.Query().Get("fresh") == "1" {
			prompt = "&prompt=login"
		}
		http.SetCookie(w, &http.Cookie{Name: "__Host-pacto-login", Value: "st"})
		http.Redirect(w, r, idp+"/auth?state=st&redirect_uri="+url.QueryEscape(srv.URL+"/auth/callback")+prompt, http.StatusFound)
	})
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("__Host-pacto-login"); err != nil || c.Value != "st" {
			http.Error(w, "no login cookie", http.StatusBadRequest)
			return
		}
		old, _ := r.Cookie(sessionCookie)
		v := "session-" + r.URL.Query().Get("code")
		if old != nil {
			v += "-rotated-from-" + old.Value
		}
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: v})
		http.Redirect(w, r, "/", http.StatusFound)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func runOut(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var b strings.Builder
	err := run(args, &b)
	return strings.TrimSpace(b.String()), err
}

func TestDashboardLoginSingleSignOnAndFresh(t *testing.T) {
	idp := fakeIdP(t)
	dash := fakeDashboard(t, idp.URL)
	jar := filepath.Join(t.TempDir(), "jar")
	first, err := runOut(t, "dashboard", "-dashboard", dash.URL, "-user", "alice", "-password", "pw", "-jar", jar)
	if err != nil || first != "session-c1" {
		t.Fatalf("first login: %q %v", first, err)
	}
	// The provider session in the jar makes the next plain login single sign-on.
	sso, err := runOut(t, "dashboard", "-dashboard", dash.URL, "-user", "alice", "-password", "pw", "-jar", jar)
	if err != nil || sso != "session-sso" {
		t.Fatalf("single sign-on: %q %v", sso, err)
	}
	fresh, err := runOut(t, "dashboard", "-dashboard", dash.URL, "-user", "alice", "-password", "pw", "-jar", jar, "-fresh", "-session", sso)
	if err != nil || fresh != "session-c1-rotated-from-session-sso" {
		t.Fatalf("fresh login asks again and carries the old session: %q %v", fresh, err)
	}
	if _, err := runOut(t, "dashboard", "-dashboard", dash.URL, "-user", "alice", "-password", "wrong"); err == nil ||
		!strings.Contains(err.Error(), "bad credentials") {
		t.Fatalf("wrong password: %v", err)
	}
}

func TestToken(t *testing.T) {
	idp := fakeIdP(t)
	tok, err := runOut(t, "token", "-issuer", idp.URL, "-client", "client", "-secret", "s", "-redirect", "http://127.0.0.1:1/cb",
		"-user", "alice", "-password", "pw")
	if err != nil || tok != "id-c1" {
		t.Fatalf("token: %q %v", tok, err)
	}
	if _, err := runOut(t, "token", "-issuer", idp.URL, "-client", "client", "-secret", "x", "-redirect", "http://127.0.0.1:1/cb",
		"-user", "alice", "-password", "pw"); err == nil {
		t.Fatal("a refused client gets no token")
	}
}

func TestUsage(t *testing.T) {
	if _, err := runOut(t); err == nil {
		t.Fatal("no command")
	}
	if _, err := runOut(t, "nope"); err == nil {
		t.Fatal("unknown command")
	}
	if _, err := runOut(t, "token", "-ca", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing CA file")
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	_ = os.WriteFile(ca, []byte("not a cert"), 0o600)
	if _, err := runOut(t, "dashboard", "-ca", ca, "-dashboard", "http://127.0.0.1:1"); err == nil {
		t.Fatal("an unreachable dashboard")
	}
}
