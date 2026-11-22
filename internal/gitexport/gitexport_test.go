package gitexport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testToken = "ghp_secret123"

// call is one request the recording transport answered.
type call struct {
	Method, URL, Auth string
	Body              map[string]any
}

// answer is a canned response.
type answer struct {
	status int
	body   string
	header http.Header
}

// hostFake answers requests from canned answers keyed by "METHOD URL", with
// no socket, and records each.
type hostFake struct {
	answers map[string]answer
	calls   []call
}

func (h *hostFake) RoundTrip(r *http.Request) (*http.Response, error) {
	c := call{Method: r.Method, URL: r.URL.String(), Auth: r.Header.Get("Authorization")}
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &c.Body)
	}
	h.calls = append(h.calls, c)
	a, ok := h.answers[r.Method+" "+r.URL.String()]
	if !ok {
		a = answer{status: http.StatusNotFound, body: `{"message":"Not Found"}`}
	}
	resp := &http.Response{StatusCode: a.status, Header: a.header, Body: io.NopCloser(strings.NewReader(a.body)), Request: r}
	if resp.Header == nil {
		resp.Header = http.Header{}
	}
	return resp, nil
}

// gitHubFake is a GitHub API at api whose repository acme/invoices is
// private (or not) with main as its default branch.
func gitHubFake(api string, private bool) *hostFake {
	repo := api + "/repos/acme/invoices"
	vis := `"private":true,"visibility":"private"`
	if !private {
		vis = `"private":false,"visibility":"public"`
	}
	return &hostFake{answers: map[string]answer{
		"GET " + api + "/user":                {status: 200, body: `{"login":"ana"}`},
		"GET " + repo:                         {status: 200, body: `{"default_branch":"main",` + vis + `}`},
		"GET " + repo + "/git/ref/heads/main": {status: 200, body: `{"object":{"sha":"c0"}}`},
		"GET " + repo + "/git/commits/c0":     {status: 200, body: `{"tree":{"sha":"t0"}}`},
		"POST " + repo + "/git/trees":         {status: 201, body: `{"sha":"t1"}`},
		"POST " + repo + "/git/commits":       {status: 201, body: `{"sha":"c1"}`},
		"POST " + repo + "/git/refs":          {status: 201, body: `{}`},
		"POST " + repo + "/pulls":             {status: 201, body: `{"html_url":"https://github.com/acme/invoices/pull/7"}`},
	}}
}

func request(url string, rt http.RoundTripper) Request {
	return Request{Kind: KindGitHub, URL: url, Folder: "deploy", Token: testToken, Transport: rt,
		Files:       map[string][]byte{"pacto.yaml": []byte("pactoVersion: \"2.5\"\n"), "pacto.inferred.yaml": []byte("declaration: {}\n")},
		Declaration: Declaration{Service: "invoices", Tag: "0.2.0", Bundle: "ghcr.io/acme/invoices@sha256:aa", State: "declared", Signer: "ana@acme.com", Rung: "gitops"}}
}

func TestGitHubOpenSequence(t *testing.T) {
	h := gitHubFake("https://api.github.com", true)
	got, e := Open(t.Context(), request("https://github.com/acme/invoices", h))
	if e != nil {
		t.Fatal(e)
	}
	if *got != (Opened{URL: "https://github.com/acme/invoices/pull/7", Login: "ana", Branch: "pacto/declare-invoices-0.2.0"}) {
		t.Fatalf("%+v", got)
	}
	repo := "https://api.github.com/repos/acme/invoices"
	want := []string{"GET https://api.github.com/user", "GET " + repo, "GET " + repo + "/git/ref/heads/main", "GET " + repo + "/git/commits/c0",
		"POST " + repo + "/git/trees", "POST " + repo + "/git/commits", "POST " + repo + "/git/refs", "POST " + repo + "/pulls"}
	if len(h.calls) != len(want) {
		t.Fatalf("%+v", h.calls)
	}
	for i, c := range h.calls {
		if c.Method+" "+c.URL != want[i] || c.Auth != "Bearer "+testToken {
			t.Fatalf("%d: %+v", i, c)
		}
	}
	wantGitHubBodies(t, h.calls)
}

