package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/random"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// hostCall is one request a recordingHost answered.
type hostCall struct {
	key, contentType string
	raw              []byte
}

// recordingHost is a Git host API that answers "METHOD URL" from canned
// JSON (anything else is 404, which every host reads as "absent") and
// records each request. It never opens a socket.
type recordingHost struct {
	answers map[string]string
	calls   []hostCall
}

func (h *recordingHost) RoundTrip(r *http.Request) (*http.Response, error) {
	c := hostCall{key: r.Method + " " + r.URL.String(), contentType: r.Header.Get("Content-Type")}
	if r.Body != nil {
		c.raw, _ = io.ReadAll(r.Body)
	}
	h.calls = append(h.calls, c)
	status, body := http.StatusOK, h.answers[c.key]
	if _, ok := h.answers[c.key]; !ok {
		status, body = http.StatusNotFound, `{"message":"Not Found"}`
	}
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

// one is the only call to key, or fails the test.
func (h *recordingHost) one(t *testing.T, key string) hostCall {
	t.Helper()
	var got []hostCall
	for _, c := range h.calls {
		if c.key == key {
			got = append(got, c)
		}
	}
	if len(got) != 1 {
		t.Fatalf("%d calls to %s: %v", len(got), key, h.calls)
	}
	return got[0]
}

// e2eHost is one Git host kind: its repository, the token variables the
// person sets, its canned API, the request that commits and the one that
// opens the pull request, and how to read each.
type e2eHost struct {
	name, repo, pr string
	env            map[string]string
	answers        map[string]string
	commit, pull   string
	files          func(t *testing.T, c hostCall) map[string]string
	body           string // the pull request's description key
}

// jsonBody decodes c's JSON body.
func jsonBody(t *testing.T, c hostCall) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(c.raw, &m); err != nil {
		t.Fatalf("%v: %s", err, c.raw)
	}
	return m
}

// entries reads list's objects into path -> content, decoding base64 when b64.
func entries(t *testing.T, list any, pathKey string, b64 bool) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, e := range list.([]any) {
		m := e.(map[string]any)
		content := m["content"].(string)
		if b64 {
			raw, err := base64.StdEncoding.DecodeString(content)
			if err != nil {
				t.Fatal(err)
			}
			content = string(raw)
		}
		out[m[pathKey].(string)] = content
	}
	return out
}

func e2eHosts() []e2eHost {
	const gh, gl, bb, gt = "https://api.github.com/repos/acme/invoices", "https://gitlab.com/api/v4/projects/acme%2Finvoices",
		"https://api.bitbucket.org/2.0/repositories/acme/invoices", "https://codeberg.org/api/v1/repos/acme/invoices"
	return []e2eHost{
		{name: "github", repo: "https://github.com/acme/invoices", pr: "https://github.com/acme/invoices/pull/7", env: map[string]string{"GITHUB_TOKEN": "ghp_secret"},
			answers: map[string]string{"GET https://api.github.com/user": `{"login":"ana"}`, "GET " + gh: `{"default_branch":"main","private":true,"visibility":"private"}`,
				"GET " + gh + "/git/ref/heads/main": `{"object":{"sha":"c0"}}`, "GET " + gh + "/git/commits/c0": `{"tree":{"sha":"t0"}}`,
				"POST " + gh + "/git/trees": `{"sha":"t1"}`, "POST " + gh + "/git/commits": `{"sha":"c1"}`, "POST " + gh + "/git/refs": `{}`,
				"POST " + gh + "/pulls": `{"html_url":"https://github.com/acme/invoices/pull/7"}`},
			commit: "POST " + gh + "/git/trees", pull: "POST " + gh + "/pulls", body: "body",
			files: func(t *testing.T, c hostCall) map[string]string {
				return entries(t, jsonBody(t, c)["tree"], "path", false)
			}},
		{name: "gitlab", repo: "https://gitlab.com/acme/invoices", pr: "https://gitlab.com/acme/invoices/-/merge_requests/3",
			env: map[string]string{"GITLAB_TOKEN": "glpat-secret"},
			answers: map[string]string{"GET https://gitlab.com/api/v4/user": `{"username":"ana"}`, "GET " + gl: `{"default_branch":"main","visibility":"private"}`,
				"POST " + gl + "/repository/commits": `{"id":"c1"}`, "POST " + gl + "/merge_requests": `{"web_url":"https://gitlab.com/acme/invoices/-/merge_requests/3"}`},
			commit: "POST " + gl + "/repository/commits", pull: "POST " + gl + "/merge_requests", body: "description",
			files: func(t *testing.T, c hostCall) map[string]string {
				return entries(t, jsonBody(t, c)["actions"], "file_path", false)
			}},
		{name: "bitbucket", repo: "https://bitbucket.org/acme/invoices", pr: "https://bitbucket.org/acme/invoices/pull-requests/5",
			env: map[string]string{"BITBUCKET_TOKEN": "bb-secret"},
			answers: map[string]string{"GET https://api.bitbucket.org/2.0/user": `{"username":"ana"}`, "GET " + bb: `{"mainbranch":{"name":"main"},"is_private":true}`,
				"GET " + bb + "/refs/branches/main": `{"target":{"hash":"c0"}}`, "POST " + bb + "/src": `{}`,
				"POST " + bb + "/pullrequests": `{"links":{"html":{"href":"https://bitbucket.org/acme/invoices/pull-requests/5"}}}`},
			commit: "POST " + bb + "/src", pull: "POST " + bb + "/pullrequests", body: "description", files: multipartFiles},
		{name: "gitea", repo: "https://codeberg.org/acme/invoices", pr: "https://codeberg.org/acme/invoices/pulls/9",
			env: map[string]string{"GITEA_TOKEN": "gt-secret", "GITEA_HOST": "codeberg.org"},
			answers: map[string]string{"GET https://codeberg.org/api/v1/user": `{"login":"ana"}`, "GET " + gt: `{"default_branch":"main","private":true}`,
				"POST " + gt + "/contents": `{}`, "POST " + gt + "/pulls": `{"html_url":"https://codeberg.org/acme/invoices/pulls/9"}`},
			commit: "POST " + gt + "/contents", pull: "POST " + gt + "/pulls", body: "body",
			files: func(t *testing.T, c hostCall) map[string]string {
				return entries(t, jsonBody(t, c)["files"], "path", true)
			}},
	}
}

