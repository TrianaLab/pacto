package gitexport

import (
	"encoding/base64"
	"encoding/json"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

const (
	glProject = "https://gitlab.com/api/v4/projects/acme%2Fbilling%2Finvoices"
	bbRepo    = "https://api.bitbucket.org/2.0/repositories/acme/invoices"
	gtRepo    = "https://codeberg.org/api/v1/repos/acme/invoices"
)

// gitLabFake is gitlab.com with the project acme/billing/invoices (a
// subgroup) whose main branch already holds deploy/pacto.yaml.
func gitLabFake(public bool) *hostFake {
	vis := "private"
	if public {
		vis = "public"
	}
	files := glProject + "/repository/files/deploy%2F"
	return &hostFake{answers: map[string]answer{
		"GET https://gitlab.com/api/v4/user":        {status: 200, body: `{"username":"ana"}`},
		"GET " + glProject:                          {status: 200, body: `{"default_branch":"main","visibility":"` + vis + `"}`},
		"HEAD " + files + "pacto.yaml?ref=main":     {status: 200},
		"POST " + glProject + "/repository/commits": {status: 201, body: `{"id":"c1"}`},
		"POST " + glProject + "/merge_requests":     {status: 201, body: `{"web_url":"https://gitlab.com/acme/billing/invoices/-/merge_requests/3"}`},
	}}
}

// bitbucketFake is Bitbucket Cloud with acme/invoices on main.
func bitbucketFake(private bool) *hostFake {
	priv := "true"
	if !private {
		priv = "false"
	}
	return &hostFake{answers: map[string]answer{
		"GET https://api.bitbucket.org/2.0/user": {status: 200, body: `{"username":"ana"}`},
		"GET " + bbRepo:                          {status: 200, body: `{"mainbranch":{"name":"main"},"is_private":` + priv + `}`},
		"GET " + bbRepo + "/refs/branches/main":  {status: 200, body: `{"target":{"hash":"c0"}}`},
		"POST " + bbRepo + "/src":                {status: 201},
		"POST " + bbRepo + "/pullrequests":       {status: 201, body: `{"links":{"html":{"href":"https://bitbucket.org/acme/invoices/pull-requests/5"}}}`},
	}}
}

// giteaFake is Codeberg with acme/invoices on main, which already holds
// deploy/pacto.yaml.
func giteaFake(private bool) *hostFake {
	vis := `"private":true`
	if !private {
		vis = `"private":false,"internal":false`
	}
	return &hostFake{answers: map[string]answer{
		"GET https://codeberg.org/api/v1/user":                   {status: 200, body: `{"login":"ana"}`},
		"GET " + gtRepo:                                          {status: 200, body: `{"default_branch":"main",` + vis + `}`},
		"GET " + gtRepo + "/contents/deploy/pacto.yaml?ref=main": {status: 200, body: `{"sha":"b0"}`},
		"POST " + gtRepo + "/contents":                           {status: 201, body: `{}`},
		"POST " + gtRepo + "/pulls":                              {status: 201, body: `{"html_url":"https://codeberg.org/acme/invoices/pulls/9"}`},
	}}
}

// hostCase is one host kind with its fake, repository URL, pull request
// URL, the request that creates the branch and the last request.
type hostCase struct {
	kind               Kind
	url, pr            string
	fake               func(private bool) *hostFake
	commitKey, pullKey string
}

var hostCases = []hostCase{
	{KindGitHub, "https://github.com/acme/invoices", "https://github.com/acme/invoices/pull/7",
		func(p bool) *hostFake { return gitHubFake("https://api.github.com", p) },
		"POST https://api.github.com/repos/acme/invoices/git/refs", "POST https://api.github.com/repos/acme/invoices/pulls"},
	{KindGitLab, "https://gitlab.com/acme/billing/invoices", "https://gitlab.com/acme/billing/invoices/-/merge_requests/3",
		func(p bool) *hostFake { return gitLabFake(!p) }, "POST " + glProject + "/repository/commits", "POST " + glProject + "/merge_requests"},
	{KindBitbucket, "https://bitbucket.org/acme/invoices", "https://bitbucket.org/acme/invoices/pull-requests/5",
		bitbucketFake, "POST " + bbRepo + "/src", "POST " + bbRepo + "/pullrequests"},
	{KindGitea, "https://codeberg.org/acme/invoices", "https://codeberg.org/acme/invoices/pulls/9",
		giteaFake, "POST " + gtRepo + "/contents", "POST " + gtRepo + "/pulls"},
}

func hostRequest(c hostCase, rt http.RoundTripper) Request {
	r := request(c.url, rt)
	r.Kind = c.kind
	return r
}

func TestGitLabOpenSequence(t *testing.T) {
	h := gitLabFake(false)
	got, e := Open(t.Context(), hostRequest(hostCases[1], h))
	if e != nil || got.URL != hostCases[1].pr || got.Login != "ana" {
		t.Fatalf("%v %+v", e, got)
	}
	files := glProject + "/repository/files/deploy%2F"
	want := []string{"GET https://gitlab.com/api/v4/user", "GET " + glProject, "HEAD " + files + "pacto.inferred.yaml?ref=main",
		"HEAD " + files + "pacto.yaml?ref=main", "POST " + glProject + "/repository/commits", "POST " + glProject + "/merge_requests"}
	wantCalls(t, h, want, "Bearer "+testToken)
	commit, _ := json.Marshal(h.calls[4].Body)
	if string(commit) != `{"actions":[{"action":"create","content":"declaration: {}\n","file_path":"deploy/pacto.inferred.yaml"},`+
		`{"action":"update","content":"pactoVersion: \"2.5\"\n","file_path":"deploy/pacto.yaml"}],`+
		`"branch":"pacto/declare-invoices-0.2.0","commit_message":"chore(pacto): declare invoices 0.2.0","start_branch":"main"}` {
		t.Fatal(string(commit))
	}
	if mr := h.calls[5].Body; mr["source_branch"] != "pacto/declare-invoices-0.2.0" || mr["target_branch"] != "main" ||
		!strings.Contains(mr["description"].(string), "`ghcr.io/acme/invoices@sha256:aa`") {
		t.Fatalf("%+v", mr)
	}
}

func TestGitLabFileProbeFails(t *testing.T) {
	h := gitLabFake(false)
	h.answers["HEAD "+glProject+"/repository/files/deploy%2Fpacto.inferred.yaml?ref=main"] = answer{status: 500}
	if _, e := Open(t.Context(), hostRequest(hostCases[1], h)); e == nil || !strings.Contains(e.Message, "500") {
		t.Fatal(e)
	}
}

func TestBitbucketOpenSequence(t *testing.T) {
	h := bitbucketFake(true)
	got, e := Open(t.Context(), hostRequest(hostCases[2], h))
	if e != nil || got.URL != hostCases[2].pr || got.Login != "ana" {
		t.Fatalf("%v %+v", e, got)
	}
	want := []string{"GET https://api.bitbucket.org/2.0/user", "GET " + bbRepo, "GET " + bbRepo + "/refs/branches/main",
		"POST " + bbRepo + "/src", "POST " + bbRepo + "/pullrequests"}
	wantCalls(t, h, want, "Bearer "+testToken)
	fields := multipartFields(t, h.calls[3])
	if len(fields) != 5 || fields["message"] != "chore(pacto): declare invoices 0.2.0" || fields["branch"] != "pacto/declare-invoices-0.2.0" ||
		fields["parents"] != "c0" || fields["deploy/pacto.yaml"] != "pactoVersion: \"2.5\"\n" || fields["deploy/pacto.inferred.yaml"] != "declaration: {}\n" {
		t.Fatalf("%+v", fields)
	}
	pr, _ := json.Marshal(h.calls[4].Body)
	if !strings.Contains(string(pr), `"destination":{"branch":{"name":"main"}},"source":{"branch":{"name":"pacto/declare-invoices-0.2.0"}}`) {
		t.Fatal(string(pr))
	}
}

func multipartFields(t *testing.T, c call) map[string]string {
	t.Helper()
	_, params, err := mime.ParseMediaType(c.ContentType)
	if err != nil {
		t.Fatal(err)
	}
	form, err := multipart.NewReader(strings.NewReader(c.Raw), params["boundary"]).ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for k, v := range form.Value {
		out[k] = v[0]
	}
	return out
}

func TestBitbucketBasicFromHelper(t *testing.T) {
	h := bitbucketFake(true)
	r := hostRequest(hostCases[2], h)
	r.Token = "ana:app-pass"
	if _, e := Open(t.Context(), r); e != nil {
		t.Fatal(e)
	}
	if h.calls[0].Auth != "Basic "+base64.StdEncoding.EncodeToString([]byte("ana:app-pass")) {
		t.Fatal(h.calls[0].Auth)
	}
}

func TestBitbucketAccessTokenHasNoUser(t *testing.T) {
	h := bitbucketFake(true)
	h.answers["GET https://api.bitbucket.org/2.0/user"] = answer{status: 403, body: `{"type":"error"}`}
	got, e := Open(t.Context(), hostRequest(hostCases[2], h))
	if e != nil || got.Login != "a Bitbucket access token" {
		t.Fatalf("%v %+v", e, got)
	}
}

func TestBitbucketBranchHeadFails(t *testing.T) {
	h := bitbucketFake(true)
	h.answers["GET "+bbRepo+"/refs/branches/main"] = answer{status: 500, body: "boom"}
	if _, e := Open(t.Context(), hostRequest(hostCases[2], h)); e == nil || !strings.Contains(e.Message, "500: boom") {
		t.Fatal(e)
	}
}

func TestGiteaOpenSequence(t *testing.T) {
	h := giteaFake(true)
	got, e := Open(t.Context(), hostRequest(hostCases[3], h))
	if e != nil || got.URL != hostCases[3].pr || got.Login != "ana" {
		t.Fatalf("%v %+v", e, got)
	}
	want := []string{"GET https://codeberg.org/api/v1/user", "GET " + gtRepo, "GET " + gtRepo + "/contents/deploy/pacto.inferred.yaml?ref=main",
		"GET " + gtRepo + "/contents/deploy/pacto.yaml?ref=main", "POST " + gtRepo + "/contents", "POST " + gtRepo + "/pulls"}
	wantCalls(t, h, want, "Bearer "+testToken)
	b64 := base64.StdEncoding.EncodeToString
	body, _ := json.Marshal(h.calls[4].Body)
	if string(body) != `{"branch":"main","files":[{"content":"`+b64([]byte("declaration: {}\n"))+`","operation":"create","path":"deploy/pacto.inferred.yaml"},`+
		`{"content":"`+b64([]byte("pactoVersion: \"2.5\"\n"))+`","operation":"update","path":"deploy/pacto.yaml","sha":"b0"}],`+
		`"message":"chore(pacto): declare invoices 0.2.0","new_branch":"pacto/declare-invoices-0.2.0"}` {
		t.Fatal(string(body))
	}
	if pr := h.calls[5].Body; pr["head"] != "pacto/declare-invoices-0.2.0" || pr["base"] != "main" {
		t.Fatalf("%+v", pr)
	}
}

func TestGiteaContentsProbeFails(t *testing.T) {
	h := giteaFake(true)
	h.answers["GET "+gtRepo+"/contents/deploy/pacto.inferred.yaml?ref=main"] = answer{status: 500, body: "boom"}
	if _, e := Open(t.Context(), hostRequest(hostCases[3], h)); e == nil || !strings.Contains(e.Message, "500: boom") {
		t.Fatal(e)
	}
}

func TestGiteaEscapesSegments(t *testing.T) {
	if got := escapeSegments("my deploy/pacto.yaml"); got != "my%20deploy/pacto.yaml" {
		t.Fatal(got)
	}
}

func TestGiteaInternalIsNotPublic(t *testing.T) {
	h := giteaFake(true)
	h.answers["GET "+gtRepo] = answer{status: 200, body: `{"default_branch":"main","private":false,"internal":true}`}
	if _, e := Open(t.Context(), hostRequest(hostCases[3], h)); e != nil {
		t.Fatal(e)
	}
}

func wantCalls(t *testing.T, h *hostFake, want []string, auth string) {
	t.Helper()
	if len(h.calls) != len(want) {
		t.Fatalf("%+v", h.calls)
	}
	for i, c := range h.calls {
		if c.Method+" "+c.URL != want[i] || c.Auth != auth {
			t.Fatalf("%d: %+v", i, c)
		}
	}
}

func TestOpenUnknownKind(t *testing.T) {
	r := request("https://github.com/acme/invoices", nil)
	r.Kind = "svn"
	if _, e := Open(t.Context(), r); e == nil || e.Code != CodeGitHostUnknown {
		t.Fatal(e)
	}
}

// TestBranchExistsEveryHost: a branch a previous run pushed before its
// pull request failed is reused, so a rerun is the retry; when no pull
// request opens from it either, the refusal names the branch to delete.
func TestBranchExistsEveryHost(t *testing.T) {
	for _, c := range hostCases {
		for _, a := range []answer{{status: 409, body: `{"message":"exists"}`}, {status: 422, body: `{"message":"exists"}`}} {
			h := c.fake(true)
			h.answers[c.commitKey] = a
			if got, e := Open(t.Context(), hostRequest(c, h)); e != nil || got.URL != c.pr || got.Branch != "pacto/declare-invoices-0.2.0" {
				t.Fatalf("%s %d: %v %+v", c.kind, a.status, e, got)
			}
			h.answers[c.pullKey] = answer{status: 422, body: `{"message":"A pull request already exists"}`}
			_, e := Open(t.Context(), hostRequest(c, h))
			if e == nil || !strings.HasPrefix(e.Message, "branch pacto/declare-invoices-0.2.0 exists and no pull request was opened from it (") ||
				!strings.HasSuffix(e.Message, "): one may already be open; else delete the branch and rerun") {
				t.Fatalf("%s %d: %v", c.kind, a.status, e)
			}
		}
	}
	// GitLab says so with a 400.
	h := gitLabFake(false)
	h.answers[hostCases[1].commitKey] = answer{status: 400, body: `{"message":"A branch called 'pacto/declare-invoices-0.2.0' already exists."}`}
	if got, e := Open(t.Context(), hostRequest(hostCases[1], h)); e != nil || got.URL != hostCases[1].pr {
		t.Fatal(e)
	}
	h = gitLabFake(false)
	h.answers[hostCases[1].commitKey] = answer{status: 400, body: `{"message":"invalid action"}`}
	if _, e := Open(t.Context(), hostRequest(hostCases[1], h)); e == nil || !strings.Contains(e.Message, "400: invalid action") {
		t.Fatal(e)
	}
}

func TestEveryHostEveryStepFails(t *testing.T) {
	for _, c := range hostCases[1:] {
		for key := range c.fake(true).answers {
			if strings.HasPrefix(key, "HEAD ") {
				continue // a missing file is a create, covered by the probe tests
			}
			h := c.fake(true)
			h.answers[key] = answer{status: 500, body: "boom"}
			if _, e := Open(t.Context(), hostRequest(c, h)); e == nil || !strings.Contains(e.Message, "500: boom") {
				t.Fatalf("%s %s: %v", c.kind, key, e)
			}
		}
	}
}

func TestPublicRepositoryNeedsPersonConfirmationEveryHost(t *testing.T) {
	for _, c := range hostCases {
		h := c.fake(false)
		_, e := Open(t.Context(), hostRequest(c, h))
		if e == nil || e.Message != "the repository is public: confirm with --repository "+c.url || len(h.calls) != 2 {
			t.Fatalf("%s: %v %d", c.kind, e, len(h.calls))
		}
		r := hostRequest(c, c.fake(false))
		r.Confirmed = true
		if got, e := Open(t.Context(), r); e != nil || got.URL != c.pr {
			t.Fatalf("%s: %v %+v", c.kind, e, got)
		}
	}
}

func TestRedirectNeverCarriesTokenEveryHost(t *testing.T) {
	for _, c := range hostCases {
		h := c.fake(true)
		for key := range h.answers {
			if strings.HasSuffix(key, "/user") {
				h.answers[key] = answer{status: http.StatusTemporaryRedirect, header: http.Header{"Location": {"https://evil.example/user"}}}
			}
		}
		_, e := Open(t.Context(), hostRequest(c, h))
		if e == nil || e.Message != "the host redirected; pass the final URL with --repository" || len(h.calls) != 1 {
			t.Fatalf("%s: %v %+v", c.kind, e, h.calls)
		}
	}
}

func TestPRBodyEscapesUntrustedValuesEveryHost(t *testing.T) {
	for _, c := range hostCases {
		h := c.fake(true)
		r := hostRequest(c, h)
		r.Declaration.OnBehalfOf = "[click](https://evil) @org/security-team"
		r.Declaration.State = "draft"
		if _, e := Open(t.Context(), r); e != nil {
			t.Fatal(c.kind, e)
		}
		raw := h.calls[len(h.calls)-1].Raw
		if !strings.Contains(raw, "`[click](https://evil) @org/security-team`") {
			t.Fatalf("%s: %s", c.kind, raw)
		}
	}
}

func TestTokenNeverPrintedEveryHost(t *testing.T) {
	for _, c := range hostCases {
		h := c.fake(true)
		h.answers[c.pullKey] = answer{status: 422, body: `{"message":"token ` + testToken + ` lacks scope"}`}
		_, e := Open(t.Context(), hostRequest(c, h))
		if e == nil || strings.Contains(e.Error(), testToken) || !strings.Contains(e.Error(), "<token>") {
			t.Fatalf("%s: %v", c.kind, e)
		}
	}
	// The password half of a Bitbucket username:password is redacted too.
	h := bitbucketFake(true)
	h.answers[hostCases[2].pullKey] = answer{status: 400, body: `{"message":"bad app-pass"}`}
	r := hostRequest(hostCases[2], h)
	r.Token = "ana:app-pass"
	if _, e := Open(t.Context(), r); e == nil || strings.Contains(e.Error(), "app-pass") {
		t.Fatal(e)
	}
}

// A GitLab project with no visibility, or one Pacto does not know, is
// never read as private: it needs the person's confirmation.
func TestGitLabUnknownVisibilityNeedsConfirmation(t *testing.T) {
	for _, body := range []string{`{"default_branch":"main"}`, `{"default_branch":"main","visibility":"unlisted"}`} {
		h := gitLabFake(false)
		h.answers["GET "+glProject] = answer{status: 200, body: body}
		if _, e := Open(t.Context(), hostRequest(hostCases[1], h)); e == nil || !strings.HasPrefix(e.Message, "the repository is public: confirm") {
			t.Fatalf("%s: %v", body, e)
		}
	}
	h := gitLabFake(false)
	h.answers["GET "+glProject] = answer{status: 200, body: `{"default_branch":"main","visibility":"internal"}`}
	if _, e := Open(t.Context(), hostRequest(hostCases[1], h)); e != nil {
		t.Fatal(e)
	}
}