// wantGitHubBodies: the tree holds both files under deploy/, one commit on
// main's head, the branch points at it and the pull request names the bundle.
func wantGitHubBodies(t *testing.T, calls []call) {
	t.Helper()
	want := []string{
		`{"base_tree":"t0","tree":[{"content":"declaration: {}\n","mode":"100644","path":"deploy/pacto.inferred.yaml","type":"blob"},` +
			`{"content":"pactoVersion: \"2.5\"\n","mode":"100644","path":"deploy/pacto.yaml","type":"blob"}]}`,
		`{"message":"chore(pacto): declare invoices 0.2.0","parents":["c0"],"tree":"t1"}`,
		`{"ref":"refs/heads/pacto/declare-invoices-0.2.0","sha":"c1"}`,
	}
	for i, w := range want {
		if got, _ := json.Marshal(calls[4+i].Body); string(got) != w {
			t.Fatalf("%d: %s", 4+i, got)
		}
	}
	if p := calls[7].Body; p["head"] != "pacto/declare-invoices-0.2.0" || p["base"] != "main" || p["title"] != "chore(pacto): declare invoices 0.2.0" ||
		!strings.Contains(p["body"].(string), "`ghcr.io/acme/invoices@sha256:aa`") {
		t.Fatalf("%+v", p)
	}
}

func TestGitHubRootFolder(t *testing.T) {
	h := gitHubFake("https://api.github.com", true)
	r := request("https://github.com/acme/invoices", h)
	r.Folder = "."
	if _, e := Open(t.Context(), r); e != nil {
		t.Fatal(e)
	}
	if p := h.calls[4].Body["tree"].([]any)[1].(map[string]any)["path"]; p != "pacto.yaml" {
		t.Fatal(p)
	}
}

func TestGitHubEnterpriseBase(t *testing.T) {
	h := gitHubFake("https://git.acme.com/api/v3", false)
	r := request("https://git.acme.com/acme/invoices", h)
	r.Confirmed = true
	got, e := Open(t.Context(), r)
	if e != nil || got == nil || h.calls[0].URL != "https://git.acme.com/api/v3/user" {
		t.Fatalf("%v %+v %+v", e, got, h.calls)
	}
}

func TestGitHubInternalIsNotPublic(t *testing.T) {
	h := gitHubFake("https://git.acme.com/api/v3", true)
	h.answers["GET https://git.acme.com/api/v3/repos/acme/invoices"] = answer{status: 200, body: `{"default_branch":"main","private":false,"visibility":"internal"}`}
	if got, e := Open(t.Context(), request("https://git.acme.com/acme/invoices", h)); e != nil || got == nil {
		t.Fatalf("%v %+v", e, got)
	}
}

func TestGitHubBranchExists(t *testing.T) {
	for _, status := range []int{http.StatusUnprocessableEntity, http.StatusConflict} {
		h := gitHubFake("https://api.github.com", true)
		h.answers["POST https://api.github.com/repos/acme/invoices/git/refs"] = answer{status: status, body: `{"message":"Reference already exists"}`}
		_, e := Open(t.Context(), request("https://github.com/acme/invoices", h))
		if e == nil || e.Code != CodePullRequestFailed || e.Message != "branch pacto/declare-invoices-0.2.0 exists: a pull request for this declaration may be open" {
			t.Fatalf("%d: %v", status, e)
		}
	}
}

func TestGitHubPushDenied(t *testing.T) {
	h := gitHubFake("https://api.github.com", true)
	h.answers["POST https://api.github.com/repos/acme/invoices/git/trees"] = answer{status: 403,
		body: `{"message":"Resource not accessible by personal access token ` + strings.Repeat("x", 300) + `"}`}
	_, e := Open(t.Context(), request("https://github.com/acme/invoices", h))
	if e == nil || e.Code != CodePullRequestFailed || !strings.Contains(e.Message, "403: Resource not accessible by personal access token") ||
		len([]rune(e.Message)) > 300 {
		t.Fatalf("%v", e)
	}
	if len(h.calls) != 5 {
		t.Fatalf("%d calls after a refused write", len(h.calls))
	}
}