// multipartFiles is a Bitbucket /src commit's file fields (the form fields
// holding a path).
func multipartFiles(t *testing.T, c hostCall) map[string]string {
	t.Helper()
	_, params, err := mime.ParseMediaType(c.contentType)
	if err != nil {
		t.Fatal(err)
	}
	form, err := multipart.NewReader(strings.NewReader(string(c.raw)), params["boundary"]).ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for k, v := range form.Value {
		if strings.Contains(k, "/") {
			out[k] = v[0]
		}
	}
	return out
}

// signedRunningImage pushes the running image into the e2e registry, signs
// it with repo's CI identity from the private Sigstore and returns its
// digest-pinned reference.
func (e *declareE2E) signedRunningImage(t *testing.T, repo string) string {
	t.Helper()
	reg := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), e.host)
	img, _ := random.Image(16, 1)
	at := e.host + "/acme/invoices-app"
	d, err := reg.Write(t.Context(), at, img)
	if err != nil {
		t.Fatal(err)
	}
	tok := testutil.CIToken(repo+"/.github/workflows/ci.yml@refs/heads/main", "https://ci.example.org", repo, "refs/heads/main")
	s, err := signing.Keyless(e.ss.Fulcio, e.ss.TSA, "", func(context.Context) (string, error) { return tok, nil }, e.ss.Root)
	if err != nil {
		t.Fatal(err)
	}
	if err := publish.SignDigest(t.Context(), reg, s, at, d); err != nil {
		t.Fatal(err)
	}
	return at + "@" + d
}

// TestDeclarePRAllHostsEndToEnd: for each Git host, the running image is
// signed by the service's CI and an Argo CD Application in argocd deploys
// it from deploy/. `pacto declare invoices --pr --yes` publishes ana's
// declaration, then commits the bundle's two files into deploy/ in one
// commit and opens a pull request that names the signed-provenance rung.
func TestDeclarePRAllHostsEndToEnd(t *testing.T) {
	for _, h := range e2eHosts() {
		t.Run(h.name, func(t *testing.T) {
			e := newDeclareE2E(t)
			prEnv(t, "")
			for k, v := range h.env {
				t.Setenv(k, v)
			}
			argoKubeAt(t, e.signedRunningImage(t, h.repo), h.repo+".git")
			host := &recordingHost{answers: h.answers}
			e.git = host
			out := e.declare(t, "--namespace", "billing", "--accept", "ledger", "--pr")
			if !strings.Contains(out, "published "+e.host+"/acme/invoices@sha256:") || !strings.Contains(out, "opened "+h.pr+" as ana") {
				t.Fatal(out)
			}
			for k, v := range h.env {
				if strings.HasSuffix(k, "_TOKEN") && strings.Contains(out, v) {
					t.Fatalf("the token was printed\n%s", out)
				}
			}
			wantCommitted(t, h.files(t, host.one(t, h.commit)), e.pulledFiles(t, "0.1.0"))
			if body, _ := jsonBody(t, host.one(t, h.pull))[h.body].(string); !strings.Contains(body, "- Repository found by: `signed-provenance`") {
				t.Fatal(body)
			}
			_, rec := e.pull(t, "0.1.0")
			r := rec.Declaration.Repository
			if r == nil || r.URL != h.repo || r.Rung != contract.RepositoryRungSigned || r.Folder != "deploy" || r.FolderRung != contract.RepositoryRungGitOps ||
				r.Signer != h.repo+"/.github/workflows/ci.yml@refs/heads/main" {
				t.Fatalf("%s", rec.Marshal())
			}
		})
	}
}