func TestGitHubEveryStepFails(t *testing.T) {
	repo := "https://api.github.com/repos/acme/invoices"
	for _, key := range []string{"GET https://api.github.com/user", "GET " + repo, "GET " + repo + "/git/ref/heads/main", "GET " + repo + "/git/commits/c0",
		"POST " + repo + "/git/commits", "POST " + repo + "/git/refs", "POST " + repo + "/pulls"} {
		h := gitHubFake("https://api.github.com", true)
		h.answers[key] = answer{status: 500, body: "boom"}
		if _, e := Open(t.Context(), request("https://github.com/acme/invoices", h)); e == nil || !strings.Contains(e.Message, "500: boom") {
			t.Fatalf("%s: %v", key, e)
		}
	}
}

func TestPublicRepositoryNeedsPersonConfirmation(t *testing.T) {
	h := gitHubFake("https://api.github.com", false)
	_, e := Open(t.Context(), request("https://github.com/acme/invoices", h))
	if e == nil || e.Message != "the repository is public: confirm with --repository https://github.com/acme/invoices" || len(h.calls) != 2 {
		t.Fatalf("%v %d", e, len(h.calls))
	}
	r := request("https://github.com/acme/invoices", gitHubFake("https://api.github.com", false))
	r.Confirmed = true
	if got, e := Open(t.Context(), r); e != nil || got == nil {
		t.Fatalf("%v %+v", e, got)
	}
}

func TestRedirectNeverCarriesToken(t *testing.T) {
	for _, loc := range []string{"http://api.github.com/user", "https://evil.example/user"} {
		h := gitHubFake("https://api.github.com", true)
		h.answers["GET https://api.github.com/user"] = answer{status: http.StatusTemporaryRedirect, header: http.Header{"Location": {loc}}}
		_, e := Open(t.Context(), request("https://github.com/acme/invoices", h))
		if e == nil || e.Message != "the host redirected; pass the final URL with --repository" || len(h.calls) != 1 {
			t.Fatalf("%s: %v %+v", loc, e, h.calls)
		}
	}
}

func TestPlainHTTPRefusedOffLoopback(t *testing.T) {
	_, e := Open(t.Context(), request("http://github.com/acme/invoices", gitHubFake("http://github.com/api/v3", true)))
	if e == nil || e.Message != "plain HTTP would send your token in clear: use https://github.com/acme/invoices" {
		t.Fatal(e)
	}
	for _, host := range []string{"127.0.0.1:3000", "localhost:3000", "[::1]:3000"} {
		r := request("http://"+host+"/acme/invoices", gitHubFake("http://"+host+"/api/v3", true))
		if _, e := Open(t.Context(), r); e != nil {
			t.Fatalf("%s: %v", host, e)
		}
	}
}

func TestOpenRefusesBadInput(t *testing.T) {
	if _, e := Open(t.Context(), request("not a url", nil)); e == nil || !strings.Contains(e.Message, "is not a repository URL") {
		t.Fatal(e)
	}
	r := request("https://github.com/acme/invoices", gitHubFake("https://api.github.com", true))
	r.Files["pacto.yaml"] = make([]byte, maxBody+1)
	if _, e := Open(t.Context(), r); e == nil || e.Message != "pacto.yaml is over 1 MiB" {
		t.Fatal(e)
	}
}

func TestPRBodyEscapesUntrustedValues(t *testing.T) {
	d := Declaration{Service: "invoices`\n@org/security-team", Tag: "draft-3", Bundle: "ghcr.io/acme/invoices@sha256:aa", State: "draft",
		OnBehalfOf: "[click](https://evil) @org/security-team " + strings.Repeat("é", 300), Rung: "given"}
	body := prBody(d)
	for _, line := range strings.Split(body, "\n") {
		// Outside code spans nothing untrusted remains.
		parts := strings.Split(line, "`")
		if len(parts)%2 == 0 {
			t.Fatalf("unbalanced code span: %q", line)
		}
		for i := 0; i < len(parts); i += 2 {
			if strings.Contains(parts[i], "@") || strings.Contains(parts[i], "](") {
				t.Fatalf("untrusted text outside a code span: %q", line)
			}
		}
	}
	if !strings.Contains(body, "- Signed by: unsigned draft, proposer asserted\n") || !strings.Contains(body, "`invoices @org/security-team`") ||
		strings.Count(body, "é") != 200-len("[click](https://evil) @org/security-team ") || strings.Contains(body, "Co-authored") {
		t.Fatal(body)
	}
	signed := prBody(Declaration{Service: "invoices", Tag: "0.2.0", Bundle: "b", State: "declared", Signer: "ana@acme.com", Rung: "gitops"})
	if !strings.Contains(signed, "- Signed by: `ana@acme.com`\n") || strings.Contains(signed, "On behalf of") ||
		!strings.Contains(signed, "CI recomputes pacto.inferred.yaml on its next publish") {
		t.Fatal(signed)
	}
}

// blockingTransport blocks every request until its context ends.
type blockingTransport struct{}

func (blockingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func TestRequestTimeout(t *testing.T) {
	old := requestTimeout
	requestTimeout = 10 * time.Millisecond
	t.Cleanup(func() { requestTimeout = old })
	_, e := Open(t.Context(), request("https://github.com/acme/invoices", blockingTransport{}))
	if e == nil || e.Message != "the host did not answer GET /user within 10ms" {
		t.Fatal(e)
	}
}

// failingTransport fails every request, echoing the token.
type failingTransport struct{}

func (failingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, errors.New("dial failed for " + r.Header.Get("Authorization"))
}

func TestTransportErrorIsARefusal(t *testing.T) {
	_, e := Open(t.Context(), request("https://github.com/acme/invoices", failingTransport{}))
	if e == nil || !strings.Contains(e.Message, "dial failed for Bearer <token>") {
		t.Fatal(e)
	}
}

func TestResponseBodyBounded(t *testing.T) {
	h := gitHubFake("https://api.github.com", true)
	h.answers["GET https://api.github.com/user"] = answer{status: 200, body: `{"login":"` + strings.Repeat("a", maxBody) + `"}`}
	if _, e := Open(t.Context(), request("https://github.com/acme/invoices", h)); e == nil || !strings.Contains(e.Message, "with more than 1 MiB") {
		t.Fatal(e)
	}
	h = gitHubFake("https://api.github.com", true)
	h.answers["GET https://api.github.com/user"] = answer{status: 200, body: `<html>`}
	if _, e := Open(t.Context(), request("https://github.com/acme/invoices", h)); e == nil || !strings.Contains(e.Message, "a body that is not JSON") {
		t.Fatal(e)
	}
}

func TestClientRefusesUnbuildableRequest(t *testing.T) {
	c := &client{http: &http.Client{}}
	if _, e := c.do(context.Background(), "BAD METHOD", "https://x", nil, nil); e == nil || !strings.Contains(e.Message, "building BAD METHOD") {
		t.Fatal(e)
	}
}

func TestHostMessageRedactsToken(t *testing.T) {
	c := &client{token: testToken}
	if got := c.hostMessage([]byte(`{"message":"bad credentials ` + testToken + `"}`)); got != "bad credentials <token>" {
		t.Fatal(got)
	}
	if got := (&client{}).redact("x"); got != "x" {
		t.Fatal(got)
	}
	if got := c.hostMessage([]byte(" plain ")); got != "plain" {
		t.Fatal(got)
	}
}

func TestTokenNeverPrinted(t *testing.T) {
	// The host echoes the token in an error, the env and the helper both
	// hold it: no refusal and no opened result carries it.
	h := gitHubFake("https://api.github.com", true)
	h.answers["POST https://api.github.com/repos/acme/invoices/pulls"] = answer{status: 422, body: `{"message":"token ` + testToken + ` lacks scope"}`}
	_, e := Open(t.Context(), request("https://github.com/acme/invoices", h))
	if e == nil || strings.Contains(e.Error(), testToken) {
		t.Fatal(e)
	}
	env := func(k string) string { return map[string]string{"GITHUB_TOKEN": testToken}[k] }
	_, src, te := Token(t.Context(), KindGitHub, "https://github.com/acme/invoices", env, nil)
	if te != nil || strings.Contains(src, testToken) {
		t.Fatal(te, src)
	}
	_, src, te = Token(t.Context(), KindGitHub, "https://github.com/acme/invoices", func(string) string { return "" },
		func(context.Context, string) (string, error) { return "password=" + testToken + "\n", nil })
	if te != nil || strings.Contains(src, testToken) {
		t.Fatal(te, src)
	}
	got, _ := Open(t.Context(), request("https://github.com/acme/invoices", gitHubFake("https://api.github.com", true)))
	if raw, _ := json.Marshal(got); strings.Contains(string(raw), testToken) {
		t.Fatal(string(raw))
	}
}

func TestBranch(t *testing.T) {
	if got := Branch("invoices", "draft-3"); got != "pacto/declare-invoices-draft-3" {
		t.Fatal(got)
	}
}

func TestDetectHostKinds(t *testing.T) {
	env := func(k string) string { return map[string]string{"GH_HOST": "git.acme.com"}[k] }
	for _, c := range []struct {
		url, override string
		want          Kind
		code          string
	}{
		{"https://github.com/acme/invoices", "", KindGitHub, ""},
		{"https://git.acme.com/acme/invoices", "", KindGitHub, ""},
		{"https://git.other.com/acme/invoices", "github", KindGitHub, ""},
		{"https://git.other.com/acme/invoices", "", "", CodeGitHostUnknown},
		{"https://github.com/acme/invoices", "svn", "", CodeGitHostUnknown},
		{"%zz", "", "", CodeGitHostUnknown},
	} {
		got, e := Detect(c.url, c.override, env)
		if got != c.want || (e == nil) != (c.code == "") || (e != nil && e.Code != c.code) {
			t.Fatalf("%+v: %v %v", c, got, e)
		}
	}
	if _, e := Detect("https://git.acme.com/x", "", func(string) string { return "" }); e == nil {
		t.Fatal("GH_HOST unset still detected")
	}
}

func TestTokenEnvOnlyForItsHost(t *testing.T) {
	noHelper := func(context.Context, string) (string, error) { return "", errors.New("no helper") }
	env := func(k string) string {
		return map[string]string{"GITHUB_TOKEN": "pub", "GH_ENTERPRISE_TOKEN": "ent", "GH_HOST": "git.acme.com"}[k]
	}
	for _, c := range []struct{ url, token, source string }{
		{"https://github.com/acme/invoices", "pub", "your GITHUB_TOKEN"},
		{"https://git.acme.com/acme/invoices", "ent", "your GH_ENTERPRISE_TOKEN"},
	} {
		tok, src, e := Token(t.Context(), KindGitHub, c.url, env, noHelper)
		if e != nil || tok != c.token || src != c.source {
			t.Fatalf("%s: %q %q %v", c.url, tok, src, e)
		}
	}
	// A host no variable names receives no environment token.
	if tok, _, e := Token(t.Context(), KindGitHub, "https://evil.example/acme/invoices", env, noHelper); tok != "" || e == nil {
		t.Fatalf("%q %v", tok, e)
	}
	// GH_TOKEN counts for github.com; without GH_HOST the enterprise token goes nowhere.
	ghOnly := func(k string) string { return map[string]string{"GH_TOKEN": "gh", "GH_ENTERPRISE_TOKEN": "ent"}[k] }
	if tok, src, _ := Token(t.Context(), KindGitHub, "https://github.com/a/b", ghOnly, noHelper); tok != "gh" || src != "your GH_TOKEN" {
		t.Fatal(tok, src)
	}
	if tok, _, _ := Token(t.Context(), "other", "https://github.com/a/b", ghOnly, noHelper); tok != "" {
		t.Fatal(tok)
	}
}

func TestTokenHelperKeyedByHost(t *testing.T) {
	var stdin string
	helper := func(_ context.Context, in string) (string, error) {
		stdin = in
		return "protocol=https\r\nhost=git.acme.com\r\nusername=ana\r\npassword=hp\r\n", nil
	}
	tok, src, e := Token(t.Context(), KindGitHub, "https://git.acme.com/acme/invoices", func(string) string { return "" }, helper)
	if e != nil || tok != "hp" || src != "your git credential helper" || stdin != "protocol=https\nhost=git.acme.com\npath=acme/invoices\n\n" {
		t.Fatalf("%q %q %v %q", tok, src, e, stdin)
	}
	// An answer with no password is no token.
	_, _, e = Token(t.Context(), KindGitHub, "https://github.com/a/b", func(string) string { return "" },
		func(context.Context, string) (string, error) { return "username=ana\n", nil })
	if e == nil {
		t.Fatal("a token from no password")
	}
}

func TestTokenMissingNamesVariables(t *testing.T) {
	_, _, e := Token(t.Context(), KindGitHub, "https://github.com/acme/invoices", func(string) string { return "" },
		func(context.Context, string) (string, error) { return "", errors.New("exit status 128") })
	want := "no Git token for github.com: set GITHUB_TOKEN or GH_TOKEN or GH_ENTERPRISE_TOKEN for that host, or store a token in a git credential helper"
	if e == nil || e.Code != CodeGitTokenMissing || e.Message != want {
		t.Fatal(e)
	}
}

func TestTokenHelperNeverStores(t *testing.T) {
	if strings.Join(credentialFillArgs, " ") != "-c credential.interactive=never credential fill" {
		t.Fatal(credentialFillArgs)
	}
	env := helperEnv([]string{"PATH=/bin", "GIT_ASKPASS=/x", "SSH_ASKPASS=/y", "GIT_TERMINAL_PROMPT=1", "GCM_INTERACTIVE=always"})
	if strings.Join(env, " ") != "PATH=/bin GIT_TERMINAL_PROMPT=0 GCM_INTERACTIVE=never" {
		t.Fatal(env)
	}
}

func TestRunGitCredentialFill(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + dir + "/argv\"\nexport -p > \"" + dir + "/env\"\n" +
		"while read -r l; do printf '%s\\n' \"$l\"; done > \"" + dir + "/stdin\"\necho password=x\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("GIT_ASKPASS", "/usr/bin/askpass")
	out, err := CredentialFill(t.Context(), "protocol=https\nhost=github.com\n\n")
	if err != nil || out != "password=x\n" {
		t.Fatalf("%v %q", err, out)
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	env, _ := os.ReadFile(filepath.Join(dir, "env"))
	stdin, _ := os.ReadFile(filepath.Join(dir, "stdin"))
	if string(argv) != "-c\ncredential.interactive=never\ncredential\nfill\n" || !strings.Contains(string(env), "GIT_TERMINAL_PROMPT") ||
		!strings.Contains(string(env), "GCM_INTERACTIVE") || strings.Contains(string(env), "export GIT_ASKPASS=") || !strings.Contains(string(stdin), "host=github.com") {
		t.Fatalf("%s\n%s", argv, stdin)
	}
	// No git on the PATH is no helper.
	t.Setenv("PATH", t.TempDir())
	if _, err := CredentialFill(t.Context(), ""); err == nil {
		t.Fatal("a helper without git")
	}
}

func TestGitExportUnitTestsUseNoSocket(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatal(err, files)
	}
	banned := []string{"httptest" + ".NewServer", "net" + ".Dial", "net" + ".Listen"}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range banned {
			if strings.Contains(string(raw), b) {
				t.Errorf("%s opens a socket: %s", f, b)
			}
		}
	}
}
